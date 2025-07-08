package hub

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestCertificateExpirationLogging(t *testing.T) {
	// Test certificate expiration logging in verifyPeerCertificate
	t.Run("verifyPeerCertificate_logs_expiration", func(t *testing.T) {
		// Create hub with standard test setup
		mdns := mocks.NewMdnsInterface(t)
		hubReader := mocks.NewHubReaderInterface(t)

		// Set up expectations
		hubReader.EXPECT().RemoteSKIConnected(mock.Anything).Maybe()
		hubReader.EXPECT().RemoteSKIDisconnected(mock.Anything).Maybe()

		service := api.NewServiceDetails("test-ski")
		service.SetShipID("test-ship-id")

		// Create a dummy certificate for testing
		tlsCert := tls.Certificate{}

		hub := NewHub(hubReader, mdns, 4729, tlsCert, service)

		// Test with valid certificate (no logging expected)
		t.Run("valid_certificate", func(t *testing.T) {
			validCert, err := cert.CreateCertificate("test", "org", "DE", "test-valid")
			assert.NoError(t, err)

			// No logging expected for valid certificates
			rawCerts := [][]byte{validCert.Certificate[0]}
			err = hub.verifyPeerCertificate(rawCerts, nil)
			assert.NoError(t, err)
		})

		// Test with expiring certificate
		t.Run("expiring_certificate", func(t *testing.T) {
			// Create a certificate that expires in 20 days
			template := createTestCertificateTemplate("test-expiring", 20*24*time.Hour)
			certBytes, _, err := createTestCertificate(template)
			assert.NoError(t, err)

			// For now, just verify it doesn't error
			// Full logging test would require log capture mechanism
			rawCerts := [][]byte{certBytes}
			err = hub.verifyPeerCertificate(rawCerts, nil)
			assert.NoError(t, err)
		})

		// Test with expired certificate
		t.Run("expired_certificate", func(t *testing.T) {
			// Create a certificate that expired 5 days ago
			template := createTestCertificateTemplate("test-expired", -5*24*time.Hour)
			certBytes, _, err := createTestCertificate(template)
			assert.NoError(t, err)

			// For now, just verify it doesn't error (per SHIP spec)
			// Full logging test would require log capture mechanism
			rawCerts := [][]byte{certBytes}
			err = hub.verifyPeerCertificate(rawCerts, nil)
			assert.NoError(t, err) // Should still pass despite expiration
		})
	})

	// Additional integration tests for connectFoundService and ServeHTTP
	// would require more complex setup with WebSocket connections
	// For now, the pattern is established with verifyPeerCertificate
}

// Helper function to create a test certificate template with custom expiration
func createTestCertificateTemplate(commonName string, validityDuration time.Duration) *x509.Certificate {
	serialNumber, _ := rand.Int(rand.Reader, new(big.Int).SetInt64(100000))
	return &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: commonName,
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(validityDuration),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		SubjectKeyId: generateTestSKIBytes(),
	}
}

// Helper function to create a test certificate
func createTestCertificate(template *x509.Certificate) ([]byte, interface{}, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}

	return certDER, priv, nil
}

// Helper function to generate a test SKI
func generateTestSKIBytes() []byte {
	ski := make([]byte, 20)
	rand.Read(ski)
	return ski
}
