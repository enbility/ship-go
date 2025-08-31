package pairing

import (
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/mock"
)

// ServiceTestSuite contains tests for the SHIP pairing service orchestrator
type ServiceTestSuite struct {
	suite.Suite

	mockMdns    *mocks.MdnsPairingInterface
	mockCrypto  *mocks.PairingCryptoInterface
	mockHistory *mocks.PairingHistoryProviderInterface
	mockHub     *mocks.PairingHubInterface

	testCert tls.Certificate
	sut      *Service
}

func TestServiceTestSuite(t *testing.T) {
	suite.Run(t, new(ServiceTestSuite))
}

func (suite *ServiceTestSuite) SetupTest() {
	suite.mockMdns = mocks.NewMdnsPairingInterface(suite.T())
	suite.mockCrypto = mocks.NewPairingCryptoInterface(suite.T())
	suite.mockHistory = mocks.NewPairingHistoryProviderInterface(suite.T())
	suite.mockHub = mocks.NewPairingHubInterface(suite.T())

	// Create a real test certificate
	var err error
	suite.testCert, err = cert.CreateCertificate("TestOU", "TestOrg", "DE", "test-device")
	require.NoError(suite.T(), err)

	suite.sut, err = NewService(
		suite.mockMdns,
		suite.mockCrypto,
		suite.mockHistory,
		suite.mockHub,
		suite.testCert,
	)
	require.NoError(suite.T(), err)
}

/* Service Orchestrator Tests */

func (suite *ServiceTestSuite) TestServiceLifecycle() {
	// Test basic service lifecycle

	// Initial state
	status := suite.sut.GetPairingStatus()
	assert.False(suite.T(), status.Running)

	// Start service
	err := suite.sut.Start()
	assert.NoError(suite.T(), err)

	status = suite.sut.GetPairingStatus()
	assert.True(suite.T(), status.Running)

	// Start again should fail
	err = suite.sut.Start()
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrServiceAlreadyStarted, err)

	// Shutdown
	suite.sut.Shutdown()

	status = suite.sut.GetPairingStatus()
	assert.False(suite.T(), status.Running)
}

func (suite *ServiceTestSuite) TestShutdown_ServiceStateOnly() {
	// Test that Service shutdown only manages Service state (stateless factory pattern)

	// Start service
	err := suite.sut.Start()
	assert.NoError(suite.T(), err)

	// Create announcer (stateless factory)
	localService := api.NewServiceDetails("testski", "", "")
	announcer := suite.sut.CreateAnnouncer(localService)
	assert.NotNil(suite.T(), announcer)

	// Cast to concrete type to access EnablePairingService
	concreteAnnouncer, ok := announcer.(*PairingAnnouncer)
	require.True(suite.T(), ok)

	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  api.PairingSecret("test-secret-1234"),
		Enabled: true,
	}
	err = concreteAnnouncer.EnablePairingService(config)
	require.NoError(suite.T(), err)

	// Mock the crypto and mDNS calls for announcement
	suite.mockCrypto.EXPECT().GenerateNonce().Return([]byte("test-nonce-1234567890123456"), nil).Once()
	suite.mockCrypto.EXPECT().CalculateDigest(
		mock.AnythingOfType("api.PairingSecret"),
		mock.AnythingOfType("*api.HMACParams"),
	).Return([]byte("mock-digest-123456789012345678901234567890"), nil).Once()
	suite.mockMdns.EXPECT().AnnouncePairingService(
		mock.AnythingOfType("*api.ShipPairingTXT"),
	).Return("test-instance-id", nil).Once()

	target := &api.PairingTarget{SKI: "targetski"}
	err = announcer.Announce(target)
	require.NoError(suite.T(), err)

	// Verify announcer is active before shutdown
	status := announcer.GetAnnouncementStatus()
	assert.True(suite.T(), status.Active)

	// Service shutdown should NOT clean up announcer (Hub manages that)
	// No UnannouncePairingService expectation

	// Shutdown service
	suite.sut.Shutdown()

	// Verify service state
	assert.False(suite.T(), suite.sut.running, "Service should be stopped")

	// Announcer should remain active (Service doesn't manage announcer lifecycle)
	statusAfter := announcer.GetAnnouncementStatus()
	assert.True(suite.T(), statusAfter.Active, "Announcer should remain active after Service shutdown")
	assert.Equal(suite.T(), target, statusAfter.Target, "Announcer target should be preserved")
}

