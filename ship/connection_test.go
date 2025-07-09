package ship

import (
	"sync"
	"testing"

	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

// ConnectionSuite provides shared test infrastructure for all connection tests
type ConnectionSuite struct {
	suite.Suite

	sut *ShipConnection

	infoProvider *mocks.ShipConnectionInfoProviderInterface
	wsDataWriter *mocks.WebsocketDataWriterInterface

	shipConnectionReader *mocks.ShipConnectionDataReaderInterface

	sentMessage []byte

	mux sync.Mutex
}

func (s *ConnectionSuite) BeforeTest(suiteName, testName string) {
	s.mux.Lock()
	s.sentMessage = nil
	s.mux.Unlock()

	s.infoProvider = mocks.NewShipConnectionInfoProviderInterface(s.T())
	s.infoProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Return().Maybe()
	s.infoProvider.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Return().Maybe()
	s.infoProvider.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(false).Maybe()
	s.infoProvider.EXPECT().AllowWaitingForTrust(mock.Anything).Return(false).Maybe()

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

	s.sut = NewConnectionHandler(s.infoProvider, s.wsDataWriter, ShipRoleServer, "LocalShipID", "RemoveDevice", "RemoteShipID")
}

// Tests for core connection struct functionality
func TestConnectionSuite(t *testing.T) {
	suite.Run(t, new(ConnectionSuite))
}

func (s *ConnectionSuite) Test_RemoteSKI() {
	remoteSki := s.sut.RemoteSKI()
	assert.NotEqual(s.T(), "", remoteSki)
}

func (s *ConnectionSuite) Test_DataHandler() {
	handler := s.sut.DataHandler()
	assert.NotNil(s.T(), handler)
}
