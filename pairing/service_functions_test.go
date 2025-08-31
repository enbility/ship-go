package pairing

import (
	"crypto/x509"
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestServiceFunctionsSuite(t *testing.T) {
	suite.Run(t, new(ServiceFunctionsSuite))
}

type ServiceFunctionsSuite struct {
	suite.Suite
	service *Service
}

func (s *ServiceFunctionsSuite) SetupTest() {
	certificate, err := cert.CreateCertificate("unit", "org", "DE", "CN")
	s.Require().NoError(err)

	// Create service using constructor
	s.service, err = NewService(
		nil, // mdns - not needed for these tests
		nil, // crypto - not needed for these tests
		nil, // history - not needed for these tests
		nil, // hub - not needed for these tests
		certificate,
	)
	s.Require().NoError(err)
}

func (s *ServiceFunctionsSuite) TestGetLocalFingerprint() {
	fingerprint, err := s.service.GetLocalFingerprint()
	assert.NoError(s.T(), err)
	assert.NotEmpty(s.T(), fingerprint)
	assert.Len(s.T(), fingerprint, 64) // SHA-256 hex string length
}

func (s *ServiceFunctionsSuite) TestGetLocalFingerprint_NilCert() {
	s.service.localCert = nil

	fingerprint, err := s.service.GetLocalFingerprint()
	assert.Error(s.T(), err)
	assert.Equal(s.T(), api.ErrInvalidCertificate, err)
	assert.Empty(s.T(), fingerprint)
}

func (s *ServiceFunctionsSuite) TestValidateRemoteFingerprint() {
	// Create a test remote certificate
	remoteCert, err := cert.CreateCertificate("remote-unit", "remote-org", "US", "remote-cn")
	s.Require().NoError(err)

	parsedRemoteCert, err := x509.ParseCertificate(remoteCert.Certificate[0])
	s.Require().NoError(err)

	// Calculate expected fingerprint
	expectedFingerprint, err := cert.FingerprintFromCertificate(parsedRemoteCert)
	s.Require().NoError(err)

	// Test valid fingerprint
	err = s.service.ValidateRemoteFingerprint(parsedRemoteCert, expectedFingerprint)
	assert.NoError(s.T(), err)

	// Test invalid fingerprint
	err = s.service.ValidateRemoteFingerprint(parsedRemoteCert, "INVALID_FINGERPRINT")
	assert.Error(s.T(), err)

	// Test nil certificate
	err = s.service.ValidateRemoteFingerprint(nil, expectedFingerprint)
	assert.Error(s.T(), err)

	// Test empty fingerprint
	err = s.service.ValidateRemoteFingerprint(parsedRemoteCert, "")
	assert.Error(s.T(), err)
}
