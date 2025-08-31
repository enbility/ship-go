package pairing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
)

// AutonomousListenerTestSuite tests SHIP Pairing Service autonomous behavior per specification
// Validates that pairing acceptance is purely based on HMAC validation, not user interaction
type AutonomousListenerTestSuite struct {
	suite.Suite

	// Mock dependencies
	mockMdns    *mocks.MdnsPairingInterface
	mockCrypto  *mocks.PairingCryptoInterface
	mockHistory *mocks.PairingHistoryProviderInterface
	mockHub     *mocks.PairingHubInterface

	// Test data
	localService *api.ServiceDetails
	testSecret   api.PairingSecret

	// System under test
	sut *PairingListener
}

func TestAutonomousListenerTestSuite(t *testing.T) {
	suite.Run(t, new(AutonomousListenerTestSuite))
}

func (suite *AutonomousListenerTestSuite) SetupTest() {
	// Setup mocks
	suite.mockMdns = mocks.NewMdnsPairingInterface(suite.T())
	suite.mockCrypto = mocks.NewPairingCryptoInterface(suite.T())
	suite.mockHistory = mocks.NewPairingHistoryProviderInterface(suite.T())
	suite.mockHub = mocks.NewPairingHubInterface(suite.T())

	// Setup test data
	suite.localService = api.NewServiceDetails("heatpumpski", "", "")
	suite.localService.SetShipID("i:983327_u:C8277H008F-3")
	suite.testSecret = api.PairingSecret("7A37DCF81BDB50F8E92CFA4160CCB3DE")

	// Create listener
	suite.sut = NewPairingListener(
		suite.mockMdns, suite.mockCrypto,
		suite.mockHistory, suite.mockHub,
		suite.localService,
	)
}

/* TDD Tests for Autonomous Behavior */

func (suite *AutonomousListenerTestSuite) TestAutonomousAcceptance_ValidHMAC() {
	// Test that valid HMAC ALWAYS results in automatic acceptance (no conditions)

	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		ForId:      suite.localService.ShipID(), // For our device
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		TrustId:    "i:46925_u:43652bk-2-gt1", // SMGW
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustCurve: api.CurveSecp256r1,
		Type:       api.CommandTypeAddCU,
		TrustNonce: "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		Alg:        api.AlgorithmHMACSHA256,
		Digest:     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}

	// Mock AddCu device check (no existing device for this test)
	suite.mockHub.EXPECT().HasTrustedAddCuDevice().Return("", "").Maybe()

	// Mock successful HMAC validation
	suite.mockCrypto.EXPECT().ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).Return(nil).Once()

	// Mock replay protection (not seen before)
	suite.mockHistory.EXPECT().HasSeenDigest(api.AlgorithmHMACSHA256, txtRecord.Digest).Return(false).Once()

	// Mock successful pairing recording
	suite.mockHistory.EXPECT().RecordPairing(api.AlgorithmHMACSHA256, txtRecord.Digest).Return().Once()

	// Mock trust establishment (NO ShouldAutoTrust check!)
	suite.mockHub.EXPECT().OnPairingSuccess(txtRecord.TrustId, txtRecord.TrustPar).Return().Once()

	// Mock notification (notification only, not decision)

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Process pairing request - should be accepted autonomously
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.False(suite.T(), result, "Valid HMAC should result in automatic acceptance and stop listening")

	// Verify listener stopped per SHIP spec section 4.3
	status := suite.sut.GetListenerStatus()
	assert.False(suite.T(), status.Active, "Listener should stop after autonomous acceptance")
}

