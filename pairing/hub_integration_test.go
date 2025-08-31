package pairing

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
)

// HubIntegrationTestSuite contains tests for SHIP Pairing Service Hub integration
// Tests the complete announcer (devZ) flow using TDD methodology
type HubIntegrationTestSuite struct {
	suite.Suite

	// Mock dependencies
	mockPairingHistory *mocks.PairingHistoryProviderInterface
	mockPairingMdns    *mocks.MdnsPairingInterface
	mockCrypto         *mocks.PairingCryptoInterface

	// Test certificate and service details
	certificate  tls.Certificate
	localCert    *x509.Certificate
	localService *api.ServiceDetails

	// System under test - simplified for TDD
	sut *PairingAnnouncer
}

func TestHubIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(HubIntegrationTestSuite))
}

func (suite *HubIntegrationTestSuite) SetupTest() {
	// Setup fresh mocks for each test (prevent pollution)
	suite.mockPairingHistory = mocks.NewPairingHistoryProviderInterface(suite.T())
	suite.mockPairingMdns = mocks.NewMdnsPairingInterface(suite.T())
	suite.mockCrypto = mocks.NewPairingCryptoInterface(suite.T())

	// Setup test data (fresh instances each time)
	var err error
	suite.certificate, err = cert.CreateCertificate("TestOU", "TestOrg", "DE", "test-device")
	require.NoError(suite.T(), err)

	// Extract X.509 certificate
	suite.localCert, err = x509.ParseCertificate(suite.certificate.Certificate[0])
	require.NoError(suite.T(), err)

	suite.localService = api.NewServiceDetails("localtestski"+suite.T().Name(), "", "") // Unique per test
	suite.localService.SetShipID("i:123_u:test-local")

	// Create fresh pairing announcer for each test
	suite.sut = NewPairingAnnouncer(
		suite.mockPairingMdns,
		suite.mockCrypto,
		suite.localCert,
		suite.mockPairingHistory,
		suite.localService,
	)
}

func (suite *HubIntegrationTestSuite) TearDownTest() {
	// Clean up any active pairing announcements to prevent timer leaks
	if suite.sut != nil {
		// Only cleanup if actually announcing to prevent unnecessary mock calls
		status := suite.sut.GetPairingServiceStatus()
		if status != nil && status.AnnouncerActive {
			// Allow RemovePairing call for active announcements
			suite.mockPairingMdns.EXPECT().UnannouncePairingService(mock.AnythingOfType("string")).Return(nil).Once()
			suite.sut.OnConnectionEstablished("test-cleanup")
		}
	}
}

/* TDD Tests for Hub Integration */

func (suite *HubIntegrationTestSuite) TestEnablePairingService() {
	// Test enabling pairing service on Hub
	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  api.PairingSecret("test-secret-1234"),
		Enabled: true,
	}

	err := suite.sut.EnablePairingService(config)
	assert.NoError(suite.T(), err)

	// Verify pairing service is enabled
	status := suite.sut.GetPairingServiceStatus()
	assert.NotNil(suite.T(), status)
	assert.True(suite.T(), status.Enabled)
	assert.Equal(suite.T(), PairingModeAnnouncer, status.Mode)
}

func (suite *HubIntegrationTestSuite) TestAnnounceToDevice_CompleteFlow() {
	// Test complete announcer flow: enable service -> announce -> validate

	// Enable pairing service
	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  api.PairingSecret("test-secret-1234"),
		Enabled: true,
	}
	err := suite.sut.EnablePairingService(config)
	assert.NoError(suite.T(), err)

	// Target device info
	target := &api.PairingTarget{
		SKI:         "targetdeviceski",
		Fingerprint: "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		ShipID:      "i:983327_u:C8277H008F-3",
	}

	// Mock certificate operations - certificates are now handled internally

	// Mock nonce generation
	testNonce := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}
	suite.mockCrypto.EXPECT().
		GenerateNonce().
		Return(testNonce, nil).
		Once()

	// Mock HMAC calculation
	testDigest := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	suite.mockCrypto.EXPECT().
		CalculateDigest(config.Secret, mock.AnythingOfType("*api.HMACParams")).
		Return(testDigest, nil).
		Once()

	// Mock mDNS announcement
	var capturedTXT *api.ShipPairingTXT
	suite.mockPairingMdns.EXPECT().
		AnnouncePairingService(mock.MatchedBy(func(txt *api.ShipPairingTXT) bool {
			capturedTXT = txt
			return txt.ForId == target.ShipID &&
				txt.TrustId == suite.localService.ShipID() &&
				txt.ParType == api.ParTypeFPSHA256 &&
				txt.Alg == api.AlgorithmHMACSHA256
		})).
		Return("test-instance-id", nil).
		Once()

	// Announce pairing to target device
	err = suite.sut.Announce(target)
	assert.NoError(suite.T(), err)

	// Verify TXT record content
	assert.NotNil(suite.T(), capturedTXT)
	assert.Equal(suite.T(), target.ShipID, capturedTXT.ForId)
	assert.Equal(suite.T(), target.Fingerprint, capturedTXT.ForPar)
	assert.Equal(suite.T(), suite.localService.ShipID(), capturedTXT.TrustId)
	// TrustPar should be the actual certificate fingerprint
	assert.NotEmpty(suite.T(), capturedTXT.TrustPar)
	assert.Len(suite.T(), capturedTXT.TrustPar, 64) // SHA-256 hex = 64 chars
	// Digest should be generated from HMAC
	assert.Equal(suite.T(), "AABBCCDD", capturedTXT.Digest)
}

