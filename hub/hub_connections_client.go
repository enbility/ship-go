package hub

import (
	"context"
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
	"github.com/gorilla/websocket"
)

// CertificateValidationResult represents the result of certificate validation
type CertificateValidationResult struct {
	Valid             bool
	RemoteSKI         string
	RemoteFingerprint string
	Error             error
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

// errCertificateRejected marks a dial that failed because the peer's certificate was
// rejected during the TLS handshake (SHIP 12.2, SHIP-TS-SEC-01/02).
var errCertificateRejected = errors.New("peer certificate rejected during TLS handshake")

// createWebSocketDialer creates a configured WebSocket dialer.
//
// remoteService carries the SKI and/or fingerprint trusted for the peer being dialled. The peer's
// certificate is verified against them during the TLS handshake, so a peer that fails is never
// admitted to the connection; without them, every certificate is rejected.
//
// When a dialState is passed, the raw socket of each attempt is handed to it so that an
// incoming connection to the same SKI can abort this attempt synchronously — see
// dialState and SHIP 12.2.2.
func (h *Hub) createWebSocketDialer(state *dialState, remoteService *api.ServiceDetails) *websocket.Dialer {
	dialer := &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 5 * time.Second,
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{h.certificate},
			// SHIP 12.1: all certificates are locally signed
			InsecureSkipVerify: true, // #nosec G402
			// SHIP 9.1: the ciphers are reported insecure but are defined to be used by SHIP
			CipherSuites: cert.CipherSuites, // #nosec G402

			// SHIP 12.2 / EEBus SHIP TestSpec SHIP-TS-SEC-01+02 (TC_SHIP_SEC_001 §4.4.1,
			// TC_SHIP_SEC_002 §4.4.2): a spoofed certificate - SKI field != SHA-1(public key),
			// or not matching the SKI or fingerprint trusted for this peer - must be rejected by
			// aborting the TLS handshake. crypto/tls calls this hook while processing the server
			// Certificate message, so an error here sends a bad_certificate alert before
			// gorilla/websocket writes "GET /ship/". Running the same check after the upgrade
			// (connectFoundService) is too late: the handshake has then already reached
			// "101 Switching Protocols".
			// The fingerprint matters as much as the SKI: after SHIP Pairing (parType=fpSha256)
			// the SKI of the trusted entry is taken from the mDNS announcement (hub_mdns.go), so
			// only the fingerprint authenticates the peer.
			// InsecureSkipVerify above is not a weakening - SHIP 12.1 certificates are
			// self-signed, so there is no chain to validate and this hook is the trust decision.
			VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				var expectedSKI, expectedFingerprint string
				if remoteService != nil {
					expectedSKI, expectedFingerprint = remoteService.SKI(), remoteService.Fingerprint()
				}
				// without a trusted SKI or fingerprint there is nothing to verify the peer against
				if expectedSKI == "" && expectedFingerprint == "" {
					return fmt.Errorf("%w: no trusted SKI or fingerprint for the peer", errCertificateRejected)
				}

				certs := make([]*x509.Certificate, 0, len(rawCerts))
				for _, raw := range rawCerts {
					parsed, err := x509.ParseCertificate(raw)
					if err != nil {
						return fmt.Errorf("%w: %w", errCertificateRejected, err)
					}
					certs = append(certs, parsed)
				}

				if result := validateRemoteCertificate(certs, expectedSKI, expectedFingerprint); !result.Valid {
					return fmt.Errorf("%w: %w", errCertificateRejected, result.Error)
				}
				return nil
			},
		},
		Subprotocols: []string{api.ShipWebsocketSubProtocol},
	}

	if state != nil {
		dialer.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			state.adopt(conn)
			return conn, nil
		}
	}

	return dialer
}

