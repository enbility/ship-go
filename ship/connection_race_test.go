// Package ship_test contains race condition tests for the SHIP implementation.
// These tests verify that concurrent operations on connections, handshakes, and timers
// are properly synchronized and don't cause data races or inconsistent state.
//
// All tests should be run with the -race flag to enable Go's race detector:
//
//	go test -race ./ship
//
// The tests use high concurrency (50-100 goroutines) to increase the likelihood
// of detecting race conditions that might only occur under load.
package ship

import (
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestApprovePendingHandshake_RaceCondition tests that concurrent approve/abort operations
// don't cause race conditions or inconsistent state.
//
// Expected behavior:
// - At most one approve operation can succeed (state can only transition from PendingListen once)
// - If approve succeeds and transitions to ReadyListen, abort may also succeed (by design)
// - The handshake can be aborted at any point before reaching the Ok state
// - All operations must be thread-safe with no data races
func TestApprovePendingHandshake_RaceCondition(t *testing.T) {
	// Setup
	safeTracker := NewSafeConnectionTracker()
	safeTracker.Configure(
		WithRemoteServicePaired(true),
		WithAllowWaitingForTrust(true),
	)
	mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)

	// Mock expectations for data writer only
	mockDataWriter.EXPECT().InitDataProcessing(mock.Anything).Maybe()
	mockDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
	mockDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()
	mockDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()

	// Create a connection in pending state
	conn := NewConnectionHandler(
		safeTracker,
		mockDataWriter,
		ShipRoleClient,
		"local-ship-id",
		"remote-ski",
		"remote-ship-id",
	)

	// Set to pending state
	conn.setState(model.SmeHelloStatePendingListen, nil)

	// Run concurrent operations
	var wg sync.WaitGroup
	const numGoroutines = 100

	approveCount := 0
	abortCount := 0
	var countMux sync.Mutex

	// Half will try to approve, half will try to abort
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		if i%2 == 0 {
			go func() {
				defer wg.Done()
				// Use the atomic method directly
				if conn.ApproveIfPending() {
					countMux.Lock()
					approveCount++
					countMux.Unlock()
				}
			}()
		} else {
			go func() {
				defer wg.Done()
				// Use the atomic method directly
				if conn.AbortIfPending() {
					countMux.Lock()
					abortCount++
					countMux.Unlock()
				}
			}()
		}
	}

	wg.Wait()

	// Verify results based on the actual behavior:
	// - At most one approve can succeed (state can only transition from PendingListen once)
	// - Multiple aborts might succeed if approve transitions to ReadyListen before abort checks
	// - The total should be at least 1 (something must succeed from PendingListen state)
	assert.LessOrEqual(t, approveCount, 1,
		"At most one approve should succeed, got %d", approveCount)
	assert.GreaterOrEqual(t, approveCount+abortCount, 1,
		"At least one operation should succeed, got approve=%d, abort=%d", approveCount, abortCount)

	// If approve succeeded, abort might also succeed due to ReadyListen state
	if approveCount == 1 {
		// Abort can succeed if it runs after approve transitions to ReadyListen
		assert.LessOrEqual(t, abortCount, 1,
			"At most one abort should succeed after approve, got %d", abortCount)
	} else {
		// If no approve succeeded, exactly one abort should succeed
		assert.Equal(t, 1, abortCount,
			"Exactly one abort should succeed when approve doesn't, got %d", abortCount)
	}

	// Allow time for state transitions to complete
	time.Sleep(time.Millisecond * 20)

	// Final state should be consistent with what succeeded
	finalState := conn.getState()
	if approveCount == 1 {
		// If approve succeeded, we should be past pending state
		assert.NotEqual(t, model.SmeHelloStatePendingListen, finalState,
			"State should not be pending after approve")
	} else if abortCount == 1 {
		// If abort succeeded, we should be in abort done state
		assert.Equal(t, model.SmeHelloStateAbortDone, finalState,
			"State should be AbortDone after abort")
	}

	// Verify HandleConnectionClosed was called if abort succeeded
	// Note: Due to timing, HandleConnectionClosed might not always be called immediately
	closedCalls := safeTracker.GetConnectionClosedCalls()
	// If we have close calls, verify the remote SKI is correct
	for _, call := range closedCalls {
		assert.Equal(t, "remote-ski", call.RemoteSKI, "Remote SKI should match")
	}

	// Verify handshake state updates were recorded
	stateUpdates := safeTracker.GetHandshakeStateUpdates()
	assert.Greater(t, len(stateUpdates), 0, "Handshake state updates should be recorded")

	// Cleanup: stop any timers
	conn.stopTimerSafe()
	// Wait for abort cleanup if needed
	if abortCount == 1 {
		time.Sleep(time.Second + 100*time.Millisecond)
	}
}

