package hub

import (
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

func TestHubConnectionsTimerSuite(t *testing.T) {
	suite.Run(t, new(HubConnectionsTimerSuite))
}

type HubConnectionsTimerSuite struct {
	suite.Suite

	hubReader      *mocks.MockHubReaderInterface
	mdnsService    *mocks.MockMdnsInterface
	shipConnection *mocks.ShipConnectionInterface
	wsDataWriter   *mocks.WebsocketDataWriterInterface
	remoteSki      string
	sut            *Hub
}

func (s *HubConnectionsTimerSuite) BeforeTest(suiteName, testName string) {
	s.remoteSki = "remotetestski"

	ctrl := gomock.NewController(s.T())

	s.hubReader = mocks.NewMockHubReaderInterface(ctrl)
	s.hubReader.EXPECT().RemoteServiceConnected(gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().RemoteServiceDisconnected(gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().ServiceUpdated(gomock.Any()).Return().AnyTimes()
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

	localService := api.NewServiceDetails("localSKI", "", "")
	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	var err error
	s.sut, err = newTestHub(s.hubReader, s.mdnsService, 4567, certificate, localService, nil)
	assert.NoError(s.T(), err)
}

func (s *HubConnectionsTimerSuite) AfterTest(suiteName, testName string) {
	s.mdnsService.EXPECT().Shutdown().AnyTimes()
	s.sut.Shutdown()
}

func (s *HubConnectionsTimerSuite) Test_GetConnectionInitiationDelayTime() {
	counter, duration := s.sut.getConnectionInitiationDelayTime(s.remoteSki)
	assert.Equal(s.T(), 0, counter)
	// First attempt should be 0-3 seconds
	assert.GreaterOrEqual(s.T(), duration, time.Duration(0))
	assert.LessOrEqual(s.T(), duration, 3*time.Second)
}