// validateRemoteCertificate validates the remote certificate and returns the SKI
// This is a pure function that's easy to test
func validateRemoteCertificate(remoteCerts []*x509.Certificate, expectedSKI, expectedFingerprint string) CertificateValidationResult {
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

	if expectedSKI != "" && remoteSKI != expectedSKI {
		return CertificateValidationResult{
			Valid: false,
			Error: fmt.Errorf("SKI mismatch: expected %s, got %s", expectedSKI, remoteSKI),
		}
	}

	remoteFingerprint, err := cert.FingerprintFromCertificate(remoteCerts[0])
	if err != nil {
		return CertificateValidationResult{
			Valid: false,
			Error: fmt.Errorf("invalid fingerprint format: %w", err),
		}
	}

	if expectedFingerprint != "" && remoteFingerprint != expectedFingerprint {
		return CertificateValidationResult{
			Valid: false,
			Error: fmt.Errorf("fingerprint mismatch: expected %s, got %s", expectedFingerprint, remoteFingerprint),
		}
	}

	// Log certificate expiration warnings (per SHIP spec 12.1.1)
	// This does not affect the connection - we log but still allow communication
	cert.LogCertificateExpiration(remoteCerts[0], remoteSKI)

	return CertificateValidationResult{
		Valid:             true,
		RemoteSKI:         remoteSKI,
		RemoteFingerprint: remoteFingerprint,
	}
}

