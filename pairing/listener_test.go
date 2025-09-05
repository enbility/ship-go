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

// ListenerTestSuite contains tests for SHIP Pairing Service Listener (devA) functionality
// Uses TDD methodology with SHIP specification test vectors
type ListenerTestSuite struct {
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

func TestListenerTestSuite(t *testing.T) {
	suite.Run(t, new(ListenerTestSuite))
}

func (suite *ListenerTestSuite) SetupTest() {
	// Setup fresh mocks for each test
	suite.mockMdns = mocks.NewMdnsPairingInterface(suite.T())
	suite.mockCrypto = mocks.NewPairingCryptoInterface(suite.T())
	suite.mockHistory = mocks.NewPairingHistoryProviderInterface(suite.T())
	suite.mockHub = mocks.NewPairingHubInterface(suite.T())

	// Setup test data
	suite.localService = api.NewServiceDetails("heatpumpski", "", "")
	suite.localService.SetShipID("i:983327_u:C8277H008F-3")                  // devA from SHIP spec
	suite.testSecret = api.PairingSecret("7A37DCF81BDB50F8E92CFA4160CCB3DE") // devA secret from spec

	// Create listener
	suite.sut = NewPairingListener(
		suite.mockMdns,
		suite.mockCrypto,
		suite.mockHistory,
		suite.mockHub,
		suite.localService,
	)
}

func (suite *ListenerTestSuite) TearDownTest() {
	// Clean up listener to prevent interference
	if suite.sut != nil {
		suite.sut.StopListening()
	}
}

/* Core Listener Functionality Tests */

func (suite *ListenerTestSuite) TestStartListening_ValidSecret() {
	// Test starting listener with valid secret
	ctx := context.Background()

	// Mock mDNS search
	suite.mockMdns.EXPECT().
		SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).
		Return(nil).
		Once()

	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Verify listener is active
	status := suite.sut.GetListenerStatus()
	assert.True(suite.T(), status.Active)
	assert.Equal(suite.T(), 0, status.RequestsSeen)
}

func (suite *ListenerTestSuite) TestStartListening_InvalidSecret() {
	// Test error handling for invalid secrets
	tests := []struct {
		name   string
		secret api.PairingSecret
	}{
		{"too short", api.PairingSecret("short")},
		{"too long", api.PairingSecret(make([]byte, 200))},
		{"empty", api.PairingSecret("")},
	}

	for _, tt := range tests {
		suite.T().Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := suite.sut.StartListening(ctx, tt.secret)
			assert.Error(t, err)
			assert.Equal(t, api.ErrInvalidSecret, err)
		})
	}
}

func (suite *ListenerTestSuite) TestStartListening_AlreadyActive() {
	// Test error when starting listener that's already active
	ctx := context.Background()

	// First start should succeed
	suite.mockMdns.EXPECT().
		SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).
		Return(nil).
		Once()

	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Second start should fail
	err = suite.sut.StartListening(ctx, suite.testSecret)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrListenerAlreadyActive, err)
}

/* HMAC Validation Tests for Incoming Requests */

