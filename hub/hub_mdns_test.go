package hub

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
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

	// Set up expectation for VisibleRemoteMdnsServicesUpdated call
	var receivedEntries []api.RemoteMdnsService
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).
		RunAndReturn(func(entries []api.RemoteMdnsService) {
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
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).Once()
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
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).Once()
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
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).Once()
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
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).Once()
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
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).Once()
	hub.hubReader = mockHubReader

	hub.ReportMdnsEntries(entries, true)

	// Since ServiceForIdentifier returns nil, should skip to next entry
	mockHubReader.AssertExpectations(t)
}

// Test mDNS cleanup functionality when services disappear
func TestReportMdnsEntries_CleanupRemovedEntries(t *testing.T) {
	mockMdns := mocks.NewMdnsInterface(t)
	hub := &Hub{
		connections:              make(map[string]api.ShipConnectionInterface),
		remoteServices:           make([]*api.ServiceDetails, 0),
		connectionAttemptCounter: make(map[string]int),
		connectionAttemptRunning: make(map[string]bool),
		connectionDelayTimers:    make(map[string]*connectionDelayTimer),
		knownMdnsEntries:         make([]*api.MdnsEntry, 0),
		muxMdns:                  sync.Mutex{},
		muxTimers:                sync.RWMutex{},
		muxConAttempt:            sync.RWMutex{},
		mdns:                     mockMdns,
	}

	mockHubReader := mocks.NewHubReaderInterface(t)
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).Times(3)
	hub.hubReader = mockHubReader

	// Step 1: Report initial entries with two services
	ski1, ski2 := "SKI1", "SKI2"
	initialEntries := map[string]*api.MdnsEntry{
		"service1": {Name: "Service 1", Ski: ski1, Identifier: "ID1"},
		"service2": {Name: "Service 2", Ski: ski2, Identifier: "ID2"},
	}

	// Set up connection attempts for both SKIs
	hub.connectionAttemptCounter[ski1] = 2
	hub.connectionAttemptCounter[ski2] = 1
	// Create proper test timers using the factory method
	hub.connectionDelayTimers[ski1] = newConnectionDelayTimer(time.Millisecond, func() {})
	hub.connectionDelayTimers[ski2] = newConnectionDelayTimer(time.Millisecond, func() {})

	hub.ReportMdnsEntries(initialEntries, true)

	// Verify both SKIs have connection state
	assert.Equal(t, 2, hub.connectionAttemptCounter[ski1])
	assert.Equal(t, 1, hub.connectionAttemptCounter[ski2])
	assert.Contains(t, hub.connectionDelayTimers, ski1)
	assert.Contains(t, hub.connectionDelayTimers, ski2)

	// Step 2: Report entries with only service1 (service2 disappeared)
	updatedEntries := map[string]*api.MdnsEntry{
		"service1": {Name: "Service 1", Ski: ski1, Identifier: "ID1"},
	}

	hub.ReportMdnsEntries(updatedEntries, true)

	// Verify SKI1 state is preserved, SKI2 state is cleaned up
	assert.Equal(t, 2, hub.connectionAttemptCounter[ski1], "SKI1 connection counter should be preserved")
	assert.NotContains(t, hub.connectionAttemptCounter, ski2, "SKI2 connection counter should be removed")
	assert.Contains(t, hub.connectionDelayTimers, ski1, "SKI1 timer should be preserved")
	assert.NotContains(t, hub.connectionDelayTimers, ski2, "SKI2 timer should be removed")

	// Step 3: Report empty entries (all services disappeared)
	emptyEntries := map[string]*api.MdnsEntry{}

	hub.ReportMdnsEntries(emptyEntries, true)

	// Verify all connection state is cleaned up
	assert.NotContains(t, hub.connectionAttemptCounter, ski1, "All connection counters should be removed")
	assert.NotContains(t, hub.connectionDelayTimers, ski1, "All connection timers should be removed")
	assert.Len(t, hub.connectionAttemptCounter, 0, "Connection counter map should be empty")
	assert.Len(t, hub.connectionDelayTimers, 0, "Connection timer map should be empty")

	mockHubReader.AssertExpectations(t)
}

