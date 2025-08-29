package mdns

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
)

// ProviderExtensionTestSuite tests mDNS provider extensions through MdnsManager
// Uses proper mock-based testing following existing ship-go patterns
type ProviderExtensionTestSuite struct {
	suite.Suite

	// Mock provider and manager following existing patterns
	mockProvider *mocks.MdnsProviderInterface
	mockReporter *mocks.MdnsReportInterface
	manager      *MdnsManager
}

func TestProviderExtensionTestSuite(t *testing.T) {
	suite.Run(t, new(ProviderExtensionTestSuite))
}

func (suite *ProviderExtensionTestSuite) SetupTest() {
	// Setup mocks following existing mdns_test.go pattern
	suite.mockProvider = mocks.NewMdnsProviderInterface(suite.T())
	suite.mockReporter = mocks.NewMdnsReportInterface(suite.T())

	// Set up mock expectations following existing patterns
	suite.mockProvider.EXPECT().Start(mock.Anything, mock.Anything, mock.Anything).Return(true).Maybe()
	suite.mockProvider.EXPECT().Shutdown().Return().Maybe()

	// Create MdnsManager with correct parameter order
	suite.manager = NewMDNS(
		"test",                   // ski
		"brand",                  // deviceBrand
		"model",                  // deviceModel
		"EnergyManagementSystem", // deviceType
		"12345",                  // deviceSerial
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem}, // deviceCategories
		"shipid",                       // shipIdentifier
		"serviceName",                  // serviceName
		4729,                           // port
		nil,                            // ifaces
		MdnsProviderSelectionTestSetup, // providerSelection
	)

	// Set provider directly using your new cleaner method
	suite.manager.SetMdnsProvider(suite.mockProvider)
}

/* TDD Tests for Direct Provider Extension */

func (suite *ProviderExtensionTestSuite) TestMdnsManagerDualServiceSupport() {
	// Test that MdnsManager properly supports both _ship._tcp and _shippairing._tcp

	// Set up specific expectations for this test (instead of relying on .Maybe() from setup)
	// Expect _ship._tcp service call from AnnounceMdnsEntry()
	suite.mockProvider.EXPECT().AnnounceService(shipZeroConfServiceType, mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("[]string")).Return("1", nil).Once()

	// Expect _shippairing._tcp service call from AnnouncePairingService()
	suite.mockProvider.EXPECT().AnnounceService(shipPairingZeroConfServiceType, mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("[]string")).Return("1", nil).Once()

	// Test existing _ship._tcp functionality (should work as before)
	err := suite.manager.AnnounceMdnsEntry()
	assert.NoError(suite.T(), err, "AnnounceMdnsEntry should work with proper setup")

	// Test that pairing service methods exist and can be called
	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		ForId:      "test-device",
		TrustId:    "test-announcer",
		TrustCurve: api.CurveSecp256r1,
		Type:       api.CommandTypeAddCU,
		Alg:        api.AlgorithmHMACSHA256,
		Digest:     "1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF",
	}

	// With mock provider, should delegate correctly
	_, err = suite.manager.AnnouncePairingService(txtRecord)
	assert.NoError(suite.T(), err, "MdnsManager should delegate to mock provider")
}

func (suite *ProviderExtensionTestSuite) TestProviderMethodDelegation() {
	// Test that enhanced provider methods are called correctly

	// Set specific expectation for pairing service delegation - use On() to avoid conflicts
	suite.mockProvider.EXPECT().AnnounceService(shipPairingZeroConfServiceType, mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("[]string")).Return("1", nil).Once()

	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		ForId:      "test-device",
		ForPar:     "test-for-par",
		TrustId:    "test-announcer",
		TrustPar:   "test-trust-par",
		TrustCurve: api.CurveSecp256r1,
		Type:       api.CommandTypeAddCU,
		TrustNonce: "test-nonce",
		Alg:        api.AlgorithmHMACSHA256,
		Digest:     "1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF",
	}

	// Test that MdnsManager delegates to provider
	instanceID, err := suite.manager.AnnouncePairingService(txtRecord)
	if err != nil {
		suite.T().Logf("AnnouncePairingService failed: %v", err)
	}
	assert.NoError(suite.T(), err, "Should delegate to provider.AnnounceService")
	assert.NotEmpty(suite.T(), instanceID, "Should return non-empty instance ID")

	// Test unannouncement delegation - use On() to avoid conflicts
	suite.mockProvider.EXPECT().UnannounceService(mock.Anything).Return(nil).Once()
	err = suite.manager.UnannouncePairingService(instanceID)
	if err != nil {
		suite.T().Logf("UnannouncePairingService failed: %v", err)
	}
	assert.NoError(suite.T(), err, "Should delegate to provider.UnannounceService")
}

/* Backward Compatibility Tests */

func (suite *ProviderExtensionTestSuite) TestBackwardCompatibility_ExistingMethods() {
	// Test that existing _ship._tcp functionality continues working unchanged

	// Set up expectation for the AnnounceMdnsEntry call
	suite.mockProvider.EXPECT().AnnounceService(shipZeroConfServiceType, mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("[]string")).Return("1", nil).Once()

	// Test existing functionality through MdnsManager
	err := suite.manager.AnnounceMdnsEntry()
	assert.NoError(suite.T(), err, "AnnounceMdnsEntry should work with proper setup")

	// Set up expectation for the UnannounceMdnsEntry call
	suite.mockProvider.EXPECT().UnannounceService(mock.Anything).Return(nil).Once()

	// Test cleanup
	suite.manager.UnannounceMdnsEntry()
	// Should not crash - validates backward compatibility
}

func (suite *ProviderExtensionTestSuite) TestInterfaceCompliance() {
	// Test that MdnsManager implements both interfaces

	var _ api.MdnsInterface = suite.manager
	var _ api.MdnsPairingInterface = suite.manager

	// This test passing proves interface compliance is maintained
	assert.NotNil(suite.T(), suite.manager)
}