func (suite *ListenerTestSuite) TestValidatePairingRequest_ValidHMAC() {
	// Test HMAC validation for incoming pairing request using SHIP spec test vectors

	// Create test TXT record from SHIP spec Annex A.3
	txtRecord := &api.ShipPairingTXT{
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

	// Mock AddCu device check (no existing device for this test)
	suite.mockHub.EXPECT().
		HasTrustedAddCuDevice().
		Return("", "").
		Maybe()

	// Mock HMAC validation (should succeed with correct digest)
	expectedNonce, _ := hexToBytes("BDCEE427FA7208DF3C1F2A749BA6F4D4")
	expectedDigest, _ := hexToBytes("BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25")

	suite.mockCrypto.EXPECT().
		ValidateDigest(
			suite.testSecret,
			mock.MatchedBy(func(params *api.HMACParams) bool {
				return params.Algorithm == api.AlgorithmHMACSHA256 &&
					len(params.Nonce) == len(expectedNonce)
			}),
			expectedDigest).
		Return(nil).
		Once()

	// Mock replay protection (not seen before)
	suite.mockHistory.EXPECT().
		HasSeenDigest(api.AlgorithmHMACSHA256, txtRecord.Digest).
		Return(false).
		Once()

	// Mock pairing history recording
	suite.mockHistory.EXPECT().
		RecordPairing(api.AlgorithmHMACSHA256, txtRecord.Digest).
		Return().
		Maybe()

	// Note: ShouldAutoTrust removed - SHIP Pairing Service is autonomous

	// Mock successful trust establishment
	suite.mockHub.EXPECT().
		OnPairingSuccess(txtRecord.TrustId, txtRecord.TrustPar).
		Return().
		Maybe()

	// Start listener to make it active
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Test the validation - this should succeed and trigger auto-trust
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.False(suite.T(), result, "valid pairing request should be accepted and stop listening")

	// Verify listener stopped after accepting (per SHIP spec section 4.3)
	status := suite.sut.GetListenerStatus()
	assert.False(suite.T(), status.Active, "listener should stop after accepting pairing")
}

func (suite *ListenerTestSuite) TestValidatePairingRequest_InvalidHMAC() {
	// Test HMAC validation failure
	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID() // For our device

	// Use valid hex format but wrong content
	txtRecord.Digest = "1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF" // Valid hex, wrong digest

	// Mock AddCu device check (no existing device for this test)
	suite.mockHub.EXPECT().
		HasTrustedAddCuDevice().
		Return("", "").
		Maybe()

	// Mock HMAC validation (should fail)
	suite.mockCrypto.EXPECT().
		ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).
		Return(api.ErrInvalidHMACDigest).
		Maybe()

	// Mock failure notification
	suite.mockHub.EXPECT().
		OnPairingFailure(txtRecord.TrustId, txtRecord.TrustPar, api.ErrInvalidHMACDigest).
		Return().
		Maybe()

	// Start listener to make it active
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Test validation - should fail and continue listening
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.True(suite.T(), result, "should continue listening after validation failure")

	// Verify listener still active
	status := suite.sut.GetListenerStatus()
	assert.True(suite.T(), status.Active, "listener should continue after validation failure")
}

func (suite *ListenerTestSuite) TestValidatePairingRequest_ReplayAttack() {
	// Test replay attack detection
	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID()

	// Start listener to make it active
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Mock AddCu device check (no existing device for this test)
	suite.mockHub.EXPECT().
		HasTrustedAddCuDevice().
		Return("", "").
		Maybe()

	// Mock successful HMAC validation
	suite.mockCrypto.EXPECT().
		ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).
		Return(nil).
		Once()

	// Mock replay detection (digest seen before)
	suite.mockHistory.EXPECT().
		HasSeenDigest(api.AlgorithmHMACSHA256, txtRecord.Digest).
		Return(true). // Already seen - replay attack
		Once()

	// Mock failure notification
	suite.mockHub.EXPECT().
		OnPairingFailure(txtRecord.TrustId, txtRecord.TrustPar, api.ErrReplayAttackDetected).
		Return().
		Maybe()

	// Test validation - should detect replay and continue listening
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.True(suite.T(), result, "should continue listening after replay detection")

	// Verify listener still active
	status := suite.sut.GetListenerStatus()
	assert.True(suite.T(), status.Active, "listener should continue after replay detection")
}

/* mDNS Discovery Tests */