func (suite *AutonomousListenerTestSuite) TestAutonomousAcceptance_IndependentOfHubSettings() {
	// Test that pairing acceptance is independent of Hub auto-accept configuration

	// This test should FAIL with current implementation because it checks ShouldAutoTrust
	// After fix, this should pass regardless of Hub configuration

	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		ForId:      suite.localService.ShipID(),
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		TrustId:    "smgw-device",
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustCurve: api.CurveSecp256r1,
		Type:       api.CommandTypeAddCU,
		TrustNonce: "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		Alg:        api.AlgorithmHMACSHA256,
		Digest:     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}

	// Mock AddCu device check (no existing device for this test)
	suite.mockHub.EXPECT().HasTrustedAddCuDevice().Return("", "").Maybe()

	// Mock successful validation
	suite.mockCrypto.EXPECT().ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).Return(nil).Once()
	suite.mockHistory.EXPECT().HasSeenDigest(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(false).Once()
	suite.mockHistory.EXPECT().RecordPairing(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return().Once()

	// Should NOT call ShouldAutoTrust (autonomous behavior)
	// Should directly call OnPairingSuccess
	suite.mockHub.EXPECT().OnPairingSuccess(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return().Once()

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Process request - should be autonomous regardless of Hub settings
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.False(suite.T(), result, "Autonomous acceptance should be independent of Hub configuration")
}

func (suite *AutonomousListenerTestSuite) TestNoUserConfirmationRequired() {
	// Test that SHIP Pairing Service NEVER requires user confirmation

	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		ForId:      suite.localService.ShipID(),
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		TrustId:    "smgw-device",
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustCurve: api.CurveSecp256r1,
		Type:       api.CommandTypeAddCU,
		TrustNonce: "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		Alg:        api.AlgorithmHMACSHA256,
		Digest:     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}

	// Mock AddCu device check (no existing device for this test)
	suite.mockHub.EXPECT().HasTrustedAddCuDevice().Return("", "").Maybe()

	// Mock successful validation chain
	suite.mockCrypto.EXPECT().ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).Return(nil).Once()
	suite.mockHistory.EXPECT().HasSeenDigest(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(false).Once()
	suite.mockHistory.EXPECT().RecordPairing(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return().Once()
	suite.mockHub.EXPECT().OnPairingSuccess(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return().Once()

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Process request - should be accepted autonomously without user interaction
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.False(suite.T(), result, "Valid request should be autonomously accepted without user confirmation")
}

func (suite *AutonomousListenerTestSuite) TestSpecificationCompliance_Section4_2() {
	// Test compliance with SHIP spec section 4.2: "SHALL trust" after successful evaluation

	// This test validates the exact SHIP specification behavior:
	// "If devA successfully evaluated and accepted a shippairing request, devA SHALL trust..."

	txtRecord := suite.createValidTestTXTRecord()

	// Mock AddCu device check (no existing device for this test)
	suite.mockHub.EXPECT().HasTrustedAddCuDevice().Return("", "").Maybe()

	// Mock the evaluation steps per SHIP spec section 9
	suite.mockCrypto.EXPECT().ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).Return(nil).Once() // Step 3
	suite.mockHistory.EXPECT().HasSeenDigest(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(false).Once()                                                   // Step 2
	suite.mockHistory.EXPECT().RecordPairing(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return().Once()                                                        // Step 4

	// After successful evaluation, SHALL trust (mandatory)
	suite.mockHub.EXPECT().OnPairingSuccess(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return().Once()

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Test autonomous behavior per specification
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.False(suite.T(), result, "SHALL trust after successful evaluation per SHIP spec 4.2")

	// Verify deactivation per specification section 4.3
	status := suite.sut.GetListenerStatus()
	assert.False(suite.T(), status.Active, "SHALL deactivate after acceptance per SHIP spec 4.3")
}

// createValidTestTXTRecord creates a valid pairing request for testing
func (suite *AutonomousListenerTestSuite) createValidTestTXTRecord() *api.ShipPairingTXT {
	return &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		ForId:      suite.localService.ShipID(),
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		TrustId:    "i:46925_u:43652bk-2-gt1",
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustCurve: api.CurveSecp256r1,
		Type:       api.CommandTypeAddCU,
		TrustNonce: "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		Alg:        api.AlgorithmHMACSHA256,
		Digest:     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}
}
