package integration

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/enbility/ship-go/ship"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestTimerIntegration tests timer behavior in realistic scenarios
func TestTimerIntegration(t *testing.T) {
	t.Run("handshake_timeout_cleanup", func(t *testing.T) {
		initialGoroutines := runtime.NumGoroutine()
		
		// Create mocked components
		mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
		mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)
		
		// Set up expectations
		mockInfoProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Maybe()
		mockInfoProvider.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Maybe()
		mockInfoProvider.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(false).Maybe()
		mockInfoProvider.EXPECT().IsAutoAcceptEnabled().Return(false).Maybe()
		
		mockDataWriter.EXPECT().InitDataProcessing(mock.Anything).Maybe()
		mockDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()
		mockDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
		mockDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()
		
		// Create connection handler
		conn := ship.NewConnectionHandler(
			mockInfoProvider,
			mockDataWriter,
			ship.ShipRoleServer,
			"local-id",
			"remote-ski",
			"remote-id",
		)
		
		// Start handshake process which sets timers
		conn.Run()
		
		// Let timers be set
		time.Sleep(50 * time.Millisecond)
		
		// Close connection to trigger cleanup
		conn.CloseConnection(false, 0, "test cleanup")
		
		// Verify goroutines are cleaned up
		assert.Eventually(t, func() bool {
			current := runtime.NumGoroutine()
			t.Logf("Goroutines: initial=%d, current=%d", initialGoroutines, current)
			return current <= initialGoroutines+2 // Allow for test framework overhead
		}, 2*time.Second, 50*time.Millisecond, "timer goroutines leaked")
	})
	
	t.Run("multiple_connections_timer_cleanup", func(t *testing.T) {
		initialGoroutines := runtime.NumGoroutine()
		
		numConnections := 10
		connections := make([]*ship.ShipConnection, numConnections)
		var wg sync.WaitGroup
		
		// Create multiple connections concurrently
		for i := 0; i < numConnections; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				
				mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
				mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)
				
				// Set up expectations
				mockInfoProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Maybe()
				mockInfoProvider.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Maybe()
				mockInfoProvider.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(false).Maybe()
				mockInfoProvider.EXPECT().IsAutoAcceptEnabled().Return(false).Maybe()
				
				mockDataWriter.EXPECT().InitDataProcessing(mock.Anything).Maybe()
				mockDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()
				mockDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
				mockDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()
				
				conn := ship.NewConnectionHandler(
					mockInfoProvider,
					mockDataWriter,
					ship.ShipRoleClient,
					"local-id",
					"remote-ski-"+string(rune(idx)),
					"remote-id-"+string(rune(idx)),
				)
				
				connections[idx] = conn
				conn.Run()
			}(i)
		}
		
		wg.Wait()
		
		// Let timers run for a bit
		time.Sleep(100 * time.Millisecond)
		
		// Close all connections
		for _, conn := range connections {
			conn.CloseConnection(false, 0, "test cleanup")
		}
		
		// Verify all timers cleaned up
		assert.Eventually(t, func() bool {
			current := runtime.NumGoroutine()
			t.Logf("Multiple connections cleanup: initial=%d, current=%d", initialGoroutines, current)
			return current <= initialGoroutines+5
		}, 3*time.Second, 100*time.Millisecond, "timer goroutines leaked with multiple connections")
	})
	
	t.Run("timer_state_transitions", func(t *testing.T) {
		// Test that timers are properly managed during state transitions
		mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
		mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)
		
		stateChanges := make([]model.ShipState, 0)
		var stateChangesMux sync.Mutex
		
		// Track state changes
		mockInfoProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).
			RunAndReturn(func(ski string, state model.ShipState) {
				stateChangesMux.Lock()
				stateChanges = append(stateChanges, state)
				stateChangesMux.Unlock()
			}).Maybe()
		
		mockInfoProvider.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Maybe()
		mockInfoProvider.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(true).Maybe() // Auto-approve
		mockInfoProvider.EXPECT().IsAutoAcceptEnabled().Return(false).Maybe()
		mockInfoProvider.EXPECT().SetupRemoteDevice(mock.Anything, mock.Anything).Return(nil).Maybe()
		
		mockDataWriter.EXPECT().InitDataProcessing(mock.Anything).Maybe()
		mockDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()
		mockDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
		mockDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()
		
		conn := ship.NewConnectionHandler(
			mockInfoProvider,
			mockDataWriter,
			ship.ShipRoleServer,
			"local-id",
			"remote-ski",
			"remote-id",
		)
		
		// Start connection
		conn.Run()
		
		// Wait for initial state
		time.Sleep(50 * time.Millisecond)
		
		// Trigger some state transitions
		conn.HandleIncomingWebsocketMessage(model.ShipInit)
		
		// Wait for processing
		time.Sleep(100 * time.Millisecond)
		
		// Close connection
		conn.CloseConnection(false, 0, "test complete")
		
		// Verify states were tracked
		stateChangesMux.Lock()
		assert.Greater(t, len(stateChanges), 0, "no state changes recorded")
		stateChangesMux.Unlock()
	})
	
	t.Run("timer_error_recovery", func(t *testing.T) {
		// Test timer cleanup when errors occur
		mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
		mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)
		
		errorCount := atomic.Int32{}
		
		// Set up to trigger errors
		mockInfoProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Maybe()
		mockInfoProvider.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Maybe()
		mockInfoProvider.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(false).Maybe()
		mockInfoProvider.EXPECT().IsAutoAcceptEnabled().Return(false).Maybe()
		
		mockDataWriter.EXPECT().InitDataProcessing(mock.Anything).Maybe()
		mockDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).
			RunAndReturn(func(msg []byte) error {
				count := errorCount.Add(1)
				if count > 2 {
					return errors.New("write error")
				}
				return nil
			}).Maybe()
		mockDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
		mockDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()
		
		conn := ship.NewConnectionHandler(
			mockInfoProvider,
			mockDataWriter,
			ship.ShipRoleClient,
			"local-id",
			"remote-ski",
			"remote-id",
		)
		
		// Start connection
		conn.Run()
		
		// Let it run and encounter errors
		time.Sleep(200 * time.Millisecond)
		
		// Report connection error
		conn.ReportConnectionError(errors.New("test error"))
		
		// Verify connection handles error gracefully
		state, err := conn.ShipHandshakeState()
		assert.NotNil(t, err, "expected error state")
		assert.Equal(t, model.SmeStateError, state)
	})
}

