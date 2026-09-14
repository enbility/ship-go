package ship

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/model"
)

// Access methods identification (SHIP 13.4.6) covers the state SmeAccessMethodsRequest.
//
// It is not part of the handshake. SHIP 13.4.6.2: the state "can run in parallel to connection
// data exchange" and "MUST NOT be entered before connection data exchange is entered". Connection
// data exchange is entered once PIN verification succeeded (SHIP 13.4.4.3), which is when
// handshakeAccessMethods_Init runs, and SPINE data is processed from then on without waiting for
// the access methods exchange (SHIP IG Transport and Connectivity 2.1).
//
// ship-go always requests the remote's access methods, and only reports the connection as
// complete once the reply arrived: it carries the remote SHIP ID, the primary identifier in the
// trust store (SHIP Pairing Service 10.4), which is verified against the SHIP ID trust was
// established for. The recipient of the request SHALL reply (SHIP 13.4.6.2.1), so a remote that
// does not reply within getAccessMethodsTimeout() is disconnected.

// handshakeAccessMethods_Init enters connection data exchange once PIN verification succeeded
func (c *ShipConnection) handshakeAccessMethods_Init() {
	accessMethodsRequest := model.AccessMethodsRequest{
		AccessMethodsRequest: model.AccessMethodsRequestType{},
	}

	if err := c.sendShipModel(model.MsgTypeControl, accessMethodsRequest); err != nil {
		c.endHandshakeWithError(err)
		return
	}

	// SHIP IG Transport and Connectivity 2.1 "Immediate readiness": set up SPINE processing now,
	// without waiting for the remote's reply to our request. This is the only place the
	// application is handed the connection.
	c.setDataReader(c.infoProvider.SetupRemoteService(c.remoteSKI, c))

	c.setHandshakeTimer(timeoutTimerTypeWaitForReady, getAccessMethodsTimeout())
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

	// If we already know the remote ID, verify it matches: a remote that authenticated with a
	// trusted certificate but reports another SHIP ID is not the node trust was established for
	if len(c.remoteShipID) > 0 && c.remoteShipID != remoteID {
		return fmt.Errorf("%w for remote SKI %s: expected '%s', got '%s'",
			api.ErrShipIDMismatch, c.remoteSKI, c.remoteShipID, remoteID)
	}

	// Save and report the SHIP ID if this is the first time we see it
	if len(c.remoteShipID) == 0 {
		c.remoteShipID = remoteID
		c.infoProvider.ReportServiceShipID(c.remoteSKI, c.remoteShipID)
	}

	return nil
}

// handleDataExchangeSmeMessage handles SME control messages in connection data exchange: while
// our own access methods request is pending (SmeAccessMethodsRequest), and afterwards
// (SmeStateComplete).
//
// Anything other than an access methods message is logged and ignored instead of ending a
// working connection (SHIP 13.4.5.1: gracefully skip unknown content). Only the access methods
// exchange itself can end the connection, and it does so with a SHIP 13.4.7 termination.
func (c *ShipConnection) handleDataExchangeSmeMessage(message []byte) {
	if len(message) == 0 {
		return
	}

	_, data := c.parseMessage(message, true)

	msgType, err := detectAccessMethodsMessageType(data)
	if err != nil {
		logging.Log().Debug(c.RemoteSKI(), "ignoring SME message in connection data exchange:", err)
		return
	}

	switch msgType {
	case "request":
		// SHIP 13.4.6.2.1: the recipient SHALL respond, whatever the state of our own request.
		// SHIP IG Transport and Connectivity 2.1 "Decoupled SME responses": answer immediately.
		if err := c.handleAccessMethodsRequest(); err != nil {
			c.endDataExchangeWithError(err)
		}

	case "methods":
		// SHIP 13.4.6.2.1: "access methods" SHALL ONLY be sent upon a request, and we send ours
		// once, so anything but the reply to it is unsolicited
		if c.getState() != model.SmeAccessMethodsRequest {
			logging.Log().Debug(c.RemoteSKI(), "ignoring unsolicited accessMethods message")
			return
		}

		// This is the reply to our only request: an unusable reply will not be followed by a
		// better one, so end the connection now instead of waiting for the timeout
		var accessMethods model.AccessMethods
		if err := json.Unmarshal(data, &accessMethods); err != nil {
			c.endDataExchangeWithError(err)
			return
		}

		if err := c.handleAccessMethodsResponse(&accessMethods); err != nil {
			c.endDataExchangeWithError(err)
			return
		}

		c.setState(model.SmeStateApproved, nil)
		c.approveHandshake()
	}
}
