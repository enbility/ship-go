package ws

import (
	"net/http"
	"net/http/httptest"
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

// TestWebSocketErrorPaths tests various error scenarios and cleanup
func TestWebSocketErrorPaths(t *testing.T) {
	t.Run("read_error_cleanup", func(t *testing.T) {
		initialGoroutines := runtime.NumGoroutine()

		// Create a server that sends invalid data then closes
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Fatal(err)
			}

			// Send invalid message (too short)
			_ = conn.WriteMessage(websocket.BinaryMessage, []byte{0x01}) // Invalid SHIP message

			// Then close abruptly
			time.Sleep(100 * time.Millisecond)
			conn.Close()
		}))
		defer server.Close()

		// Create client
		wsURL := "ws" + server.URL[4:]
		clientConn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}

		ws := NewWebsocketConnection(clientConn, "test-ski")

		// Mock reader that expects error
		mockReader := mocks.NewWebsocketDataReaderInterface(t)
		errorReported := make(chan error, 1)

		mockReader.EXPECT().ReportConnectionError(mock.Anything).RunAndReturn(
			func(err error) {
				select {
				case errorReported <- err:
				default:
				}
			}).Once()

		ws.InitDataProcessing(mockReader)

		// Wait for error
		select {
		case err := <-errorReported:
			assert.NotNil(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("error not reported")
		}

		// Verify cleanup
		assert.Eventually(t, func() bool {
			isClosed, _ := ws.IsDataConnectionClosed()
			return isClosed
		}, time.Second, 50*time.Millisecond)

		// Verify goroutines cleaned up
		assert.Eventually(t, func() bool {
			return runtime.NumGoroutine() <= initialGoroutines+3
		}, 2*time.Second, 50*time.Millisecond)
	})

	t.Run("write_error_cleanup", func(t *testing.T) {
		initialGoroutines := runtime.NumGoroutine()

		// Create a server that immediately closes after upgrade
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Close immediately to cause write errors
			conn.Close()
		}))
		defer server.Close()

		// Create client
		wsURL := "ws" + server.URL[4:]
		clientConn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}

		ws := NewWebsocketConnection(clientConn, "test-ski")

		// Mock reader
		mockReader := mocks.NewWebsocketDataReaderInterface(t)
		mockReader.EXPECT().ReportConnectionError(mock.Anything).Maybe()

		ws.InitDataProcessing(mockReader)

		// Try to write multiple messages
		time.Sleep(100 * time.Millisecond) // Let connection close propagate

		var writeErr error
		for i := 0; i < 10; i++ {
			writeErr = ws.WriteMessageToWebsocketConnection([]byte{byte(i), byte(i)})
			if writeErr != nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		// Should eventually get write error
		assert.Eventually(t, func() bool {
			err := ws.WriteMessageToWebsocketConnection([]byte{0x01, 0x02})
			return err != nil
		}, 2*time.Second, 50*time.Millisecond)

		// Verify connection marked as closed
		isClosed, _ := ws.IsDataConnectionClosed()
		assert.True(t, isClosed)

		// Verify goroutines cleaned up
		assert.Eventually(t, func() bool {
			return runtime.NumGoroutine() <= initialGoroutines+3
		}, 2*time.Second, 50*time.Millisecond)
	})

	t.Run("write_channel_full_error", func(t *testing.T) {
		// Create test server and connection
		ts := &testServer{}
		testServer, resp, testWsConn := newWSServer(t, ts)
		defer testServer.Close()
		defer resp.Body.Close()
		defer testWsConn.Close()

		ws := NewWebsocketConnection(testWsConn, "test-ski")

		// Don't initialize data processing to block the write pump
		ws.shipWriteChannel = make(chan []byte, 1) // Small buffer
		ws.closeChannel = make(chan struct{})

		// Fill the channel
		err := ws.WriteMessageToWebsocketConnection([]byte{0x01, 0x02})
		assert.NoError(t, err)

		// Next write should fail with buffer full
		err = ws.WriteMessageToWebsocketConnection([]byte{0x03, 0x04})
		assert.Error(t, err)
		assert.ErrorIs(t, err, api.ErrBufferFull)

		// Close connection
		ws.CloseDataConnection(websocket.CloseNormalClosure, "test")
	})

	t.Run("concurrent_close_and_write", func(t *testing.T) {
		// Create test server and connection
		ts := &testServer{}
		testServer, resp, testWsConn := newWSServer(t, ts)
		defer testServer.Close()
		defer resp.Body.Close()
		defer testWsConn.Close()

		ws := NewWebsocketConnection(testWsConn, "test-ski")

		mockReader := mocks.NewWebsocketDataReaderInterface(t)
		mockReader.EXPECT().ReportConnectionError(mock.Anything).Maybe()
		mockReader.EXPECT().HandleIncomingWebsocketMessage(mock.Anything).Maybe()

		ws.InitDataProcessing(mockReader)
		time.Sleep(50 * time.Millisecond)

		// Concurrent operations
		var wg sync.WaitGroup
		errors := make([]error, 100)

		// Start writers
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				errors[idx] = ws.WriteMessageToWebsocketConnection([]byte{byte(idx)})
			}(i)
		}

		// Close connection concurrently
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond)
			ws.CloseDataConnection(websocket.CloseNormalClosure, "concurrent test")
		}()

		// More writers after close
		for i := 50; i < 100; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				time.Sleep(20 * time.Millisecond)
				errors[idx] = ws.WriteMessageToWebsocketConnection([]byte{byte(idx)})
			}(i)
		}

		wg.Wait()

		// Some writes should have succeeded, some should have failed
		successCount := 0
		failCount := 0
		for _, err := range errors {
			if err == nil {
				successCount++
			} else {
				failCount++
			}
		}

		assert.Greater(t, successCount, 0, "some writes should succeed")
		assert.Greater(t, failCount, 0, "some writes should fail after close")

		// Verify closed
		isClosed, _ := ws.IsDataConnectionClosed()
		assert.True(t, isClosed)
	})

	t.Run("panic_recovery", func(t *testing.T) {
		// This test verifies that panics in goroutines don't crash the program
		// and resources are still cleaned up

		initialGoroutines := runtime.NumGoroutine()

		// Create test server and connection
		ts := &testServer{}
		testServer, resp, testWsConn := newWSServer(t, ts)
		defer testServer.Close()
		defer resp.Body.Close()
		defer testWsConn.Close()

		ws := NewWebsocketConnection(testWsConn, "test-ski")

		// Create a mock reader that panics
		mockReader := mocks.NewWebsocketDataReaderInterface(t)
		panicTriggered := false

		mockReader.EXPECT().HandleIncomingWebsocketMessage(mock.Anything).RunAndReturn(
			func(msg []byte) {
				// Panic on second message
				if len(msg) > 0 && msg[0] == 0x02 && !panicTriggered {
					panicTriggered = true
					panic("test panic")
				}
			}).Maybe()
		mockReader.EXPECT().ReportConnectionError(mock.Anything).Maybe()

		// The readShipPump should recover from panic and close connection
		ws.InitDataProcessing(mockReader)

		// Send messages
		_ = ws.WriteMessageToWebsocketConnection([]byte{0x01})
		time.Sleep(100 * time.Millisecond)
		_ = ws.WriteMessageToWebsocketConnection([]byte{0x02}) // This will cause panic
		time.Sleep(500 * time.Millisecond)                     // Give time for panic recovery

		// Connection should still be closeable
		ws.CloseDataConnection(websocket.CloseNormalClosure, "after panic")

		// Verify goroutines still cleaned up
		assert.Eventually(t, func() bool {
			return runtime.NumGoroutine() <= initialGoroutines+3
		}, 2*time.Second, 50*time.Millisecond, "goroutines not cleaned up after panic")
	})
}
