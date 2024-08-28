package ship

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

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

func (s *ConnectionSuite) Test_HandleShipCloseMessage() {
	s.sut.handleShipMessage(false, []byte{})
	state, err := s.sut.ShipHandshakeState()
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), model.CmiStateServerWait, state)

	closeMsg := model.ConnectionClose{
		ConnectionClose: model.ConnectionCloseType{
			Phase: model.ConnectionClosePhaseTypeAnnounce,
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, closeMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleShipMessage(false, msg)

	closeMsg = model.ConnectionClose{
		ConnectionClose: model.ConnectionCloseType{
			Phase: model.ConnectionClosePhaseTypeConfirm,
		},
	}

	msg, err = s.sut.shipMessage(model.MsgTypeControl, closeMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleShipMessage(false, msg)
}

func (s *ConnectionSuite) TestShipHandshakeState() {
	state, err := s.sut.ShipHandshakeState()
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), model.CmiStateInitStart, state)
}

func (s *ConnectionSuite) Test_HandleErrorState() {
	s.sut.setState(model.SmeStateError, errors.New("error"))

	state, err := s.sut.ShipHandshakeState()
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), model.SmeStateError, state)

	s.sut.handleState(false, []byte{})
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

	s.sut.HandleIncomingWebsocketMessage(msg)

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

	s.sut.HandleIncomingWebsocketMessage(msg)

	s.sut.dataReader = s.shipConnectionReader

	s.sut.processBufferedSpineMessages()

	s.sut.HandleIncomingWebsocketMessage(msg)
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

func (s *ConnectionSuite) Test_HandshakeTimer() {
	s.sut.setState(model.CmiStateInitStart, nil)
	assert.Equal(s.T(), model.CmiStateInitStart, s.sut.getState())

	s.sut.setHandshakeTimer(timeoutTimerTypeWaitForReady, time.Duration(time.Millisecond*500))
	assert.Equal(s.T(), true, s.sut.getHandshakeTimerRunning())

	time.Sleep(time.Second * 1)
	assert.Equal(s.T(), model.CmiStateServerWait, s.sut.getState())
	assert.Equal(s.T(), timeoutTimerTypeWaitForReady, s.sut.getHandshakeTimerType())
	assert.Equal(s.T(), true, s.sut.getHandshakeTimerRunning())
}
