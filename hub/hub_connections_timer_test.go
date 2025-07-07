package hub

import (
	"crypto/tls"
	"fmt"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// setupTestHubForTimer creates a test hub with mocked dependencies
func setupTestHubForTimer(t *testing.T) *Hub {
	mdns := mocks.NewMdnsInterface(t)
	mdns.EXPECT().Start(mock.Anything).Return(nil).Maybe()
	mdns.EXPECT().AnnounceMdnsEntry().Return(nil).Maybe()
	mdns.EXPECT().UnannounceMdnsEntry().Maybe()
	mdns.EXPECT().Shutdown().Maybe()
	mdns.EXPECT().RequestMdnsEntries().Maybe()
	
	hubReader := mocks.NewHubReaderInterface(t)
	hubReader.EXPECT().RemoteSKIConnected(mock.Anything).Maybe()
	hubReader.EXPECT().RemoteSKIDisconnected(mock.Anything).Maybe()
	hubReader.EXPECT().ServiceShipIDUpdate(mock.Anything, mock.Anything).Maybe()
	hubReader.EXPECT().ServicePairingDetailUpdate(mock.Anything, mock.Anything).Maybe()
	
	service := api.NewServiceDetails("test-ski")
	service.SetShipID("test-ship-id")
	
	// Create a dummy certificate for testing
	cert := tls.Certificate{}
	
	hub := NewHub(hubReader, mdns, 4729, cert, service)
	
	return hub
}

// generateTestSKI generates a test SKI
func generateTestSKI(index int) string {
	return fmt.Sprintf("test-ski-%d", index)
}

// TestConnectionDelayTimerLeak tests for timer goroutine leaks in connection coordination
func TestConnectionDelayTimerLeak(t *testing.T) {
	t.Run("timer_goroutine_cleanup", func(t *testing.T) {
		initialGoroutines := runtime.NumGoroutine()
		
		hub := setupTestHubForTimer(t)
		hub.Start()
		defer hub.Shutdown()
		
		// Give hub time to start
		time.Sleep(100 * time.Millisecond)
		
		// Track goroutines before operations
		beforeGoroutines := runtime.NumGoroutine()
		
		// Create test entries
		entries := make([]*api.MdnsEntry, 10)
		for i := 0; i < 10; i++ {
			ski := generateTestSKI(i)
			entries[i] = &api.MdnsEntry{
				Name:       "test-device-" + ski,
				Identifier: ski,
				Register:   true,
			}
			
			// Register the service as paired
			service := api.NewServiceDetails(ski)
			service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
			hub.muxReg.Lock()
			hub.remoteServices[ski] = service
			hub.muxReg.Unlock()
			
			// Simulate connection coordination with delay
			// This should create timer goroutines
			hub.coordinateConnectionInitations(ski, entries[i])
		}
		
		// Let timers run for a bit
		time.Sleep(200 * time.Millisecond)
		
		// Simulate successful connections (counters changed)
		hub.muxConAttempt.Lock()
		for i := 0; i < 10; i++ {
			hub.connectionAttemptCounter[entries[i].Identifier] = 2
		}
		hub.muxConAttempt.Unlock()
		
		// Wait for all timers to expire (max delay is typically < 10s)
		time.Sleep(12 * time.Second)
		
		// Check goroutine count
		afterGoroutines := runtime.NumGoroutine()
		goroutineGrowth := afterGoroutines - beforeGoroutines
		
		t.Logf("Goroutines: initial=%d, before=%d, after=%d, growth=%d", 
			initialGoroutines, beforeGoroutines, afterGoroutines, goroutineGrowth)
		
		// Should have no significant goroutine growth
		assert.LessOrEqual(t, goroutineGrowth, 2, "timer goroutines may have leaked")
	})
	
	t.Run("many_rapid_connections", func(t *testing.T) {
		if testing.Short() {
			t.Skip("Skipping long-running test in short mode")
		}
		
		hub := setupTestHubForTimer(t)
		hub.Start()
		defer hub.Shutdown()
		
		// Track goroutine growth
		beforeGoroutines := runtime.NumGoroutine()
		
		// Simulate many rapid connection attempts
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				
				ski := generateTestSKI(idx * 1000) // Ensure unique SKIs
				entry := &api.MdnsEntry{
					Name:       "device-" + ski,
					Identifier: ski,
					Register:   true,
				}
				
				// Register service as paired
				service := api.NewServiceDetails(ski)
				service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
				hub.muxReg.Lock()
				hub.remoteServices[ski] = service
				hub.muxReg.Unlock()
				
				// Multiple connection attempts
				for j := 0; j < 5; j++ {
					hub.coordinateConnectionInitations(ski, entry)
					time.Sleep(10 * time.Millisecond)
				}
			}(i)
		}
		
		wg.Wait()
		
		// Let system stabilize
		time.Sleep(500 * time.Millisecond)
		
		midGoroutines := runtime.NumGoroutine()
		t.Logf("Mid-test goroutines: before=%d, mid=%d, growth=%d", 
			beforeGoroutines, midGoroutines, midGoroutines-beforeGoroutines)
		
		// Demonstrate the issue: goroutines accumulate
		if midGoroutines-beforeGoroutines > 50 {
			t.Logf("ISSUE DEMONSTRATED: %d timer goroutines accumulated", midGoroutines-beforeGoroutines)
		}
		
		// Wait for all timers to expire
		time.Sleep(12 * time.Second)
		
		// Final goroutine check
		finalGoroutines := runtime.NumGoroutine()
		finalGrowth := finalGoroutines - beforeGoroutines
		
		t.Logf("Final goroutines: before=%d, final=%d, growth=%d", 
			beforeGoroutines, finalGoroutines, finalGrowth)
		
		// Should return to baseline
		assert.LessOrEqual(t, finalGrowth, 5, "goroutines leaked after timers expired")
	})
}

