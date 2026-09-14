package api

//go:generate mockery
//go:generate mockgen -destination=../mocks/mockgen_api.go -package=mocks github.com/enbility/ship-go/api MdnsInterface,HubReaderInterface

/* Hub */

// Interface for handling the server and remote connections
// BREAKING CHANGE v0.8.0: All methods now use ServiceIdentity instead of string parameters
type HubInterface interface {
	// Start the ConnectionsHub with all its services
	//
	// Returns error with description of the error that cannot be recovered from
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

	// check if auto accept is true
	IsAutoAcceptEnabled() bool

	// Get Service Details for a ServiceIdentity
	ServiceFor(identity ServiceIdentity) *ServiceDetails

	// Get Service Details for a Service SKI and fingerprint
	ServiceForIdentifier(ski string, fingerprint string) *ServiceDetails

	// Sets the maximum number of simultaneous connections allowed
	// A value of 0 or less will use the default of 10
	SetMaxConnections(maxConnections int)

	// Calculate SHA-256 fingerprint of Hub's certificate
	//
	// Returns:
	//  string:
	//  error: ErrInvalidCertificate if invalid or no certificate was provided
	GetLocalCertificateFingerprint() (string, error)

	// Generate a QR code string for pairing. The generated text will include the SHIP Pairing Service fields if
	// The pairing service is available in listener mode with a secret key provided. Otherwise, standard SHIP QRCode format
	// is generated.
	//
	// Returns:
	//  string:
	//  error: ErrInvalidSecret if the provided secret is invalid
	//  error: ErrSecretTooShort if the provided secret length is too short
	//  error: ErrInvalidCertificate if the provided certificate is invalid
	GeneratePairingQR() (string, error)

	// **************************
	// SHIP Pairing Service APIs
	// **************************

	// SetPairingService configures the optional pairing service
	// Used by devA and devZ.
	SetPairingService(service ShipPairingServiceInterface) error

	// PairingService returns the pairing service
	// Used by devA and devZ.
	PairingService() ShipPairingServiceInterface

	// Start announcing pairing to a specific target device
	// Used by devZ only.
	//
	// Parameters:
	//  - target: Pairing target
	StartAnnouncementTo(target PairingTarget) error

	// Stop announcing pairing to a specific target device
	// Used by devZ only.
	//
	// Parameters:
	//  - shipID: Target SHIP ID
	StopAnnouncementTo(shipID string) error

	// Return true if currently announcing to a specific target device
	// Used by devZ only.
	//
	// Parameters:
	//  - shipID: Target SHIP ID
	IsAnnouncingTo(shipID string) bool

	// SHIP Pairing: Get Active Announcements.
	// Used by devZ only.
	//
	// Returns: List of SHIP IDs currently being announced to
	GetActiveAnnouncements() []string

	// SHIP Pairing: Get the SHIP ID and Fingerprint of controlbox paired via SHIP Pairing
	// Used by devA only.
	//
	// Returns: the ServiceDetails of any trusted AddCu device. Or nil if none
	GetTrustedAddCuDevice() (*ServiceDetails)
}

// Interface to pass information from the hub to the eebus service
//
// Implemented by eebus service implementation, used by Hub
// BREAKING CHANGE v0.8.0: All callbacks now use ServiceIdentity instead of string parameters
type HubReaderInterface interface {
	// report a connection to a remote service
	//
	// Called once the connection is complete: in SHIP connection data exchange, with the remote's
	// SHIP ID verified by the access methods exchange. SetupRemoteService was called before.
	RemoteServiceConnected(identity ServiceIdentity)

	// report a disconnection from a remote service
	//
	// NOTE: The connection may not have been reported as connected before, e.g. if the remote's
	// SHIP ID could not be verified after SetupRemoteService was already called.
	RemoteServiceDisconnected(identity ServiceIdentity)

	// set up SPINE communication with a remote service
	//
	// Called exactly once per connection, when it enters SHIP connection data exchange after PIN
	// verification succeeded, and before RemoteServiceConnected. SPINE messages of the remote are
	// passed to the returned reader from then on (SHIP IG Transport and Connectivity 2.1). The
	// access methods exchange that verifies the remote's SHIP ID runs in parallel, so identity may
	// not contain the SHIP ID yet; ServiceUpdated reports it once known. Return nil if SPINE data
	// is not processed.
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
