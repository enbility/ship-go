package ws

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestWebSocketLifecycle tests the lifecycle management of websocket connections
// with focus on proper goroutine cleanup
func TestWebSocketLifecycle(t *testing.T) {
	t.Run("goroutines_exit_on_normal_close", func(t *testing.T) {
		// Get initial goroutine count
		initialGoroutines := countGoroutines()

		// Create test server and connection
		ts := &testServer{}
		testServer, resp, testWsConn := newWSServer(t, ts)
		defer testServer.Close()
		defer resp.Body.Close()
		defer testWsConn.Close()

		// Create websocket connection
		ws := NewWebsocketConnection(testWsConn, "test-ski")

		// Create mock data reader
		mockReader := mocks.NewWebsocketDataReaderInterface(t)
		mockReader.EXPECT().ReportConnectionError(mock.Anything).Return().Maybe()
		mockReader.EXPECT().HandleIncomingWebsocketMessage(mock.Anything).Return().Maybe()

		// Initialize and run
		ws.InitDataProcessing(mockReader)

		// Give goroutines time to start
		time.Sleep(100 * time.Millisecond)

		// Close connection
		ws.CloseDataConnection(websocket.CloseNormalClosure, "test close")

		// Wait for goroutines to exit
		// We expect the pumps to exit, but the test server goroutine remains
		assert.Eventually(t, func() bool {
			current := countGoroutines()
			t.Logf("Current goroutines: %d, initial: %d, diff: %d", current, initialGoroutines, current-initialGoroutines)
			// After close: initial goroutines + test server handler (which remains alive)
			// The 2 pump goroutines should have exited
			return current <= initialGoroutines+2 // +2: test server handler remains, plus possible runtime variance
		}, 2*time.Second, 50*time.Millisecond, "goroutines did not exit after close")
	})

	t.Run("goroutines_exit_on_error", func(t *testing.T) {
		initialGoroutines := countGoroutines()

		// Create test server and connection
		ts := &testServer{}
		testServer, resp, testWsConn := newWSServer(t, ts)
		defer testServer.Close()
		defer resp.Body.Close()
		defer testWsConn.Close()

		// Create websocket connection
		ws := NewWebsocketConnection(testWsConn, "test-ski")

		// Create mock data reader
		mockReader := mocks.NewWebsocketDataReaderInterface(t)
		mockReader.EXPECT().ReportConnectionError(mock.Anything).Return().Maybe()

		// Initialize and run
		ws.InitDataProcessing(mockReader)

		// Give goroutines time to start
		time.Sleep(100 * time.Millisecond)

		// Simulate connection error by closing the underlying connection
		testWsConn.Close()

		// Wait for goroutines to exit
		assert.Eventually(t, func() bool {
			current := countGoroutines()
			// Similar to normal close, test server handler remains
			return current <= initialGoroutines+2
		}, 2*time.Second, 50*time.Millisecond, "goroutines did not exit after error")
	})

	t.Run("concurrent_operations_safe", func(t *testing.T) {
		// Create test server and connection
		ts := &testServer{}
		testServer, resp, testWsConn := newWSServer(t, ts)
		defer testServer.Close()
		defer resp.Body.Close()
		defer testWsConn.Close()

		// Create websocket connection
		ws := NewWebsocketConnection(testWsConn, "test-ski")

		// Create mock data reader
		mockReader := mocks.NewWebsocketDataReaderInterface(t)
		mockReader.EXPECT().ReportConnectionError(mock.Anything).Return().Maybe()
		mockReader.EXPECT().HandleIncomingWebsocketMessage(mock.Anything).Return().Maybe()

		// Initialize and run
		ws.InitDataProcessing(mockReader)

		// Give goroutines time to start
		time.Sleep(100 * time.Millisecond)

		// Perform concurrent operations
		var wg sync.WaitGroup

		// Multiple writers
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_ = ws.WriteMessageToWebsocketConnection([]byte{byte(i), byte(i)})
			}(i)
		}

		// Concurrent close attempts
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ws.CloseDataConnection(websocket.CloseNormalClosure, "concurrent close")
			}()
		}

		// Wait for all operations to complete
		wg.Wait()

		// Verify connection is closed
		isClosed, _ := ws.IsDataConnectionClosed()
		assert.True(t, isClosed, "connection should be closed")

		// Give time for cleanup
		time.Sleep(100 * time.Millisecond)
	})
}

