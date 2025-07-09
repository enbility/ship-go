package hub

import (
	"crypto/tls"
	"errors"
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

func TestHubSuite(t *testing.T) {
	suite.Run(t, new(HubSuite))
}

type HubSuite struct {
	suite.Suite

	hubReader   *mocks.MockHubReaderInterface
	mdnsService *mocks.MockMdnsInterface

	// serviceProvider  *mocks.ServiceProvider
	// mdnsService      *mocks.MdnsService
	shipConnection *mocks.ShipConnectionInterface
	wsDataWriter   *mocks.WebsocketDataWriterInterface

	remoteSki string

	sut *Hub
}

func (s *HubSuite) BeforeTest(suiteName, testName string) {
	s.remoteSki = "remotetestski"

	ctrl := gomock.NewController(s.T())
	// use gomock mocks instead of mockery, as those will panic with a data race error in these tests

	s.hubReader = mocks.NewMockHubReaderInterface(ctrl)
	// s.serviceProvider = mocks.NewServiceProvider(s.T())
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

func (s *HubSuite) AfterTest(suiteName, testName string) {
	s.mdnsService.EXPECT().Shutdown().AnyTimes()

	s.sut.Shutdown()
}

func (s *HubSuite) Test_NewConnectionsHub() {
	ski := "12af9e"
	localService := api.NewServiceDetails(ski)

	hub := NewHub(s.hubReader, s.mdnsService, 4567, tls.Certificate{}, localService)
	assert.NotNil(s.T(), hub)

	s.mdnsService.EXPECT().Start(gomock.Any()).Return(nil).Times(1)

	err := hub.Start()
	assert.NoError(s.T(), err)

	s.mdnsService.EXPECT().Shutdown().Times(1)

	hub.Shutdown()
}

func (s *HubSuite) Test_AutoAccept() {
	s.mdnsService.EXPECT().SetAutoAccept(gomock.Any()).Return().AnyTimes()

	s.sut.SetAutoAccept(true)
	value := s.sut.IsAutoAcceptEnabled()
	assert.True(s.T(), value)

	s.sut.SetAutoAccept(false)
	value = s.sut.IsAutoAcceptEnabled()
	assert.False(s.T(), value)
}

func (s *HubSuite) Test_SetupRemoteDevice() {
	ski := "12af9e"
	localService := api.NewServiceDetails(ski)

	hub := NewHub(s.hubReader, s.mdnsService, 4567, tls.Certificate{}, localService)
	assert.NotNil(s.T(), hub)

	readerI := mocks.NewShipConnectionDataReaderInterface(s.T())
	s.hubReader.EXPECT().SetupRemoteDevice(gomock.Any(), gomock.Any()).Return(readerI)

	reader := hub.SetupRemoteDevice(ski, nil)

	assert.NotNil(s.T(), reader)
}



func (s *HubSuite) Test_checkHasStarted() {
	checked := s.sut.checkHasStarted()
	assert.Equal(s.T(), s.sut.hasStarted, checked)
}

func (s *HubSuite) Test_MapShipMessageExchangeState() {
	state := s.sut.mapShipMessageExchangeState(model.CmiStateInitStart, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateQueued, state)

	state = s.sut.mapShipMessageExchangeState(model.CmiStateClientSend, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateInitiated, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeHelloStateReadyInit, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateInProgress, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeHelloStatePendingListen, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateReceivedPairingRequest, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeHelloStateOk, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateTrusted, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeHelloStateAbort, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateNone, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeHelloStateRemoteAbortDone, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateRemoteDeniedTrust, state)

	state = s.sut.mapShipMessageExchangeState(model.SmePinStateCheckInit, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStatePin, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeAccessMethodsRequest, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateInProgress, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeStateComplete, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateCompleted, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeStateError, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateError, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeProtHStateTimeout, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateInProgress, state)
}

func (s *HubSuite) Test_DisconnectSKI() {
	s.sut.DisconnectSKI(s.remoteSki, "none")
}

func (s *HubSuite) Test_Mdns() {
	s.sut.checkAutoReannounce()

	pairedServices := s.sut.numberPairedServices()
	assert.Equal(s.T(), 0, len(s.sut.connections))
	assert.Equal(s.T(), 0, pairedServices)

	s.sut.RegisterRemoteSKI(s.remoteSki, "")
	pairedServices = s.sut.numberPairedServices()
	assert.Equal(s.T(), 0, len(s.sut.connections))
	assert.Equal(s.T(), 1, pairedServices)
}

func (s *HubSuite) Test_Ship() {
	s.sut.HandleShipHandshakeStateUpdate(s.remoteSki, model.ShipState{
		State: model.SmeStateError,
		Error: errors.New("test"),
	})

	s.sut.HandleShipHandshakeStateUpdate(s.remoteSki, model.ShipState{
		State: model.SmeHelloStateOk,
	})

	s.sut.ReportServiceShipID(s.remoteSki, "test")

	accept := s.sut.IsAutoAcceptEnabled()
	assert.Equal(s.T(), false, accept)

	trust := s.sut.AllowWaitingForTrust(s.remoteSki)
	assert.Equal(s.T(), true, trust)

	trust = s.sut.AllowWaitingForTrust("test")
	assert.Equal(s.T(), false, trust)

	detail := s.sut.PairingDetailForSki(s.remoteSki)
	assert.NotNil(s.T(), detail)

	s.sut.registerConnection(s.shipConnection)

	detail = s.sut.PairingDetailForSki(s.remoteSki)
	assert.NotNil(s.T(), detail)
}

func (s *HubSuite) Test_ReportMdnsEntries() {
	testski1 := "test1"
	testski2 := "test2"

	entries := make(map[string]*api.MdnsEntry)

	s.hubReader.EXPECT().VisibleRemoteServicesUpdated(gomock.Any()).AnyTimes()
	s.sut.ReportMdnsEntries(entries, true)

	entries[testski1] = &api.MdnsEntry{
		Ski: testski1,
	}
	service1 := s.sut.ServiceForSKI(testski1)
	service1.SetTrusted(true)
	service1.SetIPv4("127.0.0.1")

	entries[testski2] = &api.MdnsEntry{
		Ski: testski2,
	}
	service2 := s.sut.ServiceForSKI(testski2)
	service2.SetTrusted(true)
	service2.SetIPv4("127.0.0.1")

	s.sut.ReportMdnsEntries(entries, true)
}

