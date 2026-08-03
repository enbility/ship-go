package cert

//nolint:gosec
import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
) // #nosec G505

func TestCertSuite(t *testing.T) {
	suite.Run(t, new(CertSuite))
}

type CertSuite struct {
	suite.Suite
}

func (c *CertSuite) Test_CreateCertificate() {
	cert, err := CreateCertificate("", "Org", "DE", "CN")
	assert.Nil(c.T(), err)
	assert.NotNil(c.T(), cert)
}

func (c *CertSuite) Test_SkiFromCertificate() {
	cert, err := CreateCertificate("", "Org", "DE", "CN")
	assert.Nil(c.T(), err)

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	assert.Nil(c.T(), err)

	ski, err := SkiFromCertificate(leaf)
	assert.Nil(c.T(), err)
	assert.NotEqual(c.T(), "", ski)

	cert, err = createInvalidCertificate("unit", "org", "DE", "CN")
	assert.Nil(c.T(), err)

	leaf, err = x509.ParseCertificate(cert.Certificate[0])
	assert.Nil(c.T(), err)

	ski, err = SkiFromCertificate(leaf)
	assert.NotNil(c.T(), err)
	assert.Equal(c.T(), "", ski)
}

func (c *CertSuite) Test_SkiFromCertificate_EmptySubject() {
	cert := &x509.Certificate{
		SubjectKeyId: []byte{1, 2, 3},
		Subject:      pkix.Name{},
	}

	ski, err := SkiFromCertificate(cert)
	assert.Error(c.T(), err)
	assert.Equal(c.T(), "", ski)
	assert.Contains(c.T(), err.Error(), "unknown")
}

func (c *CertSuite) Test_ValidateFingerprint_CalculateFingerprintError() {
	cert := &x509.Certificate{
		Raw: []byte{},
	}

	err := ValidateFingerprint(cert, "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF")
	assert.Error(c.T(), err)
}

// createInvalidCertificate creates a certificate with intentionally invalid SKI length (19 bytes instead of 20)
// This is used to test the SKI validation error path in SkiFromCertificate
func createInvalidCertificate(organizationalUnit, organization, country, commonName string) (tls.Certificate, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	// Create the EEBUS service SKI using the public key
	publicKey, err := privateKey.PublicKey.ECDH()
	if err != nil {
		return tls.Certificate{}, err
	}
	// SHIP 12.2: Required to be created according to RFC 3280 4.2.1.2
	// #nosec G401
	ski := sha1.Sum(publicKey.Bytes())

	subject := pkix.Name{
		OrganizationalUnit: []string{organizationalUnit},
		Organization:       []string{organization},
		Country:            []string{country},
		CommonName:         commonName,
	}

	// Create a random serial big int value
	maxValue := new(big.Int)
	maxValue.Exp(big.NewInt(2), big.NewInt(130), nil).Sub(maxValue, big.NewInt(1))
	serialNumber, err := rand.Int(rand.Reader, maxValue)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
		SerialNumber:          serialNumber,
		Subject:               subject,
		NotBefore:             time.Now(),                                // Valid starting now
		NotAfter:              time.Now().Add(time.Hour * 24 * 365 * 10), // Valid for 10 years
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          ski[:19],
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}

	tlsCertificate := tls.Certificate{
		Certificate:                  [][]byte{certBytes},
		PrivateKey:                   privateKey,
		SupportedSignatureAlgorithms: []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
	}

	return tlsCertificate, nil
}

// Test_SkiFromCertificate_NonECDSAKey tests error handling for non-ECDSA keys
func (c *CertSuite) Test_SkiFromCertificate_NonECDSAKey() {
	// Create a certificate with RSA key instead of ECDSA
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.Nil(c.T(), err)

	// Create certificate with RSA key
	template := x509.Certificate{
		SignatureAlgorithm:    x509.SHA256WithRSA,
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour * 24 * 365),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, // 20 bytes
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &rsaKey.PublicKey, rsaKey)
	assert.Nil(c.T(), err)

	cert, err := x509.ParseCertificate(certBytes)
	assert.Nil(c.T(), err)

	// This should fail because the key is not ECDSA
	ski, err := SkiFromCertificate(cert)
	assert.NotNil(c.T(), err)
	assert.Equal(c.T(), "", ski)
	assert.Contains(c.T(), err.Error(), "unsupported key type for SHIP: expected ECDSA")
}

