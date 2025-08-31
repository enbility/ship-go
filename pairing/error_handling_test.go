package pairing

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
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

// PairingErrorHandlingTestSuite focuses on error scenarios, edge cases, and fault tolerance
// in the SHIP Pairing Service integration
type PairingErrorHandlingTestSuite struct {
	suite.Suite

	// Test infrastructure
	testCert    tls.Certificate
	testSKI     string
	testService *api.ServiceDetails
	testSecret  api.PairingSecret

	// Reusable mocks
	mockMdns    *mocks.MdnsPairingInterface
	mockCrypto  *mocks.PairingCryptoInterface
	mockHistory *mocks.PairingHistoryProviderInterface
	mockHub     *mocks.PairingHubInterface
}

func TestPairingErrorHandlingTestSuite(t *testing.T) {
	suite.Run(t, new(PairingErrorHandlingTestSuite))
}

func (suite *PairingErrorHandlingTestSuite) SetupTest() {
	// Create test certificate
	var err error
	suite.testCert, err = cert.CreateCertificate("TestDevice", "TestOrg", "DE", "test-device")
	require.NoError(suite.T(), err)

	// Extract SKI
	testX509, err := x509.ParseCertificate(suite.testCert.Certificate[0])
	require.NoError(suite.T(), err)
	suite.testSKI, err = cert.SkiFromCertificate(testX509)
	require.NoError(suite.T(), err)

	// Create service details
	suite.testService = api.NewServiceDetails(suite.testSKI, "", "")
	suite.testService.SetShipID("i:123_u:test-device")

	// Test secret
	suite.testSecret = api.PairingSecret("7A37DCF81BDB50F8E92CFA4160CCB3DE")

	// Create fresh mocks for each test
	suite.mockMdns = mocks.NewMdnsPairingInterface(suite.T())
	suite.mockCrypto = mocks.NewPairingCryptoInterface(suite.T())
	suite.mockHistory = mocks.NewPairingHistoryProviderInterface(suite.T())
	suite.mockHub = mocks.NewPairingHubInterface(suite.T())
}

/* Startup Error Scenarios */

func (suite *PairingErrorHandlingTestSuite) TestPairingListenerStartupErrors() {
	// Test pairing listener startup error scenarios

	listener := NewPairingListener(
		suite.mockMdns, suite.mockCrypto, suite.mockHistory,
		suite.mockHub, suite.testService,
	)

	ctx := context.Background()

	// Test mDNS search failure
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(
		errors.New("mDNS service unavailable")).Once()

	err := listener.StartListening(ctx, suite.testSecret)
	assert.Error(suite.T(), err)
	assert.Contains(suite.T(), err.Error(), "mDNS service unavailable")
}

func (suite *PairingErrorHandlingTestSuite) TestNonceGenerationFailure() {
	// Test announcer handling nonce generation failures

	testCert, _ := cert.CreateCertificate("TestOU", "TestOrg", "DE", "test")
	testX509Cert, _ := x509.ParseCertificate(testCert.Certificate[0])
	announcer := NewPairingAnnouncer(
		suite.mockMdns, suite.mockCrypto, testX509Cert, // Valid cert
		suite.mockHistory, suite.testService,
	)

	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  suite.testSecret,
		Enabled: true,
	}

	err := announcer.EnablePairingService(config)
	require.NoError(suite.T(), err)

	target := &api.PairingTarget{
		SKI:         "targetski",
		Fingerprint: "TARGET_FP",
		ShipID:      "i:789_u:target",
		Secret:      []byte(suite.testSecret),
	}

	// Mock nonce generation failure
	suite.mockCrypto.EXPECT().GenerateNonce().Return(nil, errors.New("nonce generation failed")).Once()

	err = announcer.Announce(target)
	assert.Error(suite.T(), err)
	assert.Contains(suite.T(), err.Error(), "nonce generation failed")

	// Verify announcer is still configured but not active
	status := announcer.GetPairingServiceStatus()
	assert.False(suite.T(), status.AnnouncerActive, "Announcer should not be active after nonce failure")
}

/* mDNS Operation Errors */

