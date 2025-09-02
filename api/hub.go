package api

//go:generate mockery
//go:generate mockgen -destination=../mocks/mockgen_api.go -package=mocks github.com/enbility/ship-go/api MdnsInterface,HubReaderInterface

/* Hub */

// Interface for handling the server and remote connections
// BREAKING CHANGE v0.8.0: All methods now use ServiceIdentity instead of string parameters
type HubInterface interface {
	// Start the ConnectionsHub with all its services
	Start() error

	// close all connections
	Shutdown()

	// Enables or disables to automatically accept incoming pairing and connection requests
	//
	// Default: false
	SetAutoAccept(bool)

	// Provide the current pairing state for a ServiceIdentity
	//
	// Parameters:
	// - identity: ServiceIdentity containing SKI, fingerprint, and/or SHIP ID
	//
	// returns:
	//	ErrNotPaired if the service is not in the (to be) paired list
	//	ErrNoConnectionFound if no connection for the service was found
	PairingDetailFor(identity ServiceIdentity) *ConnectionStateDetail

	// Pair a remote service using ServiceIdentity
	//
	// Parameters:
	// - identity: ServiceIdentity containing SKI, fingerprint, and/or SHIP ID
	//
	// Note: The SHIP ID is optional, but should be provided if available.
	// if provided, it will be used to validate the remote service is
	// providing this SHIP ID during the handshake process and will reject
	// the connection if it does not match.
	RegisterRemoteService(identity ServiceIdentity)

	// Unpair a remote service using ServiceIdentity
	//
	// Parameters:
	// - identity: ServiceIdentity containing SKI, fingerprint, and/or SHIP ID
	UnregisterRemoteService(identity ServiceIdentity)

	// Disconnect a connection using ServiceIdentity
	DisconnectService(identity ServiceIdentity, reason string)

	// Cancels the pairing process for a ServiceIdentity
	CancelPairing(identity ServiceIdentity)
}


// Interface to pass information from the hub to the eebus service
//
// Implemented by eebus service implementation, used by Hub
// BREAKING CHANGE v0.8.0: All callbacks now use ServiceIdentity instead of string parameters
type HubReaderInterface interface {
	// report a connection to a remote service
	RemoteServiceConnected(identity ServiceIdentity)

	// report a disconnection from a remote service
	RemoteServiceDisconnected(identity ServiceIdentity)

	// report an approved handshake by a remote service
	SetupRemoteService(identity ServiceIdentity, writeI ShipConnectionDataWriterInterface) ShipConnectionDataReaderInterface

	// report all currently visible EEBUS services
	VisibleRemoteMdnsServicesUpdated(entries []RemoteMdnsService)

	// report that service information has been updated
	// This includes updates to ShipID, fingerprint, or other service details discovered during handshake
	ServiceUpdated(identity ServiceIdentity)

	// Provides the current pairing state for the remote service
	// This is called whenever the state changes and can be used to
	// provide user information for the pairing/connection process
	ServicePairingDetailUpdate(identity ServiceIdentity, detail *ConnectionStateDetail)

	// return if the user is still able to trust the connection
	AllowWaitingForTrust(identity ServiceIdentity) bool
}

