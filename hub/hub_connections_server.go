package hub

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/ship"
	"github.com/enbility/ship-go/util"
	"github.com/enbility/ship-go/ws"
	"github.com/gorilla/websocket"
)

// Port returns the websocket server's actual bound port, resolved once
// Start() has run - useful when the configured port was 0.
func (h *Hub) Port() int {
	h.muxPort.RLock()
	defer h.muxPort.RUnlock()

	return h.boundPort
}

// verifyPeerCertificate validates the peer certificate for WebSocket connections
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

	// SHIP 12.2.2: "The SHIP node with the greater SKI SHOULD check for double
	// connections directly during the TLS handshake." This is the earliest point at
	// which the peer is known, and it is early enough to retire an in-flight outgoing
	// connection attempt before this handshake completes.
	//
	// This hook never rejects the incoming connection - it only retires the older one.
	//
	// Note that "before the handshake completes" only holds under TLS 1.2, which SHIP 9
	// mandates: in TLS 1.3 the client is done as soon as it has sent its Finished, which
	// is before the server processes the client certificate and reaches this callback.
	// Against a peer that negotiates TLS 1.3 the older connection is still retired, just
	// not necessarily before the peer considers its handshake complete.
	remoteSKI := util.NormalizeSKI(certSKI)
	if resolveDoubleConnection(h.localService.SKI(), remoteSKI) == dcAdopt {
		h.supersedeForIncoming(remoteSKI)
	}

	return nil
}

// isDoubleConnectionRequest reports whether an incoming request comes from a SKI that
// already has a connection, i.e. whether it is a double connection in the sense of
// SHIP 12.2.2 rather than an additional peer.
func (h *Hub) isDoubleConnectionRequest(r *http.Request) bool {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return false
	}

	ski, err := cert.SkiFromCertificate(r.TLS.PeerCertificates[0])
	if err != nil {
		return false
	}

	return h.isSkiConnected(util.NormalizeSKI(ski))
}

// startWebsocketServer starts the SHIP websocket server
func (h *Hub) startWebsocketServer() error {
	addr := fmt.Sprintf(":%d", h.port)
	logging.Log().Debug("starting websocket server on", addr)

	// Listen synchronously so a bind failure (incl. port 0 -> OS-assigned)
	// surfaces immediately and the actual bound port is known before mDNS starts.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("websocket server failed to start: %w", err)
	}

	boundPort := ln.Addr().(*net.TCPAddr).Port
	h.muxPort.Lock()
	h.boundPort = boundPort
	h.muxPort.Unlock()
	if h.port == 0 {
		// OS-assigned port: mDNS must announce the real one, not 0.
		h.mdns.SetPort(boundPort)
	}

	h.httpServer = &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: time.Duration(time.Second * 10),
		TLSConfig: &tls.Config{
			Certificates:           []tls.Certificate{h.certificate},
			ClientAuth:             tls.RequireAnyClientCert, // SHIP 9: Client authentication is required
			CipherSuites:           cert.CipherSuites,        // #nosec G402 // SHIP 9.1: the ciphers are reported insecure but are defined to be used by SHIP
			VerifyPeerCertificate:  h.verifyPeerCertificate,
			MinVersion:             tls.VersionTLS12, // SHIP 9: Mandatory TLS version
			SessionTicketsDisabled: true,             // SHIP 9.6: Disable session resumption to prevent bypassing VerifyPeerCertificate
		},
	}

	serverStarted := h.serverStarted // capture for goroutine; prevents closing a replaced channel
	go func() {
		err := h.httpServer.ServeTLS(ln, "", "")
		if err != nil && err != http.ErrServerClosed {
			logging.Log().Error("websocket server error:", err)
			h.serverStartErr = err
		}
		close(serverStarted)
	}()

	return nil
}

// ServeHTTP handles incoming HTTP connection requests
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check connection limit before accepting new connections.
	//
	// A second connection from a SKI we are already connected to is exempt: it replaces
	// the existing one rather than adding to the total, and SHIP 12.2.2 requires such a
	// double connection to be resolved by SKI comparison, not rejected on capacity.
	h.muxCon.RLock()
	currentConnections := len(h.connections)
	maxConnections := h.maxConnections
	h.muxCon.RUnlock()

	if currentConnections >= maxConnections && !h.isDoubleConnectionRequest(r) {
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
		logConnectionError(err, "websocket upgrade failed:")
		return
	}

	// check if the client supports the ship sub protocol
	if conn.Subprotocol() != api.ShipWebsocketSubProtocol {
		logging.Log().Error("client does not support the ship sub protocol")
		h.safeClose(conn, "rejected connection")
		return
	}

	// check if the clients certificate provides a SKI
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		logging.Log().Error("client certificate validation failed: no certificate provided")
		h.safeClose(conn, "rejected connection")
		return
	}

	ski, err := cert.SkiFromCertificate(r.TLS.PeerCertificates[0])
	if err != nil {
		logConnectionError(err, "client certificate SKI extraction failed:")
		h.safeClose(conn, "rejected connection")
		return
	}

	fingerprint, err := cert.FingerprintFromCertificate(r.TLS.PeerCertificates[0])
	if err != nil {
		logConnectionError(err, "client certificate fingerprint extraction failed:")
		h.safeClose(conn, "rejected connection")
		return
	}

	// Log certificate expiration warnings (per SHIP spec 12.1.1)
	// This does not affect the connection - we log but still allow communication
	cert.LogCertificateExpiration(r.TLS.PeerCertificates[0], ski)

	// Set read limit to prevent DoS attacks
	conn.SetReadLimit(ws.MaxMessageSize)

	// normalize the incoming SKI
	ski = util.NormalizeSKI(ski)
	logging.Log().Debug("incoming connection request from", ski)

	// Build a candidate from what the TLS handshake just gave us and merge
	// it into the registry. This single call handles all four cases:
	// new entry, SKI-on-existing-fingerprint-only entry, fingerprint-on-
	// existing-SKI-only entry, and exact match.
	candidate, candErr := api.NewServiceDetails(ski, fingerprint, "")
	if candErr != nil {
		logging.Log().Error("incoming connection: invalid identifiers", "ski", ski, "error", candErr)
		h.safeClose(conn, "rejected connection")
		return
	}
	service, mergeErr := h.mergeOrAddService(candidate)
	if mergeErr != nil {
		logging.Log().Error("incoming connection rejected: identifier conflict",
			"ski", ski, "fingerprint", fingerprint, "error", mergeErr)
		h.safeClose(conn, "identifier conflict")
		return
	}

	connectionStateDetail := service.ConnectionStateDetail()
	if connectionStateDetail.State() == api.ConnectionStateQueued {
		connectionStateDetail.SetState(api.ConnectionStateReceivedPairingRequest)
		// Convert SKI to ServiceIdentity for callback
		pairingIdentity := service.ToServiceIdentity()
		h.hubReader.ServicePairingDetailUpdate(pairingIdentity, connectionStateDetail)
	}

	// SHIP 12.2.2: if this is a second connection to the same SKI, the node with the
	// bigger SKI keeps the most recent one and closes the others - which is what
	// registerConnection does when it displaces the older connection.
	if h.doubleConnectionAction(service.SKI()) == dcPark {
		logging.Log().Debug("double connection on the smaller-SKI side, keeping the most recent one for now", service.SKI())
	}

	h.startShipConnection(conn, service, ship.ShipRoleServer)
}
