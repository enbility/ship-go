package ship

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/enbility/ship-go/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

func TestHelloSuite(t *testing.T) {
	suite.Run(t, new(HelloSuite))
}

type HelloSuite struct {
	suite.Suite

	mockWSWrite  *mocks.WebsocketDataWriterInterface
	mockShipInfo *mocks.ShipConnectionInfoProviderInterface

	sut *ShipConnection

	sentMessage     []byte
	wsReturnFailure error

	currentTestName string

	mux sync.Mutex
}

func (s *HelloSuite) lastMessage() []byte {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.sentMessage
}

func (s *HelloSuite) setWSReturnError() {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.wsReturnFailure = errors.New("invalid")
}

func (s *HelloSuite) BeforeTest(suiteName, testName string) {
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
	s.mockShipInfo.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Return().Maybe()
	s.mockShipInfo.EXPECT().IsAutoAcceptEnabled().Return(false).Maybe()

	s.sut = NewConnectionHandler(s.mockShipInfo, s.mockWSWrite, ShipRoleServer, "LocalShipID", "RemoveDevice", "RemoteShipID")
}

func (s *HelloSuite) AfterTest(suiteName, testName string) {
	s.sut.stopHandshakeTimer()
	assert.Equal(s.T(), false, s.sut.getHandshakeTimerRunning())
}

func (s *HelloSuite) Test_InitialState() {
	s.mockShipInfo.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(true)

	s.sut.setState(model.SmeHelloState, nil)
	s.sut.handleState(false, nil)

	assert.Equal(s.T(), true, s.sut.getHandshakeTimerRunning())
	assert.Equal(s.T(), model.SmeHelloStateReadyListen, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *HelloSuite) Test_InitialState_NotPaired() {
	s.mockShipInfo.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(false)
	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(false)
	s.sut.setState(model.SmeHelloState, nil)
	s.sut.handleState(false, nil)

	assert.Equal(s.T(), false, s.sut.getHandshakeTimerRunning())
	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *HelloSuite) Test_InitialState_Failure() {
	s.setWSReturnError()
	s.mockShipInfo.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(true).Maybe()

	s.sut.setState(model.SmeHelloState, nil)
	s.sut.handleState(false, nil)
}

func (s *HelloSuite) Test_ReadyListen_Init() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil)
	assert.Equal(s.T(), true, s.sut.getHandshakeTimerRunning())
}

func (s *HelloSuite) Test_ReadyListen_Ok() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStateReadyListen, nil)

	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase: model.ConnectionHelloPhaseTypeReady,
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	// the state goes from smeHelloStateOk directly to smeProtHStateServerInit to smeProtHStateClientListenProposal
	assert.Equal(s.T(), model.SmeProtHStateServerListenProposal, s.sut.getState())
}

func (s *HelloSuite) Test_ReadyListen_Timeout() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStateReadyListen, nil)

	if !util.IsRunningOnCI() {
		// test if the function is triggered correctly via the timer
		time.Sleep(tHelloInit + time.Second)
	} else {
		// speed up the test by running the method directly
		s.sut.handshakeHello_ReadyListen(true, nil)
	}

	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *HelloSuite) Test_ReadyListen_Ignore() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStateReadyListen, nil)

	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase: model.ConnectionHelloPhaseTypePending,
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), model.SmeHelloStateReadyListen, s.sut.getState())
}

func (s *HelloSuite) Test_ReadyListen_Ignore_Invalid() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStateReadyListen, nil)

	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase:               model.ConnectionHelloPhaseTypePending,
			ProlongationRequest: util.Ptr(false),
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), model.SmeHelloStateReadyListen, s.sut.getState())
}

func (s *HelloSuite) Test_ReadyListen_Prolongation() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStateReadyListen, nil)

	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(true)

	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase:               model.ConnectionHelloPhaseTypePending,
			ProlongationRequest: util.Ptr(true),
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), model.SmeHelloStateReadyListen, s.sut.getState())
}

func (s *HelloSuite) Test_ReadyListen_Abort() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStateReadyListen, nil)

	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase: model.ConnectionHelloPhaseTypeAborted,
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleShipMessage(false, msg)

	assert.Equal(s.T(), false, s.sut.getHandshakeTimerRunning())
	assert.Equal(s.T(), model.SmeHelloStateRemoteAbortDone, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}

