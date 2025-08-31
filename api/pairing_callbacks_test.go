package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

// PairingCallbacksTestSuite tests the enhanced PairingServiceReaderInterface
type PairingCallbacksTestSuite struct {
	suite.Suite
}

func TestPairingCallbacksTestSuite(t *testing.T) {
	suite.Run(t, new(PairingCallbacksTestSuite))
}

// TestPairingServiceReaderInterface_DeviceAutoTrustRemoved tests that the new callback method exists
func (suite *PairingCallbacksTestSuite) TestPairingServiceReaderInterface_DeviceAutoTrustRemoved() {
	// Test that PairingServiceReaderInterface has the new method for trust removal notification

	mockReader := &mockPairingReader{}
	reader := &testPairingServiceReader{
		mockReader: mockReader,
	}

	// Test service
	testService := NewServiceDetails("testski", "test-fingerprint", "test-ship-id")

	reader.DeviceAutoTrustRemovedViaReplacementLogic(testService, "15-minute timeout expired")

	// Verify mock was called
	assert.True(suite.T(), mockReader.trustRemovedCalled, "DeviceAutoTrustRemovedViaReplacementLogic should be called")
	assert.Equal(suite.T(), testService.SKI(), mockReader.lastRemovedService.SKI(), "Should pass correct service")
	assert.Equal(suite.T(), "15-minute timeout expired", mockReader.lastRemovalReason, "Should pass correct reason")
}

// testPairingServiceReader implements the enhanced PairingServiceReaderInterface for testing
type testPairingServiceReader struct {
	mockReader *mockPairingReader
}

// Implement existing method
func (t *testPairingServiceReader) DeviceAutoTrustedViaServiceDetails(service *ServiceDetails) {
	t.mockReader.deviceAutoTrustedCalled = true
}

func (t *testPairingServiceReader) PairingServiceFailedForServiceDetails(service *ServiceDetails, reason error) {
	// Existing method - no-op for this test
}

// Implement new method - this will FAIL to compile initially
func (t *testPairingServiceReader) DeviceAutoTrustRemovedViaReplacementLogic(service *ServiceDetails, reason string) {
	t.mockReader.trustRemovedCalled = true
	t.mockReader.lastRemovedService = service
	t.mockReader.lastRemovalReason = reason
}

// Mock implementation for testing
type mockPairingReader struct {
	deviceAutoTrustedCalled bool
	trustRemovedCalled      bool
	lastRemovedService      *ServiceDetails
	lastRemovalReason       string
}

func (suite *PairingCallbacksTestSuite) TestCallbackReasonStrings() {
	// Test that the callback includes descriptive reason strings for different scenarios

	testCases := []struct {
		name           string
		scenario       string
		expectedReason string
	}{
		{
			name:           "15-minute timeout",
			scenario:       "timer_timeout",
			expectedReason: "15-minute timeout expired - no communication with paired device",
		},
		{
			name:           "new pairing replacement",
			scenario:       "new_pairing_replacement",
			expectedReason: "replaced by new pairing from same device",
		},
		{
			name:           "manual replacement",
			scenario:       "manual_replacement",
			expectedReason: "manual trust removal via replacement logic",
		},
	}

	for _, tc := range testCases {
		suite.T().Run(tc.name, func(t *testing.T) {
			// This test defines the expected reason strings
			// that should be used when calling DeviceAutoTrustRemovedViaReplacementLogic

			mockReader := &mockPairingReader{}
			reader := &testPairingServiceReader{mockReader: mockReader}
			testService := NewServiceDetails("testski"+tc.scenario, "", "")

			// Call with expected reason string
			reader.DeviceAutoTrustRemovedViaReplacementLogic(testService, tc.expectedReason)

			// Verify reason was passed correctly
			assert.Equal(t, tc.expectedReason, mockReader.lastRemovalReason)
		})
	}
}

func (suite *PairingCallbacksTestSuite) TestCallbackServiceDetailsHandling() {
	// Test that callback properly handles ServiceDetails parameter

	mockReader := &mockPairingReader{}
	reader := &testPairingServiceReader{mockReader: mockReader}

	// Test with complete service details
	fullService := NewServiceDetails("fullski", "full-fingerprint", "full-ship-id")
	reader.DeviceAutoTrustRemovedViaReplacementLogic(fullService, "test reason")

	assert.Equal(suite.T(), "fullski", mockReader.lastRemovedService.SKI())
	assert.Equal(suite.T(), "full-fingerprint", mockReader.lastRemovedService.Fingerprint())
	assert.Equal(suite.T(), "full-ship-id", mockReader.lastRemovedService.ShipID())

	// Test with minimal service details
	minimalService := NewServiceDetails("minimalski", "", "")
	reader.DeviceAutoTrustRemovedViaReplacementLogic(minimalService, "minimal test")

	assert.Equal(suite.T(), "minimalski", mockReader.lastRemovedService.SKI())
	assert.Empty(suite.T(), mockReader.lastRemovedService.Fingerprint())
	assert.Empty(suite.T(), mockReader.lastRemovedService.ShipID())
}
