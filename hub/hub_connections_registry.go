package hub

import (
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/model"
	"github.com/enbility/ship-go/ship"
	"github.com/enbility/ship-go/ws"
	"github.com/gorilla/websocket"
)

// isSkiConnected returns if there is a connection for a SKI
func (h *Hub) isSkiConnected(ski string) bool {
	h.muxCon.RLock()
	defer h.muxCon.RUnlock()

	// The connection with the higher SKI should retain the connection
	_, ok := h.connections[ski]
	return ok
}

// doubleConnectionAction is what to do with a newly established connection when
// another connection to the same SKI already exists.
type doubleConnectionAction int

const (
	// dcAdopt: take this connection and retire everything else for that SKI.
	dcAdopt doubleConnectionAction = iota
	// dcPark: hold this connection back and leave the existing one in place, waiting
	// for the peer to resolve the duplicate.
	//
	// TODO(#96): not implemented yet - the call sites keep the most recent connection
	// here too. That is not what SHIP 12.2.2 asks of the smaller-SKI node, but it is
	// strictly closer to it than closing the newer connection, which is what this code
	// used to do and which leaves a compliant peer pair with nothing connected. It
	// agrees with a compliant peer in every ordering except a true simultaneous
	// crossing, where the two ends can disagree on which connection is the most recent.
	// Parking removes that last case; it is a separate change because nothing in the
	// SHIP TestSpecification exercises the smaller-SKI side.
	dcPark
)

// resolveDoubleConnection implements the SKI comparison of SHIP 12.2.2:
//
// "the SHIP node with the bigger 160 bit SKI value SHALL only keep the most recent
// connection open and close all other connections to the same SHIP node [...] each
// SHIP node may close a connection - even the SHIP node with the smaller SKI value -
// if a timeout was detected or the SHIP node with the bigger SKI value does not close
// double or multiple connections to the same SHIP node within 3 seconds."
//
// So the bigger SKI decides immediately and the smaller SKI waits. Which side opened
// which connection does not enter into it — "most recent" is the criterion, and it is
// only well defined because the smaller-SKI node stays passive while the bigger-SKI
// node resolves.
func resolveDoubleConnection(localSKI, remoteSKI string) doubleConnectionAction {
	if localSKI > remoteSKI {
		return dcAdopt
	}
	return dcPark
}

// doubleConnectionAction returns what to do with a new connection for a SKI. A
// connection is only a double connection if one is already registered.
func (h *Hub) doubleConnectionAction(remoteSKI string) doubleConnectionAction {
	h.muxCon.RLock()
	_, exists := h.connections[remoteSKI]
	h.muxCon.RUnlock()

	if !exists {
		return dcAdopt
	}

	action := resolveDoubleConnection(h.localService.SKI(), remoteSKI)
	logging.Log().Debug("double connection detected with remoteSKI", remoteSKI,
		"action", map[doubleConnectionAction]string{dcAdopt: "adopt", dcPark: "park"}[action])
	return action
}

// supersedeForIncoming retires an in-flight outgoing dial to the same SKI because an
// incoming connection has taken over. Called from the TLS handshake of the incoming
// connection (SHIP 12.2.2: "The SHIP node with the greater SKI SHOULD check for double
// connections directly during the TLS handshake"), so that the older connection is gone
// before the new handshake completes.
//
// A registered connection is not touched here: it is displaced atomically when the new
// connection registers, which keeps the registry from ever passing through an empty
// state for that SKI.
func (h *Hub) supersedeForIncoming(remoteSKI string) {
	h.muxCon.RLock()
	dial := h.connectionsInitiating[remoteSKI]
	h.muxCon.RUnlock()

	if dial == nil {
		return
	}

	logging.Log().Debug("retiring in-flight connection attempt, an incoming connection took over", remoteSKI)
	dial.supersede()
}

// supersedeGracePeriod is how long a retired outgoing dial waits for the incoming
// connection that took it over to register. Registration happens right after the
// websocket upgrade, so this only has to cover the rest of that handshake.
const supersedeGracePeriod = 2 * time.Second

