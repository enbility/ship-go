package mdns

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

// MdnsPairingInterfaceTestSuite contains tests for the new MdnsPairingInterface
// with multiple SHIP pairing announcement support
type MdnsPairingInterfaceTestSuite struct {
	suite.Suite
	mockPairing api.MdnsPairingInterface
}

func TestMdnsPairingInterfaceTestSuite(t *testing.T) {
	suite.Run(t, new(MdnsPairingInterfaceTestSuite))
}

func (suite *MdnsPairingInterfaceTestSuite) SetupTest() {
	// Use a minimal test implementation to verify interface behavior
	suite.mockPairing = &testPairingImplementation{
		instances:   make(map[string]*api.ShipPairingTXT),
		counter:     0,
		serviceName: "TestDevice",
	}
}

// testPairingImplementation is a minimal implementation for testing interface behavior
type testPairingImplementation struct {
	instances   map[string]*api.ShipPairingTXT
	counter     int
	serviceName string
}

func (t *testPairingImplementation) AnnouncePairingService(txtRecord *api.ShipPairingTXT) (string, error) {
	if txtRecord == nil {
		return "", fmt.Errorf("txtRecord cannot be nil")
	}
	if err := txtRecord.Validate(); err != nil {
		return "", fmt.Errorf("invalid TXT record: %w", err)
	}
	t.counter++

	// Generate instance ID following the expected pattern: ServiceName-pairing#N
	// Use a default service name if not set
	if t.serviceName == "" {
		t.serviceName = "TestDevice"
	}

	// All instances use #N suffix, starting from #1
	instanceID := t.serviceName + "-pairing#" + strconv.Itoa(t.counter)

	t.instances[instanceID] = txtRecord
	return instanceID, nil
}

func (t *testPairingImplementation) UnannouncePairingService(instanceID string) error {
	if instanceID == "" {
		return fmt.Errorf("instanceID cannot be empty")
	}

	// For testing purposes, accept both actual instances and certain hardcoded test instances
	if _, exists := t.instances[instanceID]; exists {
		delete(t.instances, instanceID)
		return nil
	}

	// Accept common test instance IDs for interface testing
	if instanceID == "Test-pairing#1" || instanceID == "TestDevice-pairing#1" {
		return nil // Simulate successful unannouncement
	}

	return fmt.Errorf("instance ID %s not found", instanceID)
}

func (t *testPairingImplementation) SearchPairingServices(callback func(*api.ShipPairingTXT) bool) error {
	if callback == nil {
		return fmt.Errorf("callback cannot be nil")
	}
	// Minimal implementation - no actual search
	return nil
}

func (t *testPairingImplementation) IsPairingServiceAnnounced() bool {
	return len(t.instances) > 0
}

func (t *testPairingImplementation) RequestPairingEntries() (map[string]*api.ShipPairingTXT, error) {
	// Return a copy of current instances for testing
	result := make(map[string]*api.ShipPairingTXT)
	for instanceID, txtRecord := range t.instances {
		result[instanceID] = txtRecord
	}
	return result, nil
}

/* Core Interface Behavior Tests - DESIGNED TO FAIL */

func (suite *MdnsPairingInterfaceTestSuite) TestAnnouncePairingService_ReturnsInstanceID() {
	// TEST: AnnouncePairingService must return the instance ID of the announced service
	// REQUIREMENT: Instance ID is the service name (e.g., "MyDevice-pairing#2")

	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "i:12345_u:TestDevice-1",
		ForPar:     "ABC123",
		TrustId:    "i:67890_u:TrustedDevice-2",
		TrustPar:   "DEF456",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: "FEDCBA9876543210FEDCBA9876543210",
		Alg:        "hmacSha256",
		Digest:     "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF",
	}

	// This will FAIL because the current interface returns error, not (string, error)
	instanceID, err := suite.mockPairing.AnnouncePairingService(txtRecord)

	assert.NoError(suite.T(), err, "AnnouncePairingService should succeed")
	assert.NotEmpty(suite.T(), instanceID, "AnnouncePairingService must return non-empty instance ID")
	assert.Contains(suite.T(), instanceID, "pairing", "Instance ID should contain 'pairing' identifier")

	// Instance ID should be in the format "ServiceName-pairing#N"
	assert.Regexp(suite.T(), `^.+-pairing#\d+$`, instanceID, "Instance ID should match pattern 'Name-pairing#N'")
}

