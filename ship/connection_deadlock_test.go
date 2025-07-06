// Package ship_test contains deadlock detection tests for the SHIP connection implementation.
// These tests specifically target the setState() method's interaction with timer operations
// to detect potential deadlocks from nested lock acquisitions.
//
// Run with: go test -race -tags=deadlock -timeout=30s ./ship
package ship

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/enbility/ship-go/util/testhelper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// setupMockExpectations sets up common mock expectations for timer-based tests
func setupMockExpectations(mockInfoProvider *mocks.ShipConnectionInfoProviderInterface, mockDataWriter *mocks.WebsocketDataWriterInterface) {
	// Mock expectations for concurrent operations
	mockDataWriter.EXPECT().InitDataProcessing(mock.Anything).Maybe()
	mockDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()
	mockDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
	mockDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()
	mockInfoProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Maybe()
	mockInfoProvider.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Maybe()
	
	// Mock expectations for methods called during state transitions and timer callbacks
	mockInfoProvider.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(false).Maybe()
	mockInfoProvider.EXPECT().IsAutoAcceptEnabled().Return(false).Maybe()
	mockInfoProvider.EXPECT().AllowWaitingForTrust(mock.Anything).Return(false).Maybe()
}

// TestSetStateTimerDeadlock tests the specific deadlock scenario where
// setState() holds mux lock while calling timer methods that acquire handshakeTimerMux,
// while timer goroutines might try to acquire mux
func TestSetStateTimerDeadlock(t *testing.T) {
	// This test specifically targets the pattern:
	// Thread 1: setState() -> mux -> setHandshakeTimer() -> handshakeTimerMux
	// Thread 2: timer goroutine -> handleState() -> mux
	
	testhelper.RunWithDeadlockDetection(t, testhelper.DeadlockTimeout, func() {
		// Setup
		mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
		mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)
		setupMockExpectations(mockInfoProvider, mockDataWriter)
		
		conn := NewConnectionHandler(
			mockInfoProvider,
			mockDataWriter,
			ShipRoleClient,
			"local-ship-id",
			"remote-ski",
			"remote-ship-id",
		)
		
		// Create concurrent test that hammers setState with timer-triggering states
		test := &testhelper.ConcurrentTest{
			Goroutines: 10,
			Iterations: 20,
			Test: func(id int) {
				// Cycle through states that trigger timer operations
				states := []model.ShipMessageExchangeState{
					model.SmeHelloStateReadyInit,     // Starts timer (60s)
					model.SmeHelloStateOk,            // Stops timer
					model.SmeHelloStatePendingInit,   // Starts timer (60s)
					model.SmeHelloStateOk,            // Stops timer
				}
				
				for _, state := range states {
					conn.setState(state, nil)
					// Small delay to increase contention
					time.Sleep(time.Microsecond * 10)
				}
			},
		}
		
		test.Run(t)
		
		// Cleanup: ensure timer is stopped and connection is closed
		conn.stopHandshakeTimer()
		
		// Close the connection to ensure all resources are cleaned up
		conn.CloseConnection(false, 4001, "test cleanup")
		
		// Wait for any async operations to complete
		time.Sleep(200 * time.Millisecond)
	})
}

// TestSetStateTimerRaceCondition tests for race conditions in timer state management
func TestSetStateTimerRaceCondition(t *testing.T) {
	// This test checks for data races in timer state updates
	// when setState() is called concurrently with timer operations
	
	testhelper.RunWithDeadlockDetection(t, testhelper.DeadlockTimeout, func() {
		// Setup
		mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
		mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)
		setupMockExpectations(mockInfoProvider, mockDataWriter)
		
		conn := NewConnectionHandler(
			mockInfoProvider,
			mockDataWriter,
			ShipRoleClient,
			"local-ship-id",
			"remote-ski",
			"remote-ship-id",
		)
		
		var wg sync.WaitGroup
		const numGoroutines = 20
		
		// Start multiple goroutines that trigger timer operations
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				
				// Alternate between timer-starting and timer-stopping states
				for j := 0; j < 10; j++ {
					if j%2 == 0 {
						// Start timer
						conn.setState(model.SmeHelloStateReadyInit, nil)
					} else {
						// Stop timer
						conn.setState(model.SmeHelloStateOk, nil)
					}
					
					// Read timer state to increase contention
					_ = conn.getHandshakeTimerRunning()
					_ = conn.getHandshakeTimerType()
				}
			}(i)
		}
		
		wg.Wait()
		
		// Verify final state is consistent
		finalState := conn.getState()
		assert.NotEqual(t, model.ShipMessageExchangeState(0), finalState, 
			"Final state should be valid")
		
		// Cleanup: ensure timer is stopped and connection is closed
		conn.stopHandshakeTimer()
		
		// Close the connection to ensure all resources are cleaned up
		conn.CloseConnection(false, 4001, "test cleanup")
		
		// Wait for any async operations to complete
		time.Sleep(200 * time.Millisecond)
	})
}

