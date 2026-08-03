package ship

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

func TestProClientSuite(t *testing.T) {
	suite.Run(t, new(ProClientSuite))
}

// lastProtocolError decodes the last sent frame as a protocol handshake error
// and returns its error type, for asserting the SHIP-mandated abort error codes.
func (s *ProClientSuite) lastProtocolError() model.MessageProtocolHandshakeErrorErrorType {
	_, data := s.sut.parseMessage(s.lastMessage(), true)
	var msg model.MessageProtocolHandshakeError
	_ = json.Unmarshal(data, &msg)
	return msg.Error
}

type ProClientSuite struct {
	suite.Suite

	mockWSWrite  *mocks.WebsocketDataWriterInterface
	mockShipInfo *mocks.ShipConnectionInfoProviderInterface

	sut *ShipConnection

	sentMessage     []byte
	wsReturnFailure error

	currentTestName string

	mux sync.Mutex
}

func (s *ProClientSuite) lastMessage() []byte {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.sentMessage
}

func (s *ProClientSuite) BeforeTest(suiteName, testName string) {
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
		}).
		Maybe()

	s.mockShipInfo = mocks.NewShipConnectionInfoProviderInterface(s.T())
	s.mockShipInfo.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Return().Maybe()
	s.mockShipInfo.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(true).Maybe()
	s.mockShipInfo.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Return().Maybe()

	s.sut = NewConnectionHandler(s.mockShipInfo, s.mockWSWrite, ShipRoleClient, "LocalShipID", "RemoveDevice", "RemoteShipID")
}

func (s *ProClientSuite) AfterTest(suiteName, testName string) {
	// Close the connection which will properly stop timers and wait for completion
	s.sut.CloseConnection(false, 4001, "test cleanup")
}