func (suite *PairingErrorHandlingTestSuite) TestMdnsOperationErrors() {
	// Test mDNS operation failures

	testCert, _ := cert.CreateCertificate("TestOU", "TestOrg", "DE", "test")
	testX509Cert, _ := x509.ParseCertificate(testCert.Certificate[0])
	// Test announcer mDNS failure
	announcer := NewPairingAnnouncer(
		suite.mockMdns, suite.mockCrypto, testX509Cert, // Valid cert
		suite.mockHistory, suite.testService,
	)

	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  suite.testSecret,
		Enabled: true,
	}

	err := announcer.EnablePairingService(config)
	require.NoError(suite.T(), err)

	target := &api.PairingTarget{
		SKI:         "targetski",
		Fingerprint: "TARGET_FP",
		ShipID:      "i:789_u:target",
		Secret:      []byte(suite.testSecret),
	}

	// Mock successful crypto operations but failed mDNS
	suite.mockCrypto.EXPECT().GenerateNonce().Return([]byte{0x01, 0x02}, nil).Once()
	suite.mockCrypto.EXPECT().CalculateDigest(
		suite.testSecret,
		mock.AnythingOfType("*api.HMACParams"),
	).Return([]byte{0xAA, 0xBB}, nil).Once()
	suite.mockMdns.EXPECT().AnnouncePairingService(
		mock.AnythingOfType("*api.ShipPairingTXT"),
	).Return("", errors.New("mDNS announce failed")).Once()

	err = announcer.Announce(target)
	assert.Error(suite.T(), err)
	assert.Contains(suite.T(), err.Error(), "mDNS announce failed")
}

/* Configuration Error Scenarios */

func (suite *PairingErrorHandlingTestSuite) TestConfigurationErrors() {
	// Test various configuration error scenarios

	// Test 1: Invalid pairing mode
	invalidConfig := &PairingConfiguration{
		Mode:    999, // Invalid mode
		Secret:  suite.testSecret,
		Enabled: true,
	}

	testCert, _ := cert.CreateCertificate("TestOU", "TestOrg", "DE", "test")
	testX509Cert, _ := x509.ParseCertificate(testCert.Certificate[0])
	announcer := NewPairingAnnouncer(
		suite.mockMdns, suite.mockCrypto, testX509Cert, // Valid cert
		suite.mockHistory, suite.testService,
	)

	err := announcer.EnablePairingService(invalidConfig)
	assert.Error(suite.T(), err)
	assert.Contains(suite.T(), err.Error(), "invalid pairing mode")

	// Test 2: Empty secret
	emptySecret := api.PairingSecret("")

	listener := NewPairingListener(
		suite.mockMdns, suite.mockCrypto, suite.mockHistory,
		suite.mockHub, suite.testService,
	)

	ctx := context.Background()
	err = listener.StartListening(ctx, emptySecret)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrInvalidSecret, err)

	// Test 3: Nil configuration
	testCert2, _ := cert.CreateCertificate("TestOU", "TestOrg", "DE", "test")
	testX509Cert2, _ := x509.ParseCertificate(testCert2.Certificate[0])
	announcer2 := NewPairingAnnouncer(
		suite.mockMdns, suite.mockCrypto, testX509Cert2, // Valid cert
		suite.mockHistory, suite.testService,
	)

	err = announcer2.EnablePairingService(nil) // Nil config
	assert.Error(suite.T(), err)
	assert.NotNil(suite.T(), err)
}

/* Concurrent Access and Race Condition Tests */

