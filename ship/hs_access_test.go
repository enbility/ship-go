package ship

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/enbility/ship-go/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

// spineDataMessage builds a raw websocket message carrying the given SPINE datagram payload,
// in the same shape HandleIncomingWebsocketMessage expects (SHIP header byte + ShipData JSON).
func spineDataMessage(t *testing.T, payload string) []byte {
	t.Helper()

	modelData := model.ShipData{
		Data: model.DataType{
			Payload: json.RawMessage(payload),
		},
	}
	jsonData, err := json.Marshal(modelData)
	assert.Nil(t, err)

	return append([]byte{model.MsgTypeData}, jsonData...)
}

// accessMethodsRequestMessage builds a raw SME "access methods request" message
func accessMethodsRequestMessage() []byte {
	return append([]byte{model.MsgTypeControl}, []byte(`{"accessMethodsRequest":{}}`)...)
}

// accessMethodsMessage builds a raw SME "access methods" message carrying the given SHIP ID
func accessMethodsMessage(shipID string) []byte {
	return append([]byte{model.MsgTypeControl}, []byte(`{"accessMethods":{"id":"`+shipID+`"}}`)...)
}

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

func (s *AccessSuite) resetSentMessage() {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.sentMessage = nil
}

// assertAccessMethodsReplySent checks that the last sent message is an SME "access methods"
// message carrying our own SHIP ID, as SHIP-TS-ACC-01 requires in reply to a request
func (s *AccessSuite) assertAccessMethodsReplySent() {
	msg := s.lastMessage()
	if !assert.NotNil(s.T(), msg, "we must send our accessMethods reply") {
		return
	}
	assert.Equal(s.T(), model.MsgTypeControl, msg[0])

	_, data := s.sut.parseMessage(msg, true)
	msgType, err := detectAccessMethodsMessageType(data)
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), "methods", msgType)

	var accessMethods model.AccessMethods
	assert.Nil(s.T(), json.Unmarshal(data, &accessMethods))
	if assert.NotNil(s.T(), accessMethods.AccessMethods.Id) {
		assert.Equal(s.T(), "LocalShipID", *accessMethods.AccessMethods.Id)
	}
}

// assertTerminationAnnounced checks that the connection was ended with a SHIP 13.4.7
// connectionClose announce rather than an abrupt close
func (s *AccessSuite) assertTerminationAnnounced() {
	msg := s.lastMessage()
	if !assert.NotNil(s.T(), msg, "termination must be announced (SHIP 13.4.7)") {
		return
	}
	assert.Equal(s.T(), model.MsgTypeEnd, msg[0])

	var closeMsg model.ConnectionClose
	assert.Nil(s.T(), s.sut.processShipJsonMessage(msg, &closeMsg))
	assert.Equal(s.T(), model.ConnectionClosePhaseTypeAnnounce, closeMsg.ConnectionClose.Phase)
}

// enterDataExchange drives the connection from successful PIN verification into connection data
// exchange, expecting the application to be handed the connection exactly once
func (s *AccessSuite) enterDataExchange(reader api.ShipConnectionDataReaderInterface) {
	s.mockShipInfo.EXPECT().SetupRemoteService(mock.Anything, mock.Anything).Return(reader).Once()

	s.sut.setState(model.SmePinStateCheckOk, nil)
	s.sut.handleState(false, nil)
	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())
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
	reader := mocks.NewShipConnectionDataReaderInterface(s.T())
	s.mockShipInfo.EXPECT().SetupRemoteService(mock.Anything, mock.Anything).Return(reader)
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

// Anything other than an access methods message must not end a connection that is already in
// connection data exchange (SHIP 13.4.5.1: gracefully skip unknown content)
func (s *AccessSuite) Test_Request_Invalid() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	accessMsg := model.MessageProtocolHandshake{}
	msg, err := s.sut.shipMessage(model.MsgTypeControl, accessMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}

func (s *AccessSuite) Test_Methods_Ok() {
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
	s.assertTerminationAnnounced()
}

// The SHIP ID is the primary identifier in the trust store (SHIP Pairing Service 10.4): a remote
// reporting a SHIP ID other than the one trust was established for is disconnected, with an error
// the application can tell apart from other failures
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
	state, err := s.sut.ShipHandshakeState()
	assert.Equal(s.T(), model.SmeStateError, state)
	assert.ErrorIs(s.T(), err, api.ErrShipIDMismatch)
	s.assertTerminationAnnounced()
}

