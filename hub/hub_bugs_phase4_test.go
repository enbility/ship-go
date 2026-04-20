package hub

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Phase 4 verification tests. Each pins an invariant that the connection
// registry's atomic Swap / RemoveIfMatches make true by construction. If any
// of these REDs, the Phase 3 architecture is wrong — fix the architecture,
// never weaken these tests.

// -----------------------------------------------------------------------------
// Bug 2 — TOCTOU: a third concurrent attempt cannot leak a connection
// -----------------------------------------------------------------------------

// TestBug2_ConcurrentRegistrationsConvergeToOneWinner: under concurrent dialers
// for the same SKI, the registry's atomic Swap means there is no window for
// any third party to "slip in" between a decide and a register. Either the
// caller wins (Kept=true, OldConn=any-previous-winner) or loses (Kept=false).
// No connection ever gets registered without the caller knowing (so the caller
// can clean it up).
//
// This is the architectural verification of what the old keepThisConnection +
// registerConnection split could leak.
func TestBug2_ConcurrentRegistrationsConvergeToOneWinner(t *testing.T) {
	const N = 100
	// localSKI < remoteSKI so every incoming dial wins the rule. This maximizes
	// the chance of an eviction race in the buggy version.
	r := newConnectionRegistry("A")

	var (
		wg          sync.WaitGroup
		registered  atomic.Int32 // count of swaps that returned Kept=true
		evictions   atomic.Int32 // count of OldConn returns (each must be closeable exactly once)
		buildersRan atomic.Int32 // count of builder invocations
	)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := r.Swap("Z", true, func() api.ShipConnectionInterface {
				buildersRan.Add(1)
				c := mocks.NewShipConnectionInterface(t)
				c.EXPECT().IsAlive().Return(true).Maybe()
				return c
			})
			if res.Kept {
				registered.Add(1)
			}
			if res.OldConn != nil {
				evictions.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, r.Len(), "exactly one connection must be registered after the dust settles")
	assert.NotNil(t, r.Get("Z"), "registry must hold the surviving connection")

	// Invariant: every Kept after the first must have evicted its predecessor.
	// And builder runs exactly once per Kept (never speculatively).
	assert.Equal(t, int32(N), registered.Load(),
		"every dialer in this rule-favouring scenario should win Kept=true")
	assert.Equal(t, registered.Load()-1, evictions.Load(),
		"every winner after the first must surface its predecessor for caller cleanup")
	assert.Equal(t, registered.Load(), buildersRan.Load(),
		"builder must run exactly once per kept swap (no speculative construction)")
}

// -----------------------------------------------------------------------------
// Bug 3 — HandleConnectionClosed must not fire RemoteSKIDisconnected for a
// connection that was already evicted by a double-connection swap
// -----------------------------------------------------------------------------

// TestBug3_HandleConnectionClosedSkipsCallbackForReplacedConnection: register
// conn1, swap in conn2 (which evicts conn1), then call HandleConnectionClosed
// for conn1. The callback must NOT fire — conn1 no longer owns the slot.
//
// The setupTestHubForTimer hub has localSKI = "test-ski-timer". For the rule
// to keep an INCOMING connection, the remote SKI must compare GREATER. Use
// "zz-bigger-than-tst" which is lexicographically > "test-ski-timer".
func TestBug3_HandleConnectionClosedSkipsCallbackForReplacedConnection(t *testing.T) {
	hub := setupTestHubForTimer(t)
	const remoteSKI = "zz-bigger-than-tst" // lexicographically > "test-ski-timer"

	// Count RemoteSKIDisconnected calls. setupTestHubForTimer registers a
	// .Maybe() expectation so calls don't fail; we wrap with a Run to count.
	var disconnectCalls atomic.Int32
	mockReader := hub.hubReader.(*mocks.HubReaderInterface)
	mockReader.EXPECT().RemoteSKIDisconnected(mock.AnythingOfType("string")).
		Run(func(string) { disconnectCalls.Add(1) }).Maybe()

	// Register conn1 via Swap (no existing → kept).
	conn1 := mocks.NewShipConnectionInterface(t)
	conn1.EXPECT().RemoteSKI().Return(remoteSKI).Maybe()
	conn1.EXPECT().IsAlive().Return(true).Maybe()
	res1 := hub.registry.Swap(remoteSKI, true, func() api.ShipConnectionInterface { return conn1 })
	assert.True(t, res1.Kept, "first registration must succeed")

	// Swap in conn2. Rule: incoming + remote > local → keep new.
	conn2 := mocks.NewShipConnectionInterface(t)
	conn2.EXPECT().RemoteSKI().Return(remoteSKI).Maybe()
	conn2.EXPECT().IsAlive().Return(true).Maybe()
	res2 := hub.registry.Swap(remoteSKI, true, func() api.ShipConnectionInterface { return conn2 })
	assert.True(t, res2.Kept, "second incoming from bigger SKI must win the rule")
	assert.Equal(t, conn1, res2.OldConn, "conn1 should have been evicted")

	// Snapshot disconnect count before invoking HandleConnectionClosed for
	// conn1. (The Swap above may have triggered other unrelated callbacks via
	// other code paths — we want to isolate just the post-evicted behavior.)
	startCount := disconnectCalls.Load()

	// Now simulate conn1 closing — its HandleConnectionClosed callback fires.
	hub.HandleConnectionClosed(conn1, true)

	assert.Equal(t, startCount, disconnectCalls.Load(),
		"RemoteSKIDisconnected must NOT fire for a connection that was already replaced")
	assert.Equal(t, conn2, hub.registry.Get(remoteSKI), "conn2 must remain registered")
}

