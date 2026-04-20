package hub

import (
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
)

// TestBug4_RegistrationReleasesRegistryLockBeforeAcquiringMuxTimers pins the
// lock-order invariant for the registration → cancelConnectionDelayTimer
// sequence: the connection-registry mutex must not be held while waiting for
// muxTimers. Holding both nested would deadlock against any caller that holds
// muxTimers and needs the registry lock.
//
// (Original Bug 4 was about muxCon → muxTimers nesting in registerConnection.
// Post-Phase 3, the connections map is owned by the registry, but the SAME
// invariant must hold for the registry's mutex. Both are sequenced via
// registerConnectionAndCancelTimer below — see hub_connections_client.go's
// connectFoundService which calls registry.Swap and then, OUTSIDE the registry
// lock, cancels the timer.)
//
// Test design: hold muxTimers from the test goroutine, then have the hub
// register a connection (via registry.Swap) and cancel its timer. From a
// third goroutine, try to acquire the registry lock for read. With correct
// lock-ordering discipline, the registry lock is released before the timer
// cancel, so the third goroutine succeeds quickly. With nested holding, it
// would block until the watchdog fires.
func TestBug4_RegistrationReleasesRegistryLockBeforeAcquiringMuxTimers(t *testing.T) {
	hub := setupTestHubForTimer(t)
	ski := "ski-bug4"

	mockConn := mocks.NewShipConnectionInterface(t)
	mockConn.EXPECT().RemoteSKI().Return(ski).Maybe()
	mockConn.EXPECT().IsAlive().Return(true).Maybe()

	hub.muxTimers.Lock()

	registered := make(chan struct{})
	go func() {
		// Use the same code path that production uses: registry.Swap (private
		// lock) followed by cancelConnectionDelayTimer (muxTimers).
		hub.registry.Swap(ski, false, func() api.ShipConnectionInterface { return mockConn })
		hub.cancelConnectionDelayTimer(ski)
		close(registered)
	}()

	// Give the goroutine time to complete its Swap and block on muxTimers.
	time.Sleep(50 * time.Millisecond)

	registryFree := make(chan struct{})
	go func() {
		hub.registry.mu.RLock()
		hub.registry.mu.RUnlock()
		close(registryFree)
	}()

	select {
	case <-registryFree:
		// GREEN: registry lock was released before cancelConnectionDelayTimer blocked on muxTimers.
		hub.muxTimers.Unlock()
		<-registered
	case <-time.After(500 * time.Millisecond):
		// RED: registry lock is still held while blocked on muxTimers.
		hub.muxTimers.Unlock()
		<-registered
		t.Fatal("registration sequence holds registry lock while blocked on muxTimers (lock-order inversion)")
	}
}
