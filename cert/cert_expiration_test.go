package cert

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestCertExpirationSuite(t *testing.T) {
	suite.Run(t, new(CertExpirationSuite))
}

type CertExpirationSuite struct {
	suite.Suite
}

func (s *CertExpirationSuite) Test_CheckCertificateExpiration_Valid() {
	// Create a certificate that expires in 100 days
	cert := &x509.Certificate{
		NotAfter: time.Now().Add(100 * 24 * time.Hour),
	}

	expirationStatus := CheckCertificateExpiration(cert)

	assert.Equal(s.T(), CertificateValid, expirationStatus.Status)
	assert.False(s.T(), expirationStatus.IsExpired)
	assert.False(s.T(), expirationStatus.ExpiresWithin30Days)
	assert.Equal(s.T(), 100, expirationStatus.DaysUntilExpiration)
}

func (s *CertExpirationSuite) Test_CheckCertificateExpiration_ExpiringSoon() {
	// Create a certificate that expires in 15 days
	cert := &x509.Certificate{
		NotAfter: time.Now().Add(15 * 24 * time.Hour),
	}

	expirationStatus := CheckCertificateExpiration(cert)

	assert.Equal(s.T(), CertificateExpiringSoon, expirationStatus.Status)
	assert.False(s.T(), expirationStatus.IsExpired)
	assert.True(s.T(), expirationStatus.ExpiresWithin30Days)
	assert.Equal(s.T(), 15, expirationStatus.DaysUntilExpiration)
}

func (s *CertExpirationSuite) Test_CheckCertificateExpiration_ExpiredYesterday() {
	// Create a certificate that expired yesterday
	cert := &x509.Certificate{
		NotAfter: time.Now().Add(-1 * 24 * time.Hour),
	}

	expirationStatus := CheckCertificateExpiration(cert)

	assert.Equal(s.T(), CertificateExpired, expirationStatus.Status)
	assert.True(s.T(), expirationStatus.IsExpired)
	assert.False(s.T(), expirationStatus.ExpiresWithin30Days)
	assert.Equal(s.T(), -1, expirationStatus.DaysUntilExpiration)
}

func (s *CertExpirationSuite) Test_CheckCertificateExpiration_ExpiredLongAgo() {
	// Create a certificate that expired 365 days ago
	cert := &x509.Certificate{
		NotAfter: time.Now().Add(-365 * 24 * time.Hour),
	}

	expirationStatus := CheckCertificateExpiration(cert)

	assert.Equal(s.T(), CertificateExpired, expirationStatus.Status)
	assert.True(s.T(), expirationStatus.IsExpired)
	assert.False(s.T(), expirationStatus.ExpiresWithin30Days)
	assert.Equal(s.T(), -365, expirationStatus.DaysUntilExpiration)
}

func (s *CertExpirationSuite) Test_CheckCertificateExpiration_ExactlyToday() {
	// Create a certificate that expires today (within the same second)
	now := time.Now()
	cert := &x509.Certificate{
		NotAfter: now,
	}

	expirationStatus := CheckCertificateExpiration(cert)

	assert.Equal(s.T(), CertificateExpired, expirationStatus.Status)
	assert.True(s.T(), expirationStatus.IsExpired)
	assert.False(s.T(), expirationStatus.ExpiresWithin30Days)
	assert.Equal(s.T(), 0, expirationStatus.DaysUntilExpiration)
}

func (s *CertExpirationSuite) Test_CheckCertificateExpiration_Exactly30Days() {
	// Create a certificate that expires in exactly 30 days
	cert := &x509.Certificate{
		NotAfter: time.Now().Add(30 * 24 * time.Hour),
	}

	expirationStatus := CheckCertificateExpiration(cert)

	assert.Equal(s.T(), CertificateExpiringSoon, expirationStatus.Status)
	assert.False(s.T(), expirationStatus.IsExpired)
	assert.True(s.T(), expirationStatus.ExpiresWithin30Days)
	assert.Equal(s.T(), 30, expirationStatus.DaysUntilExpiration)
}

func (s *CertExpirationSuite) Test_CheckCertificateExpiration_31Days() {
	// Create a certificate that expires in 31 days (just outside the warning window)
	cert := &x509.Certificate{
		NotAfter: time.Now().Add(31 * 24 * time.Hour),
	}

	expirationStatus := CheckCertificateExpiration(cert)

	assert.Equal(s.T(), CertificateValid, expirationStatus.Status)
	assert.False(s.T(), expirationStatus.IsExpired)
	assert.False(s.T(), expirationStatus.ExpiresWithin30Days)
	assert.Equal(s.T(), 31, expirationStatus.DaysUntilExpiration)
}

func (s *CertExpirationSuite) Test_LogCertificateExpiration_Valid() {
	// This test verifies that LogCertificateExpiration correctly logs for valid certificates
	// Since we can't easily test log output, we just ensure the function doesn't panic
	cert := &x509.Certificate{
		Subject: pkix.Name{
			CommonName: "test-device",
		},
		NotAfter: time.Now().Add(100 * 24 * time.Hour),
	}

	// Should not panic
	LogCertificateExpiration(cert, "test-ski-12345")
}

func (s *CertExpirationSuite) Test_LogCertificateExpiration_ExpiringSoon() {
	// This test verifies that LogCertificateExpiration correctly logs for expiring certificates
	cert := &x509.Certificate{
		Subject: pkix.Name{
			CommonName: "test-device",
		},
		NotAfter: time.Now().Add(15 * 24 * time.Hour),
	}

	// Should not panic
	LogCertificateExpiration(cert, "test-ski-12345")
}

func (s *CertExpirationSuite) Test_LogCertificateExpiration_Expired() {
	// This test verifies that LogCertificateExpiration correctly logs for expired certificates
	cert := &x509.Certificate{
		Subject: pkix.Name{
			CommonName: "test-device",
		},
		NotAfter: time.Now().Add(-10 * 24 * time.Hour),
	}

	// Should not panic
	LogCertificateExpiration(cert, "test-ski-12345")
}
