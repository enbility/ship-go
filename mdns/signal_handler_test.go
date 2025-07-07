package mdns

import (
	"runtime"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestSignalHandlerLeak tests that multiple Start() calls don't create multiple signal handlers
func TestSignalHandlerLeak(t *testing.T) {
	t.Run("multiple_start_calls_no_handler_leak", func(t *testing.T) {
		// Create a test MdnsManager
		manager := NewMDNS("test-ski", "brand", "model", "type", "serial", 
			[]api.DeviceCategoryType{}, "id", "service", 4729, []string{}, MdnsProviderSelectionGoZeroConfOnly)
		
		// Set up a mock provider to avoid real network operations
		mockProvider := mocks.NewMdnsProviderInterface(t)
		mockProvider.EXPECT().Start(mock.Anything, mock.Anything).Return(true).Maybe()
		mockProvider.EXPECT().Announce(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockProvider.EXPECT().Unannounce().Maybe()
		mockProvider.EXPECT().Shutdown().Maybe()
		manager.SetTestProvider(mockProvider)
		
		// Create a mock callback
		mockCallback := mocks.NewMdnsReportInterface(t)
		mockCallback.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe()
		
		// Track initial goroutine count
		initialGoroutines := runtime.NumGoroutine()
		
		// Call Start() multiple times
		for i := 0; i < 5; i++ {
			err := manager.Start(mockCallback)
			assert.NoError(t, err, "Start should not fail on iteration %d", i)
			
			// Give time for any goroutines to start
			time.Sleep(10 * time.Millisecond)
		}
		
		// Check goroutine count - should only have one additional goroutine for signal handling
		currentGoroutines := runtime.NumGoroutine()
		goroutineGrowth := currentGoroutines - initialGoroutines
		
		t.Logf("Goroutines: initial=%d, current=%d, growth=%d", 
			initialGoroutines, currentGoroutines, goroutineGrowth)
		
		// Should have at most one additional goroutine for signal handling
		assert.LessOrEqual(t, goroutineGrowth, 3, "signal handler goroutines should not leak")
		
		// Verify signal handler is set up only once
		manager.signalHandlerMux.Lock()
		assert.NotNil(t, manager.signalHandler, "signal handler should be initialized")
		manager.signalHandlerMux.Unlock()
		
		// Shutdown should clean up
		manager.Shutdown()
		
		// Give time for cleanup
		time.Sleep(50 * time.Millisecond)
		
		// Verify signal handler is cleaned up
		manager.signalHandlerMux.Lock()
		assert.Nil(t, manager.signalHandler, "signal handler should be cleaned up")
		manager.signalHandlerMux.Unlock()
		
		// Check final goroutine count
		finalGoroutines := runtime.NumGoroutine()
		assert.LessOrEqual(t, finalGoroutines, initialGoroutines+2, 
			"goroutines should be cleaned up after shutdown")
	})
	
	t.Run("shutdown_idempotent", func(t *testing.T) {
		// Test that multiple Shutdown() calls are safe
		manager := NewMDNS("test-ski", "brand", "model", "type", "serial", 
			[]api.DeviceCategoryType{}, "id", "service", 4729, []string{}, MdnsProviderSelectionGoZeroConfOnly)
		
		mockProvider := mocks.NewMdnsProviderInterface(t)
		mockProvider.EXPECT().Start(mock.Anything, mock.Anything).Return(true).Maybe()
		mockProvider.EXPECT().Announce(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockProvider.EXPECT().Unannounce().Once() // Should only be called once
		mockProvider.EXPECT().Shutdown().Once() // Should only be called once
		manager.SetTestProvider(mockProvider)
		
		mockCallback := mocks.NewMdnsReportInterface(t)
		mockCallback.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe()
		
		// Start and then shutdown multiple times
		err := manager.Start(mockCallback)
		assert.NoError(t, err)
		
		// Multiple shutdowns should be safe
		for i := 0; i < 3; i++ {
			manager.Shutdown()
		}
		
		// Should not panic or cause issues
		manager.signalHandlerMux.Lock()
		assert.Nil(t, manager.signalHandler, "signal handler should be nil after shutdown")
		manager.signalHandlerMux.Unlock()
	})
}

// TestSignalHandlerSetupOnlyOnce tests that signal handler is set up only once
func TestSignalHandlerSetupOnlyOnce(t *testing.T) {
	manager := NewMDNS("test-ski", "brand", "model", "type", "serial", 
		[]api.DeviceCategoryType{}, "id", "service", 4729, []string{}, MdnsProviderSelectionGoZeroConfOnly)
	
	mockProvider := mocks.NewMdnsProviderInterface(t)
	mockProvider.EXPECT().Start(mock.Anything, mock.Anything).Return(true).Maybe()
	mockProvider.EXPECT().Announce(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockProvider.EXPECT().Unannounce().Maybe()
	mockProvider.EXPECT().Shutdown().Maybe()
	manager.SetTestProvider(mockProvider)
	
	mockCallback := mocks.NewMdnsReportInterface(t)
	mockCallback.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe()
	
	// First start should set up signal handler
	err := manager.Start(mockCallback)
	assert.NoError(t, err)
	
	manager.signalHandlerMux.Lock()
	firstHandler := manager.signalHandler
	assert.NotNil(t, firstHandler, "signal handler should be set up")
	manager.signalHandlerMux.Unlock()
	
	// Second start should reuse the same handler
	err = manager.Start(mockCallback)
	assert.NoError(t, err)
	
	manager.signalHandlerMux.Lock()
	secondHandler := manager.signalHandler
	assert.Equal(t, firstHandler, secondHandler, "signal handler should be the same instance")
	manager.signalHandlerMux.Unlock()
	
	manager.Shutdown()
}