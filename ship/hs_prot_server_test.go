package ship

import (
	"sync"
	"testing"

	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

func TestProServerSuite(t *testing.T) {
	suite.Run(t, new(ProServerSuite))
}

type ProServerSuite struct {
	suite.Suite

	mockWSWrite  *mocks.WebsocketDataWriterInterface
	mockShipInfo *mocks.ShipConnectionInfoProviderInterface

	sut *ShipConnection

	sentMessage     []byte
	wsReturnFailure error

	currentTestName string

	mux sync.Mutex
}

func (s *ProServerSuite) lastMessage() []byte {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.sentMessage
}

func (s *ProServerSuite) BeforeTest(suiteName, testName string) {
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

	s.sut = NewConnectionHandler(s.mockShipInfo, s.mockWSWrite, ShipRoleServer, "LocalShipID", "RemoveDevice", "RemoteShipID")
}

func (s *ProServerSuite) AfterTest(suiteName, testName string) {
	// Close the connection which will properly stop timers and wait for completion
	s.sut.CloseConnection(false, 4001, "test cleanup")
}

func (s *ProServerSuite) Test_Init() {
	s.sut.setState(model.SmeHelloStateOk, nil)

	s.sut.handleState(false, nil)

	assert.Equal(s.T(), true, s.sut.handshakeTimerRunning)

	// the state goes from smeHelloStateOk to smeProtHStateServerInit to smeProtHStateServerListenProposal
	assert.Equal(s.T(), model.SmeProtHStateServerListenProposal, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}

func (s *ProServerSuite) Test_ListenProposal() {
	s.sut.setState(model.SmeProtHStateServerListenProposal, nil)

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

	assert.Equal(s.T(), true, s.sut.handshakeTimerRunning)

	assert.Equal(s.T(), model.SmeProtHStateServerListenConfirm, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *ProServerSuite) Test_ListenProposal_Failure() {
	s.sut.setState(model.SmeProtHStateServerListenProposal, nil)

	protMsg := model.MessageProtocolHandshake{
		MessageProtocolHandshake: model.MessageProtocolHandshakeType{
			HandshakeType: model.ProtocolHandshakeTypeTypeSelect,
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, protMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}

func (s *ProServerSuite) Test_ListenConfirm() {
	s.sut.setState(model.SmeProtHStateServerListenConfirm, nil)

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

	// state smeProtHStateServerOk directly goes to smePinStateCheckInit to smePinStateCheckListen
	assert.Equal(s.T(), model.SmePinStateCheckListen, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *ProServerSuite) Test_ListenConfirm_Failures() {
	s.sut.setState(model.SmeProtHStateServerListenConfirm, nil)

	protMsg := model.MessageProtocolHandshake{
		MessageProtocolHandshake: model.MessageProtocolHandshakeType{
			HandshakeType: model.ProtocolHandshakeTypeTypeAnnounceMax,
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, protMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

// Test for JSON unmarshal error in handshakeProtocol_smeProtHStateServerListenProposal
func (s *ProServerSuite) Test_ListenProposal_JSONError() {
	s.sut.setState(model.SmeProtHStateServerListenProposal, nil)

	// Send invalid JSON that will cause unmarshal error
	invalidMessage := []byte{model.MsgTypeControl, 0x00, 0x00, 0x00, 'i', 'n', 'v', 'a', 'l', 'i', 'd'}

	s.sut.handleState(false, invalidMessage)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}

// Test for sendShipModel error in handshakeProtocol_smeProtHStateServerListenProposal
func (s *ProServerSuite) Test_ListenProposal_SendError() {
	s.sut.setState(model.SmeProtHStateServerListenProposal, nil)

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

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}

// Test for JSON unmarshal error in handshakeProtocol_smeProtHStateServerListenConfirm
func (s *ProServerSuite) Test_ListenConfirm_JSONError() {
	s.sut.setState(model.SmeProtHStateServerListenConfirm, nil)

	// Send invalid JSON that will cause unmarshal error
	invalidMessage := []byte{model.MsgTypeControl, 0x00, 0x00, 0x00, 'i', 'n', 'v', 'a', 'l', 'i', 'd'}

	s.sut.handleState(false, invalidMessage)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}
