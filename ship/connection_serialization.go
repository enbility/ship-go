package ship

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/model"
)

const payloadPlaceholder = `{"place":"holder"}`

// transformSpineDataIntoShipJson transforms SPINE data into SHIP JSON format
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

// sendSpineData sends SPINE data via SHIP protocol
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

// sendShipModel sends a json message for a provided model to the websocket connection
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

// processShipJsonMessage processes a SHIP Json message
func (c *ShipConnection) processShipJsonMessage(message []byte, target any) error {
	_, data := c.parseMessage(message, true)

	return json.Unmarshal(data, &target)
}

// shipMessage transforms a SHIP model into EEBUS specific JSON
func (c *ShipConnection) shipMessage(typ byte, model interface{}) ([]byte, error) {
	if isClosed, err := c.dataWriter.IsDataConnectionClosed(); isClosed {
		c.CloseConnection(false, 0, "")
		return nil, err
	}

	if model == nil {
		return nil, fmt.Errorf("%w from remote SKI %s: model is nil", api.ErrInvalidProtocolMessage, c.remoteSKI)
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

// parseMessage returns the SHIP message type, the SHIP message and an error
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
