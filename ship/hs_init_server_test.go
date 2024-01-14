package ship

import (
	"testing"

	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestInitServerSuite(t *testing.T) {
	suite.Run(t, new(InitServerSuite))
}

type InitServerSuite struct {
	suite.Suite
	role shipRole
}

func (s *InitServerSuite) BeforeTest(suiteName, testName string) {
	s.role = ShipRoleServer
}

func (s *InitServerSuite) Test_Init() {
	sut, _ := initTest(s.role)

	assert.Equal(s.T(), model.CmiStateInitStart, sut.getState())

	shutdownTest(sut)
}

func (s *InitServerSuite) Test_Start() {
	sut, _ := initTest(s.role)

	sut.setState(model.CmiStateInitStart, nil)

	sut.handleState(false, nil)

	assert.Equal(s.T(), true, sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.CmiStateServerWait, sut.getState())

	shutdownTest(sut)
}

func (s *InitServerSuite) Test_ServerWait() {
	sut, data := initTest(s.role)

	sut.setState(model.CmiStateServerWait, nil)

	sut.handleState(false, model.ShipInit)

	// the state goes from smeHelloState directly to smeHelloStateReadyInit to smeHelloStateReadyListen
	assert.Equal(s.T(), model.SmeHelloStateReadyListen, sut.getState())
	assert.NotNil(s.T(), data.lastMessage())

	shutdownTest(sut)
}

func (s *InitServerSuite) Test_ServerWait_InvalidMsgType() {
	sut, data := initTest(s.role)

	sut.setState(model.CmiStateServerWait, nil)

	sut.handleState(false, []byte{0x05, 0x00})

	assert.Equal(s.T(), model.SmeStateError, sut.getState())
	assert.Nil(s.T(), data.lastMessage())

	shutdownTest(sut)
}

func (s *InitServerSuite) Test_ServerWait_InvalidData() {
	sut, data := initTest(s.role)

	sut.setState(model.CmiStateServerWait, nil)

	sut.handleState(false, []byte{model.MsgTypeInit, 0x05})

	assert.Equal(s.T(), model.SmeStateError, sut.getState())
	assert.Nil(s.T(), data.lastMessage())

	shutdownTest(sut)
}
