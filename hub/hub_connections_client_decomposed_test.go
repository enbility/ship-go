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
	"strings"
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
	s.localSKI = "testlocalski"
	s.localService = api.NewServiceDetails(s.localSKI, "", "")

	cert, err := cert.CreateCertificate("test", "test", "DE", "test")
	require.NoError(s.T(), err)

	s.mockMdns = mocks.NewMdnsInterface(s.T())
	s.mockReader = mocks.NewHubReaderInterface(s.T())

	s.hub, err = newTestHub(s.mockReader, s.mockMdns, 0, cert, s.localService, nil)
	assert.NoError(s.T(), err)
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

// Test validateRemoteCertificate function - comprehensive coverage
func (s *HubConnectionsDecomposedTestSuite) Test_ValidateRemoteCertificate() {
	// Create valid certificates for testing
	validCertificate, err := cert.CreateCertificate("unit", "org", "DE", "validtestski")
	require.NoError(s.T(), err)
	validCert, err := x509.ParseCertificate(validCertificate.Certificate[0])
	require.NoError(s.T(), err)
	validSKI, err := cert.SkiFromCertificate(validCert)
	require.NoError(s.T(), err)
	validFingerprint, err := cert.FingerprintFromCertificate(validCert)
	require.NoError(s.T(), err)

	// Create another valid certificate for mismatch tests
	otherCertificate, err := cert.CreateCertificate("other", "org", "DE", "othertestski")
	require.NoError(s.T(), err)
	otherCert, err := x509.ParseCertificate(otherCertificate.Certificate[0])
	require.NoError(s.T(), err)
	otherSKI, err := cert.SkiFromCertificate(otherCert)
	require.NoError(s.T(), err)
	otherFingerprint, err := cert.FingerprintFromCertificate(otherCert)
	require.NoError(s.T(), err)

	tests := []struct {
		name                string
		certs               []*x509.Certificate
		expectedSKI         string
		expectedFingerprint string
		expectValid         bool
		errorMsg            string
		expectedReturnSKI   string
		expectedReturnFP    string
	}{
		// Nil/Empty certificate chain tests (3 cases)
		{
			name:                "empty_certificates",
			certs:               []*x509.Certificate{},
			expectedSKI:         "testski",
			expectedFingerprint: "",
			expectValid:         false,
			errorMsg:            "no SKI in certificate",
		},
		{
			name:                "nil_certificates",
			certs:               nil,
			expectedSKI:         "testski",
			expectedFingerprint: "",
			expectValid:         false,
			errorMsg:            "no SKI in certificate",
		},
		{
			name:                "nil_subject_key_id",
			certs:               []*x509.Certificate{createValidationTestCertificate("")},
			expectedSKI:         "testski",
			expectedFingerprint: "",
			expectValid:         false,
			errorMsg:            "no SKI in certificate",
		},

		// Valid certificate scenarios (4 cases)
		{
			name:                "valid_certificate_no_expectations",
			certs:               []*x509.Certificate{validCert},
			expectedSKI:         "",
			expectedFingerprint: "",
			expectValid:         true,
			expectedReturnSKI:   validSKI,
			expectedReturnFP:    validFingerprint,
		},
		{
			name:                "valid_certificate_with_correct_ski",
			certs:               []*x509.Certificate{validCert},
			expectedSKI:         validSKI,
			expectedFingerprint: "",
			expectValid:         true,
			expectedReturnSKI:   validSKI,
			expectedReturnFP:    validFingerprint,
		},
		{
			name:                "valid_certificate_with_correct_fingerprint",
			certs:               []*x509.Certificate{validCert},
			expectedSKI:         "",
			expectedFingerprint: validFingerprint,
			expectValid:         true,
			expectedReturnSKI:   validSKI,
			expectedReturnFP:    validFingerprint,
		},
		{
			name:                "valid_certificate_with_both_ski_and_fingerprint",
			certs:               []*x509.Certificate{validCert},
			expectedSKI:         validSKI,
			expectedFingerprint: validFingerprint,
			expectValid:         true,
			expectedReturnSKI:   validSKI,
			expectedReturnFP:    validFingerprint,
		},

		// SKI mismatch tests (2 cases)
		{
			name:                "ski_mismatch",
			certs:               []*x509.Certificate{validCert},
			expectedSKI:         "wrongski",
			expectedFingerprint: "",
			expectValid:         false,
			errorMsg:            "SKI mismatch",
		},
		{
			name:                "ski_mismatch_with_correct_fingerprint",
			certs:               []*x509.Certificate{validCert},
			expectedSKI:         otherSKI,
			expectedFingerprint: validFingerprint,
			expectValid:         false,
			errorMsg:            "SKI mismatch",
		},

		// Fingerprint mismatch tests (2 cases)
		{
			name:                "fingerprint_mismatch",
			certs:               []*x509.Certificate{validCert},
			expectedSKI:         "",
			expectedFingerprint: "wrong-fingerprint",
			expectValid:         false,
			errorMsg:            "fingerprint mismatch",
		},
		{
			name:                "fingerprint_mismatch_with_correct_ski",
			certs:               []*x509.Certificate{validCert},
			expectedSKI:         validSKI,
			expectedFingerprint: otherFingerprint,
			expectValid:         false,
			errorMsg:            "fingerprint mismatch",
		},

		// Multiple certificates - only first is checked (1 case)
		{
			name:                "multiple_certificates_first_valid",
			certs:               []*x509.Certificate{validCert, otherCert},
			expectedSKI:         validSKI,
			expectedFingerprint: validFingerprint,
			expectValid:         true,
			expectedReturnSKI:   validSKI,
			expectedReturnFP:    validFingerprint,
		},

		// Edge case - empty expectations match anything (1 case)
		{
			name:                "empty_expectations_always_match",
			certs:               []*x509.Certificate{otherCert},
			expectedSKI:         "",
			expectedFingerprint: "",
			expectValid:         true,
			expectedReturnSKI:   otherSKI,
			expectedReturnFP:    otherFingerprint,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := validateRemoteCertificate(tt.certs, tt.expectedSKI, tt.expectedFingerprint)

			assert.Equal(s.T(), tt.expectValid, result.Valid, "Valid field mismatch")

			if tt.expectValid {
				assert.NoError(s.T(), result.Error, "Should not have error for valid result")
				assert.Equal(s.T(), tt.expectedReturnSKI, result.RemoteSKI, "Returned SKI mismatch")
				assert.Equal(s.T(), tt.expectedReturnFP, result.RemoteFingerprint, "Returned fingerprint mismatch")
			} else {
				assert.Error(s.T(), result.Error, "Should have error for invalid result")
				assert.Contains(s.T(), result.Error.Error(), tt.errorMsg, "Error message mismatch")
				assert.Empty(s.T(), result.RemoteSKI, "SKI should be empty on error")
				assert.Empty(s.T(), result.RemoteFingerprint, "Fingerprint should be empty on error")
			}
		})
	}
}