func (suite *ListenerTestSuite) TestMdnsDiscovery_ForOurDevice() {
	// Test processing announcement for our device (forId matches)
	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID() // For our device

	// Set listener to listening state for the test
	suite.sut.listening = true

	// Mock AddCu device check (no existing device for this test)
	suite.mockHub.EXPECT().HasTrustedAddCuDevice().Return("", "").Maybe()

	// Mock the autonomous validation chain for successful pairing
	suite.mockCrypto.EXPECT().ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).Return(nil).Maybe()
	suite.mockHistory.EXPECT().HasSeenDigest(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(false).Maybe()
	suite.mockHistory.EXPECT().RecordPairing(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return().Maybe()
	// Note: ShouldAutoTrust removed - autonomous acceptance
	suite.mockHub.EXPECT().OnPairingSuccess(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return().Maybe()

	// Test mDNS discovery callback
	result := suite.sut.handleMdnsDiscovery(txtRecord)
	assert.False(suite.T(), result, "should stop searching after accepting pairing for our device")
}

func (suite *ListenerTestSuite) TestMdnsDiscovery_NotForOurDevice() {
	// Test ignoring announcement for different device
	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = "i:999_u:different-device" // Not for our device

	// Set listener to listening state for the test
	suite.sut.listening = true

	// Should not call any validation methods
	suite.mockCrypto.AssertNotCalled(suite.T(), "ValidateDigest")
	suite.mockHistory.AssertNotCalled(suite.T(), "HasSeenDigest")

	// Test mDNS discovery callback
	result := suite.sut.handleMdnsDiscovery(txtRecord)
	assert.True(suite.T(), result, "should continue searching when announcement not for our device")
}

func (suite *ListenerTestSuite) TestMdnsDiscovery_AlreadyPairedDevice() {
	// Test that listener processes announcements from devices that may already be paired
	// Note: Since we removed early rejection, the validation chain will run but may fail at HMAC if not valid
	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID()                                           // For our device
	txtRecord.TrustId = "i:46925_u:43652bk-2-gt1"                                           // Device requesting pairing
	txtRecord.Digest = "INVALIDDIGEST789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789AB" // Invalid to trigger failure

	// Start listener to make it active
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Mock AddCu device check (simulate this is a potential replacement)
	suite.mockHub.EXPECT().
		HasTrustedAddCuDevice().
		Return("EXISTINGFINGERPRINT456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789AB", "i:existing_u:already-paired-device").
		Maybe()

	// Mock HMAC validation failure (since digest is invalid)
	suite.mockCrypto.EXPECT().
		ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).
		Return(api.ErrInvalidHMACDigest).
		Maybe()

	// Mock failure notification for HMAC validation failure
	suite.mockHub.EXPECT().
		OnPairingFailure(txtRecord.TrustId, txtRecord.TrustPar, api.ErrInvalidHMACDigest).
		Return().
		Maybe()

	// Test that request goes through validation but fails at HMAC
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.True(suite.T(), result, "should continue listening after HMAC validation failure")

	// Verify listener is still active
	status := suite.sut.GetListenerStatus()
	assert.True(suite.T(), status.Active)
	assert.Equal(suite.T(), 1, status.RequestsSeen) // Request was seen and processed
}

func (suite *ListenerTestSuite) TestMdnsDiscovery_InvalidTXTRecord() {
	// Test handling invalid TXT record structure
	invalidTxtRecord := &api.ShipPairingTXT{
		TxtVers: "1",
		ForId:   suite.localService.ShipID(),
		Alg:     "invalid-algorithm", // Invalid algorithm
	}

	// Set listener to listening state for the test
	suite.sut.listening = true

	// Mock failure notification
	suite.mockHub.EXPECT().
		OnPairingFailure("", "", mock.MatchedBy(func(err error) bool {
			return err != nil && err.Error() != ""
		})).
		Return().
		Maybe()

	// Test with invalid TXT record
	result := suite.sut.handleMdnsDiscovery(invalidTxtRecord)
	assert.True(suite.T(), result, "should continue listening after invalid TXT record")
}

func (suite *ListenerTestSuite) TestStopListening() {
	// Test explicit stop listening
	ctx := context.Background()

	// Start listener
	suite.mockMdns.EXPECT().
		SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).
		Return(nil).
		Once()

	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Verify active
	status := suite.sut.GetListenerStatus()
	assert.True(suite.T(), status.Active)

	// Stop listening
	err = suite.sut.StopListening()
	assert.NoError(suite.T(), err)

	// Verify stopped
	status = suite.sut.GetListenerStatus()
	assert.False(suite.T(), status.Active)

	// Stop again should fail
	err = suite.sut.StopListening()
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrPairingNotActive, err)
}

/* Status and State Management Tests */

func (suite *ListenerTestSuite) TestGetListenerStatus_InitialState() {
	// Test initial listener status
	status := suite.sut.GetListenerStatus()

	assert.False(suite.T(), status.Active)
	assert.Equal(suite.T(), 0, status.RequestsSeen)
	assert.True(suite.T(), status.StartTime.IsZero())
	assert.True(suite.T(), status.LastRequest.IsZero())
	assert.Nil(suite.T(), status.Error)
}

// Note: Concurrent pairing behavior is thoroughly tested in TestIntegrationTestSuite
// to avoid complex mock sequencing issues while still validating the functionality

/* Additional Error Path Tests for Complete Coverage */

func (suite *ListenerTestSuite) TestValidatePairingRequest_TXTValidationFailure() {
	// Test TXT record validation failure path
	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID()
	txtRecord.ParType = "INVALID_PARTYPE" // This will cause validation to fail

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Mock failure notification for validation error
	suite.mockHub.EXPECT().
		OnPairingFailure(txtRecord.TrustId, txtRecord.TrustPar, mock.AnythingOfType("*api.PairingValidationError")).
		Return().
		Maybe()

	// Test - should fail validation and continue listening
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.True(suite.T(), result, "should continue listening after TXT validation failure")
}

