package ship

import (
	"errors"
	"testing"
	"time"

	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

func TestConnectionHandshakeSuite(t *testing.T) {
	suite.Run(t, new(ConnectionHandshakeSuite))
}

type ConnectionHandshakeSuite struct {
	ConnectionSuite
}

func (s *ConnectionHandshakeSuite) TestApprovePendingHandshake() {
	s.sut.smeState = model.CmiStateInitStart
	s.sut.ApprovePendingHandshake()
	assert.Equal(s.T(), model.CmiStateInitStart, s.sut.smeState)

	// Test with validation failing (current mock setup returns false)
	s.sut.smeState = model.SmeHelloStatePendingListen
	s.sut.ApprovePendingHandshake()
	// Allow time for state transition to complete
	time.Sleep(50 * time.Millisecond)
	// With validation failing, the handshake should be aborted
	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.smeState)
}

func (s *ConnectionHandshakeSuite) TestApprovePendingHandshake_ValidationSuccess() {
	// Create a new connection with successful validation mocks
	infoProvider := mocks.NewShipConnectionInfoProviderInterface(s.T())
	infoProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Return().Maybe()
	infoProvider.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Return().Maybe()
	infoProvider.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(true).Maybe()
	infoProvider.EXPECT().AllowWaitingForTrust(mock.Anything).Return(true).Maybe()

	wsDataWriter := mocks.NewWebsocketDataWriterInterface(s.T())
	wsDataWriter.EXPECT().InitDataProcessing(mock.Anything).Return().Maybe()
	wsDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()
	wsDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
	wsDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Return().Maybe()

	conn := NewConnectionHandler(infoProvider, wsDataWriter, ShipRoleServer, "localShipId", "remoteSKI", "remoteShipId")

	// Test successful approval when validation passes
	conn.smeState = model.SmeHelloStatePendingListen
	conn.ApprovePendingHandshake()
	// Allow time for state transitions to complete
	time.Sleep(50 * time.Millisecond)
	assert.Equal(s.T(), model.SmeProtHStateServerListenProposal, conn.smeState)
}

func (s *ConnectionHandshakeSuite) TestAbortPendingHandshake() {
	s.sut.smeState = model.CmiStateInitStart
	s.sut.AbortPendingHandshake()
	assert.Equal(s.T(), model.CmiStateInitStart, s.sut.smeState)

	s.sut.smeState = model.SmeHelloStatePendingListen
	s.sut.AbortPendingHandshake()
	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.smeState)
}

func (s *ConnectionHandshakeSuite) Test_HandleErrorState() {
	s.sut.setState(model.SmeStateError, errors.New("error"))

	state, err := s.sut.ShipHandshakeState()
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), model.SmeStateError, state)

	s.sut.handleState(false, []byte{})
}

func (s *ConnectionHandshakeSuite) Test_HandshakeTimer() {
	// Test that timer is started when state is set
	s.sut.setState(model.CmiStateInitStart, nil)
	assert.Equal(s.T(), model.CmiStateInitStart, s.sut.getState())

	// Start timer
	s.sut.setHandshakeTimer(timeoutTimerTypeWaitForReady, time.Duration(time.Millisecond*500))
	assert.Equal(s.T(), true, s.sut.getHandshakeTimerRunning())
	assert.Equal(s.T(), timeoutTimerTypeWaitForReady, s.sut.getHandshakeTimerType())

	// Test what happens when timer expires - directly trigger timeout behavior
	s.sut.handleState(true, nil) // timeout=true
	assert.Equal(s.T(), model.CmiStateServerWait, s.sut.getState())
}

func (s *ConnectionHandshakeSuite) Test_HandleShipCloseMessage() {
	s.sut.handleShipMessage(false, []byte{})
	state, err := s.sut.ShipHandshakeState()
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), model.CmiStateServerWait, state)

	closeMsg := model.ConnectionClose{
		ConnectionClose: model.ConnectionCloseType{
			Phase: model.ConnectionClosePhaseTypeAnnounce,
		},
	}

	msg, err := s.sut.shipMessage(model.MsgTypeControl, closeMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleShipMessage(false, msg)

	closeMsg = model.ConnectionClose{
		ConnectionClose: model.ConnectionCloseType{
			Phase: model.ConnectionClosePhaseTypeConfirm,
		},
	}

	msg, err = s.sut.shipMessage(model.MsgTypeControl, closeMsg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), msg)

	s.sut.handleShipMessage(false, msg)
}