func (s *AccessSuite) Test_Methods_NoShipID() {
	s.mockShipInfo.EXPECT().ReportServiceShipID(mock.Anything, mock.Anything)
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
	s.sut.setState(model.SmeAccessMethodsRequest, nil)
	s.sut.remoteShipID = "i:46353_u:1234567890"

	// EEBUS JSON with spaces after colons
	// After JsonFromEEBUSJson conversion: {"accessMethods": {"id": "i:46353_u:1234567890"}}
	eebusMsg := []byte{model.MsgTypeControl}
	eebusMsg = append(eebusMsg, []byte(`{"accessMethods": [{"id": "i:46353_u:1234567890"}]}`)...)

	s.sut.handleState(false, eebusMsg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())
}

func (s *AccessSuite) Test_Methods_ArrayFormat_NoSpaces() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)
	s.sut.remoteShipID = "i:46353_u:1234567890"

	// EEBUS JSON without spaces after colons
	// After JsonFromEEBUSJson conversion: {"accessMethods":{"id":"i:46353_u:1234567890"}}
	eebusMsg := []byte{model.MsgTypeControl}
	eebusMsg = append(eebusMsg, []byte(`{"accessMethods":[{"id":"i:46353_u:1234567890"}]}`)...)

	s.sut.handleState(false, eebusMsg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())
}

func (s *AccessSuite) Test_AccessMethodsRequest_WithSpaces() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	// whitespace in the JSON must not matter (SHIP IG Transport and Connectivity 2.2)
	eebusMsg := []byte{model.MsgTypeControl}
	eebusMsg = append(eebusMsg, []byte(`{"accessMethodsRequest": {}}`)...)

	s.sut.handleState(false, eebusMsg)

	// Should send response and stay in same state
	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())
	s.assertAccessMethodsReplySent()
}

func (s *AccessSuite) Test_AccessMethodsRequest_NoSpaces() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	eebusMsg := []byte{model.MsgTypeControl}
	eebusMsg = append(eebusMsg, []byte(`{"accessMethodsRequest":{}}`)...)

	s.sut.handleState(false, eebusMsg)

	// Should send response and stay in same state
	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())
	s.assertAccessMethodsReplySent()
}

// Direct tests for handleDataExchangeSmeMessage

func (s *AccessSuite) Test_DataExchangeSmeMessage_InvalidJSON_Ignored() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	// invalid JSON must not end a connection that is already in connection data exchange
	invalidMessage := []byte{model.MsgTypeControl, 'i', 'n', 'v', 'a', 'l', 'i', 'd'}

	s.sut.handleDataExchangeSmeMessage(invalidMessage)

	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}

func (s *AccessSuite) Test_DataExchangeSmeMessage_RequestType_Success() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	// Create access methods request message
	requestMsg := `{"accessMethodsRequest": {}}`
	message := append([]byte{model.MsgTypeControl}, []byte(requestMsg)...)

	s.sut.handleDataExchangeSmeMessage(message)

	// Verify state remains unchanged (waiting for response)
	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())
	s.assertAccessMethodsReplySent()
}

func (s *AccessSuite) Test_DataExchangeSmeMessage_RequestType_SendError() {
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

	// Make WriteMessageToWebsocketConnection fail: once for the reply, once for the SHIP 13.4.7
	// termination announce that follows
	expectedErr := errors.New("websocket write failed")
	s.mockWSWrite.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(expectedErr).Times(2)

	s.sut.handleDataExchangeSmeMessage(message)

	// Verify state changed to error due to sendShipModel failure
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}

func (s *AccessSuite) Test_DataExchangeSmeMessage_MethodsType_UnmarshalError() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	// Create invalid access methods message that will fail unmarshal
	methodsMsg := `{"accessMethods": "invalid"}`
	message := append([]byte{model.MsgTypeControl}, []byte(methodsMsg)...)

	s.sut.handleDataExchangeSmeMessage(message)

	// This is the reply to our only request, so no usable reply follows: end the connection
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	s.assertTerminationAnnounced()
}