// TestTimerStress performs stress testing on timer management
func TestTimerStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}
	
	t.Run("rapid_state_changes", func(t *testing.T) {
		initialGoroutines := runtime.NumGoroutine()
		
		mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
		mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)
		
		// Set up expectations
		mockInfoProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Maybe()
		mockInfoProvider.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Maybe()
		mockInfoProvider.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(false).Maybe()
		mockInfoProvider.EXPECT().IsAutoAcceptEnabled().Return(true).Maybe() // Auto-accept for testing
		mockInfoProvider.EXPECT().SetupRemoteDevice(mock.Anything, mock.Anything).Return(nil).Maybe()
		
		mockDataWriter.EXPECT().InitDataProcessing(mock.Anything).Maybe()
		mockDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()
		mockDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
		mockDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()
		
		conn := ship.NewConnectionHandler(
			mockInfoProvider,
			mockDataWriter,
			ship.ShipRoleServer,
			"local-id", 
			"remote-ski",
			"remote-id",
		)
		
		// Start connection
		conn.Run()
		
		// Rapidly change states
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				
				// Simulate various incoming messages
				conn.HandleIncomingWebsocketMessage(model.ShipInit)
				time.Sleep(time.Millisecond)
			}()
		}
		
		wg.Wait()
		
		// Close connection
		conn.CloseConnection(false, 0, "stress test complete")
		
		// Verify no goroutine leaks
		assert.Eventually(t, func() bool {
			current := runtime.NumGoroutine()
			return current <= initialGoroutines+5
		}, 3*time.Second, 100*time.Millisecond, "goroutines leaked during stress test")
	})
}

// TestTimerMemoryUsage checks for memory leaks in timer management
func TestTimerMemoryUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory test in short mode")
	}
	
	t.Run("long_running_timers", func(t *testing.T) {
		initialMemStats := &runtime.MemStats{}
		runtime.ReadMemStats(initialMemStats)
		
		// Create and destroy many connections with timers
		for i := 0; i < 100; i++ {
			mockInfoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
			mockDataWriter := mocks.NewWebsocketDataWriterInterface(t)
			
			// Minimal setup
			mockInfoProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Maybe()
			mockInfoProvider.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Maybe()
			mockInfoProvider.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(false).Maybe()
			mockInfoProvider.EXPECT().IsAutoAcceptEnabled().Return(false).Maybe()
			mockDataWriter.EXPECT().InitDataProcessing(mock.Anything).Maybe()
			mockDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()
			mockDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()
			mockDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
			
			conn := ship.NewConnectionHandler(
				mockInfoProvider,
				mockDataWriter,
				ship.ShipRoleClient,
				"local-id",
				"remote-ski",
				"remote-id",
			)
			
			conn.Run()
			time.Sleep(10 * time.Millisecond)
			conn.CloseConnection(false, 0, "memory test")
		}
		
		// Force GC
		runtime.GC()
		runtime.GC()
		
		// Check memory growth
		finalMemStats := &runtime.MemStats{}
		runtime.ReadMemStats(finalMemStats)
		
		// Calculate memory growth (handle potential underflow)
		var memGrowth int64
		if finalMemStats.Alloc > initialMemStats.Alloc {
			memGrowth = int64(finalMemStats.Alloc - initialMemStats.Alloc)
		} else {
			memGrowth = -int64(initialMemStats.Alloc - finalMemStats.Alloc)
		}
		
		t.Logf("Memory stats: initial=%d, final=%d, growth=%d bytes (%.2f MB)", 
			initialMemStats.Alloc, finalMemStats.Alloc, memGrowth, float64(memGrowth)/1024/1024)
		
		// Memory can decrease due to GC, just verify no massive leaks
		// Check total allocated memory is reasonable
		assert.Less(t, finalMemStats.Alloc, uint64(50*1024*1024), "excessive memory usage")
	})
}