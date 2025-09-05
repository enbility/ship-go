package hub

import (
	"errors"
	"net"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
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

// keepThisConnection prevents double connections
// only keep the connection initiated by the higher SKI
//
// returns true if this connection is fine to be continued
// returns false if this connection should not be established or kept
func (h *Hub) keepThisConnection(conn *websocket.Conn, incomingRequest bool, remoteService *api.ServiceDetails) bool {
	// SHIP 12.2.2 defines:
	// prevent double connections with SKI Comparison
	// the node with the higher SKI value keeps the most recent connection and
	// and closes all other connections to the same SHIP node
	//
	// This is hard to implement without any flaws. Therefore I chose a
	// different approach: The connection initiated by the higher SKI will be kept

	remoteSKI := remoteService.SKI()

	// Atomic check-and-action to prevent TOCTOU race conditions
	h.muxCon.Lock()
	existingC, exists := h.connections[remoteSKI]
	if !exists {
		h.muxCon.Unlock()
		return true
	}

	// Log the double connection scenario for diagnostics  
	logging.Log().Debug("double connection detected with remoteSKI", remoteSKI,
		"incoming", incomingRequest)

	keep := false
	localSKI := h.localService.SKI()
	if incomingRequest {
		keep = remoteSKI > localSKI
	} else {
		keep = localSKI > remoteSKI
	}

	if keep {
		// we have an existing connection
		// so keep the new (most recent) and close the old one
		// Atomically remove the old connection while holding the lock
		delete(h.connections, remoteSKI)
		h.muxCon.Unlock()

		logging.Log().Debug("closing existing double connection, keep the new one")
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

// sendWSCloseMessage sends a WebSocket close message
func (h *Hub) sendWSCloseMessage(conn *websocket.Conn) {
	err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "double connection"))
	if err != nil && !errors.Is(err, net.ErrClosed) {
		logging.Log().Debug("failed to send close message:", err)
	}
	<-time.After(time.Millisecond * 100)
	h.safeClose(conn, "websocket close")
}

// registerConnection registers a new SHIP connection
func (h *Hub) registerConnection(connection api.ShipConnectionInterface) {
	h.muxCon.Lock()
	defer h.muxCon.Unlock()

	ski := connection.RemoteSKI()
	h.connections[ski] = connection

	// Cancel any pending connection delay timer since connection succeeded
	h.cancelConnectionDelayTimer(ski)
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