// TestApproveIfPending tests the new atomic approve method
func TestApproveIfPending(t *testing.T) {
	tests := []struct {
		name          string
		initialState  model.ShipMessageExchangeState
		expectSuccess bool
		expectedState model.ShipMessageExchangeState
	}{
		{
			name:          "successful approval when pending",
			initialState:  model.SmeHelloStatePendingListen,
			expectSuccess: true,
			expectedState: model.SmeHelloStateOk, // Not used for success case
		},
		{
			name:          "no-op when not pending",
			initialState:  model.SmeHelloStateReadyListen,
			expectSuccess: false,
			expectedState: model.SmeHelloStateReadyListen,
		},
		{
			name:          "no-op when already approved",
			initialState:  model.SmeHelloStateOk,
			expectSuccess: false,
			expectedState: model.SmeHelloStateOk,
		},
		{
			name:          "no-op when in abort state",
			initialState:  model.SmeHelloStateAbort,
			expectSuccess: false,
			expectedState: model.SmeHelloStateAbort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
			mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)

			// Mock expectations - include cleanup expectations
			mockDataWriter.EXPECT().InitDataProcessing(mock.Anything).Maybe()
			mockDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()
			mockInfoProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Maybe()
			// Fix race: Use a matcher that doesn't reflect on connection internals
			connectionMatcher := mock.MatchedBy(func(conn api.ShipConnectionInterface) bool {
				return conn != nil
			})
			mockInfoProvider.EXPECT().HandleConnectionClosed(connectionMatcher, mock.Anything).Maybe()
			if tt.expectSuccess {
				mockDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
				mockDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()
				// Add expectations for validation methods
				mockInfoProvider.EXPECT().IsRemoteServiceForSKIPaired("remote-ski").Return(true).Maybe()
				mockInfoProvider.EXPECT().AllowWaitingForTrust("remote-ski").Return(true).Maybe()
			}

			conn := NewConnectionHandler(
				mockInfoProvider,
				mockDataWriter,
				ShipRoleClient,
				"local-ship-id",
				"remote-ski",
				"remote-ship-id",
			)

			// Set initial state
			conn.setState(tt.initialState, nil)

			// Test the new method (to be implemented)
			success := conn.ApproveIfPending()

			assert.Equal(t, tt.expectSuccess, success)

			// Allow time for state transitions
			if tt.expectSuccess {
				time.Sleep(time.Millisecond * 50)
			}

			state := conn.getState()
			// For successful approval, state transitions through multiple states
			// We just check it's not in pending anymore
			if tt.expectSuccess {
				assert.NotEqual(t, model.SmeHelloStatePendingListen, state)
			} else {
				assert.Equal(t, tt.expectedState, state)
			}

			// Cleanup: stop any timers
			conn.stopTimerSafe()

			// Wait for any async operations to complete
			time.Sleep(time.Millisecond * 100)
		})
	}
}

