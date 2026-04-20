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

// TestIsAlive verifies the IsAlive() invariant used by the hub connection
// registry: true initially, flips to false once CloseConnection has fired.
func (s *ConnectionLifecycleSuite) TestIsAlive() {
	assert.True(s.T(), s.sut.IsAlive(), "fresh connection must be alive")
	s.sut.CloseConnection(false, 0, "test")
	assert.False(s.T(), s.sut.IsAlive(), "IsAlive must return false after CloseConnection")
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