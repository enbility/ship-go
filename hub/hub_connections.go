package hub

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/ship"
	"github.com/enbility/ship-go/ws"
	"github.com/gorilla/websocket"
)

// connectionDelayTimer manages a cancellable timer for connection delays
type connectionDelayTimer struct {
	timer *time.Timer
	done  chan struct{}
}

// newConnectionDelayTimer creates a new cancellable timer
func newConnectionDelayTimer(duration time.Duration, f func()) *connectionDelayTimer {
	cdt := &connectionDelayTimer{
		done: make(chan struct{}),
	}

	cdt.timer = time.AfterFunc(duration, func() {
		select {
		case <-cdt.done:
			// Timer was cancelled, don't run the function
			return
		default:
			// Timer not cancelled, run the function
			f()
		}
	})

	return cdt
}

// Stop cancels the timer if it hasn't fired yet
func (cdt *connectionDelayTimer) Stop() bool {
	if cdt.timer.Stop() {
		// Timer was stopped before firing
		close(cdt.done)
		return true
	}
	// Timer already fired or was stopped
	return false
}

// Websocket connection handling
func (h *Hub) verifyPeerCertificate(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	skiFound := false
	var validCert *x509.Certificate
	var certSKI string

	for _, v := range rawCerts {
		cerificate, err := x509.ParseCertificate(v)
		if err != nil {
			return err
		}

		if ski, err := cert.SkiFromCertificate(cerificate); err == nil {
			skiFound = true
			validCert = cerificate
			certSKI = ski
			break
		}
	}
	if !skiFound {
		return errors.New("no valid SKI provided in certificate")
	}

	// Log certificate expiration warnings (per SHIP spec 12.1.1)
	// This does not affect the connection - we log but still allow communication
	if validCert != nil {
		cert.LogCertificateExpiration(validCert, certSKI)
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
			// if the server doesn't start, we just log the error
			// instead we should think about how to handle this error and
			// get to a defined working state
		}
	}()

	return nil
}

// Connection Handling

