package hub

import (
	"context"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/model"
	"github.com/enbility/ship-go/pairing"
)

// AutoTrustEstablishmentRequest contains the required parameters for establishing auto trust via pairing
type AutoTrustEstablishmentRequest struct {
	SKI         string
	ShipID      string
	Fingerprint string
	Context     string // for logging
}

// AutoTrustEstablishmentResult contains the result of establishing auto trust via pairing
type AutoTrustEstablishmentResult struct {
	Success        bool
	ServiceCreated bool // true if service was created, false if updated existing
	Error          error
}

// Provide the current pairing state for a ServiceIdentity
func (h *Hub) PairingDetailFor(identity api.ServiceIdentity) *api.ConnectionStateDetail {
	// Convert ServiceIdentity to ServiceDetails for service-based lookup
	serviceForLookup := identity.ToServiceDetails()
	if conn := h.connectionForService(serviceForLookup); conn != nil {
		shipState, shipError := conn.ShipHandshakeState()
		state := h.mapShipMessageExchangeState(shipState, identity.SKI)
		return api.NewConnectionStateDetail(state, shipError)
	}

	service := h.serviceFor(identity)
	if service == nil {
		return nil
	}
	return service.ConnectionStateDetail()
}

// maps ShipMessageExchangeState to PairingState
func (h *Hub) mapShipMessageExchangeState(state model.ShipMessageExchangeState, _ string) api.ConnectionState {
	var connState api.ConnectionState

	// map the SHIP states to a public ConnectionState
	switch state {
	case model.CmiStateInitStart:
		connState = api.ConnectionStateQueued
	case model.CmiStateClientSend, model.CmiStateClientWait, model.CmiStateClientEvaluate,
		model.CmiStateServerWait, model.CmiStateServerEvaluate:
		connState = api.ConnectionStateInitiated
	case model.SmeHelloStateReadyInit, model.SmeHelloStateReadyListen, model.SmeHelloStateReadyTimeout,
		model.SmeHelloStatePendingInit, model.SmeHelloStatePendingTimeout:
		connState = api.ConnectionStateInProgress
	case model.SmeHelloStatePendingListen:
		connState = api.ConnectionStateReceivedPairingRequest
	case model.SmeHelloStateOk:
		connState = api.ConnectionStateTrusted
	case model.SmeHelloStateAbort, model.SmeHelloStateAbortDone:
		connState = api.ConnectionStateNone
	case model.SmeHelloStateRemoteAbortDone, model.SmeHelloStateRejected:
		connState = api.ConnectionStateRemoteDeniedTrust
	case model.SmePinStateCheckInit, model.SmePinStateCheckListen, model.SmePinStateCheckError,
		model.SmePinStateCheckBusyInit, model.SmePinStateCheckBusyWait, model.SmePinStateCheckOk,
		model.SmePinStateAskInit, model.SmePinStateAskProcess, model.SmePinStateAskRestricted,
		model.SmePinStateAskOk:
		connState = api.ConnectionStatePin
	case model.SmeAccessMethodsRequest, model.SmeStateApproved:
		connState = api.ConnectionStateInProgress
	case model.SmeStateComplete:
		connState = api.ConnectionStateCompleted
	case model.SmeStateError:
		connState = api.ConnectionStateError
	default:
		connState = api.ConnectionStateInProgress
	}

	return connState
}

func (h *Hub) SetAutoAccept(autoaccept bool) {
	h.muxReg.Lock()
	defer h.muxReg.Unlock()

	h.autoaccept = autoaccept

	h.mdns.SetAutoAccept(autoaccept)
}

// check if auto accept is true
func (h *Hub) IsAutoAcceptEnabled() bool {
	h.muxReg.RLock()
	defer h.muxReg.RUnlock()

	return h.autoaccept
}

func (h *Hub) checkHasStarted() bool {
	h.muxStarted.RLock()
	defer h.muxStarted.RUnlock()
	return h.hasStarted
}

