package mdns

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
)

// MdnsPairingExtensionTestSuite tests direct extension of MdnsManager for pairing service support
// Uses TDD methodology to extend existing MdnsManager with _shippairing._tcp functionality
type MdnsPairingExtensionTestSuite struct {
	suite.Suite

	// Mock dependencies
	mockProvider *mocks.MdnsProviderInterface
	mockReporter *mocks.MdnsReportInterface

	// System under test - Real MdnsManager
	sut *MdnsManager
}

func TestMdnsPairingExtensionTestSuite(t *testing.T) {
	suite.Run(t, new(MdnsPairingExtensionTestSuite))
}

func (suite *MdnsPairingExtensionTestSuite) SetupTest() {
	// Setup mocks
	suite.mockProvider = mocks.NewMdnsProviderInterface(suite.T())
	suite.mockReporter = mocks.NewMdnsReportInterface(suite.T())

	// Allow basic provider operations - minimal setup, individual tests set specific expectations
	suite.mockProvider.EXPECT().Shutdown().Return().Maybe()

	// Create real MdnsManager with proper parameters (following existing test patterns)
	suite.sut = NewMDNS(
		"testski",                      // ski
		"TestBrand",                    // deviceBrand
		"TestModel",                    // deviceModel
		"TestType",                     // deviceType
		"serial123",                    // deviceSerial
		[]api.DeviceCategoryType{},     // deviceCategories
		"test-identifier",              // shipIdentifier
		"v1.0",                         // serviceName
		4712,                           // port
		[]string{},                     // ifaces (empty for no interface restrictions)
		MdnsProviderSelectionTestSetup, // providerSelection
	)
	suite.sut.SetMdnsProvider(suite.mockProvider)
	suite.sut.providerSelection = MdnsProviderSelectionTestSetup
}

/* TDD Tests for MdnsInterface Extension */

func (suite *MdnsPairingExtensionTestSuite) TestAnnouncePairingService() {
	// Test announcing _shippairing._tcp service through real MdnsManager

	// Set up mock expectation for this test - AnnounceService will be called with full service name
	suite.mockProvider.EXPECT().AnnounceService("_shippairing._tcp", "v1.0-pairing#1", 4712, mock.AnythingOfType("[]string")).Return("1", nil).Once()

	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		ForId:      "i:983327_u:C8277H008F-3",
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		TrustId:    "i:46925_u:43652bk-2-gt1",
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustCurve: api.CurveSecp256r1,
		Type:       api.CommandTypeAddCU,
		TrustNonce: "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		Alg:        api.AlgorithmHMACSHA256,
		Digest:     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}

	// Should succeed - method is now implemented
	instanceID, err := suite.sut.AnnouncePairingService(txtRecord)
	assert.NoError(suite.T(), err, "AnnouncePairingService should work with valid input")
	assert.NotEmpty(suite.T(), instanceID, "Should return non-empty instance ID")

	// Verify state tracking updated
	assert.True(suite.T(), suite.sut.IsPairingServiceAnnounced(), "State should reflect successful announcement")
}

func (suite *MdnsPairingExtensionTestSuite) TestUnannouncePairingService() {
	// Test removing _shippairing._tcp announcement

	// First announce a service to get an instance ID - need complete TXT record for validation
	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		ForId:      "i:983327_u:C8277H008F-3",
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		TrustId:    "i:46925_u:43652bk-2-gt1",
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustCurve: api.CurveSecp256r1,
		Type:       api.CommandTypeAddCU,
		TrustNonce: "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		Alg:        api.AlgorithmHMACSHA256,
		Digest:     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}

	// Set up mock expectations for announce then unannounce
	// AnnouncePairingService calls AnnounceService and returns the provider's instance ID
	suite.mockProvider.EXPECT().AnnounceService("_shippairing._tcp", "v1.0-pairing#1", 4712, mock.AnythingOfType("[]string")).Return("provider-instance-id", nil).Once()
	// UnannouncePairingService takes provider's instance ID and passes it directly to UnannounceService
	suite.mockProvider.EXPECT().UnannounceService("provider-instance-id").Return(nil).Once()

	// Announce to get instance ID
	instanceID, err := suite.sut.AnnouncePairingService(txtRecord)
	assert.NoError(suite.T(), err)
	assert.NotEmpty(suite.T(), instanceID)

	// Should succeed - method is now implemented
	err = suite.sut.UnannouncePairingService(instanceID)
	assert.NoError(suite.T(), err, "UnannouncePairingService should work when provider available")

	// Verify state tracking updated
	assert.False(suite.T(), suite.sut.IsPairingServiceAnnounced(), "State should reflect successful unannouncement")
}

func (suite *MdnsPairingExtensionTestSuite) TestSearchPairingServices() {
	// Test searching for _shippairing._tcp services

	callback := func(txt *api.ShipPairingTXT) bool {
		return true
	}

	// Should fail if manager not started
	err := suite.sut.SearchPairingServices(callback)
	assert.Error(suite.T(), err, "Should fail when manager not started")
	assert.Contains(suite.T(), err.Error(), "mDNS manager not started", "Should indicate manager needs to be started")

	// Setup provider for Start()
	suite.mockProvider.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true).Once()
	suite.mockProvider.EXPECT().AnnounceService(mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("[]string")).Return("1", nil).Once()

	// Start the manager
	err = suite.sut.Start(api.PairingModeBoth, suite.mockReporter)
	assert.NoError(suite.T(), err, "Start should succeed")

	// Now SearchPairingServices should work
	err = suite.sut.SearchPairingServices(callback)
	assert.NoError(suite.T(), err, "SearchPairingServices should succeed after Start()")
}