// HTTP Server callback for handling incoming connection requests
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check connection limit before accepting new connections
	h.muxCon.RLock()
	currentConnections := len(h.connections)
	maxConnections := h.maxConnections
	h.muxCon.RUnlock()

	if currentConnections >= maxConnections {
		logging.Log().Debug("connection limit reached, rejecting new connection", currentConnections, maxConnections)
		http.Error(w, "Connection limit reached", http.StatusServiceUnavailable)
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin:  func(r *http.Request) bool { return true },
		Subprotocols: []string{api.ShipWebsocketSubProtocol}, // SHIP 10.2: Sub protocol "ship" is required
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

	// Log certificate expiration warnings (per SHIP spec 12.1.1)
	// This does not affect the connection - we log but still allow communication
	cert.LogCertificateExpiration(r.TLS.PeerCertificates[0], ski)

	// Set read limit to prevent DoS attacks
	conn.SetReadLimit(ws.MaxMessageSize)

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
	h.muxCon.RLock()
	defer h.muxCon.RUnlock()

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

	// Check connection limit before initiating new connection
	h.muxCon.RLock()
	currentConnections := len(h.connections)
	maxConnections := h.maxConnections
	h.muxCon.RUnlock()

	if currentConnections >= maxConnections {
		logging.Log().Debug("connection limit reached, not initiating new connection", currentConnections, maxConnections)
		return fmt.Errorf("connection limit reached (%d/%d)", currentConnections, maxConnections)
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

	hostPort := net.JoinHostPort(host, port)
	address := fmt.Sprintf("wss://%s%s", hostPort, path)
	conn, resp, err := dialer.Dial(address, nil)
	if err == nil {
		defer resp.Body.Close()
	} else {
		address = fmt.Sprintf("wss://%s", hostPort)
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

	// Log certificate expiration warnings (per SHIP spec 12.1.1)
	// This does not affect the connection - we log but still allow communication
	cert.LogCertificateExpiration(remoteCerts[0], remoteSKI)

	if !h.keepThisConnection(conn, false, remoteService) {
		errorString := fmt.Sprintf("closing connection to %s: ignoring this connection", remoteService.SKI())
		return errors.New(errorString)
	}

	// Set read limit to prevent DoS attacks
	conn.SetReadLimit(ws.MaxMessageSize)

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

	// Atomic check-and-action to prevent TOCTOU race conditions
	h.muxCon.Lock()
	existingC, exists := h.connections[remoteSKI]
	if !exists {
		h.muxCon.Unlock()
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
		// Atomically remove the old connection while holding the lock
		delete(h.connections, remoteSKI)
		h.muxCon.Unlock()

		logging.Log().Debug("closing existing double connection")
		// Close the old connection outside the lock to prevent deadlock
		go func(oldConn api.ShipConnectionInterface) {
			oldConn.CloseConnection(false, 0, "")
		}(existingC)
	} else {
		h.muxCon.Unlock()

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

	// Create a cancellable timer
	timer := newConnectionDelayTimer(duration, func() {
		h.prepareConnectionInitation(ski, counter, entry)
	})

	// Store the timer so it can be cancelled if needed
	h.storeConnectionDelayTimer(ski, timer)
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

	// try IPv4 addresses before IPv6 addresses
	entry.Addresses = h.sortIPAddresses(entry.Addresses)

	// try connecting via the provided IP addresses
	for _, address := range entry.Addresses {
		logging.Log().Debug("trying to connect to", remoteService.SKI(), "at", address)
		// IPv4
		addressValue := address.String()
		if address.To4() == nil {
			// IPv6
			addressValue = "[" + address.String() + "]"
		}
		if err = h.connectFoundService(remoteService, addressValue, strconv.Itoa(entry.Port), entry.Path); err != nil {
			logging.Log().Debug("connection to", remoteService.SKI(), "failed: ", err)
		} else {
			return true
		}
	}

	// no connection could be estabished via any of the provided addresses
	// because no service was reachable at any of the addresses
	return false
}

// try IPv4 addresses before IPv6 addresses
func (h *Hub) sortIPAddresses(addresses []net.IP) []net.IP {
	// Sort IP addresses to prefer IPv4 over IPv6
	slices.SortFunc(addresses, func(a, b net.IP) int {
		if a.To4() != nil && b.To4() == nil {
			return -1 // a is IPv4, b is IPv6
		}
		if a.To4() == nil && b.To4() != nil {
			return 1 // a is IPv6, b is IPv4
		}
		return 0 // both are either IPv4 or IPv6
	})

	return addresses
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
	h.muxConAttempt.RLock()
	defer h.muxConAttempt.RUnlock()

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
	minRange := timeRange.min * 1000
	maxRange := timeRange.max * 1000

	// #nosec G404
	duration := rand.Intn(maxRange-minRange) + minRange

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
	h.muxConAttempt.RLock()
	defer h.muxConAttempt.RUnlock()

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

	ski := connection.RemoteSKI()
	h.connections[ski] = connection

	// Cancel any pending connection delay timer since connection succeeded
	h.cancelConnectionDelayTimer(ski)
}

// return the connection for a specific SKI
func (h *Hub) connectionForSKI(ski string) api.ShipConnectionInterface {
	h.muxCon.RLock()
	defer h.muxCon.RUnlock()

	con, ok := h.connections[ski]
	if !ok {
		return nil
	}
	return con
}

// UnregisterConnectionIfMatch atomically unregisters a connection if it matches the provided one
// Returns true if the connection was unregistered, false otherwise
//
// This method prevents race conditions during connection cleanup where a connection
// could be replaced between the lookup and delete operations. The previous implementation
// would check the connection without holding the lock, then delete it in a separate
// operation, allowing a new connection to be registered and accidentally deleted.
//
// The atomic compare-and-delete ensures we only remove the specific connection instance
// that is being closed, not a newly registered replacement.
func (h *Hub) UnregisterConnectionIfMatch(ski string, conn api.ShipConnectionInterface) bool {
	h.muxCon.Lock()
	defer h.muxCon.Unlock()

	current, exists := h.connections[ski]
	if !exists || current != conn {
		return false
	}

	delete(h.connections, ski)
	return true
}

// storeConnectionDelayTimer stores a timer for a SKI, cancelling any existing timer
func (h *Hub) storeConnectionDelayTimer(ski string, timer *connectionDelayTimer) {
	h.muxTimers.Lock()
	defer h.muxTimers.Unlock()

	// Cancel any existing timer
	if existing, ok := h.connectionDelayTimers[ski]; ok {
		existing.Stop()
	}

	h.connectionDelayTimers[ski] = timer
}

// cancelConnectionDelayTimer cancels and removes a timer for a SKI
func (h *Hub) cancelConnectionDelayTimer(ski string) {
	h.muxTimers.Lock()
	defer h.muxTimers.Unlock()

	if timer, ok := h.connectionDelayTimers[ski]; ok {
		timer.Stop()
		delete(h.connectionDelayTimers, ski)
	}
}