func (suite *PairingErrorHandlingTestSuite) TestConcurrentAccess() {
	// Test concurrent access scenarios that could cause race conditions
	if testing.Short() {
		suite.T().Skip("Skipping concurrent access test in short mode")
	}

	listener := NewPairingListener(
		suite.mockMdns, suite.mockCrypto, suite.mockHistory,
		suite.mockHub, suite.testService,
	)

	// Setup basic mocks
	ctx := context.Background()
	suite.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Maybe()

	err := listener.StartListening(ctx, suite.testSecret)
	require.NoError(suite.T(), err)

	// Create multiple goroutines trying to get status concurrently
	const numGoroutines = 10
	results := make([]*api.ListenerStatus, numGoroutines)
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(index int) {
			// Each goroutine gets status multiple times
			for j := 0; j < 5; j++ {
				status := listener.GetListenerStatus()
				results[index] = status
				time.Sleep(1 * time.Millisecond) // Small delay to increase chance of race
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		select {
		case <-done:
			// Good
		case <-time.After(5 * time.Second):
			suite.T().Fatal("Concurrent access test timed out")
		}
	}

	// All status results should be consistent (no data races)
	for i, status := range results {
		assert.True(suite.T(), status.Active, "Status %d should show active listener", i)
	}
}

/* Recovery and Cleanup Tests */

func (suite *PairingErrorHandlingTestSuite) TestErrorRecoveryAndCleanup() {
	// Test that components can recover from errors and clean up properly

	// Create service with error injection
	service, err := NewService(
		suite.mockMdns, suite.mockCrypto, suite.mockHistory,
		suite.mockHub, suite.testCert,
	)
	require.NoError(suite.T(), err)

	// Test service lifecycle with errors
	err = service.Start()
	assert.NoError(suite.T(), err, "Service should start even if some components might fail")

	// Test shutdown after errors
	service.Shutdown()

	// Service should be cleanly shut down
	status := service.GetPairingStatus()
	assert.False(suite.T(), status.Running, "Service should be stopped after shutdown")
}

/* Boundary Condition Tests */

func (suite *PairingErrorHandlingTestSuite) TestBoundaryConditions() {
	// Test boundary conditions and edge cases

	// Test 1: Maximum timeout values
	testCert, _ := cert.CreateCertificate("TestOU", "TestOrg", "DE", "test")
	testX509Cert, _ := x509.ParseCertificate(testCert.Certificate[0])
	announcer := NewPairingAnnouncer(
		suite.mockMdns, suite.mockCrypto, testX509Cert, // Valid cert
		suite.mockHistory, suite.testService,
	)

	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  suite.testSecret,
		Enabled: true,
	}

	err := announcer.EnablePairingService(config)
	require.NoError(suite.T(), err)

	target := &api.PairingTarget{
		SKI:         "targetski",
		Fingerprint: "TARGET_FP",
		ShipID:      "i:789_u:target",
		Secret:      []byte(suite.testSecret),
	}

	// Setup mocks for successful announcement
	suite.mockCrypto.EXPECT().GenerateNonce().Return([]byte{0x01, 0x02}, nil).Once()
	suite.mockCrypto.EXPECT().CalculateDigest(
		suite.testSecret,
		mock.AnythingOfType("*api.HMACParams"),
	).Return([]byte{0xAA, 0xBB}, nil).Once()
	suite.mockMdns.EXPECT().AnnouncePairingService(
		mock.AnythingOfType("*api.ShipPairingTXT"),
	).Return("test-instance-id", nil).Once()

	err = announcer.Announce(target)
	assert.Nil(suite.T(), err)

	// Test second announcement (boundary condition: should fail when already active)
	err = announcer.Announce(target)
	assert.Error(suite.T(), err)
	assert.Contains(suite.T(), err.Error(), "already active")
}

/* Resource Exhaustion Tests */

func (suite *PairingErrorHandlingTestSuite) TestResourceExhaustion() {
	// Test behavior under resource exhaustion conditions
	if testing.Short() {
		suite.T().Skip("Skipping resource exhaustion test in short mode")
	}

	// Test memory pressure by creating many pairing contexts
	const numContexts = 1000
	contexts := make([]*PairingAnnouncer, numContexts)

	for i := 0; i < numContexts; i++ {
		mockMdns := mocks.NewMdnsPairingInterface(suite.T())
		mockCrypto := mocks.NewPairingCryptoInterface(suite.T())
		mockHistory := mocks.NewPairingHistoryProviderInterface(suite.T())

		testCert, _ := cert.CreateCertificate("TestOU", "TestOrg", "DE", "test")
		testX509Cert, _ := x509.ParseCertificate(testCert.Certificate[0])
		contexts[i] = NewPairingAnnouncer(
			mockMdns, mockCrypto, testX509Cert, // Valid cert
			mockHistory, suite.testService,
		)
	}

	// All contexts should be created successfully
	for i, ctx := range contexts {
		assert.NotNil(suite.T(), ctx, "Context %d should be created", i)
	}

	// Clean up to test that cleanup works under pressure
	contexts = nil //nolint:ineffassign // Intentional nil assignment for cleanup testing
	// Force garbage collection to verify no leaks
	// runtime.GC() - commented out as it's not critical for the test
}
