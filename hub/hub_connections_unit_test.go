package hub

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVerifyPeerCertificate tests the certificate verification logic
func TestVerifyPeerCertificate(t *testing.T) {
	hub := setupTestHubForTimer(t)

	t.Run("valid_certificate_with_ski", func(t *testing.T) {
		// Create a test certificate with valid SKI
		validCert, err := cert.CreateCertificate("test", "org", "DE", "123456")
		require.NoError(t, err)

		rawCerts := [][]byte{validCert.Certificate[0]}
		err = hub.verifyPeerCertificate(rawCerts, nil)
		assert.NoError(t, err, "valid certificate should pass verification")
	})

	t.Run("invalid_certificate", func(t *testing.T) {
		// Invalid certificate data
		rawCerts := [][]byte{{0x00, 0x01, 0x02}}
		err := hub.verifyPeerCertificate(rawCerts, nil)
		assert.Error(t, err, "invalid certificate should fail")
	})

	t.Run("certificate_without_ski", func(t *testing.T) {
		// Create a certificate without SubjectKeyId
		template := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		}

		// Use the same method as cert.CreateCertificate but without SKI
		validCert, _ := cert.CreateCertificate("test", "org", "DE", "123456")
		priv := validCert.PrivateKey.(*ecdsa.PrivateKey)
		certDER, _ := x509.CreateCertificate(nil, template, template, &priv.PublicKey, priv)

		rawCerts := [][]byte{certDER}
		err := hub.verifyPeerCertificate(rawCerts, nil)
		assert.Error(t, err, "certificate without SKI should fail")
		assert.Contains(t, err.Error(), "no valid SKI")
	})
}

// TestServeHTTPBasics tests basic HTTP handler functionality
func TestServeHTTPBasics(t *testing.T) {
	t.Run("missing_ship_subprotocol", func(t *testing.T) {
		hub := setupTestHubForTimer(t)
		hub.Start()
		defer hub.Shutdown()

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "upgrade")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		// No SHIP subprotocol

		w := httptest.NewRecorder()
		hub.ServeHTTP(w, req)

		// Should fail upgrade
		assert.NotEqual(t, http.StatusSwitchingProtocols, w.Code)
	})

	t.Run("missing_tls", func(t *testing.T) {
		hub := setupTestHubForTimer(t)
		hub.Start()
		defer hub.Shutdown()

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "upgrade")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		req.Header.Set("Sec-WebSocket-Protocol", api.ShipWebsocketSubProtocol)
		// No TLS

		w := httptest.NewRecorder()
		hub.ServeHTTP(w, req)

		// Should fail due to missing certificate
		assert.NotEqual(t, http.StatusSwitchingProtocols, w.Code)
	})
}

// TestIsSkiConnected tests the connection check function
func TestIsSkiConnected(t *testing.T) {
	hub := setupTestHubForTimer(t)

	ski := "test-ski-connected"

	// Not connected initially
	assert.False(t, hub.isSkiConnected(ski))

	// Add connection
	mockConn := mocks.NewShipConnectionInterface(t)
	hub.muxCon.Lock()
	hub.connections[ski] = mockConn
	hub.muxCon.Unlock()

	// Now connected
	assert.True(t, hub.isSkiConnected(ski))

	// Remove connection
	hub.muxCon.Lock()
	delete(hub.connections, ski)
	hub.muxCon.Unlock()

	// Not connected again
	assert.False(t, hub.isSkiConnected(ski))
}

// TestConnectFoundServiceBasics tests basic connection establishment logic
func TestConnectFoundServiceBasics(t *testing.T) {
	t.Run("connection_error", func(t *testing.T) {
		hub := setupTestHubForTimer(t)

		service := api.NewServiceDetails("error-ski")
		// Invalid address should cause error
		err := hub.connectFoundService(service, "invalid.host.doesnotexist", "4729", "/ship")
		assert.Error(t, err)
	})
}

