package hub

import (
	"sync"

	"github.com/enbility/ship-go/api"
)

// connectionRegistry owns the hub's per-SKI connection map and serializes the
// SHIP §12.2.2 double-connection rule with map mutations under a single private
// mutex.
//
// All atomic decisions (decide-and-act for double connections, compare-and-delete
// for cleanup) are confined to its methods, so external code cannot accidentally
// produce TOCTOU windows between map lookups and map writes.
//
// # Lock ordering
//
// This mutex is private and is never held across calls to arbitrary code.
// Callers may hold other hub mutexes (muxReg, muxConAttempt, muxTimers,
// muxStarted) BEFORE acquiring registry.mu, but must never reverse the order —
// and the registry itself never acquires those other locks.
type connectionRegistry struct {
	mu          sync.RWMutex
	connections map[string]api.ShipConnectionInterface
	// localSKI is captured at construction; the §12.2.2 rule is a function of
	// localSKI vs. remoteSKI, and we don't expect it to change at runtime.
	localSKI string
}

// newConnectionRegistry creates an empty registry that will apply the
// double-connection rule against localSKI.
func newConnectionRegistry(localSKI string) *connectionRegistry {
	return &connectionRegistry{
		connections: make(map[string]api.ShipConnectionInterface),
		localSKI:    localSKI,
	}
}

// Get returns the connection registered for ski, or nil. No liveness check —
// callers that care about liveness should use IsConnected.
func (r *connectionRegistry) Get(ski string) api.ShipConnectionInterface {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.connections[ski]
}

// IsConnected returns true iff a connection is registered for ski AND it
// reports IsAlive(). A registered-but-not-alive entry is "stale" — its
// HandleConnectionClosed callback hasn't propagated through yet — and is
// treated as "not connected" so reconnect attempts proceed instead of being
// short-circuited by a zombie entry.
func (r *connectionRegistry) IsConnected(ski string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conn, ok := r.connections[ski]
	if !ok {
		return false
	}
	return conn.IsAlive()
}

// Len returns the number of registered connections (including any stale ones).
func (r *connectionRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.connections)
}

// Snapshot returns a shallow copy of the connections map. Used by Shutdown to
// iterate-and-close outside the registry lock (since CloseConnection can fire
// HandleConnectionClosed callbacks that need to take the lock again).
func (r *connectionRegistry) Snapshot() map[string]api.ShipConnectionInterface {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]api.ShipConnectionInterface, len(r.connections))
	for k, v := range r.connections {
		out[k] = v
	}
	return out
}

// SwapResult describes the outcome of Swap. Exposed (rather than multi-return)
// so the caller's intent is clear at the call site.
type SwapResult struct {
	// Kept is true iff the new connection was registered.
	Kept bool
	// OldConn is the existing connection that was evicted from the slot. The
	// caller is responsible for closing it exactly once. nil if no eviction.
	OldConn api.ShipConnectionInterface
	// NewConn is the freshly-built connection if Kept; nil if rejected.
	NewConn api.ShipConnectionInterface
}

// Swap atomically resolves any existing connection for ski against a new one
// using the §12.2.2 double-connection rule, and registers the new connection
// if it wins.
//
// builder is invoked under the registry's lock IFF the new connection would
// win, so construction happens only when needed and is fully serialized with
// other Swap calls. builder MUST be quick (no I/O, no lock acquisitions that
// could re-enter the registry).
//
// Caller-owned cleanup:
//   - If result.Kept is false, the caller must close any raw resources tied to
//     the rejected new connection (typically the *websocket.Conn it dialed/upgraded).
//   - If result.OldConn is non-nil, the caller must call CloseConnection on it
//     exactly once (best done in a goroutine to avoid blocking the caller).
//   - If result.Kept is true, the caller must call result.NewConn.Run() (or
//     equivalent) AFTER Swap returns.
//
// A stale (non-alive) existing entry is silently evicted and treated as if no
// entry existed, so the new connection always wins in that case (resolves Bug
// 12 by construction).
func (r *connectionRegistry) Swap(
	ski string,
	isIncoming bool,
	builder func() api.ShipConnectionInterface,
) SwapResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, hasExisting := r.connections[ski]

	// Treat a stale (non-alive) existing entry as "no entry" — this lets a new
	// connection take over instead of being short-circuited by a zombie. The
	// stale entry's CloseConnection has already fired (that's why it's not
	// alive), so we don't surface it as OldConn for the caller to close.
	if hasExisting && !existing.IsAlive() {
		delete(r.connections, ski)
		existing = nil
		hasExisting = false
	}

	if !r.shouldKeepNew(ski, isIncoming, hasExisting) {
		return SwapResult{Kept: false}
	}

	newConn := builder()
	r.connections[ski] = newConn

	res := SwapResult{Kept: true, NewConn: newConn}
	if hasExisting {
		res.OldConn = existing
	}
	return res
}

// RemoveIfMatches atomically removes the connection registered for ski iff it
// equals conn. Returns whether a removal happened.
//
// Used by HandleConnectionClosed to clean up AFTER a connection's close: if
// the slot has already been replaced (e.g., by a double-connection swap), we
// don't remove the replacement, and the caller knows not to fire user-facing
// disconnect callbacks for a connection that no longer owns the slot.
func (r *connectionRegistry) RemoveIfMatches(ski string, conn api.ShipConnectionInterface) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.connections[ski]
	if !ok || current != conn {
		return false
	}
	delete(r.connections, ski)
	return true
}

// shouldKeepNew encapsulates the SHIP §12.2.2 double-connection rule.
//
// Current rule (ship-go's documented deviation): "the connection initiated by
// the higher-SKI side wins." This is a single, deterministic, convergent
// decision both peers can compute identically without coordination. It does
// NOT match the spec's "bigger-SKI keeps most recent" rule, which requires
// asynchronous coordination (the 3-second WebSocket-ping fallback for the
// smaller-SKI side).
//
// This is the single point of change for a future spec-compliant rule swap.
func (r *connectionRegistry) shouldKeepNew(remoteSKI string, isIncoming, hasExisting bool) bool {
	if !hasExisting {
		return true
	}
	if isIncoming {
		return remoteSKI > r.localSKI
	}
	return r.localSKI > remoteSKI
}
