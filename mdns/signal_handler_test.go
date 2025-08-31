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

// TestNoSignalHandlerLeak tests that multiple Start() calls don't create goroutine leaks
// Note: Signal handler functionality has been removed from the library as signal handling
// should be the application's responsibility, not a library's
func TestNoSignalHandlerLeak(t *testing.T) {
	t.Run("multiple_start_calls_no_goroutine_leak", func(t *testing.T) {
		// Create a test MdnsManager with test setup selection
		manager := NewMDNS("testski", "brand", "model", "type", "serial",
			[]api.DeviceCategoryType{}, "id", "service", 4729, []string{}, MdnsProviderSelectionTestSetup)

		// Set up a mock provider to avoid real network operations
		mockProvider := mocks.NewMdnsProviderInterface(t)
		mockProvider.EXPECT().Start(mock.Anything, mock.Anything, mock.Anything).Return(true).Once() // Should only be called once due to isStarted check
		// Note: AnnounceMdnsEntry() now has early return if already announced, so only called once
		mockProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil).Once() // Only called on first Start() due to early return
		mockProvider.EXPECT().UnannounceService(mock.Anything).Maybe()
		mockProvider.EXPECT().Shutdown().Maybe()
		manager.SetMdnsProvider(mockProvider)

		// Create a mock callback
		mockCallback := mocks.NewMdnsReportInterface(t)
		mockCallback.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe()

		// Track initial goroutine count
		initialGoroutines := runtime.NumGoroutine()

		// Call Start() multiple times
		for i := 0; i < 5; i++ {
			err := manager.Start(api.PairingModeBoth, mockCallback)
			assert.NoError(t, err, "Start should not fail on iteration %d", i)

			// Give time for any goroutines to start
			time.Sleep(10 * time.Millisecond)
		}

		// Check goroutine count - should not have any additional goroutines
		currentGoroutines := runtime.NumGoroutine()
		goroutineGrowth := currentGoroutines - initialGoroutines

		t.Logf("Goroutines: initial=%d, current=%d, growth=%d",
			initialGoroutines, currentGoroutines, goroutineGrowth)

		// Without signal handler, there should be minimal or no goroutine growth
		assert.LessOrEqual(t, goroutineGrowth, 1,
			"Goroutine count should not grow significantly with multiple Start() calls")

		// Shutdown should clean up
		manager.Shutdown()

		// Give time for cleanup
		time.Sleep(50 * time.Millisecond)

		// Check final goroutine count
		finalGoroutines := runtime.NumGoroutine()
		assert.LessOrEqual(t, finalGoroutines, initialGoroutines+1,
			"goroutines should be cleaned up after shutdown")
	})
}

// TestShutdownWithoutSignalHandler tests that shutdown works correctly without signal handler
func TestShutdownWithoutSignalHandler(t *testing.T) {
	t.Run("shutdown_completes_without_signal_handler", func(t *testing.T) {
		manager := NewMDNS("testski", "brand", "model", "type", "serial",
			[]api.DeviceCategoryType{}, "id", "service", 4729, []string{}, MdnsProviderSelectionTestSetup)

		mockProvider := mocks.NewMdnsProviderInterface(t)
		mockProvider.EXPECT().Start(mock.Anything, mock.Anything, mock.Anything).Return(true).Once()
		mockProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil).Maybe()
		mockProvider.EXPECT().UnannounceService(mock.Anything).Maybe()
		mockProvider.EXPECT().Shutdown().Maybe()
		manager.SetMdnsProvider(mockProvider)

		mockCallback := mocks.NewMdnsReportInterface(t)
		mockCallback.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe()

		err := manager.Start(api.PairingModeBoth, mockCallback)
		assert.NoError(t, err)

		// Shutdown should complete without hanging
		done := make(chan bool)
		go func() {
			manager.Shutdown()
			done <- true
		}()

		select {
		case <-done:
			// Success - shutdown completed
		case <-time.After(1 * time.Second):
			t.Fatal("Shutdown timed out")
		}
	})

	t.Run("multiple_shutdown_calls_safe", func(t *testing.T) {
		manager := NewMDNS("testski", "brand", "model", "type", "serial",
			[]api.DeviceCategoryType{}, "id", "service", 4729, []string{}, MdnsProviderSelectionTestSetup)

		// Multiple shutdown calls should not panic or cause issues
		manager.Shutdown()
		manager.Shutdown()
		manager.Shutdown()
	})
}