// TestConnectionDelayTimerCancellation tests timer cancellation with the fix
func TestConnectionDelayTimerCancellation(t *testing.T) {
	t.Run("timer_cancelled_on_connection", func(t *testing.T) {
		// This test verifies that timers are properly cancelled
		// when a connection is established
		
		hub := setupTestHubForTimer(t)
		hub.Start()
		defer hub.Shutdown()
		
		ski := generateTestSKI(999)
		
		// Mock a ship connection
		shipConn := mocks.NewShipConnectionInterface(t)
		shipConn.EXPECT().RemoteSKI().Return(ski).Maybe()
		shipConn.EXPECT().DataHandler().Return(nil).Maybe()
		shipConn.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()
		
		// Register service as paired to trigger delay
		service := api.NewServiceDetails(ski)
		service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
		hub.muxReg.Lock()
		hub.remoteServices[ski] = service
		hub.muxReg.Unlock()
		
		entry := &api.MdnsEntry{
			Name:       "test-device",
			Identifier: ski,
			Register:   true,
			Port:       4729,
			Addresses:  []net.IP{net.ParseIP("127.0.0.1")},
		}
		
		// Start connection with delay
		hub.coordinateConnectionInitations(ski, entry)
		
		// Give timer a moment to be created
		time.Sleep(50 * time.Millisecond)
		
		// Verify timer was stored
		hub.muxTimers.RLock()
		timer, exists := hub.connectionDelayTimers[ski]
		hub.muxTimers.RUnlock()
		assert.True(t, exists, "timer should be stored")
		assert.NotNil(t, timer, "timer should not be nil")
		
		// Simulate successful connection
		hub.registerConnection(shipConn)
		
		// Verify timer was cancelled and removed
		hub.muxTimers.RLock()
		_, exists = hub.connectionDelayTimers[ski]
		hub.muxTimers.RUnlock()
		assert.False(t, exists, "timer should be removed after connection")
		
		// Wait to ensure timer doesn't fire
		time.Sleep(4 * time.Second)
		
		// prepareConnectionInitation should not have been called
		// (we can't easily test this without more mocking, but no goroutine leak is good)
	})
	
	t.Run("multiple_timers_cancelled_on_shutdown", func(t *testing.T) {
		hub := setupTestHubForTimer(t)
		hub.Start()
		
		// Create multiple delayed connections
		for i := 0; i < 5; i++ {
			ski := generateTestSKI(1000 + i)
			service := api.NewServiceDetails(ski)
			service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
			hub.muxReg.Lock()
			hub.remoteServices[ski] = service
			hub.muxReg.Unlock()
			
			entry := &api.MdnsEntry{
				Name:       fmt.Sprintf("device-%d", i),
				Identifier: ski,
				Register:   true,
			}
			hub.coordinateConnectionInitations(ski, entry)
		}
		
		// Give timers a moment to be created
		time.Sleep(50 * time.Millisecond)
		
		// Verify timers exist
		hub.muxTimers.RLock()
		timerCount := len(hub.connectionDelayTimers)
		hub.muxTimers.RUnlock()
		assert.Equal(t, 5, timerCount, "should have 5 timers")
		
		// Shutdown should cancel all timers
		hub.Shutdown()
		
		// Verify all timers were cleaned up
		hub.muxTimers.RLock()
		finalCount := len(hub.connectionDelayTimers)
		hub.muxTimers.RUnlock()
		assert.Equal(t, 0, finalCount, "all timers should be cancelled on shutdown")
	})
}

// TestConnectionDelayResourceUsage measures resource usage of delay timers
func TestConnectionDelayResourceUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping resource usage test in short mode")
	}
	
	t.Run("memory_usage_with_delays", func(t *testing.T) {
		hub := setupTestHubForTimer(t)
		hub.Start()
		defer hub.Shutdown()
		
		// Force GC and get baseline
		runtime.GC()
		runtime.GC()
		var baselineStats runtime.MemStats
		runtime.ReadMemStats(&baselineStats)
		
		// Create many delayed connections
		for i := 0; i < 1000; i++ {
			ski := generateTestSKI(i * 10000) // Ensure unique SKIs
			entry := &api.MdnsEntry{
				Name:       "device-" + ski,
				Identifier: ski,
				Register:   true,
			}
			
			service := api.NewServiceDetails(ski)
			service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
			hub.muxReg.Lock()
			hub.remoteServices[ski] = service
			hub.muxReg.Unlock()
			hub.coordinateConnectionInitations(ski, entry)
		}
		
		// Check memory growth
		runtime.GC()
		var afterStats runtime.MemStats
		runtime.ReadMemStats(&afterStats)
		
		memGrowth := int64(afterStats.Alloc) - int64(baselineStats.Alloc)
		memGrowthMB := float64(memGrowth) / 1024 / 1024
		
		t.Logf("Memory growth: %d bytes (%.2f MB) for 1000 timers", memGrowth, memGrowthMB)
		
		// Each timer goroutine should use minimal memory
		assert.Less(t, memGrowthMB, 10.0, "excessive memory usage for timers")
	})
}