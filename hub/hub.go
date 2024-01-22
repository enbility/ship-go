package hub

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/model"
	"github.com/enbility/ship-go/ship"
	"github.com/enbility/ship-go/ws"
	"github.com/gorilla/websocket"
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
type HubImpl struct {
	connections map[string]api.ShipConnection

	// which attempt is it to initate an connection to the remote SKI
	connectionAttemptCounter map[string]int
	connectionAttemptRunning map[string]bool

	port        int
	certifciate tls.Certificate

	localService *api.ServiceDetails

	serviceProvider api.HubConnection

	// The list of known remote services
	remoteServices map[string]*api.ServiceDetails

	// The web server for handling incoming websocket connections
	httpServer *http.Server

	// Handling mDNS related tasks
	mdns api.MdnsService

	// list of currently known/reported mDNS entries
	knownMdnsEntries []*api.MdnsEntry

	muxCon        sync.Mutex
	muxConAttempt sync.Mutex
	muxReg        sync.Mutex
	muxMdns       sync.Mutex
}

func NewHub(serviceProvider api.HubConnection, mdns api.MdnsService, port int, certificate tls.Certificate, localService *api.ServiceDetails) *HubImpl {
	hub := &HubImpl{
		connections:              make(map[string]api.ShipConnection),
		connectionAttemptCounter: make(map[string]int),
		connectionAttemptRunning: make(map[string]bool),
		remoteServices:           make(map[string]*api.ServiceDetails),
		knownMdnsEntries:         make([]*api.MdnsEntry, 0),
		serviceProvider:          serviceProvider,
		port:                     port,
		certifciate:              certificate,
		localService:             localService,
		mdns:                     mdns,
	}

	return hub
}

// Start the ConnectionsHub with all its services
func (h *HubImpl) Start() {
	// start the websocket server
	if err := h.startWebsocketServer(); err != nil {
		logging.Log().Debug("error during websocket server starting:", err)
	}

	// start mDNS
	err := h.mdns.SetupMdnsService()
	if err != nil {
		logging.Log().Debug("error during mdns setup:", err)
	}

	h.checkRestartMdnsSearch()
}

var _ api.ShipServiceDataProvider = (*HubImpl)(nil)

// Returns if the provided SKI is from a registered service
func (h *HubImpl) IsRemoteServiceForSKIPaired(ski string) bool {
	service := h.ServiceForSKI(ski)

	return service.Trusted()
}

// The connection was closed, we need to clean up
func (h *HubImpl) HandleConnectionClosed(connection api.ShipConnection, handshakeCompleted bool) {
	remoteSki := connection.RemoteSKI()

	// only remove this connection if it is the registered one for the ski!
	// as we can have double connections but only one can be registered
	if existingC := h.connectionForSKI(remoteSki); existingC != nil {
		if existingC.DataHandler() == connection.DataHandler() {
			h.muxCon.Lock()
			delete(h.connections, connection.RemoteSKI())
			h.muxCon.Unlock()
		}

		// connection close was after a completed handshake, so we can reset the attetmpt counter
		if handshakeCompleted {
			h.removeConnectionAttemptCounter(connection.RemoteSKI())
		}
	}

	h.serviceProvider.RemoteSKIDisconnected(connection.RemoteSKI())

	// Do not automatically reconnect if handshake failed and not already paired
	remoteService := h.ServiceForSKI(connection.RemoteSKI())
	if !handshakeCompleted && !remoteService.Trusted() {
		return
	}

	h.checkRestartMdnsSearch()
}

func (h *HubImpl) SetupRemoteDevice(ski string, writeI api.SpineDataConnection) api.SpineDataProcessing {
	return h.serviceProvider.SetupRemoteDevice(ski, writeI)
}

