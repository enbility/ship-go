package ship

import (
	"errors"
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

func (s *AccessSuite) Test_Init_SendError() {
	s.sut.setState(model.SmePinStateCheckOk, nil)
	
	// Clear the existing mock expectations first
	s.mockWSWrite.ExpectedCalls = nil
	s.mockWSWrite.Calls = nil
	
	// Mock the WebSocket writer to fail on write (which will cause sendShipModel to fail)
	s.mockWSWrite.EXPECT().InitDataProcessing(mock.Anything).Return().Maybe()
	s.mockWSWrite.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
	s.mockWSWrite.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Return().Maybe()
	
	// Make WriteMessageToWebsocketConnection fail
	expectedErr := errors.New("websocket write failed during init")
	s.mockWSWrite.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(expectedErr).Once()
	
	s.sut.handleState(false, nil)
	
	// Verify state changed to error due to sendShipModel failure in handshakeAccessMethods_Init
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
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

// Direct tests for handshakeAccessMethods_Request function

func (s *AccessSuite) Test_HandshakeAccessMethods_Request_DetectMessageTypeError() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	// Send invalid JSON that will fail detectAccessMethodsMessageType
	invalidMessage := []byte{model.MsgTypeControl, 'i', 'n', 'v', 'a', 'l', 'i', 'd'}

	s.sut.handshakeAccessMethods_Request(invalidMessage)

	// Verify state changed to error
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}

func (s *AccessSuite) Test_HandshakeAccessMethods_Request_RequestType_Success() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	// Create access methods request message
	requestMsg := `{"accessMethodsRequest": {}}`
	message := append([]byte{model.MsgTypeControl}, []byte(requestMsg)...)

	s.sut.handshakeAccessMethods_Request(message)

	// Verify state remains unchanged (waiting for response)
	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())
	// Verify response was sent
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *AccessSuite) Test_HandshakeAccessMethods_Request_RequestType_SendError() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	// Create access methods request message
	requestMsg := `{"accessMethodsRequest": {}}`
	message := append([]byte{model.MsgTypeControl}, []byte(requestMsg)...)

	// Clear the existing mock expectations first
	s.mockWSWrite.ExpectedCalls = nil
	s.mockWSWrite.Calls = nil

	// Mock the WebSocket writer to fail on write
	s.mockWSWrite.EXPECT().InitDataProcessing(mock.Anything).Return().Maybe()
	s.mockWSWrite.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
	s.mockWSWrite.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Return().Maybe()

	// Make WriteMessageToWebsocketConnection fail
	expectedErr := errors.New("websocket write failed")
	s.mockWSWrite.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(expectedErr).Once()

	s.sut.handshakeAccessMethods_Request(message)

	// Verify state changed to error due to sendShipModel failure
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}

func (s *AccessSuite) Test_HandshakeAccessMethods_Request_MethodsType_UnmarshalError() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	// Create invalid access methods message that will fail unmarshal
	methodsMsg := `{"accessMethods": "invalid"}`
	message := append([]byte{model.MsgTypeControl}, []byte(methodsMsg)...)

	s.sut.handshakeAccessMethods_Request(message)

	// Verify state changed to error
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}

func (s *AccessSuite) Test_HandshakeAccessMethods_Request_MethodsType_HandleResponseError() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	// Create access methods message with no ID (will fail validation)
	methodsMsg := `{"accessMethods": {}}`
	message := append([]byte{model.MsgTypeControl}, []byte(methodsMsg)...)

	s.sut.handshakeAccessMethods_Request(message)

	// Verify state changed to error
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}

func (s *AccessSuite) Test_HandshakeAccessMethods_Request_MethodsType_Success() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)
	// Set the remote SHIP ID to match what we'll send
	s.sut.remoteShipID = "RemoteShipID"

	// Create valid access methods message that matches the expected remoteShipID
	methodsMsg := `{"accessMethods": {"id": "RemoteShipID"}}`
	message := append([]byte{model.MsgTypeControl}, []byte(methodsMsg)...)

	// Mock approveHandshake expectations
	mockReader := mocks.NewShipConnectionDataReaderInterface(s.T())
	s.mockShipInfo.EXPECT().SetupRemoteDevice(mock.Anything, mock.Anything).Return(mockReader).Once()

	s.sut.handshakeAccessMethods_Request(message)

	// Verify state changed to complete
	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())
}

func (s *AccessSuite) Test_HandshakeAccessMethods_Request_UnknownMessageType() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	// Create message with unknown type
	unknownMsg := `{"unknownType": {}}`
	message := append([]byte{model.MsgTypeControl}, []byte(unknownMsg)...)

	s.sut.handshakeAccessMethods_Request(message)

	// Verify state changed to error
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}
