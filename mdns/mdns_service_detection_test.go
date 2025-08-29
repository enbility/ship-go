package mdns

import (
	"net"
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestServiceDetectionSuite(t *testing.T) {
	suite.Run(t, new(ServiceDetectionSuite))
}

type ServiceDetectionSuite struct {
	suite.Suite

	sut *MdnsManager
}

func (s *ServiceDetectionSuite) SetupTest() {
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		nil,
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
}

func (s *ServiceDetectionSuite) Test_detectServiceType() {
	tests := []struct {
		name         string
		serviceType  string
		expectedType ServiceType
	}{
		{
			name:         "detects SHIP service",
			serviceType:  "_ship._tcp",
			expectedType: ServiceTypeShip,
		},
		{
			name:         "detects SHIP pairing service",
			serviceType:  "_shippairing._tcp",
			expectedType: ServiceTypeShipPairing,
		},
		{
			name:         "returns unknown for unrecognized service",
			serviceType:  "_http._tcp",
			expectedType: ServiceTypeUnknown,
		},
		{
			name:         "returns unknown when empty",
			serviceType:  "",
			expectedType: ServiceTypeUnknown,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := s.sut.detectServiceType(tt.serviceType)
			assert.Equal(s.T(), tt.expectedType, result)
		})
	}
}

func (s *ServiceDetectionSuite) Test_routeServiceEntry() {
	// Test data
	elements := map[string]string{
		"txtvers":  "1",
		"id":       "test-id",
		"path":     "/ship/",
		"ski":      "testpar",
		"register": "true",
	}
	name := "test-service"
	host := "test-host.local"
	addresses := []net.IP{net.ParseIP("192.168.1.1")}
	port := 4729
	remove := false

	// Test SHIP service routing
	s.sut.processMdnsEntry(elements, name, host, "_ship._tcp", addresses, port, remove)
	// Should have one entry for SHIP service
	assert.Equal(s.T(), 1, len(s.sut.mdnsEntries()))

	// Clear entries
	s.sut.mux.Lock()
	s.sut.entries = make(map[string]*api.MdnsEntry)
	s.sut.mux.Unlock()

	// Test SHIP pairing service routing (should be ignored for now as processPairingEntry not yet implemented)
	s.sut.processMdnsEntry(elements, name, host, "_shippairing._tcp", addresses, port, remove)
	// Should have no entries as pairing processing not yet implemented
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))

	// Test unknown service routing (should be ignored)
	s.sut.processMdnsEntry(elements, name, host, "_http._tcp", addresses, port, remove)
	// Should still have no entries
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))
}
