package hub

import (
	"net"
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/mock"
)

func TestReportMdnsEntries(t *testing.T) {
	mockMdns := mocks.NewMdnsInterface(t)
	hub := &Hub{
		connections:              make(map[string]api.ShipConnectionInterface),
		remoteServices:           make([]*api.ServiceDetails, 0),
		connectionAttemptCounter: make(map[string]int),
		connectionAttemptRunning: make(map[string]bool),
		connectionDelayTimers:    make(map[string]*connectionDelayTimer),
		mdns:                     mockMdns,
	}

	entries := map[string]*api.MdnsEntry{
		"service1": {
			Name:       "Service 1",
			Ski:        "SKI1",
			Identifier: "ID1",
			Path:       "/ws",
			Register:   true,
			Brand:      "BrandA",
			Type:       "TypeA",
			Model:      "ModelA",
			Serial:     "SerialA",
			Categories: []api.DeviceCategoryType{api.DeviceCategoryTypeEMobility},
			Host:       "host1.local",
			Port:       1234,
			Addresses:  []net.IP{net.ParseIP("192.168.1.2")},
		},
		"service2": {
			Name:       "Service 2",
			Ski:        "SKI2",
			Identifier: "ID2",
			Path:       "/ws",
			Register:   false,
			Brand:      "BrandB",
			Type:       "TypeB",
			Model:      "ModelB",
			Serial:     "SerialB",
			Categories: []api.DeviceCategoryType{api.DeviceCategoryTypeHVAC},
			Host:       "host2.local",
			Port:       5678,
			Addresses:  []net.IP{net.ParseIP("192.168.1.3")},
		},
	}

	// Create mock HubReader using the official mock
	mockHubReader := mocks.NewHubReaderInterface(t)

	// Set up expectation for VisibleRemoteServicesUpdated call
	var receivedEntries []api.RemoteService
	mockHubReader.EXPECT().VisibleRemoteServicesUpdated(mock.AnythingOfType("[]api.RemoteService")).
		RunAndReturn(func(entries []api.RemoteService) {
			receivedEntries = entries
		}).
		Once()

	hub.hubReader = mockHubReader

	hub.ReportMdnsEntries(entries, true)

	// Verify the mock expectations
	mockHubReader.AssertExpectations(t)

	if len(receivedEntries) != 2 {
		t.Errorf("Expected 2 remote services, got %d", len(receivedEntries))
	}

	for _, entry := range receivedEntries {
		if entry.Name == "Service 1" {
			if entry.Ski != "SKI1" || entry.Brand != "BrandA" {
				t.Errorf("Service 1 fields do not match")
			}
		}
		if entry.Name == "Service 2" {
			if entry.Ski != "SKI2" || entry.Brand != "BrandB" {
				t.Errorf("Service 2 fields do not match")
			}
		}
	}
}

func TestReportMdnsEntries_WithConnectedService(t *testing.T) {
	mockMdns := mocks.NewMdnsInterface(t)
	hub := &Hub{
		connections:              make(map[string]api.ShipConnectionInterface),
		remoteServices:           make([]*api.ServiceDetails, 0),
		connectionAttemptCounter: make(map[string]int),
		connectionAttemptRunning: make(map[string]bool),
		connectionDelayTimers:    make(map[string]*connectionDelayTimer),
		mdns:                     mockMdns,
	}

	// Add a connected service
	ski := "SKI1"
	mockConnection := mocks.NewShipConnectionInterface(t)
	hub.connections[ski] = mockConnection

	entries := map[string]*api.MdnsEntry{
		"service1": {
			Name:       "Service 1",
			Ski:        ski,
			Identifier: "ID1",
			Register:   true,
		},
	}

	mockHubReader := mocks.NewHubReaderInterface(t)
	mockHubReader.EXPECT().VisibleRemoteServicesUpdated(mock.AnythingOfType("[]api.RemoteService")).Once()
	hub.hubReader = mockHubReader

	hub.ReportMdnsEntries(entries, true)

	// Since the service is already connected, coordinateConnectionInitations should not be called
	mockHubReader.AssertExpectations(t)
}

func TestReportMdnsEntries_WithUnpairedService(t *testing.T) {
	mockMdns := mocks.NewMdnsInterface(t)
	hub := &Hub{
		connections:              make(map[string]api.ShipConnectionInterface),
		remoteServices:           make([]*api.ServiceDetails, 0),
		connectionAttemptCounter: make(map[string]int),
		connectionAttemptRunning: make(map[string]bool),
		connectionDelayTimers:    make(map[string]*connectionDelayTimer),
		mdns:                     mockMdns,
	}

	ski := "SKI1"
	service := api.NewServiceDetails(ski, "", "")
	service.SetTrusted(false) // Not paired
	hub.remoteServices = append(hub.remoteServices, service)

	entries := map[string]*api.MdnsEntry{
		"service1": {
			Name:       "Service 1",
			Ski:        ski,
			Identifier: "ID1",
			Register:   true,
		},
	}

	mockHubReader := mocks.NewHubReaderInterface(t)
	mockHubReader.EXPECT().VisibleRemoteServicesUpdated(mock.AnythingOfType("[]api.RemoteService")).Once()
	hub.hubReader = mockHubReader

	hub.ReportMdnsEntries(entries, true)

	// Since the service is not paired, coordinateConnectionInitations should not be called
	mockHubReader.AssertExpectations(t)
}

