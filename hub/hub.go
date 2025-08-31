package hub

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/util"
)

// used for randomizing the connection initiation delay
// this limits the possibility of concurrent connection attempts from both sides
type connectionInitiationDelayTimeRange struct {
	// defines the minimum and maximum wait time for when to try to initate an connection
	min, max int
}

// defines the delay timeframes in seconds depening on the connection attempt counter
// the last item will be re-used for higher attempt counter values
var connectionInitiationDelayTimeRanges = []connectionInitiationDelayTimeRange{
	{min: 0, max: 3},
	{min: 3, max: 10},
	{min: 10, max: 20},
}

// announcementState tracks the state of an active announcement to a target device
type announcementState struct {
	target     *api.PairingTarget
	announcer  api.PairingAnnouncerInterface
	startTime  time.Time
	cancelFunc context.CancelFunc
}

// handling the server and all connections to remote services
type Hub struct {
	connections map[string]api.ShipConnectionInterface

	// which attempt is it to initate an connection to the remote SKI
	connectionAttemptCounter map[string]int
	connectionAttemptRunning map[string]bool

	port        int
	certificate tls.Certificate

	localService *api.ServiceDetails

	hubReader api.HubReaderInterface

	// if this service shall auto accept pairing requests
	autoaccept bool

	// The list of known remote services
	remoteServices []*api.ServiceDetails

	// The web server for handling incoming websocket connections
	httpServer *http.Server

	// Handling mDNS related tasks
	mdns api.MdnsInterface

	// list of currently known/reported mDNS entries
	knownMdnsEntries []*api.MdnsEntry

	hasStarted bool

	// For tracking server startup errors
	serverStartErr error
	serverStarted  chan struct{}

	// connection delay timers that can be cancelled
	connectionDelayTimers map[string]*connectionDelayTimer
	muxTimers             sync.RWMutex

	// Maximum number of simultaneous connections allowed
	// Default is 10 if not configured
	maxConnections int

	muxCon        sync.RWMutex
	muxConAttempt sync.RWMutex
	muxReg        sync.RWMutex
	muxMdns       sync.Mutex
	muxStarted    sync.RWMutex

	// SHIP Pairing Service integration
	pairingService        api.ShipPairingServiceInterface
	pairingConfig         *api.PairingConfig
	ringBufferPersistence api.RingBufferPersistence
	muxPairing            sync.RWMutex

	// Pairing lifecycle management
	pairingCtx    context.Context
	pairingCancel context.CancelFunc

	// QR-based announcement tracking
	activeAnnouncements map[string]*announcementState
	muxAnnouncements    sync.RWMutex

	// AddCu replacement detection tracker
	addCuReplacementTracker *AddCuReplacementTracker
}

func NewHub(hubReader api.HubReaderInterface,
	mdns api.MdnsInterface,
	port int,
	certificate tls.Certificate,
	localService *api.ServiceDetails,
	pairingConfig *api.PairingConfig, // nil = no pairing
	ringBufferPersistence api.RingBufferPersistence,
) (*Hub, error) {
	// Validate ring buffer persistence requirement based on pairing mode
	if pairingConfig != nil {
		requiresPersistence := pairingConfig.Mode == api.PairingModeListener ||
			pairingConfig.Mode == api.PairingModeBoth
		if requiresPersistence && ringBufferPersistence == nil {
			return nil, fmt.Errorf("RingBufferPersistence required for listener/both pairing modes")
		}
	}

	// Create autonomous context for lifecycle management
	pairingCtx, pairingCancel := context.WithCancel(context.Background())

	hub := &Hub{
		connections:              make(map[string]api.ShipConnectionInterface),
		connectionAttemptCounter: make(map[string]int),
		connectionAttemptRunning: make(map[string]bool),
		remoteServices:           make([]*api.ServiceDetails, 0),
		knownMdnsEntries:         make([]*api.MdnsEntry, 0),
		connectionDelayTimers:    make(map[string]*connectionDelayTimer),
		hubReader:                hubReader,
		port:                     port,
		certificate:              certificate,
		localService:             localService,
		mdns:                     mdns,
		maxConnections:           10, // Default connection limit
		serverStarted:            make(chan struct{}),
		ringBufferPersistence:    ringBufferPersistence,
		pairingCtx:               pairingCtx,
		pairingCancel:            pairingCancel,
		activeAnnouncements:      make(map[string]*announcementState),
		addCuReplacementTracker:  NewAddCuReplacementTracker(),
	}

	// Validate and create pairing service if configuration provided
	if pairingConfig != nil {
		if err := pairingConfig.Validate(); err != nil {
			return nil, fmt.Errorf("invalid pairing configuration: %w", err)
		}

		hub.pairingConfig = pairingConfig
	}

	return hub, nil
}

