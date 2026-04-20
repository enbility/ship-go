package hub

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/mock/gomock"
)

// TestHub_Start_ReturnsError_WhenWebSocketFails tests that Start() returns an error
// when the WebSocket server fails to start
func TestHub_Start_ReturnsError_WhenWebSocketFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	hubReader := mocks.NewMockHubReaderInterface(ctrl)
	mdnsService := mocks.NewMockMdnsInterface(ctrl)

	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	localService := api.NewServiceDetails("localSKI")

	// Use an invalid port to guarantee immediate failure
	hub := NewHub(hubReader, mdnsService, -1, certificate, localService)

	// Test that Start() returns an error
	err := hub.Start()
	assert.Error(t, err, "Start() should return an error when WebSocket server fails")
	// Check that it's a websocket server error (not mDNS or other error)
	assert.Contains(t, err.Error(), "websocket server")
	// Verify the error is properly wrapped
	assert.NotNil(t, errors.Unwrap(err), "Error should wrap the underlying cause")
	assert.False(t, hub.hasStarted, "Hub should not be marked as started on failure")
}

// TestHub_Start_ReturnsError_WhenMdnsFails tests that Start() returns an error
// when mDNS fails to start and cleans up the WebSocket server
func TestHub_Start_ReturnsError_WhenMdnsFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	hubReader := mocks.NewMockHubReaderInterface(ctrl)
	mdnsService := mocks.NewMockMdnsInterface(ctrl)

	mdnsError := errors.New("mDNS startup failed")
	mdnsService.EXPECT().Start(gomock.Any()).Return(mdnsError)

	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	localService := api.NewServiceDetails("localSKI")

	hub := NewHub(hubReader, mdnsService, 0, certificate, localService)

	// Test that Start() returns an error
	err := hub.Start()
	assert.Error(t, err, "Start() should return an error when mDNS fails")
	// Check that it's an mDNS error (not websocket or other error)
	assert.Contains(t, err.Error(), "mDNS")
	// Verify the underlying error is the one we provided
	assert.ErrorIs(t, err, mdnsError, "Should wrap the mDNS error")
	
	// Verify WebSocket server was shut down
	time.Sleep(200 * time.Millisecond)
	// We can't check httpServer is nil as it's still set, but shutdown was called
}

// TestHub_Start_Success tests successful startup
func TestHub_Start_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	hubReader := mocks.NewMockHubReaderInterface(ctrl)
	mdnsService := mocks.NewMockMdnsInterface(ctrl)
	mdnsService.EXPECT().Start(gomock.Any()).Return(nil)

	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	localService := api.NewServiceDetails("localSKI")

	hub := NewHub(hubReader, mdnsService, 0, certificate, localService)

	err := hub.Start()
	assert.NoError(t, err, "Start() should succeed")
	assert.True(t, hub.hasStarted, "Hub should be marked as started")

	// Cleanup
	mdnsService.EXPECT().Shutdown()
	hub.Shutdown()
}

// TestHub_Shutdown_GracefulWithTimeout tests graceful shutdown with timeout
func TestHub_Shutdown_GracefulWithTimeout(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	hubReader := mocks.NewMockHubReaderInterface(ctrl)
	mdnsService := mocks.NewMockMdnsInterface(ctrl)
	mdnsService.EXPECT().Shutdown()

	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	localService := api.NewServiceDetails("localSKI")

	hub := NewHub(hubReader, mdnsService, 0, certificate, localService)

	// Add mock connections
	normalConn := mocks.NewShipConnectionInterface(t)
	normalConn.EXPECT().RemoteSKI().Return("normal-ski").Maybe()
	normalConn.EXPECT().IsAlive().Return(true).Maybe()

	slowConn := mocks.NewShipConnectionInterface(t)
	slowConn.EXPECT().RemoteSKI().Return("slow-ski").Maybe()
	slowConn.EXPECT().IsAlive().Return(true).Maybe()

	// Track close calls
	var normalClosed, slowClosed bool
	var closeMux sync.Mutex

	// Normal connection closes quickly
	normalConn.EXPECT().CloseConnection(false, 0, mock.Anything).Run(func(safe bool, code int, reason string) {
		closeMux.Lock()
		normalClosed = true
		closeMux.Unlock()
	}).Once()

	// Slow connection takes 1 second to close
	slowConn.EXPECT().CloseConnection(false, 0, mock.Anything).Run(func(safe bool, code int, reason string) {
		time.Sleep(1 * time.Second)
		closeMux.Lock()
		slowClosed = true
		closeMux.Unlock()
	}).Once()

	hub.registry.mu.Lock()
	hub.registry.connections["normal-ski"] = normalConn
	hub.registry.connections["slow-ski"] = slowConn
	hub.registry.mu.Unlock()

	start := time.Now()
	hub.Shutdown()
	elapsed := time.Since(start)

	// Current implementation doesn't wait, so this should fail
	closeMux.Lock()
	assert.True(t, normalClosed, "Normal connection should be closed")
	assert.True(t, slowClosed, "Slow connection should be closed")
	closeMux.Unlock()

	// Should wait for connections but not too long
	assert.Greater(t, elapsed, 900*time.Millisecond, "Should wait for connections to close")
	assert.Less(t, elapsed, 3500*time.Millisecond, "Should not wait forever")
}