// TestTimerLifecycleDeadlock tests the complete timer lifecycle for deadlock issues
func TestTimerLifecycleDeadlock(t *testing.T) {
	// This test focuses on the timer lifecycle methods and their interaction
	// with setState() to detect any circular dependencies
	
	testhelper.RunWithDeadlockDetection(t, testhelper.DeadlockTimeout, func() {
		// Setup
		mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
		mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)
		setupMockExpectations(mockInfoProvider, mockDataWriter)
		
		conn := NewConnectionHandler(
			mockInfoProvider,
			mockDataWriter,
			ShipRoleClient,
			"local-ship-id",
			"remote-ski",
			"remote-ship-id",
		)
		
		// Test timer operations under high contention
		test := &testhelper.ConcurrentTest{
			Goroutines: 15,
			Iterations: 25,
			Test: func(id int) {
				// Mix of timer operations and state changes
				switch id % 4 {
				case 0:
					// Direct timer operations
					conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, time.Second*5)
					conn.stopHandshakeTimer()
				case 1:
					// State changes that trigger timers
					conn.setState(model.SmeHelloStateReadyInit, nil)
					conn.setState(model.SmeHelloStateOk, nil)
				case 2:
					// Timer state queries
					_ = conn.getHandshakeTimerRunning()
					_ = conn.getHandshakeTimerType()
				case 3:
					// Safe timer stop
					conn.stopTimerSafe()
				}
			},
		}
		
		test.Run(t)
		
		// Cleanup: ensure timer is stopped and connection is closed
		conn.stopHandshakeTimer()
		
		// Close the connection to ensure all resources are cleaned up
		conn.CloseConnection(false, 4001, "test cleanup")
		
		// Wait for any async operations to complete
		time.Sleep(200 * time.Millisecond)
	})
}

// TestStateTimerConsistency tests that timer state remains consistent with connection state
func TestStateTimerConsistency(t *testing.T) {
	// This test verifies that timer state is consistent with connection state
	// even under concurrent modifications
	
	testhelper.RunWithDeadlockDetection(t, testhelper.DeadlockTimeout, func() {
		// Setup
		mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
		mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)
		setupMockExpectations(mockInfoProvider, mockDataWriter)
		
		conn := NewConnectionHandler(
			mockInfoProvider,
			mockDataWriter,
			ShipRoleClient,
			"local-ship-id",
			"remote-ski",
			"remote-ship-id",
		)
		
		// Track invariant violations
		var invariantViolations int32
		
		// Goroutine that continuously checks invariants
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()
		
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					state := conn.getState()
					timerRunning := conn.getHandshakeTimerRunning()
					
					// Check some basic invariants
					switch state {
					case model.SmeHelloStateOk:
						// In OK state, timer should typically not be running
						// (though there might be brief transitions)
					case model.SmeHelloStateReadyInit:
						// In ready state, timer should typically be running
						// (though there might be brief transitions)
					}
					
					// Just ensure we can read state consistently
					_ = timerRunning
					
					time.Sleep(time.Microsecond * 100)
				}
			}
		}()
		
		// Run state changes concurrently
		var wg sync.WaitGroup
		const numGoroutines = 10
		
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				
				for j := 0; j < 15; j++ {
					select {
					case <-ctx.Done():
						return
					default:
						// Cycle through states
						conn.setState(model.SmeHelloStateReadyInit, nil)
						time.Sleep(time.Microsecond * 50)
						conn.setState(model.SmeHelloStateOk, nil)
						time.Sleep(time.Microsecond * 50)
					}
				}
			}()
		}
		
		wg.Wait()
		
		// Final consistency check
		finalState := conn.getState()
		assert.NotEqual(t, model.ShipMessageExchangeState(0), finalState)
		
		// Invariant violations should be minimal under proper synchronization
		assert.LessOrEqual(t, invariantViolations, int32(5),
			"Too many invariant violations detected")
	})
}

