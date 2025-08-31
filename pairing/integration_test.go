package pairing

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
)

// IntegrationTestSuite contains end-to-end integration tests for complete SHIP Pairing Service
// Tests both announcer (devZ) and listener (devA) functionality together
type IntegrationTestSuite struct {
	suite.Suite

	// Mock infrastructure
	mockMdns     *mocks.MdnsPairingInterface
	mockCrypto   *mocks.PairingCryptoInterface
	mockHistoryA *mocks.PairingHistoryProviderInterface // devA history
	mockHistoryZ *mocks.PairingHistoryProviderInterface // devZ history
	mockHubA     *mocks.PairingHubInterface             // devA hub
	mockHubZ     *mocks.PairingHubInterface             // devZ hub

	// Test certificates
	testCert  tls.Certificate
	localCert *x509.Certificate

	// Device configurations from SHIP spec
	devAService  *api.ServiceDetails // Heat pump (listener)
	devZService  *api.ServiceDetails // SMGW (announcer)
	sharedSecret api.PairingSecret

	// System components
	announcer *PairingAnnouncer
	listener  *PairingListener
	service   *Service
}

func TestIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(IntegrationTestSuite))
}

func (suite *IntegrationTestSuite) SetupTest() {
	// Setup mock infrastructure
	suite.mockMdns = mocks.NewMdnsPairingInterface(suite.T())
	suite.mockCrypto = mocks.NewPairingCryptoInterface(suite.T())
	suite.mockHistoryA = mocks.NewPairingHistoryProviderInterface(suite.T())
	suite.mockHistoryZ = mocks.NewPairingHistoryProviderInterface(suite.T())
	suite.mockHubA = mocks.NewPairingHubInterface(suite.T())
	suite.mockHubZ = mocks.NewPairingHubInterface(suite.T())

	// Create real test certificate
	var err error
	suite.testCert, err = cert.CreateCertificate("TestOU", "TestOrg", "DE", "test-device")
	require.NoError(suite.T(), err)

	// Extract X.509 certificate
	suite.localCert, err = x509.ParseCertificate(suite.testCert.Certificate[0])
	require.NoError(suite.T(), err)

	// Setup device configurations using SHIP spec test vectors
	suite.devAService = api.NewServiceDetails("heatpumpski", "", "")
	suite.devAService.SetShipID("i:983327_u:C8277H008F-3") // devA from spec

	suite.devZService = api.NewServiceDetails("smgwski", "", "")
	suite.devZService.SetShipID("i:46925_u:43652bk-2-gt1") // devZ from spec

	suite.sharedSecret = api.PairingSecret("7A37DCF81BDB50F8E92CFA4160CCB3DE") // From spec

	// Create components
	suite.announcer = NewPairingAnnouncer(
		suite.mockMdns, suite.mockCrypto, suite.localCert,
		suite.mockHistoryZ, suite.devZService,
	)

	suite.listener = NewPairingListener(
		suite.mockMdns, suite.mockCrypto,
		suite.mockHistoryA, suite.mockHubA,
		suite.devAService,
	)

	suite.service, err = NewService(
		suite.mockMdns, suite.mockCrypto,
		suite.mockHistoryA, suite.mockHubA,
		suite.testCert,
	)
	require.NoError(suite.T(), err)
}

/* End-to-End Integration Tests */

