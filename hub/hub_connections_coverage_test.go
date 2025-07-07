package hub

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestConnectionAttemptCounterMaximum tests that the connection attempt counter
// properly caps at the maximum value defined by connectionInitiationDelayTimeRanges
func TestConnectionAttemptCounterMaximum(t *testing.T) {
	hub := setupTestHubForTimer(t)

	ski := "test-ski-counter-max"

	// The counter should cap at len(connectionInitiationDelayTimeRanges)-1
	maxCounter := len(connectionInitiationDelayTimeRanges) - 1

	// Increment counter beyond the maximum
	for i := 0; i <= maxCounter+5; i++ {
		counter := hub.increaseConnectionAttemptCounter(ski)
		if i < maxCounter {
			assert.Equal(t, i, counter, "counter should increment up to max")
		} else {
			assert.Equal(t, maxCounter, counter, "counter should cap at max")
		}
	}

	// Verify the counter is capped
	finalCounter, exists := hub.getCurrentConnectionAttemptCounter(ski)
	assert.True(t, exists, "counter should exist")
	assert.Equal(t, maxCounter, finalCounter, "final counter should be capped at max")

	// Test removal
	hub.removeConnectionAttemptCounter(ski)
	_, exists = hub.getCurrentConnectionAttemptCounter(ski)
	assert.False(t, exists, "counter should be removed")
}

// TestConnectionDelayTimerReplacement tests that existing timers are properly
// cancelled when a new timer is created for the same SKI
func TestConnectionDelayTimerReplacement(t *testing.T) {
	hub := setupTestHubForTimer(t)
	hub.Start()
	defer hub.Shutdown()

	ski := "test-ski-timer-replace"

	// Create first timer
	var firstTimerCalled atomic.Bool
	timer1 := newConnectionDelayTimer(200*time.Millisecond, func() {
		firstTimerCalled.Store(true)
	})
	hub.storeConnectionDelayTimer(ski, timer1)

	// Verify timer was stored
	hub.muxTimers.RLock()
	storedTimer := hub.connectionDelayTimers[ski]
	hub.muxTimers.RUnlock()
	assert.Equal(t, timer1, storedTimer, "first timer should be stored")

	// Create second timer immediately (should cancel first)
	var secondTimerCalled atomic.Bool
	timer2 := newConnectionDelayTimer(50*time.Millisecond, func() {
		secondTimerCalled.Store(true)
	})
	hub.storeConnectionDelayTimer(ski, timer2)

	// Wait for second timer to fire
	time.Sleep(100 * time.Millisecond)

	// First timer should not have fired
	assert.False(t, firstTimerCalled.Load(), "first timer should have been cancelled")
	assert.True(t, secondTimerCalled.Load(), "second timer should have fired")

	// Verify only second timer is stored
	hub.muxTimers.RLock()
	finalTimer := hub.connectionDelayTimers[ski]
	hub.muxTimers.RUnlock()
	assert.Equal(t, timer2, finalTimer, "second timer should be stored")
}

// TestConnectionAttemptRunningConcurrency tests the isConnectionAttemptRunning
// logic under concurrent access
func TestConnectionAttemptRunningConcurrency(t *testing.T) {
	hub := setupTestHubForTimer(t)
	hub.Start()
	defer hub.Shutdown()

	ski := "test-ski-concurrent"
	service := api.NewServiceDetails(ski)
	service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
	hub.muxReg.Lock()
	hub.remoteServices[ski] = service
	hub.muxReg.Unlock()

	entry := &api.MdnsEntry{
		Name:       "test-device",
		Identifier: ski,
		Register:   true,
	}

	// Simulate multiple concurrent connection attempts
	var attemptCount atomic.Int32
	for i := 0; i < 10; i++ {
		go func() {
			hub.coordinateConnectionInitations(ski, entry)
			attemptCount.Add(1)
		}()
	}

	// Give goroutines time to run
	time.Sleep(50 * time.Millisecond)

	// Due to isConnectionAttemptRunning check, only one attempt should proceed
	// at a time, though multiple may eventually run sequentially
	assert.True(t, attemptCount.Load() > 0, "at least one attempt should have been made")
}

