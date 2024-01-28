package ship

import (
	"errors"
	"sync"
	"testing"

	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

func TestPinSuite(t *testing.T) {
	suite.Run(t, new(PinSuite))
}

type PinSuite struct {
	suite.Suite

	mockWSWrite  *mocks.WebsocketDataWriterInterface
	mockShipInfo *mocks.ShipConnectionInfoProviderInterface

	sut *ShipConnection

	sentMessage     []byte
	wsReturnFailure error

	currentTestName string

	mux sync.Mutex
}

func (s *PinSuite) lastMessage() []byte {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.sentMessage
}

func (s *PinSuite) setWSReturnError() {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.wsReturnFailure = errors.New("invalid")
}

func (s *PinSuite) BeforeTest(suiteName, testName string) {
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

func (s *PinSuite) AfterTest(suiteName, testName string) {
	s.sut.stopHandshakeTimer()
}

func (s *PinSuite) Test_Init() {
	s.sut.setState(model.SmePinStateCheckInit, nil)
	s.sut.handleState(false, nil)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmePinStateCheckListen, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *PinSuite) Test_Init_Failure() {
	s.setWSReturnError()

	s.sut.setState(model.SmePinStateCheckInit, nil)
	s.sut.handleState(false, nil)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *PinSuite) Test_CheckListen_Failure() {
	s.sut.setState(model.SmePinStateCheckListen, nil)

	s.sut.handshakePin_smePinStateCheckListen([]byte{0x5, 0x5, 0x5})

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}

func (s *PinSuite) Test_CheckListen_None() {
	s.sut.setState(model.SmePinStateCheckListen, nil)

	pinState := model.ConnectionPinState{
		ConnectionPinState: model.ConnectionPinStateType{
			PinState: model.PinStateTypeNone,
		},
	}
	msg, err := s.sut.shipMessage(model.MsgTypeControl, pinState)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), true, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeAccessMethodsRequest, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *PinSuite) Test_CheckListen_Required() {
	s.sut.setState(model.SmePinStateCheckListen, nil)

	pinState := model.ConnectionPinState{
		ConnectionPinState: model.ConnectionPinStateType{
			PinState: model.PinStateTypeRequired,
		},
	}
	msg, err := s.sut.shipMessage(model.MsgTypeControl, pinState)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}

func (s *PinSuite) Test_CheckListen_Optional() {
	s.sut.setState(model.SmePinStateCheckListen, nil)

	pinState := model.ConnectionPinState{
		ConnectionPinState: model.ConnectionPinStateType{
			PinState: model.PinStateTypeOptional,
		},
	}
	msg, err := s.sut.shipMessage(model.MsgTypeControl, pinState)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}

func (s *PinSuite) Test_CheckListen_Ok() {
	s.sut.setState(model.SmePinStateCheckListen, nil)

	pinState := model.ConnectionPinState{
		ConnectionPinState: model.ConnectionPinStateType{
			PinState: model.PinStateTypePinOk,
		},
	}
	msg, err := s.sut.shipMessage(model.MsgTypeControl, pinState)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}

func (s *PinSuite) Test_CheckListen_Invalid() {
	s.sut.setState(model.SmePinStateCheckListen, nil)

	pinState := model.ConnectionPinState{
		ConnectionPinState: model.ConnectionPinStateType{
			PinState: model.PinStateType("invalid"),
		},
	}
	msg, err := s.sut.shipMessage(model.MsgTypeControl, pinState)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), false, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}