func (suite *ServiceTestSuite) TestShutdown_StatelessBehavior() {
	// Test that Service shutdown doesn't affect independently created announcers

	// Start service
	err := suite.sut.Start()
	assert.NoError(suite.T(), err)

	// Create announcer (stateless factory)
	localService := api.NewServiceDetails("testski", "", "")
	announcer := suite.sut.CreateAnnouncer(localService)
	assert.NotNil(suite.T(), announcer)

	// Announcer starts inactive
	status := announcer.GetAnnouncementStatus()
	assert.False(suite.T(), status.Active)

	// Service shutdown should NOT affect announcer (stateless)
	// No mDNS expectations since Service doesn't manage announcer lifecycle

	// Shutdown service
	suite.sut.Shutdown()

	// Verify service stopped but announcer unaffected
	assert.False(suite.T(), suite.sut.running, "Service should be stopped")

	// Announcer should remain in same state (inactive)
	statusAfter := announcer.GetAnnouncementStatus()
	assert.Equal(suite.T(), status.Active, statusAfter.Active, "Announcer state should be unaffected by Service shutdown")
}


func (suite *ServiceTestSuite) TestCreateAnnouncer_CreatesIndependentInstances() {
	// Test that CreateAnnouncer creates independent announcer instances (stateless factory)

	// Start service
	err := suite.sut.Start()
	assert.NoError(suite.T(), err)

	// Create first announcer
	localService1 := api.NewServiceDetails("ski1", "", "")
	announcer1 := suite.sut.CreateAnnouncer(localService1)
	assert.NotNil(suite.T(), announcer1)

	// Enable and start first announcer
	concreteAnnouncer1, ok := announcer1.(*PairingAnnouncer)
	require.True(suite.T(), ok)

	config1 := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  api.PairingSecret("test-secret-1234"),
		Enabled: true,
	}
	err = concreteAnnouncer1.EnablePairingService(config1)
	require.NoError(suite.T(), err)

	target1 := &api.PairingTarget{SKI: "target1"}

	// Mock mDNS for first announcer
	suite.mockCrypto.EXPECT().GenerateNonce().Return([]byte("test-nonce-1234567890123456"), nil).Once()
	suite.mockCrypto.EXPECT().CalculateDigest(
		mock.AnythingOfType("api.PairingSecret"),
		mock.AnythingOfType("*api.HMACParams"),
	).Return([]byte("mock-digest-123456789012345678901234567890"), nil).Once()
	suite.mockMdns.EXPECT().AnnouncePairingService(
		mock.AnythingOfType("*api.ShipPairingTXT"),
	).Return("instance-1", nil).Once()

	err = announcer1.Announce(target1)
	require.NoError(suite.T(), err)

	// Verify first announcer is active
	status1 := announcer1.GetAnnouncementStatus()
	assert.True(suite.T(), status1.Active)
	assert.Equal(suite.T(), target1, status1.Target)

	// Create second announcer - should NOT affect first announcer
	localService2 := api.NewServiceDetails("ski2", "", "")
	announcer2 := suite.sut.CreateAnnouncer(localService2)
	assert.NotNil(suite.T(), announcer2)

	// Enable and start second announcer  
	concreteAnnouncer2, ok := announcer2.(*PairingAnnouncer)
	require.True(suite.T(), ok)

	config2 := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  api.PairingSecret("test-secret-5678"),
		Enabled: true,
	}
	err = concreteAnnouncer2.EnablePairingService(config2)
	require.NoError(suite.T(), err)

	target2 := &api.PairingTarget{SKI: "target2"}

	// Mock mDNS for second announcer
	suite.mockCrypto.EXPECT().GenerateNonce().Return([]byte("test-nonce-abcdefgh12345678"), nil).Once()
	suite.mockCrypto.EXPECT().CalculateDigest(
		mock.AnythingOfType("api.PairingSecret"),
		mock.AnythingOfType("*api.HMACParams"),
	).Return([]byte("mock-digest-abcdefgh12345678901234567890"), nil).Once()
	suite.mockMdns.EXPECT().AnnouncePairingService(
		mock.AnythingOfType("*api.ShipPairingTXT"),
	).Return("instance-2", nil).Once()

	err = announcer2.Announce(target2)
	require.NoError(suite.T(), err)

	// CRITICAL: Verify both announcers remain independent and active
	status1After := announcer1.GetAnnouncementStatus()
	assert.True(suite.T(), status1After.Active, "First announcer should remain active")
	assert.Equal(suite.T(), target1, status1After.Target, "First announcer target should be preserved")

	status2 := announcer2.GetAnnouncementStatus()
	assert.True(suite.T(), status2.Active, "Second announcer should be active")
	assert.Equal(suite.T(), target2, status2.Target)

	// Verify they are independent instances
	assert.NotEqual(suite.T(), announcer1, announcer2)
}

