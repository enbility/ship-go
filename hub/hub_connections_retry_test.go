package hub

import (
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

func TestHubConnectionsRetrySuite(t *testing.T) {
	suite.Run(t, new(HubConnectionsRetrySuite))
}

type HubConnectionsRetrySuite struct {
	suite.Suite

	hubReader   *mocks.MockHubReaderInterface
	mdnsService *mocks.MockMdnsInterface
	shipConnection *mocks.ShipConnectionInterface
	wsDataWriter   *mocks.WebsocketDataWriterInterface
	remoteSki string
	sut *Hub
}

func (s *HubConnectionsRetrySuite) BeforeTest(suiteName, testName string) {
	s.remoteSki = "remotetestski"

	ctrl := gomock.NewController(s.T())
	
	s.hubReader = mocks.NewMockHubReaderInterface(ctrl)
	s.hubReader.EXPECT().RemoteSKIConnected(gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().RemoteSKIDisconnected(gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().ServiceShipIDUpdate(gomock.Any(), gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().ServicePairingDetailUpdate(gomock.Any(), gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().AllowWaitingForTrust(gomock.Any()).Return(false).AnyTimes()

	s.mdnsService = mocks.NewMockMdnsInterface(ctrl)
	s.mdnsService.EXPECT().AnnounceMdnsEntry().Return(nil).AnyTimes()
	s.mdnsService.EXPECT().UnannounceMdnsEntry().Return().AnyTimes()
	s.mdnsService.EXPECT().RequestMdnsEntries().Return().AnyTimes()

	s.wsDataWriter = mocks.NewWebsocketDataWriterInterface(s.T())

	s.shipConnection = mocks.NewShipConnectionInterface(s.T())
	s.shipConnection.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	s.shipConnection.EXPECT().RemoteSKI().Return(s.remoteSki).Maybe()
	s.shipConnection.EXPECT().ApprovePendingHandshake().Return().Maybe()
	s.shipConnection.EXPECT().AbortPendingHandshake().Return().Maybe()
	s.shipConnection.EXPECT().DataHandler().Return(s.wsDataWriter).Maybe()
	s.shipConnection.EXPECT().ShipHandshakeState().Return(model.SmeStateComplete, nil).Maybe()

	localService := api.NewServiceDetails("localSKI")
	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	s.sut = NewHub(s.hubReader, s.mdnsService, 4567, certificate, localService)
}

func (s *HubConnectionsRetrySuite) AfterTest(suiteName, testName string) {
	s.mdnsService.EXPECT().Shutdown().AnyTimes()
	s.sut.Shutdown()
}

func (s *HubConnectionsRetrySuite) Test_IncreaseConnectionAttemptCounter() {
	// Test that the counter increments properly
	counter1 := s.sut.increaseConnectionAttemptCounter(s.remoteSki)
	assert.Equal(s.T(), 0, counter1)

	counter2 := s.sut.increaseConnectionAttemptCounter(s.remoteSki)
	assert.Equal(s.T(), 1, counter2)

	// Verify it exists
	currentCounter, exists := s.sut.getCurrentConnectionAttemptCounter(s.remoteSki)
	assert.True(s.T(), exists)
	assert.Equal(s.T(), 1, currentCounter)
}

func (s *HubConnectionsRetrySuite) Test_RemoveConnectionAttemptCounter() {
	s.sut.increaseConnectionAttemptCounter(s.remoteSki)
	_, exists := s.sut.connectionAttemptCounter[s.remoteSki]
	assert.Equal(s.T(), true, exists)

	s.sut.removeConnectionAttemptCounter(s.remoteSki)
	_, exists = s.sut.connectionAttemptCounter[s.remoteSki]
	assert.Equal(s.T(), false, exists)
}

func (s *HubConnectionsRetrySuite) Test_GetCurrentConnectionAttemptCounter() {
	s.sut.increaseConnectionAttemptCounter(s.remoteSki)
	_, exists := s.sut.connectionAttemptCounter[s.remoteSki]
	assert.Equal(s.T(), exists, true)
	s.sut.increaseConnectionAttemptCounter(s.remoteSki)

	value, exists := s.sut.getCurrentConnectionAttemptCounter(s.remoteSki)
	assert.Equal(s.T(), 1, value)
	assert.Equal(s.T(), true, exists)
}

func (s *HubConnectionsRetrySuite) Test_ConnectionAttemptRunning() {
	s.sut.setConnectionAttemptRunning(s.remoteSki, true)
	status := s.sut.isConnectionAttemptRunning(s.remoteSki)
	assert.Equal(s.T(), true, status)
	s.sut.setConnectionAttemptRunning(s.remoteSki, false)
	status = s.sut.isConnectionAttemptRunning(s.remoteSki)
	assert.Equal(s.T(), false, status)
}