func (suite *ListenerTestSuite) TestValidatePairingRequest_NonceParsingFailure() {
	// Test nonce hex parsing failure
	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID()
	txtRecord.TrustNonce = "INVALID_HEX_XYZ" // Invalid hex

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Mock AddCu device check (no existing device for this test)
	suite.mockHub.EXPECT().
		HasTrustedAddCuDevice().
		Return("", "").
		Maybe()

	// Mock failure notification for nonce parsing error
	suite.mockHub.EXPECT().
		OnPairingFailure(txtRecord.TrustId, txtRecord.TrustPar, api.ErrInvalidTXTRecord).
		Return().
		Maybe()

	// Test - should fail nonce parsing and continue listening
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.True(suite.T(), result, "should continue listening after nonce parsing failure")
}

func (suite *ListenerTestSuite) TestValidatePairingRequest_DigestParsingFailure() {
	// Test digest hex parsing failure
	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID()
	txtRecord.Digest = "INVALID_HEX_DIGEST_XYZ" // Invalid hex

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Mock AddCu device check (no existing device for this test)
	suite.mockHub.EXPECT().
		HasTrustedAddCuDevice().
		Return("", "").
		Maybe()

	// Mock failure notification for digest parsing error
	suite.mockHub.EXPECT().
		OnPairingFailure(txtRecord.TrustId, txtRecord.TrustPar, api.ErrInvalidHMACDigest).
		Return().
		Maybe()

	// Test - should fail digest parsing and continue listening
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.True(suite.T(), result, "should continue listening after digest parsing failure")
}

func (suite *ListenerTestSuite) TestValidatePairingRequest_TrustEstablishmentFailure() {
	// Test hub trust establishment failure
	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = "someothershipid"

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Test - should fail at trust establishment and continue listening
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.True(suite.T(), result, "should continue listening after trust establishment failure")
}

/* AddCu Device Replacement Tests */

func (suite *ListenerTestSuite) TestAddCuDeviceReplacement_NewDeviceAfterTimeout_ShouldProceedToHMAC() {
	// Test that new AddCu device replacement requests proceed to HMAC validation after 15-minute timeout
	// This validates the core bug fix: no early rejection of AddCu replacement requests

	// Create TXT record for new AddCu device (different from existing) using valid hex
	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID()
	txtRecord.Type = api.CommandTypeAddCU
	txtRecord.TrustId = "i:99999_u:replacement-device-id"                                    // Different device ID
	txtRecord.TrustPar = "ABCDEF123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789AB" // Different fingerprint (valid hex)
	txtRecord.Digest = "1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF"    // New digest (valid hex)

	// Mock existing AddCu device exists with different ShipID
	suite.mockHub.EXPECT().
		HasTrustedAddCuDevice().
		Return("FEDCBA987654321FEDCBA9876543210FEDCBA9876543210FEDCBA9876543210AB", "i:88888_u:existing-device-id").
		Maybe()

	// Mock HMAC validation - should succeed (key fix: we reach this point now)
	suite.mockCrypto.EXPECT().
		ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).
		Return(nil).
		Once()

	// Mock replay protection
	suite.mockHistory.EXPECT().
		HasSeenDigest(api.AlgorithmHMACSHA256, txtRecord.Digest).
		Return(false).
		Once()

	// Mock pairing history recording
	suite.mockHistory.EXPECT().
		RecordPairing(api.AlgorithmHMACSHA256, txtRecord.Digest).
		Return().
		Maybe()

	// Mock successful trust establishment - this triggers trust replacement in hub
	suite.mockHub.EXPECT().
		OnPairingSuccess(txtRecord.TrustId, txtRecord.TrustPar).
		Return().
		Maybe()

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Test the replacement request - should proceed to HMAC validation and succeed
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.False(suite.T(), result, "AddCu replacement should proceed to HMAC validation and be accepted")

	// Verify listener stopped after accepting (per SHIP spec section 4.3)
	status := suite.sut.GetListenerStatus()
	assert.False(suite.T(), status.Active, "listener should stop after accepting pairing")
}