var _ api.HubInterface = (*Hub)(nil)

// Start the ConnectionsHub with all its services
func (h *Hub) Start() error {
	h.muxStarted.Lock()
	defer h.muxStarted.Unlock()

	// start the websocket server
	if err := h.startWebsocketServer(); err != nil {
		return fmt.Errorf("failed to start hub: %w", err)
	}

	// Wait briefly to catch immediate startup errors
	select {
	case <-h.serverStarted:
		if h.serverStartErr != nil {
			return fmt.Errorf("websocket server failed to start: %w", h.serverStartErr)
		}
	case <-time.After(100 * time.Millisecond):
		// Server is likely starting successfully
	}

	pairingMode := api.PairingModeOff
	if h.pairingConfig != nil {
		pairingMode = h.pairingConfig.Mode
	}

	// start mDNS
	if err := h.mdns.Start(pairingMode, h); err != nil {
		// Shutdown the server if mDNS fails
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := h.httpServer.Shutdown(ctx); shutdownErr != nil {
			logging.Log().Error("failed to shutdown HTTP server after mDNS error:", shutdownErr)
		}
		return fmt.Errorf("failed to start mDNS: %w", err)
	}

	if pairingMode != api.PairingModeOff {
		// Initialize service if not already done (in case of programmatic setup)
		if err := h.initializePairingServiceWithConfig(h.pairingConfig); err != nil {
			return fmt.Errorf("failed to initialize pairing: %w", err)
		}

		h.startPairingService()
	}

	h.hasStarted = true
	return nil
}

func (h *Hub) startPairingService() {
	// Start optional pairing service after mDNS (service was already initialized in NewHub)
	h.muxPairing.RLock()
	pairingService := h.pairingService
	pairingConfig := h.pairingConfig
	h.muxPairing.RUnlock()

	if pairingService == nil || pairingConfig == nil {
		return
	}

	if err := pairingService.Start(); err != nil {
		logging.Log().Error("pairing service failed to start:", err)
		// Continue without pairing service rather than failing Hub startup
		return
	}

	logging.Log().Debug("pairing service started successfully")

	// Start SHIP pairing behavior based on configuration
	switch pairingConfig.Mode {
	case api.PairingModeListener, api.PairingModeBoth:
		if err := h.enablePairingListener(pairingConfig); err != nil {
			logging.Log().Error("ship pairing listener failed to start:", err)
			// Continue Hub startup - pairing is optional
		}
	case api.PairingModeAnnouncer:
		// Announcer mode is enabled per-target in StartAnnouncementTo()
		// No global configuration needed since each target has its own secret
	}
}

// close all connections
func (h *Hub) Shutdown() {
	// Cancel active announcements first
	h.muxAnnouncements.Lock()
	for shipID, state := range h.activeAnnouncements {
		logging.Log().Debug("stopping announcement to", shipID, "during shutdown")
		if state.cancelFunc != nil {
			state.cancelFunc()
		}
		if state.announcer != nil {
			_ = state.announcer.StopAnnouncement()
		}
	}
	// Clear the map
	h.activeAnnouncements = make(map[string]*announcementState)
	h.muxAnnouncements.Unlock()

	// Cancel pairing operations
	if h.pairingCancel != nil {
		h.pairingCancel()
	}

	// First, stop accepting new connections by shutting down the HTTP server
	if h.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.httpServer.Shutdown(ctx); err != nil {
			logging.Log().Error("HTTP server shutdown error:", err)
		}
	}

	// Shutdown optional pairing service before mDNS
	h.muxPairing.RLock()
	pairingService := h.pairingService
	h.muxPairing.RUnlock()

	if pairingService != nil {
		logging.Log().Debug("shutting down pairing service")
		pairingService.Shutdown()
	}

	// Then shutdown mDNS
	h.mdns.Shutdown()

	// Cancel all pending connection delay timers
	h.muxTimers.Lock()
	for ski, timer := range h.connectionDelayTimers {
		timer.Stop()
		delete(h.connectionDelayTimers, ski)
	}
	h.muxTimers.Unlock()

	// Close all connections with timeout
	var wg sync.WaitGroup
	h.muxCon.RLock()
	connections := make(map[string]api.ShipConnectionInterface)
	for ski, c := range h.connections {
		connections[ski] = c
	}
	h.muxCon.RUnlock()

	for ski, conn := range connections {
		wg.Add(1)
		go func(ski string, conn api.ShipConnectionInterface) {
			defer wg.Done()
			// Give connections 2 seconds to close gracefully
			done := make(chan struct{})
			go func() {
				conn.CloseConnection(false, 0, "hub shutdown")
				close(done)
			}()

			select {
			case <-done:
				logging.Log().Debug("connection closed:", ski)
			case <-time.After(2 * time.Second):
				logging.Log().Error("connection failed to close in time:", ski)
			}
		}(ski, conn)
	}

	// Wait up to 3 seconds for all connections to close
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logging.Log().Debug("all connections closed successfully")
	case <-time.After(3 * time.Second):
		logging.Log().Error("timeout waiting for connections to close")
	}
}