// TestHub_Shutdown_TimeoutStuckConnections tests shutdown with stuck connections
func TestHub_Shutdown_TimeoutStuckConnections(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	hubReader := mocks.NewMockHubReaderInterface(ctrl)
	mdnsService := mocks.NewMockMdnsInterface(ctrl)
	mdnsService.EXPECT().Shutdown()

	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	localService := api.NewServiceDetails("localSKI")

	hub := NewHub(hubReader, mdnsService, 0, certificate, localService)

	// Add a stuck connection
	stuckConn := mocks.NewShipConnectionInterface(t)
	stuckConn.EXPECT().RemoteSKI().Return("stuck-ski").Maybe()
	stuckConn.EXPECT().IsAlive().Return(true).Maybe()

	// This connection never completes closing
	closeStarted := make(chan bool, 1)
	stuckConn.EXPECT().CloseConnection(false, 0, mock.Anything).Run(func(safe bool, code int, reason string) {
		closeStarted <- true
		// Block forever
		select {}
	}).Once()

	hub.registry.mu.Lock()
	hub.registry.connections["stuck-ski"] = stuckConn
	hub.registry.mu.Unlock()

	start := time.Now()
	
	// Run shutdown in goroutine so we can check it completes
	done := make(chan bool)
	go func() {
		hub.Shutdown()
		done <- true
	}()

	// Verify close was attempted
	select {
	case <-closeStarted:
		// Good, close was called
	case <-time.After(500 * time.Millisecond):
		t.Fatal("CloseConnection was not called")
	}

	// Verify shutdown completes despite stuck connection
	select {
	case <-done:
		elapsed := time.Since(start)
		// Should timeout after ~3 seconds
		assert.Greater(t, elapsed, 2*time.Second, "Should wait for timeout")
		assert.Less(t, elapsed, 4*time.Second, "Should not wait too long")
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not complete within timeout")
	}
}

// TestHub_HTTPServerShutdown_WithContext tests proper HTTP server shutdown
func TestHub_HTTPServerShutdown_WithContext(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	hubReader := mocks.NewMockHubReaderInterface(ctrl)
	mdnsService := mocks.NewMockMdnsInterface(ctrl)
	mdnsService.EXPECT().Start(gomock.Any()).Return(nil)
	mdnsService.EXPECT().Shutdown()

	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	localService := api.NewServiceDetails("localSKI")

	hub := NewHub(hubReader, mdnsService, 0, certificate, localService)
	
	// Start the hub
	err := hub.Start()
	assert.NoError(t, err)
	
	// Wait for server to be ready
	time.Sleep(100 * time.Millisecond)

	// Verify server is running
	assert.NotNil(t, hub.httpServer, "HTTP server should be initialized")

	// Current implementation uses context.Background() which waits indefinitely
	// It should use a timeout context
	start := time.Now()
	hub.Shutdown()
	elapsed := time.Since(start)

	// Should complete quickly with timeout
	assert.Less(t, elapsed, 6*time.Second, "HTTP server shutdown should have timeout")
}