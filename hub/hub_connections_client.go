package hub

import (
	"crypto/tls"
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

// connectFoundService establishes a connection to another EEBUS service
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
		errorString := fmt.Sprintf("certificate validation failed for %s: no SKI in certificate", remoteService.SKI())
		h.safeClose(conn, "certificate validation failed")
		return errors.New(errorString)
	}

	if _, err := cert.SkiFromCertificate(remoteCerts[0]); err != nil {
		// Close connection as the remote SKI can't be correct
		errorString := fmt.Sprintf("certificate validation failed for %s: %s", remoteService.SKI(), err)
		h.safeClose(conn, "certificate validation failed")
		return errors.New(errorString)
	}

	remoteSKI := fmt.Sprintf("%0x", remoteCerts[0].SubjectKeyId)

	if remoteSKI != remoteService.SKI() {
		errorString := fmt.Sprintf("certificate SKI mismatch: expected %s, got %s", remoteService.SKI(), remoteSKI)
		h.safeClose(conn, "certificate validation failed")
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

// initateConnection attempts to establish a connection to a remote service
// returns true if successful
func (h *Hub) initateConnection(remoteService *api.ServiceDetails, entry *api.MdnsEntry) bool {
	var err error

	// connection attempt is not relevant if the device is no longer paired
	// or it is not queued for pairing
	pairingState := h.ServiceForSKI(remoteService.SKI()).ConnectionStateDetail().State()
	if !h.IsRemoteServiceForSKIPaired(remoteService.SKI()) && pairingState != api.ConnectionStateQueued {
		return false
	}

	// try connection via hostname
	if len(entry.Host) > 0 {
		logging.Log().Debug("trying to connect to", remoteService.SKI(), "at", entry.Host)
		if err = h.connectFoundService(remoteService, entry.Host, strconv.Itoa(entry.Port), entry.Path); err != nil {
			logConnectionError(err, fmt.Sprintf("connection to %s at %s failed:", remoteService.SKI(), entry.Host))
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
			logConnectionError(err, fmt.Sprintf("connection to %s at %s failed:", remoteService.SKI(), addressValue))
		} else {
			return true
		}
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