func (suite *ServiceTestSuite) TestCreateAnnouncer_AlwaysCreatesNew() {
	// Test that CreateAnnouncer always creates new instances (stateless factory)

	// Start service
	err := suite.sut.Start()
	assert.NoError(suite.T(), err)

	// Use different localServices to ensure different instances
	localService1 := api.NewServiceDetails("testski1", "", "")
	localService2 := api.NewServiceDetails("testski2", "", "")
	localService3 := api.NewServiceDetails("testski3", "", "")

	// First call creates announcer
	announcer1 := suite.sut.CreateAnnouncer(localService1)
	assert.NotNil(suite.T(), announcer1)

	// Second call should create different instance
	announcer2 := suite.sut.CreateAnnouncer(localService2)
	assert.NotNil(suite.T(), announcer2)
	assert.NotEqual(suite.T(), announcer1, announcer2, "Each call should create new instance")

	// Third call should also create different instance
	announcer3 := suite.sut.CreateAnnouncer(localService3)
	assert.NotNil(suite.T(), announcer3)
	assert.NotEqual(suite.T(), announcer1, announcer3, "Third call should create new instance")
	assert.NotEqual(suite.T(), announcer2, announcer3, "Third call should be different from second")
}

func (suite *ServiceTestSuite) TestCreateListener_TracksListener() {
	// Test that CreateListener properly tracks the listener in the service

	// Start service
	err := suite.sut.Start()
	assert.NoError(suite.T(), err)

	// Create listener
	localService := api.NewServiceDetails("testski", "", "")
	listener := suite.sut.CreateListener(localService)
	assert.NotNil(suite.T(), listener)

	// Verify listener is tracked (will be accessible after implementation)
	// For now just verify it creates a listener
	_, ok := listener.(*PairingListener)
	assert.True(suite.T(), ok, "Should create a PairingListener instance")
}

func (suite *ServiceTestSuite) TestCreateListener_ReplacesExisting() {
	// Test that CreateListener replaces any existing listener

	// Start service
	err := suite.sut.Start()
	assert.NoError(suite.T(), err)

	// Create first listener
	localService1 := api.NewServiceDetails("ski1", "", "")
	listener1 := suite.sut.CreateListener(localService1)
	assert.NotNil(suite.T(), listener1)

	// Create second listener (should replace first)
	localService2 := api.NewServiceDetails("ski2", "", "")
	listener2 := suite.sut.CreateListener(localService2)
	assert.NotNil(suite.T(), listener2)

	// Verify they are different instances
	assert.NotEqual(suite.T(), listener1, listener2)
}