// Test validateRemoteCertificate - additional edge cases and error conditions
func (s *HubConnectionsDecomposedTestSuite) Test_ValidateRemoteCertificate_ErrorConditions() {
	// Create malformed certificate scenarios that would cause parsing errors
	tests := []struct {
		name        string
		setupCert   func() *x509.Certificate
		expectedSKI string
		expectValid bool
		errorMsg    string
	}{
		{
			name: "certificate_with_malformed_ski_bytes",
			setupCert: func() *x509.Certificate {
				// Create certificate with invalid SKI format (too short)
				cert := createValidationTestCertificate("valid")
				// Manually set an invalid SKI that would cause cert.SkiFromCertificate to fail
				cert.SubjectKeyId = []byte{0x01, 0x02} // Too short for proper SKI
				return cert
			},
			expectedSKI: "testski",
			expectValid: false,
			errorMsg:    "invalid SKI format",
		},
		{
			name: "certificate_with_valid_but_wrong_ski_format_fails",
			setupCert: func() *x509.Certificate {
				// The createValidationTestCertificate with "valid" parameter creates a certificate
				// with a malformed SKI that causes cert.SkiFromCertificate to fail
				return createValidationTestCertificate("valid")
			},
			expectedSKI: "",
			expectValid: false, // This actually fails due to SKI format validation
			errorMsg:    "invalid SKI format",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			cert := tt.setupCert()
			result := validateRemoteCertificate([]*x509.Certificate{cert}, tt.expectedSKI, "")

			assert.Equal(s.T(), tt.expectValid, result.Valid, "Valid field mismatch")
			if !tt.expectValid && tt.errorMsg != "" {
				assert.Error(s.T(), result.Error, "Should have error for invalid result")
				assert.Contains(s.T(), result.Error.Error(), tt.errorMsg, "Error message mismatch")
			} else if tt.expectValid {
				assert.NoError(s.T(), result.Error, "Should not have error for valid result")
			}
		})
	}
}