// Test that cleanup doesn't interfere with normal operation
func TestReportMdnsEntries_CleanupWithNewEntries(t *testing.T) {
	mockMdns := mocks.NewMdnsInterface(t)
	hub := &Hub{
		connections:              make(map[string]api.ShipConnectionInterface),
		remoteServices:           make([]*api.ServiceDetails, 0),
		connectionAttemptCounter: make(map[string]int),
		connectionAttemptRunning: make(map[string]bool),
		connectionDelayTimers:    make(map[string]*connectionDelayTimer),
		knownMdnsEntries:         make([]*api.MdnsEntry, 0),
		muxMdns:                  sync.Mutex{},
		muxTimers:                sync.RWMutex{},
		muxConAttempt:            sync.RWMutex{},
		mdns:                     mockMdns,
	}

	mockHubReader := mocks.NewHubReaderInterface(t)
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).Times(2)
	hub.hubReader = mockHubReader

	// Initial state: service1 and service2
	initialEntries := map[string]*api.MdnsEntry{
		"service1": {Name: "Service 1", Ski: "SKI1", Identifier: "ID1"},
		"service2": {Name: "Service 2", Ski: "SKI2", Identifier: "ID2"},
	}
	hub.connectionAttemptCounter["SKI1"] = 1
	hub.connectionAttemptCounter["SKI2"] = 2

	hub.ReportMdnsEntries(initialEntries, true)

	// Updated state: remove service1, keep service2, add service3
	updatedEntries := map[string]*api.MdnsEntry{
		"service2": {Name: "Service 2", Ski: "SKI2", Identifier: "ID2"},
		"service3": {Name: "Service 3", Ski: "SKI3", Identifier: "ID3"},
	}

	hub.ReportMdnsEntries(updatedEntries, true)

	// Verify selective cleanup: SKI1 removed, SKI2 preserved, SKI3 is new
	assert.NotContains(t, hub.connectionAttemptCounter, "SKI1", "SKI1 should be cleaned up")
	assert.Equal(t, 2, hub.connectionAttemptCounter["SKI2"], "SKI2 should be preserved")
	// Note: SKI3 won't have connection state since it wasn't in connectionAttemptCounter initially

	mockHubReader.AssertExpectations(t)
}

// Test edge case: no previous entries
func TestReportMdnsEntries_CleanupWithNoPreviousEntries(t *testing.T) {
	mockMdns := mocks.NewMdnsInterface(t)
	hub := &Hub{
		connections:              make(map[string]api.ShipConnectionInterface),
		remoteServices:           make([]*api.ServiceDetails, 0),
		connectionAttemptCounter: make(map[string]int),
		connectionAttemptRunning: make(map[string]bool),
		connectionDelayTimers:    make(map[string]*connectionDelayTimer),
		knownMdnsEntries:         make([]*api.MdnsEntry, 0), // No previous entries
		muxMdns:                  sync.Mutex{},
		muxTimers:                sync.RWMutex{},
		muxConAttempt:            sync.RWMutex{},
		mdns:                     mockMdns,
	}

	mockHubReader := mocks.NewHubReaderInterface(t)
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).Once()
	hub.hubReader = mockHubReader

	// Add some connection state that shouldn't be affected (since no previous entries)
	hub.connectionAttemptCounter["SKI1"] = 3

	entries := map[string]*api.MdnsEntry{
		"service1": {Name: "Service 1", Ski: "SKI1", Identifier: "ID1"},
	}

	hub.ReportMdnsEntries(entries, true)

	// With no previous entries, no cleanup should occur
	assert.Equal(t, 3, hub.connectionAttemptCounter["SKI1"], "Existing connection state should be preserved")

	mockHubReader.AssertExpectations(t)
}

// Create tests the cover ReportMdnsEntries implementation
