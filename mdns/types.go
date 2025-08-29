package mdns

const shipZeroConfServiceType string = "_ship._tcp"
const shipZeroConfDomain string = "local."
const shipPairingZeroConfServiceType string = "_shippairing._tcp"

// ServiceType represents the type of mDNS service being processed
type ServiceType uint

const (
	ServiceTypeUnknown ServiceType = iota
	ServiceTypeShip
	ServiceTypeShipPairing
)