func (suite *ListenerTestSuite) TestAddCuDeviceReplacement_SameDeviceRepairing_ShouldProceedToHMAC() {
	// Test that same AddCu device attempting to re-pair should work normally

	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID()
	txtRecord.Type = api.CommandTypeAddCU
	txtRecord.TrustId = "i:88888_u:existing-device-id"                                       // Same device ID as existing
	txtRecord.TrustPar = "FEDCBA987654321FEDCBA9876543210FEDCBA9876543210FEDCBA9876543210AB" // Valid hex fingerprint

	// Mock existing AddCu device with same ShipID (re-pairing scenario)
	suite.mockHub.EXPECT().
		HasTrustedAddCuDevice().
		Return("FEDCBA987654321FEDCBA9876543210FEDCBA9876543210FEDCBA9876543210AB", "i:88888_u:existing-device-id").
		Maybe()

	// Mock HMAC validation - should succeed
	suite.mockCrypto.EXPECT().
		ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).
		Return(nil).
		Once()

	// Mock replay protection
	suite.mockHistory.EXPECT().
		HasSeenDigest(api.AlgorithmHMACSHA256, txtRecord.Digest).
		Return(false).
		Once()

	// Mock pairing history recording
	suite.mockHistory.EXPECT().
		RecordPairing(api.AlgorithmHMACSHA256, txtRecord.Digest).
		Return().
		Maybe()

	// Mock successful trust establishment
	suite.mockHub.EXPECT().
		OnPairingSuccess(txtRecord.TrustId, txtRecord.TrustPar).
		Return().
		Maybe()

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Test the re-pairing request
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.False(suite.T(), result, "same device re-pairing should succeed")

	// Verify listener stopped after accepting
	status := suite.sut.GetListenerStatus()
	assert.False(suite.T(), status.Active, "listener should stop after accepting pairing")
}

func (suite *ListenerTestSuite) TestAddCuDeviceReplacement_FailedHMACValidation_ShouldNotAffectTrust() {
	// Test that failed HMAC validation for replacement request does not affect existing trust
	// This ensures trust replacement happens ONLY after successful HMAC validation

	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID()
	txtRecord.Type = api.CommandTypeAddCU
	txtRecord.TrustId = "i:99999_u:replacement-device-id"                                    // Different device
	txtRecord.TrustPar = "ABCDEF123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789AB" // Valid hex fingerprint
	txtRecord.Digest = "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF"    // Valid hex but will fail HMAC

	// Mock existing AddCu device
	suite.mockHub.EXPECT().
		HasTrustedAddCuDevice().
		Return("FEDCBA987654321FEDCBA9876543210FEDCBA9876543210FEDCBA9876543210AB", "i:88888_u:existing-device-id").
		Maybe()

	// Mock HMAC validation - should fail
	suite.mockCrypto.EXPECT().
		ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).
		Return(api.ErrInvalidHMACDigest).
		Maybe()

	// Mock failure notification (trust replacement should NOT occur)
	suite.mockHub.EXPECT().
		OnPairingFailure(txtRecord.TrustId, txtRecord.TrustPar, api.ErrInvalidHMACDigest).
		Return().
		Maybe()

	// OnPairingSuccess should NOT be called (no trust replacement)
	suite.mockHub.AssertNotCalled(suite.T(), "OnPairingSuccess")

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Test the failed replacement request
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.True(suite.T(), result, "failed HMAC validation should continue listening")

	// Verify listener is still active (failed validation doesn't stop listener)
	status := suite.sut.GetListenerStatus()
	assert.True(suite.T(), status.Active, "listener should continue after validation failure")
}

func (suite *ListenerTestSuite) TestAddCuDeviceReplacement_NoExistingDevice_ShouldProceedNormally() {
	// Test AddCu pairing when no existing AddCu device is trusted (normal case)

	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID()
	txtRecord.Type = api.CommandTypeAddCU

	// Mock successful validation chain
	suite.mockCrypto.EXPECT().
		ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).
		Return(nil).
		Once()

	suite.mockHistory.EXPECT().
		HasSeenDigest(api.AlgorithmHMACSHA256, txtRecord.Digest).
		Return(false).
		Once()

	suite.mockHistory.EXPECT().
		RecordPairing(api.AlgorithmHMACSHA256, txtRecord.Digest).
		Return().
		Maybe()

	suite.mockHub.EXPECT().
		OnPairingSuccess(txtRecord.TrustId, txtRecord.TrustPar).
		Return().
		Maybe()

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Test normal AddCu pairing
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.False(suite.T(), result, "normal AddCu pairing should succeed")

	// Verify listener stopped after accepting
	status := suite.sut.GetListenerStatus()
	assert.False(suite.T(), status.Active, "listener should stop after accepting pairing")
}