func (s *AccessSuite) Test_DataExchangeSmeMessage_MethodsType_HandleResponseError() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	// Create access methods message with no ID (will fail validation)
	methodsMsg := `{"accessMethods": {}}`
	message := append([]byte{model.MsgTypeControl}, []byte(methodsMsg)...)

	s.sut.handleDataExchangeSmeMessage(message)

	// Verify state changed to error
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	s.assertTerminationAnnounced()
}

func (s *AccessSuite) Test_DataExchangeSmeMessage_MethodsType_Success() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)
	// Set the remote SHIP ID to match what we'll send
	s.sut.remoteShipID = "RemoteShipID"

	// Create valid access methods message that matches the expected remoteShipID
	methodsMsg := `{"accessMethods": {"id": "RemoteShipID"}}`
	message := append([]byte{model.MsgTypeControl}, []byte(methodsMsg)...)

	// No SetupRemoteService expectation: the connection was set up when data exchange was
	// entered, and the reply only completes it
	s.sut.handleDataExchangeSmeMessage(message)

	// Verify state changed to complete
	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())
}

func (s *AccessSuite) Test_DataExchangeSmeMessage_UnknownMessageType_Ignored() {
	s.sut.setState(model.SmeAccessMethodsRequest, nil)

	// Create message with unknown type
	unknownMsg := `{"unknownType": {}}`
	message := append([]byte{model.MsgTypeControl}, []byte(unknownMsg)...)

	s.sut.handleDataExchangeSmeMessage(message)

	// SHIP 13.4.5.1: unknown content is skipped, the connection stays up
	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}

// TC_SHIP_AMDATA_001: we must not drop incoming SPINE "data" messages while an accessMethods
// request of the remote is unanswered. Connection data exchange, and with it SPINE processing,
// starts once PIN verification succeeded - independently of any access methods exchange.
func (s *AccessSuite) Test_TC_SHIP_AMDATA_001_SpineProcessingEnabledWithOwnAccessMethodsRequest() {
	reader := mocks.NewShipConnectionDataReaderInterface(s.T())
	reader.EXPECT().HandleShipPayloadMessage(mock.Anything).Return()

	// PIN verification succeeded: we enter data exchange and send our own accessMethods request
	s.enterDataExchange(reader)
	assert.NotNil(s.T(), s.sut.getDataReader())

	// (1) test tool sends a SPINE data message - processed immediately
	s.sut.HandleIncomingWebsocketMessage(spineDataMessage(s.T(), `{"datagram":{"cmd":"detailed-discovery"}}`))
	reader.AssertNumberOfCalls(s.T(), "HandleShipPayloadMessage", 1)

	// (2) test tool sends its own accessMethodsRequest - we must answer it. Our own request is
	// the last message sent so far, so clear it to be sure the reply is what gets checked.
	s.resetSentMessage()
	s.sut.HandleIncomingWebsocketMessage(accessMethodsRequestMessage())
	s.assertAccessMethodsReplySent()
	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())

	// (3) test tool sends a third SPINE data message - still processed immediately, without
	// waiting for the response to our own outgoing accessMethods request
	s.sut.HandleIncomingWebsocketMessage(spineDataMessage(s.T(), `{"datagram":{"cmd":"use-case-discovery"}}`))
	reader.AssertNumberOfCalls(s.T(), "HandleShipPayloadMessage", 2)
}

// SPINE data outside connection data exchange - before PIN verification succeeded, or after the
// connection failed - is a protocol violation: it must be dropped without reaching the reader,
// even if one is set
func (s *AccessSuite) Test_SpineDataMessage_DroppedOutsideDataExchange() {
	reader := mocks.NewShipConnectionDataReaderInterface(s.T())
	s.sut.setDataReader(reader)

	for _, state := range []model.ShipMessageExchangeState{
		model.SmeHelloStateReadyListen,
		model.SmePinStateCheckListen,
		model.SmeStateError,
	} {
		s.sut.setState(state, nil)
		s.sut.HandleIncomingWebsocketMessage(spineDataMessage(s.T(), `{"datagram":{"cmd":"detailed-discovery"}}`))
	}

	reader.AssertNumberOfCalls(s.T(), "HandleShipPayloadMessage", 0)
}

