package hub

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
)

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// aliveConn returns a mock ShipConnectionInterface whose IsAlive() returns true.
func aliveConn(t *testing.T) *mocks.ShipConnectionInterface {
	c := mocks.NewShipConnectionInterface(t)
	c.EXPECT().IsAlive().Return(true).Maybe()
	return c
}

// deadConn returns a mock ShipConnectionInterface whose IsAlive() returns false.
func deadConn(t *testing.T) *mocks.ShipConnectionInterface {
	c := mocks.NewShipConnectionInterface(t)
	c.EXPECT().IsAlive().Return(false).Maybe()
	return c
}

// -----------------------------------------------------------------------------
// Get / IsConnected / Len
// -----------------------------------------------------------------------------

func TestRegistry_Get_ReturnsNilForUnknownSKI(t *testing.T) {
	r := newConnectionRegistry("local")
	assert.Nil(t, r.Get("missing"))
}

func TestRegistry_Get_ReturnsRegisteredConn(t *testing.T) {
	r := newConnectionRegistry("local")
	conn := aliveConn(t)
	res := r.Swap("ski", true, func() api.ShipConnectionInterface { return conn })
	assert.True(t, res.Kept)
	assert.Equal(t, conn, r.Get("ski"))
}

func TestRegistry_IsConnected_FalseForUnknown(t *testing.T) {
	r := newConnectionRegistry("local")
	assert.False(t, r.IsConnected("missing"))
}

func TestRegistry_IsConnected_TrueForAlive(t *testing.T) {
	r := newConnectionRegistry("local")
	conn := aliveConn(t)
	r.Swap("ski", true, func() api.ShipConnectionInterface { return conn })
	assert.True(t, r.IsConnected("ski"))
}

func TestRegistry_IsConnected_FalseForStaleEntry(t *testing.T) {
	// A connection registered as alive can become non-alive (CloseConnection
	// fires asynchronously). IsConnected must report false for the dead entry
	// so reconnects aren't blocked. This is Bug 12's resolution by construction.
	r := newConnectionRegistry("local")

	// Inject directly into the map (bypassing Swap so IsAlive isn't queried at
	// insertion time — we want to simulate a registered conn that later died).
	dc := deadConn(t)
	r.mu.Lock()
	r.connections["ski"] = dc
	r.mu.Unlock()

	assert.False(t, r.IsConnected("ski"), "dead connections must report not-connected")
}

func TestRegistry_Len(t *testing.T) {
	r := newConnectionRegistry("local")
	assert.Equal(t, 0, r.Len())
	r.Swap("a", true, func() api.ShipConnectionInterface { return aliveConn(t) })
	r.Swap("b", true, func() api.ShipConnectionInterface { return aliveConn(t) })
	assert.Equal(t, 2, r.Len())
}

// -----------------------------------------------------------------------------
// Swap — no existing entry
// -----------------------------------------------------------------------------

func TestRegistry_Swap_NoExisting_KeepsNew(t *testing.T) {
	r := newConnectionRegistry("local")
	newConn := aliveConn(t)
	res := r.Swap("ski", true, func() api.ShipConnectionInterface { return newConn })

	assert.True(t, res.Kept, "no existing → must keep new")
	assert.Nil(t, res.OldConn)
	assert.Equal(t, newConn, res.NewConn)
}

// -----------------------------------------------------------------------------
// Swap — double connection rule (bigger-SKI initiator wins)
// -----------------------------------------------------------------------------

// localSKI = "B". Test the four combinations of incoming/outgoing × bigger/smaller.

func TestRegistry_Swap_Existing_IncomingFromBiggerSKI_KeepsNew(t *testing.T) {
	// local="B", remote="C" (bigger). Incoming from bigger SKI → keep new.
	r := newConnectionRegistry("B")
	old := aliveConn(t)
	r.Swap("C", true, func() api.ShipConnectionInterface { return old })

	newConn := aliveConn(t)
	res := r.Swap("C", true, func() api.ShipConnectionInterface { return newConn })

	assert.True(t, res.Kept)
	assert.Equal(t, old, res.OldConn, "existing must be returned for caller to close")
	assert.Equal(t, newConn, res.NewConn)
	assert.Equal(t, newConn, r.Get("C"), "registry must hold new")
}

func TestRegistry_Swap_Existing_IncomingFromSmallerSKI_RejectsNew(t *testing.T) {
	// local="B", remote="A" (smaller). Incoming from smaller SKI → reject new.
	r := newConnectionRegistry("B")
	old := aliveConn(t)
	r.Swap("A", true, func() api.ShipConnectionInterface { return old })

	builderCalled := false
	res := r.Swap("A", true, func() api.ShipConnectionInterface {
		builderCalled = true
		return aliveConn(t)
	})

	assert.False(t, res.Kept)
	assert.Nil(t, res.OldConn)
	assert.Nil(t, res.NewConn)
	assert.False(t, builderCalled, "builder must not run when new is rejected")
	assert.Equal(t, old, r.Get("A"), "existing must remain")
}

func TestRegistry_Swap_Existing_OutgoingFromBiggerLocal_KeepsNew(t *testing.T) {
	// local="C", remote="A". Outgoing → keep iff local > remote → true.
	r := newConnectionRegistry("C")
	old := aliveConn(t)
	r.Swap("A", false, func() api.ShipConnectionInterface { return old })

	newConn := aliveConn(t)
	res := r.Swap("A", false, func() api.ShipConnectionInterface { return newConn })

	assert.True(t, res.Kept)
	assert.Equal(t, old, res.OldConn)
	assert.Equal(t, newConn, res.NewConn)
}

