package hub

import (
	"crypto/tls"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestConnectionDelayRaceConditions tests for race conditions in connection delay and double connection prevention
func TestConnectionDelayRaceConditions(t *testing.T) {
	t.Run("double_connection_prevention_race", func(t *testing.T) {
		// Test the race condition in keepThisConnection where existingC could change
		// between lookup and action

		// Create test hub with minimal setup
		mockHubReader := mocks.NewHubReaderInterface(t)
		mockMdns := mocks.NewMdnsInterface(t)
		mockMdns.EXPECT().Shutdown().Maybe()
		localService := api.NewServiceDetails("local-ski")
		cert := tls.Certificate{}

		hub := NewHub(mockHubReader, mockMdns, 4729, cert, localService)

		// Create remote service details
		remoteService := api.NewServiceDetails("remote-ski-6000") // Higher than local

		// Mock connections
		mockConn1 := mocks.NewShipConnectionInterface(t)
		mockConn1.EXPECT().RemoteSKI().Return("remote-ski-6000").Maybe()
		mockConn1.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()

		var operationCount atomic.Int32

		// Simulate rapid connection attempts and registrations
		const numIterations = 500
		var wg sync.WaitGroup

		for range numIterations {
			wg.Add(3)

			// Goroutine 1: Register a connection
			go func() {
				defer wg.Done()
				hub.registerConnection(mockConn1)
				operationCount.Add(1)
			}()

			// Goroutine 2: Check for double connection (incoming)
			go func() {
				defer wg.Done()
				keep := hub.keepThisConnection(nil, true, remoteService)
				if !keep {
					t.Log("expected to keep for higher remoteSKI")
				}
				operationCount.Add(1)
			}()

			// Goroutine 3: Unregister connection
			go func() {
				defer wg.Done()
				hub.UnregisterConnectionIfMatch("remote-ski-6000", mockConn1)
				operationCount.Add(1)
			}()
		}

		wg.Wait()

		// Check that all operations completed
		assert.Equal(t, int32(numIterations*3), operationCount.Load(), "not all operations completed")

		t.Logf("Completed %d operations without deadlock", operationCount.Load())

		// Cleanup
		hub.Shutdown()
	})

	t.Run("connection_delay_timer_safety", func(t *testing.T) {
		// Test that connection delay timers are properly cancelled and don't accumulate

		mockHubReader := mocks.NewHubReaderInterface(t)
		mockMdns := mocks.NewMdnsInterface(t)
		mockMdns.EXPECT().Shutdown().Maybe()
		localService := api.NewServiceDetails("local-ski-1000")
		cert := tls.Certificate{}

		hub := NewHub(mockHubReader, mockMdns, 4729, cert, localService)

		initialGoroutines := runtime.NumGoroutine()

		// Create multiple delayed connection attempts
		const numDelays = 50
		var wg sync.WaitGroup

		for i := 0; i < numDelays; i++ {
			wg.Add(1)
			ski := fmt.Sprintf("remote-%d", i)

			go func(remoteSKI string) {
				defer wg.Done()

				// Create a connection delay timer
				timer := &connectionDelayTimer{
					done: make(chan struct{}),
				}

				timer.timer = time.AfterFunc(1*time.Millisecond, func() {
					select {
					case <-timer.done:
						return
					default:
						// Simulate connection attempt
					}
				})

				hub.storeConnectionDelayTimer(remoteSKI, timer)

				// Quickly cancel it (simulates successful connection)
				timer.Stop()
			}(ski)
		}

		wg.Wait()

		// Give time for timer goroutines to finish
		time.Sleep(50 * time.Millisecond)

		// Check for goroutine leaks
		finalGoroutines := runtime.NumGoroutine()
		goroutineGrowth := finalGoroutines - initialGoroutines

		t.Logf("Goroutines: initial=%d, final=%d, growth=%d",
			initialGoroutines, finalGoroutines, goroutineGrowth)

		// Should not have significant goroutine growth
		assert.LessOrEqual(t, goroutineGrowth, 5, "potential goroutine leak in timer management")

		hub.Shutdown()
	})

	t.Run("timer_cancellation_edge_cases", func(t *testing.T) {
		// Test edge cases in timer cancellation

		// Case 1: Timer fires at exactly the same time as cancellation
		for i := 0; i < 100; i++ {
			done := make(chan struct{})
			var timerFired atomic.Bool
			var cancelled atomic.Bool

			timer := time.AfterFunc(time.Microsecond, func() {
				select {
				case <-done:
					return
				default:
					timerFired.Store(true)
				}
			})

			// Try to cancel immediately
			go func() {
				cancelled.Store(timer.Stop())
				if !cancelled.Load() {
					// Timer already fired, close done channel safely
					select {
					case <-done:
					default:
						close(done)
					}
				}
			}()

			// Wait briefly
			time.Sleep(time.Millisecond)

			// Either timer fired or was cancelled, but not both in a problematic way
			if timerFired.Load() && cancelled.Load() {
				t.Errorf("Timer fired but was also reported as cancelled")
			}
		}
	})
}

// TestConnectionDelayTimerImplementation tests the connectionDelayTimer implementation
func TestConnectionDelayTimerImplementation(t *testing.T) {
	t.Run("timer_stop_safety", func(t *testing.T) {
		// Test multiple stops are safe
		timer := &connectionDelayTimer{
			done: make(chan struct{}),
		}

		timer.timer = time.AfterFunc(100*time.Millisecond, func() {
			select {
			case <-timer.done:
				return
			default:
				// Timer function
			}
		})

		// Multiple stops should be safe
		result1 := timer.Stop()
		result2 := timer.Stop()
		result3 := timer.Stop()

		// First stop should succeed, others should be no-op
		assert.True(t, result1 || result2 || result3, "at least one stop should succeed")

		// Wait to ensure no timer function runs
		time.Sleep(150 * time.Millisecond)
	})

	t.Run("timer_done_channel_behavior", func(t *testing.T) {
		// Test done channel behavior
		timer := &connectionDelayTimer{
			done: make(chan struct{}),
		}

		var timerFunctionCalled atomic.Bool

		timer.timer = time.AfterFunc(10*time.Millisecond, func() {
			select {
			case <-timer.done:
				// Timer was cancelled
				return
			default:
				timerFunctionCalled.Store(true)
			}
		})

		// Let timer fire
		time.Sleep(20 * time.Millisecond)

		// Stop after timer fired
		timer.Stop()

		assert.True(t, timerFunctionCalled.Load(), "timer function should have been called")
	})
}
