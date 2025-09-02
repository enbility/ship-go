package api

import (
	"sync"

	"github.com/enbility/ship-go/util"
)

// PairingType defines the type of pairing mechanism used for a service.
//
// The pairing type determines how trust is established and managed, particularly
// for the Device Replacement Timing Logic feature introduced in SHIP Pairing Service.
//
// Example usage:
//
//	// Check if a service uses AddCu pairing
//	if service.PairingType() == api.PairingTypeAddCu {
//	    // This device was paired via SHIP Pairing Service
//	    // and is subject to replacement timing logic
//	}
//
//	// Set pairing type for a new AddCu device
//	service.SetPairingType(api.PairingTypeAddCu)
type PairingType int

const (
	// PairingTypeDefault represents traditional SHIP 1.0.1 pairing.
	// Services with this type use manual trust establishment and are not
	// subject to automatic replacement timing logic.
	PairingTypeDefault PairingType = iota

	// PairingTypeAddCu represents SHIP Pairing Service with control unit.
	// Services with this type:
	// - Are automatically trusted via SHIP Pairing Service
	// - Are subject to 15-minute replacement timing logic
	// - May be automatically untrusted if replaced by another device
	// - Trigger timer-based pairing listener reactivation
	//
	// The replacement timing logic ensures that when an AddCu device disconnects,
	// the system waits 15 minutes for a potential replacement device before
	// reactivating the pairing listener. If the device reconnects within this
	// window, the timer is cancelled.
	PairingTypeAddCu
)

// generic service details about the local or any remote service
type ServiceDetails struct {
	// Certificate fingerprint of the service
	fingerprint string

	// Certificate public key identification of the service
	ski string

	// shipID is the SHIP identifier of the service
	shipID string

	// The pairing type for this service
	pairingType PairingType

	// Flags if the service auto accepts other services
	autoAccept bool

	// This is the IPv4 address of the device running the service
	ipv4 string

	// Flags if the service is trusted and should be reconnected to
	trusted bool

	// the current connection state details
	connectionStateDetail *ConnectionStateDetail

	mux sync.Mutex
}

// create a new ServiceDetails record
//
// This function initializes a new ServiceDetails instance with the provided
// SKI, fingerprint, and ship ID. It sets the initial connection state to
// ConnectionStateNone and the pairing type to PairingTypeDefault.
//
// Parameters:
//   - ski: The SKI (Subject Key Identifier) of the service. Required if fingerprint is not provided
//   - fingerprint: The expected certificate fingerprint of the service. Required if SKI is not provided
//   - shipid: The SHIP ID of the service. Required if fingerprint is provided and ski is not provided
func NewServiceDetails(ski, fingerprint, shipid string) *ServiceDetails {
	connState := NewConnectionStateDetail(ConnectionStateNone, nil)

	// check if we have all the required parameters
	if ski == "" && fingerprint == "" {
		return nil
	}

	if ski == "" && fingerprint != "" && shipid == "" {
		return nil
	}

	service := &ServiceDetails{
		ski:                   util.NormalizeSKI(ski), // standardize the provided SKI strings
		fingerprint:           fingerprint,
		shipID:                shipid,
		connectionStateDetail: connState,
		pairingType:           PairingTypeDefault, // default to traditional pairing
	}

	return service
}

// Fingerprint returns the expected certificate fingerprint of the service.
//
// This fingerprint is used for additional certificate validation
// and is optional. If not set, the service will not perform
// fingerprint validation.
func (s *ServiceDetails) Fingerprint() string {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.fingerprint
}

// SetFingerprint sets the expected certificate fingerprint of the service.
func (s *ServiceDetails) SetFingerprint(fingerprint string) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.fingerprint = fingerprint
}

// SKI returns the expected service's SKI (Subject Key Identifier).
//
// This SKI is used to uniquely identify the service within the SHIP network
// and is required for all service interactions.
func (s *ServiceDetails) SKI() string {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.ski
}

// SetSKI sets the expected service's SKI (Subject Key Identifier).
//
// This is useful for internal use when the fingerprint is provided and we got the matching SKI
func (s *ServiceDetails) SetSKI(ski string) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.ski = util.NormalizeSKI(ski)
}

// ShipID returns the expected service's SHIP ID.
func (s *ServiceDetails) ShipID() string {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.shipID
}

// SetShipID sets the expected service's SHIP ID.
func (s *ServiceDetails) SetShipID(shipid string) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.shipID = shipid
}