func (s *ProClientSuite) Test_Init() {
	s.sut.setState(model.SmeHelloStateOk, nil)

	s.sut.handleState(false, nil)

	// the state goes from smeHelloStateOk to smeProtHStateClientInit to smeProtHStateClientListenChoice
	assert.Equal(s.T(), model.SmeProtHStateClientListenChoice, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

// TC_SHIP_PROT_002: DUT as client accepts the server's select and confirms the JSON-UTF8 format.
func (s *ProClientSuite) Test_ListenChoice() {
	s.sut.setState(model.SmeProtHStateClientListenChoice, nil)

	protMsg := model.MessageProtocolHandshake{
		MessageProtocolHandshake: model.MessageProtocolHandshakeType{
			HandshakeType: model.ProtocolHandshakeTypeTypeSelect,
			Version:       model.Version{Major: 1, Minor: 0},
			Formats: model.MessageProtocolFormatsType{
				Format: []model.MessageProtocolFormatType{model.MessageProtocolFormatTypeUTF8},
			},
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, protMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)

	// state goes directly from smeProtHStateClientOk to smePinStateCheckInit to smePinStateCheckListen
	assert.Equal(s.T(), model.SmePinStateCheckListen, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *ProClientSuite) Test_ListenChoice_Failures() {
	s.sut.setState(model.SmeProtHStateClientListenChoice, nil)

	protMsg := model.MessageProtocolHandshake{
		MessageProtocolHandshake: model.MessageProtocolHandshakeType{
			HandshakeType: model.ProtocolHandshakeTypeTypeAnnounceMax,
			Version:       model.Version{Major: 0, Minor: 1},
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, protMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	s.sut.setState(model.SmeProtHStateClientListenChoice, nil)

	protMsg = model.MessageProtocolHandshake{
		MessageProtocolHandshake: model.MessageProtocolHandshakeType{
			HandshakeType: model.ProtocolHandshakeTypeTypeAnnounceMax,
			Version:       model.Version{Major: 0, Minor: 1},
			Formats: model.MessageProtocolFormatsType{
				Format: []model.MessageProtocolFormatType{model.MessageProtocolFormatTypeUTF16},
			},
		},
	}

	msg, err = s.sut.shipMessage(model.MsgTypeControl, protMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

// TC_SHIP_PROT_004: if the server does not reply with a select within the wait
// timer, the client executes the common abort with error=1 (timeout) and closes.
func (s *ProClientSuite) Test_ListenChoice_Timeout() {
	s.sut.setState(model.SmeProtHStateClientListenChoice, nil)

	s.sut.handleState(true, nil)

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.Equal(s.T(), model.MessageProtocolHandshakeErrorErrorTypeTimeout, s.lastProtocolError())
}

// TC_SHIP_PROT_006: a valid SME message other than the expected select (here: an
// announceMax) must trigger the common abort with error=2 (unexpected message),
// as opposed to a malformed select which is a selection mismatch (error=3).
func (s *ProClientSuite) Test_ListenChoice_UnexpectedMessage() {
	s.sut.setState(model.SmeProtHStateClientListenChoice, nil)

	protMsg := model.MessageProtocolHandshake{
		MessageProtocolHandshake: model.MessageProtocolHandshakeType{
			HandshakeType: model.ProtocolHandshakeTypeTypeAnnounceMax,
			Version:       model.Version{Major: 1, Minor: 0},
			Formats: model.MessageProtocolFormatsType{
				Format: []model.MessageProtocolFormatType{model.MessageProtocolFormatTypeUTF8},
			},
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, protMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.Equal(s.T(), model.MessageProtocolHandshakeErrorErrorTypeUnexpectedMessage, s.lastProtocolError())
}

// TC_SHIP_PROT_006 (contrast): a select with an unsupported version/format is a
// selection mismatch (error=3), not an unexpected message.
func (s *ProClientSuite) Test_ListenChoice_SelectionMismatch() {
	s.sut.setState(model.SmeProtHStateClientListenChoice, nil)

	protMsg := model.MessageProtocolHandshake{
		MessageProtocolHandshake: model.MessageProtocolHandshakeType{
			HandshakeType: model.ProtocolHandshakeTypeTypeSelect,
			Version:       model.Version{Major: 0, Minor: 1},
			Formats: model.MessageProtocolFormatsType{
				Format: []model.MessageProtocolFormatType{model.MessageProtocolFormatTypeUTF8},
			},
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, protMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.Equal(s.T(), model.MessageProtocolHandshakeErrorErrorTypeSelectionMismatch, s.lastProtocolError())
}

func (s *ProClientSuite) Test_Abort() {
	s.sut.setState(model.SmeProtHStateClientListenChoice, nil)

	s.sut.abortProtocolHandshake(model.MessageProtocolHandshakeErrorErrorTypeTimeout)

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())

	timer := s.sut.getHandshakeTimerRunning()
	assert.Equal(s.T(), false, timer)
}

// Test for sendShipModel error in handshakeProtocol_smeProtHStateClientInit
func (s *ProClientSuite) Test_ClientInit_SendError() {
	s.sut.setState(model.SmeProtHStateClientInit, nil)

	// Clear existing mock expectations
	s.mockWSWrite.ExpectedCalls = nil
	s.mockWSWrite.Calls = nil

	// Set up basic mocks without write expectation
	s.mockWSWrite.EXPECT().InitDataProcessing(mock.Anything).Return().Maybe()
	s.mockWSWrite.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
	s.mockWSWrite.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Return().Maybe()

	// Make WriteMessageToWebsocketConnection fail
	s.mux.Lock()
	s.wsReturnFailure = assert.AnError
	s.mux.Unlock()

	s.mockWSWrite.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(assert.AnError).Once()

	s.sut.handshakeProtocol_smeProtHStateClientInit()

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}

// Test for JSON unmarshal error in handshakeProtocol_smeProtHStateClientListenChoice
func (s *ProClientSuite) Test_ListenChoice_JSONError() {
	s.sut.setState(model.SmeProtHStateClientListenChoice, nil)

	// Send invalid JSON that will cause unmarshal error
	invalidMessage := []byte{model.MsgTypeControl, 0x00, 0x00, 0x00, 'i', 'n', 'v', 'a', 'l', 'i', 'd'}

	s.sut.handleState(false, invalidMessage)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

// Test for sendShipModel error in handshakeProtocol_smeProtHStateClientListenChoice final send
func (s *ProClientSuite) Test_ListenChoice_FinalSendError() {
	s.sut.setState(model.SmeProtHStateClientListenChoice, nil)

	// Clear existing mock expectations
	s.mockWSWrite.ExpectedCalls = nil
	s.mockWSWrite.Calls = nil

	// Set up basic mocks without write expectation
	s.mockWSWrite.EXPECT().InitDataProcessing(mock.Anything).Return().Maybe()
	s.mockWSWrite.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
	s.mockWSWrite.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Return().Maybe()

	// Make WriteMessageToWebsocketConnection fail for the final send
	// The function will process the message and then try to send its response, which should fail
	s.mockWSWrite.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(assert.AnError).Once()

	protMsg := model.MessageProtocolHandshake{
		MessageProtocolHandshake: model.MessageProtocolHandshakeType{
			HandshakeType: model.ProtocolHandshakeTypeTypeSelect,
			Version:       model.Version{Major: 1, Minor: 0},
			Formats: model.MessageProtocolFormatsType{
				Format: []model.MessageProtocolFormatType{model.MessageProtocolFormatTypeUTF8},
			},
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, protMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}
