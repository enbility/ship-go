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
	// Don't set AllowWaitingForTrust here - let individual tests set it as needed

	s.sut = NewConnectionHandler(s.mockShipInfo, s.mockWSWrite, ShipRoleServer, "LocalShipID", "RemoveDevice", "RemoteShipID")
}

func (s *HelloSuite) AfterTest(suiteName, testName string) {
	// Close the connection which will properly stop timers and wait for completion
	s.sut.CloseConnection(false, 4001, "test cleanup")
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

	// Always use direct method call to avoid timer race conditions with mocks
	s.sut.handshakeHello_ReadyListen(true, nil)

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

	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(true).Maybe()

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

func (s *HelloSuite) prolongationRequest() []byte {
	msg, err := s.sut.shipMessage(model.MsgTypeControl, model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase:               model.ConnectionHelloPhaseTypePending,
			ProlongationRequest: util.Ptr(true),
		},
	})
	assert.Nil(s.T(), err)

	return msg
}

// assertAnnouncedWaiting checks that the last SME "hello" update announced the given time left
func (s *HelloSuite) assertAnnouncedWaiting(remaining time.Duration) {
	var hello model.ConnectionHello
	assert.Nil(s.T(), s.sut.processShipJsonMessage(s.lastMessage(), &hello))
	assert.Equal(s.T(), model.ConnectionHelloPhaseTypeReady, hello.ConnectionHello.Phase)
	if assert.NotNil(s.T(), hello.ConnectionHello.Waiting) {
		announced := time.Duration(*hello.ConnectionHello.Waiting) * time.Millisecond
		assert.InDelta(s.T(), remaining, announced, float64(100*time.Millisecond))
	}
}

// SHIP 13.4.4.1.3: an accepted prolongation request increases the Wait-For-Ready-Timer by
// T_hello_inc, the first two requests are accepted even when sent back to back, and each update
// announces the time left
func (s *HelloSuite) Test_ReadyListen_Prolongation_IncreasesTimer() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStateReadyListen, nil)
	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(true).Maybe()

	initial, ok := s.sut.handshakeTimerRemaining(timeoutTimerTypeWaitForReady)
	assert.True(s.T(), ok)

	for request := 1; request <= 2; request++ {
		s.sut.handleState(false, s.prolongationRequest())

		remaining, ok := s.sut.handshakeTimerRemaining(timeoutTimerTypeWaitForReady)
		assert.True(s.T(), ok)
		assert.InDelta(s.T(), initial+time.Duration(request)*getHelloIncTimeout(), remaining,
			float64(100*time.Millisecond), "request %d must increase the timer, not restart it", request)
		s.assertAnnouncedWaiting(remaining)
	}

	assert.Equal(s.T(), model.SmeHelloStateReadyListen, s.sut.getState())
}

// SHIP 13.4.4.1.3: the first two prolongation requests are accepted even when the application does
// not allow waiting for trust
func (s *HelloSuite) Test_ReadyListen_Prolongation_FirstTwoAcceptedWithoutWaitingForTrust() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStateReadyListen, nil)
	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(false).Maybe()

	initial, ok := s.sut.handshakeTimerRemaining(timeoutTimerTypeWaitForReady)
	assert.True(s.T(), ok)

	for request := 1; request <= 2; request++ {
		s.sut.handleState(false, s.prolongationRequest())

		remaining, ok := s.sut.handshakeTimerRemaining(timeoutTimerTypeWaitForReady)
		assert.True(s.T(), ok)
		assert.InDelta(s.T(), initial+time.Duration(request)*getHelloIncTimeout(), remaining,
			float64(100*time.Millisecond), "request %d must be accepted", request)
		s.assertAnnouncedWaiting(remaining)
	}
}

// Beyond the first two, a prolongation request is declined while the application does not allow
// waiting for trust: the timer is left as it is, and the update still announces the time left.
// The application is only asked about that request, not about the first two.
func (s *HelloSuite) Test_ReadyListen_Prolongation_DeclinedWhenWaitingForTrustNotAllowed() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStateReadyListen, nil)
	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(false).Once()

	s.sut.handleState(false, s.prolongationRequest())
	s.sut.handleState(false, s.prolongationRequest())

	// stands in for time passing until the deadline is near, so only the application can decline
	s.sut.setHandshakeTimer(timeoutTimerTypeWaitForReady, getHelloInitTimeout()/4)

	before, _ := s.sut.handshakeTimerRemaining(timeoutTimerTypeWaitForReady)
	s.sut.handleState(false, s.prolongationRequest())

	after, ok := s.sut.handshakeTimerRemaining(timeoutTimerTypeWaitForReady)
	assert.True(s.T(), ok)
	assert.LessOrEqual(s.T(), after, before)
	assert.InDelta(s.T(), before, after, float64(100*time.Millisecond))
	s.assertAnnouncedWaiting(after)
}