// Pair a remote service using ServiceIdentity
func (h *Hub) RegisterRemoteService(identity api.ServiceIdentity) {
	// Direct field extraction - cleaner than conversion function
	if identity.IsZero() {
		return
	}

	service := api.NewServiceDetails(identity.SKI, identity.Fingerprint, identity.ShipID)
	service.SetPairingType(identity.PairingType)
	service.SetIPv4(identity.IPv4)

	if success := h.addService(service); !success {
		return
	}

	service.SetTrusted(true)

	// if the hub has not started, simply add it
	if !h.checkHasStarted() {
		h.checkAutoReannounce()
		return
	}

	// if the hub has started, trigger a search and connection attempt
	conn := h.connectionForService(service)
	// remotely initiated?
	if conn != nil {
		conn.ApprovePendingHandshake()
		return
	}

	h.mdns.RequestMdnsEntries()
}

// Remove pairing using ServiceIdentity
func (h *Hub) UnregisterRemoteService(identity api.ServiceIdentity) {
	if service := h.serviceFor(identity); service != nil {
		service.SetTrusted(false)
		service.ConnectionStateDetail().SetState(api.ConnectionStateNone)
		h.hubReader.ServicePairingDetailUpdate(identity, service.ConnectionStateDetail())
		h.removeService(identity.SKI, identity.Fingerprint)
	}

	h.removeConnectionAttemptCounter(identity.SKI)

	// Convert ServiceIdentity to ServiceDetails for service-based lookup
	serviceForLookup := identity.ToServiceDetails()
	if existingC := h.connectionForService(serviceForLookup); existingC != nil {
		existingC.CloseConnection(true, 4500, "User close")
	}
}

// Disconnect a connection using ServiceIdentity, used by a service implementation
// e.g. if heartbeats go wrong
func (h *Hub) DisconnectService(identity api.ServiceIdentity, reason string) {
	// Convert ServiceIdentity to ServiceDetails for service-based lookup
	serviceForLookup := identity.ToServiceDetails()
	con := h.connectionForService(serviceForLookup)
	if con == nil {
		return
	}

	con.CloseConnection(true, 0, reason)
}

// Cancels the pairing process using ServiceIdentity
func (h *Hub) CancelPairing(identity api.ServiceIdentity) {
	h.removeConnectionAttemptCounter(identity.SKI)

	// Convert ServiceIdentity to ServiceDetails for service-based lookup
	serviceForLookup := identity.ToServiceDetails()
	if existingC := h.connectionForService(serviceForLookup); existingC != nil {
		existingC.AbortPendingHandshake()
	}

	if service := h.serviceFor(identity); service != nil {
		service.ConnectionStateDetail().SetState(api.ConnectionStateNone)
		service.SetTrusted(false)

		h.hubReader.ServicePairingDetailUpdate(identity, service.ConnectionStateDetail())
	}
}

/* SHIP Pairing Service Integration - Composition Pattern */

// PairingService returns the pairing service if available (implements HubInterface)
func (h *Hub) PairingService() api.ShipPairingServiceInterface {
	h.muxPairing.RLock()
	defer h.muxPairing.RUnlock()

	return h.pairingService
}

// SetPairingService configures the optional pairing service (called during Hub construction)
func (h *Hub) SetPairingService(service api.ShipPairingServiceInterface) error {
	h.muxPairing.Lock()
	defer h.muxPairing.Unlock()

	if h.hasStarted {
		return api.ErrServiceAlreadyStarted
	}

	h.pairingService = service
	return nil
}

/* Pairing package integration */

