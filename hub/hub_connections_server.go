package hub

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
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

	return nil
}

// startWebsocketServer starts the SHIP websocket server
func (h *Hub) startWebsocketServer() error {
	addr := fmt.Sprintf(":%d", h.port)
	logging.Log().Debug("starting websocket server on", addr)

	h.httpServer = &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: time.Duration(time.Second * 10),
		TLSConfig: &tls.Config{
			Certificates:          []tls.Certificate{h.certificate},
			ClientAuth:            tls.RequireAnyClientCert, // SHIP 9: Client authentication is required
			CipherSuites:          cert.CipherSuites,        // #nosec G402 // SHIP 9.1: the ciphers are reported insecure but are defined to be used by SHIP
			VerifyPeerCertificate: h.verifyPeerCertificate,
			MinVersion:            tls.VersionTLS12, // SHIP 9: Mandatory TLS version
		},
	}

	go func() {
		err := h.httpServer.ListenAndServeTLS("", "")
		if err != nil && err != http.ErrServerClosed {
			logging.Log().Error("websocket server error:", err)
			h.serverStartErr = err
		}
		close(h.serverStarted)
	}()

	return nil
}

// ServeHTTP handles incoming HTTP connection requests
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

	// Check if the remote service is paired
	service := h.ServiceForIdentifier(ski, fingerprint)
	if service == nil {
		// Create new service if not found at all
		service = api.NewServiceDetails(ski, fingerprint, "")
		h.addService(service)
	} else if service.SKI() != ski && service.Fingerprint() == fingerprint {
		// Update the service with the actual SKI from the connection
		service.SetSKI(ski)
	} else if service.SKI() == ski && service.Fingerprint() == "" {
		// Update fingerprint if it was empty (e.g., from SKI-only registration)
		service.SetFingerprint(fingerprint)
	}

	connectionStateDetail := service.ConnectionStateDetail()
	if connectionStateDetail.State() == api.ConnectionStateQueued {
		connectionStateDetail.SetState(api.ConnectionStateReceivedPairingRequest)
		// Convert SKI to ServiceIdentity for callback
		pairingIdentity := api.SKIToServiceIdentity(ski)
		h.hubReader.ServicePairingDetailUpdate(pairingIdentity, connectionStateDetail)
	}

	// don't allow a second connection
	if !h.keepThisConnection(conn, true, service) {
		h.safeClose(conn, "double connection rejected")
		return
	}

	dataHandler := ws.NewWebsocketConnection(conn, service.SKI())
	shipConnection := ship.NewConnectionHandler(h, dataHandler, ship.ShipRoleServer,
		h.localService.ShipID(), service.SKI(), service.ShipID())
	shipConnection.Run()

	h.registerConnection(shipConnection)
}
