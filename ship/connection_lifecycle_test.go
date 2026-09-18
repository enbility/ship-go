package ship

import (
	"testing"

	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestConnectionLifecycleSuite(t *testing.T) {
	suite.Run(t, new(ConnectionLifecycleSuite))
}

type ConnectionLifecycleSuite struct {
	ConnectionSuite
}

func (s *ConnectionLifecycleSuite) TestRun() {
	s.sut.Run()
	state, err := s.sut.ShipHandshakeState()
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), model.CmiStateServerWait, state)
}

func (s *ConnectionLifecycleSuite) TestShipHandshakeState() {
	state, err := s.sut.ShipHandshakeState()
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), model.CmiStateInitStart, state)
}

func (s *ConnectionLifecycleSuite) TestCloseConnection_StateComplete() {
	s.sut.smeState = model.SmeStateComplete
	s.sut.CloseConnection(true, 450, "User Close")
	state, err := s.sut.ShipHandshakeState()
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), model.SmeStateComplete, state)
}

func (s *ConnectionLifecycleSuite) TestCloseConnection_StateComplete_2() {
	s.sut.smeState = model.SmeStateError
	s.sut.CloseConnection(false, 0, "User Close")
	state, err := s.sut.ShipHandshakeState()
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), model.SmeStateError, state)
}

func (s *ConnectionLifecycleSuite) TestCloseConnection_StateComplete_3() {
	s.sut.smeState = model.SmeStateError
	s.sut.CloseConnection(false, 450, "User Close")
	state, err := s.sut.ShipHandshakeState()
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), model.SmeStateError, state)
}

func (s *ConnectionLifecycleSuite) sentShipMessage() []byte {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.sentMessage
}

// SHIP 13.4.7 termination applies from the moment connection data exchange is entered, which
// includes access methods identification (SHIP 13.4.6.2)
func (s *ConnectionLifecycleSuite) TestCloseConnection_DataExchange_AnnouncesTermination() {
	s.sut.smeState = model.SmeAccessMethodsRequest
	s.sut.CloseConnection(true, 0, "User Close")

	msg := s.sentShipMessage()
	if assert.NotNil(s.T(), msg) {
		assert.Equal(s.T(), model.MsgTypeEnd, msg[0])
	}
}

// Before connection data exchange there is nothing to announce
func (s *ConnectionLifecycleSuite) TestCloseConnection_Handshake_NoAnnounce() {
	s.sut.smeState = model.SmePinStateCheckListen
	s.sut.CloseConnection(true, 0, "User Close")

	assert.Nil(s.T(), s.sentShipMessage())
}

// TC_SHIP_TERM_001: the announced close message must carry a valid SHIP
// ConnectionCloseReasonType. Free-form caller strings (e.g. "User close") map to
// the "unspecific" reason; valid enum values pass through unchanged.
func TestValidCloseReason(t *testing.T) {
	assert.Equal(t, model.ConnectionCloseReasonTypeUnspecific, validCloseReason("User close"))
	assert.Equal(t, model.ConnectionCloseReasonTypeUnspecific, validCloseReason(""))
	assert.Equal(t, model.ConnectionCloseReasonTypeUnspecific,
		validCloseReason(string(model.ConnectionCloseReasonTypeUnspecific)))
	assert.Equal(t, model.ConnectionCloseReasonTypeRemovedconnection,
		validCloseReason(string(model.ConnectionCloseReasonTypeRemovedconnection)))
}
