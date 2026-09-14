package ship

import (
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

// getDataReader returns the application's SPINE reader: nil before connection data exchange is
// entered, or if the application does not process SPINE data
func (c *ShipConnection) getDataReader() api.ShipConnectionDataReaderInterface {
	c.mux.Lock()
	defer c.mux.Unlock()

	return c.dataReader
}

// setDataReader stores the reader returned by SetupRemoteService. The websocket reader goroutine
// reads it for every SPINE message, so it is guarded by mux like the SHIP state.
func (c *ShipConnection) setDataReader(reader api.ShipConnectionDataReaderInterface) {
	c.mux.Lock()
	defer c.mux.Unlock()

	c.dataReader = reader
}

// HandleIncomingWebsocketMessage routes the incoming message to either SHIP or SPINE message handlers
func (c *ShipConnection) HandleIncomingWebsocketMessage(message []byte) {
	// SHIP 13.4.5.2.1: SPINE data is carried in "data" messages with MessageType 0x02. Route on
	// that header byte: scanning the content would misroute SME control messages that happen to
	// contain "datagram", e.g. in a SHIP ID.
	if len(message) == 0 || message[0] != model.MsgTypeData {
		c.handleShipMessage(false, message)
		return
	}

	// decide before parsing, so data that is dropped costs nothing
	if state := c.getState(); !isDataExchangeState(state) {
		// SHIP 13.4.4.3: data exchange is only enabled once PIN verification succeeded, so data
		// before that, or after the connection failed, is a protocol violation. Drop it, like
		// other messages that are invalid in the current state.
		c.droppedDataLogOnce.Do(func() {
			logging.Log().Debug(c.RemoteSKI(), "dropping SPINE data received outside connection data exchange, state:", state)
		})
		return
	}

	reader := c.getDataReader()
	if reader == nil {
		// the application does not process SPINE data on this connection
		return
	}

	data, err := c.shipModelFromMessage(message)
	if err != nil {
		return
	}

	// pass the payload to the SPINE read handler
	reader.HandleShipPayloadMessage([]byte(data.Data.Payload))
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
