package api

import (
	"fmt"
	"strings"

	"github.com/enbility/ship-go/util"
)

// ServiceIdentity represents the public API struct for service identity information.
// This is a simple data container used for external APIs and JSON/YAML serialization.
type ServiceIdentity struct {
	// Certificate public key identification of the service
	//
	// This is mandatory for SHIP 1.0.1/1.1 services, and not provided initially when
	// a service was paired using SHIP Pairing.
	//
	// Note: This needs to be persisted in the applications trust store and provided with
	// RegisterRemoveService call if known!
	SKI string `json:"ski"`

	// Certificate fingerprint of the service
	//
	// This is used for certificate validation, and optional for now.
	// It will be provided instead of SKI when a service is paired using SHIP Pairing.
	//
	// Note: This needs to be persisted in the applications trust store and provided with
	// RegisterRemoveService call if known!
	Fingerprint string `json:"fingerprint"`

	// shipID is the SHIP identifier of the service
	//
	// This is mandatory for SHIP 1.0.1/1.1 services, and not provided initially when
	// a service was paired using SHIP Pairing.
	//
	// Note: This needs to be persisted in the applications trust store and provided with
	// RegisterRemoveService call if known!
	ShipID string `json:"shipID"`

	// The pairing type for this service
	//
	// By default this is set to PairingTypeDefault (0). For control units
	// paired using SHIP Pairing this will be set to PairingTypeAddCu (1)
	//
	// Note: This needs to be persisted in the applications trust store and provided with
	// RegisterRemoveService call!
	PairingType PairingType `json:"pairingType"`

	// This is the IPv4 address of the device running the service
	//
	// This is optional only needed when this runs with
	// zeroconf as mDNS and the remote device is using the latest
	// avahi version and thus zeroconf can sometimes not detect
	// the IPv4 address and not initiate a connection
	//
	// Only relevant with RegisterRemoveService, shall be ignored otherwise
	IPv4 string `json:"ipv4,omitempty"`
}

// NewServiceIdentity creates a new ServiceIdentity with validation.
//
// Parameters:
//   - ski: The SKI (Subject Key Identifier) of the service. Required if fingerprint is not provided
//   - fingerprint: The expected certificate fingerprint of the service. Required if SKI is not provided
//   - shipid: The SHIP ID of the service. Required if fingerprint is provided and ski is not provided
//
// Returns zero value if validation fails (replaces nil return with empty struct).
func NewServiceIdentity(ski, fingerprint, shipid string) ServiceIdentity {
	// Validation logic from current ServiceDetails.NewServiceDetails()
	if ski == "" && fingerprint == "" {
		return ServiceIdentity{} // Return zero value instead of nil
	}

	if ski == "" && fingerprint != "" && shipid == "" {
		return ServiceIdentity{} // Return zero value instead of nil
	}

	return ServiceIdentity{
		SKI:         util.NormalizeSKI(ski), // Normalize SKI like current implementation
		Fingerprint: fingerprint,
		ShipID:      shipid,
		PairingType: PairingTypeDefault, // Default to traditional pairing
		IPv4:        "",                 // Empty by default
	}
}

// IsZero returns true if ServiceIdentity is empty (replaces nil checks).
func (s ServiceIdentity) IsZero() bool {
	return s.SKI == "" && s.Fingerprint == "" && s.ShipID == ""
}

// String returns a string representation of the ServiceIdentity.
// Shows the most relevant identifying information based on what's available.
func (s ServiceIdentity) String() string {
	var parts []string

	if s.SKI != "" {
		parts = append(parts, fmt.Sprintf("SKI:%s", s.SKI))
	}

	if s.ShipID != "" {
		parts = append(parts, fmt.Sprintf("ShipID:%s", s.ShipID))
	}

	if s.Fingerprint != "" {
		parts = append(parts, fmt.Sprintf("Fingerprint:%s", s.Fingerprint[:8]+"...")) // Truncate for readability
	}

	if len(parts) == 0 {
		return "ServiceIdentity{empty}"
	}

	return fmt.Sprintf("ServiceIdentity{%s}", strings.Join(parts, " "))
}

// ToServiceDetails converts a ServiceIdentity to a ServiceDetails object.
// This is useful for interfacing with internal APIs that use ServiceDetails.
func (s ServiceIdentity) ToServiceDetails() *ServiceDetails {
	serviceDetails := NewServiceDetails(s.SKI, s.Fingerprint, s.ShipID)
	if serviceDetails == nil {
		return nil
	}

	serviceDetails.SetPairingType(s.PairingType)
	if s.IPv4 != "" {
		serviceDetails.SetIPv4(s.IPv4)
	}

	return serviceDetails
}
