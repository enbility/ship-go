package hub

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// HubConnectionsDecomposedTestSuite tests the decomposed pure functions
type HubConnectionsDecomposedTestSuite struct {
	suite.Suite
	hub          *Hub
	mockMdns     *mocks.MdnsInterface
	mockReader   *mocks.HubReaderInterface
	localService *api.ServiceDetails
	localSKI     string
}

func (s *HubConnectionsDecomposedTestSuite) SetupTest() {
	s.localSKI = "test-local-ski"
	s.localService = api.NewServiceDetails(s.localSKI)

	cert, err := cert.CreateCertificate("test", "test", "DE", "test")
	require.NoError(s.T(), err)

	s.mockMdns = mocks.NewMdnsInterface(s.T())
	s.mockReader = mocks.NewHubReaderInterface(s.T())

	s.hub = NewHub(s.mockReader, s.mockMdns, 0, cert, s.localService)
}

func TestHubConnectionsDecomposedTestSuite(t *testing.T) {
	suite.Run(t, new(HubConnectionsDecomposedTestSuite))
}

// Test validateConnectionLimit function
func (s *HubConnectionsDecomposedTestSuite) Test_ValidateConnectionLimit() {
	tests := []struct {
		name               string
		currentConnections int
		maxConnections     int
		expectError        bool
		errorMessage       string
	}{
		{
			name:               "under_limit",
			currentConnections: 5,
			maxConnections:     10,
			expectError:        false,
		},
		{
			name:               "at_limit",
			currentConnections: 10,
			maxConnections:     10,
			expectError:        true,
			errorMessage:       "connection limit reached (10/10)",
		},
		{
			name:               "over_limit",
			currentConnections: 12,
			maxConnections:     10,
			expectError:        true,
			errorMessage:       "connection limit reached (12/10)",
		},
		{
			name:               "zero_connections",
			currentConnections: 0,
			maxConnections:     5,
			expectError:        false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			// Setup hub with specific limits
			s.hub.maxConnections = tt.maxConnections

			// Add dummy connections to reach the current count
			s.hub.connections = make(map[string]api.ShipConnectionInterface)
			for i := 0; i < tt.currentConnections; i++ {
				mockConn := mocks.NewShipConnectionInterface(s.T())
				s.hub.connections[fmt.Sprintf("ski-%d", i)] = mockConn
			}

			err := s.hub.validateConnectionLimit()

			if tt.expectError {
				assert.Error(s.T(), err)
				assert.Contains(s.T(), err.Error(), tt.errorMessage)
			} else {
				assert.NoError(s.T(), err)
			}
		})
	}
}

// Test createWebSocketDialer function
func (s *HubConnectionsDecomposedTestSuite) Test_CreateWebSocketDialer() {
	dialer := s.hub.createWebSocketDialer()

	assert.NotNil(s.T(), dialer)
	assert.Equal(s.T(), 5*time.Second, dialer.HandshakeTimeout)
	assert.NotNil(s.T(), dialer.TLSClientConfig)
	assert.True(s.T(), dialer.TLSClientConfig.InsecureSkipVerify)
	assert.Equal(s.T(), []string{"ship"}, dialer.Subprotocols)
	assert.Equal(s.T(), cert.CipherSuites, dialer.TLSClientConfig.CipherSuites)
	assert.Len(s.T(), dialer.TLSClientConfig.Certificates, 1)
}

// Helper function to create test certificate for validation tests
func createValidationTestCertificate(skiHex string) *x509.Certificate {
	// Generate a private key
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// Create proper 20-byte SKI from hex string or make it empty
	switch skiHex {
	case "":
		template.SubjectKeyId = nil
	case "746573742d736b69":
		// Create a proper 20-byte SHA1 hash for test-ski
		template.SubjectKeyId = []byte{0x74, 0x65, 0x73, 0x74, 0x2d, 0x73, 0x6b, 0x69, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	default:
		// For wrong-ski test - create a different 20-byte hash
		template.SubjectKeyId = []byte{0x77, 0x72, 0x6f, 0x6e, 0x67, 0x2d, 0x73, 0x6b, 0x69, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	}

	// Create certificate
	certDER, _ := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	cert, _ := x509.ParseCertificate(certDER)

	return cert
}

// Test validateRemoteCertificate function
func (s *HubConnectionsDecomposedTestSuite) Test_ValidateRemoteCertificate() {
	tests := []struct {
		name        string
		certs       []*x509.Certificate
		expectedSKI string
		expectValid bool
		errorMsg    string
	}{
		{
			name:        "empty_certificates",
			certs:       []*x509.Certificate{},
			expectedSKI: "testski",
			expectValid: false,
			errorMsg:    "no SKI in certificate",
		},
		{
			name:        "nil_subject_key_id",
			certs:       []*x509.Certificate{createValidationTestCertificate("")},
			expectedSKI: "testski",
			expectValid: false,
			errorMsg:    "no SKI in certificate",
		},
	}

	// Create a valid certificate test case using the cert package
	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "validtestski")
	validCert, _ := x509.ParseCertificate(certificate.Certificate[0])
	validSKI, _ := cert.SkiFromCertificate(validCert)

	tests = append(tests, struct {
		name        string
		certs       []*x509.Certificate
		expectedSKI string
		expectValid bool
		errorMsg    string
	}{
		name:        "valid_certificate_with_correct_ski",
		certs:       []*x509.Certificate{validCert},
		expectedSKI: validSKI,
		expectValid: true,
	})

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := validateRemoteCertificate(tt.certs, tt.expectedSKI)

			assert.Equal(s.T(), tt.expectValid, result.Valid)
			if tt.expectValid {
				assert.NoError(s.T(), result.Error)
				assert.Equal(s.T(), tt.expectedSKI, result.RemoteSKI)
			} else {
				assert.Error(s.T(), result.Error)
				assert.Contains(s.T(), result.Error.Error(), tt.errorMsg)
			}
		})
	}
}

