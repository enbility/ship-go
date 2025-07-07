package ws

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enbility/ship-go/mocks"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestWebSocketStress performs stress testing on websocket connections
func TestWebSocketStress(t *testing.T) {
	t.Run("many_connections", func(t *testing.T) {
		initialGoroutines := runtime.NumGoroutine()
		initialMemStats := &runtime.MemStats{}
		runtime.ReadMemStats(initialMemStats)
		
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
		
		// Create many connections
		numConnections := 100
		connections := make([]*WebsocketConnection, numConnections)
		var wg sync.WaitGroup
		
		for i := 0; i < numConnections; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				
				wsURL := "ws" + server.URL[4:]
				clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
				if err != nil {
					t.Errorf("connection %d failed: %v", idx, err)
					return
				}
				
				ws := NewWebsocketConnection(clientConn, fmt.Sprintf("ski-%d", idx))
				connections[idx] = ws
				
				mockReader := mocks.NewWebsocketDataReaderInterface(t)
				mockReader.EXPECT().HandleIncomingWebsocketMessage(mock.Anything).Maybe()
				mockReader.EXPECT().ReportConnectionError(mock.Anything).Maybe()
				
				ws.InitDataProcessing(mockReader)
				
				// Send some messages
				for j := 0; j < 10; j++ {
					_ = ws.WriteMessageToWebsocketConnection([]byte{byte(idx), byte(j)})
				}
			}(i)
		}
		
		wg.Wait()
		
		// Let connections run for a bit
		time.Sleep(500 * time.Millisecond)
		
		// Close all connections
		var closeWg sync.WaitGroup
		for i, conn := range connections {
			if conn != nil {
				closeWg.Add(1)
				go func(c *WebsocketConnection, idx int) {
					defer closeWg.Done()
					c.CloseDataConnection(websocket.CloseNormalClosure, fmt.Sprintf("close-%d", idx))
				}(conn, i)
			}
		}
		
		closeWg.Wait()
		
		// Verify all connections closed
		for i, conn := range connections {
			if conn != nil {
				isClosed, _ := conn.IsDataConnectionClosed()
				assert.True(t, isClosed, "connection %d not closed", i)
			}
		}
		
		// Check goroutine cleanup
		assert.Eventually(t, func() bool {
			current := runtime.NumGoroutine()
			t.Logf("Goroutines after stress test: current=%d, initial=%d, diff=%d", current, initialGoroutines, current-initialGoroutines)
			return current <= initialGoroutines+5 // Allow some variance
		}, 5*time.Second, 100*time.Millisecond, "goroutines did not clean up after stress test")
		
		// Check memory usage
		finalMemStats := &runtime.MemStats{}
		runtime.ReadMemStats(finalMemStats)
		memGrowth := finalMemStats.Alloc - initialMemStats.Alloc
		t.Logf("Memory growth: %d bytes (%.2f MB)", memGrowth, float64(memGrowth)/1024/1024)
		assert.Less(t, memGrowth, uint64(100*1024*1024), "excessive memory growth (>100MB)")
	})
	
	t.Run("rapid_connect_disconnect", func(t *testing.T) {
		initialGoroutines := runtime.NumGoroutine()
		
		// Create echo server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			
			for {
				_, _, err := conn.ReadMessage()
				if err != nil {
					return
				}
			}
		}))
		defer server.Close()
		
		// Rapid connect/disconnect cycles
		cycles := 50
		var successCount int32
		var errorCount int32
		var wg sync.WaitGroup
		
		for i := 0; i < cycles; i++ {
			wg.Add(1)
			go func(cycle int) {
				defer wg.Done()
				
				wsURL := "ws" + server.URL[4:]
				clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
				if err != nil {
					atomic.AddInt32(&errorCount, 1)
					return
				}
				
				ws := NewWebsocketConnection(clientConn, fmt.Sprintf("rapid-%d", cycle))
				
				mockReader := mocks.NewWebsocketDataReaderInterface(t)
				mockReader.EXPECT().HandleIncomingWebsocketMessage(mock.Anything).Maybe()
				mockReader.EXPECT().ReportConnectionError(mock.Anything).Maybe()
				
				ws.InitDataProcessing(mockReader)
				
				// Quick message exchange
				_ = ws.WriteMessageToWebsocketConnection([]byte{byte(cycle)})
				
				// Random delay before close
				time.Sleep(time.Duration(cycle%10) * time.Millisecond)
				
				ws.CloseDataConnection(websocket.CloseNormalClosure, "rapid test")
				
				isClosed, _ := ws.IsDataConnectionClosed()
				if isClosed {
					atomic.AddInt32(&successCount, 1)
				}
			}(i)
			
			// Small delay between connections
			if i%10 == 0 {
				time.Sleep(10 * time.Millisecond)
			}
		}
		
		wg.Wait()
		
		t.Logf("Rapid test results: %d successful, %d errors", successCount, errorCount)
		assert.Equal(t, int32(cycles), successCount+errorCount, "not all cycles completed")
		
		// Verify goroutine cleanup
		assert.Eventually(t, func() bool {
			return runtime.NumGoroutine() <= initialGoroutines+5
		}, 3*time.Second, 100*time.Millisecond, "goroutines leaked after rapid test")
	})
	
	t.Run("high_throughput_messages", func(t *testing.T) {
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
		
		// Create connection
		wsURL := "ws" + server.URL[4:]
		clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		
		ws := NewWebsocketConnection(clientConn, "throughput-test")
		
		// Track received messages
		var receivedCount int32
		mockReader := mocks.NewWebsocketDataReaderInterface(t)
		mockReader.EXPECT().HandleIncomingWebsocketMessage(mock.Anything).RunAndReturn(
			func(msg []byte) {
				atomic.AddInt32(&receivedCount, 1)
			}).Maybe()
		mockReader.EXPECT().ReportConnectionError(mock.Anything).Maybe()
		
		ws.InitDataProcessing(mockReader)
		
		// Send many messages concurrently
		numSenders := 10
		messagesPerSender := 100
		var sendWg sync.WaitGroup
		var sentCount int32
		var sendErrors int32
		
		start := time.Now()
		
		for s := 0; s < numSenders; s++ {
			sendWg.Add(1)
			go func(sender int) {
				defer sendWg.Done()
				
				for i := 0; i < messagesPerSender; i++ {
					msg := []byte{byte(sender), byte(i % 256), byte(i / 256)}
					err := ws.WriteMessageToWebsocketConnection(msg)
					if err != nil {
						atomic.AddInt32(&sendErrors, 1)
					} else {
						atomic.AddInt32(&sentCount, 1)
					}
				}
			}(s)
		}
		
		sendWg.Wait()
		duration := time.Since(start)
		
		// Wait for all messages to be received
		assert.Eventually(t, func() bool {
			return atomic.LoadInt32(&receivedCount) >= atomic.LoadInt32(&sentCount)-10 // Allow some loss
		}, 5*time.Second, 100*time.Millisecond)
		
		// Calculate throughput
		totalMessages := atomic.LoadInt32(&sentCount)
		throughput := float64(totalMessages) / duration.Seconds()
		t.Logf("Throughput: %.2f messages/second (%d messages in %v)", throughput, totalMessages, duration)
		t.Logf("Send errors: %d, Received: %d", sendErrors, receivedCount)
		
		// Close connection
		ws.CloseDataConnection(websocket.CloseNormalClosure, "throughput test complete")
		
		// Verify closed
		isClosed, _ := ws.IsDataConnectionClosed()
		assert.True(t, isClosed)
	})
	
	t.Run("long_running_connections", func(t *testing.T) {
		if testing.Short() {
			t.Skip("Skipping long-running test in short mode")
		}
		
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
		
		// Create a few long-running connections
		numConnections := 5
		connections := make([]*WebsocketConnection, numConnections)
		
		for i := 0; i < numConnections; i++ {
			wsURL := "ws" + server.URL[4:]
			clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			require.NoError(t, err)
			
			ws := NewWebsocketConnection(clientConn, fmt.Sprintf("long-%d", i))
			connections[i] = ws
			
			mockReader := mocks.NewWebsocketDataReaderInterface(t)
			mockReader.EXPECT().HandleIncomingWebsocketMessage(mock.Anything).Maybe()
			mockReader.EXPECT().ReportConnectionError(mock.Anything).Maybe()
			
			ws.InitDataProcessing(mockReader)
		}
		
		// Run for 10 seconds, sending periodic messages
		stopTime := time.Now().Add(10 * time.Second)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		
		messageCount := 0
		for time.Now().Before(stopTime) {
			<-ticker.C
			for i, ws := range connections {
				_ = ws.WriteMessageToWebsocketConnection([]byte{byte(i), byte(messageCount)})
			}
			messageCount++
		}
		
		t.Logf("Sent %d messages over 10 seconds", messageCount*numConnections)
		
		// Close all connections
		for _, ws := range connections {
			ws.CloseDataConnection(websocket.CloseNormalClosure, "long test complete")
		}
		
		// Verify cleanup
		assert.Eventually(t, func() bool {
			return runtime.NumGoroutine() <= initialGoroutines+5
		}, 3*time.Second, 100*time.Millisecond, "goroutines leaked after long-running test")
	})
}