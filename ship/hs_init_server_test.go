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

func TestInitServerSuite(t *testing.T) {
	suite.Run(t, new(InitServerSuite))
}

type InitServerSuite struct {
	suite.Suite

	mockWSWrite  *mocks.WebsocketDataWriterInterface
	mockShipInfo *mocks.ShipConnectionInfoProviderInterface

	sut *ShipConnection

	sentMessage     []byte
	wsReturnFailure error

	currentTestName string

	mux sync.Mutex
}

func (s *InitServerSuite) lastMessage() []byte {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.sentMessage
}

func (s *InitServerSuite) BeforeTest(suiteName, testName string) {
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

func (s *InitServerSuite) AfterTest(suiteName, testName string) {
	s.sut.stopHandshakeTimer()
}

func (s *InitServerSuite) Test_Init() {
	assert.Equal(s.T(), model.CmiStateInitStart, s.sut.getState())
}

func (s *InitServerSuite) Test_Start() {
	s.sut.setState(model.CmiStateInitStart, nil)

	s.sut.handleState(false, nil)

	assert.Equal(s.T(), true, s.sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.CmiStateServerWait, s.sut.getState())
}

func (s *InitServerSuite) Test_ServerWait() {
	s.sut.setState(model.CmiStateServerWait, nil)

	s.sut.handleState(false, model.ShipInit)

	// the state goes from smeHelloState directly to smeHelloStateReadyInit to smeHelloStateReadyListen
	assert.Equal(s.T(), model.SmeHelloStateReadyListen, s.sut.getState())
	assert.NotNil(s.T(), s.lastMessage())
}

func (s *InitServerSuite) Test_ServerWait_InvalidMsgType() {
	s.sut.setState(model.CmiStateServerWait, nil)

	s.sut.handleState(false, []byte{0x05, 0x00})

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}

func (s *InitServerSuite) Test_ServerWait_InvalidData() {
	s.sut.setState(model.CmiStateServerWait, nil)

	s.sut.handleState(false, []byte{model.MsgTypeInit, 0x05})

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}

func (s *InitServerSuite) Test_ServerWait_Timeout() {
	s.sut.setState(model.CmiStateServerWait, nil)

	s.sut.handleState(true, []byte{})

	assert.Equal(s.T(), model.SmeStateError, s.sut.getState())
	assert.Nil(s.T(), s.lastMessage())
}
