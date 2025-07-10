package ship

import (
	"sync"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

// ConnectionSuiteSafe is a version of ConnectionSuite that uses SafeConnectionTracker
// to avoid race conditions when testing concurrent operations.
// This suite should be used for tests that involve concurrent connection operations
// or handshake state transitions.
type ConnectionSuiteSafe struct {
	suite.Suite

	sut *ShipConnection

	// Use SafeConnectionTracker instead of mock for thread safety
	infoProvider api.ShipConnectionInfoProviderInterface

	// Keep other mocks as they don't have sync primitive issues
	wsDataWriter         *mocks.WebsocketDataWriterInterface
	shipConnectionReader *mocks.ShipConnectionDataReaderInterface

	sentMessage []byte
	mux         sync.Mutex
}

// SetupTest runs before each test
func (s *ConnectionSuiteSafe) SetupTest() {
	s.sentMessage = nil

	// Create SafeConnectionTracker with default configuration
	safeTracker := NewSafeConnectionTracker()
	safeTracker.Configure(
		WithRemoteServicePaired(false), // Default to not paired
		WithAutoAcceptEnabled(false),
		WithAllowWaitingForTrust(false),
	)
	s.infoProvider = safeTracker

	// Setup other mocks normally
	s.wsDataWriter = mocks.NewWebsocketDataWriterInterface(s.T())
	s.wsDataWriter.EXPECT().InitDataProcessing(mock.Anything).Return().Maybe()
	s.wsDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).
		RunAndReturn(func(message []byte) error {
			s.mux.Lock()
			defer s.mux.Unlock()
			s.sentMessage = message
			return nil
		}).
		Maybe()
	s.wsDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
	s.wsDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Return().Maybe()

	s.shipConnectionReader = mocks.NewShipConnectionDataReaderInterface(s.T())
	s.shipConnectionReader.EXPECT().HandleShipPayloadMessage(mock.Anything).Return().Maybe()

	// Create the connection under test
	s.sut = NewConnectionHandler(s.infoProvider, s.wsDataWriter, ShipRoleServer, "LocalShipID", "RemoveDevice", "RemoteShipID")
}

// Helper method to configure the SafeConnectionTracker for specific test scenarios
func (s *ConnectionSuiteSafe) ConfigureInfoProvider(opts ...func(*SafeConnectionTracker)) {
	if safeTracker, ok := s.infoProvider.(*SafeConnectionTracker); ok {
		safeTracker.Configure(opts...)
	}
}

// Helper method to assert connection closed was called
func (s *ConnectionSuiteSafe) AssertConnectionClosed(count int) {
	if safeTracker, ok := s.infoProvider.(*SafeConnectionTracker); ok {
		calls := safeTracker.GetConnectionClosedCalls()
		s.Assert().Equal(count, len(calls), "Expected %d HandleConnectionClosed calls, got %d", count, len(calls))
	}
}

// Helper method to assert handshake state updates
func (s *ConnectionSuiteSafe) AssertHandshakeStates(expectedStates ...model.ShipMessageExchangeState) {
	if safeTracker, ok := s.infoProvider.(*SafeConnectionTracker); ok {
		updates := safeTracker.GetHandshakeStateUpdates()
		actualStates := make([]model.ShipMessageExchangeState, len(updates))
		for i, u := range updates {
			actualStates[i] = u.State.State
		}
		s.Assert().Equal(expectedStates, actualStates, "Handshake state sequence mismatch")
	}
}

// Helper to get the last sent message
func (s *ConnectionSuiteSafe) GetLastSentMessage() []byte {
	s.mux.Lock()
	defer s.mux.Unlock()
	return s.sentMessage
}