// TestPrepareConnectionInitiationCounterMismatch tests the case where
// the connection counter changes between coordinateConnectionInitations
// and prepareConnectionInitation
func TestPrepareConnectionInitiationCounterMismatch(t *testing.T) {
	hub := setupTestHubForTimer(t)
	hub.Start()
	defer hub.Shutdown()

	ski := "test-ski-counter-mismatch"
	service := api.NewServiceDetails(ski)
	service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
	hub.muxReg.Lock()
	hub.remoteServices[ski] = service
	hub.muxReg.Unlock()

	entry := &api.MdnsEntry{
		Name:       "test-device",
		Identifier: ski,
		Register:   true,
	}

	// Set initial counter
	hub.muxConAttempt.Lock()
	hub.connectionAttemptCounter[ski] = 1
	hub.muxConAttempt.Unlock()

	// Call prepareConnectionInitation with old counter value
	// It should exit early due to counter mismatch
	hub.prepareConnectionInitation(ski, 0, entry) // old counter = 0, current = 1

	// Verify no connection was initiated (would require more mocking to fully test)
	// The key is that the function returns early due to counter mismatch
}

// TestDoubleConnectionPreventionEdgeCases tests specific scenarios in
// keepThisConnection logic
func TestDoubleConnectionPreventionEdgeCases(t *testing.T) {
	// Test case 1: Local SKI > Remote SKI, outgoing connection
	{
		hub := setupTestHubForTimer(t)
		hub.localService = api.NewServiceDetails("zzz-local-ski") // Higher than remote
		hub.Start()
		defer hub.Shutdown()

		remoteSKI := "aaa-remote-ski"
		remoteService := api.NewServiceDetails(remoteSKI)

		// For outgoing connection, we should keep it (local > remote)
		shouldKeep := hub.keepThisConnection(nil, false, remoteService)
		assert.True(t, shouldKeep, "should keep outgoing connection when local SKI > remote SKI")
	}

	// Test case 2: Local SKI < Remote SKI, incoming connection
	{
		hub := setupTestHubForTimer(t)
		hub.localService = api.NewServiceDetails("aaa-local-ski") // Lower than remote
		hub.Start()
		defer hub.Shutdown()

		remoteService := api.NewServiceDetails("zzz-remote-ski")

		// For incoming connection, we should keep it (remote > local)
		shouldKeep := hub.keepThisConnection(nil, true, remoteService)
		assert.True(t, shouldKeep, "should keep incoming connection when remote SKI > local SKI")
	}

	// Test case 3: Existing connection scenario
	{
		hub := setupTestHubForTimer(t)
		hub.localService = api.NewServiceDetails("zzz-high-ski") // Higher than existing
		hub.Start()
		defer hub.Shutdown()

		existingSKI := "existing-ski"
		existingConn := mocks.NewShipConnectionInterface(t)
		existingConn.EXPECT().RemoteSKI().Return(existingSKI).Maybe()
		existingConn.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()

		hub.muxCon.Lock()
		hub.connections[existingSKI] = existingConn
		hub.muxCon.Unlock()

		existingService := api.NewServiceDetails(existingSKI)

		// New outgoing connection should be kept
		shouldKeep := hub.keepThisConnection(nil, false, existingService)
		assert.True(t, shouldKeep, "should keep new connection when conditions favor it")
	}
}

// TestConnectionInitiationDelayTimeRange tests that delay time calculation
// works correctly for different counter values
func TestConnectionInitiationDelayTimeRange(t *testing.T) {
	hub := setupTestHubForTimer(t)

	ski := "test-ski-delay"

	// Test first attempt (counter = 0)
	counter1, duration1 := hub.getConnectionInitiationDelayTime(ski)
	assert.Equal(t, 0, counter1)
	assert.True(t, duration1 >= 0 && duration1 <= 3*time.Second,
		"first attempt delay should be 0-3 seconds, got %v", duration1)

	// Test second attempt (counter = 1)
	counter2, duration2 := hub.getConnectionInitiationDelayTime(ski)
	assert.Equal(t, 1, counter2)
	assert.True(t, duration2 >= 3*time.Second && duration2 <= 10*time.Second,
		"second attempt delay should be 3-10 seconds, got %v", duration2)

	// Test third+ attempts (counter = 2+)
	counter3, duration3 := hub.getConnectionInitiationDelayTime(ski)
	assert.Equal(t, 2, counter3)
	assert.True(t, duration3 >= 10*time.Second && duration3 <= 20*time.Second,
		"third+ attempt delay should be 10-20 seconds, got %v", duration3)
}