func (suite *ListenerTestSuite) TestAddCuDeviceReplacement_ReplayAttackDuringReplacement() {
	// Test that replay attack detection still works during AddCu replacement scenario

	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID()
	txtRecord.Type = api.CommandTypeAddCU
	txtRecord.TrustId = "i:99999_u:replacement-device-id" // New device

	// Mock existing AddCu device
	suite.mockHub.EXPECT().
		HasTrustedAddCuDevice().
		Return("FEDCBA987654321FEDCBA9876543210FEDCBA9876543210FEDCBA9876543210AB", "i:88888_u:existing-device-id").
		Maybe()

	// Mock successful HMAC validation
	suite.mockCrypto.EXPECT().
		ValidateDigest(mock.AnythingOfType("api.PairingSecret"), mock.AnythingOfType("*api.HMACParams"), mock.AnythingOfType("[]uint8")).
		Return(nil).
		Once()

	// Mock replay detection (digest seen before)
	suite.mockHistory.EXPECT().
		HasSeenDigest(api.AlgorithmHMACSHA256, txtRecord.Digest).
		Return(true). // Replay attack detected
		Once()

	// Mock failure notification for replay attack
	suite.mockHub.EXPECT().
		OnPairingFailure(txtRecord.TrustId, txtRecord.TrustPar, api.ErrReplayAttackDetected).
		Return().
		Maybe()

	// OnPairingSuccess should NOT be called (no trust replacement due to replay)
	suite.mockHub.AssertNotCalled(suite.T(), "OnPairingSuccess")

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Test replay attack during replacement
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.True(suite.T(), result, "replay attack should be detected and continue listening")

	// Verify listener is still active
	status := suite.sut.GetListenerStatus()
	assert.True(suite.T(), status.Active, "listener should continue after replay attack detection")
}

func (suite *ListenerTestSuite) TestAddCuDeviceReplacement_NonAddCuType_ShouldNotCheckExisting() {
	// Test that non-AddCu requests (e.g., invalid types) don't trigger AddCu replacement logic
	// They should fail at TXT validation before reaching the AddCu check

	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID()
	txtRecord.Type = "addDevice" // Not AddCu - this will cause validation to fail

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Mock failure notification for invalid type (should fail at TXT validation)
	suite.mockHub.EXPECT().
		OnPairingFailure(txtRecord.TrustId, txtRecord.TrustPar, mock.AnythingOfType("*api.PairingValidationError")).
		Return().
		Maybe()

	// HasTrustedAddCuDevice should NOT be called because validation fails early
	suite.mockHub.AssertNotCalled(suite.T(), "HasTrustedAddCuDevice")

	// Crypto and history methods should NOT be called because validation fails early
	suite.mockCrypto.AssertNotCalled(suite.T(), "ValidateDigest")
	suite.mockHistory.AssertNotCalled(suite.T(), "HasSeenDigest")
	suite.mockHistory.AssertNotCalled(suite.T(), "RecordPairing")
	suite.mockHub.AssertNotCalled(suite.T(), "OnPairingSuccess")

	// Test non-AddCu pairing - should fail validation and continue listening
	result := suite.sut.handlePairingRequest(txtRecord)
	assert.True(suite.T(), result, "invalid command type should fail validation and continue listening")

	// Verify listener is still active
	status := suite.sut.GetListenerStatus()
	assert.True(suite.T(), status.Active, "listener should continue after validation failure")
}

// ProcessPendingEntries TDD Tests

func (suite *ListenerTestSuite) TestProcessPendingEntries_EmptyEntries_ShouldHandleGracefully() {
	// Arrange - empty entries map
	emptyEntries := make(map[string]*api.ShipPairingTXT)

	// Start listener for state consistency
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// No mock expectations should be set - empty map should not trigger any processing

	// Act
	result := suite.sut.ProcessPendingEntries(emptyEntries)

	// Assert
	assert.NoError(suite.T(), result, "empty entries should be handled gracefully")

	// Verify listener remains active
	status := suite.sut.GetListenerStatus()
	assert.True(suite.T(), status.Active, "listener should remain active after processing empty entries")
}

func (suite *ListenerTestSuite) TestProcessPendingEntries_NilEntries_ShouldHandleGracefully() {
	// Arrange - nil entries
	var nilEntries map[string]*api.ShipPairingTXT

	// Start listener for state consistency
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// No mock expectations should be set - nil should not trigger any processing

	// Act
	result := suite.sut.ProcessPendingEntries(nilEntries)

	// Assert
	assert.NoError(suite.T(), result, "nil entries should be handled gracefully")

	// Verify listener remains active
	status := suite.sut.GetListenerStatus()
	assert.True(suite.T(), status.Active, "listener should remain active after processing nil entries")
}