// return the service for a SKI, fingerprint, or SHIP ID
func (h *Hub) ServiceForIdentifier(ski, fingerprint string) *api.ServiceDetails {
	ski = util.NormalizeSKI(ski)

	h.muxReg.Lock()
	defer h.muxReg.Unlock()

	for _, service := range h.remoteServices {
		// Check if any provided identifier contradicts existing ones
		skiConflict := (ski != "" && service.SKI() != "" && service.SKI() != ski)
		fpConflict := (fingerprint != "" && service.Fingerprint() != "" && service.Fingerprint() != fingerprint)

		if skiConflict || fpConflict {
			continue // This represents a different device
		}

		// At least one identifier must match
		skiMatch := (ski != "" && service.SKI() == ski)
		fpMatch := (fingerprint != "" && service.Fingerprint() == fingerprint)

		if skiMatch || fpMatch {
			return service
		}
	}

	return nil
}

// add a new remote service
func (h *Hub) AddService(service *api.ServiceDetails) bool {
	if service == nil {
		return false
	}

	h.muxReg.Lock()
	defer h.muxReg.Unlock()

	// Check if the service is already registered
	for _, s := range h.remoteServices {
		if (service.SKI() != "" && s.SKI() == service.SKI()) ||
			(service.Fingerprint() != "" && s.Fingerprint() == service.Fingerprint()) {
			return false
		}
	}

	h.remoteServices = append(h.remoteServices, service)
	return true
}

// remove a service from remote services
func (h *Hub) RemoveService(ski, fingerprint string) {
	h.muxReg.Lock()
	defer h.muxReg.Unlock()

	for i, service := range h.remoteServices {
		if ski != "" && service.SKI() != ski {
			continue
		}
		if fingerprint != "" && service.Fingerprint() != fingerprint {
			continue
		}

		h.remoteServices = append(h.remoteServices[:i], h.remoteServices[i+1:]...)
		return
	}
}

// return the service for a trusted SHIP ID
func (h *Hub) serviceForTrustedShipID(shipID string) *api.ServiceDetails {
	h.muxReg.RLock()
	defer h.muxReg.RUnlock()

	for _, service := range h.remoteServices {
		if service.Trusted() && service.ShipID() == shipID {
			return service
		}
	}

	return nil
}

// HasTrustedAddCuDevice returns the ShipID of any trusted AddCu device, or empty string if none
func (h *Hub) HasTrustedAddCuDevice() (string, string) {
	h.muxReg.RLock()
	defer h.muxReg.RUnlock()

	for _, service := range h.remoteServices {
		if service.Trusted() &&
			service.PairingType() == api.PairingTypeAddCu &&
			service.ShipID() != "" &&
			service.Fingerprint() != "" {
			return service.Fingerprint(), service.ShipID()
		}
	}

	return "", ""
}

// return the number of paired services
func (h *Hub) numberPairedServices() int {
	amount := 0

	h.muxReg.RLock()
	for _, service := range h.remoteServices {
		if service.Trusted() {
			amount++
		}
	}
	h.muxReg.RUnlock()

	return amount
}

// SetMaxConnections sets the maximum number of simultaneous connections allowed
// A value of 0 or less will use the default of 10
func (h *Hub) SetMaxConnections(max int) {
	h.muxCon.Lock()
	defer h.muxCon.Unlock()

	if max <= 0 {
		max = 10
	}
	h.maxConnections = max
}

// startup mDNS if a paired service is not connected
func (h *Hub) checkAutoReannounce() {
	countPairedServices := h.numberPairedServices()
	h.muxCon.RLock()
	countConnections := len(h.connections)
	h.muxCon.RUnlock()

	if countPairedServices > countConnections {
		_ = h.mdns.AnnounceMdnsEntry()

		// also check currently known mDNS entries to see if they
		// already contain the not connected remote service
		h.mdns.RequestMdnsEntries()
	}
}