// return the number of paired services
func (h *HubImpl) numberPairedServices() int {
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
func (h *HubImpl) checkRestartMdnsSearch() {
	countPairedServices := h.numberPairedServices()
	h.muxCon.Lock()
	countConnections := len(h.connections)
	h.muxCon.Unlock()

	if countPairedServices > countConnections {
		_ = h.mdns.AnnounceMdnsEntry()

		h.mdns.RegisterMdnsSearch(h)
	}
}

func (h *HubImpl) StartBrowseMdnsSearch() {
	// TODO: this currently collides with searching for a specific SKI
	h.mdns.RegisterMdnsSearch(h)
}

func (h *HubImpl) StopBrowseMdnsSearch() {
	// TODO: this currently collides with searching for a specific SKI
	h.mdns.UnregisterMdnsSearch(h)
}

// Provides the SHIP ID the remote service reported during the handshake process
func (h *HubImpl) ReportServiceShipID(ski string, shipdID string) {
	h.serviceProvider.RemoteSKIConnected(ski)

	h.serviceProvider.ServiceShipIDUpdate(ski, shipdID)
}

// return if the user is still able to trust the connection
func (h *HubImpl) AllowWaitingForTrust(ski string) bool {
	if service := h.ServiceForSKI(ski); service != nil {
		if service.Trusted() {
			return true
		}
	}

	return h.serviceProvider.AllowWaitingForTrust(ski)
}

// Provides the current ship message exchange state for a given SKI and the corresponding error if state is error
func (h *HubImpl) HandleShipHandshakeStateUpdate(ski string, state model.ShipState) {
	// overwrite service Paired value
	if state.State == model.SmeHelloStateOk {
		h.RegisterRemoteSKI(ski, true)
	}

	pairingState := h.mapShipMessageExchangeState(state.State, ski)
	if state.Error != nil && state.Error != api.ErrConnectionNotFound {
		pairingState = api.ConnectionStateError
	}

	pairingDetail := api.NewConnectionStateDetail(pairingState, state.Error)

	service := h.ServiceForSKI(ski)

	existingDetails := service.ConnectionStateDetail()
	existingState := existingDetails.State()
	if existingState != pairingState || existingDetails.Error() != state.Error {
		service.SetConnectionStateDetail(pairingDetail)

		h.serviceProvider.ServicePairingDetailUpdate(ski, pairingDetail)
	}
}

// Provide the current pairing state for a given SKI
//
// returns:
//
//	ErrNotPaired if the SKI is not in the (to be) paired list
//	ErrNoConnectionFound if no connection for the SKI was found
func (h *HubImpl) PairingDetailForSki(ski string) *api.ConnectionStateDetail {
	service := h.ServiceForSKI(ski)

	if conn := h.connectionForSKI(ski); conn != nil {
		shipState, shipError := conn.ShipHandshakeState()
		state := h.mapShipMessageExchangeState(shipState, ski)
		return api.NewConnectionStateDetail(state, shipError)
	}

	return service.ConnectionStateDetail()
}

// maps ShipMessageExchangeState to PairingState
func (h *HubImpl) mapShipMessageExchangeState(state model.ShipMessageExchangeState, ski string) api.ConnectionState {
	var connState api.ConnectionState

	// map the SHIP states to a public gState
	switch state {
	case model.CmiStateInitStart:
		connState = api.ConnectionStateQueued
	case model.CmiStateClientSend, model.CmiStateClientWait, model.CmiStateClientEvaluate,
		model.CmiStateServerWait, model.CmiStateServerEvaluate:
		connState = api.ConnectionStateInitiated
	case model.SmeHelloStateReadyInit, model.SmeHelloStateReadyListen, model.SmeHelloStateReadyTimeout:
		connState = api.ConnectionStateInProgress
	case model.SmeHelloStatePendingInit, model.SmeHelloStatePendingListen, model.SmeHelloStatePendingTimeout:
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

// Disconnect a connection to an SKI, used by a service implementation
// e.g. if heartbeats go wrong
func (h *HubImpl) DisconnectSKI(ski string, reason string) {
	h.muxCon.Lock()
	defer h.muxCon.Unlock()

	// The connection with the higher SKI should retain the connection
	con, ok := h.connections[ski]
	if !ok {
		return
	}

	con.CloseConnection(true, 0, reason)
}

// register a new ship Connection
func (h *HubImpl) registerConnection(connection api.ShipConnection) {
	h.muxCon.Lock()
	h.connections[connection.RemoteSKI()] = connection
	h.muxCon.Unlock()
}

// return the connection for a specific SKI
func (h *HubImpl) connectionForSKI(ski string) api.ShipConnection {
	h.muxCon.Lock()
	defer h.muxCon.Unlock()

	con, ok := h.connections[ski]
	if !ok {
		return nil
	}
	return con
}

// close all connections
func (h *HubImpl) Shutdown() {
	h.mdns.ShutdownMdnsService()
	for _, c := range h.connections {
		c.CloseConnection(false, 0, "")
	}
}

// return if there is a connection for a SKI
func (h *HubImpl) isSkiConnected(ski string) bool {
	h.muxCon.Lock()
	defer h.muxCon.Unlock()

	// The connection with the higher SKI should retain the connection
	_, ok := h.connections[ski]
	return ok
}

// Websocket connection handling
func (h *HubImpl) verifyPeerCertificate(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	skiFound := false
	for _, v := range rawCerts {
		cerificate, err := x509.ParseCertificate(v)
		if err != nil {
			return err
		}

		if _, err := cert.SkiFromCertificate(cerificate); err == nil {
			skiFound = true
			break
		}
	}
	if !skiFound {
		return errors.New("no valid SKI provided in certificate")
	}

	return nil
}

// start the ship websocket server
func (h *HubImpl) startWebsocketServer() error {
	addr := fmt.Sprintf(":%d", h.port)
	logging.Log().Debug("starting websocket server on", addr)

	h.httpServer = &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: time.Duration(time.Second * 10),
		TLSConfig: &tls.Config{
			Certificates:          []tls.Certificate{h.certifciate},
			ClientAuth:            tls.RequireAnyClientCert, // SHIP 9: Client authentication is required
			CipherSuites:          cert.CipherSuites,        // #nosec G402 // SHIP 9.1: the ciphers are reported insecure but are defined to be used by SHIP
			VerifyPeerCertificate: h.verifyPeerCertificate,
			MinVersion:            tls.VersionTLS12, // SHIP 9: Mandatory TLS version
		},
	}

	go func() {
		if err := h.httpServer.ListenAndServeTLS("", ""); err != nil {
			logging.Log().Debug("websocket server error:", err)
			// TODO: decide how to handle this case
		}
	}()

	return nil
}

// Connection Handling

// HTTP Server callback for handling incoming connection requests
func (h *HubImpl) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  ws.MaxMessageSize,
		WriteBufferSize: ws.MaxMessageSize,
		CheckOrigin:     func(r *http.Request) bool { return true },
		Subprotocols:    []string{api.ShipWebsocketSubProtocol}, // SHIP 10.2: Sub protocol "ship" is required
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logging.Log().Debug("error during connection upgrading:", err)
		return
	}

	// check if the client supports the ship sub protocol
	if conn.Subprotocol() != api.ShipWebsocketSubProtocol {
		logging.Log().Debug("client does not support the ship sub protocol")
		_ = conn.Close()
		return
	}

	// check if the clients certificate provides a SKI
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		logging.Log().Debug("client does not provide a certificate")
		_ = conn.Close()
		return
	}

	ski, err := cert.SkiFromCertificate(r.TLS.PeerCertificates[0])
	if err != nil {
		logging.Log().Debug(err)
		_ = conn.Close()
		return
	}

	// normalize the incoming SKI
	remoteService := api.NewServiceDetails(ski)
	logging.Log().Debug("incoming connection request from", remoteService.SKI)

	// Check if the remote service is paired
	service := h.ServiceForSKI(remoteService.SKI())
	connectionStateDetail := service.ConnectionStateDetail()
	if connectionStateDetail.State() == api.ConnectionStateQueued {
		connectionStateDetail.SetState(api.ConnectionStateReceivedPairingRequest)
		h.serviceProvider.ServicePairingDetailUpdate(ski, connectionStateDetail)
	}

	remoteService = service

	// don't allow a second connection
	if !h.keepThisConnection(conn, true, remoteService) {
		_ = conn.Close()
		return
	}

	dataHandler := ws.NewWebsocketConnection(conn, remoteService.SKI())
	shipConnection := ship.NewConnectionHandler(h, dataHandler, ship.ShipRoleServer,
		h.localService.ShipID(), remoteService.SKI(), remoteService.ShipID())
	shipConnection.Run()

	h.registerConnection(shipConnection)
}