// Test formatIPAddress function
func (s *HubConnectionsDecomposedTestSuite) Test_FormatIPAddress() {
	tests := []struct {
		name     string
		input    net.IP
		expected string
	}{
		{
			name:     "ipv4_address",
			input:    net.ParseIP("192.168.1.1"),
			expected: "192.168.1.1",
		},
		{
			name:     "ipv6_address",
			input:    net.ParseIP("2001:db8::1"),
			expected: "[2001:db8::1]",
		},
		{
			name:     "ipv6_loopback",
			input:    net.ParseIP("::1"),
			expected: "[::1]",
		},
		{
			name:     "ipv4_loopback",
			input:    net.ParseIP("127.0.0.1"),
			expected: "127.0.0.1",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := formatIPAddress(tt.input)
			assert.Equal(s.T(), tt.expected, result)
		})
	}
}

// Test sortIPAddresses function
func (s *HubConnectionsDecomposedTestSuite) Test_SortIPAddresses() {
	tests := []struct {
		name     string
		input    []net.IP
		expected []net.IP
	}{
		{
			name:     "empty_slice",
			input:    []net.IP{},
			expected: []net.IP{},
		},
		{
			name: "only_ipv4",
			input: []net.IP{
				net.ParseIP("192.168.1.2"),
				net.ParseIP("192.168.1.1"),
			},
			expected: []net.IP{
				net.ParseIP("192.168.1.2"),
				net.ParseIP("192.168.1.1"),
			},
		},
		{
			name: "only_ipv6",
			input: []net.IP{
				net.ParseIP("2001:db8::2"),
				net.ParseIP("2001:db8::1"),
			},
			expected: []net.IP{
				net.ParseIP("2001:db8::2"),
				net.ParseIP("2001:db8::1"),
			},
		},
		{
			name: "mixed_ipv4_first",
			input: []net.IP{
				net.ParseIP("192.168.1.1"),
				net.ParseIP("2001:db8::1"),
			},
			expected: []net.IP{
				net.ParseIP("192.168.1.1"),
				net.ParseIP("2001:db8::1"),
			},
		},
		{
			name: "mixed_ipv6_first",
			input: []net.IP{
				net.ParseIP("2001:db8::1"),
				net.ParseIP("192.168.1.1"),
				net.ParseIP("2001:db8::2"),
				net.ParseIP("192.168.1.2"),
			},
			expected: []net.IP{
				net.ParseIP("192.168.1.1"),
				net.ParseIP("192.168.1.2"),
				net.ParseIP("2001:db8::1"),
				net.ParseIP("2001:db8::2"),
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

// Test shouldAttemptConnection function (this tests the logic without real connections)
func (s *HubConnectionsDecomposedTestSuite) Test_ShouldAttemptConnection() {
	tests := []struct {
		name          string
		setupService  func() *api.ServiceDetails
		expectAttempt bool
	}{
		{
			name: "paired_service",
			setupService: func() *api.ServiceDetails {
				// Use ServiceForSKI which handles normalization and creation
				service := s.hub.ServiceForSKI("pairedski") // normalized version
				service.SetTrusted(true)
				return service
			},
			expectAttempt: true,
		},
		{
			name: "queued_service",
			setupService: func() *api.ServiceDetails {
				// Use ServiceForSKI which handles normalization and creation
				service := s.hub.ServiceForSKI("queuedski") // normalized version
				service.ConnectionStateDetail().SetState(api.ConnectionStateQueued)
				return service
			},
			expectAttempt: true,
		},
		{
			name: "unpaired_unqueued_service",
			setupService: func() *api.ServiceDetails {
				// Use ServiceForSKI which handles normalization and creation
				service := s.hub.ServiceForSKI("unpairedski") // normalized version
				service.SetTrusted(false)
				service.ConnectionStateDetail().SetState(api.ConnectionStateNone)
				return service
			},
			expectAttempt: false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			service := tt.setupService()
			result := s.hub.shouldAttemptConnection(service)
			assert.Equal(s.T(), tt.expectAttempt, result)
		})
	}
}