// initializePairingServiceWithConfig creates pairing service with provided configuration
func (h *Hub) initializePairingServiceWithConfig(config *api.PairingConfig) error {
	if config.Mode == api.PairingModeOff {
		return nil // No pairing service needed
	}

	// Avoid double initialization
	h.muxPairing.RLock()
	if h.pairingService != nil {
		h.muxPairing.RUnlock()
		return nil
	}
	h.muxPairing.RUnlock()

	h.pairingConfig = config

	// Create crypto provider
	cryptoProvider := pairing.NewHMACCalculator()

	// Check if mDNS supports pairing (type assertion)
	mdnsPairing, ok := h.mdns.(api.MdnsPairingInterface)
	if !ok {
		return fmt.Errorf("mDNS interface does not support pairing operations")
	}

	// Create ring buffer history provider from persistence interface
	// Use default ring buffer size of 100 entries (SHIP spec minimum is 10)
	var historyProvider api.PairingHistoryProviderInterface
	if h.ringBufferPersistence != nil {
		ringBufferProvider, err := pairing.NewRingBufferHistoryProvider(100, h.ringBufferPersistence)
		if err != nil {
			return fmt.Errorf("failed to create ring buffer history provider: %w", err)
		}
		historyProvider = ringBufferProvider
	}

	// Create pairing service with all dependencies
	service, err := pairing.NewService(
		mdnsPairing,     // mDNS pairing interface
		cryptoProvider,  // Crypto for HMAC
		historyProvider, // Ring buffer history provider
		h,               // Hub as PairingHubInterface
		h.certificate,   // Certificate
	)
	if err != nil {
		return fmt.Errorf("failed to create pairing service: %w", err)
	}

	h.pairingService = service

	return nil
}

// enablePairingListener starts ship pairing listener
func (h *Hub) enablePairingListener(config *api.PairingConfig) error {
	if h.pairingService == nil {
		return fmt.Errorf("pairing service not available")
	}

	// Note: Secret validation already done in PairingConfig.Validate() during Hub creation
	// But we need to validate secret exists for autonomous operation
	if len(config.Secret) == 0 {
		return fmt.Errorf("pairing secret required for autonomous listener")
	}

	// Thread-safe check and create listener (double-checked locking pattern)
	h.muxPairingListener.Lock()
	defer h.muxPairingListener.Unlock()

	var listener api.PairingListenerInterface
	if h.activePairingListener != nil {
		// Reuse existing listener
		listener = h.activePairingListener
	} else {
		// Create new listener through pairing service
		listener = h.pairingService.CreateListener(h.localService)
		if listener == nil {
			return fmt.Errorf("failed to create pairing listener")
		}

		// Store the listener for future reuse
		h.activePairingListener = listener
	}

	// Start listening automatically with configured secret
	ctx := h.pairingCtx // Use Hub's context for proper lifecycle management

	if err := listener.StartListening(ctx, config.Secret); err != nil {
		return fmt.Errorf("failed to start autonomous listener: %w", err)
	}

	return nil
}

// StartAnnouncementTo starts announcing pairing to a specific target device
func (h *Hub) StartAnnouncementTo(target *api.PairingTarget) error {
	if target == nil {
		return fmt.Errorf("target cannot be nil")
	}

	if target.ShipID == "" {
		return fmt.Errorf("target SHIP ID cannot be empty")
	}

	if target.SKI == "" {
		return fmt.Errorf("target SKI cannot be empty")
	}

	if len(target.Secret) == 0 {
		return fmt.Errorf("target secret cannot be empty")
	}

	// check if we are already connected to the target
	service := api.NewServiceDetails(target.SKI, target.Fingerprint, target.ShipID)
	conn := h.connectionForService(service)
	if conn != nil {
		if connState, err := conn.ShipHandshakeState(); err == nil && connState == model.SmeStateComplete {
			return nil
		}
	}

	h.muxPairing.RLock()
	pairingService := h.pairingService
	h.muxPairing.RUnlock()

	if pairingService == nil {
		return fmt.Errorf("pairing service not available")
	}

	h.muxAnnouncements.Lock()
	defer h.muxAnnouncements.Unlock()

	// Check if already announcing to this target
	if _, exists := h.activeAnnouncements[target.ShipID]; exists {
		return fmt.Errorf("already announcing to device: %s", target.ShipID)
	}

	// Create announcer for this target
	announcer := pairingService.CreateAnnouncer(h.localService)
	if announcer == nil {
		return fmt.Errorf("failed to create pairing announcer")
	}

	// Create context for this announcement (child of Hub's pairing context)
	_, cancel := context.WithCancel(h.pairingCtx)

	// Create announcement state
	state := &announcementState{
		target:     target,
		announcer:  announcer,
		startTime:  time.Now(),
		cancelFunc: cancel,
	}

	// Enable the announcer with the target's secret
	concreteAnnouncer, ok := announcer.(*pairing.PairingAnnouncer)
	if !ok {
		return fmt.Errorf("failed to cast announcer to concrete type")
	}

	// Configure announcer with target's secret
	pairingConfig := &pairing.PairingConfiguration{
		Mode:    pairing.PairingModeAnnouncer,
		Secret:  target.Secret,
		Enabled: true,
	}

	if err := concreteAnnouncer.EnablePairingService(pairingConfig); err != nil {
		return fmt.Errorf("failed to enable announcer: %w", err)
	}

	// Start the announcement - this starts the mDNS announcement and returns immediately
	// The announcement will remain active until explicitly stopped
	if err := announcer.Announce(target); err != nil {
		return fmt.Errorf("failed to start announcement: %w", err)
	}

	// Store the announcement state
	h.activeAnnouncements[target.ShipID] = state

	logging.Log().Debug("started announcement to", target.ShipID)
	return nil
}

