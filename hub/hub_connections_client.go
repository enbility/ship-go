package hub

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
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

// CertificateValidationResult represents the result of certificate validation
type CertificateValidationResult struct {
	Valid     bool
	RemoteSKI string
	Error     error
}

// validateConnectionLimit checks if a new connection can be established
// This is a pure function that's easy to test
func (h *Hub) validateConnectionLimit() error {
	h.muxCon.RLock()
	currentConnections := len(h.connections)
	maxConnections := h.maxConnections
	h.muxCon.RUnlock()

	if currentConnections >= maxConnections {
		logging.Log().Debug("connection limit reached, not initiating new connection", currentConnections, maxConnections)
		return fmt.Errorf("connection limit reached (%d/%d)", currentConnections, maxConnections)
	}
	return nil
}

// createWebSocketDialer creates a configured WebSocket dialer
// This is a pure function that's easy to test
func (h *Hub) createWebSocketDialer() *websocket.Dialer {
	return &websocket.Dialer{
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
}

// validateRemoteCertificate validates the remote certificate and returns the SKI
// This is a pure function that's easy to test
func validateRemoteCertificate(remoteCerts []*x509.Certificate, expectedSKI string) CertificateValidationResult {
	if len(remoteCerts) == 0 || remoteCerts[0].SubjectKeyId == nil {
		return CertificateValidationResult{
			Valid: false,
			Error: fmt.Errorf("no SKI in certificate"),
		}
	}

	remoteSKI, err := cert.SkiFromCertificate(remoteCerts[0])
	if err != nil {
		return CertificateValidationResult{
			Valid: false,
			Error: fmt.Errorf("invalid SKI format: %w", err),
		}
	}

	if remoteSKI != expectedSKI {
		return CertificateValidationResult{
			Valid: false,
			Error: fmt.Errorf("SKI mismatch: expected %s, got %s", expectedSKI, remoteSKI),
		}
	}

	// Log certificate expiration warnings (per SHIP spec 12.1.1)
	// This does not affect the connection - we log but still allow communication
	cert.LogCertificateExpiration(remoteCerts[0], remoteSKI)

	return CertificateValidationResult{
		Valid:     true,
		RemoteSKI: remoteSKI,
	}
}

// establishWebSocketConnection creates and establishes a WebSocket connection
// This is a focused function that handles the connection establishment details
func (h *Hub) establishWebSocketConnection(host, port, path string) (*websocket.Conn, error) {
	dialer := h.createWebSocketDialer()

	hostPort := net.JoinHostPort(host, port)
	address := fmt.Sprintf("wss://%s%s", hostPort, path)
	conn, resp, err := dialer.Dial(address, nil)
	if err == nil {
		defer resp.Body.Close()
		return conn, nil
	}

	// Try without path if the first attempt failed
	address = fmt.Sprintf("wss://%s", hostPort)
	conn, resp, err = dialer.Dial(address, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return conn, nil
}

// createShipConnection creates and initializes a SHIP connection
// This is a focused function that handles SHIP connection setup
func (h *Hub) createShipConnection(conn *websocket.Conn, remoteService *api.ServiceDetails) {
	// Set read limit to prevent DoS attacks
	conn.SetReadLimit(ws.MaxMessageSize)

	dataHandler := ws.NewWebsocketConnection(conn, remoteService.SKI())
	shipConnection := ship.NewConnectionHandler(h, dataHandler, ship.ShipRoleClient,
		h.localService.ShipID(), remoteService.SKI(), remoteService.ShipID())
	shipConnection.Run()

	h.registerConnection(shipConnection)
}

// connectFoundService establishes a connection to another EEBUS service
//
// returns error contains a reason for failing the connection or nil if no further tries should be processed
func (h *Hub) connectFoundService(remoteService *api.ServiceDetails, host, port, path string) error {
	if h.isSkiConnected(remoteService.SKI()) {
		return nil
	}

	// Check connection limit before initiating new connection
	if err := h.validateConnectionLimit(); err != nil {
		return err
	}

	logging.Log().Debugf("initiating connection to %s at %s:%s%s", remoteService.SKI(), host, port, path)

	// Establish WebSocket connection
	conn, err := h.establishWebSocketConnection(host, port, path)
	if err != nil {
		return err
	}

	// Validate remote certificate
	tlsConn := conn.UnderlyingConn().(*tls.Conn)
	remoteCerts := tlsConn.ConnectionState().PeerCertificates
	validationResult := validateRemoteCertificate(remoteCerts, remoteService.SKI())
	if !validationResult.Valid {
		errorString := fmt.Sprintf("certificate validation failed for %s: %s", remoteService.SKI(), validationResult.Error)
		h.safeClose(conn, "certificate validation failed")
		return errors.New(errorString)
	}

	// Check for double connections
	if !h.keepThisConnection(conn, false, remoteService) {
		errorString := fmt.Sprintf("closing connection to %s: ignoring this connection", remoteService.SKI())
		return errors.New(errorString)
	}

	// Create and setup SHIP connection
	h.createShipConnection(conn, remoteService)

	return nil
}

// shouldAttemptConnection checks if a connection attempt should be made
// This is a pure function that's easy to test
func (h *Hub) shouldAttemptConnection(remoteService *api.ServiceDetails) bool {
	// connection attempt is not relevant if the device is no longer paired
	// or it is not queued for pairing
	pairingState := h.ServiceForSKI(remoteService.SKI()).ConnectionStateDetail().State()
	return h.IsRemoteServiceForSKIPaired(remoteService.SKI()) || pairingState == api.ConnectionStateQueued
}

// tryConnectionViaHost attempts connection using hostname
// This is a focused function that handles hostname-based connection attempts
func (h *Hub) tryConnectionViaHost(remoteService *api.ServiceDetails, entry *api.MdnsEntry) bool {
	if len(entry.Host) == 0 {
		return false
	}

	logging.Log().Debugf("trying to connect to %s at %s", remoteService.SKI(), entry.Host)
	if err := h.connectFoundService(remoteService, entry.Host, strconv.Itoa(entry.Port), entry.Path); err != nil {
		logConnectionError(err, fmt.Sprintf("connection to %s at %s failed:", remoteService.SKI(), entry.Host))
		return false
	}
	return true
}

// formatIPAddress formats an IP address for connection (handles IPv6 brackets)
// This is a pure function that's easy to test
func formatIPAddress(address net.IP) string {
	if address.To4() == nil {
		// IPv6
		return "[" + address.String() + "]"
	}
	// IPv4
	return address.String()
}

// tryConnectionViaAddresses attempts connection using IP addresses
// This is a focused function that handles IP-based connection attempts
func (h *Hub) tryConnectionViaAddresses(remoteService *api.ServiceDetails, entry *api.MdnsEntry) bool {
	// try IPv4 addresses before IPv6 addresses
	entry.Addresses = h.sortIPAddresses(entry.Addresses)

	// try connecting via the provided IP addresses
	for _, address := range entry.Addresses {
		logging.Log().Debugf("trying to connect to %s at %s", remoteService.SKI(), address)
		addressValue := formatIPAddress(address)
		if err := h.connectFoundService(remoteService, addressValue, strconv.Itoa(entry.Port), entry.Path); err != nil {
			logConnectionError(err, fmt.Sprintf("connection to %s at %s failed:", remoteService.SKI(), addressValue))
		} else {
			return true
		}
	}
	return false
}

// initateConnection attempts to establish a connection to a remote service
// returns true if successful
func (h *Hub) initateConnection(remoteService *api.ServiceDetails, entry *api.MdnsEntry) bool {
	// Check if connection attempt should be made
	if !h.shouldAttemptConnection(remoteService) {
		return false
	}

	// Try connection via hostname first
	if h.tryConnectionViaHost(remoteService, entry) {
		return true
	}

	// Try connection via IP addresses
	if h.tryConnectionViaAddresses(remoteService, entry) {
		return true
	}

	// no connection could be established via any of the provided addresses
	// because no service was reachable at any of the addresses
	return false
}

// sortIPAddresses sorts IP addresses to prefer IPv4 over IPv6
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