func (suite *IntegrationTestSuite) TestCompleteAnnouncerListenerFlow() {
	// Test complete pairing flow: devZ announces -> devA receives -> auto-trust

	ctx := context.Background()

	// Start listener (devA/heat pump)
	suite.mockMdns.EXPECT().
		SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).
		Return(nil).
		Once()

	err := suite.listener.StartListening(ctx, suite.sharedSecret)
	assert.NoError(suite.T(), err)

	// Configure announcer (devZ/SMGW)
	announcerConfig := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  suite.sharedSecret,
		Enabled: true,
	}
	err = suite.announcer.EnablePairingService(announcerConfig)
	assert.NoError(suite.T(), err)

	// Setup announcement mocks for devZ
	suite.mockCrypto.EXPECT().GenerateNonce().Return([]byte{0x01, 0x02}, nil).Once()
	suite.mockCrypto.EXPECT().CalculateDigest(suite.sharedSecret, mock.AnythingOfType("*api.HMACParams")).Return([]byte{0xAA, 0xBB}, nil).Once()
	suite.mockMdns.EXPECT().AnnouncePairingService(mock.AnythingOfType("*api.ShipPairingTXT")).Return("test-instance-id", nil).Once()

	// Create target (devA from perspective of devZ)
	target := &api.PairingTarget{
		SKI:         "heatpumpski",
		Fingerprint: "HEAT_PUMP_FINGERPRINT",
		ShipID:      suite.devAService.ShipID(),
	}

	// devZ announces pairing
	err = suite.announcer.Announce(target)
	assert.NoError(suite.T(), err)

	// Simulate devA receiving the announcement (this would come via mDNS in real scenario)
	announcementTXT := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		ForId:      suite.devAService.ShipID(), // For heat pump
		ForPar:     "HEAT_PUMP_FINGERPRINT",
		TrustId:    suite.devZService.ShipID(), // From SMGW
		TrustPar:   "SMGW_FINGERPRINT",
		TrustCurve: api.CurveSecp256r1,
		Type:       api.CommandTypeAddCU,
		TrustNonce: "0102",
		Alg:        api.AlgorithmHMACSHA256,
		Digest:     "AABB",
	}

	// Setup listener validation mocks
	suite.mockCrypto.EXPECT().ValidateDigest(suite.sharedSecret, mock.AnythingOfType("*api.HMACParams"), []byte{0xAA, 0xBB}).Return(nil).Once()
	suite.mockHistoryA.EXPECT().HasSeenDigest(api.AlgorithmHMACSHA256, "AABB").Return(false).Once()
	suite.mockHistoryA.EXPECT().RecordPairing(api.AlgorithmHMACSHA256, "AABB").Return().Once()
	// Note: ShouldAutoTrust removed - SHIP Pairing Service is autonomous
	suite.mockHubA.EXPECT().OnPairingSuccess(suite.devZService.ShipID(), "SMGW_FINGERPRINT").Return().Once()

	// devA processes the announcement
	result := suite.listener.handleMdnsDiscovery(announcementTXT)
	assert.False(suite.T(), result, "listener should stop after accepting pairing")

	// Verify final states
	listenerStatus := suite.listener.GetListenerStatus()
	assert.False(suite.T(), listenerStatus.Active, "listener should have stopped")
	assert.Equal(suite.T(), 1, listenerStatus.RequestsSeen, "should have processed one request")

	announcerStatus := suite.announcer.GetPairingServiceStatus()
	assert.True(suite.T(), announcerStatus.AnnouncerActive, "announcer should still be active until connection")
}

func (suite *IntegrationTestSuite) TestServiceOrchestration() {
	// Test the Service orchestrator with both announcer and listener

	// Start service
	err := suite.service.Start()
	assert.NoError(suite.T(), err)

	// Create and configure listener
	listener := suite.service.CreateListener(suite.devAService)
	assert.NotNil(suite.T(), listener)

	// Verify service status
	status := suite.service.GetPairingStatus()
	assert.True(suite.T(), status.Running)
	assert.False(suite.T(), status.AnnouncerActive)
	assert.False(suite.T(), status.ListenerActive)

	// Shutdown service
	suite.service.Shutdown()

	status = suite.service.GetPairingStatus()
	assert.False(suite.T(), status.Running)
}

func (suite *IntegrationTestSuite) TestReplayAttackPrevention() {
	// Test replay attack prevention across announcer/listener interaction

	ctx := context.Background()

	// Start listener
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.listener.StartListening(ctx, suite.sharedSecret)
	assert.NoError(suite.T(), err)

	// Create announcement
	announcement := suite.createValidTestTXTRecord()
	announcement.ForId = suite.devAService.ShipID()
	announcement.TrustId = suite.devZService.ShipID()

	// First attempt - should succeed
	suite.mockCrypto.EXPECT().ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).Return(nil).Once()
	suite.mockHistoryA.EXPECT().HasSeenDigest(announcement.Alg, announcement.Digest).Return(false).Once() // Not seen before
	suite.mockHistoryA.EXPECT().RecordPairing(announcement.Alg, announcement.Digest).Return().Once()
	// Note: ShouldAutoTrust removed - SHIP Pairing Service is autonomous
	suite.mockHubA.EXPECT().OnPairingSuccess(announcement.TrustId, announcement.TrustPar).Return().Once()

	result := suite.listener.handleMdnsDiscovery(announcement)
	assert.False(suite.T(), result, "first attempt should succeed and stop listener")

	// Restart listener for second attempt
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err = suite.listener.StartListening(ctx, suite.sharedSecret)
	assert.NoError(suite.T(), err)

	// Second attempt with same digest - should detect replay
	suite.mockCrypto.EXPECT().ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).Return(nil).Once()
	suite.mockHistoryA.EXPECT().HasSeenDigest(announcement.Alg, announcement.Digest).Return(true).Once() // Already seen - replay!
	suite.mockHubA.EXPECT().OnPairingFailure(announcement.TrustId, announcement.TrustPar, api.ErrReplayAttackDetected).Return().Once()

	result = suite.listener.handleMdnsDiscovery(announcement)
	assert.True(suite.T(), result, "replay attempt should be rejected and continue listening")

	// Verify listener still active after replay detection
	status := suite.listener.GetListenerStatus()
	assert.True(suite.T(), status.Active, "listener should remain active after replay detection")
}