// StopAnnouncementTo stops announcing pairing to a specific target device
func (h *Hub) StopAnnouncementTo(shipID string) error {
	if shipID == "" {
		return fmt.Errorf("SHIP ID cannot be empty")
	}

	h.muxAnnouncements.Lock()
	defer h.muxAnnouncements.Unlock()

	state, exists := h.activeAnnouncements[shipID]
	if !exists {
		return fmt.Errorf("no active announcement for device: %s", shipID)
	}

	// Cancel the announcement context
	if state.cancelFunc != nil {
		state.cancelFunc()
	}

	// Stop the announcer
	if state.announcer != nil {
		if err := state.announcer.StopAnnouncement(); err != nil {
			logging.Log().Error("error stopping announcement to", shipID, ":", err)
		}
	}

	// Remove from active announcements
	delete(h.activeAnnouncements, shipID)

	logging.Log().Debug("stopped announcement to", shipID)
	return nil
}

// GetActiveAnnouncements returns the list of SHIP IDs currently being announced to
func (h *Hub) GetActiveAnnouncements() []string {
	h.muxAnnouncements.RLock()
	defer h.muxAnnouncements.RUnlock()

	shipIDs := make([]string, 0, len(h.activeAnnouncements))
	for shipID := range h.activeAnnouncements {
		shipIDs = append(shipIDs, shipID)
	}

	return shipIDs
}

// IsAnnouncingTo checks if currently announcing to a specific target device
func (h *Hub) IsAnnouncingTo(shipID string) bool {
	h.muxAnnouncements.RLock()
	defer h.muxAnnouncements.RUnlock()

	_, exists := h.activeAnnouncements[shipID]
	return exists
}

/* PairingHubInterface Implementation */

var _ api.PairingHubInterface = (*Hub)(nil)

