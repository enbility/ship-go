package ship

import (
	"sync"
	"testing"

	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/enbility/ship-go/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

func TestAccessSuite(t *testing.T) {
	suite.Run(t, new(AccessSuite))
}

type AccessSuite struct {
	suite.Suite

	mockWSWrite  *mocks.WebsocketDataWriterInterface
	mockShipInfo *mocks.ShipConnectionInfoProviderInterface

	sut *ShipConnection

	sentMessage     []byte
	wsReturnFailure error

	currentTestName string

	mux sync.Mutex
}

func (s *AccessSuite) lastMessage() []byte {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.sentMessage
}

func (s *AccessSuite) BeforeTest(suiteName, testName string) {
	s.mux.Lock()
	s.sentMessage = nil
	s.wsReturnFailure = nil
	s.currentTestName = testName
	s.mux.Unlock()

	s.mockWSWrite = mocks.NewWebsocketDataWriterInterface(s.T())
	s.mockWSWrite.EXPECT().InitDataProcessing(mock.Anything).Return().Maybe()
	s.mockWSWrite.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
	s.mockWSWrite.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Return().Maybe()
	s.mockWSWrite.
		EXPECT().
		WriteMessageToWebsocketConnection(mock.Anything).
		RunAndReturn(func(msg []byte) error {
			s.mux.Lock()
			defer s.mux.Unlock()

			if s.currentTestName != testName {
				return nil
			}

			s.sentMessage = msg

			return s.wsReturnFailure
		}).Maybe()

	s.mockShipInfo = mocks.NewShipConnectionInfoProviderInterface(s.T())
	s.mockShipInfo.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Return().Maybe()
	s.mockShipInfo.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(true).Maybe()
	s.mockShipInfo.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Return().Maybe()

	s.sut = NewConnectionHandler(s.mockShipInfo, s.mockWSWrite, ShipRoleClient, "LocalShipID", "RemoveDevice", "RemoteShipID")
}

func (s *AccessSuite) AfterTest(suiteName, testName string) {
	// Close the connection which will properly stop timers and wait for completion
	s.sut.CloseConnection(false, 4001, "test cleanup")
}

func (s *AccessSuite) Test_Init() {
	s.sut.setState(model.SmePinStateCheckOk, nil)
	s.sut.handleState(false, nil)

	assert.Equal(s.T(), true, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *AccessSuite) Test_Request() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	accessMsg := model.AccessMethodsRequest{
		AccessMethodsRequest: model.AccessMethodsRequestType{},
	}
	msg, err := s.sut.shipMessage(model.MsgTypeControl, accessMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *AccessSuite) Test_Request_Invalid() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	accessMsg := model.MessageProtocolHandshake{}
	msg, err := s.sut.shipMessage(model.MsgTypeControl, accessMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}

func (s *AccessSuite) Test_Methods_Ok() {
	reader := mocks.NewShipConnectionDataReaderInterface(s.T())
	s.mockShipInfo.EXPECT().SetupRemoteDevice(mock.Anything, mock.Anything).Return(reader)
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	accessMsg := model.AccessMethods{
		AccessMethods: model.AccessMethodsType{
			Id: util.Ptr("RemoteShipID"),
		},
	}
	msg, err := s.sut.shipMessage(model.MsgTypeControl, accessMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())
}

func (s *AccessSuite) Test_Methods_NoID() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	accessMsg := model.AccessMethods{
		AccessMethods: model.AccessMethodsType{},
	}
	msg, err := s.sut.shipMessage(model.MsgTypeControl, accessMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}

func (s *AccessSuite) Test_Methods_WrongShipID() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	accessMsg := model.AccessMethods{
		AccessMethods: model.AccessMethodsType{
			Id: util.Ptr("WrongRemoteShipID"),
		},
	}
	msg, err := s.sut.shipMessage(model.MsgTypeControl, accessMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}

func (s *AccessSuite) Test_Methods_NoShipID() {
	reader := mocks.NewShipConnectionDataReaderInterface(s.T())
	s.mockShipInfo.EXPECT().ReportServiceShipID(mock.Anything, mock.Anything)
	s.mockShipInfo.EXPECT().SetupRemoteDevice(mock.Anything, mock.Anything).Return(reader)
	s.sut.remoteShipID = ""

	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	accessMsg := model.AccessMethods{
		AccessMethods: model.AccessMethodsType{
			Id: util.Ptr(""),
		},
	}
	msg, err := s.sut.shipMessage(model.MsgTypeControl, accessMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())
}

func (s *AccessSuite) Test_Methods_ArrayFormat_WithSpaces() {
	reader := mocks.NewShipConnectionDataReaderInterface(s.T())
	s.mockShipInfo.EXPECT().SetupRemoteDevice(mock.Anything, mock.Anything).Return(reader)
	s.sut.setState(model.SmeAccessMethodsRequest, nil)
	s.sut.remoteShipID = "i:46353_u:1234567890"

	// EEBUS JSON with spaces after colons
	// After JsonFromEEBUSJson conversion: {"accessMethods": {"id": "i:46353_u:1234567890"}}
	// This SHOULD succeed but currently fails because string check looks for "accessMethods":{" (no space)
	eebusMsg := []byte{model.MsgTypeControl}
	eebusMsg = append(eebusMsg, []byte(`{"accessMethods": [{"id": "i:46353_u:1234567890"}]}`)...)

	s.sut.handleState(false, eebusMsg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())
}

func (s *AccessSuite) Test_Methods_ArrayFormat_NoSpaces() {
	reader := mocks.NewShipConnectionDataReaderInterface(s.T())
	s.mockShipInfo.EXPECT().SetupRemoteDevice(mock.Anything, mock.Anything).Return(reader)
	s.sut.setState(model.SmeAccessMethodsRequest, nil)
	s.sut.remoteShipID = "i:46353_u:1234567890"

	// EEBUS JSON without spaces after colons
	// After JsonFromEEBUSJson conversion: {"accessMethods":{"id":"i:46353_u:1234567890"}}
	// This succeeds because string check matches "accessMethods":{"
	eebusMsg := []byte{model.MsgTypeControl}
	eebusMsg = append(eebusMsg, []byte(`{"accessMethods":[{"id":"i:46353_u:1234567890"}]}`)...)

	s.sut.handleState(false, eebusMsg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())
}

func (s *AccessSuite) Test_AccessMethodsRequest_WithSpaces() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	// Test accessMethodsRequest with spaces - should fail
	eebusMsg := []byte{model.MsgTypeControl}
	eebusMsg = append(eebusMsg, []byte(`{"accessMethodsRequest": {}}`)...)

	s.sut.handleState(false, eebusMsg)

	// Should fail due to spacing mismatch
	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *AccessSuite) Test_AccessMethodsRequest_NoSpaces() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	// Test accessMethodsRequest without spaces - should succeed
	eebusMsg := []byte{model.MsgTypeControl}
	eebusMsg = append(eebusMsg, []byte(`{"accessMethodsRequest":{}}`)...)

	s.sut.handleState(false, eebusMsg)

	// Should send response and stay in same state
	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}