func (s *HelloSuite) Test_PendingInit() {
	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(false)

	s.sut.setState(model.SmeHelloStatePendingInit, nil)
	s.sut.handleState(false, nil)

	assert.Equal(s.T(), false, s.sut.getHandshakeTimerRunning())
	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *HelloSuite) Test_PendingInit_Failure() {
	s.setWSReturnError()

	s.sut.setState(model.SmeHelloStatePendingInit, nil)
	s.sut.handleState(false, nil)

	assert.Equal(s.T(), false, s.sut.getHandshakeTimerRunning())
	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *HelloSuite) Test_PendingListen() {
	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)
	s.sut.handleState(false, nil)
}

func (s *HelloSuite) Test_PendingListen_PhaseInvalid() {
	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase: model.ConnectionHelloPhaseType("invalid"),
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.getState())
}

func (s *HelloSuite) Test_PendingListen_Timeout() {
	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(false)

	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	if !util.IsRunningOnCI() {
		// test if the function is triggered correctly via the timer
		time.Sleep(tHelloInit + time.Second)
	} else {
		// speed up the test by running the method directly
		s.sut.handshakeHello_PendingListen(true, nil)
	}

	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *HelloSuite) Test_PendingListen_Timeout_Failure() {
	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(true).Maybe()

	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	s.sut.setHandshakeTimerType(timeoutTimerTypeSendProlongationRequest)
	s.setWSReturnError()

	// speed up the test by running the method directly, the timer is already checked
	s.sut.handshakeHello_PendingTimeout()

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *HelloSuite) Test_PendingListen_Timeout_WaitingValueZero() {
	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(true).Maybe()

	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	s.sut.setHandshakeTimerType(timeoutTimerTypeSendProlongationRequest)

	// speed up the test by running the method directly, the timer is already checked
	s.sut.handshakeHello_PendingTimeout()

	assert.Equal(s.T(), model.SmeHelloStatePendingListen, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *HelloSuite) Test_PendingListen_Timeout_Prolongation() {
	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(true).Maybe()

	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	// speed up the test by running the method directly, the timer is already checked
	s.sut.handshakeHello_PendingListen(true, nil)

	assert.Equal(s.T(), model.SmeHelloStatePendingListen, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *HelloSuite) Test_PendingListen_Timeout_Prolongation_Failure() {
	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(true).Maybe()

	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	s.setWSReturnError()

	// speed up the test by running the method directly, the timer is already checked
	s.sut.handshakeHello_PendingListen(true, nil)

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *HelloSuite) Test_PendingListen_ReadyAbort() {
	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase: model.ConnectionHelloPhaseTypeReady,
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleShipMessage(false, msg)

	assert.Equal(s.T(), false, s.sut.getHandshakeTimerRunning())
	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *HelloSuite) Test_PendingListen_ReadyWaiting() {
	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(true).Maybe()

	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase:   model.ConnectionHelloPhaseTypeReady,
			Waiting: util.Ptr(uint(tHelloInit.Milliseconds())),
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleShipMessage(false, msg)

	assert.Equal(s.T(), true, s.sut.getHandshakeTimerRunning())
	assert.Equal(s.T(), model.SmeHelloStatePendingListen, s.sut.getState())
}

func (s *HelloSuite) Test_PendingListen_Abort() {
	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase: model.ConnectionHelloPhaseTypeAborted,
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleShipMessage(false, msg)

	assert.Equal(s.T(), false, s.sut.getHandshakeTimerRunning())
	assert.Equal(s.T(), model.SmeHelloStateRemoteAbortDone, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}

func (s *HelloSuite) Test_PendingListen_PendingWaiting() {
	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(true).Maybe()

	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase:   model.ConnectionHelloPhaseTypePending,
			Waiting: util.Ptr(uint(tHelloInit.Milliseconds())),
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleShipMessage(false, msg)

	assert.Equal(s.T(), true, s.sut.getHandshakeTimerRunning())
	assert.Equal(s.T(), model.SmeHelloStatePendingListen, s.sut.getState())
}

func (s *HelloSuite) Test_PendingListen_PendingProlongation() {
	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase:               model.ConnectionHelloPhaseTypePending,
			ProlongationRequest: util.Ptr(true),
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleShipMessage(false, msg)

	assert.Equal(s.T(), true, s.sut.getHandshakeTimerRunning())
	assert.Equal(s.T(), model.SmeHelloStatePendingListen, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *HelloSuite) Test_HelloSend_Failure() {
	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	s.setWSReturnError()

	err := s.sut.handshakeHelloSend(model.ConnectionHelloPhaseTypeAborted, 0, false)
	assert.NotNil(s.T(), err)
}
