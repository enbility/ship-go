package hub

import (
	"errors"
	"net"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/gorilla/websocket"
)

// This file holds thin Hub-method wrappers around connectionRegistry. The
// previous hand-rolled keepThisConnection / registerConnection / isSkiConnected
// have been replaced by registry.Swap / registry.IsConnected — see
// hub/connection_registry.go for the implementation and the §12.2.2 rule.

// isSkiConnected returns true iff a live connection is registered for the SKI.
// Stale (non-alive) entries report false so reconnects can proceed.
func (h *Hub) isSkiConnected(ski string) bool {
	return h.registry.IsConnected(ski)
}

// connectionForSKI returns the connection for a specific SKI, or nil. No
// liveness check — callers needing liveness use isSkiConnected.
func (h *Hub) connectionForSKI(ski string) api.ShipConnectionInterface {
	return h.registry.Get(ski)
}

// UnregisterConnectionIfMatch atomically unregisters a connection iff it
// matches the registered one. Returns whether the removal happened.
//
// Used by HandleConnectionClosed to avoid removing a replacement connection
// when the closing one was already evicted by a double-connection swap.
//
// Public for backward compatibility.
func (h *Hub) UnregisterConnectionIfMatch(ski string, conn api.ShipConnectionInterface) bool {
	return h.registry.RemoveIfMatches(ski, conn)
}

// sendWSCloseMessage writes a websocket close frame and closes the connection.
// Used by ServeHTTP and connectFoundService to terminate a rejected raw
// websocket. Single owner of the close — no concurrent paths touch the same
// *websocket.Conn (resolves Bug 9).
func (h *Hub) sendWSCloseMessage(conn *websocket.Conn) {
	err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "double connection"))
	if err != nil && !errors.Is(err, net.ErrClosed) {
		logging.Log().Debug("failed to send close message:", err)
	}
	h.safeClose(conn, "websocket close")
}