// Connect to another EEBUS service
//
// returns error contains a reason for failing the connection or nil if no further tries should be processed
func (h *HubImpl) connectFoundService(remoteService *api.ServiceDetails, host, port, path string) error {
	if h.isSkiConnected(remoteService.SKI()) {
		return nil
	}

	logging.Log().Debugf("initiating connection to %s at %s:%s%s", remoteService.SKI, host, port, path)

	dialer := &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 5 * time.Second,
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{h.certifciate},
			// SHIP 12.1: all certificates are locally signed
			InsecureSkipVerify: true, // #nosec G402
			// SHIP 9.1: the ciphers are reported insecure but are defined to be used by SHIP
			CipherSuites: cert.CipherSuites, // #nosec G402
		},
		Subprotocols: []string{api.ShipWebsocketSubProtocol},
	}

	address := fmt.Sprintf("wss://%s:%s%s", host, port, path)
	conn, _, err := dialer.Dial(address, nil)
	if err != nil {
		address = fmt.Sprintf("wss://%s:%s", host, port)
		conn, _, err = dialer.Dial(address, nil)
	}
	if err != nil {
		return err
	}

	tlsConn := conn.UnderlyingConn().(*tls.Conn)
	remoteCerts := tlsConn.ConnectionState().PeerCertificates

	if len(remoteCerts) == 0 || remoteCerts[0].SubjectKeyId == nil {
		// Close connection as we couldn't get the remote SKI
		errorString := fmt.Sprintf("closing connection to %s: could not get remote SKI from certificate", remoteService.SKI())
		_ = conn.Close()
		return errors.New(errorString)
	}

	if _, err := cert.SkiFromCertificate(remoteCerts[0]); err != nil {
		// Close connection as the remote SKI can't be correct
		errorString := fmt.Sprintf("closing connection to %s: %s", remoteService.SKI(), err)
		_ = conn.Close()
		return errors.New(errorString)
	}

	remoteSKI := fmt.Sprintf("%0x", remoteCerts[0].SubjectKeyId)

	if remoteSKI != remoteService.SKI() {
		errorString := fmt.Sprintf("closing connection to %s: SKI does not match %s", remoteService.SKI(), remoteSKI)
		_ = conn.Close()
		return errors.New(errorString)
	}

	if !h.keepThisConnection(conn, false, remoteService) {
		errorString := fmt.Sprintf("closing connection to %s: ignoring this connection", remoteService.SKI())
		return errors.New(errorString)
	}

	dataHandler := ws.NewWebsocketConnection(conn, remoteService.SKI())
	shipConnection := ship.NewConnectionHandler(h, dataHandler, ship.ShipRoleClient,
		h.localService.ShipID(), remoteService.SKI(), remoteService.ShipID())
	shipConnection.Run()

	h.registerConnection(shipConnection)

	return nil
}

