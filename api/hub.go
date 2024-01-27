package api

//go:generate mockery
//go:generate mockgen -destination=../mocks/mockgen_api.go -package=mocks github.com/enbility/ship-go/api MdnsInterface,HubReaderInterface

/* Hub */

// interface for handling the server and remote connections
type HubInterface interface {
	PairingDetailForSki(ski string) *ConnectionStateDetail
	Start()
	Shutdown()
	ServiceForSKI(ski string) *ServiceDetails
	RegisterRemoteSKI(ski string, enable bool)
	InitiatePairingWithSKI(ski string)
	CancelPairingWithSKI(ski string)
	DisconnectSKI(ski string, reason string)
}

// Used to pass information from the hub to the eebus service
//
// Implemented by eebus Service, used by Hub
type HubReaderInterface interface {
	// report a newly discovered remote EEBUS service
	VisibleMDNSRecordsUpdated(entries []*MdnsEntry)

	// report a connection to a SKI
	RemoteSKIConnected(ski string)

	// report a disconnection to a SKI
	RemoteSKIDisconnected(ski string)

	// provide the SHIP ID received during SHIP handshake process
	// the ID needs to be stored and then provided for remote services so it can be compared and verified
	ServiceShipIDUpdate(ski string, shipID string)

	// provides the current handshake state for a given SKI
	ServicePairingDetailUpdate(ski string, detail *ConnectionStateDetail)

	// report an approved handshake by a remote device
	SetupRemoteDevice(ski string, writeI ShipConnectionDataWriterInterface) ShipConnectionDataReaderInterface

	// return if the user is still able to trust the connection
	AllowWaitingForTrust(ski string) bool
}