// awaitSupersedingConnection waits for the incoming connection that retired an outgoing
// dial to establish itself.
//
// Retiring the dial is decided during the TLS handshake, before the incoming connection
// has proven it can get any further: it can still fail the subprotocol or certificate
// checks in ServeHTTP, lose an identifier conflict, or simply be dropped by the peer.
// Reporting the dial as a success in that case would leave the SKI with no connection and
// nothing scheduled to fix it, so the caller has to be told the attempt failed.
func (h *Hub) awaitSupersedingConnection(remoteSKI string) bool {
	deadline := time.Now().Add(supersedeGracePeriod)

	for {
		if h.isSkiConnected(remoteSKI) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// retire closes a connection that lost a double connection resolution.
//
// SHIP 12.2.2: "If an older connection is already in the SME data exchange phase, the
// SHIP node with the bigger SKI value SHOULD initiate a connection termination as
// described in section 13.4.7." The reason for that announce is "unspecific" — per SHIP
// 13.4.7.1.1 "removedConnection" means the node was removed from the list of known
// devices, which is not the case here.
func (h *Hub) retire(connection api.ShipConnectionInterface) {
	if connection == nil {
		return
	}

	go func() {
		if state, _ := connection.ShipHandshakeState(); state == model.SmeStateComplete {
			connection.CloseConnection(true, 0, string(model.ConnectionCloseReasonTypeUnspecific))
			return
		}
		connection.CloseConnection(false, 4001, "double connection")
	}()
}

// registerConnection registers a new SHIP connection, retiring any connection it
// displaces (SHIP 12.2.2: the most recent connection is the one that is kept).
//
// The swap is atomic: the registry never passes through a state where the SKI has no
// connection, so a displaced connection can tell it was replaced rather than
// disconnected — see HandleConnectionClosed.
func (h *Hub) registerConnection(connection api.ShipConnectionInterface) {
	h.muxCon.Lock()

	if h.shutDown {
		h.muxCon.Unlock()
		connection.CloseConnection(false, 0, "hub shutdown")
		return
	}

	// A websocket is marked closed before HandleConnectionClosed runs, so a connection that closed
	// before this point was not found there and must not be registered now.
	if c, ok := connection.(*ship.ShipConnection); ok {
		if closed, _ := c.DataHandler().IsDataConnectionClosed(); closed {
			h.muxCon.Unlock()
			return
		}
	}

	ski := connection.RemoteSKI()
	displaced := h.connections[ski]
	if displaced == connection {
		displaced = nil
	}
	h.connections[ski] = connection

	// Cancel any pending connection delay timer since connection succeeded
	h.cancelConnectionDelayTimer(ski)
	h.muxCon.Unlock()

	if displaced != nil {
		logging.Log().Debug("closing existing double connection, keep the new one", ski)
		h.retire(displaced)
	}
}

// startShipConnection runs the SHIP handshake on an established websocket connection
// and registers it.
func (h *Hub) startShipConnection(conn *websocket.Conn, service *api.ServiceDetails, role ship.ShipRole) {
	conn.SetPongHandler(nil)
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		logging.Log().Debug("failed to reset read deadline", err)
	}
	conn.SetReadLimit(ws.MaxMessageSize)

	dataHandler := ws.NewWebsocketConnection(conn, service.SKI())
	shipConnection := ship.NewConnectionHandler(h, dataHandler, role,
		h.localService.ShipID(), service.SKI(), service.ShipID())
	shipConnection.Run()

	h.registerConnection(shipConnection)
}

// connectionForService finds an active connection for a given service using multiple identifiers.
// This method provides robust connection lookup that handles the AddCu device identifier lifecycle
// where SKI might be empty initially but fingerprint and shipID are available.
//
// Lookup priority:
// 1. SKI (fastest - direct map lookup)
// 2. Fingerprint match (reliable for AddCu devices)
// 3. ShipID match (fallback for identifier consistency)
//
// Parameters:
// - service: ServiceDetails object with available identifiers
//
// Returns:
// - ShipConnectionInterface: Active connection if found, nil otherwise
//
// Thread-safe: Uses read lock for safe concurrent access
//
// Example usage:
//
//	service := api.NewServiceDetails("", fingerprint, shipID) // AddCu from persistence
//	if conn := hub.connectionForService(service); conn != nil {
//	    // Device is connected
//	}
func (h *Hub) connectionForService(service *api.ServiceDetails) api.ShipConnectionInterface {
	if service == nil {
		return nil
	}

	h.muxCon.RLock()
	defer h.muxCon.RUnlock()

	// Fast path: Direct SKI lookup (most common case)
	if ski := service.SKI(); ski != "" {
		if conn, exists := h.connections[ski]; exists {
			return conn
		}
	}

	// Fallback path: Iterate connections for fingerprint/shipID match
	// This handles AddCu devices where SKI might be empty at startup
	targetFingerprint := service.Fingerprint()
	targetShipID := service.ShipID()

	if targetFingerprint == "" && targetShipID == "" {
		return nil // No fallback identifiers available
	}

	for connSKI, conn := range h.connections {
		// Get the service details for this connection's remote service
		// Use thread-safe service lookup to get connection's service details
		h.muxReg.RLock()
		var connService *api.ServiceDetails
		for _, remoteService := range h.remoteServices {
			if remoteService.SKI() == connSKI {
				connService = remoteService
				break
			}
		}
		h.muxReg.RUnlock()

		if connService == nil {
			continue
		}

		// Match by fingerprint (reliable for AddCu devices)
		if targetFingerprint != "" && connService.Fingerprint() == targetFingerprint {
			return conn
		}

		// Match by shipID (fallback for consistency)
		if targetShipID != "" && connService.ShipID() == targetShipID {
			return conn
		}
	}

	return nil
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
