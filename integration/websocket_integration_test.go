package integration

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/ws"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// TestWebSocketIntegration tests full websocket lifecycle with client-server interaction
func TestWebSocketIntegration(t *testing.T) {
	t.Run("full_connection_lifecycle", func(t *testing.T) {
		initialGoroutines := runtime.NumGoroutine()
		
		// Create a test server that echoes messages
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			
			// Echo server - read and write back
			for {
				messageType, message, err := conn.ReadMessage()
				if err != nil {
					if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
						t.Logf("server read error: %v", err)
					}
					return
				}
				
				err = conn.WriteMessage(messageType, message)
				if err != nil {
					return
				}
			}
		}))
		defer server.Close()
		
		// Create client connection
		wsURL := "ws" + server.URL[4:]
		clientConn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		
		// Create WebSocket wrapper
		wsClient := ws.NewWebsocketConnection(clientConn, "test-ski")
		
		// Create mock data reader
		mockReader := mocks.NewWebsocketDataReaderInterface(t)
		receivedMessages := make([][]byte, 0)
		var messagesMux sync.Mutex
		
		mockReader.EXPECT().HandleIncomingWebsocketMessage(mock.Anything).RunAndReturn(
			func(msg []byte) {
				messagesMux.Lock()
				receivedMessages = append(receivedMessages, msg)
				messagesMux.Unlock()
			}).Maybe()
		mockReader.EXPECT().ReportConnectionError(mock.Anything).Maybe()
		
		// Initialize connection
		wsClient.InitDataProcessing(mockReader)
		
		// Allow connection to establish
		time.Sleep(100 * time.Millisecond)
		
		// Send test messages
		testMessages := [][]byte{
			{0x01, 0x02, 0x03},
			{0x04, 0x05, 0x06, 0x07},
			{0x08, 0x09},
		}
		
		for _, msg := range testMessages {
			err := wsClient.WriteMessageToWebsocketConnection(msg)
			assert.NoError(t, err)
		}
		
		// Wait for echo responses
		assert.Eventually(t, func() bool {
			messagesMux.Lock()
			defer messagesMux.Unlock()
			return len(receivedMessages) == len(testMessages)
		}, 2*time.Second, 50*time.Millisecond, "did not receive all echo messages")
		
		// Verify messages
		messagesMux.Lock()
		for i, msg := range testMessages {
			assert.Equal(t, msg, receivedMessages[i], "message %d mismatch", i)
		}
		messagesMux.Unlock()
		
		// Close connection gracefully
		wsClient.CloseDataConnection(websocket.CloseNormalClosure, "test complete")
		
		// Verify connection is closed
		isClosed, _ := wsClient.IsDataConnectionClosed()
		assert.True(t, isClosed)
		
		// Wait for cleanup
		assert.Eventually(t, func() bool {
			// Allow for test infrastructure goroutines
			return runtime.NumGoroutine() <= initialGoroutines+3
		}, 2*time.Second, 50*time.Millisecond, "goroutines did not clean up")
	})
	
	t.Run("connection_error_recovery", func(t *testing.T) {
		initialGoroutines := runtime.NumGoroutine()
		
		// Create a test server that closes connection after first message
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Fatal(err)
			}
			
			// Read one message then close abruptly
			_, _, _ = conn.ReadMessage()
			conn.Close() // Abrupt close to simulate error
		}))
		defer server.Close()
		
		// Create client connection
		wsURL := "ws" + server.URL[4:]
		clientConn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		
		// Create WebSocket wrapper
		wsClient := ws.NewWebsocketConnection(clientConn, "test-ski")
		
		// Create mock data reader
		mockReader := mocks.NewWebsocketDataReaderInterface(t)
		errorReceived := make(chan error, 1)
		
		mockReader.EXPECT().HandleIncomingWebsocketMessage(mock.Anything).Maybe()
		mockReader.EXPECT().ReportConnectionError(mock.Anything).RunAndReturn(
			func(err error) {
				select {
				case errorReceived <- err:
				default:
				}
			}).Once()
		
		// Initialize connection
		wsClient.InitDataProcessing(mockReader)
		
		// Send a message to trigger server close
		err = wsClient.WriteMessageToWebsocketConnection([]byte{0x01, 0x02})
		assert.NoError(t, err)
		
		// Wait for error to be reported
		select {
		case err := <-errorReceived:
			assert.NotNil(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("error not reported")
		}
		
		// Verify connection is closed
		isClosed, _ := wsClient.IsDataConnectionClosed()
		assert.True(t, isClosed)
		
		// Verify goroutines cleaned up
		assert.Eventually(t, func() bool {
			return runtime.NumGoroutine() <= initialGoroutines+3
		}, 2*time.Second, 50*time.Millisecond, "goroutines did not clean up after error")
	})
	
	t.Run("concurrent_client_connections", func(t *testing.T) {
		initialGoroutines := runtime.NumGoroutine()
		
		// Create echo server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			
			for {
				messageType, message, err := conn.ReadMessage()
				if err != nil {
					return
				}
				_ = conn.WriteMessage(messageType, message)
			}
		}))
		defer server.Close()
		
		// Create multiple concurrent connections
		numConnections := 10
		var wg sync.WaitGroup
		connections := make([]*ws.WebsocketConnection, numConnections)
		
		for i := 0; i < numConnections; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				
				// Create client connection
				wsURL := "ws" + server.URL[4:]
				clientConn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
				if err != nil {
					t.Errorf("connection %d failed: %v", idx, err)
					return
				}
				if resp != nil && resp.Body != nil {
					resp.Body.Close()
				}
				
				// Create WebSocket wrapper
				wsClient := ws.NewWebsocketConnection(clientConn, "test-ski")
				connections[idx] = wsClient
				
				// Create mock data reader
				mockReader := mocks.NewWebsocketDataReaderInterface(t)
				mockReader.EXPECT().HandleIncomingWebsocketMessage(mock.Anything).Maybe()
				mockReader.EXPECT().ReportConnectionError(mock.Anything).Maybe()
				
				// Initialize connection
				wsClient.InitDataProcessing(mockReader)
				
				// Send some messages
				for j := 0; j < 5; j++ {
					_ = wsClient.WriteMessageToWebsocketConnection([]byte{byte(idx), byte(j)})
					time.Sleep(10 * time.Millisecond)
				}
			}(i)
		}
		
		// Wait for all connections to be established
		wg.Wait()
		
		// Close all connections
		for i, conn := range connections {
			if conn != nil {
				conn.CloseDataConnection(websocket.CloseNormalClosure, "batch close")
				isClosed, _ := conn.IsDataConnectionClosed()
				assert.True(t, isClosed, "connection %d not closed", i)
			}
		}
		
		// Verify all goroutines cleaned up
		assert.Eventually(t, func() bool {
			current := runtime.NumGoroutine()
			t.Logf("Goroutines: current=%d, initial=%d, diff=%d", current, initialGoroutines, current-initialGoroutines)
			return current <= initialGoroutines+3
		}, 3*time.Second, 100*time.Millisecond, "goroutines did not clean up after concurrent connections")
	})
}