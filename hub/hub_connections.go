package hub

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/ship"
	"github.com/enbility/ship-go/ws"
	"github.com/gorilla/websocket"
)

// Websocket connection handling
func (h *Hub) verifyPeerCertificate(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
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
func (h *Hub) startWebsocketServer() error {
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
			logging.Log().Error("websocket server error:", err)
			// TODO: decide how to handle this case
		}
	}()

	return nil
}

// Connection Handling

// HTTP Server callback for handling incoming connection requests
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	logging.Log().Debug("incoming connection request from", remoteService.SKI())

	// Check if the remote service is paired
	service := h.ServiceForSKI(remoteService.SKI())
	connectionStateDetail := service.ConnectionStateDetail()
	if connectionStateDetail.State() == api.ConnectionStateQueued {
		connectionStateDetail.SetState(api.ConnectionStateReceivedPairingRequest)
		h.hubReader.ServicePairingDetailUpdate(ski, connectionStateDetail)
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

// return if there is a connection for a SKI
func (h *Hub) isSkiConnected(ski string) bool {
	h.muxCon.Lock()
	defer h.muxCon.Unlock()

	// The connection with the higher SKI should retain the connection
	_, ok := h.connections[ski]
	return ok
}

// Connect to another EEBUS service
//
// returns error contains a reason for failing the connection or nil if no further tries should be processed
func (h *Hub) connectFoundService(remoteService *api.ServiceDetails, host, port, path string) error {
	if h.isSkiConnected(remoteService.SKI()) {
		return nil
	}

	logging.Log().Debugf("initiating connection to %s at %s:%s%s", remoteService.SKI(), host, port, path)

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
	conn, resp, err := dialer.Dial(address, nil)
	if err == nil {
		defer resp.Body.Close()
	} else {
		address = fmt.Sprintf("wss://%s:%s", host, port)
		conn, resp, err = dialer.Dial(address, nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
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
func (h *Hub) keepThisConnection(conn *websocket.Conn, incomingRequest bool, remoteService *api.ServiceDetails) bool {
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
			go h.sendWSCloseMessage(conn)
		}
	}

	return keep
}

func (h *Hub) sendWSCloseMessage(conn *websocket.Conn) {
	_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "double connection"))
	<-time.After(time.Millisecond * 100)
	_ = conn.Close()
}

// coordinate connection initiation attempts to a remove service
func (h *Hub) coordinateConnectionInitations(ski string, entry *api.MdnsEntry) {
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
func (h *Hub) prepareConnectionInitation(ski string, counter int, entry *api.MdnsEntry) {
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
		h.checkAutoReannounce()
	}
}

// attempt to establish a connection to a remote service
// returns true if successful
func (h *Hub) initateConnection(remoteService *api.ServiceDetails, entry *api.MdnsEntry) bool {
	var err error

	// connection attempt is not relevant if the device is no longer paired
	// or it is not queued for pairing
	pairingState := h.ServiceForSKI(remoteService.SKI()).ConnectionStateDetail().State()
	if !h.IsRemoteServiceForSKIPaired(remoteService.SKI()) && pairingState != api.ConnectionStateQueued {
		return false
	}

	// try connetion via hostname
	if len(entry.Host) > 0 {
		logging.Log().Debug("trying to connect to", remoteService.SKI(), "at", entry.Host)
		if err = h.connectFoundService(remoteService, entry.Host, strconv.Itoa(entry.Port), entry.Path); err != nil {
			logging.Log().Debugf("connection to %s failed: %s", remoteService.SKI(), err)
		} else {
			return true
		}
	}

	// try connecting via the provided IP addresses
	for _, address := range entry.Addresses {
		logging.Log().Debug("trying to connect to", remoteService.SKI(), "at", address)
		if err = h.connectFoundService(remoteService, address.String(), strconv.Itoa(entry.Port), entry.Path); err != nil {
			logging.Log().Debug("connection to", remoteService.SKI(), "failed: ", err)
		} else {
			return true
		}
	}

	// no connection could be estabished via any of the provided addresses
	// because no service was reachable at any of the addresses
	return false
}

// increase the connection attempt counter for the given ski
func (h *Hub) increaseConnectionAttemptCounter(ski string) int {
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
func (h *Hub) removeConnectionAttemptCounter(ski string) {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	delete(h.connectionAttemptCounter, ski)
}

// get the current attempt counter
func (h *Hub) getCurrentConnectionAttemptCounter(ski string) (int, bool) {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	counter, exists := h.connectionAttemptCounter[ski]

	return counter, exists
}

// get the connection initiation delay time range for a given ski
// returns the current counter and the duration
func (h *Hub) getConnectionInitiationDelayTime(ski string) (int, time.Duration) {
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
func (h *Hub) setConnectionAttemptRunning(ski string, active bool) {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	h.connectionAttemptRunning[ski] = active
}

// return if a connection attempt is runnning/in progress
func (h *Hub) isConnectionAttemptRunning(ski string) bool {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	running, exists := h.connectionAttemptRunning[ski]
	if !exists {
		return false
	}

	return running
}

// register a new ship Connection
func (h *Hub) registerConnection(connection api.ShipConnectionInterface) {
	h.muxCon.Lock()
	defer h.muxCon.Unlock()

	h.connections[connection.RemoteSKI()] = connection
}

// return the connection for a specific SKI
func (h *Hub) connectionForSKI(ski string) api.ShipConnectionInterface {
	h.muxCon.Lock()
	defer h.muxCon.Unlock()

	con, ok := h.connections[ski]
	if !ok {
		return nil
	}
	return con
}