// TC_SHIP_AMDATA_002: same burst as TC_SHIP_AMDATA_001, followed by a delayed accessMethods
// response completing our own outgoing request. The connection must complete without setting up
// the remote service a second time.
func (s *AccessSuite) Test_TC_SHIP_AMDATA_002_DelayedAccessMethodsResponseCompletesHandshakeOnce() {
	reader := mocks.NewShipConnectionDataReaderInterface(s.T())
	reader.EXPECT().HandleShipPayloadMessage(mock.Anything).Return()

	// PIN verification succeeded: we enter data exchange and send our own accessMethods request
	s.enterDataExchange(reader)

	// burst: SPINE data, the test tool's own accessMethodsRequest, more SPINE data - all
	// processed immediately, without waiting for the response to our own request
	s.sut.HandleIncomingWebsocketMessage(spineDataMessage(s.T(), `{"datagram":{"cmd":"detailed-discovery"}}`))
	s.resetSentMessage()
	s.sut.HandleIncomingWebsocketMessage(accessMethodsRequestMessage())
	s.assertAccessMethodsReplySent()
	s.sut.HandleIncomingWebsocketMessage(spineDataMessage(s.T(), `{"datagram":{"cmd":"use-case-discovery"}}`))

	reader.AssertNumberOfCalls(s.T(), "HandleShipPayloadMessage", 2)
	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())

	// 8s later: the response to our own outgoing accessMethodsRequest finally arrives
	s.sut.HandleIncomingWebsocketMessage(accessMethodsMessage("RemoteShipID"))

	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())
	// the .Once() expectation in enterDataExchange fails the test if SetupRemoteService is called
	// again here
}

// TC_SHIP_AM_001 / SHIP-TS-ACC-01: the recipient of an accessMethodsRequest SHALL respond, which is
// not limited to the time our own request is pending. PRE_SHIP_ConnectionEstablished does not
// include the access methods exchange, so the test tool may send its request once we are complete.
func (s *AccessSuite) Test_Complete_AnswersAccessMethodsRequest() {
	s.enterDataExchange(mocks.NewShipConnectionDataReaderInterface(s.T()))
	s.sut.HandleIncomingWebsocketMessage(accessMethodsMessage("RemoteShipID"))
	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())

	s.resetSentMessage()
	s.sut.HandleIncomingWebsocketMessage(accessMethodsRequestMessage())

	s.assertAccessMethodsReplySent()
	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())
}

// SHIP 13.4.6.2.1: "access methods" SHALL ONLY be sent upon a request. Once complete, another one is
// unsolicited: like unknown SME content, it must neither change the connection nor set it up again.
func (s *AccessSuite) Test_Complete_IgnoresUnsolicitedAndUnknownSmeMessages() {
	s.enterDataExchange(mocks.NewShipConnectionDataReaderInterface(s.T()))
	s.sut.HandleIncomingWebsocketMessage(accessMethodsMessage("RemoteShipID"))
	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())

	s.resetSentMessage()
	s.sut.HandleIncomingWebsocketMessage(accessMethodsMessage("OtherShipID"))
	s.sut.HandleIncomingWebsocketMessage(append([]byte{model.MsgTypeControl}, []byte(`{"unknownType":{}}`)...))

	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}

// SetupRemoteService may return nil when the application does not process SPINE data, as the
// quickstart and pairing examples do. It must still be called exactly once, the connection must
// still complete, and SPINE data must be dropped without failing.
func (s *AccessSuite) Test_NilReader_SetupOnceAndCompletes() {
	s.enterDataExchange(nil)
	assert.Nil(s.T(), s.sut.getDataReader())

	s.sut.HandleIncomingWebsocketMessage(accessMethodsMessage("RemoteShipID"))
	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())

	assert.NotPanics(s.T(), func() {
		s.sut.HandleIncomingWebsocketMessage(spineDataMessage(s.T(), `{"datagram":{"cmd":"detailed-discovery"}}`))
	})
	// the .Once() expectation in enterDataExchange fails the test if SetupRemoteService is called
	// again
}

// The SHIP header byte decides whether a message is SPINE data (SHIP 13.4.5.2.1), not its content:
// a control message whose SHIP ID contains "datagram" must still reach the SME handling
func (s *AccessSuite) Test_ControlMessageContainingDatagram_RoutedToSme() {
	s.sut.remoteShipID = "acme-datagram-01"
	s.enterDataExchange(mocks.NewShipConnectionDataReaderInterface(s.T()))

	s.sut.HandleIncomingWebsocketMessage(accessMethodsMessage("acme-datagram-01"))

	assert.Equal(s.T(), model.SmeStateComplete, s.sut.getState())
}