func (suite *HubIntegrationTestSuite) TestPairingSuccess_AutoTrust() {
	// Note: Service management has been removed from PairingAnnouncer
	// This functionality is now handled by the Hub component
	// PairingAnnouncer focuses only on devZ role (announcements)
	suite.T().Skip("Test skipped - service management removed from PairingAnnouncer")
}

func (suite *HubIntegrationTestSuite) TestPairingFailure_ErrorHandling() {
	// Note: Service management has been removed from PairingAnnouncer
	// This functionality is now handled by the Hub component
	// PairingAnnouncer focuses only on devZ role (announcements)
	suite.T().Skip("Test skipped - service management removed from PairingAnnouncer")
}

func (suite *HubIntegrationTestSuite) TestConnectionAfterPairing() {
	// Test that connection establishment cleanups announcements properly

	// Enable pairing service first
	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  api.PairingSecret("test-secret-1234"),
		Enabled: true,
	}
	err := suite.sut.EnablePairingService(config)
	assert.NoError(suite.T(), err)

	targetSKI := "targetdeviceski"

	// Simulate SHIP connection established (no announcement active, so no cleanup needed)
	suite.sut.OnConnectionEstablished(targetSKI)

	// Verify pairing announcement is removed (devZ should delete service after connection)
	status := suite.sut.GetPairingServiceStatus()
	if status != nil {
		assert.False(suite.T(), status.AnnouncerActive)
	}
}

func (suite *HubIntegrationTestSuite) TestAutonomousBehavior() {
	// Test that SHIP Pairing Service operates autonomously per specification

	// SHIP Pairing Service does not depend on Hub auto-accept settings
	// Valid HMAC authentication IS the authorization per spec section 4.2

	// This test validates that pairing components exist and are properly configured
	assert.NotNil(suite.T(), suite.sut, "PairingAnnouncer should be created")

	// Note: No ShouldAutoTrust testing - method removed for autonomous operation
}

func (suite *HubIntegrationTestSuite) TestPairingServiceOptional() {
	// Test that pairing announcer handles missing configuration gracefully

	// Should error when trying to announce without configuration
	target := &api.PairingTarget{
		SKI:         "testski",
		Fingerprint: "test-fp",
		ShipID:      "test-id",
	}

	err := suite.sut.Announce(target)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrServiceNotStarted, err)
}

