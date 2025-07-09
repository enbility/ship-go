package ship

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/model"
)

var _ api.ShipConnectionDataWriterInterface = (*ShipConnection)(nil)

// WriteShipMessageWithPayload sends a SPINE message via SHIP protocol
func (c *ShipConnection) WriteShipMessageWithPayload(message []byte) {
	if err := c.sendSpineData(message); err != nil {
		logging.Log().Debug(c.RemoteSKI(), "Error sending spine message: ", err)
		return
	}
}

var _ api.WebsocketDataReaderInterface = (*ShipConnection)(nil)

// shipModelFromMessage parses a SHIP message into a ShipData model
func (c *ShipConnection) shipModelFromMessage(message []byte) (*model.ShipData, error) {
	_, jsonData := c.parseMessage(message, true)

	// Get the datagram from the message
	data := model.ShipData{}
	if err := json.Unmarshal(jsonData, &data); err != nil {
		logging.Log().Debug(c.RemoteSKI(), "error unmarshalling message: ", err)
		return nil, err
	}

	if data.Data.Payload == nil {
		errorMsg := "received no valid payload"
		logging.Log().Debug(c.RemoteSKI(), errorMsg)
		return nil, errors.New(errorMsg)
	}

	return &data, nil
}

// processBufferedSpineMessages processes any SPINE messages that came in before the handshake completed
// this will be called once the handshake is completed and spineDataProcessing is set
func (c *ShipConnection) processBufferedSpineMessages() {
	c.bufferMux.Lock()
	defer c.bufferMux.Unlock()

	for _, item := range c.spineBuffer {
		c.dataReader.HandleShipPayloadMessage(item)
	}

	c.spineBuffer = nil
}

// HandleIncomingWebsocketMessage routes the incoming message to either SHIP or SPINE message handlers
func (c *ShipConnection) HandleIncomingWebsocketMessage(message []byte) {
	// Check if this is a SHIP SME or SPINE message
	if !c.hasSpineDatagram(message) {
		c.handleShipMessage(false, message)
		return
	}

	data, err := c.shipModelFromMessage(message)
	if err != nil {
		return
	}

	if c.dataReader == nil {
		// buffer message for processing once the handshake is completed
		c.bufferMux.Lock()
		defer c.bufferMux.Unlock()

		c.spineBuffer = append(c.spineBuffer, []byte(data.Data.Payload))

		return
	}

	// pass the payload to the SPINE read handler
	c.dataReader.HandleShipPayloadMessage([]byte(data.Data.Payload))
}

// hasSpineDatagram checks whether the provided message is a SHIP message
func (c *ShipConnection) hasSpineDatagram(message []byte) bool {
	return bytes.Contains(message, []byte("datagram"))
}

// ReportConnectionError handles WebSocket connection errors from remote
func (c *ShipConnection) ReportConnectionError(err error) {
	// if the handshake is aborted, a closed connection is no error
	currentState := c.getState()

	// rejections are also received by sending `{"connectionHello":[{"phase":"pending"},{"waiting":60000}]}`
	// and then closing the websocket connection with `4452: Node rejected by application.`
	if currentState == model.SmeHelloStateReadyListen {
		c.setState(model.SmeHelloStateRejected, nil)
		c.CloseConnection(false, 0, "")
		return
	}

	if currentState == model.SmeHelloStateRemoteAbortDone {
		// remote service should close the connection
		c.CloseConnection(false, 0, "")
		return
	}

	if currentState == model.SmeHelloStateAbort ||
		currentState == model.SmeHelloStateAbortDone {
		c.CloseConnection(false, 4452, "Node rejected by application")
		return
	}

	c.setState(model.SmeStateError, err)

	c.CloseConnection(false, 0, "")

	state := model.ShipState{
		State: model.SmeStateError,
		Error: err,
	}
	c.infoProvider.HandleShipHandshakeStateUpdate(c.remoteSKI, state)
}
