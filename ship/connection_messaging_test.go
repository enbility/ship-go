package ship

import (
	"encoding/json"
	"testing"

	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestConnectionMessagingSuite(t *testing.T) {
	suite.Run(t, new(ConnectionMessagingSuite))
}

type ConnectionMessagingSuite struct {
	ConnectionSuite
}

func (s *ConnectionMessagingSuite) TestShipModelFromMessage() {
	msg := []byte{}
	data, err := s.sut.shipModelFromMessage(msg)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), data)

	modelData := model.ShipData{}
	jsonData, err := json.Marshal(modelData)
	assert.Nil(s.T(), err)

	msg = []byte{0}
	msg = append(msg, jsonData...)
	data, err = s.sut.shipModelFromMessage(msg)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), data)
}

func (s *ConnectionMessagingSuite) TestHandleIncomingShipMessage() {
	modelData := model.ShipData{}
	jsonData, err := json.Marshal(modelData)
	assert.Nil(s.T(), err)

	msg := []byte{0}
	msg = append(msg, jsonData...)

	s.sut.HandleIncomingWebsocketMessage(msg)

	spineData := `{"datagram":{}}`
	jsonData = []byte(spineData)

	modelData = model.ShipData{
		Data: model.DataType{
			Payload: jsonData,
		},
	}
	jsonData, err = json.Marshal(modelData)
	assert.Nil(s.T(), err)

	msg = []byte{0}
	msg = append(msg, jsonData...)

	s.sut.HandleIncomingWebsocketMessage(msg)

	s.sut.dataReader = s.shipConnectionReader

	s.sut.processBufferedSpineMessages()

	s.sut.HandleIncomingWebsocketMessage(msg)
}

func (s *ConnectionMessagingSuite) TestReportConnectionError() {
	s.sut.ReportConnectionError(nil)
	assert.Equal(s.T(), model.SmeStateError, s.sut.smeState)

	s.sut.smeState = model.SmeHelloStateReadyListen
	s.sut.ReportConnectionError(nil)
	assert.Equal(s.T(), model.SmeHelloStateRejected, s.sut.smeState)

	s.sut.smeState = model.SmeHelloStateRemoteAbortDone
	s.sut.ReportConnectionError(nil)
	assert.Equal(s.T(), model.SmeHelloStateRemoteAbortDone, s.sut.smeState)

	s.sut.smeState = model.SmeHelloStateAbort
	s.sut.ReportConnectionError(nil)
	assert.Equal(s.T(), model.SmeHelloStateAbort, s.sut.smeState)
}

func (s *ConnectionMessagingSuite) TestProcessShipJsonMessage() {
	closeMessage := model.ConnectionClose{
		ConnectionClose: model.ConnectionCloseType{
			Phase: model.ConnectionClosePhaseTypeAnnounce,
		},
	}
	msg, err := json.Marshal(closeMessage)
	assert.Nil(s.T(), err)

	newMsg := []byte{model.MsgTypeControl}
	newMsg = append(newMsg, msg...)

	var data any
	err = s.sut.processShipJsonMessage(newMsg, &data)
	assert.Nil(s.T(), err)
}

func (s *ConnectionMessagingSuite) TestSendSpineMessage() {
	data := `{"datagram":{"header":{},"payload":{"cmd":[]}}}`

	err := s.sut.sendSpineData([]byte(data))
	assert.Nil(s.T(), err)
}