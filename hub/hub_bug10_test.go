package hub

import (
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBug10_PrepareConnectionInitationRemovesItsTimerEntry verifies that the
// timer-callback entry-point removes its own entry from connectionDelayTimers.
//
// Without this self-cleanup, fired timers leave stale entries forever — only
// cancelConnectionDelayTimer (called on Stop or successful registration) ever
// deletes. For long-lived hubs with high SKI churn, this is a slow leak.
func TestBug10_PrepareConnectionInitationRemovesItsTimerEntry(t *testing.T) {
	hub := setupTestHubForTimer(t)
	ski := "ski-bug10"

	// Plant a timer for this SKI as if coordinateConnectionInitations had
	// scheduled it. Use a long duration so the timer can't fire on its own
	// during the test — we want to test prepareConnectionInitation's cleanup,
	// not the timer's own callback path.
	timer := newConnectionDelayTimer(time.Hour, func() {})
	hub.storeConnectionDelayTimer(ski, timer)

	hub.muxTimers.RLock()
	_, presentBefore := hub.connectionDelayTimers[ski]
	hub.muxTimers.RUnlock()
	require.True(t, presentBefore, "timer should be planted in the map")

	// Run the callback path. The counter-mismatch early return is fine — we just
	// need prepareConnectionInitation to execute its self-cleanup before exiting.
	hub.prepareConnectionInitation(ski, 99, &api.MdnsEntry{})

	hub.muxTimers.RLock()
	_, presentAfter := hub.connectionDelayTimers[ski]
	hub.muxTimers.RUnlock()

	assert.False(t, presentAfter,
		"timer entry must be removed by prepareConnectionInitation (self-cleanup)")
}