// IPv4 returns a manually set IPv4 address of the service.
func (s *ServiceDetails) IPv4() string {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.ipv4
}

// SetIPv4 sets an additional IPv4 address of the service to be used for connection
//
// This is used as an alternative IP address to attempt for cases
// where the device doesn't provide one and the user can provide on instead
func (s *ServiceDetails) SetIPv4(ipv4 string) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.ipv4 = ipv4
}

// AutoAccept returns whether the service should or does automatically accept connections.
func (s *ServiceDetails) AutoAccept() bool {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.autoAccept
}

// SetAutoAccept sets whether the service should automatically accept connections.
func (s *ServiceDetails) SetAutoAccept(value bool) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.autoAccept = value
}

// Trusted returns whether the service is trusted.
func (s *ServiceDetails) Trusted() bool {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.trusted
}

// SetTrusted sets whether the service is trusted.
//
// Should only be used internally!
func (s *ServiceDetails) SetTrusted(trust bool) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.trusted = trust
}

// ConnectionStateDetail returns the current connection state details for this service
func (s *ServiceDetails) ConnectionStateDetail() *ConnectionStateDetail {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.connectionStateDetail
}

// SetConnectionStateDetail sets the current connection state details for this service
//
// Should only be used internally!
func (s *ServiceDetails) SetConnectionStateDetail(detail *ConnectionStateDetail) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.connectionStateDetail = detail
}

// PairingType returns the pairing type for this service.
//
// The pairing type indicates how this service was paired and determines
// which trust management rules apply. AddCu devices are subject to
// automatic replacement timing logic.
//
// Returns:
// - PairingTypeDefault: Traditional SHIP pairing (manual trust)
// - PairingTypeAddCu: SHIP Pairing Service (automatic trust with replacement logic)
//
// Example:
//
//	if service.PairingType() == api.PairingTypeAddCu {
//	    // Handle AddCu-specific logic
//	    log.Printf("Device %s is an AddCu device", service.ShipID())
//	}
func (s *ServiceDetails) PairingType() PairingType {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.pairingType
}

// SetPairingType sets the pairing type for this service.
//
// This should be called when a device is paired via SHIP Pairing Service
// to enable the Device Replacement Timing Logic. The pairing type affects:
// - Whether the 15-minute replacement timer is started on disconnect
// - How trust is managed for this service
// - Whether automatic trust removal can occur
//
// Parameters:
// - t: The pairing type to set (PairingTypeDefault or PairingTypeAddCu)
//
// Example:
//
//	// Mark a device as paired via SHIP Pairing Service
//	service.SetPairingType(api.PairingTypeAddCu)
//	service.SetTrusted(true)
func (s *ServiceDetails) SetPairingType(t PairingType) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.pairingType = t
}

// Copy creates a deep copy of the ServiceDetails instance.
func (s *ServiceDetails) Copy() *ServiceDetails {
	s.mux.Lock()
	defer s.mux.Unlock()

	newConnectionStateDetail := &ConnectionStateDetail{
		state: s.connectionStateDetail.State(),
		error: s.connectionStateDetail.Error(),
	}

	return &ServiceDetails{
		ski:                   s.ski,
		shipID:                s.shipID,
		fingerprint:           s.fingerprint,
		ipv4:                  s.ipv4,
		autoAccept:            s.autoAccept,
		trusted:               s.trusted,
		connectionStateDetail: newConnectionStateDetail,
		pairingType:           s.pairingType,
	}
}

// ToServiceIdentity converts a ServiceDetails to a ServiceIdentity object.
// This is useful for interfacing with public APIs that use ServiceIdentity.
func (s *ServiceDetails) ToServiceIdentity() ServiceIdentity {
	s.mux.Lock()
	defer s.mux.Unlock()

	return ServiceIdentity{
		SKI:         s.ski,
		Fingerprint: s.fingerprint,
		ShipID:      s.shipID,
		PairingType: s.pairingType,
		IPv4:        s.ipv4,
	}
}


// SKIToServiceIdentity creates a minimal ServiceIdentity from just an SKI.
// This is a helper for converting SKI-only callbacks to ServiceIdentity format.
func SKIToServiceIdentity(ski string) ServiceIdentity {
	return ServiceIdentity{
		SKI:         ski,
		Fingerprint: "",
		ShipID:      "",
		PairingType: PairingTypeDefault,
		IPv4:        "",
	}
}
