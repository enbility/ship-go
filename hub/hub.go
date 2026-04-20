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

// handling the server and all connections to remote services
type Hub struct {
	// registry owns the per-SKI connection map and serializes the §12.2.2
	// double-connection rule with map mutations. All connection lifecycle
	// touches go through this; no direct map access remains in this file.
	registry *connectionRegistry

	// which attempt is it to initate an connection to the remote SKI
	connectionAttemptCounter map[string]int
	connectionAttemptRunning map[string]bool

	port        int
	certifciate tls.Certificate

	localService *api.ServiceDetails

	hubReader api.HubReaderInterface

	autoaccept bool

	// The list of known remote services
	remoteServices map[string]*api.ServiceDetails

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

	// Lifecycle: ctx is cancelled by Shutdown to signal goroutines (timer
	// callbacks, in-flight dials) to abort. inflightWg tracks in-flight
	// connection-establishment goroutines so Shutdown can drain them before
	// returning. ctx must be checked at the entry of any long-lived path that
	// could outlive Shutdown.
	ctx        context.Context
	cancel     context.CancelFunc
	inflightWg sync.WaitGroup

	// muxCon historically protected the connections map. With registry in place
	// it now only protects maxConnections (read by validateConnectionLimit /
	// ServeHTTP, written by SetMaxConnections). The connection map itself
	// is owned by registry.mu.
	muxCon        sync.RWMutex
	muxConAttempt sync.RWMutex
	muxReg        sync.RWMutex
	muxMdns       sync.Mutex
	muxStarted    sync.RWMutex
}

func NewHub(hubReader api.HubReaderInterface,
	mdns api.MdnsInterface,
	port int,
	certificate tls.Certificate,
	localService *api.ServiceDetails) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	hub := &Hub{
		registry:                 newConnectionRegistry(localService.SKI()),
		connectionAttemptCounter: make(map[string]int),
		connectionAttemptRunning: make(map[string]bool),
		remoteServices:           make(map[string]*api.ServiceDetails),
		knownMdnsEntries:         make([]*api.MdnsEntry, 0),
		connectionDelayTimers:    make(map[string]*connectionDelayTimer),
		hubReader:                hubReader,
		port:                     port,
		certifciate:              certificate,
		localService:             localService,
		mdns:                     mdns,
		maxConnections:           10, // Default connection limit
		serverStarted:            make(chan struct{}),
		ctx:                      ctx,
		cancel:                   cancel,
	}

	return hub
}

var _ api.HubInterface = (*Hub)(nil)

// Start the ConnectionsHub with all its services
func (h *Hub) Start() error {
	h.muxStarted.Lock()
	defer h.muxStarted.Unlock()

	if h.hasStarted {
		return fmt.Errorf("%w: call Shutdown() before restarting", api.ErrHubAlreadyStarted)
	}

	// Reset server state for a fresh start or retry after a previous failure
	h.serverStarted = make(chan struct{})
	h.serverStartErr = nil

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

	// start mDNS
	if err := h.mdns.Start(h); err != nil {
		// Shutdown the server if mDNS fails
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := h.httpServer.Shutdown(ctx); shutdownErr != nil {
			logging.Log().Error("failed to shutdown HTTP server after mDNS error:", shutdownErr)
		}
		return fmt.Errorf("failed to start mDNS: %w", err)
	}

	h.hasStarted = true
	return nil
}

// close all connections
func (h *Hub) Shutdown() {
	// Cancel the lifecycle context FIRST so that:
	//   - timer callbacks (prepareConnectionInitation) short-circuit on h.ctx.Err()
	//   - in-flight connectFoundService goroutines see ctx.Err() and abort
	// before we tear down state they might touch.
	h.cancel()

	// First, stop accepting new connections by shutting down the HTTP server
	if h.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.httpServer.Shutdown(ctx); err != nil {
			logging.Log().Error("HTTP server shutdown error:", err)
		}
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
	connections := h.registry.Snapshot()

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

	// Drain in-flight connection-establishment goroutines (connectFoundService).
	// Each such goroutine calls h.inflightWg.Add(1)/Done() at its boundary.
	// 6s budget covers the 5s websocket dial HandshakeTimeout plus slack.
	inflightDone := make(chan struct{})
	go func() {
		h.inflightWg.Wait()
		close(inflightDone)
	}()
	select {
	case <-inflightDone:
	case <-time.After(6 * time.Second):
		logging.Log().Error("timeout waiting for in-flight connection attempts to drain")
	}

	// Reset lifecycle state so the Hub can be restarted.
	// NOTE: ctx is intentionally NOT re-armed here — reassigning a context
	// interface concurrently with reads from connectFoundService /
	// prepareConnectionInitation would be a torn-read race. Shutdown is therefore
	// terminal for the lifecycle context; Start after Shutdown will succeed but
	// any subsequent connection attempts will short-circuit on ctx.Err(). To
	// restart with connections, create a new Hub instance.
	h.muxStarted.Lock()
	h.hasStarted = false
	h.serverStarted = make(chan struct{})
	h.serverStartErr = nil
	h.muxStarted.Unlock()
}

// return the service for a SKI
func (h *Hub) ServiceForSKI(ski string) *api.ServiceDetails {
	h.muxReg.Lock()
	defer h.muxReg.Unlock()

	ski = util.NormalizeSKI(ski)

	service, ok := h.remoteServices[ski]
	if !ok {
		service = api.NewServiceDetails(ski)
		service.ConnectionStateDetail().SetState(api.ConnectionStateNone)
		h.remoteServices[ski] = service
	}

	return service
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
	countConnections := h.registry.Len()

	if countPairedServices > countConnections {
		_ = h.mdns.AnnounceMdnsEntry()

		// also check currently known mDNS entries to see if they
		// already contain the not connected remote service
		h.mdns.RequestMdnsEntries()
	}
}