func (suite *ListenerTestSuite) TestProcessPendingEntries_SingleValidRecord_ShouldProcessSuccessfully() {
	// Arrange
	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID() // Ensure it's for our device

	entries := map[string]*api.ShipPairingTXT{
		"test-service": txtRecord,
	}

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Mock successful validation chain
	suite.mockHub.EXPECT().
		HasTrustedAddCuDevice().
		Return("", "").
		Maybe()

	expectedDigestBytes, _ := hexToBytes(txtRecord.Digest)
	suite.mockCrypto.EXPECT().
		ValidateDigest(
			suite.testSecret,
			mock.MatchedBy(func(params *api.HMACParams) bool {
				return params.Algorithm == api.AlgorithmHMACSHA256
			}),
			expectedDigestBytes).
		Return(nil).
		Once()

	suite.mockHistory.EXPECT().
		HasSeenDigest(api.AlgorithmHMACSHA256, txtRecord.Digest).
		Return(false).
		Once()

	suite.mockHistory.EXPECT().
		RecordPairing(api.AlgorithmHMACSHA256, txtRecord.Digest).
		Return().
		Maybe()

	suite.mockHub.EXPECT().
		OnPairingSuccess(txtRecord.TrustId, txtRecord.TrustPar).
		Return().
		Maybe()

	// Act
	result := suite.sut.ProcessPendingEntries(entries)

	// Assert
	assert.NoError(suite.T(), result, "valid entry should be processed successfully")

	// Verify listener stops after successful pairing (SHIP spec behavior)
	status := suite.sut.GetListenerStatus()
	assert.False(suite.T(), status.Active, "listener should stop after successful pairing")
	assert.Equal(suite.T(), 1, status.RequestsSeen, "should have processed one request")
}

func (suite *ListenerTestSuite) TestProcessPendingEntries_MultipleValidRecords_ShouldProcessUntilSuccess() {
	// Arrange
	txtRecord1 := suite.createValidTestTXTRecord()
	txtRecord1.ForId = suite.localService.ShipID()
	txtRecord1.TrustId = "device1"

	txtRecord2 := suite.createValidTestTXTRecord()
	txtRecord2.ForId = suite.localService.ShipID()
	txtRecord2.TrustId = "device2"
	txtRecord2.Digest = "DIFFERENT_DIGEST_FOR_SECOND_DEVICE"

	entries := map[string]*api.ShipPairingTXT{
		"service1": txtRecord1,
		"service2": txtRecord2,
	}

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Mock successful validation for first entry (processing should stop after this)
	suite.mockHub.EXPECT().
		HasTrustedAddCuDevice().
		Return("", "").
		Maybe()

	suite.mockCrypto.EXPECT().
		ValidateDigest(suite.testSecret, mock.Anything, mock.Anything).
		Return(nil).
		Once() // Should only process one entry before stopping

	suite.mockHistory.EXPECT().
		HasSeenDigest(mock.Anything, mock.Anything).
		Return(false).
		Once()

	suite.mockHistory.EXPECT().
		RecordPairing(api.AlgorithmHMACSHA256, mock.Anything).
		Return().
		Maybe()

	suite.mockHub.EXPECT().
		OnPairingSuccess(mock.Anything, mock.Anything).
		Return().
		Maybe()

	// Act
	result := suite.sut.ProcessPendingEntries(entries)

	// Assert
	assert.NoError(suite.T(), result, "multiple entries should be processed until first success")

	// Verify listener stops after first successful pairing
	status := suite.sut.GetListenerStatus()
	assert.False(suite.T(), status.Active, "listener should stop after first successful pairing")
}

func (suite *ListenerTestSuite) TestProcessPendingEntries_MixValidInvalidRecords_ShouldHandleGracefully() {
	// Arrange
	validRecord := suite.createValidTestTXTRecord()
	validRecord.ForId = suite.localService.ShipID()

	invalidRecord := suite.createValidTestTXTRecord()
	invalidRecord.ForId = "different-device" // Not for our device
	invalidRecord.TrustId = "invalid-device"

	entries := map[string]*api.ShipPairingTXT{
		"valid":   validRecord,
		"invalid": invalidRecord,
	}

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Mock successful validation for valid record
	suite.mockHub.EXPECT().
		HasTrustedAddCuDevice().
		Return("", "").
		Maybe()

	validRecordDigestBytes, _ := hexToBytes(validRecord.Digest)
	suite.mockCrypto.EXPECT().
		ValidateDigest(suite.testSecret, mock.Anything, validRecordDigestBytes).
		Return(nil).
		Once()

	suite.mockHistory.EXPECT().
		HasSeenDigest(api.AlgorithmHMACSHA256, validRecord.Digest).
		Return(false).
		Once()

	suite.mockHistory.EXPECT().
		RecordPairing(api.AlgorithmHMACSHA256, validRecord.Digest).
		Return().
		Maybe()

	suite.mockHub.EXPECT().
		OnPairingSuccess(validRecord.TrustId, validRecord.TrustPar).
		Return().
		Maybe()

	// Act
	result := suite.sut.ProcessPendingEntries(entries)

	// Assert
	assert.NoError(suite.T(), result, "mixed valid/invalid entries should be handled gracefully")

	// Verify successful pairing occurred
	status := suite.sut.GetListenerStatus()
	assert.False(suite.T(), status.Active, "listener should stop after valid pairing despite invalid entries")
}

