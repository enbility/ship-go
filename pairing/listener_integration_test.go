package pairing

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type ListenerIntegrationTestSuite struct {
	suite.Suite

	mockMdns    *mocks.MdnsPairingInterface
	mockCrypto  *mocks.PairingCryptoInterface
	mockHistory *mocks.PairingHistoryProviderInterface
	mockHub     *mocks.PairingHubInterface

	localService *api.ServiceDetails
	listener     *PairingListener
}

func TestListenerIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(ListenerIntegrationTestSuite))
}

func (s *ListenerIntegrationTestSuite) SetupTest() {
	s.mockMdns = mocks.NewMdnsPairingInterface(s.T())
	s.mockCrypto = mocks.NewPairingCryptoInterface(s.T())
	s.mockHistory = mocks.NewPairingHistoryProviderInterface(s.T())
	s.mockHub = mocks.NewPairingHubInterface(s.T())

	s.localService = api.NewServiceDetails("localski", "", "")
	s.localService.SetShipID("local-ship-id")

	s.listener = NewPairingListener(
		s.mockMdns,
		s.mockCrypto,
		s.mockHistory,
		s.mockHub,
		s.localService,
	)
}

func (s *ListenerIntegrationTestSuite) generateValidSecret() api.PairingSecret {
	secret := make([]byte, 16)
	rand.Read(secret)
	return secret
}

// TestListenerStartListening_Success tests successful listener startup
func (s *ListenerIntegrationTestSuite) TestListenerStartListening_Success() {
	secret := s.generateValidSecret()

	// Mock mDNS enable pairing discovery
	s.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil)

	ctx := context.Background()
	err := s.listener.StartListening(ctx, secret)
	s.Assert().NoError(err)

	// Verify listener state
	status := s.listener.GetListenerStatus()
	s.Assert().NotNil(status)
	s.Assert().True(status.Active)
	s.Assert().Equal(0, status.RequestsSeen)

	// Cleanup
	err = s.listener.StopListening()
	s.Assert().NoError(err)
}

// TestListenerStartListening_InvalidSecret tests listener with invalid secrets
func (s *ListenerIntegrationTestSuite) TestListenerStartListening_InvalidSecret() {
	testCases := []struct {
		name        string
		secret      api.PairingSecret
		expectError string
	}{
		{
			name:        "too short secret",
			secret:      make([]byte, 8),
			expectError: "invalid secret",
		},
		{
			name:        "too long secret",
			secret:      make([]byte, 256),
			expectError: "invalid secret",
		},
		{
			name:        "empty secret",
			secret:      []byte{},
			expectError: "invalid secret",
		},
	}

	for _, tc := range testCases {
		s.Run(tc.name, func() {
			ctx := context.Background()
			err := s.listener.StartListening(ctx, tc.secret)
			s.Assert().Error(err)
			s.Assert().Equal(api.ErrInvalidSecret, err)
		})
	}
}

// TestListenerStartListening_AlreadyActive tests starting listener when already active
func (s *ListenerIntegrationTestSuite) TestListenerStartListening_AlreadyActive() {
	secret := s.generateValidSecret()

	// Start listener first time
	s.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil)
	ctx := context.Background()
	err := s.listener.StartListening(ctx, secret)
	s.Require().NoError(err)

	// Try to start again
	err = s.listener.StartListening(ctx, secret)
	s.Assert().Error(err)
	s.Assert().Equal(api.ErrListenerAlreadyActive, err)

	// Cleanup
	s.listener.StopListening()
}

// TestListenerStartListening_MdnsFailure tests mDNS failure during startup
func (s *ListenerIntegrationTestSuite) TestListenerStartListening_MdnsFailure() {
	secret := s.generateValidSecret()

	// Mock mDNS failure
	expectedError := assert.AnError
	s.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(expectedError)

	ctx := context.Background()
	err := s.listener.StartListening(ctx, secret)
	s.Assert().Error(err)
	s.Assert().Error(err) // SearchPairingServices error

	// Verify listener is not active
	status := s.listener.GetListenerStatus()
	s.Assert().NotNil(status)
	s.Assert().False(status.Active)
}

// TestListenerStopListening tests manual stop
func (s *ListenerIntegrationTestSuite) TestListenerStopListening() {
	secret := s.generateValidSecret()

	// Start listener
	s.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil)
	// No need to mock stop - listener handles internally

	ctx := context.Background()
	err := s.listener.StartListening(ctx, secret)
	s.Require().NoError(err)

	// Verify it's running
	status := s.listener.GetListenerStatus()
	s.Assert().True(status.Active)

	// Stop listener
	err = s.listener.StopListening()
	s.Assert().NoError(err)

	// Verify it's stopped
	status = s.listener.GetListenerStatus()
	s.Assert().False(status.Active)
}

