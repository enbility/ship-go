package hub

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/logging"
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
		// Use specific type matchers to avoid race conditions
		hubReader.EXPECT().RemoteSKIConnected(mock.AnythingOfType("api.ShipConnectionInterface")).Maybe()
		hubReader.EXPECT().RemoteSKIDisconnected(mock.AnythingOfType("string")).Maybe()

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
		t.Run("certificate_expiration_logging_direct", func(t *testing.T) {
			logger := NewTestLogger()
			logging.SetLogging(logger)
			defer logging.SetLogging(nil)

			certificate := createDirectTestCertificate(time.Now().Add(10*24*time.Hour), "test-device")
			ski := fmt.Sprintf("%0x", certificate.SubjectKeyId)

			cert.LogCertificateExpiration(certificate, ski)

			output := logger.GetOutput()
			assert.Contains(t, output, "expires in 10 days")
		})
	})
}

func createDirectTestCertificate(expiryTime time.Time, commonName string) *x509.Certificate {
	privateKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	ski := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A,
		0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10, 0x11, 0x12, 0x13, 0x14}

	if commonName != "" {
		hash := []byte(commonName)
		for i := 0; i < len(hash) && i < 4; i++ {
			ski[19-i] = hash[i]
		}
	}

	template := x509.Certificate{
		SignatureAlgorithm: x509.ECDSAWithSHA256,
		SerialNumber:       big.NewInt(1),
		Subject: pkix.Name{
			OrganizationalUnit: []string{"test"},
			Organization:       []string{"test"},
			Country:            []string{"DE"},
			CommonName:         commonName,
		},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              expiryTime,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          ski,
	}

	certBytes, _ := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	certificate, _ := x509.ParseCertificate(certBytes)

	return certificate
}
