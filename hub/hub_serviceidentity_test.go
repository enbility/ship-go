package hub

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/enbility/ship-go/api"
)

func TestHubServiceIdentity(t *testing.T) {
	suite.Run(t, new(HubServiceIdentitySuite))
}

type HubServiceIdentitySuite struct {
	HubSuite // Reuse existing test setup
}

// Test ServiceFor method works the same as ServiceForIdentifier
func (s *HubServiceIdentitySuite) TestServiceFor_EquivalentToServiceForIdentifier() {
	// Create a service and add it to the hub
	service := api.NewServiceDetails("testski", "test-fingerprint", "test-shipid")
	s.sut.addService(service)

	// Create equivalent ServiceIdentity
	identity := api.NewServiceIdentity("testski", "test-fingerprint", "test-shipid")
	assert.NotNil(s.T(), identity)

	// Both methods should return the same result
	result1 := s.sut.ServiceForIdentifier("testski", "test-fingerprint")
	result2 := s.sut.ServiceFor(identity)

	assert.Equal(s.T(), result1, result2)
	assert.NotNil(s.T(), result1)
	assert.Equal(s.T(), "testski", result1.SKI()) // Should be normalized
}

// Test ServiceFor with missing service
func (s *HubServiceIdentitySuite) TestServiceFor_MissingService() {
	identity := api.NewServiceIdentity("missingski", "missing-fingerprint", "missing-shipid")
	assert.NotNil(s.T(), identity)

	result := s.sut.ServiceFor(identity)
	assert.Nil(s.T(), result)
}

// Test PairingDetailFor method works the same as PairingDetailForIdentifier
func (s *HubServiceIdentitySuite) TestPairingDetailFor_EquivalentToPairingDetailForIdentifier() {
	// Create a service and add it to the hub
	service := api.NewServiceDetails("pairingski", "pairing-fingerprint", "pairing-shipid")
	s.sut.addService(service)

	// Create equivalent ServiceIdentity
	identity := api.NewServiceIdentity("pairingski", "pairing-fingerprint", "pairing-shipid")
	assert.NotNil(s.T(), identity)

	// Both methods should return the same result
	result1 := s.sut.PairingDetailFor(api.NewServiceIdentity("pairingski", "pairing-fingerprint", "pairing-shipid"))
	result2 := s.sut.PairingDetailFor(identity)

	assert.Equal(s.T(), result1, result2)
	assert.NotNil(s.T(), result1) // Should have connection state detail
}

// Test RegisterRemoteServiceIdentity equivalence
func (s *HubServiceIdentitySuite) TestRegisterRemoteServiceIdentity_EquivalentToRegisterRemoteService() {
	identity := api.NewServiceIdentity("registerski", "register-fingerprint", "register-shipid")
	assert.NotNil(s.T(), identity)

	// Use the new ServiceIdentity method
	s.sut.RegisterRemoteService(identity)

	// Verify the service was added correctly
	service := s.sut.ServiceFor(identity)
	assert.NotNil(s.T(), service)
	assert.Equal(s.T(), "registerski", service.SKI()) // Should be normalized
	assert.Equal(s.T(), "register-fingerprint", service.Fingerprint())
	assert.Equal(s.T(), "register-shipid", service.ShipID())
	assert.True(s.T(), service.Trusted()) // Should be trusted after registration
}

// Test UnregisterRemoteServiceIdentity equivalence
func (s *HubServiceIdentitySuite) TestUnregisterRemoteServiceIdentity_EquivalentToUnregisterRemoteService() {
	identity := api.NewServiceIdentity("unregisterski", "unregister-fingerprint", "unregister-shipid")
	assert.NotNil(s.T(), identity)

	// First register the service
	s.sut.RegisterRemoteService(identity)
	service := s.sut.ServiceFor(identity)
	assert.NotNil(s.T(), service)
	assert.True(s.T(), service.Trusted())

	// Then unregister it
	s.sut.UnregisterRemoteService(identity)

	// Service should no longer be trusted (but still exist)
	service = s.sut.ServiceFor(identity)
	if service != nil {
		assert.False(s.T(), service.Trusted()) // Should no longer be trusted
	}
}

// Test CancelPairingWithServiceIdentity equivalence
func (s *HubServiceIdentitySuite) TestCancelPairingWithServiceIdentity_EquivalentToCancelPairingWithSKI() {
	identity := api.NewServiceIdentity("cancelski", "cancel-fingerprint", "cancel-shipid")
	assert.NotNil(s.T(), identity)

	// First register the service
	s.sut.RegisterRemoteService(identity)

	// Cancel pairing using ServiceIdentity method
	s.sut.CancelPairing(identity)

	// Service should no longer be trusted
	service := s.sut.ServiceFor(identity)
	if service != nil {
		assert.False(s.T(), service.Trusted())
	}
}

// Test conversion between ServiceIdentity and ServiceDetails
func (s *HubServiceIdentitySuite) TestServiceIdentity_ServiceDetailsConversion() {
	// Create ServiceIdentity
	identity := api.NewServiceIdentity("convertski", "convert-fingerprint", "convert-shipid")
	assert.NotNil(s.T(), identity)

	// Register using ServiceIdentity
	s.sut.RegisterRemoteService(identity)

	// Retrieve using ServiceDetails-based method
	service1 := s.sut.ServiceForIdentifier(identity.SKI, identity.Fingerprint)
	assert.NotNil(s.T(), service1)

	// Retrieve using ServiceIdentity-based method
	service2 := s.sut.ServiceFor(identity)
	assert.NotNil(s.T(), service2)

	// Should be the same service
	assert.Equal(s.T(), service1, service2)

	// Values should match the original identity
	assert.Equal(s.T(), "convertski", service1.SKI()) // Normalized
	assert.Equal(s.T(), identity.Fingerprint, service1.Fingerprint())
	assert.Equal(s.T(), identity.ShipID, service1.ShipID())
}

// Test ServiceIdentity validation in Hub methods
func (s *HubServiceIdentitySuite) TestHub_ServiceIdentityValidation() {
	// Test with valid identity
	validIdentity := api.NewServiceIdentity("validski", "valid-fingerprint", "valid-shipid")
	assert.NotNil(s.T(), validIdentity)

	// Should work without panicking
	s.sut.RegisterRemoteService(validIdentity)
	result := s.sut.ServiceFor(validIdentity)
	assert.NotNil(s.T(), result)

	// Test with minimal identity (SKI only)
	minimalIdentity := api.NewServiceIdentity("minimalski", "", "")
	assert.NotNil(s.T(), minimalIdentity)

	// Should also work
	s.sut.RegisterRemoteService(minimalIdentity)
	result = s.sut.ServiceFor(minimalIdentity)
	assert.NotNil(s.T(), result)
}

// Test DisconnectServiceIdentity (this method won't have visible effects without active connections)
func (s *HubServiceIdentitySuite) TestDisconnectServiceIdentity_NoActiveConnection() {
	identity := api.NewServiceIdentity("disconnectski", "disconnect-fingerprint", "disconnect-shipid")
	assert.NotNil(s.T(), identity)

	// Register the service first
	s.sut.RegisterRemoteService(identity)

	// Disconnect should not panic even without active connection
	s.sut.DisconnectService(identity, "test disconnect")

	// Service should still exist (disconnect doesn't remove registration)
	service := s.sut.ServiceFor(identity)
	assert.NotNil(s.T(), service)
}