// TestWebSocketShutdownTiming verifies shutdown completes in reasonable time
func TestWebSocketShutdownTiming(t *testing.T) {
	// Create test server and connection
	ts := &testServer{}
	testServer, resp, testWsConn := newWSServer(t, ts)
	defer testServer.Close()
	defer resp.Body.Close()
	defer testWsConn.Close()

	// Create websocket connection
	ws := NewWebsocketConnection(testWsConn, "test-ski")

	// Create mock data reader
	mockReader := mocks.NewWebsocketDataReaderInterface(t)
	mockReader.EXPECT().ReportConnectionError(mock.Anything).Return().Maybe()

	// Initialize and run
	ws.InitDataProcessing(mockReader)

	// Give goroutines time to start
	time.Sleep(100 * time.Millisecond)

	// Measure shutdown time
	start := time.Now()
	ws.CloseDataConnection(websocket.CloseNormalClosure, "timing test")
	shutdownDuration := time.Since(start)

	// Verify shutdown completed quickly
	assert.Less(t, shutdownDuration, 500*time.Millisecond, "shutdown took too long")
}

// TestWebSocketResourceCleanup verifies all resources are properly cleaned up
func TestWebSocketResourceCleanup(t *testing.T) {
	t.Run("channels_closed_on_shutdown", func(t *testing.T) {
		// Create test server and connection
		ts := &testServer{}
		testServer, resp, testWsConn := newWSServer(t, ts)
		defer testServer.Close()
		defer resp.Body.Close()
		defer testWsConn.Close()

		// Create websocket connection
		ws := NewWebsocketConnection(testWsConn, "test-ski")

		// Create mock data reader
		mockReader := mocks.NewWebsocketDataReaderInterface(t)
		mockReader.EXPECT().ReportConnectionError(mock.Anything).Return().Maybe()

		// Initialize and run
		ws.InitDataProcessing(mockReader)

		// Give goroutines time to start
		time.Sleep(100 * time.Millisecond)

		// Close connection
		ws.CloseDataConnection(websocket.CloseNormalClosure, "channel test")

		// Wait a bit for cleanup
		time.Sleep(100 * time.Millisecond)

		// Try to write - should fail with closed connection error
		err := ws.WriteMessageToWebsocketConnection([]byte{1, 2, 3})
		require.Error(t, err)
		assert.ErrorIs(t, err, api.ErrConnectionClosed)
	})

	t.Run("multiple_close_calls_safe", func(t *testing.T) {
		// Create test server and connection
		ts := &testServer{}
		testServer, resp, testWsConn := newWSServer(t, ts)
		defer testServer.Close()
		defer resp.Body.Close()
		defer testWsConn.Close()

		// Create websocket connection
		ws := NewWebsocketConnection(testWsConn, "test-ski")

		// Create mock data reader
		mockReader := mocks.NewWebsocketDataReaderInterface(t)
		mockReader.EXPECT().ReportConnectionError(mock.Anything).Return().Maybe()

		// Initialize and run
		ws.InitDataProcessing(mockReader)

		// Multiple close calls should not panic
		for i := 0; i < 5; i++ {
			ws.CloseDataConnection(websocket.CloseNormalClosure, "multiple close test")
		}

		// Verify connection is closed
		isClosed, _ := ws.IsDataConnectionClosed()
		assert.True(t, isClosed)
	})
}

// Helper function to count current goroutines
func countGoroutines() int {
	return runtime.NumGoroutine()
}
