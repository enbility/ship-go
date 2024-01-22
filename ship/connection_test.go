package ship

import (
	"encoding/json"
	"testing"

	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

func TestConnectionSuite(t *testing.T) {
	suite.Run(t, new(ConnectionSuite))
}

type ConnectionSuite struct {
	suite.Suite

	sut *ShipConnectionImpl

	shipDataProvider *mocks.ShipServiceDataProvider
	wsDataConn       *mocks.WebsocketDataConnection

	spineDataProcessing *mocks.SpineDataProcessing

	sentMessage []byte
}

func (s *ConnectionSuite) BeforeTest(suiteName, testName string) {
	s.sentMessage = nil

	s.shipDataProvider = mocks.NewShipServiceDataProvider(s.T())
	s.shipDataProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Return().Maybe()
	s.shipDataProvider.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Return().Maybe()
	s.shipDataProvider.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(false).Maybe()
	s.shipDataProvider.EXPECT().AllowWaitingForTrust(mock.Anything).Return(false).Maybe()

	s.wsDataConn = mocks.NewWebsocketDataConnection(s.T())
	s.wsDataConn.EXPECT().InitDataProcessing(mock.Anything).Return().Maybe()
	s.wsDataConn.EXPECT().WriteMessageToDataConnection(mock.Anything).RunAndReturn(func(message []byte) error { s.sentMessage = message; return nil }).Maybe()
	s.wsDataConn.EXPECT().IsDataConnectionClosed().RunAndReturn(func() (bool, error) { return false, nil }).Maybe()
	s.wsDataConn.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Return().Maybe()

	s.spineDataProcessing = mocks.NewSpineDataProcessing(s.T())
	s.spineDataProcessing.EXPECT().HandleIncomingSpineMesssage(mock.Anything).Return().Maybe()

	s.sut = NewConnectionHandler(s.shipDataProvider, s.wsDataConn, ShipRoleServer, "LocalShipID", "RemoveDevice", "RemoteShipID")
}

func (s *ConnectionSuite) Test_RemoteSKI() {
	remoteSki := s.sut.RemoteSKI()
	assert.NotEqual(s.T(), "", remoteSki)
}

func (s *ConnectionSuite) Test_DataHandler() {
	handler := s.sut.DataHandler()
	assert.NotNil(s.T(), handler)
}

func (s *ConnectionSuite) TestRun() {
	s.sut.Run()
	state, err := s.sut.ShipHandshakeState()
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), model.CmiStateServerWait, state)
}

func (s *ConnectionSuite) TestShipHandshakeState() {
	state, err := s.sut.ShipHandshakeState()
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), model.CmiStateInitStart, state)
}

func (s *ConnectionSuite) TestApprovePendingHandshake() {
	s.sut.smeState = model.CmiStateInitStart
	s.sut.ApprovePendingHandshake()
	assert.Equal(s.T(), model.CmiStateInitStart, s.sut.smeState)

	s.sut.smeState = model.SmeHelloStatePendingListen
	s.sut.ApprovePendingHandshake()
	assert.Equal(s.T(), model.SmeProtHStateServerListenProposal, s.sut.smeState)
}

func (s *ConnectionSuite) TestAbortPendingHandshake() {
	s.sut.smeState = model.CmiStateInitStart
	s.sut.AbortPendingHandshake()
	assert.Equal(s.T(), model.CmiStateInitStart, s.sut.smeState)

	s.sut.smeState = model.SmeHelloStatePendingListen
	s.sut.AbortPendingHandshake()
	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.smeState)
}

func (s *ConnectionSuite) TestCloseConnection_StateComplete() {
	s.sut.smeState = model.SmeStateComplete
	s.sut.CloseConnection(true, 450, "User Close")
	state, err := s.sut.ShipHandshakeState()
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), model.SmeStateComplete, state)
}