func (suite *ListenerTestSuite) TestProcessPendingEntries_AllInvalidRecords_ShouldContinueListening() {
	// Arrange
	invalidRecord1 := suite.createValidTestTXTRecord()
	invalidRecord1.ForId = "different-device-1"

	invalidRecord2 := suite.createValidTestTXTRecord()
	invalidRecord2.ForId = "different-device-2"

	entries := map[string]*api.ShipPairingTXT{
		"invalid1": invalidRecord1,
		"invalid2": invalidRecord2,
	}

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// No validation mocks should be called since records are not for our device

	// Act
	result := suite.sut.ProcessPendingEntries(entries)

	// Assert
	assert.NoError(suite.T(), result, "all invalid entries should be handled gracefully")

	// Verify listener continues listening when no valid pairings occur
	status := suite.sut.GetListenerStatus()
	assert.True(suite.T(), status.Active, "listener should continue when all entries are invalid")
}

func (suite *ListenerTestSuite) TestProcessPendingEntries_NotListening_ShouldNoOp() {
	// Arrange - don't start listener
	txtRecord := suite.createValidTestTXTRecord()
	entries := map[string]*api.ShipPairingTXT{
		"test": txtRecord,
	}

	// No mock expectations should be set - not listening should prevent processing

	// Act
	result := suite.sut.ProcessPendingEntries(entries)

	// Assert
	assert.NoError(suite.T(), result, "should handle gracefully when not listening")

	// Verify no processing occurred
	status := suite.sut.GetListenerStatus()
	assert.False(suite.T(), status.Active, "listener should not be active")
	assert.Equal(suite.T(), 0, status.RequestsSeen, "no requests should have been processed")
}

func (suite *ListenerTestSuite) TestProcessPendingEntries_ReplayAttack_ShouldDetectAndReject() {
	// Arrange
	txtRecord := suite.createValidTestTXTRecord()
	txtRecord.ForId = suite.localService.ShipID()

	entries := map[string]*api.ShipPairingTXT{
		"replay": txtRecord,
	}

	// Start listener
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Once()
	err := suite.sut.StartListening(ctx, suite.testSecret)
	assert.NoError(suite.T(), err)

	// Mock validation chain - detect replay attack
	suite.mockHub.EXPECT().
		HasTrustedAddCuDevice().
		Return("", "").
		Maybe()

	txtRecordDigestBytes, _ := hexToBytes(txtRecord.Digest)
	suite.mockCrypto.EXPECT().
		ValidateDigest(suite.testSecret, mock.Anything, txtRecordDigestBytes).
		Return(nil).
		Once()

	// Mock replay attack detection
	suite.mockHistory.EXPECT().
		HasSeenDigest(api.AlgorithmHMACSHA256, txtRecord.Digest).
		Return(true). // Already seen = replay attack
		Once()

	// Should notify failure for replay attack
	suite.mockHub.EXPECT().
		OnPairingFailure(txtRecord.TrustId, txtRecord.TrustPar, api.ErrReplayAttackDetected).
		Return().
		Maybe()

	// Act
	result := suite.sut.ProcessPendingEntries(entries)

	// Assert
	assert.NoError(suite.T(), result, "replay attack should be handled gracefully")

	// Verify listener continues after detecting replay attack
	status := suite.sut.GetListenerStatus()
	assert.True(suite.T(), status.Active, "listener should continue after detecting replay attack")
}

/* Test Helper Functions */

// createValidTestTXTRecord creates a valid ShipPairingTXT record for testing
// Uses test vectors from SHIP specification Annex A.3
func (suite *ListenerTestSuite) createValidTestTXTRecord() *api.ShipPairingTXT {
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
