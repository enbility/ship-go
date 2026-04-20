package hub

import (
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/stretchr/testify/assert"
)

// TestBug6_PrepareConnectionInitationSkipsWhenCtxCancelled verifies that the
// timer callback (prepareConnectionInitation) short-circuits when the hub's
// lifecycle context is cancelled.
//
// Without the ctx check, a timer that fires concurrently with Shutdown can run
// against a torn-down hub: it deletes its timer entry, clears the running flag,
// looks up services, and may even attempt initateConnection — all after the hub
// is already in shutdown.
func TestBug6_PrepareConnectionInitationSkipsWhenCtxCancelled(t *testing.T) {
	hub := setupTestHubForTimer(t)
	ski := "ski-bug6"

	hub.setConnectionAttemptRunning(ski, true)
	timer := newConnectionDelayTimer(time.Hour, func() {})
	hub.storeConnectionDelayTimer(ski, timer)

	// Cancel the lifecycle context — simulates Shutdown having started.
	hub.cancel()

	hub.prepareConnectionInitation(ski, 0, &api.MdnsEntry{})

	hub.muxTimers.RLock()
	_, timerStillPresent := hub.connectionDelayTimers[ski]
	hub.muxTimers.RUnlock()

	assert.True(t, timerStillPresent,
		"timer entry should NOT be removed when ctx is cancelled (callback should short-circuit before cancelConnectionDelayTimer)")
	assert.True(t, hub.isConnectionAttemptRunning(ski),
		"running flag should NOT be cleared when ctx is cancelled (callback should short-circuit before defer)")
}