// establishWebSocketConnection creates and establishes a WebSocket connection
// This is a focused function that handles the connection establishment details
func (h *Hub) establishWebSocketConnection(host, port, path string, state *dialState, remoteService *api.ServiceDetails) (*websocket.Conn, error) {
	dialer := h.createWebSocketDialer(state, remoteService)

	hostPort := net.JoinHostPort(host, port)
	address := fmt.Sprintf("wss://%s%s", hostPort, path)
	conn, resp, err := dialer.Dial(address, nil)
	if err == nil {
		defer resp.Body.Close()
		return conn, nil
	}

	// an incoming connection took this attempt over, so do not open a second socket
	if state != nil && state.wasSuperseded() {
		return nil, err
	}

	// A rejected certificate is a property of the peer, not of the URL path - retrying
	// without the path would only repeat the same failed handshake.
	if errors.Is(err, errCertificateRejected) {
		return nil, err
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
	h.startShipConnection(conn, remoteService, ship.ShipRoleClient)
}

// connectFoundService establishes a connection to another EEBUS service
//
// returns error contains a reason for failing the connection or nil if no further tries should be processed
func (h *Hub) connectFoundService(remoteService *api.ServiceDetails, host, port, path string) error {
	// Atomically skip if this SKI is already connected or a concurrent outgoing
	// dial is already in flight. registerConnection only marks the SKI connected
	// after the websocket dial and SHIP handshake, so without an in-flight guard
	// two concurrent outgoing dials to the same SKI can both pass this check,
	// both establish, and then displace each other in the registry.
	state := newDialState()
	if ski := remoteService.SKI(); ski != "" {
		h.muxCon.Lock()
		if _, connected := h.connections[ski]; connected || h.connectionsInitiating[ski] != nil {
			h.muxCon.Unlock()
			return nil
		}
		h.connectionsInitiating[ski] = state
		h.muxCon.Unlock()

		defer func() {
			h.muxCon.Lock()
			delete(h.connectionsInitiating, ski)
			h.muxCon.Unlock()
		}()
	}

	// Check connection limit before initiating new connection
	if err := h.validateConnectionLimit(); err != nil {
		return err
	}

	logging.Log().Debugf("initiating connection to %s at %s:%s%s", remoteService.SKI(), host, port, path)

	// Establish WebSocket connection
	conn, err := h.establishWebSocketConnection(host, port, path, state, remoteService)
	if err != nil {
		// SHIP 12.2.2: an incoming connection took this attempt over while it was in
		// flight. Not a failure - no retry, no backoff - as long as that connection
		// actually establishes itself.
		if state.wasSuperseded() {
			return h.supersededDialResult(remoteService.SKI())
		}
		return err
	}

	// Already validated during the TLS handshake; this also yields the remote identifiers used below
	tlsConn := conn.UnderlyingConn().(*tls.Conn)
	remoteCerts := tlsConn.ConnectionState().PeerCertificates
	validationResult := validateRemoteCertificate(remoteCerts, remoteService.SKI(), remoteService.Fingerprint())
	if !validationResult.Valid {
		errorString := fmt.Sprintf("certificate validation failed for %s: %s", remoteService.SKI(), validationResult.Error)
		h.safeClose(conn, "certificate validation failed")
		return errors.New(errorString)
	}

	// Update service identifiers if they were empty (e.g., from SKI-only registration)
	if remoteService.SKI() == "" && validationResult.RemoteSKI != "" && remoteService.Fingerprint() == validationResult.RemoteFingerprint {
		remoteService.SetSKI(validationResult.RemoteSKI)
	}
	// Update fingerprint if it was empty (e.g., from SKI-only registration)
	if remoteService.Fingerprint() == "" && validationResult.RemoteFingerprint != "" {
		remoteService.SetFingerprint(validationResult.RemoteFingerprint)
	}
	// Now that the cert has revealed the remote SKI and fingerprint, fold
	// any other registry entry that matches by these identifiers — this
	// prevents an orphan untrusted entry from shadowing the trusted one.
	if merged, mergeErr := h.mergeOrAddService(remoteService); mergeErr == nil {
		remoteService = merged
	} else {
		logging.Log().Error("outgoing connection: identifier conflict during merge",
			"ski", remoteService.SKI(), "fingerprint", remoteService.Fingerprint(), "error", mergeErr)
	}

	// SHIP 12.2.2: an incoming connection took over while we were dialling. The
	// handshake-time check already decided this connection loses.
	if state.wasSuperseded() {
		h.safeClose(conn, "superseded by incoming connection")
		return h.supersededDialResult(remoteService.SKI())
	}

	// SHIP 12.2.2: if a connection to this SKI is already registered, the bigger SKI
	// keeps the most recent one - which is what registerConnection does when it
	// displaces the older connection.
	if h.doubleConnectionAction(remoteService.SKI()) == dcPark {
		logging.Log().Debug("double connection on the smaller-SKI side, keeping the most recent one for now", remoteService.SKI())
	}

	// Create and setup SHIP connection
	h.createShipConnection(conn, remoteService)

	return nil
}

// supersededDialResult reports how an outgoing dial that was retired for an incoming
// connection ended: nil once that connection is registered, an error if it never got
// there, so the usual retry path picks the SKI back up.
func (h *Hub) supersededDialResult(remoteSKI string) error {
	if h.awaitSupersedingConnection(remoteSKI) {
		logging.Log().Debug("connection attempt superseded by an incoming connection", remoteSKI)
		return nil
	}

	return fmt.Errorf("connection attempt to %s was superseded by an incoming connection that did not establish", remoteSKI)
}

// shouldAttemptConnection checks if a connection attempt should be made
// This is a pure function that's easy to test
func (h *Hub) shouldAttemptConnection(remoteService *api.ServiceDetails) bool {
	// connection attempt is not relevant if the device is no longer paired
	// or it is not queued for pairing
	service := h.ServiceForIdentifier(remoteService.SKI(), remoteService.Fingerprint())
	if service == nil {
		return false
	}

	return h.IsRemoteServiceForSKIPaired(remoteService.SKI())
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

// tryConnectionViaAddresses attempts connection using IP addresses
// This is a focused function that handles IP-based connection attempts
func (h *Hub) tryConnectionViaAddresses(remoteService *api.ServiceDetails, entry *api.MdnsEntry) bool {
	// try IPv4 addresses before IPv6 addresses
	entry.Addresses = h.sortIPAddresses(entry.Addresses)

	// try connecting via the provided IP addresses
	for _, address := range entry.Addresses {
		logging.Log().Debugf("trying to connect to %s at %s", remoteService.SKI(), address)
		addressValue := address.String()
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
