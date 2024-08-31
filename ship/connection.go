package ship

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/model"
	"github.com/enbility/ship-go/util"
)

// A ShipConnection handles the data connection and coordinates SHIP and SPINE messages i/o
type ShipConnection struct {
	// The ship connection mode of this connection
	role shipRole

	// The remote SKI
	remoteSKI string

	// the remote SHIP Id
	remoteShipID string

	// The local SHIP ID
	localShipID string

	// data provider
	infoProvider api.ShipConnectionInfoProviderInterface

	// Where to pass incoming SPINE messages to
	dataReader api.ShipConnectionDataReaderInterface

	// the (web socket) handler for sending messages
	dataWriter api.WebsocketDataWriterInterface

	// The current SHIP state
	smeState model.ShipMessageExchangeState

	// the current error value if SHIP state is in error
	smeError error

	// handles timeouts for the various states
	//
	// WaitForReady SHIP 13.4.4.1.3: The communication partner must send its "READY" state (or request for prolongation") before the timer expires.
	//
	// SendProlongationRequest SHIP 13.4.4.1.3: Local timer to request for prolongation at the communication partner in time (i.e. before the communication partner's Wait-For-Ready-Timer expires).
	//
	// ProlongationRequestReply SHIP 13.4.4.1.3: Detection of response timeout on prolongation request.
	handshakeTimerRunning  bool
	handshakeTimerType     timeoutTimerType
	handshakeTimerStopChan chan struct{}
	handshakeTimerMux      sync.Mutex

	lastReceivedWaitingValue time.Duration // required for Prolong-Request-Reply-Timer

	shutdownOnce sync.Once

	// buffer for SPINE messages that came in before the handshake was completed
	spineBuffer [][]byte

	mux       sync.Mutex
	bufferMux sync.Mutex
}

var _ api.ShipConnectionInterface = (*ShipConnection)(nil)

func NewConnectionHandler(
	dataProvider api.ShipConnectionInfoProviderInterface,
	dataHandler api.WebsocketDataWriterInterface,
	role shipRole,
	localShipID,
	remoteSki,
	remoteShipId string) *ShipConnection {
	ship := &ShipConnection{
		infoProvider: dataProvider,
		dataWriter:   dataHandler,
		role:         role,
		localShipID:  localShipID,
		remoteSKI:    remoteSki,
		remoteShipID: remoteShipId,
		smeState:     model.CmiStateInitStart,
		smeError:     nil,
	}

	ship.handshakeTimerStopChan = make(chan struct{})

	if dataHandler != nil {
		dataHandler.InitDataProcessing(ship)
	}

	return ship
}

func (c *ShipConnection) RemoteSKI() string {
	return c.remoteSKI
}

func (c *ShipConnection) DataHandler() api.WebsocketDataWriterInterface {
	return c.dataWriter
}

// start SHIP communication
func (c *ShipConnection) Run() {
	c.handleShipMessage(false, nil)
}

// provides the current ship state and error value if the state is in error
func (c *ShipConnection) ShipHandshakeState() (model.ShipMessageExchangeState, error) {
	return c.getState(), c.smeError
}

// invoked when pairing for a pending request is approved
func (c *ShipConnection) ApprovePendingHandshake() {
	state := c.getState()
	if state != model.SmeHelloStatePendingListen {
		// TODO: what to do if the state is different?

		return
	}

	// TODO: move this into hs_hello.go and add tests

	// HELLO_OK
	c.stopHandshakeTimer()
	c.setAndHandleState(model.SmeHelloStateReadyInit)

	// TODO: check if we need to do some validations before moving on to the next state
	c.setAndHandleState(model.SmeHelloStateOk)
}

// invoked when pairing for a pending request is denied
func (c *ShipConnection) AbortPendingHandshake() {
	state := c.getState()
	if state != model.SmeHelloStatePendingListen && state != model.SmeHelloStateReadyListen {
		// TODO: what to do if the state is differnet?

		return
	}

	// TODO: Move this into hs_hello.go and add tests

	c.stopHandshakeTimer()
	c.setAndHandleState(model.SmeHelloStateAbort)
}

// close this ship connection
func (c *ShipConnection) CloseConnection(safe bool, code int, reason string) {
	c.shutdownOnce.Do(func() {
		c.stopHandshakeTimer()

		// handshake is completed if approved or aborted
		state := c.getState()
		handshakeEnd := state == model.SmeStateComplete ||
			state == model.SmeHelloStateAbortDone ||
			state == model.SmeHelloStateRemoteAbortDone ||
			state == model.SmeHelloStateRejected

		// this may not be used for Connection Data Exchange is entered!
		if safe && state == model.SmeStateComplete {
			// SHIP 13.4.7: Connection Termination Announce
			closeMessage := model.ConnectionClose{
				ConnectionClose: model.ConnectionCloseType{
					Phase:   model.ConnectionClosePhaseTypeAnnounce,
					MaxTime: util.Ptr(uint(500)),
					Reason:  util.Ptr(model.ConnectionCloseReasonType(reason)),
				},
			}

			_ = c.sendShipModel(model.MsgTypeEnd, closeMessage)

			go func() {
				// wait a bit to let it send
				<-time.After(500 * time.Millisecond)

				//
				c.dataWriter.CloseDataConnection(4001, "close")
				c.infoProvider.HandleConnectionClosed(c, handshakeEnd)
			}()
			return
		}

		closeCode := 4001
		if code != 0 {
			closeCode = code
		}
		c.dataWriter.CloseDataConnection(closeCode, reason)

		c.infoProvider.HandleConnectionClosed(c, handshakeEnd)
	})
}