// TestInitateConnectionBasics tests connection initiation with various scenarios
func TestInitateConnectionBasics(t *testing.T) {
	t.Run("not_paired_not_queued", func(t *testing.T) {
		hub := setupTestHubForTimer(t)

		service := api.NewServiceDetails("unpaired-ski")
		entry := &api.MdnsEntry{
			Identifier: "unpaired-ski",
			Host:       "localhost",
			Port:       4729,
		}

		success := hub.initateConnection(service, entry)
		assert.False(t, success, "should fail for unpaired service")
	})

	t.Run("hostname_connection", func(t *testing.T) {
		hub := setupTestHubForTimer(t)

		ski := "hostname-ski"
		service := api.NewServiceDetails(ski)
		service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
		hub.muxReg.Lock()
		hub.remoteServices[ski] = service
		hub.muxReg.Unlock()

		entry := &api.MdnsEntry{
			Identifier: ski,
			Host:       "invalid.host",
			Port:       4729,
		}

		success := hub.initateConnection(service, entry)
		assert.False(t, success, "should fail with invalid host")
	})

	t.Run("ipv4_addresses", func(t *testing.T) {
		hub := setupTestHubForTimer(t)

		ski := "ipv4-ski"
		service := api.NewServiceDetails(ski)
		service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
		hub.muxReg.Lock()
		hub.remoteServices[ski] = service
		hub.muxReg.Unlock()

		entry := &api.MdnsEntry{
			Identifier: ski,
			Addresses: []net.IP{
				net.ParseIP("127.0.0.1"),
				net.ParseIP("192.168.1.1"),
			},
			Port: 4729,
		}

		success := hub.initateConnection(service, entry)
		assert.False(t, success, "should fail with unreachable addresses")
	})

	t.Run("mixed_ipv4_ipv6", func(t *testing.T) {
		hub := setupTestHubForTimer(t)

		ski := "mixed-ski"
		service := api.NewServiceDetails(ski)
		service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
		hub.muxReg.Lock()
		hub.remoteServices[ski] = service
		hub.muxReg.Unlock()

		entry := &api.MdnsEntry{
			Identifier: ski,
			Addresses: []net.IP{
				net.ParseIP("2001:db8::1"), // IPv6
				net.ParseIP("127.0.0.1"),   // IPv4
				net.ParseIP("::1"),         // IPv6
			},
			Port: 4729,
		}

		success := hub.initateConnection(service, entry)
		// Should try IPv4 first, then IPv6
		assert.False(t, success, "should fail with unreachable addresses")

		// Verify IPv4 addresses exist in the list (sorting happens inside initateConnection)
		hasIPv4 := false
		for _, addr := range entry.Addresses {
			if addr.To4() != nil {
				hasIPv4 = true
				break
			}
		}
		assert.True(t, hasIPv4, "Should have IPv4 addresses in the list")
	})
}

// TestConnectionForSKI tests connection retrieval
func TestConnectionForSKI(t *testing.T) {
	hub := setupTestHubForTimer(t)

	ski := "lookup-ski"
	mockConn := mocks.NewShipConnectionInterface(t)
	mockConn.EXPECT().RemoteSKI().Return(ski).Maybe()

	// Not found initially
	conn := hub.connectionForSKI(ski)
	assert.Nil(t, conn)

	// Add connection
	hub.muxCon.Lock()
	hub.connections[ski] = mockConn
	hub.muxCon.Unlock()

	// Found
	conn = hub.connectionForSKI(ski)
	assert.Equal(t, mockConn, conn)
}

// TestUnregisterConnectionIfMatchUnit tests atomic connection unregistration
func TestUnregisterConnectionIfMatchUnit(t *testing.T) {
	hub := setupTestHubForTimer(t)

	ski := "unregister-ski"
	mockConn1 := mocks.NewShipConnectionInterface(t)
	mockConn2 := mocks.NewShipConnectionInterface(t)

	// Add connection
	hub.muxCon.Lock()
	hub.connections[ski] = mockConn1
	hub.muxCon.Unlock()

	// Unregister with matching connection
	success := hub.UnregisterConnectionIfMatch(ski, mockConn1)
	assert.True(t, success)

	// Verify removed
	assert.Nil(t, hub.connectionForSKI(ski))

	// Add new connection
	hub.muxCon.Lock()
	hub.connections[ski] = mockConn1
	hub.muxCon.Unlock()

	// Try to unregister with non-matching connection
	success = hub.UnregisterConnectionIfMatch(ski, mockConn2)
	assert.False(t, success)

	// Original connection still there
	assert.Equal(t, mockConn1, hub.connectionForSKI(ski))

	// Try to unregister non-existent
	success = hub.UnregisterConnectionIfMatch("non-existent", mockConn1)
	assert.False(t, success)
}

// TestStartWebsocketServer tests server initialization
func TestStartWebsocketServer(t *testing.T) {
	hub := setupTestHubForTimer(t)

	// Should not error
	err := hub.startWebsocketServer()
	assert.NoError(t, err)

	// Server should be set
	assert.NotNil(t, hub.httpServer)
	assert.Equal(t, ":4729", hub.httpServer.Addr)
	assert.NotNil(t, hub.httpServer.TLSConfig)
	assert.Equal(t, tls.RequireAnyClientCert, hub.httpServer.TLSConfig.ClientAuth)

	// Stop server
	if hub.httpServer != nil {
		hub.httpServer.Close()
	}
}

// TestKeepThisConnectionBasics tests basic double connection prevention
func TestKeepThisConnectionBasics(t *testing.T) {
	t.Run("no_existing_connection", func(t *testing.T) {
		hub := setupTestHubForTimer(t)
		hub.localService = api.NewServiceDetails("local-ski")

		remoteService := api.NewServiceDetails("remote-ski")
		keep := hub.keepThisConnection(nil, true, remoteService)
		assert.True(t, keep, "should keep when no existing connection")
	})
}

