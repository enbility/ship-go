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

func TestHelloClientSuite(t *testing.T) {
	suite.Run(t, new(HelloClientSuite))
}

// Hello Client role specific tests
type HelloClientSuite struct {
	suite.Suite

	mockWSWrite  *mocks.WebsocketDataWriterInterface
	mockShipInfo *mocks.ShipConnectionInfoProviderInterface

	sut *ShipConnection

	sentMessage     []byte
	wsReturnFailure error

	currentTestName string

	mux sync.Mutex
}

func (s *HelloClientSuite) lastMessage() []byte {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.sentMessage
}

func (s *HelloClientSuite) setWSReturnError() {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.wsReturnFailure = errors.New("invalid")
}

func (s *HelloClientSuite) BeforeTest(suiteName, testName string) {
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

func (s *HelloClientSuite) AfterTest(suiteName, testName string) {
	s.sut.stopHandshakeTimer()
}

func (s *HelloClientSuite) Test_InitialState() {
	s.sut.setState(model.SmeHelloState, nil)
	s.sut.handleState(false, nil)

	assert.Equal(s.T(), true, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.SmeHelloStateReadyListen, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *HelloClientSuite) Test_ReadyListen_Ok() {
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

	// the state goes from smeHelloStateOk directly to smeProtHStateClientInit to smeProtHStateClientListenChoice
	assert.Equal(s.T(), model.SmeProtHStateClientListenChoice, s.sut.getState())
}

func (s *HelloClientSuite) Test_ReadyListen_ShipFailure() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStateReadyListen, nil)

	msg, err := s.sut.shipMessage(model.MsgTypeControl, []byte{0x5, 0x5, 0x5})
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), msg)

	s.sut.handleState(false, msg)

	// the state goes from smeHelloStateOk directly to smeProtHStateClientInit to smeProtHStateClientListenChoice
	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.getState())
}

func (s *HelloClientSuite) Test_ReadyListen_PhaseFailure() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStateReadyListen, nil)

	helloMsg := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase: model.ConnectionHelloPhaseType("invalid"),
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, helloMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleState(false, msg)

	// the state goes from smeHelloStateOk directly to smeProtHStateClientInit to smeProtHStateClientListenChoice
	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.getState())
}

func (s *HelloClientSuite) Test_Abort() {
	s.sut.setState(model.SmeHelloStateReadyInit, nil) // inits the timer
	s.sut.setState(model.SmeHelloStateReadyListen, nil)

	s.setWSReturnError()

	s.sut.handshakeHello_Abort()

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
}