func (suite *IntegrationTestSuite) TestFailureScenarios() {
	// Test various failure scenarios in the pairing flow

	// Test 1: Invalid HMAC should not affect announcer
	target := &api.PairingTarget{
		SKI:         suite.devAService.SKI(),
		Fingerprint: "INVALID_FINGERPRINT",
		ShipID:      suite.devAService.ShipID(),
	}

	// Configure announcer
	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  suite.sharedSecret,
		Enabled: true,
	}
	err := suite.announcer.EnablePairingService(config)
	assert.NoError(suite.T(), err)

	// Mock crypto operations for announcement
	suite.mockCrypto.EXPECT().GenerateNonce().Return([]byte{0x01, 0x02}, nil).Once()
	suite.mockCrypto.EXPECT().CalculateDigest(suite.sharedSecret, mock.AnythingOfType("*api.HMACParams")).Return([]byte{0xAA, 0xBB}, nil).Once()
	suite.mockMdns.EXPECT().AnnouncePairingService(mock.AnythingOfType("*api.ShipPairingTXT")).Return("test-instance-id", nil).Once()

	// Announce should succeed with real certificates
	err = suite.announcer.Announce(target)
	assert.NoError(suite.T(), err)

	// Announcer should be marked as active after successful announcement
	status := suite.announcer.GetPairingServiceStatus()
	assert.True(suite.T(), status.AnnouncerActive)
}

func (suite *IntegrationTestSuite) TestDeviceFilteringAndMatching() {
	// Test that devices only respond to announcements intended for them

	ctx := context.Background()

	// Start listener for devA
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.listener.StartListening(ctx, suite.sharedSecret)
	assert.NoError(suite.T(), err)

	// Create announcement for different device
	wrongAnnouncement := suite.createValidTestTXTRecord()
	wrongAnnouncement.ForId = "i:999_u:different-device" // Not for our devA
	wrongAnnouncement.TrustId = suite.devZService.ShipID()

	// Process announcement - should be ignored (no mocks called)
	result := suite.listener.handleMdnsDiscovery(wrongAnnouncement)
	assert.True(suite.T(), result, "should ignore announcements for different devices")

	// Verify no state changes
	status := suite.listener.GetListenerStatus()
	assert.True(suite.T(), status.Active, "listener should remain active")
	assert.Equal(suite.T(), 0, status.RequestsSeen, "should not count ignored announcements")
}

func (suite *IntegrationTestSuite) TestConnectionCleanupFlow() {
	// Test that announcer cleans up after successful connection
	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  suite.sharedSecret,
		Enabled: true,
	}
	err := suite.announcer.EnablePairingService(config)
	assert.NoError(suite.T(), err)

	// Mock successful announcement
	target := &api.PairingTarget{
		SKI:         suite.devAService.SKI(),
		Fingerprint: "HEAT_PUMP_FP",
		ShipID:      suite.devAService.ShipID(),
	}

	suite.mockCrypto.EXPECT().GenerateNonce().Return([]byte{0x01, 0x02}, nil).Once()
	suite.mockCrypto.EXPECT().CalculateDigest(suite.sharedSecret, mock.AnythingOfType("*api.HMACParams")).Return([]byte{0xAA, 0xBB}, nil).Once()
	suite.mockMdns.EXPECT().AnnouncePairingService(mock.AnythingOfType("*api.ShipPairingTXT")).Return("test-instance-id", nil).Once()

	err = suite.announcer.Announce(target)
	assert.NoError(suite.T(), err)

	// Verify announcement is active
	status := suite.announcer.GetPairingServiceStatus()
	assert.True(suite.T(), status.AnnouncerActive)

	// Mock pairing cleanup when connection established
	suite.mockMdns.EXPECT().UnannouncePairingService(mock.AnythingOfType("string")).Return(nil).Once()

	// Simulate connection establishment (per SHIP spec section 4.2)
	suite.announcer.OnConnectionEstablished(suite.devAService.SKI())

	// Verify announcement cleaned up
	status = suite.announcer.GetPairingServiceStatus()
	assert.False(suite.T(), status.AnnouncerActive, "announcer should cleanup after connection")
}

/* Test Helper Functions */

// createValidTestTXTRecord creates a valid ShipPairingTXT record for testing
// Uses test vectors from SHIP specification Annex A.3
func (suite *IntegrationTestSuite) createValidTestTXTRecord() *api.ShipPairingTXT {
	return &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		ForId:      "i:983327_u:C8277H008F-3", // Our device (devA)
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		TrustId:    "i:46925_u:43652bk-2-gt1", // SMGW (devZ)
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustCurve: api.CurveSecp256r1,
		Type:       api.CommandTypeAddCU,
		TrustNonce: "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		Alg:        api.AlgorithmHMACSHA256,
		Digest:     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}
}