/* Backward Compatibility Tests */

func (suite *MdnsPairingExtensionTestSuite) TestExistingFunctionalityPreserved() {
	// Test that existing _ship._tcp functionality continues working unchanged

	// Verify MdnsManager still implements MdnsInterface
	var _ api.MdnsInterface = suite.sut

	// Verify MdnsManager now also implements MdnsPairingInterface
	var _ api.MdnsPairingInterface = suite.sut

	// Test that methods exist and have expected behavior (interface compliance)
	assert.NotNil(suite.T(), suite.sut, "MdnsManager should be created successfully")
}

func (suite *MdnsPairingExtensionTestSuite) TestMdnsInterfaceCompliance() {
	// Test that MdnsManager still implements MdnsInterface correctly

	var _ api.MdnsInterface = suite.sut

	// Verify all methods exist (compilation will fail if interface not implemented)
	assert.NotNil(suite.T(), suite.sut)
}

/* Integration Readiness Tests */

func (suite *MdnsPairingExtensionTestSuite) TestDualServiceState() {
	// Test that MdnsManager can track both _ship._tcp and _shippairing._tcp state

	// Create test pairing data
	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		ForId:      "i:983327_u:C8277H008F-3",
		TrustId:    "i:46925_u:43652bk-2-gt1",
		TrustCurve: api.CurveSecp256r1,
		Type:       api.CommandTypeAddCU,
		Alg:        api.AlgorithmHMACSHA256,
		Digest:     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}

	// Mock provider expectations for dual service
	suite.mockProvider.EXPECT().AnnounceService(shipZeroConfServiceType, "v1.0", 4712, mock.AnythingOfType("[]string")).Return("1", nil).Once()                  // _ship._tcp
	suite.mockProvider.EXPECT().AnnounceService(shipPairingZeroConfServiceType, "v1.0-pairing#1", 4712, mock.AnythingOfType("[]string")).Return("1", nil).Once() // _shippairing._tcp

	// Test 1: Announce _ship._tcp service (existing functionality)
	err := suite.sut.AnnounceMdnsEntry()
	assert.NoError(suite.T(), err, "AnnounceMdnsEntry should work with proper setup")

	// Test 2: Announce _shippairing._tcp service simultaneously
	pairingInstanceID, err := suite.sut.AnnouncePairingService(txtRecord)
	assert.NoError(suite.T(), err, "Pairing service announcement should work")
	assert.NotEmpty(suite.T(), pairingInstanceID, "Should return non-empty pairing instance ID")

	// Test 3: State independence - pairing service state should be true after announcement
	assert.True(suite.T(), suite.sut.IsPairingServiceAnnounced(), "Pairing service should be announced after successful call")

	// Test 4: Remove pairing service independently - UnannounceService expects provider instance ID
	suite.mockProvider.EXPECT().UnannounceService("1").Return(nil).Once()
	err = suite.sut.UnannouncePairingService(pairingInstanceID)
	assert.NoError(suite.T(), err, "Should remove pairing service independently")
}

func (suite *MdnsPairingExtensionTestSuite) TestProviderDelegation() {
	// Test that pairing methods properly delegate to provider.AnnounceService

	// Create test pairing data
	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		ForId:      "i:983327_u:C8277H008F-3",
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		TrustId:    "i:46925_u:43652bk-2-gt1",
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustCurve: api.CurveSecp256r1,
		Type:       api.CommandTypeAddCU,
		TrustNonce: "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		Alg:        api.AlgorithmHMACSHA256,
		Digest:     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}

	// Test 1: AnnouncePairingService calls provider.AnnounceService("_shippairing._tcp", ...)
	suite.mockProvider.EXPECT().AnnounceService(
		shipPairingZeroConfServiceType,
		mock.MatchedBy(func(serviceName string) bool {
			// Implementation generates "v1.0-pairing#1" format
			return strings.HasSuffix(serviceName, "-pairing#1") // Actual ship-go pattern includes instance ID
		}),
		mock.AnythingOfType("int"), // Port for pairing services
		mock.MatchedBy(func(txtArray []string) bool {
			// Validate TXT array format and SHIP spec compliance
			if len(txtArray) == 0 {
				return false
			}
			// Check for required fields per SHIP spec
			hasVersion := false
			hasAlgorithm := false
			for _, entry := range txtArray {
				if strings.HasPrefix(entry, "txtvers=1") {
					hasVersion = true
				}
				if strings.HasPrefix(entry, "alg=hmacSha256") {
					hasAlgorithm = true
				}
			}
			return hasVersion && hasAlgorithm
		}),
	).Return("1", nil).Once()

	delegationInstanceID, err := suite.sut.AnnouncePairingService(txtRecord)
	assert.NoError(suite.T(), err, "AnnouncePairingService should delegate correctly")
	assert.NotEmpty(suite.T(), delegationInstanceID, "Should return non-empty instance ID")

	// Test 2: UnannouncePairingService calls provider.UnannounceService with provider's instance ID
	// UnannouncePairingService takes provider instanceID "1" and passes it directly to provider
	suite.mockProvider.EXPECT().UnannounceService("1").Return(nil).Once()

	err = suite.sut.UnannouncePairingService(delegationInstanceID)
	assert.NoError(suite.T(), err, "UnannouncePairingService should delegate correctly")
}
