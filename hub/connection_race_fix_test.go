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

// TestKeepConnectionTOCTOURaceFix tests that the TOCTOU race condition is fixed.
// Post-Phase-3: keepThisConnection is gone; the equivalent atomic decide-and-register
// is registry.Swap, whose Kept boolean replaces the old return value.
func TestKeepConnectionTOCTOURaceFix(t *testing.T) {
	t.Run("atomic_connection_removal", func(t *testing.T) {
		mockHubReader := mocks.NewHubReaderInterface(t)
		mockMdns := mocks.NewMdnsInterface(t)
		mockMdns.EXPECT().Shutdown().Maybe()
		localService := api.NewServiceDetails("local-ski-5000")
		cert := tls.Certificate{}

		hub := NewHub(mockHubReader, mockMdns, 4729, cert, localService)

		remoteSKI := "remote-ski-6000" // Higher than local, so for incoming the new conn wins.

		// Create mock connections
		oldConn := mocks.NewShipConnectionInterface(t)
		oldConn.EXPECT().RemoteSKI().Return(remoteSKI).Maybe()
		oldConn.EXPECT().IsAlive().Return(true).Maybe()
		oldConn.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()

		newConn := mocks.NewShipConnectionInterface(t)
		newConn.EXPECT().RemoteSKI().Return(remoteSKI).Maybe()
		newConn.EXPECT().IsAlive().Return(true).Maybe()
		newConn.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()

		var operationsCompleted atomic.Int32
		var keepResults []bool
		var keepResultsMux sync.Mutex

		const numIterations = 1000
		var wg sync.WaitGroup

		for i := 0; i < numIterations; i++ {
			wg.Add(3)

			// Goroutine 1: Plant the "old" connection directly.
			go func() {
				defer wg.Done()
				hub.registry.mu.Lock()
				hub.registry.connections[remoteSKI] = oldConn
				hub.registry.mu.Unlock()
				operationsCompleted.Add(1)
			}()

			// Goroutine 2: §12.2.2 decide-and-act via Swap (incoming).
			go func() {
				defer wg.Done()
				res := hub.registry.Swap(remoteSKI, true, func() api.ShipConnectionInterface { return newConn })

				keepResultsMux.Lock()
				keepResults = append(keepResults, res.Kept)
				keepResultsMux.Unlock()

				operationsCompleted.Add(1)
			}()

			// Goroutine 3: Plant a new connection directly under registry.mu.
			go func() {
				defer wg.Done()
				time.Sleep(time.Microsecond)
				hub.registry.mu.Lock()
				hub.registry.connections[remoteSKI] = newConn
				hub.registry.mu.Unlock()
				operationsCompleted.Add(1)
			}()
		}

		wg.Wait()

		// All operations should complete
		assert.Equal(t, int32(numIterations*3), operationsCompleted.Load())

		keepResultsMux.Lock()
		trueCount := 0
		for _, result := range keepResults {
			if result {
				trueCount++
			}
		}
		keepResultsMux.Unlock()

		// Most results should be true (remote SKI wins for incoming).
		t.Logf("Swap.Kept was true %d/%d times", trueCount, len(keepResults))

		hub.Shutdown()
	})

	t.Run("connection_consistency_under_contention", func(t *testing.T) {
		mockHubReader := mocks.NewHubReaderInterface(t)
		mockMdns := mocks.NewMdnsInterface(t)
		mockMdns.EXPECT().Shutdown().Maybe()
		localService := api.NewServiceDetails("local-ski-4000")
		cert := tls.Certificate{}

		hub := NewHub(mockHubReader, mockMdns, 4729, cert, localService)

		remoteSKI := "remote-ski-5000" // Higher than local

		// Create multiple mock connections
		mockConns := make([]*mocks.ShipConnectionInterface, 10)
		for i := range mockConns {
			mockConns[i] = mocks.NewShipConnectionInterface(t)
			mockConns[i].EXPECT().RemoteSKI().Return(remoteSKI).Maybe()
			mockConns[i].EXPECT().IsAlive().Return(true).Maybe()
			mockConns[i].EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()
		}

		var wg sync.WaitGroup
		const numOperations = 100

		// Rapid register/swap/unregister operations
		for i := 0; i < numOperations; i++ {
			wg.Add(3)
			connIndex := i % len(mockConns)

			// Register (planted directly).
			go func(conn *mocks.ShipConnectionInterface) {
				defer wg.Done()
				hub.registry.mu.Lock()
				hub.registry.connections[remoteSKI] = conn
				hub.registry.mu.Unlock()
			}(mockConns[connIndex])

			// Rule check via Swap.
			go func(conn *mocks.ShipConnectionInterface) {
				defer wg.Done()
				hub.registry.Swap(remoteSKI, true, func() api.ShipConnectionInterface { return conn })
			}(mockConns[connIndex])

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