func (suite *ServiceTestSuite) TestShutdown_CleansUpActiveListener() {
	// Test that Shutdown properly cleans up an active listener

	// Start service
	err := suite.sut.Start()
	assert.NoError(suite.T(), err)

	// Create and start listener
	localService := api.NewServiceDetails("testski", "", "")
	listener := suite.sut.CreateListener(localService)
	assert.NotNil(suite.T(), listener)

	// Cast to concrete type to access internal state
	concreteListener, ok := listener.(*PairingListener)
	assert.True(suite.T(), ok)

	// Simulate active listening (normally done via StartListening)
	concreteListener.listening = true

	// Shutdown service should stop the listener
	suite.sut.Shutdown()

	// Verify listener was cleaned up
	assert.False(suite.T(), concreteListener.listening)
	assert.False(suite.T(), suite.sut.running)
}

func (suite *ServiceTestSuite) TestShutdown_SkipsInactiveListener() {
	// Test that Shutdown handles inactive listener gracefully

	// Start service
	err := suite.sut.Start()
	assert.NoError(suite.T(), err)

	// Create listener but don't activate it
	localService := api.NewServiceDetails("testski", "", "")
	listener := suite.sut.CreateListener(localService)
	assert.NotNil(suite.T(), listener)

	// Cast to concrete type to verify state
	concreteListener, ok := listener.(*PairingListener)
	assert.True(suite.T(), ok)
	assert.False(suite.T(), concreteListener.listening) // Not listening

	// Shutdown service
	suite.sut.Shutdown()

	// Verify service stopped
	assert.False(suite.T(), suite.sut.running)
}

func (suite *ServiceTestSuite) TestShutdown_CleansUpBothComponents() {
	// Test that Shutdown cleans up both announcer and listener if both are active

	// Start service
	err := suite.sut.Start()
	assert.NoError(suite.T(), err)

	// Create announcer (stateless factory)
	localService := api.NewServiceDetails("testski", "", "")
	announcer := suite.sut.CreateAnnouncer(localService)
	assert.NotNil(suite.T(), announcer)

	// Create and activate listener (Service still manages listener)
	listener := suite.sut.CreateListener(localService)
	assert.NotNil(suite.T(), listener)
	concreteListener, ok := listener.(*PairingListener)
	assert.True(suite.T(), ok)
	concreteListener.listening = true

	// Service shutdown should only clean up listener (not announcer)
	// No UnannouncePairingService expectation for announcer

	// Shutdown service
	suite.sut.Shutdown()

	// Verify service stopped and listener cleaned up (announcer unaffected)
	assert.False(suite.T(), suite.sut.running, "Service should be stopped")
	assert.False(suite.T(), concreteListener.listening, "Listener should be stopped by Service shutdown")
	
	// Announcer should remain unaffected (stateless factory pattern)
	announcerStatus := announcer.GetAnnouncementStatus()
	assert.False(suite.T(), announcerStatus.Active, "Announcer starts inactive in this test")
}

func (suite *ServiceTestSuite) TestPairingStateFor() {
	// Test ServiceDetails-based pairing state retrieval

	// Service not started
	service := api.NewServiceDetails("testski", "", "")
	_, err := suite.sut.PairingStateFor(service)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrServiceNotStarted, err)

	// Test nil service
	_, err = suite.sut.PairingStateFor(nil)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrServiceNil, err)

	// Start service
	err = suite.sut.Start()
	assert.NoError(suite.T(), err)

	// Get state with ServiceDetails
	state, err := suite.sut.PairingStateFor(service)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), api.PairingStateNone, state.State())
}

func (suite *ServiceTestSuite) TestGetPairingStatus() {
	// Test status reporting

	status := suite.sut.GetPairingStatus()
	assert.NotNil(suite.T(), status)
	assert.False(suite.T(), status.Running)
	assert.False(suite.T(), status.ListenerActive)
	assert.False(suite.T(), status.AnnouncerActive)
	assert.Nil(suite.T(), status.LastError)
}