// StopAddCuReplacementTimer stops the Device Replacement Timing Logic timer for an AddCu service.
//
// This method cancels the 15-minute replacement timer that was started when an AddCu device
// disconnected. It should be called when:
// - The AddCu device reconnects within the 15-minute window
// - The service is being explicitly untrusted or removed
// - The hub is shutting down
//
// The timer cancellation prevents:
// - Automatic trust removal due to timeout
// - Unnecessary pairing listener reactivation
// - False positive device replacement detection
//
// Parameters:
// - service: The ServiceDetails of the AddCu device whose timer should be stopped
//
// Behavior:
// - Only processes services with PairingType == PairingTypeAddCu
// - Requires a valid ShipID for timer tracking
// - Idempotent: safe to call multiple times for the same service
// - No-op if service is nil or not an AddCu device
//
// Example:
//
//	// Stop timer when AddCu device reconnects
//	func (h *Hub) onDeviceReconnected(service *api.ServiceDetails) {
//	    h.StopAddCuReplacementTimer(service)
//	    log.Printf("Cancelled replacement timer for %s", service.ShipID())
//	}
//
// Thread-safety: This method is thread-safe and can be called concurrently.
func (h *Hub) StopAddCuReplacementTimer(service *api.ServiceDetails) {
	if service == nil {
		return
	}

	// Only handle AddCu services
	if service.PairingType() != api.PairingTypeAddCu {
		return
	}

	shipID := service.ShipID()
	if shipID == "" {
		return
	}

	// Stop the timer using the tracker - this is idempotent
	logging.Log().Trace("stopping AddCu replacement timer", "shipID", shipID, "ski", service.SKI())
	h.addCuReplacementTracker.StopTimer(shipID)
}

// handleAddCuReplacementTimeout handles timeout callback from AddCu replacement tracker
// Timeout only reactivates pairing listener - trust removal happens during replacement pairing
func (h *Hub) handleAddCuReplacementTimeout(expiredShipID string) {
	logging.Log().Debug("AddCu device replacement timeout - reactivating pairing listener", "shipID", expiredShipID)

	// Find the service for this ShipID
	service := h.serviceForTrustedShipID(expiredShipID)
	if service == nil {
		logging.Log().Trace("No service found for ShipID during AddCu timeout", "shipID", expiredShipID)
		h.reactivatePairingListener("AddCu device replacement timeout")
		return
	}

	// Only handle AddCu devices
	if service.PairingType() != api.PairingTypeAddCu {
		logging.Log().Trace("Service is not AddCu type, ignoring timeout", "shipID", expiredShipID, "pairingType", service.PairingType())
		return
	}

	h.reactivatePairingListener("AddCu device replacement timeout")
}

// reactivatePairingListener reactivates the pairing listener when AddCu replacement timeout occurs
func (h *Hub) reactivatePairingListener(reason string) {
	logging.Log().Trace("Reactivating pairing listener", "reason", reason)

	h.muxPairing.RLock()
	pairingService := h.pairingService
	pairingConfig := h.pairingConfig
	h.muxPairing.RUnlock()

	// Handle case when no pairing service is configured
	if pairingService == nil || pairingConfig == nil {
		logging.Log().Trace("No pairing service configured, skipping reactivation")
		return
	}

	// Only reactivate for listener modes
	if pairingConfig.Mode != api.PairingModeListener && pairingConfig.Mode != api.PairingModeBoth {
		logging.Log().Trace("Pairing mode does not support listener, skipping reactivation", "mode", pairingConfig.Mode)
		return
	}

	// Attempt to reactivate the pairing listener
	if err := h.enablePairingListener(pairingConfig); err != nil {
		logging.Log().Error("Failed to reactivate pairing listener", "error", err, "reason", reason)
	} else {
		logging.Log().Trace("Successfully reactivated pairing listener", "reason", reason)
	}
}

// callDeviceAutoTrustRemovedCallback calls the DeviceAutoTrustRemovedViaReplacementLogic callback
// if the hub reader implements PairingServiceReaderInterface
func (h *Hub) callDeviceAutoTrustRemovedCallback(service *api.ServiceDetails, reason string) {
	// Check if hubReader implements PairingServiceReaderInterface
	if pairingReader, ok := h.hubReader.(api.PairingServiceReaderInterface); ok {
		logging.Log().Trace("Calling DeviceAutoTrustRemovedViaReplacementLogic callback", "ski", service.SKI(), "reason", reason)

		// Use Copy() to avoid race conditions as per ship-go patterns
		serviceCopy := service.Copy()
		pairingReader.DeviceAutoTrustRemovedViaReplacementLogic(serviceCopy, reason)
	} else {
		logging.Log().Trace("Hub reader does not implement PairingServiceReaderInterface, skipping trust removal callback")
	}
}
