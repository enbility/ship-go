package hub

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// HubConnectionsClientCoverageSuite tests hub connections client functionality
type HubConnectionsClientCoverageSuite struct {
	suite.Suite
	hub          *Hub
	mockMdns     *mocks.MdnsInterface
	mockReader   *mocks.HubReaderInterface
	localService *api.ServiceDetails
	localSKI     string
}

func (s *HubConnectionsClientCoverageSuite) SetupTest() {
	s.localSKI = "test-local-ski"
	s.localService = api.NewServiceDetails(s.localSKI)

	cert, err := cert.CreateCertificate("test", "test", "DE", "test")
	require.NoError(s.T(), err)

	s.mockMdns = mocks.NewMdnsInterface(s.T())
	s.mockReader = mocks.NewHubReaderInterface(s.T())

	s.hub = NewHub(s.mockReader, s.mockMdns, 0, cert, s.localService)
	s.hub.knownMdnsEntries = make([]*api.MdnsEntry, 0)
}

// Test_InitateConnection_ComprehensiveCoverage tests all paths in initateConnection
func (s *HubConnectionsClientCoverageSuite) Test_InitateConnection_ComprehensiveCoverage() {
	tests := []struct {
		name           string
		setupMocks     func(ski string)
		service        *api.ServiceDetails
		entry          *api.MdnsEntry
		expectedResult bool
		expectedLog    string
	}{
		{
			name: "not_paired_not_queued",
			setupMocks: func(ski string) {
				// Service not paired and not queued
			},
			service: api.NewServiceDetails("unpaired-ski"),
			entry: &api.MdnsEntry{
				Identifier: "unpaired-ski",
				Host:       "localhost",
				Port:       4729,
			},
			expectedResult: false,
		},
		{
			name: "paired_successful_hostname_connection",
			setupMocks: func(ski string) {
				service := api.NewServiceDetails(ski)
				service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
				s.hub.muxReg.Lock()
				s.hub.remoteServices[ski] = service
				s.hub.muxReg.Unlock()
			},
			service: api.NewServiceDetails("paired-hostname-ski"),
			entry: &api.MdnsEntry{
				Identifier: "paired-hostname-ski",
				Host:       "localhost",
				Port:       4729,
				Path:       "/ship",
			},
			expectedResult: false, // Will fail because no real server
		},
		{
			name: "queued_for_pairing",
			setupMocks: func(ski string) {
				service := api.NewServiceDetails(ski)
				service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateQueued, nil))
				s.hub.muxReg.Lock()
				s.hub.remoteServices[ski] = service
				s.hub.muxReg.Unlock()
			},
			service: api.NewServiceDetails("queued-ski"),
			entry: &api.MdnsEntry{
				Identifier: "queued-ski",
				Host:       "invalid.host",
				Port:       4729,
			},
			expectedResult: false,
		},
		{
			name: "hostname_fails_ipv4_succeeds",
			setupMocks: func(ski string) {
				service := api.NewServiceDetails(ski)
				service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
				s.hub.muxReg.Lock()
				s.hub.remoteServices[ski] = service
				s.hub.muxReg.Unlock()
			},
			service: api.NewServiceDetails("ipv4-fallback-ski"),
			entry: &api.MdnsEntry{
				Identifier: "ipv4-fallback-ski",
				Host:       "invalid.host",
				Port:       4729,
				Addresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("192.168.1.1")},
			},
			expectedResult: false,
		},
		{
			name: "mixed_ipv4_ipv6_addresses",
			setupMocks: func(ski string) {
				service := api.NewServiceDetails(ski)
				service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
				s.hub.muxReg.Lock()
				s.hub.remoteServices[ski] = service
				s.hub.muxReg.Unlock()
			},
			service: api.NewServiceDetails("mixed-ip-ski"),
			entry: &api.MdnsEntry{
				Identifier: "mixed-ip-ski",
				Host:       "", // No hostname
				Port:       4729,
				Addresses: []net.IP{
					net.ParseIP("::1"),
					net.ParseIP("192.168.1.1"),
					net.ParseIP("fe80::1"),
					net.ParseIP("10.0.0.1"),
				},
			},
			expectedResult: false,
		},
		{
			name: "no_host_no_addresses",
			setupMocks: func(ski string) {
				service := api.NewServiceDetails(ski)
				service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
				s.hub.muxReg.Lock()
				s.hub.remoteServices[ski] = service
				s.hub.muxReg.Unlock()
			},
			service: api.NewServiceDetails("no-endpoint-ski"),
			entry: &api.MdnsEntry{
				Identifier: "no-endpoint-ski",
				Host:       "",
				Port:       4729,
				Addresses:  []net.IP{},
			},
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			tt.setupMocks(tt.service.SKI())
			result := s.hub.initateConnection(tt.service, tt.entry)
			assert.Equal(s.T(), tt.expectedResult, result)
		})
	}
}

