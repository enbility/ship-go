package hub

import (
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/stretchr/testify/assert"
)

// TestBug7a_ShutdownWaitsForInflightWg verifies that Shutdown does not return
// until inflightWg drains. This is the regression guard for the Phase 2
// infrastructure (#24) that wired wg.Wait into Shutdown.
//
// If a future change removes the wait, in-flight connection-establishment
// goroutines could complete after Shutdown returned and register connections
// into a torn-down hub.
func TestBug7a_ShutdownWaitsForInflightWg(t *testing.T) {
	hub := setupTestHubForTimer(t)

	// Simulate one in-flight connection-establishment goroutine.
	hub.inflightWg.Add(1)

	shutdownReturned := make(chan struct{})
	go func() {
		hub.Shutdown()
		close(shutdownReturned)
	}()

	time.Sleep(100 * time.Millisecond)

	select {
	case <-shutdownReturned:
		t.Fatal("Shutdown returned before draining inflightWg")
	default:
		// good — Shutdown is blocked on Wait
	}

	hub.inflightWg.Done()

	select {
	case <-shutdownReturned:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return after inflightWg drained")
	}
}

// TestBug7b_ConnectFoundServiceAbortsOnCancelledCtx verifies that
// connectFoundService short-circuits at entry when the lifecycle context is
// cancelled, instead of proceeding through the 5-second websocket dial timeout.
//
// Without the entry check, a goroutine started just before Shutdown can spend
// the full HandshakeTimeout dialling a remote that may already be unreachable,
// then attempt to register the resulting connection into a hub that has
// finished its connections-map cleanup.
func TestBug7b_ConnectFoundServiceAbortsOnCancelledCtx(t *testing.T) {
	hub := setupTestHubForTimer(t)

	// Cancel the lifecycle context — simulates Shutdown having started.
	hub.cancel()

	// A non-routable IP would block for HandshakeTimeout (5s) without the
	// entry check. The fix returns immediately with an error, well under 1s.
	service := api.NewServiceDetails("ski-bug7b")

	start := time.Now()
	err := hub.connectFoundService(service, "192.0.2.1", "1234", "/")
	elapsed := time.Since(start)

	assert.Error(t, err, "connectFoundService should return an error when ctx is cancelled")
	assert.Less(t, elapsed, 1*time.Second,
		"connectFoundService should return quickly when ctx is cancelled (no actual dial); took %s", elapsed)
}
