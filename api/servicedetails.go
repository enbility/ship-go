package api

import (
	"sync"

	"github.com/enbility/ship-go/util"
)

// generic service details about the local or any remote service
type ServiceDetails struct {
	// This is the SKI of the service
	// This needs to be persisted
	ski string

	// This is the IPv4 address of the device running the service
	// This is optional only needed when this runs with
	// zeroconf as mDNS and the remote device is using the latest
	// avahi version and thus zeroconf can sometimes not detect
	// the IPv4 address and not initiate a connection
	ipv4 string

	// shipID is the SHIP identifier of the service
	// This needs to be persisted
	shipID string

	// The EEBUS device type of the device model
	deviceType string

	// Flags if the service auto accepts other services
	autoAccept bool

	// Flags if the service is trusted and should be reconnected to
	// Should be enabled after the connection process resulted
	// ConnectionStateDetail == ConnectionStateTrusted the first time
	trusted bool

	// the current connection state details
	connectionStateDetail *ConnectionStateDetail

	mux sync.Mutex
}

// create a new ServiceDetails record with a SKI
func NewServiceDetails(ski string) *ServiceDetails {
	connState := NewConnectionStateDetail(ConnectionStateNone, nil)
	service := &ServiceDetails{
		ski:                   util.NormalizeSKI(ski), // standardize the provided SKI strings
		connectionStateDetail: connState,
	}

	return service
}

func (s *ServiceDetails) SKI() string {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.ski
}

func (s *ServiceDetails) IPv4() string {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.ipv4
}

func (s *ServiceDetails) SetIPv4(ipv4 string) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.ipv4 = ipv4
}

func (s *ServiceDetails) ShipID() string {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.shipID
}

func (s *ServiceDetails) SetShipID(shipid string) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.shipID = shipid
}

func (s *ServiceDetails) DeviceType() string {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.deviceType
}

func (s *ServiceDetails) SetDeviceType(deviceType string) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.deviceType = deviceType
}

func (s *ServiceDetails) AutoAccept() bool {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.autoAccept
}

func (s *ServiceDetails) SetAutoAccept(value bool) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.autoAccept = value
}

func (s *ServiceDetails) Trusted() bool {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.trusted
}

func (s *ServiceDetails) SetTrusted(trust bool) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.trusted = trust
}

func (s *ServiceDetails) ConnectionStateDetail() *ConnectionStateDetail {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.connectionStateDetail
}

func (s *ServiceDetails) SetConnectionStateDetail(detail *ConnectionStateDetail) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.connectionStateDetail = detail
}