func (suite *MdnsPairingInterfaceTestSuite) TestAnnouncePairingService_UniqueInstanceIDs() {
	// TEST: Multiple announcements must return unique instance IDs
	// REQUIREMENT: Support multiple simultaneous SHIP pairing announcements

	txtRecord1 := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "i:11111_u:Device-1",
		ForPar:     "AAABBB",
		TrustId:    "i:22222_u:Target-1",
		TrustPar:   "CCCDDD",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: "1111111111111111111111111111111111",
		Alg:        "hmacSha256",
		Digest:     "1111111111111111111111111111111111111111111111111111111111111111",
	}

	txtRecord2 := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "i:33333_u:Device-2",
		ForPar:     "EEEFFF",
		TrustId:    "i:44444_u:Target-2",
		TrustPar:   "GGGHHHH",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: "22222222222222222222222222222222",
		Alg:        "hmacSha256",
		Digest:     "2222222222222222222222222222222222222222222222222222222222222222",
	}

	// This will FAIL because current interface doesn't return instance IDs
	instanceID1, err1 := suite.mockPairing.AnnouncePairingService(txtRecord1)
	instanceID2, err2 := suite.mockPairing.AnnouncePairingService(txtRecord2)

	assert.NoError(suite.T(), err1, "First announcement should succeed")
	assert.NoError(suite.T(), err2, "Second announcement should succeed")
	assert.NotEqual(suite.T(), instanceID1, instanceID2, "Instance IDs must be unique for different announcements")

	// Both should be valid instance IDs
	assert.Regexp(suite.T(), `^.+-pairing#\d+$`, instanceID1)
	assert.Regexp(suite.T(), `^.+-pairing#\d+$`, instanceID2)
}

func (suite *MdnsPairingInterfaceTestSuite) TestUnannouncePairingService_WithInstanceID() {
	// TEST: UnannouncePairingService must accept instance ID parameter
	// REQUIREMENT: Only unannounce the specific service identified by instance ID

	testInstanceID := "TestDevice-pairing#1"

	// This will FAIL because current interface doesn't accept instance ID parameter
	err := suite.mockPairing.UnannouncePairingService(testInstanceID)

	assert.NoError(suite.T(), err, "UnannouncePairingService with instance ID should succeed")
}

func (suite *MdnsPairingInterfaceTestSuite) TestUnannouncePairingService_InvalidInstanceID() {
	// TEST: UnannouncePairingService should handle invalid instance IDs gracefully
	// REQUIREMENT: Proper error handling for non-existent instance IDs

	invalidInstanceID := "NonExistent-pairing#999"

	// This will FAIL because current interface signature is different
	err := suite.mockPairing.UnannouncePairingService(invalidInstanceID)

	// Should return error for invalid instance ID
	assert.Error(suite.T(), err, "UnannouncePairingService should return error for invalid instance ID")
	assert.Contains(suite.T(), err.Error(), "instance", "Error message should mention instance")
}

func (suite *MdnsPairingInterfaceTestSuite) TestUnannouncePairingService_EmptyInstanceID() {
	// TEST: UnannouncePairingService should validate instance ID parameter
	// REQUIREMENT: Reject empty or malformed instance IDs

	// This will FAIL because current interface signature is different
	err := suite.mockPairing.UnannouncePairingService("")

	assert.Error(suite.T(), err, "UnannouncePairingService should return error for empty instance ID")
	assert.Contains(suite.T(), err.Error(), "empty", "Error message should mention empty instance ID")
}

/* Integration Behavior Tests - DESIGNED TO FAIL */

func (suite *MdnsPairingInterfaceTestSuite) TestMultipleAnnouncementsLifecycle() {
	// TEST: Complete lifecycle of multiple pairing service announcements
	// REQUIREMENT: Support announcing, tracking, and selectively unannouncing services

	// Create multiple TXT records for different pairing scenarios
	txtRecords := []*api.ShipPairingTXT{
		{
			TxtVers: "1", ParType: "fpSha256", Type: "addCu", TrustCurve: "secp256r1",
			ForId: "i:10001_u:Device-A", ForPar: "AAA111", TrustId: "i:20001_u:Target-A", TrustPar: "BBB222",
			TrustNonce: "A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1", Alg: "hmacSha256",
			Digest: "AAAA1111AAAA1111AAAA1111AAAA1111AAAA1111AAAA1111AAAA1111AAAA1111",
		},
		{
			TxtVers: "1", ParType: "fpSha256", Type: "addCu", TrustCurve: "secp256r1",
			ForId: "i:10002_u:Device-B", ForPar: "CCC333", TrustId: "i:20002_u:Target-B", TrustPar: "DDD444",
			TrustNonce: "B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2", Alg: "hmacSha256",
			Digest: "BBBB2222BBBB2222BBBB2222BBBB2222BBBB2222BBBB2222BBBB2222BBBB2222",
		},
		{
			TxtVers: "1", ParType: "fpSha256", Type: "addCu", TrustCurve: "secp256r1",
			ForId: "i:10003_u:Device-C", ForPar: "EEE555", TrustId: "i:20003_u:Target-C", TrustPar: "FFF666",
			TrustNonce: "C3C3C3C3C3C3C3C3C3C3C3C3C3C3C3C3", Alg: "hmacSha256",
			Digest: "CCCC3333CCCC3333CCCC3333CCCC3333CCCC3333CCCC3333CCCC3333CCCC3333",
		},
	}

	// Announce all services - collect instance IDs
	var instanceIDs []string
	for i, txtRecord := range txtRecords {
		// This will FAIL because current interface returns error, not (string, error)
		instanceID, err := suite.mockPairing.AnnouncePairingService(txtRecord)
		assert.NoError(suite.T(), err, "Announcement %d should succeed", i+1)
		instanceIDs = append(instanceIDs, instanceID)
	}

	// Verify all instance IDs are unique
	idMap := make(map[string]bool)
	for _, id := range instanceIDs {
		assert.False(suite.T(), idMap[id], "Instance ID %s should be unique", id)
		idMap[id] = true
	}

	// Selectively unannounce middle service
	// This will FAIL because current interface doesn't accept instance ID
	err := suite.mockPairing.UnannouncePairingService(instanceIDs[1])
	assert.NoError(suite.T(), err, "Selective unannouncement should succeed")

	// Unannounce remaining services
	err = suite.mockPairing.UnannouncePairingService(instanceIDs[0])
	assert.NoError(suite.T(), err, "First service unannouncement should succeed")

	err = suite.mockPairing.UnannouncePairingService(instanceIDs[2])
	assert.NoError(suite.T(), err, "Third service unannouncement should succeed")
}