// A burst of prolongation requests is capped: the first two are accepted as SHIP 13.4.4.1.3
// requires, the rest are declined while more than T_hello_init is left, and every update still
// announces the time left
func (s *HelloSuite) Test_ReadyListen_Prolongation_BurstIsCapped() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStateReadyListen, nil)
	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(true).Maybe()

	initial, ok := s.sut.handshakeTimerRemaining(timeoutTimerTypeWaitForReady)
	assert.True(s.T(), ok)

	for request := 1; request <= 5; request++ {
		s.sut.handleState(false, s.prolongationRequest())

		remaining, _ := s.sut.handshakeTimerRemaining(timeoutTimerTypeWaitForReady)
		s.assertAnnouncedWaiting(remaining)
	}

	remaining, ok := s.sut.handshakeTimerRemaining(timeoutTimerTypeWaitForReady)
	assert.True(s.T(), ok)
	assert.InDelta(s.T(), initial+2*getHelloIncTimeout(), remaining, float64(100*time.Millisecond),
		"only the first two requests of a burst may increase the timer")
	assert.Equal(s.T(), model.SmeHelloStateReadyListen, s.sut.getState())
}

// Once no more than T_hello_init is left, a further prolongation request is accepted again
func (s *HelloSuite) Test_ReadyListen_Prolongation_AcceptedAgainWhenTimeRunsLow() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStateReadyListen, nil)
	s.mockShipInfo.EXPECT().AllowWaitingForTrust(mock.Anything).Return(true)

	s.sut.handleState(false, s.prolongationRequest())
	s.sut.handleState(false, s.prolongationRequest())

	// stands in for time passing until the deadline is near
	left := getHelloInitTimeout() / 4
	s.sut.setHandshakeTimer(timeoutTimerTypeWaitForReady, left)

	s.sut.handleState(false, s.prolongationRequest())

	remaining, ok := s.sut.handshakeTimerRemaining(timeoutTimerTypeWaitForReady)
	assert.True(s.T(), ok)
	assert.InDelta(s.T(), left+getHelloIncTimeout(), remaining, float64(100*time.Millisecond))
	s.assertAnnouncedWaiting(remaining)
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

	// Always use direct method call to avoid timer race conditions with mocks
	s.sut.handshakeHello_PendingListen(true, nil)

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
			Waiting: util.Ptr(uint(getHelloInitTimeout().Milliseconds())),
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
			Waiting: util.Ptr(uint(getHelloInitTimeout().Milliseconds())),
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

// Additional tests for handshakeHello_PendingListen to improve coverage

func (s *HelloSuite) Test_PendingListen_ProcessMessageError() {
	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	// Send invalid JSON that will cause processShipJsonMessage to fail
	invalidMessage := []byte{model.MsgTypeControl, 'i', 'n', 'v', 'a', 'l', 'i', 'd'}

	s.sut.handshakeHello_PendingListen(false, invalidMessage)

	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.getState())
}

func (s *HelloSuite) Test_PendingListen_ReadyWaitingBelowMinimum() {
	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	// Create message with waiting time below minimum threshold (1 second = 1000ms)
	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase:   model.ConnectionHelloPhaseTypeReady,
			Waiting: util.Ptr(uint(500)), // Below tHelloProlongMin (1000ms)
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handshakeHello_PendingListen(false, msg)

	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.getState())
}

func (s *HelloSuite) Test_PendingListen_PendingWaitingBelowMinimum() {
	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	// Create message with waiting time below minimum threshold
	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase:   model.ConnectionHelloPhaseTypePending,
			Waiting: util.Ptr(uint(300)), // Below tHelloProlongMin (1000ms)
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handshakeHello_PendingListen(false, msg)

	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.getState())
}

func (s *HelloSuite) Test_PendingListen_PendingProlongationRequestSendError() {
	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	s.setWSReturnError()

	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase:               model.ConnectionHelloPhaseTypePending,
			ProlongationRequest: util.Ptr(true),
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handshakeHello_PendingListen(false, msg)

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}

func (s *HelloSuite) Test_PendingListen_PendingInvalidCombination() {
	s.sut.setState(model.SmeHelloStatePendingInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStatePendingListen, nil)

	// Create message with neither valid waiting nor prolongation request
	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase:               model.ConnectionHelloPhaseTypePending,
			ProlongationRequest: util.Ptr(false), // false prolongation request
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handshakeHello_PendingListen(false, msg)

	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.getState())
}