// prevent double connections
// only keep the connection initiated by the higher SKI
//
// returns true if this connection is fine to be continue
// returns false if this connection should not be established or kept
func (h *HubImpl) keepThisConnection(conn *websocket.Conn, incomingRequest bool, remoteService *api.ServiceDetails) bool {
	// SHIP 12.2.2 defines:
	// prevent double connections with SKI Comparison
	// the node with the hight SKI value kees the most recent connection and
	// and closes all other connections to the same SHIP node
	//
	// This is hard to implement without any flaws. Therefor I chose a
	// different approach: The connection initiated by the higher SKI will be kept

	remoteSKI := remoteService.SKI()
	existingC := h.connectionForSKI(remoteSKI)
	if existingC == nil {
		return true
	}

	keep := false
	if incomingRequest {
		keep = remoteSKI > h.localService.SKI()
	} else {
		keep = h.localService.SKI() > remoteSKI
	}

	if keep {
		// we have an existing connection
		// so keep the new (most recent) and close the old one
		logging.Log().Debug("closing existing double connection")
		go existingC.CloseConnection(false, 0, "")
	} else {
		connType := "incoming"
		if !incomingRequest {
			connType = "outgoing"
		}
		logging.Log().Debugf("closing %s double connection, as the existing connection will be used", connType)
		if conn != nil {
			go func() {
				_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "double connection"))
				time.Sleep(time.Millisecond * 100)
				_ = conn.Close()
			}()
		}
	}

	return keep
}