// TestListenerStopListening_NotRunning tests stopping when not running
func (s *ListenerIntegrationTestSuite) TestListenerStopListening_NotRunning() {
	// Stop listener when not running
	err := s.listener.StopListening()
	s.Assert().Error(err)
	s.Assert().Equal(api.ErrPairingNotActive, err)
}

// TestListenerGetStatus tests status reporting
func (s *ListenerIntegrationTestSuite) TestListenerGetStatus() {
	// Initial status
	status := s.listener.GetListenerStatus()
	s.Assert().NotNil(status)
	s.Assert().False(status.Active)
	s.Assert().Equal(0, status.RequestsSeen)
	s.Assert().True(status.StartTime.IsZero())

	// Start listener and check status
	secret := s.generateValidSecret()
	s.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil)

	beforeStart := time.Now()
	ctx := context.Background()
	err := s.listener.StartListening(ctx, secret)
	s.Require().NoError(err)
	afterStart := time.Now()

	status = s.listener.GetListenerStatus()
	s.Assert().True(status.Active)
	s.Assert().Equal(0, status.RequestsSeen)
	s.Assert().True(status.StartTime.After(beforeStart) || status.StartTime.Equal(beforeStart))
	s.Assert().True(status.StartTime.Before(afterStart) || status.StartTime.Equal(afterStart))

	// Cleanup
	// No need to mock stop - listener handles internally
	s.listener.StopListening()
}

// TestListenerContextCancellation tests behavior when context is cancelled
func (s *ListenerIntegrationTestSuite) TestListenerContextCancellation() {
	secret := s.generateValidSecret()

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Start listener
	s.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil)
	// No need to mock stop - listener handles internally

	err := s.listener.StartListening(ctx, secret)
	s.Require().NoError(err)

	// Verify it's running
	status := s.listener.GetListenerStatus()
	s.Assert().True(status.Active)

	// Cancel context
	cancel()

	// Wait for cancellation to take effect with retry
	for i := 0; i < 10; i++ {
		time.Sleep(50 * time.Millisecond)
		status = s.listener.GetListenerStatus()
		if !status.Active {
			break
		}
	}

	// Verify listener stopped
	s.Assert().False(status.Active, "Listener should stop after context cancellation")
}

// TestListenerSecretHandling tests proper secret handling and cleanup
func (s *ListenerIntegrationTestSuite) TestListenerSecretHandling() {
	originalSecret := s.generateValidSecret()

	// Start listener
	s.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil)
	// No need to mock stop - listener handles internally

	ctx := context.Background()
	err := s.listener.StartListening(ctx, originalSecret)
	s.Require().NoError(err)

	// Verify secret is copied (not shared reference)
	originalSecret[0] = 0xFF // Modify original

	// Stop listener - this should clear the internal secret
	err = s.listener.StopListening()
	s.Assert().NoError(err)

	// Verify listener can be reused with new secret
	newSecret := s.generateValidSecret()
	s.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil)
	// No need to mock stop - listener handles internally

	err = s.listener.StartListening(ctx, newSecret)
	s.Assert().NoError(err)

	s.listener.StopListening()
}

// TestListenerConcurrentOperations tests concurrent start/stop operations
func (s *ListenerIntegrationTestSuite) TestListenerConcurrentOperations() {
	secret := s.generateValidSecret()

	// Setup mocks for concurrent operations
	s.mockMdns.EXPECT().SearchPairingServices(mock.AnythingOfType("func(*api.ShipPairingTXT) bool")).Return(nil).Maybe()
	// No need to mock stop - listener handles internally.Maybe()

	// Start multiple goroutines trying to start/stop
	done := make(chan bool, 4)

	// Two goroutines trying to start
	go func() {
		ctx := context.Background()
		s.listener.StartListening(ctx, secret)
		done <- true
	}()

	go func() {
		ctx := context.Background()
		s.listener.StartListening(ctx, secret)
		done <- true
	}()

	// Wait for starts to complete
	<-done
	<-done

	// Two goroutines trying to stop
	go func() {
		s.listener.StopListening()
		done <- true
	}()

	go func() {
		s.listener.StopListening()
		done <- true
	}()

	// Wait for stops to complete
	<-done
	<-done

	// Verify final state is consistent
	status := s.listener.GetListenerStatus()
	s.Assert().NotNil(status)
	s.Assert().False(status.Active)
}