var _ api.ShipConnectionDataWriterInterface = (*ShipConnection)(nil)

// SpineDataConnection interface implementation
func (c *ShipConnection) WriteShipMessageWithPayload(message []byte) {
	if err := c.sendSpineData(message); err != nil {
		logging.Log().Debug(c.RemoteSKI(), "Error sending spine message: ", err)
		return
	}
}

var _ api.WebsocketDataReaderInterface = (*ShipConnection)(nil)

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

// process any SPINE messages that came in before the handshake completed
// this will be called once the handshake is completed and
// spineDataProcessing is set
func (c *ShipConnection) processBufferedSpineMessages() {
	c.bufferMux.Lock()
	defer c.bufferMux.Unlock()

	for _, item := range c.spineBuffer {
		c.dataReader.HandleShipPayloadMessage(item)
	}

	c.spineBuffer = nil
}

// route the incoming message to either SHIP or SPINE message handlers
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

// checks wether the provided messages is a SHIP message
func (c *ShipConnection) hasSpineDatagram(message []byte) bool {
	return bytes.Contains(message, []byte("datagram"))
}

// the websocket data connection was closed from remote
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

const payloadPlaceholder = `{"place":"holder"}`

func (c *ShipConnection) transformSpineDataIntoShipJson(data []byte) ([]byte, error) {
	spineMsg, err := JsonIntoEEBUSJson(data)
	if err != nil {
		return nil, err
	}

	payload := json.RawMessage([]byte(spineMsg))

	// Workaround for the fact that SHIP payload is a json.RawMessage
	// which would also be transformed into an array element but it shouldn't
	// hence patching the payload into the message later after the SHIP
	// and SPINE model are transformed independently

	// Create the message
	shipMessage := model.ShipData{
		Data: model.DataType{
			Header: model.HeaderType{
				ProtocolId: model.ShipProtocolId,
			},
			Payload: json.RawMessage([]byte(payloadPlaceholder)),
		},
	}

	msg, err := json.Marshal(shipMessage)
	if err != nil {
		return nil, err
	}

	eebusMsg, err := JsonIntoEEBUSJson(msg)
	if err != nil {
		return nil, err
	}

	eebusMsg = strings.ReplaceAll(eebusMsg, `[`+payloadPlaceholder+`]`, string(payload))

	return []byte(eebusMsg), nil
}

func (c *ShipConnection) sendSpineData(data []byte) error {
	eebusMsg, err := c.transformSpineDataIntoShipJson(data)
	if err != nil {
		return err
	}

	if isClosed, err := c.dataWriter.IsDataConnectionClosed(); isClosed {
		c.CloseConnection(false, 0, "")
		return err
	}

	// Wrap the message into a binary message with the ship header
	shipMsg := []byte{model.MsgTypeData}
	shipMsg = append(shipMsg, eebusMsg...)

	err = c.dataWriter.WriteMessageToWebsocketConnection(shipMsg)
	if err != nil {
		logging.Log().Debug("error sending message: ", err)
		return err
	}

	return nil
}

// send a json message for a provided model to the websocket connection
func (c *ShipConnection) sendShipModel(typ byte, model interface{}) error {
	shipMsg, err := c.shipMessage(typ, model)
	if err != nil {
		return err
	}

	err = c.dataWriter.WriteMessageToWebsocketConnection(shipMsg)
	if err != nil {
		return err
	}

	return nil
}

// Process a SHIP Json message
func (c *ShipConnection) processShipJsonMessage(message []byte, target any) error {
	_, data := c.parseMessage(message, true)

	return json.Unmarshal(data, &target)
}

// transform a SHIP model into EEBUS specific JSON
func (c *ShipConnection) shipMessage(typ byte, model interface{}) ([]byte, error) {
	if isClosed, err := c.dataWriter.IsDataConnectionClosed(); isClosed {
		c.CloseConnection(false, 0, "")
		return nil, err
	}

	if model == nil {
		return nil, errors.New("invalid data")
	}

	msg, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}

	eebusMsg, err := JsonIntoEEBUSJson(msg)
	if err != nil {
		return nil, err
	}

	// Wrap the message into a binary message with the ship header
	shipMsg := []byte{typ}
	shipMsg = append(shipMsg, eebusMsg...)

	return shipMsg, nil
}

// return the SHIP message type, the SHIP message and an error
//
// enable jsonFormat if the return message is expected to be encoded in the eebus json format
func (c *ShipConnection) parseMessage(msg []byte, jsonFormat bool) (byte, []byte) {
	if len(msg) == 0 {
		return 0, nil
	}

	// Extract the SHIP header byte
	shipHeaderByte := msg[0]
	// remove the SHIP header byte from the message
	msg = msg[1:]

	if jsonFormat {
		return shipHeaderByte, JsonFromEEBUSJson(msg)
	}

	return shipHeaderByte, msg
}
