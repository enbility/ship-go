package hub

import (
	"crypto/tls"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestConnectionRegistration_ConcurrentCloseAndReplace tests race conditions
// in connection registration when a connection is being closed while a new one registers
func TestConnectionRegistration_ConcurrentCloseAndReplace(t *testing.T) {
	hub := setupTestHub(t)
	
	const testSKI = "test-ski-123"
	const numIterations = 100
	
	for i := 0; i < numIterations; i++ {
		// Create two different connections
		conn1 := mocks.NewShipConnectionInterface(t)
		conn1.EXPECT().RemoteSKI().Return(testSKI).Maybe()
		conn1.EXPECT().DataHandler().Return(nil).Maybe()
		conn1.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()
		
		conn2 := mocks.NewShipConnectionInterface(t)
		conn2.EXPECT().RemoteSKI().Return(testSKI).Maybe()
		conn2.EXPECT().DataHandler().Return(nil).Maybe()
		conn2.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()
		
		// Register first connection
		hub.registerConnection(conn1)
		
		var wg sync.WaitGroup
		wg.Add(2)
		
		// Goroutine 1: Close and unregister the first connection
		go func() {
			defer wg.Done()
			// Simulate HandleConnectionClosed logic
			if existingC := hub.connectionForSKI(testSKI); existingC != nil {
				// Small delay to increase race probability
				time.Sleep(time.Microsecond)
				if existingC == conn1 {
					hub.muxCon.Lock()
					delete(hub.connections, testSKI)
					hub.muxCon.Unlock()
				}
			}
		}()
		
		// Goroutine 2: Register a new connection
		go func() {
			defer wg.Done()
			hub.registerConnection(conn2)
		}()
		
		wg.Wait()
		
		// Verify state is consistent
		finalConn := hub.connectionForSKI(testSKI)
		assert.True(t, finalConn == nil || finalConn == conn2,
			"Final connection should be either nil or conn2, iteration %d", i)
	}
}

// TestUnregisterConnectionIfMatch tests the new atomic unregister method
func TestUnregisterConnectionIfMatch(t *testing.T) {
	tests := []struct {
		name           string
		setupConn      bool
		matchingConn   bool
		expectSuccess  bool
		expectRemoved  bool
	}{
		{
			name:          "successful unregister with matching connection",
			setupConn:     true,
			matchingConn:  true,
			expectSuccess: true,
			expectRemoved: true,
		},
		{
			name:          "no-op when connection doesn't match",
			setupConn:     true,
			matchingConn:  false,
			expectSuccess: false,
			expectRemoved: false,
		},
		{
			name:          "no-op when no connection exists",
			setupConn:     false,
			matchingConn:  false,
			expectSuccess: false,
			expectRemoved: true, // No connection to begin with
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub := setupTestHub(t)
			const testSKI = "test-ski"
			
			conn := mocks.NewShipConnectionInterface(t)
			conn.EXPECT().RemoteSKI().Return(testSKI).Maybe()
			
			if tt.setupConn {
				hub.registerConnection(conn)
			}
			
			// Determine which connection to pass
			connToUnregister := conn
			if !tt.matchingConn {
				// Create a different connection
				otherConn := mocks.NewShipConnectionInterface(t)
				connToUnregister = otherConn
			}
			
			// Test the new method (to be implemented)
			success := hub.UnregisterConnectionIfMatch(testSKI, connToUnregister)
			
			assert.Equal(t, tt.expectSuccess, success)
			
			// Verify connection state
			finalConn := hub.connectionForSKI(testSKI)
			if tt.expectRemoved {
				assert.Nil(t, finalConn)
			} else {
				assert.Equal(t, conn, finalConn)
			}
		})
	}
}

// TestConcurrentConnectionOperations tests multiple concurrent operations
func TestConcurrentConnectionOperations(t *testing.T) {
	hub := setupTestHub(t)
	
	var wg sync.WaitGroup
	const numGoroutines = 100
	const numSKIs = 10
	
	// Create a pool of connections
	connections := make([]api.ShipConnectionInterface, numSKIs)
	skis := make([]string, numSKIs)
	
	for i := 0; i < numSKIs; i++ {
		ski := string(rune('a' + i)) + "-ski"
		skis[i] = ski
		
		conn := mocks.NewShipConnectionInterface(t)
		conn.EXPECT().RemoteSKI().Return(ski).Maybe()
		conn.EXPECT().DataHandler().Return(nil).Maybe()
		conn.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()
		connections[i] = conn
	}
	
	// Run concurrent operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			
			// Pick a random SKI
			skiIdx := idx % numSKIs
			ski := skis[skiIdx]
			conn := connections[skiIdx]
			
			// Perform random operation
			switch idx % 3 {
			case 0: // Register
				hub.registerConnection(conn)
			case 1: // Read
				_ = hub.connectionForSKI(ski)
			case 2: // Unregister if match
				hub.UnregisterConnectionIfMatch(ski, conn)
			}
		}(i)
	}
	
	wg.Wait()
	
	// Verify no panics and state is consistent
	for i, ski := range skis {
		conn := hub.connectionForSKI(ski)
		if conn != nil {
			assert.Equal(t, connections[i], conn, "Connection mismatch for SKI %s", ski)
		}
	}
}

// setupTestHub creates a test hub with mocked dependencies
func setupTestHub(t *testing.T) *Hub {
	mdns := mocks.NewMdnsInterface(t)
	hubReader := mocks.NewHubReaderInterface(t)
	
	// Set up expectations
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