package hub

import (
	"context"
	"crypto/tls"
	"net/http"
	"sync"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
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
	connections map[string]api.ShipConnectionInterface

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

	muxCon        sync.Mutex
	muxConAttempt sync.Mutex
	muxReg        sync.Mutex
	muxMdns       sync.Mutex
	muxStarted    sync.Mutex
}

func NewHub(hubReader api.HubReaderInterface,
	mdns api.MdnsInterface,
	port int,
	certificate tls.Certificate,
	localService *api.ServiceDetails) *Hub {
	hub := &Hub{
		connections:              make(map[string]api.ShipConnectionInterface),
		connectionAttemptCounter: make(map[string]int),
		connectionAttemptRunning: make(map[string]bool),
		remoteServices:           make(map[string]*api.ServiceDetails),
		knownMdnsEntries:         make([]*api.MdnsEntry, 0),
		hubReader:                hubReader,
		port:                     port,
		certifciate:              certificate,
		localService:             localService,
		mdns:                     mdns,
	}

	return hub
}

var _ api.HubInterface = (*Hub)(nil)

// Start the ConnectionsHub with all its services
func (h *Hub) Start() {
	h.muxStarted.Lock()
	h.hasStarted = true
	h.muxStarted.Unlock()

	// start the websocket server
	if err := h.startWebsocketServer(); err != nil {
		logging.Log().Debug("error during websocket server starting:", err)
	}

	// start mDNS
	err := h.mdns.Start(h)
	if err != nil {
		logging.Log().Debug("error during mdns setup:", err)
	}
}

// close all connections
func (h *Hub) Shutdown() {
	h.mdns.Shutdown()
	for _, c := range h.connections {
		c.CloseConnection(false, 0, "")
	}
	if h.httpServer == nil {
		return
	}
	if err := h.httpServer.Shutdown(context.Background()); err != nil {
		logging.Log().Error("HTTP server shutdown:", err)
	}
}

// return the service for a SKI
func (h *Hub) ServiceForSKI(ski string) *api.ServiceDetails {
	h.muxReg.Lock()
	defer h.muxReg.Unlock()

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

	h.muxReg.Lock()
	for _, service := range h.remoteServices {
		if service.Trusted() {
			amount++
		}
	}
	h.muxReg.Unlock()

	return amount
}

// startup mDNS if a paired service is not connected
func (h *Hub) checkAutoReannounce() {
	countPairedServices := h.numberPairedServices()
	h.muxCon.Lock()
	countConnections := len(h.connections)
	h.muxCon.Unlock()

	if countPairedServices > countConnections {
		_ = h.mdns.AnnounceMdnsEntry()
	}
}
