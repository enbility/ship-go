package api

import (
	"github.com/enbility/ship-go/model"
)

/* ShipConnection */

type ShipConnectionInterface interface {
	DataHandler() WebsocketDataWriterInterface
	CloseConnection(safe bool, code int, reason string)
	RemoteSKI() string
	ApprovePendingHandshake()
	AbortPendingHandshake()
	ShipHandshakeState() (model.ShipMessageExchangeState, error)
}

// interface for getting service wide information
//
// implemented by Hub, used by shipConnection
type ShipConnectionInfoProviderInterface interface {
	// check if the SKI is paired
	IsRemoteServiceForSKIPaired(string) bool

	// report closing of a connection and if handshake did complete
	HandleConnectionClosed(ShipConnectionInterface, bool)

	// report the ship ID provided during the handshake
	ReportServiceShipID(string, string)

	// check if the user is still able to trust the connection
	AllowWaitingForTrust(string) bool

	// report the updated SHIP handshake state and optional error message for a SKI
	HandleShipHandshakeStateUpdate(string, model.ShipState)

	// report an approved handshake by a remote device
	SetupRemoteDevice(ski string, writeI ShipConnectionDataWriterInterface) ShipConnectionDataReaderInterface
}

// Used to pass an outgoing SPINE message from a DeviceLocal to the SHIP connection
//
// Implemented by ShipConnection, used by spine DeviceLocal
type ShipConnectionDataWriterInterface interface {
	WriteShipMessageWithPayload(message []byte)
}

// Used to pass an incoming SPINE message from a SHIP connection to the proper DeviceRemote
//
// Implemented by spine DeviceRemote, used by ShipConnection
type ShipConnectionDataReaderInterface interface {
	HandleShipPayloadMessage(message []byte)
}
