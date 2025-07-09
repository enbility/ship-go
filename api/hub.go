package api

//go:generate mockery
//go:generate mockgen -destination=../mocks/mockgen_api.go -package=mocks github.com/enbility/ship-go/api MdnsInterface,HubReaderInterface

/* Hub */

// Interface for handling the server and remote connections
type HubInterface interface {
	// Start the ConnectionsHub with all its services
	Start() error

	// close all connections
	Shutdown()

	// return the service for a SKI
	ServiceForSKI(ski string) *ServiceDetails

	// Provide the current pairing state for a SKI
	PairingDetailForSki(ski string) *ConnectionStateDetail

	// Enables or disables to automatically accept incoming pairing and connection requests
	//
	// Default: false
	SetAutoAccept(bool)

	// Pair a remote service based on the SKI
	//
	// Parameters:
	// - ski: the SKI of the remote service (required)
	// - shipID: the SHIP ID of the remote service (optional)
	//
	// Note: The SHIP ID is optional, but should be provided if available.
	// if provided, it will be used to validate the remote service is
	// providing this SHIP ID during the handshake process and will reject
	// the connection if it does not match.
	RegisterRemoteSKI(ski, shipID string)

	// Unpair the SKI
	UnregisterRemoteSKI(ski string)

	// Disconnect a connection to an SKI
	DisconnectSKI(ski string, reason string)

	// Cancels the pairing process for a SKI
	CancelPairingWithSKI(ski string)
}

// Interface to pass information from the hub to the eebus service
//
// Implemented by eebus service implementation, used by Hub
type HubReaderInterface interface {
	// report a connection to a SKI
	RemoteSKIConnected(ski string)

	// report a disconnection to a SKI
	RemoteSKIDisconnected(ski string)

	// report an approved handshake by a remote device
	SetupRemoteDevice(ski string, writeI ShipConnectionDataWriterInterface) ShipConnectionDataReaderInterface

	// report all currently visible EEBUS services
	VisibleRemoteServicesUpdated(entries []RemoteService)

	// Provides the SHIP ID the remote service reported during the handshake process
	// This needs to be persisted and passed on for future remote service connections
	// when using `RegisterRemoteSKI`
	ServiceShipIDUpdate(ski string, shipdID string)

	// Provides the current pairing state for the remote service
	// This is called whenever the state changes and can be used to
	// provide user information for the pairing/connection process
	ServicePairingDetailUpdate(ski string, detail *ConnectionStateDetail)

	// return if the user is still able to trust the connection
	AllowWaitingForTrust(ski string) bool
}
