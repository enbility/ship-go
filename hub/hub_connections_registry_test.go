package hub

import (
	"crypto/tls"
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

func TestHubConnectionsRegistrySuite(t *testing.T) {
	suite.Run(t, new(HubConnectionsRegistrySuite))
}

type HubConnectionsRegistrySuite struct {
	suite.Suite

	hubReader   *mocks.MockHubReaderInterface
	mdnsService *mocks.MockMdnsInterface
	shipConnection *mocks.ShipConnectionInterface
	wsDataWriter   *mocks.WebsocketDataWriterInterface
	remoteSki string
	sut *Hub
}

func (s *HubConnectionsRegistrySuite) BeforeTest(suiteName, testName string) {
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

func (s *HubConnectionsRegistrySuite) AfterTest(suiteName, testName string) {
	s.mdnsService.EXPECT().Shutdown().AnyTimes()
	s.sut.Shutdown()
}

func (s *HubConnectionsRegistrySuite) Test_IsRemoteSKIPaired() {
	paired := s.sut.IsRemoteServiceForSKIPaired(s.remoteSki)
	assert.Equal(s.T(), false, paired)

	s.sut.registerConnection(s.shipConnection)
	s.sut.RegisterRemoteSKI(s.remoteSki, "")

	paired = s.sut.IsRemoteServiceForSKIPaired(s.remoteSki)
	assert.Equal(s.T(), true, paired)

	// remove the connection, so the test doesn't try to close it
	delete(s.sut.connections, s.remoteSki)
	s.sut.UnregisterRemoteSKI(s.remoteSki)
	paired = s.sut.IsRemoteServiceForSKIPaired(s.remoteSki)
	assert.Equal(s.T(), false, paired)

	ski := "12af9e"
	localService := api.NewServiceDetails(ski)

	hub := NewHub(s.hubReader, s.mdnsService, 4567, tls.Certificate{}, localService)
	assert.NotNil(s.T(), hub)

	s.mdnsService.EXPECT().Start(gomock.Any()).Return(nil).Times(1)
	err := hub.Start()
	assert.NoError(s.T(), err)

	hub.UnregisterRemoteSKI(s.remoteSki)
	paired = s.sut.IsRemoteServiceForSKIPaired(s.remoteSki)
	assert.Equal(s.T(), false, paired)

	s.mdnsService.EXPECT().Shutdown().Times(1)
	hub.Shutdown()
}

func (s *HubConnectionsRegistrySuite) Test_RegisterRemoteSKI_AfterStart() {
	s.sut.hasStarted = true

	s.sut.RegisterRemoteSKI(s.remoteSki, "")
	assert.Equal(s.T(), 0, len(s.sut.connections))

	s.sut.registerConnection(s.shipConnection)
	s.sut.RegisterRemoteSKI(s.remoteSki, "")
	assert.Equal(s.T(), 1, len(s.sut.connections))
}

func (s *HubConnectionsRegistrySuite) Test_HandleConnectionClosed() {
	s.sut.HandleConnectionClosed(s.shipConnection, false)

	s.sut.registerConnection(s.shipConnection)

	s.sut.HandleConnectionClosed(s.shipConnection, true)

	assert.Equal(s.T(), 0, len(s.sut.connections))
}

func (s *HubConnectionsRegistrySuite) Test_RegisterConnection() {
	s.sut.registerConnection(s.shipConnection)
	assert.Equal(s.T(), 1, len(s.sut.connections))
	con := s.sut.connectionForSKI(s.remoteSki)
	assert.NotNil(s.T(), con)
}

func (s *HubConnectionsRegistrySuite) Test_CancelPairingWithSKI() {
	s.sut.CancelPairingWithSKI(s.remoteSki)
	assert.Equal(s.T(), 0, len(s.sut.connections))
	assert.Equal(s.T(), 0, len(s.sut.connectionAttemptRunning))

	s.sut.registerConnection(s.shipConnection)
	assert.Equal(s.T(), 1, len(s.sut.connections))

	s.sut.CancelPairingWithSKI(s.remoteSki)
	assert.Equal(s.T(), 0, len(s.sut.connectionAttemptRunning))
}