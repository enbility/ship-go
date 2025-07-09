package ship

import (
	"encoding/json"
	"fmt"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/model"
)

// Handshake Pin covers the states smePin...

func (c *ShipConnection) handshakePin_Init() {
	c.setState(model.SmePinStateCheckInit, nil)

	pinState := model.ConnectionPinState{
		ConnectionPinState: model.ConnectionPinStateType{
			PinState: model.PinStateTypeNone,
		},
	}

	if err := c.sendShipModel(model.MsgTypeControl, pinState); err != nil {
		c.endHandshakeWithError(err)
		return
	}

	c.setState(model.SmePinStateCheckListen, nil)
}

func (c *ShipConnection) handshakePin_smePinStateCheckListen(message []byte) {
	_, data := c.parseMessage(message, true)

	var connectionPinState model.ConnectionPinState
	if err := json.Unmarshal([]byte(data), &connectionPinState); err != nil {
		c.endHandshakeWithError(err)
		return
	}

	switch connectionPinState.ConnectionPinState.PinState {
	case model.PinStateTypeNone:
		c.setAndHandleState(model.SmePinStateCheckOk)
	case model.PinStateTypeRequired:
		c.endHandshakeWithError(fmt.Errorf("%w: 'required' for remote SKI %s", api.ErrUnsupportedPinState, c.remoteSKI))
	case model.PinStateTypeOptional:
		c.endHandshakeWithError(fmt.Errorf("%w: 'optional' for remote SKI %s", api.ErrUnsupportedPinState, c.remoteSKI))
	case model.PinStateTypePinOk:
		c.endHandshakeWithError(fmt.Errorf("%w: 'ok' for remote SKI %s", api.ErrUnsupportedPinState, c.remoteSKI))
	default:
		c.endHandshakeWithError(fmt.Errorf("%w: '%v' from remote SKI %s", 
			api.ErrUnsupportedPinState, connectionPinState.ConnectionPinState.PinState, c.remoteSKI))
	}
}