func TestReportMdnsEntries_WithTrustedServiceAndIPv4(t *testing.T) {
	mockMdns := mocks.NewMdnsInterface(t)
	// Set up mock to expect AnnounceMdnsEntry call when checkAutoReannounce is triggered
	mockMdns.EXPECT().AnnounceMdnsEntry().Return(nil).Maybe()
	mockMdns.EXPECT().RequestMdnsEntries().Maybe()

	hub := &Hub{
		connections:              make(map[string]api.ShipConnectionInterface),
		remoteServices:           make([]*api.ServiceDetails, 0),
		connectionAttemptCounter: make(map[string]int),
		connectionAttemptRunning: make(map[string]bool),
		connectionDelayTimers:    make(map[string]*connectionDelayTimer),
		mdns:                     mockMdns,
	}

	ski := "SKI1"
	service := api.NewServiceDetails(ski, "", "")
	service.SetTrusted(true)                                            // Paired
	service.SetIPv4("192.168.1.100")                                    // Set IPv4 address
	service.ConnectionStateDetail().SetState(api.ConnectionStateQueued) // Queued for connection
	hub.remoteServices = append(hub.remoteServices, service)

	originalIP := net.ParseIP("192.168.1.2")
	entries := map[string]*api.MdnsEntry{
		"service1": {
			Name:       "Service 1",
			Ski:        ski,
			Identifier: "ID1",
			Register:   false, // Will be set to service auto-accept
			Addresses:  []net.IP{originalIP},
		},
	}

	mockHubReader := mocks.NewHubReaderInterface(t)
	mockHubReader.EXPECT().VisibleRemoteServicesUpdated(mock.AnythingOfType("[]api.RemoteService")).Once()
	hub.hubReader = mockHubReader

	hub.ReportMdnsEntries(entries, true)

	// Verify that the IPv4 address was patched
	expectedIP := net.ParseIP("192.168.1.100")
	if !entries["service1"].Addresses[0].Equal(expectedIP) {
		t.Errorf("Expected IP address to be patched to %v, got %v", expectedIP, entries["service1"].Addresses[0])
	}

	// Verify that auto-accept was set
	if service.AutoAccept() != false {
		t.Errorf("Expected auto-accept to be set to false, got %t", service.AutoAccept())
	}

	mockHubReader.AssertExpectations(t)
}

func TestReportMdnsEntries_WithNonPairedTrustedService(t *testing.T) {
	mockMdns := mocks.NewMdnsInterface(t)
	mockMdns.EXPECT().AnnounceMdnsEntry().Return(nil).Maybe()
	mockMdns.EXPECT().RequestMdnsEntries().Maybe()

	hub := &Hub{
		connections:              make(map[string]api.ShipConnectionInterface),
		remoteServices:           make([]*api.ServiceDetails, 0),
		connectionAttemptCounter: make(map[string]int),
		connectionAttemptRunning: make(map[string]bool),
		connectionDelayTimers:    make(map[string]*connectionDelayTimer),
		mdns:                     mockMdns,
	}

	ski := "SKI1"
	service := api.NewServiceDetails(ski, "", "")
	service.SetTrusted(true)                                               // Trusted but not paired through IsRemoteServiceForSKIPaired
	service.ConnectionStateDetail().SetState(api.ConnectionStateCompleted) // Not queued
	hub.remoteServices = append(hub.remoteServices, service)

	entries := map[string]*api.MdnsEntry{
		"service1": {
			Name:       "Service 1",
			Ski:        ski,
			Identifier: "ID1",
			Register:   true,
		},
	}

	mockHubReader := mocks.NewHubReaderInterface(t)
	mockHubReader.EXPECT().VisibleRemoteServicesUpdated(mock.AnythingOfType("[]api.RemoteService")).Once()
	hub.hubReader = mockHubReader

	hub.ReportMdnsEntries(entries, true)

	// Should not proceed to coordinateConnectionInitations due to the condition check
	mockHubReader.AssertExpectations(t)
}

func TestReportMdnsEntries_WithUnknownService(t *testing.T) {
	mockMdns := mocks.NewMdnsInterface(t)
	hub := &Hub{
		connections:              make(map[string]api.ShipConnectionInterface),
		remoteServices:           make([]*api.ServiceDetails, 0),
		connectionAttemptCounter: make(map[string]int),
		connectionAttemptRunning: make(map[string]bool),
		connectionDelayTimers:    make(map[string]*connectionDelayTimer),
		mdns:                     mockMdns,
	}

	// Don't add any services to remoteServices, so ServiceForIdentifier will return nil
	entries := map[string]*api.MdnsEntry{
		"service1": {
			Name:       "Unknown Service",
			Ski:        "UNKNOWN_SKI",
			Identifier: "ID1",
			Register:   true,
		},
	}

	mockHubReader := mocks.NewHubReaderInterface(t)
	mockHubReader.EXPECT().VisibleRemoteServicesUpdated(mock.AnythingOfType("[]api.RemoteService")).Once()
	hub.hubReader = mockHubReader

	hub.ReportMdnsEntries(entries, true)

	// Since ServiceForIdentifier returns nil, should skip to next entry
	mockHubReader.AssertExpectations(t)
}

// Create tests the cover ReportMdnsEntries implementation
