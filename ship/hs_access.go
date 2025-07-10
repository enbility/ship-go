package ship

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/enbility/ship-go/model"
)

// Handshake Access covers the states smeAccess...

func (c *ShipConnection) handshakeAccessMethods_Init() {
	// Access Methods
	accessMethodsRequest := model.AccessMethodsRequest{
		AccessMethodsRequest: model.AccessMethodsRequestType{},
	}

	if err := c.sendShipModel(model.MsgTypeControl, accessMethodsRequest); err != nil {
		c.endHandshakeWithError(err)
		return
	}

	c.setHandshakeTimer(timeoutTimerTypeWaitForReady, cmiTimeout)
	c.setState(model.SmeAccessMethodsRequest, nil)
}

// detectAccessMethodsMessageType determines the type of access methods message
// by parsing the JSON and checking which fields are present
func detectAccessMethodsMessageType(data []byte) (string, error) {
	var detector map[string]json.RawMessage
	if err := json.Unmarshal(data, &detector); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}

	// Check for accessMethodsRequest first
	if _, hasRequest := detector["accessMethodsRequest"]; hasRequest {
		return "request", nil
	}

	// Check for accessMethods
	if _, hasMethods := detector["accessMethods"]; hasMethods {
		return "methods", nil
	}

	return "", errors.New("unknown access message type: expected accessMethodsRequest or accessMethods")
}

// handleAccessMethodsRequest processes an incoming access methods request
// by sending back our local access methods
func (c *ShipConnection) handleAccessMethodsRequest() error {
	accessMethods := model.AccessMethods{
		AccessMethods: model.AccessMethodsType{
			Id: &c.localShipID,
		},
	}

	return c.sendShipModel(model.MsgTypeControl, accessMethods)
}

// handleAccessMethodsResponse processes an incoming access methods response
// by validating and storing the remote SHIP ID
func (c *ShipConnection) handleAccessMethodsResponse(accessMethods *model.AccessMethods) error {
	if accessMethods.AccessMethods.Id == nil {
		return fmt.Errorf("access methods response from remote SKI %s does not contain SHIP ID", c.remoteSKI)
	}

	remoteID := *accessMethods.AccessMethods.Id

	// If we already know the remote ID, verify it matches
	if len(c.remoteShipID) > 0 && c.remoteShipID != remoteID {
		return fmt.Errorf("SHIP ID mismatch for remote SKI %s: expected '%s', got '%s'",
			c.remoteSKI, c.remoteShipID, remoteID)
	}

	// Save and report the SHIP ID if this is the first time we see it
	if len(c.remoteShipID) == 0 {
		c.remoteShipID = remoteID
		c.infoProvider.ReportServiceShipID(c.remoteSKI, c.remoteShipID)
	}

	return nil
}

func (c *ShipConnection) handshakeAccessMethods_Request(message []byte) {
	_, data := c.parseMessage(message, true)

	// Determine message type using JSON parsing instead of string matching
	msgType, err := detectAccessMethodsMessageType(data)
	if err != nil {
		c.endHandshakeWithError(err)
		return
	}

	switch msgType {
	case "request":
		if err := c.handleAccessMethodsRequest(); err != nil {
			c.endHandshakeWithError(err)
			return
		}
		// Stay in current state waiting for response
		return

	case "methods":
		var accessMethods model.AccessMethods
		if err := json.Unmarshal(data, &accessMethods); err != nil {
			c.endHandshakeWithError(err)
			return
		}

		if err := c.handleAccessMethodsResponse(&accessMethods); err != nil {
			c.endHandshakeWithError(err)
			return
		}

		// Transition to approved state
		c.setState(model.SmeStateApproved, nil)
		c.approveHandshake()
	}
}