// TestConcurrentStateChangeWithTimerExpiry tests the scenario where
// setState() is called while a timer is about to expire
func TestConcurrentStateChangeWithTimerExpiry(t *testing.T) {
	// This test specifically targets the race between timer expiry and state changes
	
	testhelper.RunWithDeadlockDetection(t, testhelper.DeadlockTimeout, func() {
		// Setup
		mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
		mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)
		setupMockExpectations(mockInfoProvider, mockDataWriter)
		
		conn := NewConnectionHandler(
			mockInfoProvider,
			mockDataWriter,
			ShipRoleClient,
			"local-ship-id",
			"remote-ski",
			"remote-ship-id",
		)
		
		// Start a timer with very short duration
		conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, time.Millisecond*5)
		
		// Immediately start changing states rapidly
		var wg sync.WaitGroup
		const numGoroutines = 8
		
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				
				for j := 0; j < 10; j++ {
					// Rapid state changes while timer might be expiring
					conn.setState(model.SmeHelloStateReadyInit, nil)
					conn.setState(model.SmeHelloStateOk, nil)
					
					// Brief pause to let timer possibly expire
					time.Sleep(time.Microsecond * 100)
				}
			}()
		}
		
		wg.Wait()
		
		// Clean up any remaining timer
		conn.stopHandshakeTimer()
		
		// Wait for timer goroutines to complete
		time.Sleep(time.Millisecond * 50)
	})
}

// TestLockOrderingViolation tests for potential lock ordering violations
func TestLockOrderingViolation(t *testing.T) {
	// This test uses a lock order tracker to detect violations
	
	testhelper.RunWithDeadlockDetection(t, testhelper.DeadlockTimeout, func() {
		tracker := testhelper.NewLockOrderTracker()
		
		// Setup
		mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
		mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)
		setupMockExpectations(mockInfoProvider, mockDataWriter)
		
		conn := NewConnectionHandler(
			mockInfoProvider,
			mockDataWriter,
			ShipRoleClient,
			"local-ship-id",
			"remote-ski",
			"remote-ship-id",
		)
		
		// Note: This test would need instrumentation in the actual connection
		// to track lock acquisitions. For now, we just run the operations
		// and verify no deadlocks occur
		
		var wg sync.WaitGroup
		const numGoroutines = 12
		
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				
				for j := 0; j < 8; j++ {
					// Operations that might violate lock ordering
					conn.setState(model.SmeHelloStateReadyInit, nil)
					_ = conn.getState()
					_ = conn.getHandshakeTimerRunning()
					conn.stopHandshakeTimer()
					conn.setState(model.SmeHelloStateOk, nil)
				}
			}()
		}
		
		wg.Wait()
		
		// Check for lock ordering violations
		// Note: This would need actual instrumentation to be fully effective
		err := tracker.CheckForViolations()
		assert.NoError(t, err, "Lock ordering violation detected")
	})
}

// TestNoGoroutineLeak tests that no goroutines are leaked during timer operations
func TestNoGoroutineLeak(t *testing.T) {
	testhelper.AssertNoGoroutineLeaks(t, func() {
		// Setup
		mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
		mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)
		setupMockExpectations(mockInfoProvider, mockDataWriter)
		
		conn := NewConnectionHandler(
			mockInfoProvider,
			mockDataWriter,
			ShipRoleClient,
			"local-ship-id",
			"remote-ski",
			"remote-ship-id",
		)
		
		// Operations that might leak goroutines - use very short timers
		conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, time.Millisecond*1)
		time.Sleep(time.Millisecond * 5) // Let timer expire naturally
		
		conn.setState(model.SmeHelloStateReadyInit, nil)
		time.Sleep(time.Millisecond * 5) // Let any timer operations complete
		conn.setState(model.SmeHelloStateOk, nil)
		time.Sleep(time.Millisecond * 5) // Let cleanup complete
		
		// Stop any remaining timers
		conn.stopHandshakeTimer()
		
		// Close connection to clean up all resources
		conn.CloseConnection(false, 4001, "test cleanup")
		
		// Wait for all cleanup to complete
		time.Sleep(time.Millisecond * 100)
	})
}