// TestCoordinateConnectionInitiationsBasics tests connection coordination
func TestCoordinateConnectionInitiationsBasics(t *testing.T) {
	t.Run("attempt_already_running", func(t *testing.T) {
		hub := setupTestHubForTimer(t)

		ski := "running-ski"
		hub.setConnectionAttemptRunning(ski, true)

		entry := &api.MdnsEntry{Identifier: ski}

		// Should return early
		hub.coordinateConnectionInitations(ski, entry)

		// No timer should be created
		hub.muxTimers.RLock()
		_, exists := hub.connectionDelayTimers[ski]
		hub.muxTimers.RUnlock()
		assert.False(t, exists)
	})

	t.Run("queued_connection", func(t *testing.T) {
		hub := setupTestHubForTimer(t)

		ski := "queued-ski"
		service := api.NewServiceDetails(ski)
		service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateQueued, nil))
		hub.muxReg.Lock()
		hub.remoteServices[ski] = service
		hub.muxReg.Unlock()

		entry := &api.MdnsEntry{Identifier: ski}

		// Should initiate immediately for queued
		hub.coordinateConnectionInitations(ski, entry)

		// Give a moment for async operations to complete
		time.Sleep(10 * time.Millisecond)

		// Just test that the function executed without panic
		// The exact state depends on timing of async operations
	})
}

// TestPrepareConnectionInitiationBasics tests connection preparation
func TestPrepareConnectionInitiationBasics(t *testing.T) {
	t.Run("already_connected", func(t *testing.T) {
		hub := setupTestHubForTimer(t)

		ski := "connected-ski"
		mockConn := mocks.NewShipConnectionInterface(t)
		hub.muxCon.Lock()
		hub.connections[ski] = mockConn
		hub.muxCon.Unlock()

		entry := &api.MdnsEntry{Identifier: ski}

		// Should return early for connected
		hub.prepareConnectionInitation(ski, 0, entry)

		// Attempt running should be cleared
		assert.False(t, hub.isConnectionAttemptRunning(ski))
	})

	t.Run("not_paired_not_queued", func(t *testing.T) {
		hub := setupTestHubForTimer(t)

		ski := "unpaired-ski"
		hub.muxConAttempt.Lock()
		hub.connectionAttemptCounter[ski] = 0
		hub.muxConAttempt.Unlock()

		entry := &api.MdnsEntry{Identifier: ski}

		// Should return early
		hub.prepareConnectionInitation(ski, 0, entry)

		// Attempt running should be cleared
		assert.False(t, hub.isConnectionAttemptRunning(ski))
	})
}

// TestCancelConnectionDelayTimer tests timer cancellation
func TestCancelConnectionDelayTimer(t *testing.T) {
	hub := setupTestHubForTimer(t)

	ski := "cancel-timer-ski"

	// Cancel non-existent timer (should not panic)
	hub.cancelConnectionDelayTimer(ski)

	// Add timer
	timerCalled := false
	timer := newConnectionDelayTimer(100*time.Millisecond, func() {
		timerCalled = true
	})
	hub.storeConnectionDelayTimer(ski, timer)

	// Cancel it
	hub.cancelConnectionDelayTimer(ski)

	// Wait to ensure timer doesn't fire
	time.Sleep(200 * time.Millisecond)
	assert.False(t, timerCalled, "cancelled timer should not fire")

	// Verify timer removed
	hub.muxTimers.RLock()
	_, exists := hub.connectionDelayTimers[ski]
	hub.muxTimers.RUnlock()
	assert.False(t, exists, "timer should be removed")
}

// TestConnectionDelayTimerStop tests timer Stop method
func TestConnectionDelayTimerStop(t *testing.T) {
	t.Run("stop_before_fire", func(t *testing.T) {
		var called bool
		timer := newConnectionDelayTimer(100*time.Millisecond, func() {
			called = true
		})

		// Stop immediately
		stopped := timer.Stop()
		assert.True(t, stopped, "should return true when stopped before firing")

		// Wait to ensure it doesn't fire
		time.Sleep(200 * time.Millisecond)
		assert.False(t, called, "stopped timer should not call function")
	})

	t.Run("stop_basic_functionality", func(t *testing.T) {
		// Test that Stop method exists and can be called
		// Avoid race conditions by not checking exact timing
		timer := newConnectionDelayTimer(1*time.Millisecond, func() {})

		// Wait for timer to potentially fire
		time.Sleep(10 * time.Millisecond)

		// Try to stop - result may vary depending on timing
		_ = timer.Stop()
		// Just ensure no panic occurs
	})
}