// return the service for a given SKI or an error if not found
func (h *HubImpl) ServiceForSKI(ski string) *api.ServiceDetails {
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

// Sets the SKI as being paired or not
// Should be used for services which completed the pairing process and
// which were stored as having the process completed
func (h *HubImpl) RegisterRemoteSKI(ski string, enable bool) {
	service := h.ServiceForSKI(ski)
	service.SetTrusted(enable)

	if enable {
		h.checkRestartMdnsSearch()
		return
	}

	h.removeConnectionAttemptCounter(ski)

	service.ConnectionStateDetail().SetState(api.ConnectionStateNone)

	h.serviceProvider.ServicePairingDetailUpdate(ski, service.ConnectionStateDetail())

	if existingC := h.connectionForSKI(ski); existingC != nil {
		existingC.CloseConnection(true, 4500, "User close")
	}
}

// Triggers the pairing process for a SKI
func (h *HubImpl) InitiatePairingWithSKI(ski string) {
	conn := h.connectionForSKI(ski)

	// remotely initiated
	if conn != nil {
		conn.ApprovePendingHandshake()

		return
	}

	// locally initiated
	service := h.ServiceForSKI(ski)
	service.ConnectionStateDetail().SetState(api.ConnectionStateQueued)

	h.serviceProvider.ServicePairingDetailUpdate(ski, service.ConnectionStateDetail())

	// initiate a search and also a connection if it does not yet exist
	if !h.isSkiConnected(service.SKI()) {
		h.mdns.RegisterMdnsSearch(h)
	}
}

// Cancels the pairing process for a SKI
func (h *HubImpl) CancelPairingWithSKI(ski string) {
	h.removeConnectionAttemptCounter(ski)

	if existingC := h.connectionForSKI(ski); existingC != nil {
		existingC.AbortPendingHandshake()
	}

	service := h.ServiceForSKI(ski)
	service.ConnectionStateDetail().SetState(api.ConnectionStateNone)
	service.SetTrusted(false)

	h.serviceProvider.ServicePairingDetailUpdate(ski, service.ConnectionStateDetail())
}

// Process reported mDNS services
func (h *HubImpl) ReportMdnsEntries(entries map[string]*api.MdnsEntry) {
	h.muxMdns.Lock()
	defer h.muxMdns.Unlock()

	var mdnsEntries []*api.MdnsEntry

	for ski, entry := range entries {
		mdnsEntries = append(mdnsEntries, entry)

		// check if this ski is already connected
		if h.isSkiConnected(ski) {
			continue
		}

		// Check if the remote service is paired or queued for connection
		service := h.ServiceForSKI(ski)
		if !h.IsRemoteServiceForSKIPaired(ski) &&
			service.ConnectionStateDetail().State() != api.ConnectionStateQueued {
			continue
		}

		// patch the addresses list if an IPv4 address was provided
		if service.IPv4() != "" {
			if ip := net.ParseIP(service.IPv4()); ip != nil {
				entry.Addresses = []net.IP{ip}
			}
		}

		h.coordinateConnectionInitations(ski, entry)
	}

	sort.Slice(mdnsEntries, func(i, j int) bool {
		item1 := mdnsEntries[i]
		item2 := mdnsEntries[j]
		a := strings.ToLower(item1.Brand + item1.Model + item1.Ski)
		b := strings.ToLower(item2.Brand + item2.Model + item2.Ski)
		return a < b
	})

	h.knownMdnsEntries = mdnsEntries

	h.serviceProvider.VisibleMDNSRecordsUpdated(mdnsEntries)
}

// coordinate connection initiation attempts to a remove service
func (h *HubImpl) coordinateConnectionInitations(ski string, entry *api.MdnsEntry) {
	if h.isConnectionAttemptRunning(ski) {
		return
	}

	h.setConnectionAttemptRunning(ski, true)

	counter, duration := h.getConnectionInitiationDelayTime(ski)

	service := h.ServiceForSKI(ski)
	if service.ConnectionStateDetail().State() == api.ConnectionStateQueued {
		go h.prepareConnectionInitation(ski, counter, entry)
		return
	}

	logging.Log().Debugf("delaying connection to %s by %s to minimize double connection probability", ski, duration)

	// we do not stop this thread and just let the timer run out
	// otherwise we would need a stop channel for each ski
	go func() {
		// wait
		<-time.After(duration)

		h.prepareConnectionInitation(ski, counter, entry)
	}()
}

// invoked by coordinateConnectionInitations either with a delay or directly
// when initating a pairing process
func (h *HubImpl) prepareConnectionInitation(ski string, counter int, entry *api.MdnsEntry) {
	h.setConnectionAttemptRunning(ski, false)

	// check if the current counter is still the same, otherwise this counter is irrelevant
	currentCounter, exists := h.getCurrentConnectionAttemptCounter(ski)
	if !exists || currentCounter != counter {
		return
	}

	// connection attempt is not relevant if the device is no longer paired
	// or it is not queued for pairing
	pairingState := h.ServiceForSKI(ski).ConnectionStateDetail().State()
	if !h.IsRemoteServiceForSKIPaired(ski) && pairingState != api.ConnectionStateQueued {
		return
	}

	// connection attempt is not relevant if the device is already connected
	if h.isSkiConnected(ski) {
		return
	}

	// now initiate the connection
	// check if the remoteService still exists
	service := h.ServiceForSKI(ski)

	if success := h.initateConnection(service, entry); !success {
		h.checkRestartMdnsSearch()
	}
}

// attempt to establish a connection to a remote service
// returns true if successful
func (h *HubImpl) initateConnection(remoteService *api.ServiceDetails, entry *api.MdnsEntry) bool {
	var err error

	// try connecting via an IP address first
	for _, address := range entry.Addresses {
		// connection attempt is not relevant if the device is no longer paired
		// or it is not queued for pairing
		pairingState := h.ServiceForSKI(remoteService.SKI()).ConnectionStateDetail().State()
		if !h.IsRemoteServiceForSKIPaired(remoteService.SKI()) && pairingState != api.ConnectionStateQueued {
			return false
		}

		logging.Log().Debug("trying to connect to", remoteService.SKI, "at", address)
		if err = h.connectFoundService(remoteService, address.String(), strconv.Itoa(entry.Port), entry.Path); err != nil {
			logging.Log().Debug("connection to", remoteService.SKI, "failed: ", err)
		} else {
			return true
		}
	}

	// connectdion via IP address failed, try hostname
	if len(entry.Host) > 0 {
		logging.Log().Debug("trying to connect to", remoteService.SKI, "at", entry.Host)
		if err = h.connectFoundService(remoteService, entry.Host, strconv.Itoa(entry.Port), entry.Path); err != nil {
			logging.Log().Debugf("connection to %s failed: %s", remoteService.SKI, err)
		} else {
			return true
		}
	}

	// no connection could be estabished via any of the provided addresses
	// because no service was reachable at any of the addresses
	return false
}

// increase the connection attempt counter for the given ski
func (h *HubImpl) increaseConnectionAttemptCounter(ski string) int {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	currentCounter := 0
	if counter, exists := h.connectionAttemptCounter[ski]; exists {
		currentCounter = counter + 1

		if currentCounter >= len(connectionInitiationDelayTimeRanges)-1 {
			currentCounter = len(connectionInitiationDelayTimeRanges) - 1
		}
	}

	h.connectionAttemptCounter[ski] = currentCounter

	return currentCounter
}

// remove the connection attempt counter for the given ski
func (h *HubImpl) removeConnectionAttemptCounter(ski string) {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	delete(h.connectionAttemptCounter, ski)
}

// get the current attempt counter
func (h *HubImpl) getCurrentConnectionAttemptCounter(ski string) (int, bool) {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	counter, exists := h.connectionAttemptCounter[ski]

	return counter, exists
}

// get the connection initiation delay time range for a given ski
// returns the current counter and the duration
func (h *HubImpl) getConnectionInitiationDelayTime(ski string) (int, time.Duration) {
	counter := h.increaseConnectionAttemptCounter(ski)

	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	timeRange := connectionInitiationDelayTimeRanges[counter]

	// get range in Milliseconds
	min := timeRange.min * 1000
	max := timeRange.max * 1000

	// #nosec G404
	duration := rand.Intn(max-min) + min

	return counter, time.Duration(duration) * time.Millisecond
}

// set if a connection attempt is running/in progress
func (h *HubImpl) setConnectionAttemptRunning(ski string, active bool) {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	h.connectionAttemptRunning[ski] = active
}

// return if a connection attempt is runnning/in progress
func (h *HubImpl) isConnectionAttemptRunning(ski string) bool {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	running, exists := h.connectionAttemptRunning[ski]
	if !exists {
		return false
	}

	return running
}