// OnPairingSuccess handles successful pairing validation (implements PairingHubInterface)
func (h *Hub) OnPairingSuccess(remoteShipID, remoteFingerprint string) {
	// Validate inputs - reject empty fingerprints for security
	if strings.TrimSpace(remoteFingerprint) == "" {
		logging.Log().Error("Pairing rejected: empty fingerprint provided for ShipID:", remoteShipID)
		return
	}

	// Check for existing trusted AddCu device BEFORE updating any services
	// Only replace if this is a NEW device (different fingerprint) replacing an existing one
	var replacedService *api.ServiceDetails
	existingAddCuFingerprint, existingAddCuShipID := h.HasTrustedAddCuDevice()

	if existingAddCuShipID != "" && existingAddCuFingerprint != remoteFingerprint {
		// Check if replacement timer is running
		if h.addCuReplacementTracker.IsInReplacementWindow() {
			// Timer is running - ignore this announcement (will be processed when timer expires via mDNS polling)
			logging.Log().Debug("Ignoring pairing announcement during replacement window",
				"existingShipID", existingAddCuShipID, "newShipID", remoteShipID)
			return
		}
		replacedService = h.ServiceForIdentifier("", existingAddCuFingerprint)
		// Only replace if the fingerprints are different (different devices)
		if replacedService != nil && replacedService.ShipID() != remoteShipID {
			replacedService.SetTrusted(false)
			h.removeService(replacedService.SKI(), replacedService.Fingerprint())
			// Stop any active replacement timer for the old device
			h.addCuReplacementTracker.StopTimer(existingAddCuShipID)
		} else if replacedService != nil {
			// Same fingerprint with different ShipID - don't replace, just update ShipID below
			replacedService = nil
		}
	}

	// Create service from pairing data and
	// establish trust immediately per SHIP spec section 4.2 step 3
	service := h.ServiceForIdentifier("", remoteFingerprint)
	if service == nil {
		// This should be the normal case
		service = api.NewServiceDetails("", remoteFingerprint, remoteShipID)
		if success := h.addService(service); !success {
			logging.Log().Error("Failed to add service during pairing success - ShipID:", remoteShipID, "Fingerprint:", remoteFingerprint)
			return
		}
	} else {
		// Handle existing service with same fingerprint
		existingShipID := service.ShipID()
		if existingShipID != "" && existingShipID != remoteShipID {
			// Security check: same fingerprint with different ShipID from trusted service
			// This could be an impersonation attempt - reject the pairing
			logging.Log().Error("Security violation: fingerprint already associated with different ShipID", "existingShipID", existingShipID, "newShipID", remoteShipID, "fingerprint", remoteFingerprint)
			return
		}
		// Safe to update ShipID (either empty or same as new one)
		service.SetShipID(remoteShipID)
	}

	service.SetTrusted(true)
	service.SetPairingType(api.PairingTypeAddCu)

	logging.Log().Trace("Pairing success - trust established immediately - ShipID:", remoteShipID, "Fingerprint:", remoteFingerprint)

	// Handle replacement callback before success callback
	if replacedService != nil {
		callbackReason := "Device replaced by new AddCu device during pairing"
		h.callDeviceAutoTrustRemovedCallback(replacedService, callbackReason)
	}

	// Direct callbacks following existing Hub patterns - call PairingServiceReaderInterface if available
	if pairingReader, ok := h.hubReader.(api.PairingServiceReaderInterface); ok {
		// Convert ServiceDetails to ServiceIdentity - thread-safe, no Copy() needed
		identity := service.ToServiceIdentity()
		pairingReader.ServiceAutoTrusted(identity)
	}

	// we have to initiate checking the mds records again, to trigger a connection
	h.mdns.RequestMdnsEntries()
}

// OnPairingFailure handles pairing validation failure (implements PairingHubInterface)
func (h *Hub) OnPairingFailure(remoteShipID, remoteFingerprint string, reason error) {
	// Use ServiceForFingerprint for consistent service management and SKI normalization
	service := api.NewServiceDetails("", remoteFingerprint, remoteShipID)
	service.ConnectionStateDetail().SetState(api.ConnectionStateError)
	service.ConnectionStateDetail().SetError(reason)

	// Direct callbacks following existing Hub patterns
	if pairingReader, ok := h.hubReader.(api.PairingServiceReaderInterface); ok {
		// Convert ServiceDetails to ServiceIdentity - thread-safe, no Copy() needed
		identity := service.ToServiceIdentity()
		pairingReader.ServiceAutoTrustFailed(identity, reason)
	}
}

// GetLocalCertificateFingerprint calculates SHA-256 fingerprint of Hub's certificate
func (h *Hub) GetLocalCertificateFingerprint() (string, error) {
	if len(h.certificate.Certificate) == 0 {
		return "", api.ErrInvalidCertificate
	}

	// Parse DER certificate
	certificate, err := x509.ParseCertificate(h.certificate.Certificate[0])
	if err != nil {
		return "", api.ErrInvalidCertificate
	}

	// Calculate SHA-256 fingerprint using cert package
	return cert.FingerprintFromCertificate(certificate)
}