// Test_SkiFromCertificate_SKIMismatch tests SKI validation errors
// TC_SHIP_SEC_001: DUT rejects a spoofed certificate whose SKI != SHA-1(public key) (no prior pairing).
// TC_SHIP_SEC_002: same SKI-mismatch detection applies regardless of the SKI being known from a prior pairing.
func (c *CertSuite) Test_SkiFromCertificate_SKIMismatch() {
	// Create a valid ECDSA key
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.Nil(c.T(), err)

	// Calculate correct SKI
	ecdhKey, err := privateKey.PublicKey.ECDH()
	assert.Nil(c.T(), err)
	correctSki := sha1.Sum(ecdhKey.Bytes()) // #nosec G401

	// Create certificate with WRONG SKI (correct length but wrong value)
	wrongSki := make([]byte, 20)
	copy(wrongSki, correctSki[:])
	wrongSki[0] = ^wrongSki[0] // Flip first byte to make it wrong

	template := x509.Certificate{
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-mismatch"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour * 24 * 365),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          wrongSki,
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	assert.Nil(c.T(), err)

	cert, err := x509.ParseCertificate(certBytes)
	assert.Nil(c.T(), err)

	// This should fail because SKI doesn't match calculated value
	ski, err := SkiFromCertificate(cert)
	assert.NotNil(c.T(), err)
	assert.Equal(c.T(), "", ski)
	assert.Contains(c.T(), err.Error(), "invalid SKI")
	assert.Contains(c.T(), err.Error(), "test-mismatch")
}

// Test_SkiFromCertificate_InvalidSKILength tests certificates with invalid SKI lengths
func (c *CertSuite) Test_SkiFromCertificate_InvalidSKILength() {
	testCases := []struct {
		name      string
		skiLength int
	}{
		{"too_short", 19},
		{"too_long", 21},
		{"empty", 0},
		{"way_too_short", 5},
		{"way_too_long", 50},
	}

	for _, tc := range testCases {
		c.T().Run(tc.name, func(t *testing.T) {
			// Create certificate with invalid SKI length
			invalidSki := make([]byte, tc.skiLength)
			for i := range invalidSki {
				invalidSki[i] = byte(i + 1)
			}

			cert := &x509.Certificate{
				SubjectKeyId: invalidSki,
				Subject:      pkix.Name{CommonName: "invalid-length"},
			}

			ski, err := SkiFromCertificate(cert)
			assert.Error(t, err)
			assert.Equal(t, "", ski)
			assert.Contains(t, err.Error(), "invalid SKI")
			assert.Contains(t, err.Error(), "invalid-length")
			assert.Contains(t, err.Error(), "expected: 20")
		})
	}
}

// Test_skiFromECDSAKey_Success tests the successful path
func (c *CertSuite) Test_skiFromECDSAKey_Success() {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.Nil(c.T(), err)

	ski, err := skiFromECDSAKey(&privateKey.PublicKey)
	assert.Nil(c.T(), err)
	assert.Len(c.T(), ski, 20) // SHA-1 produces 20 bytes

	// Verify SKI is deterministic
	ski2, err := skiFromECDSAKey(&privateKey.PublicKey)
	assert.Nil(c.T(), err)
	assert.Equal(c.T(), ski, ski2)
}

// Test_skiFromECDSAKey_SFailure tests the failure path for skiFromECDSAKey
func (c *CertSuite) Test_skiFromECDSAKey_SFailure() {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.Nil(c.T(), err)

	// Modify the public key to trigger failure
	ecdsaKey := &privateKey.PublicKey
	ecdsaKey.Curve = elliptic.P384() // Change curve to induce failure

	ski, err := skiFromECDSAKey(ecdsaKey)
	assert.NotNil(c.T(), err)
	assert.Equal(c.T(), "", ski)
	assert.Contains(c.T(), err.Error(), "failed to convert ECDSA key")
}

// Test_SkiFromCertificate_ErrorIntegration demonstrates comprehensive error scenarios
func (c *CertSuite) Test_SkiFromCertificate_ErrorIntegration() {
	// Test that all error paths return appropriate api.ErrInvalidSKI errors
	testCases := []struct {
		name        string
		cert        *x509.Certificate
		expectedErr string
	}{
		{
			name: "nil_certificate",
			cert: &x509.Certificate{
				SubjectKeyId: nil,
				Subject:      pkix.Name{CommonName: "nil-ski"},
			},
			expectedErr: "invalid SKI",
		},
		{
			name: "zero_length_ski",
			cert: &x509.Certificate{
				SubjectKeyId: []byte{},
				Subject:      pkix.Name{CommonName: "empty-ski"},
			},
			expectedErr: "invalid SKI",
		},
	}

	for _, tc := range testCases {
		c.T().Run(tc.name, func(t *testing.T) {
			ski, err := SkiFromCertificate(tc.cert)
			assert.Error(t, err)
			assert.Equal(t, "", ski)
			assert.Contains(t, err.Error(), tc.expectedErr)
			// Verify it's the expected error type
			assert.ErrorIs(t, err, api.ErrInvalidSKI)
		})
	}
}

// Test_CertificateCreationEdgeCases tests certificate creation with various inputs
func (c *CertSuite) Test_CertificateCreationEdgeCases() {
	// Test certificate creation with edge case inputs
	testCases := []struct {
		name    string
		ou      string
		org     string
		country string
		cn      string
	}{
		{"empty_fields", "", "", "", ""},
		{"unicode_content", "测试部门", "Test Org", "CN", "device-测试"},
		{"long_strings", string(make([]byte, 100)), string(make([]byte, 100)), "US", string(make([]byte, 50))},
		{"special_chars", "OU-123!@#", "Org & Co.", "DE", "device_model-serial#123"},
	}

	for _, tc := range testCases {
		c.T().Run(tc.name, func(t *testing.T) {
			// All these should succeed - CreateCertificate is robust
			cert, err := CreateCertificate(tc.ou, tc.org, tc.country, tc.cn)
			assert.Nil(t, err)
			assert.NotEqual(t, tls.Certificate{}, cert)

			// Verify certificate is valid
			leaf, err := x509.ParseCertificate(cert.Certificate[0])
			assert.Nil(t, err)
			assert.Equal(t, tc.cn, leaf.Subject.CommonName)

			// Verify SKI extraction works
			ski, err := SkiFromCertificate(leaf)
			assert.Nil(t, err)
			assert.Len(t, ski, 40) // 20 bytes = 40 hex chars
		})
	}
}