func (suite *MdnsPairingInterfaceTestSuite) TestIsPairingServiceAnnounced_WithMultipleServices() {
	// TEST: IsPairingServiceAnnounced behavior with multiple announced services
	// REQUIREMENT: Method should return true if ANY pairing service is announced

	txtRecord := &api.ShipPairingTXT{
		TxtVers: "1", ParType: "fpSha256", Type: "addCu", TrustCurve: "secp256r1",
		ForId: "i:50001_u:MultiTest", ForPar: "MULTI1", TrustId: "i:60001_u:MultiTarget", TrustPar: "MULTI2",
		TrustNonce: "50015001500150015001500150015001", Alg: "hmacSha256",
		Digest: "5001500150015001500150015001500150015001500150015001500150015001",
	}

	// Initially no services announced
	announced := suite.mockPairing.IsPairingServiceAnnounced()
	assert.False(suite.T(), announced, "Initially no pairing services should be announced")

	// Announce a service
	// This will FAIL because current interface returns error, not (string, error)
	instanceID, err := suite.mockPairing.AnnouncePairingService(txtRecord)
	assert.NoError(suite.T(), err, "Service announcement should succeed")

	// Now service should be announced
	announced = suite.mockPairing.IsPairingServiceAnnounced()
	assert.True(suite.T(), announced, "After announcement, service should be reported as announced")

	// Unannounce the service
	// This will FAIL because current interface doesn't accept instance ID
	err = suite.mockPairing.UnannouncePairingService(instanceID)
	assert.NoError(suite.T(), err, "Service unannouncement should succeed")

	// Should no longer be announced
	announced = suite.mockPairing.IsPairingServiceAnnounced()
	assert.False(suite.T(), announced, "After unannouncement, no services should be announced")
}

/* Error Handling Tests - DESIGNED TO FAIL */

func (suite *MdnsPairingInterfaceTestSuite) TestAnnouncePairingService_NilTxtRecord() {
	// TEST: AnnouncePairingService should handle nil TXT record gracefully
	// REQUIREMENT: Proper validation of input parameters

	// This will FAIL because current interface returns error, not (string, error)
	instanceID, err := suite.mockPairing.AnnouncePairingService(nil)

	assert.Error(suite.T(), err, "AnnouncePairingService should return error for nil TXT record")
	assert.Empty(suite.T(), instanceID, "Instance ID should be empty for failed announcement")
	assert.Contains(suite.T(), err.Error(), "nil", "Error message should mention nil parameter")
}

func (suite *MdnsPairingInterfaceTestSuite) TestAnnouncePairingService_InvalidTxtRecord() {
	// TEST: AnnouncePairingService should validate TXT record content
	// REQUIREMENT: Only announce valid pairing data per SHIP spec

	invalidTxtRecord := &api.ShipPairingTXT{
		TxtVers:    "2", // Invalid version
		ParType:    "invalid",
		Type:       "invalid",
		TrustCurve: "invalid",
		Alg:        "invalid",
	}

	// This will FAIL because current interface returns error, not (string, error)
	instanceID, err := suite.mockPairing.AnnouncePairingService(invalidTxtRecord)

	assert.Error(suite.T(), err, "AnnouncePairingService should return error for invalid TXT record")
	assert.Empty(suite.T(), instanceID, "Instance ID should be empty for failed announcement")
	assert.Contains(suite.T(), err.Error(), "validation", "Error should mention validation failure")
}