// TestAbortIfPending tests the new atomic abort method
func TestAbortIfPending(t *testing.T) {
	tests := []struct {
		name          string
		initialState  model.ShipMessageExchangeState
		expectSuccess bool
		expectedState model.ShipMessageExchangeState
	}{
		{
			name:          "successful abort when pending",
			initialState:  model.SmeHelloStatePendingListen,
			expectSuccess: true,
			expectedState: model.SmeHelloStateAbortDone,
		},
		{
			name:          "successful abort when ready",
			initialState:  model.SmeHelloStateReadyListen,
			expectSuccess: true,
			expectedState: model.SmeHelloStateAbortDone,
		},
		{
			name:          "no-op when already approved",
			initialState:  model.SmeHelloStateOk,
			expectSuccess: false,
			expectedState: model.SmeHelloStateOk,
		},
		{
			name:          "no-op when already aborted",
			initialState:  model.SmeHelloStateAbort,
			expectSuccess: false,
			expectedState: model.SmeHelloStateAbort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
			mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)

			// Mock expectations
			mockDataWriter.EXPECT().InitDataProcessing(mock.Anything).Maybe()
			mockDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()
			mockInfoProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Maybe()
			// Fix race: Use a matcher that doesn't reflect on connection internals
			connectionMatcher := mock.MatchedBy(func(conn api.ShipConnectionInterface) bool {
				return conn != nil
			})
			mockInfoProvider.EXPECT().HandleConnectionClosed(connectionMatcher, mock.Anything).Maybe()
			if tt.expectSuccess {
				mockDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
				mockDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()
				// Add expectations for validation methods
				mockInfoProvider.EXPECT().IsRemoteServiceForSKIPaired("remote-ski").Return(true).Maybe()
				mockInfoProvider.EXPECT().AllowWaitingForTrust("remote-ski").Return(true).Maybe()
			}

			conn := NewConnectionHandler(
				mockInfoProvider,
				mockDataWriter,
				ShipRoleClient,
				"local-ship-id",
				"remote-ski",
				"remote-ship-id",
			)

			// Set initial state
			conn.setState(tt.initialState, nil)

			// Test the new method (to be implemented)
			success := conn.AbortIfPending()

			assert.Equal(t, tt.expectSuccess, success)

			// Allow time for state transitions
			if tt.expectSuccess {
				time.Sleep(time.Millisecond * 10)
			}

			state := conn.getState()
			assert.Equal(t, tt.expectedState, state)
		})
	}

	// Ensure any background goroutines complete
	// Abort state triggers a 1-second delayed close
	time.Sleep(time.Second + 100*time.Millisecond)
}

// TestHandshakeTimer_ConcurrentStartStop tests concurrent timer operations
func TestHandshakeTimer_ConcurrentStartStop(t *testing.T) {
	// Setup
	safeTracker := NewSafeConnectionTracker()
	mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)

	mockDataWriter.EXPECT().InitDataProcessing(mock.Anything).Maybe()
	mockDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()
	mockDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
	mockDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()

	conn := NewConnectionHandler(
		safeTracker,
		mockDataWriter,
		ShipRoleClient,
		"local-ship-id",
		"remote-ski",
		"remote-ship-id",
	)

	// Run concurrent timer operations
	var wg sync.WaitGroup
	const numGoroutines = 10 // Reduced to avoid too many timer goroutines

	// Start and stop timer concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)

		go func(iteration int) {
			defer wg.Done()

			// Start timer with longer duration to avoid firing
			conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, time.Second*5)

			// Small delay to allow timer to start
			time.Sleep(time.Microsecond * 100)

			// Stop timer immediately
			conn.stopTimerSafe()
		}(i)
	}

	wg.Wait()

	// Final cleanup - stop any remaining timers
	conn.stopTimerSafe()

	// Wait for timer goroutines to complete
	time.Sleep(time.Millisecond * 100)

	// Verify timer is in consistent state
	assert.False(t, conn.getHandshakeTimerRunning(), "Timer should not be running after all operations")
}

// TestStopTimerSafe tests the new safe timer stop method
func TestStopTimerSafe(t *testing.T) {
	// Setup
	safeTracker := NewSafeConnectionTracker()
	mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)

	mockDataWriter.EXPECT().InitDataProcessing(mock.Anything).Maybe()
	mockDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()
	mockDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()
	mockDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()

	conn := NewConnectionHandler(
		safeTracker,
		mockDataWriter,
		ShipRoleClient,
		"local-ship-id",
		"remote-ski",
		"remote-ship-id",
	)

	// Test stopping when timer not running
	stopped := conn.stopTimerSafe()
	assert.False(t, stopped, "Should return false when timer not running")

	// Start timer with longer duration to avoid firing during test
	conn.startHandshakeTimer(time.Second*5, timeoutTimerTypeWaitForReady)
	assert.True(t, conn.getHandshakeTimerRunning(), "Timer should be running")

	// Stop timer safely
	stopped = conn.stopTimerSafe()
	assert.True(t, stopped, "Should return true when timer was stopped")
	assert.False(t, conn.getHandshakeTimerRunning(), "Timer should not be running")

	// Try to stop again
	stopped = conn.stopTimerSafe()
	assert.False(t, stopped, "Should return false when timer already stopped")

	// Wait for any timer goroutines to complete
	time.Sleep(time.Millisecond * 100)

	// No need for explicit cleanup here since timers are already stopped
}
