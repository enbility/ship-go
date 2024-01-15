package api

import (
	"net"

	"github.com/enbility/ship-go/model"
)

//go:generate mockery
//go:generate mockgen -destination=../mocks/mockgen_api.go -package=mocks github.com/enbility/ship-go/api MdnsService

type ShipConnection interface {
	DataHandler() WebsocketDataConnection
	CloseConnection(safe bool, code int, reason string)
	RemoteSKI() string
	ApprovePendingHandshake()
	AbortPendingHandshake()
	ShipHandshakeState() (model.ShipMessageExchangeState, error)
}

// interface for getting service wide information
//
// implemented by connectionsHub, used by shipConnection
type ShipServiceDataProvider interface {
	// check if the SKI is paired
	IsRemoteServiceForSKIPaired(string) bool

	// report closing of a connection and if handshake did complete
	HandleConnectionClosed(ShipConnection, bool)

	// report the ship ID provided during the handshake
	ReportServiceShipID(string, string)

	// check if the user is still able to trust the connection
	AllowWaitingForTrust(string) bool

	// report the updated SHIP handshake state and optional error message for a SKI
	HandleShipHandshakeStateUpdate(string, model.ShipState)

	// report an approved handshake by a remote device
	SetupRemoteDevice(ski string, writeI SpineDataConnection) SpineDataProcessing
}

// Used to pass an outgoing SPINE message from a DeviceLocal to the SHIP connection
//
// Implemented by ShipConnection, used by spine DeviceLocal
type SpineDataConnection interface {
	WriteSpineMessage(message []byte)
}

// Used to pass an incoming SPINE message from a SHIP connection to the proper DeviceRemote
//
// Implemented by spine DeviceRemote, used by ShipConnection
type SpineDataProcessing interface {
	HandleIncomingSpineMesssage(message []byte)
}

// interface for handling the actual remote device data connection
//
// implemented by websocketConnection, used by ShipConnection
type WebsocketDataConnection interface {
	// initialize data processing
	InitDataProcessing(WebsocketDataProcessing)

	// send data via the connection to the remote device
	WriteMessageToDataConnection([]byte) error

	// close the data connection
	CloseDataConnection(closeCode int, reason string)

	// report if the data connection is closed and the error if availab le
	IsDataConnectionClosed() (bool, error)
}

// interface for handling incoming data
//
// implemented by shipConnection, used by websocketConnection
type WebsocketDataProcessing interface {
	// called for each incoming message
	HandleIncomingShipMessage([]byte)

	// called if the data connection is closed unsafe
	// e.g. due to connection issues
	ReportConnectionError(error)
}

/* Mdns */

type MdnsEntry struct {
	Name       string
	Ski        string
	Identifier string   // mandatory
	Path       string   // mandatory
	Register   bool     // mandatory
	Brand      string   // optional
	Type       string   // optional
	Model      string   // optional
	Host       string   // mandatory
	Port       int      // mandatory
	Addresses  []net.IP // mandatory
}

// implemented by hubConnection, used by mdns
type MdnsSearch interface {
	ReportMdnsEntries(entries map[string]*MdnsEntry)
}

// implemented by mdns, used by hubConnection
type MdnsService interface {
	SetupMdnsService() error
	ShutdownMdnsService()
	AnnounceMdnsEntry() error
	UnannounceMdnsEntry()
	RegisterMdnsSearch(cb MdnsSearch)
	UnregisterMdnsSearch(cb MdnsSearch)
	SetAutoAccept(bool)
}

type MdnsProvider interface {
	CheckAvailability() bool
	Shutdown()
	Announce(serviceName string, port int, txt []string) error
	Unannounce()
	ResolveEntries(cancelChan chan bool, callback func(elements map[string]string, name, host string, addresses []net.IP, port int, remove bool))
}

const ShipWebsocketSubProtocol = "ship" // SHIP 10.2: sub protocol is required for websocket connections