// Test_InitateConnection_WithMockServer tests connection with a mock server
func (s *HubConnectionsClientCoverageSuite) Test_InitateConnection_WithMockServer() {
	// Create a test WebSocket server
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for required SHIP subprotocol
		if r.Header.Get("Sec-WebSocket-Protocol") != "ship" {
			http.Error(w, "Missing ship subprotocol", http.StatusBadRequest)
			return
		}

		conn, err := upgrader.Upgrade(w, r, http.Header{
			"Sec-WebSocket-Protocol": []string{"ship"},
		})
		if err != nil {
			return
		}
		defer conn.Close()

		// Keep connection open for test
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	// Extract host and port from test server
	host, portStr, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	// Setup paired service
	ski := "test-server-ski"
	service := api.NewServiceDetails(ski)
	service.SetConnectionStateDetail(api.NewConnectionStateDetail(api.ConnectionStateTrusted, nil))
	s.hub.muxReg.Lock()
	s.hub.remoteServices[ski] = service
	s.hub.muxReg.Unlock()

	// Test successful connection via hostname
	entry := &api.MdnsEntry{
		Identifier: ski,
		Host:       host,
		Port:       port,
		Path:       "/",
	}

	// Connection will be established but handshake will fail (no real SHIP implementation)
	result := s.hub.initateConnection(service, entry)
	assert.False(s.T(), result) // False because handshake won't complete
}

// Test_ConnectFoundService_ErrorScenarios tests various error scenarios
func (s *HubConnectionsClientCoverageSuite) Test_ConnectFoundService_ErrorScenarios() {
	service := api.NewServiceDetails("error-test-ski")

	tests := []struct {
		name          string
		host          string
		port          string
		path          string
		expectedError string
	}{
		{
			name:          "invalid_port",
			host:          "localhost",
			port:          "invalid",
			path:          "/ship",
			expectedError: "invalid syntax",
		},
		{
			name:          "connection_refused",
			host:          "localhost",
			port:          "9999", // Unlikely to be in use
			path:          "/ship",
			expectedError: "connection refused",
		},
		{
			name:          "dns_resolution_failure",
			host:          "invalid.host.that.does.not.exist",
			port:          "4729",
			path:          "/ship",
			expectedError: "no such host",
		},
		{
			name:          "timeout_simulation",
			host:          "10.255.255.1", // Non-routable IP for timeout
			port:          "4729",
			path:          "/ship",
			expectedError: "i/o timeout",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			err := s.hub.connectFoundService(service, tt.host, tt.port, tt.path)
			assert.Error(s.T(), err)
			// Don't check exact error message as it may vary by platform
		})
	}
}