// Test validateRemoteCertificate - comprehensive boundary conditions
func (s *HubConnectionsDecomposedTestSuite) Test_ValidateRemoteCertificate_BoundaryConditions() {
	// Create valid certificate for boundary testing
	validCertificate, err := cert.CreateCertificate("boundary", "test", "DE", "boundaryski")
	require.NoError(s.T(), err)
	validCert, err := x509.ParseCertificate(validCertificate.Certificate[0])
	require.NoError(s.T(), err)
	validSKI, err := cert.SkiFromCertificate(validCert)
	require.NoError(s.T(), err)
	validFingerprint, err := cert.FingerprintFromCertificate(validCert)
	require.NoError(s.T(), err)

	tests := []struct {
		name                string
		certs               []*x509.Certificate
		expectedSKI         string
		expectedFingerprint string
		expectValid         bool
		description         string
	}{
		{
			name:                "case_sensitive_ski_mismatch",
			certs:               []*x509.Certificate{validCert},
			expectedSKI:         strings.ToUpper(validSKI), // Different case
			expectedFingerprint: "",
			expectValid:         false,
			description:         "SKI comparison should be case sensitive",
		},
		{
			name:                "case_sensitive_fingerprint_mismatch",
			certs:               []*x509.Certificate{validCert},
			expectedSKI:         "",
			expectedFingerprint: strings.ToLower(validFingerprint), // Different case (lowercase when expecting uppercase)
			expectValid:         false,
			description:         "Fingerprint comparison should be case sensitive",
		},
		{
			name:                "whitespace_in_ski",
			certs:               []*x509.Certificate{validCert},
			expectedSKI:         " " + validSKI + " ", // With whitespace
			expectedFingerprint: "",
			expectValid:         false,
			description:         "Whitespace should cause mismatch",
		},
		{
			name:                "whitespace_in_fingerprint",
			certs:               []*x509.Certificate{validCert},
			expectedSKI:         "",
			expectedFingerprint: " " + validFingerprint + " ", // With whitespace
			expectValid:         false,
			description:         "Whitespace should cause mismatch",
		},
		{
			name:                "exact_match_required",
			certs:               []*x509.Certificate{validCert},
			expectedSKI:         validSKI[:len(validSKI)-1], // Truncated
			expectedFingerprint: "",
			expectValid:         false,
			description:         "Partial matches should fail",
		},
		{
			name:                "multiple_certs_only_first_checked",
			certs:               []*x509.Certificate{validCert, createValidationTestCertificate("other")},
			expectedSKI:         validSKI,
			expectedFingerprint: validFingerprint,
			expectValid:         true,
			description:         "Only first certificate should be validated",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := validateRemoteCertificate(tt.certs, tt.expectedSKI, tt.expectedFingerprint)
			assert.Equal(s.T(), tt.expectValid, result.Valid, tt.description)
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
				// Use ServiceForIdentifier which handles normalization and creation
				service := api.NewServiceDetails("pairedski", "", "")
				service.SetTrusted(true)
				success := s.hub.addService(service)
				assert.True(s.T(), success)
				return service
			},
			expectAttempt: true,
		},
		{
			name: "unpaired_unqueued_service",
			setupService: func() *api.ServiceDetails {
				// Use ServiceForIdentifier which handles normalization and creation
				service := api.NewServiceDetails("unpairedski", "", "")
				service.SetTrusted(false)
				service.ConnectionStateDetail().SetState(api.ConnectionStateNone)
				success := s.hub.addService(service)
				assert.True(s.T(), success)
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
