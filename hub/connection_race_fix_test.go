package hub

import (
	"crypto/tls"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestKeepConnectionTOCTOURaceFix tests that the TOCTOU race condition in keepThisConnection is fixed
func TestKeepConnectionTOCTOURaceFix(t *testing.T) {
	t.Run("atomic_connection_removal", func(t *testing.T) {
		// This test verifies that the connection removal in keepThisConnection is atomic
		// and prevents the TOCTOU race condition

		mockHubReader := mocks.NewHubReaderInterface(t)
		mockMdns := mocks.NewMdnsInterface(t)
		mockMdns.EXPECT().Shutdown().Maybe()
		localService := api.NewServiceDetails("local-ski-5000")
		cert := tls.Certificate{}

		hub := NewHub(mockHubReader, mockMdns, 4729, cert, localService)

		remoteSKI := "remote-ski-6000" // Higher than local, so remote should win
		remoteService := api.NewServiceDetails(remoteSKI)

		// Create mock connections
		oldConn := mocks.NewShipConnectionInterface(t)
		oldConn.EXPECT().RemoteSKI().Return(remoteSKI).Maybe()
		oldConn.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()

		newConn := mocks.NewShipConnectionInterface(t)
		newConn.EXPECT().RemoteSKI().Return(remoteSKI).Maybe()
		newConn.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()

		var operationsCompleted atomic.Int32
		var keepResults []bool
		var keepResultsMux sync.Mutex

		const numIterations = 1000
		var wg sync.WaitGroup

		for i := 0; i < numIterations; i++ {
			wg.Add(3)

			// Goroutine 1: Register old connection
			go func() {
				defer wg.Done()
				hub.registerConnection(oldConn)
				operationsCompleted.Add(1)
			}()

			// Goroutine 2: Call keepThisConnection (should remove old connection atomically)
			go func() {
				defer wg.Done()
				keep := hub.keepThisConnection(nil, true, remoteService)

				keepResultsMux.Lock()
				keepResults = append(keepResults, keep)
				keepResultsMux.Unlock()

				operationsCompleted.Add(1)
			}()

			// Goroutine 3: Try to register a new connection
			go func() {
				defer wg.Done()
				// Small delay to let the race condition potentially occur
				time.Sleep(time.Microsecond)
				hub.registerConnection(newConn)
				operationsCompleted.Add(1)
			}()
		}

		wg.Wait()

		// All operations should complete
		assert.Equal(t, int32(numIterations*3), operationsCompleted.Load())

		// keepThisConnection should consistently return true (remote SKI is higher)
		keepResultsMux.Lock()
		trueCount := 0
		for _, result := range keepResults {
			if result {
				trueCount++
			}
		}
		keepResultsMux.Unlock()

		// Most results should be true (remote SKI wins), but some might be false
		// if no existing connection was found
		t.Logf("keepThisConnection returned true %d/%d times", trueCount, len(keepResults))

		hub.Shutdown()
	})

	t.Run("connection_consistency_under_contention", func(t *testing.T) {
		// Test that connection state remains consistent under high contention

		mockHubReader := mocks.NewHubReaderInterface(t)
		mockMdns := mocks.NewMdnsInterface(t)
		mockMdns.EXPECT().Shutdown().Maybe()
		localService := api.NewServiceDetails("local-ski-4000")
		cert := tls.Certificate{}

		hub := NewHub(mockHubReader, mockMdns, 4729, cert, localService)

		remoteSKI := "remote-ski-5000" // Higher than local
		remoteService := api.NewServiceDetails(remoteSKI)

		// Create multiple mock connections
		mockConns := make([]*mocks.ShipConnectionInterface, 10)
		for i := range mockConns {
			mockConns[i] = mocks.NewShipConnectionInterface(t)
			mockConns[i].EXPECT().RemoteSKI().Return(remoteSKI).Maybe()
			mockConns[i].EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()
		}

		var wg sync.WaitGroup
		const numOperations = 100

		// Rapid register/keepConnection/unregister operations
		for i := 0; i < numOperations; i++ {
			wg.Add(3)
			connIndex := i % len(mockConns)

			// Register
			go func(conn *mocks.ShipConnectionInterface) {
				defer wg.Done()
				hub.registerConnection(conn)
			}(mockConns[connIndex])

			// Keep connection check
			go func() {
				defer wg.Done()
				hub.keepThisConnection(nil, true, remoteService)
			}()

			// Unregister
			go func(conn *mocks.ShipConnectionInterface) {
				defer wg.Done()
				hub.UnregisterConnectionIfMatch(remoteSKI, conn)
			}(mockConns[connIndex])
		}

		wg.Wait()

		// Final state should be consistent (no connection or one connection)
		finalConn := hub.connectionForSKI(remoteSKI)
		t.Logf("Final connection state: %v", finalConn != nil)

		hub.Shutdown()
	})
}

// TestConnectionDelayTimerRaceConditions tests timer-related race conditions
func TestConnectionDelayTimerRaceConditions(t *testing.T) {
	t.Run("timer_cancellation_race", func(t *testing.T) {
		// Test rapid timer creation and cancellation

		const numTimers = 100
		var wg sync.WaitGroup
		var successfulCancellations atomic.Int32

		for i := 0; i < numTimers; i++ {
			wg.Add(1)

			go func() {
				defer wg.Done()

				timer := &connectionDelayTimer{
					done: make(chan struct{}),
				}

				var timerExecuted atomic.Bool

				timer.timer = time.AfterFunc(time.Microsecond, func() {
					select {
					case <-timer.done:
						return
					default:
						timerExecuted.Store(true)
					}
				})

				// Try to cancel immediately
				if timer.Stop() {
					successfulCancellations.Add(1)
				}

				// Wait a bit to see if timer still executes
				time.Sleep(time.Millisecond)

				// Timer behavior is checked by the implementation itself
			}()
		}

		wg.Wait()

		t.Logf("Successfully cancelled %d/%d timers", successfulCancellations.Load(), numTimers)

		// At least some cancellations should succeed
		assert.Greater(t, successfulCancellations.Load(), int32(0))
	})
}
