package ship

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestHandshakeTimer tests the timer lifecycle management
func TestHandshakeTimer(t *testing.T) {
	t.Run("timer_cancellation", func(t *testing.T) {
		initialGoroutines := runtime.NumGoroutine()

		// Create test connection
		conn := createTestConnection(t)

		// Set a timer that would fire after 200ms
		conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, 200*time.Millisecond)

		// Cancel it before it fires
		time.Sleep(50 * time.Millisecond)
		done := conn.stopHandshakeTimer()

		// Wait for timer goroutine to complete
		select {
		case <-done:
			// Timer stopped successfully
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timer goroutine did not complete")
		}

		// Verify no goroutine leak
		assert.Eventually(t, func() bool {
			return runtime.NumGoroutine() <= initialGoroutines+1
		}, 2*time.Second, 50*time.Millisecond, "timer goroutine leaked")

		// Verify timer state
		assert.False(t, conn.getHandshakeTimerRunning())
		conn.handshakeTimerMux.Lock()
		assert.Nil(t, conn.handshakeTimer)
		conn.handshakeTimerMux.Unlock()
	})

	t.Run("timer_replacement", func(t *testing.T) {
		initialGoroutines := runtime.NumGoroutine()

		// Create test connection with mocks
		conn, mockInfoProvider, _ := createTestConnectionWithMocks(t)

		// Track state changes to detect timer fires
		stateChanges := atomic.Int32{}
		mockInfoProvider.
			EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).
			RunAndReturn(func(ski string, state model.ShipState) {
				stateChanges.Add(1)
			}).Maybe()

		// Set multiple timers rapidly
		for i := 0; i < 10; i++ {
			conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, time.Duration(100+i*10)*time.Millisecond)
			time.Sleep(5 * time.Millisecond)
		}

		// Wait for potential timer fires
		time.Sleep(500 * time.Millisecond)

		// Only the last timer should fire (tracked by state changes)
		assert.LessOrEqual(t, stateChanges.Load(), int32(2), "too many state changes from timers")

		// Stop any remaining timer
		conn.stopHandshakeTimer()

		// Verify no goroutine leak
		assert.Eventually(t, func() bool {
			return runtime.NumGoroutine() <= initialGoroutines+1
		}, 2*time.Second, 50*time.Millisecond, "timer goroutines leaked")
	})

	t.Run("timer_cleanup_on_close", func(t *testing.T) {
		initialGoroutines := runtime.NumGoroutine()

		// Create test connection
		conn := createTestConnection(t)

		// Set a long timer
		conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, 10*time.Second)

		// Close connection
		conn.CloseConnection(false, 0, "test close")

		// Verify timer was stopped
		assert.Eventually(t, func() bool {
			return runtime.NumGoroutine() <= initialGoroutines+1
		}, 2*time.Second, 50*time.Millisecond, "timer goroutine not cleaned up on close")
	})

	t.Run("concurrent_timer_operations", func(t *testing.T) {
		// Create test connection
		conn := createTestConnection(t)

		// Run concurrent timer operations
		var wg sync.WaitGroup
		errors := make([]error, 100)

		// Start timers
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						errors[idx] = r.(error)
					}
				}()
				conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, time.Duration(50+idx)*time.Millisecond)
			}(i)
		}

		// Stop timers
		for i := 50; i < 100; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						errors[idx] = r.(error)
					}
				}()
				conn.stopHandshakeTimer()
			}(i)
		}

		wg.Wait()

		// Check for panics
		panicCount := 0
		for _, err := range errors {
			if err != nil {
				panicCount++
			}
		}
		assert.Equal(t, 0, panicCount, "concurrent operations caused panics")

		// Final cleanup
		conn.stopHandshakeTimer()
	})

	t.Run("timer_fires_correctly", func(t *testing.T) {
		// Create test connection
		conn := createTestConnection(t)

		// Use a raw timer to test the done channel mechanism
		conn.handshakeTimerMux.Lock()
		done := make(chan struct{})
		conn.handshakeTimerDone = done
		conn.handshakeTimerRunning = true
		conn.handshakeTimer = time.AfterFunc(50*time.Millisecond, func() {
			defer close(done)

			conn.handshakeTimerMux.Lock()
			conn.handshakeTimer = nil
			conn.handshakeTimerRunning = false
			conn.handshakeTimerMux.Unlock()
		})
		conn.handshakeTimerMux.Unlock()

		// Verify timer is running
		assert.True(t, conn.getHandshakeTimerRunning())

		// Wait for done channel to close
		select {
		case <-done:
			// Timer fired successfully
		case <-time.After(200 * time.Millisecond):
			t.Fatal("timer did not fire")
		}

		// Verify timer state after firing
		assert.False(t, conn.getHandshakeTimerRunning())
		conn.handshakeTimerMux.Lock()
		assert.Nil(t, conn.handshakeTimer)
		conn.handshakeTimerMux.Unlock()
	})

	t.Run("stop_timer_multiple_times", func(t *testing.T) {
		// Create test connection
		conn := createTestConnection(t)

		// Set timer
		conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, 100*time.Millisecond)

		// Stop multiple times - should not panic
		for i := 0; i < 5; i++ {
			done := conn.stopHandshakeTimer()
			select {
			case <-done:
				// OK
			case <-time.After(100 * time.Millisecond):
				// OK - channel might not close if timer already stopped
			}
		}

		// Verify clean state
		assert.False(t, conn.getHandshakeTimerRunning())
	})

	t.Run("timer_done_channel_behavior", func(t *testing.T) {
		// Create test connection
		conn := createTestConnection(t)

		// Case 1: Stop before timer fires
		conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, 200*time.Millisecond)
		done1 := conn.stopHandshakeTimer()

		select {
		case <-done1:
			// Should complete quickly since timer was stopped
		case <-time.After(50 * time.Millisecond):
			t.Error("done channel not closed after stop")
		}

		// Case 2: Test that done channel from timer that already fired is closed
		// First stop any timer to start fresh
		conn.stopHandshakeTimer()

		// Set a timer and let it fire naturally
		conn.handshakeTimerMux.Lock()
		done2 := make(chan struct{})
		conn.handshakeTimerDone = done2
		conn.handshakeTimerRunning = true
		fired := false
		conn.handshakeTimer = time.AfterFunc(50*time.Millisecond, func() {
			defer close(done2)

			conn.handshakeTimerMux.Lock()
			conn.handshakeTimer = nil
			conn.handshakeTimerRunning = false
			fired = true
			conn.handshakeTimerMux.Unlock()
		})
		conn.handshakeTimerMux.Unlock()

		// Wait for timer to fire
		select {
		case <-done2:
			// Timer fired
		case <-time.After(200 * time.Millisecond):
			t.Error("timer did not fire")
		}

		// Verify timer fired
		assert.True(t, fired, "timer callback did not execute")

		// Now call stopHandshakeTimer on already fired timer
		done3 := conn.stopHandshakeTimer()

		// Should return immediately closed channel
		select {
		case <-done3:
			// Good - channel is closed
		default:
			t.Error("stopHandshakeTimer should return closed channel for already fired timer")
		}
	})
}

// Helper function to create test connection
func createTestConnection(t *testing.T) *ShipConnection {
	conn, _, _ := createTestConnectionWithMocks(t)
	return conn
}

// Helper function that returns both the connection and the mocks
func createTestConnectionWithMocks(t *testing.T) (*ShipConnection, *mocks.ShipConnectionInfoProviderInterface, *mocks.WebsocketDataWriterInterface) {
	mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
	mockDataHandler := mocks.NewWebsocketDataWriterInterface(t)

	// Setup basic mocks
	mockInfoProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Maybe()
	mockInfoProvider.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Maybe()
	mockDataHandler.EXPECT().InitDataProcessing(mock.Anything).Maybe()
	mockDataHandler.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()
	mockDataHandler.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
	mockDataHandler.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()

	conn := NewConnectionHandler(
		mockInfoProvider,
		mockDataHandler,
		ShipRoleClient,
		"local-id",
		"remote-ski",
		"remote-id",
	)

	return conn, mockInfoProvider, mockDataHandler
}
