package ship

import (
	"testing"

	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestInitClientSuite(t *testing.T) {
	suite.Run(t, new(InitClientSuite))
}

type InitClientSuite struct {
	suite.Suite
	role shipRole
}

func (s *InitClientSuite) BeforeTest(suiteName, testName string) {
	s.role = ShipRoleClient
}

func (s *InitClientSuite) Test_Init() {
	sut, _ := initTest(s.role)

	assert.Equal(s.T(), model.CmiStateInitStart, sut.getState())

	shutdownTest(sut)
}

func (s *InitClientSuite) Test_Start() {
	sut, data := initTest(s.role)

	sut.setState(model.CmiStateInitStart, nil)

	sut.handleState(false, nil)

	assert.Equal(s.T(), true, sut.handshakeTimerRunning)
	assert.Equal(s.T(), model.CmiStateClientWait, sut.getState())
	assert.NotNil(s.T(), data.lastMessage())
	assert.Equal(s.T(), model.ShipInit, data.lastMessage())

	shutdownTest(sut)
}

func (s *InitClientSuite) Test_ClientWait() {
	sut, data := initTest(s.role)

	sut.setState(model.CmiStateClientWait, nil)

	sut.handleState(false, model.ShipInit)

	// the state goes from smeHelloState directly to smeHelloStateReadyInit to smeHelloStateReadyListen
	assert.Equal(s.T(), model.SmeHelloStateReadyListen, sut.getState())
	assert.NotNil(s.T(), data.lastMessage())

	shutdownTest(sut)
}

func (s *InitClientSuite) Test_ClientWait_Timeout() {
	sut, data := initTest(s.role)

	sut.setState(model.CmiStateClientWait, nil)

	sut.handleState(true, nil)

	assert.Equal(s.T(), model.SmeStateError, sut.getState())
	assert.Nil(s.T(), data.lastMessage())
	assert.Equal(s.T(), data.handleConnectionClosedInvoked, true)

	shutdownTest(sut)
}

func (s *InitClientSuite) Test_ClientWait_InvalidMsgType() {
	sut, data := initTest(s.role)

	sut.setState(model.CmiStateClientWait, nil)

	sut.handleState(false, []byte{0x05, 0x00})

	assert.Equal(s.T(), model.SmeStateError, sut.getState())
	assert.Nil(s.T(), data.lastMessage())

	shutdownTest(sut)
}

func (s *InitClientSuite) Test_ClientWait_InvalidData() {
	sut, data := initTest(s.role)

	sut.setState(model.CmiStateClientWait, nil)

	sut.handleState(false, []byte{model.MsgTypeInit, 0x05})

	assert.Equal(s.T(), model.SmeStateError, sut.getState())
	assert.Nil(s.T(), data.lastMessage())

	shutdownTest(sut)
}