func (s *ConnectionSuite) TestCloseConnection_StateComplete_2() {
	s.sut.smeState = model.SmeStateError
	s.sut.CloseConnection(false, 0, "User Close")
	state, err := s.sut.ShipHandshakeState()
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), model.SmeStateError, state)
}

func (s *ConnectionSuite) TestCloseConnection_StateComplete_3() {
	s.sut.smeState = model.SmeStateError
	s.sut.CloseConnection(false, 450, "User Close")
	state, err := s.sut.ShipHandshakeState()
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), model.SmeStateError, state)
}

func (s *ConnectionSuite) TestShipModelFromMessage() {
	msg := []byte{}
	data, err := s.sut.shipModelFromMessage(msg)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), data)

	modelData := model.ShipData{}
	jsonData, err := json.Marshal(modelData)
	assert.Nil(s.T(), err)

	msg = []byte{0}
	msg = append(msg, jsonData...)
	data, err = s.sut.shipModelFromMessage(msg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), data)
}

func (s *ConnectionSuite) TestHandleIncomingShipMessage() {
	modelData := model.ShipData{}
	jsonData, err := json.Marshal(modelData)
	assert.Nil(s.T(), err)

	msg := []byte{0}
	msg = append(msg, jsonData...)

	s.sut.HandleIncomingShipMessage(msg)

	spineData := `{"datagram":{}}`
	jsonData = []byte(spineData)

	modelData = model.ShipData{
		Data: model.DataType{
			Payload: jsonData,
		},
	}
	jsonData, err = json.Marshal(modelData)
	assert.Nil(s.T(), err)

	msg = []byte{0}
	msg = append(msg, jsonData...)

	s.sut.HandleIncomingShipMessage(msg)

	s.sut.spineDataProcessing = s.spineDataProcessing

	s.sut.processBufferedSpineMessages()

	s.sut.HandleIncomingShipMessage(msg)
}

func (s *ConnectionSuite) TestReportConnectionError() {
	s.sut.ReportConnectionError(nil)
	assert.Equal(s.T(), model.SmeStateError, s.sut.smeState)

	s.sut.smeState = model.SmeHelloStateReadyListen
	s.sut.ReportConnectionError(nil)
	assert.Equal(s.T(), model.SmeHelloStateRejected, s.sut.smeState)

	s.sut.smeState = model.SmeHelloStateRemoteAbortDone
	s.sut.ReportConnectionError(nil)
	assert.Equal(s.T(), model.SmeHelloStateRemoteAbortDone, s.sut.smeState)

	s.sut.smeState = model.SmeHelloStateAbort
	s.sut.ReportConnectionError(nil)
	assert.Equal(s.T(), model.SmeHelloStateAbort, s.sut.smeState)
}

func (s *ConnectionSuite) TestSendShipModel() {
	err := s.sut.sendShipModel(model.MsgTypeInit, nil)
	assert.NotNil(s.T(), err)

	closeMessage := model.ConnectionClose{
		ConnectionClose: model.ConnectionCloseType{
			Phase: model.ConnectionClosePhaseTypeAnnounce,
		},
	}

	err = s.sut.sendShipModel(model.MsgTypeControl, closeMessage)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), s.sentMessage)
}

func (s *ConnectionSuite) TestProcessShipJsonMessage() {
	closeMessage := model.ConnectionClose{
		ConnectionClose: model.ConnectionCloseType{
			Phase: model.ConnectionClosePhaseTypeAnnounce,
		},
	}
	msg, err := json.Marshal(closeMessage)
	assert.Nil(s.T(), err)

	newMsg := []byte{model.MsgTypeControl}
	newMsg = append(newMsg, msg...)

	var data any
	err = s.sut.processShipJsonMessage(newMsg, &data)
	assert.Nil(s.T(), err)
}

func (s *ConnectionSuite) TestSendSpineMessage() {
	data := `{"datagram":{"header":{},"payload":{"cmd":[]}}}`

	err := s.sut.sendSpineData([]byte(data))
	assert.Nil(s.T(), err)
}
