package ship

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
)

// TestHandshakeTimerRaceConditions specifically tests for race conditions in timer management
func TestHandshakeTimerRaceConditions(t *testing.T) {
	// This test file should be run with: go test -race

	t.Run("concurrent_set_and_stop", func(t *testing.T) {
		// Use connection without mocks to avoid race conditions
		conn := createTestConnectionNoMocks(t)

		// Number of concurrent operations
		const numOps = 100
		var wg sync.WaitGroup

		// Track successful operations
		var setCount atomic.Int32
		var stopCount atomic.Int32

		// Half the goroutines set timers
		for i := 0; i < numOps/2; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()

				// Vary the duration to create more contention
				duration := time.Duration(10+idx%50) * time.Millisecond
				conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, duration)
				setCount.Add(1)
			}(i)
		}

		// Other half stop timers
		for i := 0; i < numOps/2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()

				done := conn.stopHandshakeTimer()
				select {
				case <-done:
					// Timer stopped or already fired
				case <-time.After(100 * time.Millisecond):
					// Timeout is OK, timer might not have been set
				}
				stopCount.Add(1)
			}()
		}

		wg.Wait()

		// Verify all operations completed
		assert.Equal(t, int32(numOps/2), setCount.Load(), "not all set operations completed")
		assert.Equal(t, int32(numOps/2), stopCount.Load(), "not all stop operations completed")

		// Final cleanup
		conn.stopHandshakeTimer()
	})

	t.Run("state_changes_during_timer_operations", func(t *testing.T) {
		// Use connection without mocks to avoid race conditions
		conn := createTestConnectionNoMocks(t)

		var wg sync.WaitGroup

		// Goroutine 1: Rapidly change states
		wg.Add(1)
		go func() {
			defer wg.Done()

			states := []model.ShipMessageExchangeState{
				model.CmiStateInitStart,
				model.CmiStateClientWait,
				model.SmeHelloStateReadyInit,
				model.SmeHelloStatePendingInit,
				model.SmeProtHStateClientListenChoice,
			}

			for i := 0; i < 50; i++ {
				state := states[i%len(states)]
				conn.setState(state, nil)
				time.Sleep(time.Millisecond)
			}
		}()

		// Goroutine 2: Set timers during state changes
		wg.Add(1)
		go func() {
			defer wg.Done()

			for i := 0; i < 50; i++ {
				conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, 20*time.Millisecond)
				time.Sleep(time.Millisecond)
			}
		}()

		// Goroutine 3: Stop timers during state changes
		wg.Add(1)
		go func() {
			defer wg.Done()

			for i := 0; i < 50; i++ {
				conn.stopHandshakeTimer()
				time.Sleep(time.Millisecond)
			}
		}()

		wg.Wait()

		// Verify system is still consistent
		_, err := conn.ShipHandshakeState()
		assert.NoError(t, err, "connection in error state after concurrent operations")
	})

	t.Run("timer_expiry_during_stop", func(t *testing.T) {
		// Use connection without mocks to avoid race conditions
		conn := createTestConnectionNoMocks(t)

		// This tests the race between timer firing and stopping
		for i := 0; i < 100; i++ {
			// Set a very short timer
			conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, time.Microsecond)

			// Immediately try to stop it
			done := conn.stopHandshakeTimer()

			// The done channel should always be valid
			select {
			case <-done:
				// Either stopped or fired
			case <-time.After(50 * time.Millisecond):
				t.Fatal("done channel not closed")
			}
		}
	})

	t.Run("multiple_readers_of_done_channel", func(t *testing.T) {
		conn := createTestConnection(t)

		// Set a timer
		conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, 50*time.Millisecond)

		// Stop and get done channel
		done := conn.stopHandshakeTimer()

		// Multiple goroutines waiting on the same done channel
		var wg sync.WaitGroup
		successCount := atomic.Int32{}

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()

				select {
				case <-done:
					successCount.Add(1)
				case <-time.After(100 * time.Millisecond):
					// Should not timeout
				}
			}()
		}

		wg.Wait()

		// All goroutines should have received the close signal
		assert.Equal(t, int32(10), successCount.Load(), "not all goroutines received done signal")
	})

	t.Run("stress_test_timer_lifecycle", func(t *testing.T) {
		if testing.Short() {
			t.Skip("Skipping stress test in short mode")
		}

		initialGoroutines := runtime.NumGoroutine()
		// Use basic connection without mocks to avoid race conditions in stress test
		conn := createTestConnectionNoMocks(t)

		// Run for 5 seconds with continuous timer operations
		stop := make(chan struct{})
		var wg sync.WaitGroup

		// Multiple goroutines performing timer operations
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				for {
					select {
					case <-stop:
						return
					default:
						if id%2 == 0 {
							// Even IDs set timers
							duration := time.Duration(5+id) * time.Millisecond
							conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, duration)
						} else {
							// Odd IDs stop timers
							done := conn.stopHandshakeTimer()
							select {
							case <-done:
							case <-time.After(10 * time.Millisecond):
							}
						}
					}
				}
			}(i)
		}

		// Let it run
		time.Sleep(5 * time.Second)

		// Stop all goroutines
		close(stop)
		wg.Wait()

		// Final cleanup
		conn.stopHandshakeTimer()

		// Verify no goroutine leaks
		assert.Eventually(t, func() bool {
			current := runtime.NumGoroutine()
			return current <= initialGoroutines+5
		}, 2*time.Second, 100*time.Millisecond, "goroutine leak detected")
	})

	t.Run("timer_operations_during_connection_close", func(t *testing.T) {
		// Use connection without mocks to avoid race conditions
		conn := createTestConnectionNoMocks(t)

		var wg sync.WaitGroup

		// Start timer operations
		wg.Add(1)
		go func() {
			defer wg.Done()

			for i := 0; i < 50; i++ {
				conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, 10*time.Millisecond)
				time.Sleep(time.Millisecond)
			}
		}()

		// Concurrently close the connection
		wg.Add(1)
		go func() {
			defer wg.Done()

			time.Sleep(25 * time.Millisecond) // Let some timers be set
			conn.CloseConnection(false, 0, "race test")
		}()

		// Continue timer operations after close
		wg.Add(1)
		go func() {
			defer wg.Done()

			time.Sleep(30 * time.Millisecond) // After close
			for i := 0; i < 20; i++ {
				// These should handle closed connection gracefully
				conn.stopHandshakeTimer()
				time.Sleep(time.Millisecond)
			}
		}()

		wg.Wait()

		// Give time for close to complete
		time.Sleep(50 * time.Millisecond)

		// Verify connection is in a valid end state
		state, _ := conn.ShipHandshakeState()
		// After closing, the connection could be in various states depending on timing
		validEndStates := []model.ShipMessageExchangeState{
			model.SmeStateError,                // Error state after close
			model.CmiStateInitStart,            // Initial state if close happened very early
			model.CmiStateClientWait,           // Client waiting state (if handshake was in progress)
			model.CmiStateServerWait,           // Server waiting state
			model.SmeHelloState,                // Hello state
			model.SmeHelloStateAbortDone,       // Abort completed
			model.SmeHelloStateRemoteAbortDone, // Remote abort
		}
		assert.Contains(t, validEndStates, state,
			"connection should be in a valid state after close (actual: %v)", state)
	})
}

// TestTimerDoneChannelRace specifically tests the done channel implementation
func TestTimerDoneChannelRace(t *testing.T) {
	t.Run("done_channel_close_race", func(t *testing.T) {
		// Direct test of the pattern used in stopHandshakeTimer
		for i := 0; i < 1000; i++ {
			done := make(chan struct{})
			var closeOnce sync.Once

			// Simulate timer firing
			go func() {
				time.Sleep(time.Microsecond * time.Duration(i%10))
				closeOnce.Do(func() {
					close(done)
				})
			}()

			// Simulate stopHandshakeTimer
			go func() {
				closeOnce.Do(func() {
					close(done)
				})
			}()

			// Wait for done
			select {
			case <-done:
				// Success
			case <-time.After(100 * time.Millisecond):
				t.Fatal("done channel not closed")
			}
		}
	})
}