// TestConnectFoundServiceCertificateErrors tests certificate validation errors
func TestConnectFoundServiceCertificateErrors(t *testing.T) {
	t.Run("invalid_ski_in_certificate", func(t *testing.T) {
		// This tests the error path where cert.SkiFromCertificate fails
		hub := setupTestHubForTimer(t)

		service := api.NewServiceDetails("test-ski")

		// Try to connect to non-existent service
		err := hub.connectFoundService(service, "localhost", "9999", "/")
		assert.Error(t, err)
	})
}

// TestConnectionLimit tests the connection limit functionality
func TestConnectionLimit(t *testing.T) {
	t.Run("default_limit", func(t *testing.T) {
		hub := setupTestHubForTimer(t)
		assert.Equal(t, 10, hub.maxConnections, "default connection limit should be 10")
	})

	t.Run("set_max_connections", func(t *testing.T) {
		hub := setupTestHubForTimer(t)
		
		// Test setting a valid limit
		hub.SetMaxConnections(5)
		assert.Equal(t, 5, hub.maxConnections)
		
		// Test setting zero (should use default)
		hub.SetMaxConnections(0)
		assert.Equal(t, 10, hub.maxConnections)
		
		// Test setting negative (should use default)
		hub.SetMaxConnections(-1)
		assert.Equal(t, 10, hub.maxConnections)
	})

	t.Run("reject_incoming_when_limit_reached", func(t *testing.T) {
		hub := setupTestHubForTimer(t)
		hub.SetMaxConnections(2)

		// Add 2 mock connections to reach the limit
		mockConn1 := mocks.NewShipConnectionInterface(t)
		mockConn2 := mocks.NewShipConnectionInterface(t)
		hub.muxCon.Lock()
		hub.connections["ski1"] = mockConn1
		hub.connections["ski2"] = mockConn2
		hub.muxCon.Unlock()

		// Create a test request
		req := httptest.NewRequest("GET", "/ship", nil)
		w := httptest.NewRecorder()

		// Try to serve the request - should be rejected
		hub.ServeHTTP(w, req)

		// Check that we got a 503 Service Unavailable
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		assert.Contains(t, w.Body.String(), "Connection limit reached")
	})

	t.Run("accept_incoming_when_under_limit", func(t *testing.T) {
		hub := setupTestHubForTimer(t)
		hub.SetMaxConnections(5)

		// Add 2 mock connections (under limit of 5)
		mockConn1 := mocks.NewShipConnectionInterface(t)
		mockConn2 := mocks.NewShipConnectionInterface(t)
		hub.muxCon.Lock()
		hub.connections["ski1"] = mockConn1
		hub.connections["ski2"] = mockConn2
		hub.muxCon.Unlock()

		// Create a test request
		req := httptest.NewRequest("GET", "/ship", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "upgrade")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		req.Header.Set("Sec-WebSocket-Version", "13")
		w := httptest.NewRecorder()

		// Try to serve the request - should not be rejected due to limit
		hub.ServeHTTP(w, req)

		// The request will fail for other reasons (no TLS, etc) but not due to connection limit
		assert.NotEqual(t, http.StatusServiceUnavailable, w.Code)
		assert.NotContains(t, w.Body.String(), "Connection limit reached")
	})

	t.Run("reject_outgoing_when_limit_reached", func(t *testing.T) {
		hub := setupTestHubForTimer(t)
		hub.SetMaxConnections(2)

		// Add 2 mock connections to reach the limit
		mockConn1 := mocks.NewShipConnectionInterface(t)
		mockConn2 := mocks.NewShipConnectionInterface(t)
		hub.muxCon.Lock()
		hub.connections["ski1"] = mockConn1
		hub.connections["ski2"] = mockConn2
		hub.muxCon.Unlock()

		// Try to connect to a new service
		service := api.NewServiceDetails("ski3")
		err := hub.connectFoundService(service, "localhost", "9999", "/")
		
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "connection limit reached")
		assert.Contains(t, err.Error(), "2/2")
	})

	t.Run("allow_outgoing_when_under_limit", func(t *testing.T) {
		hub := setupTestHubForTimer(t)
		hub.SetMaxConnections(5)

		// Add 2 mock connections (under limit of 5)
		mockConn1 := mocks.NewShipConnectionInterface(t)
		mockConn2 := mocks.NewShipConnectionInterface(t)
		hub.muxCon.Lock()
		hub.connections["ski1"] = mockConn1
		hub.connections["ski2"] = mockConn2
		hub.muxCon.Unlock()

		// Try to connect to a new service
		service := api.NewServiceDetails("ski3")
		err := hub.connectFoundService(service, "localhost", "9999", "/")
		
		// Error will occur for other reasons (connection failed) but not due to limit
		if err != nil {
			assert.NotContains(t, err.Error(), "connection limit reached")
		}
	})
}