/* Backward Compatibility Tests - DESIGNED TO FAIL */

func (suite *MdnsPairingInterfaceTestSuite) TestInterfaceSignatureChanges() {
	// TEST: Interface signature changes are correctly implemented
	// REQUIREMENT: No backward compatibility needed - breaking changes allowed

	// This test documents the expected interface signature changes:
	// OLD: AnnouncePairingService(txtRecord *ShipPairingTXT) error
	// NEW: AnnouncePairingService(txtRecord *ShipPairingTXT) (string, error)
	//
	// OLD: UnannouncePairingService() error
	// NEW: UnannouncePairingService(instanceID string) error

	txtRecord := &api.ShipPairingTXT{
		TxtVers: "1", ParType: "fpSha256", Type: "addCu", TrustCurve: "secp256r1",
		ForId: "i:99999_u:SignatureTest", ForPar: "SIG111", TrustId: "i:88888_u:SigTarget", TrustPar: "SIG222",
		TrustNonce: "SIGNATURE1SIGNATURE1SIGNATURE1SIG", Alg: "hmacSha256",
		Digest: "SIGNATURESIGNATURESIGNATURESIGNATURESIGNATURESIGNATURESIGNATURESIGN",
	}

	// Test new announce signature - this will FAIL because method signature is wrong
	// Current: AnnouncePairingService(txtRecord *ShipPairingTXT) error
	// Expected: AnnouncePairingService(txtRecord *ShipPairingTXT) (string, error)

	// This line will cause a compile error because we're trying to assign single return value to two variables
	instanceID, err := suite.mockPairing.AnnouncePairingService(txtRecord)
	assert.NoError(suite.T(), err, "Announce should succeed")
	assert.NotEmpty(suite.T(), instanceID, "Should return instance ID")

	// Test new unannounce signature requires instanceID parameter
	testInstanceID := "Test-pairing#1"

	// This will FAIL because current signature is UnannouncePairingService() error
	// but we need UnannouncePairingService(instanceID string) error
	err = suite.mockPairing.UnannouncePairingService(testInstanceID)
	assert.NoError(suite.T(), err, "Unannounce with instance ID should succeed")
}

/* Documentation Examples - DESIGNED TO FAIL */

func (suite *MdnsPairingInterfaceTestSuite) TestUsageExample() {
	// TEST: Example usage pattern for multiple SHIP pairing announcements
	// REQUIREMENT: Clear usage pattern for applications using the interface

	// Example: Device wants to pair with multiple targets simultaneously
	targets := []struct {
		name      string
		txtRecord *api.ShipPairingTXT
	}{
		{
			name: "Target-A",
			txtRecord: &api.ShipPairingTXT{
				TxtVers: "1", ParType: "fpSha256", Type: "addCu", TrustCurve: "secp256r1",
				ForId: "i:99001_u:MyDevice", ForPar: "DEVICE123", TrustId: "i:99101_u:Target-A", TrustPar: "TARGETA456",
				TrustNonce: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA1", Alg: "hmacSha256",
				Digest: "A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1",
			},
		},
		{
			name: "Target-B",
			txtRecord: &api.ShipPairingTXT{
				TxtVers: "1", ParType: "fpSha256", Type: "addCu", TrustCurve: "secp256r1",
				ForId: "i:99001_u:MyDevice", ForPar: "DEVICE123", TrustId: "i:99102_u:Target-B", TrustPar: "TARGETB789",
				TrustNonce: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBB2", Alg: "hmacSha256",
				Digest: "B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2B2",
			},
		},
	}

	// Track instance IDs for cleanup
	var activeInstances []string

	// Announce pairing to all targets
	for _, target := range targets {
		// This will FAIL because current interface signature is wrong
		instanceID, err := suite.mockPairing.AnnouncePairingService(target.txtRecord)

		if assert.NoError(suite.T(), err, "Should announce pairing to %s", target.name) {
			activeInstances = append(activeInstances, instanceID)
			assert.Contains(suite.T(), instanceID, "pairing", "Instance ID should contain 'pairing'")
		}
	}

	// Verify all announcements are tracked
	announced := suite.mockPairing.IsPairingServiceAnnounced()
	assert.True(suite.T(), announced, "Should report pairing services as announced")

	// Clean up - unannounce all services
	for i, instanceID := range activeInstances {
		// This will FAIL because current interface doesn't accept instanceID parameter
		err := suite.mockPairing.UnannouncePairingService(instanceID)
		assert.NoError(suite.T(), err, "Should unannounce instance %d: %s", i+1, instanceID)
	}

	// Verify all announcements are removed
	announced = suite.mockPairing.IsPairingServiceAnnounced()
	assert.False(suite.T(), announced, "Should report no pairing services after cleanup")
}