// -----------------------------------------------------------------------------
// Bug 9 — registered single owner of websocket close on rejected double conn
// -----------------------------------------------------------------------------

// TestBug9_RegistryDoesNotSpawnConcurrentWebsocketWriter verifies the single-
// owner property: when registry.Swap rejects a new connection, it does NOT
// touch the underlying *websocket.Conn — that responsibility is entirely the
// caller's (ServeHTTP / connectFoundService each do exactly one sendWSCloseMessage).
//
// Direct test by inspection: the registry's Swap method body has no reference
// to websocket; the rejected path sets Kept=false and returns. We assert this
// behavioral contract by confirming the builder is NEVER invoked on a reject
// (so no ShipConnection is constructed and no goroutine on the websocket is
// spawned by the registry).
func TestBug9_RegistryRejectedSwapDoesNotInvokeBuilderOrSpawnWriter(t *testing.T) {
	// localSKI > remoteSKI, incoming → reject (rule: keep iff remote > local).
	r := newConnectionRegistry("Z")

	old := mocks.NewShipConnectionInterface(t)
	old.EXPECT().IsAlive().Return(true).Maybe()
	r.Swap("A", true, func() api.ShipConnectionInterface { return old })

	var builderCalls atomic.Int32
	res := r.Swap("A", true, func() api.ShipConnectionInterface {
		builderCalls.Add(1)
		return mocks.NewShipConnectionInterface(t)
	})

	assert.False(t, res.Kept, "rule-rejected swap")
	assert.Nil(t, res.NewConn, "no connection constructed for rejected swap")
	assert.Nil(t, res.OldConn, "rejected swap must not surface OldConn for cleanup")
	assert.Equal(t, int32(0), builderCalls.Load(),
		"builder must not run on a rejected swap (no resource the registry needs to clean up)")

	// Existing connection remains untouched.
	assert.Equal(t, old, r.Get("A"))
}

// -----------------------------------------------------------------------------
// Bug 12 — stale (non-alive) registry entry must not block reconnect
// -----------------------------------------------------------------------------

// TestBug12_StaleEntryDoesNotShortCircuitConnect: when a registered connection
// reports IsAlive()==false (its CloseConnection has fired but
// HandleConnectionClosed has not yet propagated), IsConnected returns false so
// the reconnect path proceeds, and Swap evicts the stale entry rather than
// rejecting on the rule.
//
// localSKI=B, remoteSKI=A (smaller than local) so the §12.2.2 rule for
// outgoing would normally REJECT a new conn. The test confirms a stale entry
// is treated as "no entry" and the new conn always wins.
func TestBug12_StaleEntryDoesNotShortCircuitConnect(t *testing.T) {
	r := newConnectionRegistry("B")

	stale := mocks.NewShipConnectionInterface(t)
	stale.EXPECT().IsAlive().Return(false).Maybe()
	r.mu.Lock()
	r.connections["A"] = stale
	r.mu.Unlock()

	// IsConnected must report false even though an entry exists.
	assert.False(t, r.IsConnected("A"),
		"a registered-but-dead connection must report not-connected")

	// Outgoing Swap from smaller-SKI side: under the rule with an alive existing,
	// this would REJECT. With a stale existing, the registry treats it as absent
	// and KEEPS the new conn.
	newConn := mocks.NewShipConnectionInterface(t)
	newConn.EXPECT().IsAlive().Return(true).Maybe()
	res := r.Swap("A", false, func() api.ShipConnectionInterface { return newConn })

	assert.True(t, res.Kept, "stale entry must not block a new connection")
	assert.Nil(t, res.OldConn, "stale conn already had Close fired; not surfaced for re-cleanup")
	assert.Equal(t, newConn, res.NewConn)
	assert.Equal(t, newConn, r.Get("A"), "registry now holds the new (alive) connection")
}