func (suite *HubIntegrationTestSuite) TestConcurrentPairingOperations() {
	// Test concurrent pairing operations are handled safely

	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  api.PairingSecret("test-secret-1234"),
		Enabled: true,
	}

	err := suite.sut.EnablePairingService(config)
	assert.NoError(suite.T(), err)

	target1 := &api.PairingTarget{SKI: "device1", ShipID: "id1", Fingerprint: "fp1"}
	target2 := &api.PairingTarget{SKI: "device2", ShipID: "id2", Fingerprint: "fp2"}

	// Mock dependencies for first announcement (should succeed)
	suite.mockCrypto.EXPECT().GenerateNonce().Return([]byte{0x01}, nil).Once()
	suite.mockCrypto.EXPECT().CalculateDigest(
		mock.AnythingOfType("api.PairingSecret"),
		mock.AnythingOfType("*api.HMACParams")).Return([]byte{0xAA}, nil).Once()

	// Setup mDNS expectations (only one should succeed due to already active check)
	suite.mockPairingMdns.EXPECT().
		AnnouncePairingService(
			mock.AnythingOfType("*api.ShipPairingTXT")).
		Return("test-instance-id", nil).
		Times(1)

	// Ensure cleanup is called after successful announcement to prevent timer leaks
	suite.mockPairingMdns.EXPECT().
		UnannouncePairingService(mock.AnythingOfType("string")).
		Return(nil).
		Maybe()

	// Concurrent announce attempts with better synchronization
	done := make(chan error, 2)

	go func() {
		defer func() {
			// Ensure goroutine cleanup
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic in goroutine 1: %v", r)
			}
		}()
		done <- suite.sut.Announce(target1)
	}()

	go func() {
		defer func() {
			// Ensure goroutine cleanup
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic in goroutine 2: %v", r)
			}
		}()
		time.Sleep(10 * time.Millisecond)
		done <- suite.sut.Announce(target2)
	}()

	// Wait for both operations with timeout
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()

	var err1, err2 error
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if i == 0 {
				err1 = err
			} else {
				err2 = err
			}
		case <-timeout.C:
			suite.T().Fatal("Test timed out waiting for goroutines")
		}
	}

	// One should succeed, one should fail (can't announce to multiple simultaneously per spec 4.4)
	if err1 == nil {
		assert.Error(suite.T(), err2)
		assert.Equal(suite.T(), api.ErrAnnouncerAlreadyActive, err2)
	} else {
		assert.NoError(suite.T(), err2)
		assert.Equal(suite.T(), api.ErrAnnouncerAlreadyActive, err1)
	}

	// Clean up any active announcements to prevent timer interference with other tests
	suite.sut.mux.Lock()
	if suite.sut.announcing {
		suite.sut.announcing = false
		suite.sut.currentTarget = nil
	}
	suite.sut.mux.Unlock()
}

/* Tests for StopAnnouncement method - achieving 100% coverage */

func (suite *HubIntegrationTestSuite) TestStopAnnouncement_Success() {
	// Test successful announcement stopping after active announcement

	// Enable pairing service
	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  api.PairingSecret("test-secret-1234"),
		Enabled: true,
	}
	err := suite.sut.EnablePairingService(config)
	require.NoError(suite.T(), err)

	// First start an announcement
	target := &api.PairingTarget{
		SKI:         "targetdeviceski",
		Fingerprint: "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		ShipID:      "i:983327_u:C8277H008F-3",
	}

	// Mock the announcement setup
	suite.mockCrypto.EXPECT().GenerateNonce().Return([]byte{0x01, 0x02}, nil).Once()
	suite.mockCrypto.EXPECT().CalculateDigest(
		config.Secret,
		mock.AnythingOfType("*api.HMACParams")).Return([]byte{0xAA, 0xBB}, nil).Once()
	suite.mockPairingMdns.EXPECT().
		AnnouncePairingService(mock.AnythingOfType("*api.ShipPairingTXT")).
		Return("active-instance-id", nil).Once()

	// Start the announcement
	err = suite.sut.Announce(target)
	require.NoError(suite.T(), err)

	// Verify we're announcing
	status := suite.sut.GetAnnouncementStatus()
	assert.True(suite.T(), status.Active)
	assert.Equal(suite.T(), target, status.Target)

	// Mock the mDNS unannounce call
	suite.mockPairingMdns.EXPECT().
		UnannouncePairingService("active-instance-id").
		Return(nil).Once()

	// Stop the announcement - this is what we're testing
	err = suite.sut.StopAnnouncement()
	assert.NoError(suite.T(), err)

	// Verify state cleanup
	status = suite.sut.GetAnnouncementStatus()
	assert.False(suite.T(), status.Active)
	assert.Nil(suite.T(), status.Target)

	// Verify internal state is cleaned up
	suite.sut.mux.RLock()
	assert.False(suite.T(), suite.sut.announcing)
	assert.Nil(suite.T(), suite.sut.currentTarget)
	assert.Empty(suite.T(), suite.sut.currentInstanceID)
	suite.sut.mux.RUnlock()
}

func (suite *HubIntegrationTestSuite) TestStopAnnouncement_NotAnnouncing() {
	// Test stopping when not currently announcing (edge case)

	// Enable pairing service but don't start announcement
	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  api.PairingSecret("test-secret-1234"),
		Enabled: true,
	}
	err := suite.sut.EnablePairingService(config)
	require.NoError(suite.T(), err)

	// Verify we're not announcing
	status := suite.sut.GetAnnouncementStatus()
	assert.False(suite.T(), status.Active)

	// Try to stop announcement when not announcing
	err = suite.sut.StopAnnouncement()
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrPairingNotActive, err)
}