func TestRegistry_Swap_Existing_OutgoingFromSmallerLocal_RejectsNew(t *testing.T) {
	// local="A", remote="C". Outgoing → keep iff local > remote → false.
	r := newConnectionRegistry("A")
	old := aliveConn(t)
	r.Swap("C", false, func() api.ShipConnectionInterface { return old })

	res := r.Swap("C", false, func() api.ShipConnectionInterface {
		t.Fatal("builder must not run")
		return nil
	})

	assert.False(t, res.Kept)
	assert.Equal(t, old, r.Get("C"))
}

// -----------------------------------------------------------------------------
// Swap — stale existing entry (Bug 12)
// -----------------------------------------------------------------------------

func TestRegistry_Swap_StaleExisting_TreatedAsAbsent_KeepsNew(t *testing.T) {
	// localSKI=B, remoteSKI=A. Outgoing from smaller-SKI side: under the rule
	// the new conn would be REJECTED if existing were alive. But the existing
	// is dead, so registry treats slot as empty and keeps the new conn.
	r := newConnectionRegistry("B")

	dead := deadConn(t)
	r.mu.Lock()
	r.connections["A"] = dead
	r.mu.Unlock()

	newConn := aliveConn(t)
	res := r.Swap("A", false, func() api.ShipConnectionInterface { return newConn })

	assert.True(t, res.Kept, "dead existing must not block new")
	assert.Nil(t, res.OldConn, "dead conn already had Close fired; not surfaced")
	assert.Equal(t, newConn, res.NewConn)
	assert.Equal(t, newConn, r.Get("A"), "registry holds new conn")
}

// -----------------------------------------------------------------------------
// RemoveIfMatches
// -----------------------------------------------------------------------------

func TestRegistry_RemoveIfMatches_MatchingConn_Removes(t *testing.T) {
	r := newConnectionRegistry("local")
	conn := aliveConn(t)
	r.Swap("ski", true, func() api.ShipConnectionInterface { return conn })

	removed := r.RemoveIfMatches("ski", conn)
	assert.True(t, removed)
	assert.Nil(t, r.Get("ski"))
}

func TestRegistry_RemoveIfMatches_MismatchedConn_NoOp(t *testing.T) {
	// After a double-connection swap, the slot holds NEW. HandleConnectionClosed
	// fires for OLD (the evicted one). RemoveIfMatches must not delete NEW.
	r := newConnectionRegistry("local")

	old := aliveConn(t)
	r.Swap("ski", true, func() api.ShipConnectionInterface { return old })

	// Replace via Swap — only works on bigger remote SKI.
	r2 := newConnectionRegistry("A")
	old2 := aliveConn(t)
	new2 := aliveConn(t)
	r2.Swap("Z", true, func() api.ShipConnectionInterface { return old2 })
	r2.Swap("Z", true, func() api.ShipConnectionInterface { return new2 })
	// r2 now holds new2. Try to remove old2: must be no-op.

	removed := r2.RemoveIfMatches("Z", old2)
	assert.False(t, removed, "mismatched conn must not be removed")
	assert.Equal(t, new2, r2.Get("Z"), "current conn must remain")
}

func TestRegistry_RemoveIfMatches_NoEntry_ReturnsFalse(t *testing.T) {
	r := newConnectionRegistry("local")
	conn := aliveConn(t)
	assert.False(t, r.RemoveIfMatches("ski", conn))
}

// -----------------------------------------------------------------------------
// Concurrency: many Swaps for the same SKI converge to one winner
// -----------------------------------------------------------------------------

func TestRegistry_Swap_ConcurrentSameSKI_NoLeak(t *testing.T) {
	// Bug 2's verification (in spirit): the registry's atomic Swap means
	// concurrent dialers for the same SKI cannot leak. Either each loses the
	// rule (rejected, no NewConn) or wins it (registered, exactly one survives).
	const N = 50
	r := newConnectionRegistry("M")

	var (
		wg          sync.WaitGroup
		registered  atomic.Int32 // count of Swaps that returned Kept=true
		evicted     atomic.Int32 // count of OldConn returns (each must be closed once)
		built       atomic.Int32 // count of times builder was invoked
		totalBuilds = make(map[api.ShipConnectionInterface]bool)
		mu          sync.Mutex
	)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := r.Swap("Z", true, func() api.ShipConnectionInterface {
				built.Add(1)
				c := aliveConn(t)
				mu.Lock()
				totalBuilds[c] = true
				mu.Unlock()
				return c
			})
			if res.Kept {
				registered.Add(1)
			}
			if res.OldConn != nil {
				evicted.Add(1)
			}
		}()
	}
	wg.Wait()

	// Final state: exactly one conn registered.
	assert.NotNil(t, r.Get("Z"), "registry must hold a connection")
	assert.Equal(t, 1, r.Len(), "registry must hold exactly one connection")

	// Invariant: every built connection except the final one must have been
	// returned for caller cleanup (either as the rejected NewConn, which never
	// happened in this test since builder runs only on Kept; or as OldConn).
	// Since we only Keep on remoteSKI > localSKI (Z > M) which is always true,
	// every Swap should be Kept=true. Every Kept after the first evicts the
	// previous → evicted == registered - 1.
	assert.Equal(t, int32(N), registered.Load(), "all swaps must win the rule (Z > M)")
	assert.Equal(t, registered.Load()-1, evicted.Load(),
		"every keeper after the first must evict its predecessor")
	assert.Equal(t, registered.Load(), built.Load(),
		"builder must run exactly once per kept swap")
}
