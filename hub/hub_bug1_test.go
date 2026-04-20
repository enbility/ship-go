package hub

import (
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
)

// TestBug1_AttemptFlagStuckOnEarlyReturn verifies that the connectionAttemptRunning
// flag is cleared on every early-return path of prepareConnectionInitation.
//
// The flag represents "an attempt is scheduled or running." If it stays true after
// the attempt has short-circuited, future mDNS-driven reconnect attempts for that
// SKI silently no-op (coordinateConnectionInitations early-returns at its
// isConnectionAttemptRunning check), permanently silencing reconnects until the
// process restarts.
//
// prepareConnectionInitation has three early-return paths that bypass
// initateConnection's deferred flag-clear: counter mismatch, !IsRemoteServiceForSKIPaired,
// and isSkiConnected. Each must independently clear the flag.
func TestBug1_AttemptFlagStuckOnEarlyReturn(t *testing.T) {
	t.Run("counter mismatch", func(t *testing.T) {
		hub := setupTestHubForTimer(t)
		ski := "ski-bug1-counter"

		// Simulate coordinateConnectionInitations having set the flag and counter.
		hub.setConnectionAttemptRunning(ski, true)
		hub.muxConAttempt.Lock()
		hub.connectionAttemptCounter[ski] = 5
		hub.muxConAttempt.Unlock()

		// Pass a stale counter (99) so the counter-mismatch check fires.
		hub.prepareConnectionInitation(ski, 99, &api.MdnsEntry{})

		assert.False(t, hub.isConnectionAttemptRunning(ski),
			"running flag must clear after counter-mismatch early return")
	})

	t.Run("service not paired", func(t *testing.T) {
		hub := setupTestHubForTimer(t)
		ski := "ski-bug1-notpaired"

		hub.setConnectionAttemptRunning(ski, true)
		hub.muxConAttempt.Lock()
		hub.connectionAttemptCounter[ski] = 0
		hub.muxConAttempt.Unlock()

		// No service is pre-registered as trusted; ServiceForSKI auto-creates one
		// with Trusted()==false, so IsRemoteServiceForSKIPaired returns false.
		hub.prepareConnectionInitation(ski, 0, &api.MdnsEntry{})

		assert.False(t, hub.isConnectionAttemptRunning(ski),
			"running flag must clear after not-paired early return")
	})

	t.Run("already connected", func(t *testing.T) {
		hub := setupTestHubForTimer(t)
		ski := "ski-bug1-connected"

		hub.setConnectionAttemptRunning(ski, true)
		hub.muxConAttempt.Lock()
		hub.connectionAttemptCounter[ski] = 0
		hub.muxConAttempt.Unlock()

		// Make IsRemoteServiceForSKIPaired return true so we reach the isSkiConnected check.
		service := api.NewServiceDetails(ski)
		service.SetTrusted(true)
		hub.muxReg.Lock()
		hub.remoteServices[ski] = service
		hub.muxReg.Unlock()

		// Inject a connection so isSkiConnected returns true.
		mockConn := mocks.NewShipConnectionInterface(t)
		mockConn.EXPECT().IsAlive().Return(true).Maybe()
		hub.registry.mu.Lock()
		hub.registry.connections[ski] = mockConn
		hub.registry.mu.Unlock()

		hub.prepareConnectionInitation(ski, 0, &api.MdnsEntry{})

		assert.False(t, hub.isConnectionAttemptRunning(ski),
			"running flag must clear after already-connected early return")
	})
}