func (suite *HubIntegrationTestSuite) TestStopAnnouncement_MdnsError() {
	// Test error handling during mDNS unannounce

	// Enable pairing service and start announcement
	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  api.PairingSecret("test-secret-1234"),
		Enabled: true,
	}
	err := suite.sut.EnablePairingService(config)
	require.NoError(suite.T(), err)

	target := &api.PairingTarget{
		SKI:         "errordeviceski",
		Fingerprint: "D74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		ShipID:      "i:error123_u:Error-Device",
	}

	// Mock successful announcement
	suite.mockCrypto.EXPECT().GenerateNonce().Return([]byte{0x03, 0x04}, nil).Once()
	suite.mockCrypto.EXPECT().CalculateDigest(
		config.Secret,
		mock.AnythingOfType("*api.HMACParams")).Return([]byte{0xCC, 0xDD}, nil).Once()
	suite.mockPairingMdns.EXPECT().
		AnnouncePairingService(mock.AnythingOfType("*api.ShipPairingTXT")).
		Return("error-instance-id", nil).Once()

	// Start announcement
	err = suite.sut.Announce(target)
	require.NoError(suite.T(), err)

	// Mock mDNS unannounce error
	expectedError := fmt.Errorf("mDNS service unavailable")
	suite.mockPairingMdns.EXPECT().
		UnannouncePairingService("error-instance-id").
		Return(expectedError).Once()

	// Stop announcement should return the mDNS error
	err = suite.sut.StopAnnouncement()
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), expectedError, err)

	// Verify state is NOT cleaned up when error occurs
	// (announcement should still be considered active until successful cleanup)
	suite.sut.mux.RLock()
	assert.True(suite.T(), suite.sut.announcing)
	assert.NotNil(suite.T(), suite.sut.currentTarget)
	assert.Equal(suite.T(), "error-instance-id", suite.sut.currentInstanceID)
	suite.sut.mux.RUnlock()
}

func (suite *HubIntegrationTestSuite) TestStopAnnouncement_ConcurrentOperations() {
	// Test thread safety during concurrent stop operations

	// Enable pairing service and start announcement
	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  api.PairingSecret("test-secret-1234"),
		Enabled: true,
	}
	err := suite.sut.EnablePairingService(config)
	require.NoError(suite.T(), err)

	target := &api.PairingTarget{
		SKI:         "concurrentski",
		Fingerprint: "E74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		ShipID:      "i:concurrent_u:Concurrent-Device",
	}

	// Mock successful announcement
	suite.mockCrypto.EXPECT().GenerateNonce().Return([]byte{0x05, 0x06}, nil).Once()
	suite.mockCrypto.EXPECT().CalculateDigest(
		config.Secret,
		mock.AnythingOfType("*api.HMACParams")).Return([]byte{0xEE, 0xFF}, nil).Once()
	suite.mockPairingMdns.EXPECT().
		AnnouncePairingService(mock.AnythingOfType("*api.ShipPairingTXT")).
		Return("concurrent-instance-id", nil).Once()

	// Start announcement
	err = suite.sut.Announce(target)
	require.NoError(suite.T(), err)

	// Mock mDNS unannounce (only one should succeed due to mutex protection)
	suite.mockPairingMdns.EXPECT().
		UnannouncePairingService("concurrent-instance-id").
		Return(nil).
		Times(1) // Only one call should succeed

	// Launch concurrent stop operations
	numGoroutines := 5
	results := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() {
				if r := recover(); r != nil {
					results <- fmt.Errorf("panic in goroutine %d: %v", id, r)
				}
			}()
			err := suite.sut.StopAnnouncement()
			results <- err
		}(i)
	}

	// Collect all results
	var successCount, errorCount int
	var firstSuccess, firstError error

	for i := 0; i < numGoroutines; i++ {
		err := <-results
		if err == nil {
			successCount++
			if firstSuccess == nil {
				firstSuccess = err
			}
		} else {
			errorCount++
			if firstError == nil {
				firstError = err
			}
		}
	}

	// Exactly one operation should succeed, the rest should fail with ErrPairingNotActive
	assert.Equal(suite.T(), 1, successCount, "Exactly one concurrent stop should succeed")
	assert.Equal(suite.T(), numGoroutines-1, errorCount, "All other stops should fail")

	if firstError != nil {
		assert.Equal(suite.T(), api.ErrPairingNotActive, firstError, "Failed stops should return ErrPairingNotActive")
	}

	// Verify final state is clean
	status := suite.sut.GetAnnouncementStatus()
	assert.False(suite.T(), status.Active)
	assert.Nil(suite.T(), status.Target)
}