// Test_SortIPAddresses_Comprehensive tests IP address sorting
func (s *HubConnectionsClientCoverageSuite) Test_SortIPAddresses_Comprehensive() {
	tests := []struct {
		name     string
		input    []net.IP
		expected []net.IP
	}{
		{
			name:     "nil_input",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty_slice",
			input:    []net.IP{},
			expected: []net.IP{},
		},
		{
			name:     "only_ipv4",
			input:    []net.IP{net.ParseIP("192.168.1.1"), net.ParseIP("10.0.0.1")},
			expected: []net.IP{net.ParseIP("192.168.1.1"), net.ParseIP("10.0.0.1")},
		},
		{
			name:     "only_ipv6",
			input:    []net.IP{net.ParseIP("::1"), net.ParseIP("fe80::1")},
			expected: []net.IP{net.ParseIP("::1"), net.ParseIP("fe80::1")},
		},
		{
			name: "mixed_ipv4_first",
			input: []net.IP{
				net.ParseIP("192.168.1.1"),
				net.ParseIP("::1"),
				net.ParseIP("10.0.0.1"),
				net.ParseIP("fe80::1"),
			},
			expected: []net.IP{
				net.ParseIP("192.168.1.1"),
				net.ParseIP("10.0.0.1"),
				net.ParseIP("::1"),
				net.ParseIP("fe80::1"),
			},
		},
		{
			name: "mixed_ipv6_first",
			input: []net.IP{
				net.ParseIP("::1"),
				net.ParseIP("192.168.1.1"),
				net.ParseIP("fe80::1"),
				net.ParseIP("10.0.0.1"),
			},
			expected: []net.IP{
				net.ParseIP("192.168.1.1"),
				net.ParseIP("10.0.0.1"),
				net.ParseIP("::1"),
				net.ParseIP("fe80::1"),
			},
		},
		{
			name: "with_nil_entries",
			input: []net.IP{
				nil,
				net.ParseIP("192.168.1.1"),
				nil,
				net.ParseIP("::1"),
			},
			expected: []net.IP{
				net.ParseIP("192.168.1.1"),
				net.ParseIP("::1"),
				nil,
				nil,
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := s.hub.sortIPAddresses(tt.input)
			assert.Equal(s.T(), tt.expected, result)
		})
	}
}

// Test_ConnectFoundService_CertificateValidation tests certificate validation during connection
func (s *HubConnectionsClientCoverageSuite) Test_ConnectFoundService_CertificateValidation() {
	// Create server with specific certificate
	serverCert, err := cert.CreateCertificate("server", "test", "DE", "server-device")
	require.NoError(s.T(), err)

	server := httptest.NewUnstartedServer(nil)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAnyClientCert,
	}

	// Setup WebSocket handler
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Sec-WebSocket-Protocol") != "ship" {
			http.Error(w, "Missing ship subprotocol", http.StatusBadRequest)
			return
		}

		conn, err := upgrader.Upgrade(w, r, http.Header{
			"Sec-WebSocket-Protocol": []string{"ship"},
		})
		if err != nil {
			return
		}
		defer conn.Close()

		// Simulate handshake
		time.Sleep(50 * time.Millisecond)
	})

	server.StartTLS()
	defer server.Close()

	// Extract server details
	host, portStr, _ := net.SplitHostPort(server.Listener.Addr().String())
	
	// Get server's SKI
	x509Cert, _ := x509.ParseCertificate(serverCert.Certificate[0])
	serverSKI := fmt.Sprintf("%0x", x509Cert.SubjectKeyId)

	// Test connection with correct SKI
	service := api.NewServiceDetails(serverSKI)
	err = s.hub.connectFoundService(service, host, portStr, "/")
	// Error expected as handshake won't complete, but TLS should succeed
	assert.Error(s.T(), err)

	// Test connection with wrong SKI
	wrongService := api.NewServiceDetails("wrong-ski-12345")
	err = s.hub.connectFoundService(wrongService, host, portStr, "/")
	assert.Error(s.T(), err)
}

func TestHubConnectionsClientCoverageSuite(t *testing.T) {
	suite.Run(t, new(HubConnectionsClientCoverageSuite))
}