// GeneratePairingQR generates a QR code string for pairing
// When secret is nil/empty: generates standard SHIP QR format
// When secret is provided: generates SHIP Pairing Service QR format
func (h *Hub) GeneratePairingQR(secret api.PairingSecret) (string, error) {
	if len(secret) == 0 {
		// Generate standard SHIP QR format: SHIP;SKI:<ski>;ID:<identifier>;<optionals>ENDSHIP;
		return h.generateStandardShipQR()
	}

	// Generate SHIP Pairing Service QR format
	return h.generatePairingServiceQR(secret)
}

// generateStandardShipQR generates the standard SHIP QR format
func (h *Hub) generateStandardShipQR() (string, error) {
	ski := h.localService.SKI()
	identifier := h.localService.ShipID()
	optionals := h.buildOptionalMetadata()

	qrcode := fmt.Sprintf("SHIP;SKI:%s;ID:%s;%sENDSHIP;", ski, identifier, optionals)
	return qrcode, nil
}

// generatePairingServiceQR generates the SHIP Pairing Service QR format
func (h *Hub) generatePairingServiceQR(secret api.PairingSecret) (string, error) {
	// Validate secret is exactly 16 bytes
	if len(secret) < 16 {
		return "", api.ErrSecretTooShort
	}
	if len(secret) > 16 {
		return "", api.ErrInvalidSecret
	}

	// Get certificate fingerprint
	fingerprint, err := h.GetLocalCertificateFingerprint()
	if err != nil {
		return "", err
	}

	// Get required fields
	ski := h.localService.SKI()
	shipID := h.localService.ShipID()

	// Encode secret as uppercase hex
	secretHex := strings.ToUpper(hex.EncodeToString(secret))

	// Get optional device metadata
	optionals := h.buildOptionalMetadata()

	// Generate SHIP Pairing Service QR format per SHIP spec Annex A.1:
	// SHIP;SKI:xxx;ID:xxx;BRAND:xxx;TYPE:xxx;MODEL:xxx;SERIAL:xxx;CAT:xxx;FPH256:xxx;SPSEC:xxx;ENDSHIP;
	qrString := fmt.Sprintf("SHIP;SKI:%s;ID:%s;%sFPH256:%s;SPSEC:%s;ENDSHIP;",
		ski, shipID, optionals, fingerprint, secretHex)

	return qrString, nil
}

// buildOptionalMetadata builds the optional metadata string for QR codes
func (h *Hub) buildOptionalMetadata() string {
	var optionals string

	// Get device metadata from mDNS interface
	if brand := h.mdns.DeviceBrand(); len(brand) > 0 {
		optionals += h.safeQRCodeKeyValue("BRAND", brand)
	}

	if deviceType := h.mdns.DeviceType(); len(deviceType) > 0 {
		optionals += h.safeQRCodeKeyValue("TYPE", deviceType)
	}

	if model := h.mdns.DeviceModel(); len(model) > 0 {
		optionals += h.safeQRCodeKeyValue("MODEL", model)
	}

	if serial := h.mdns.DeviceSerial(); len(serial) > 0 {
		optionals += h.safeQRCodeKeyValue("SERIAL", serial)
	}

	if categories := h.mdns.DeviceCategories(); categories != nil {
		optionals += h.safeQRCodeKeyValue("CAT", h.deviceCategoriesString(categories))
	}

	return optionals
}

// safeQRCodeKeyValue returns a safe to use key value pair for the QR code text in the proper format
// according to SHIP Requirements for Installation Process V1.0.0
func (h *Hub) safeQRCodeKeyValue(key, value string) string {
	if len(value) > 0 {
		// make sure the value contains no ; chars
		value = strings.ReplaceAll(value, ";", "")

		// make sure the keys are all uppercase
		key = strings.ToUpper(key)
		return fmt.Sprintf("%s:%s;", key, value)
	}

	return ""
}

// deviceCategoriesString returns the device categories as a string, with categories separated by commas
func (h *Hub) deviceCategoriesString(categories []api.DeviceCategoryType) string {
	var cat string
	for _, category := range categories {
		if len(cat) > 0 {
			cat += ","
		}
		cat += fmt.Sprintf("%d", category)
	}
	return cat
}
