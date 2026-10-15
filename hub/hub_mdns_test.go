package hub

import (
	"net"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// newMdnsTestHub builds a Hub with the state ReportMdnsEntries touches.
func newMdnsTestHub(mockMdns api.MdnsInterface) *Hub {
	return &Hub{
		connections:              make(map[string]api.ShipConnectionInterface),
		remoteServices:           make([]*api.ServiceDetails, 0),
		connectionAttemptCounter: make(map[string]int),
		connectionsInitiating:    make(map[string]*dialState),
		connectionAttemptRunning: make(map[string]bool),
		connectionDelayTimers:    make(map[string]*connectionDelayTimer),
		knownMdnsEntries:         make([]*api.MdnsEntry, 0),
		mdns:                     mockMdns,
	}
}

func TestReportMdnsEntries(t *testing.T) {
	mockMdns := mocks.NewMdnsInterface(t)
	hub := newMdnsTestHub(mockMdns)

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
			assert.Equal(t, []net.IP{net.ParseIP("192.168.1.2")}, entry.Addresses)
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
	hub := newMdnsTestHub(mockMdns)

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
	hub := newMdnsTestHub(mockMdns)

	ski := "SKI1"
	service, err := api.NewServiceDetails(ski, "", "")
	assert.NoError(t, err)
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

	hub := newMdnsTestHub(mockMdns)

	ski := "0123456789abcdef0123456789abcdefffffffff"
	service, err := api.NewServiceDetails(ski, "", "")
	assert.NoError(t, err)
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

func TestReportMdnsEntries_ShipIDCheck_Valid(t *testing.T) {
	mockMdns := mocks.NewMdnsInterface(t)
	// Set up mock to expect AnnounceMdnsEntry call when checkAutoReannounce is triggered
	mockMdns.EXPECT().AnnounceMdnsEntry().Return(nil).Maybe()
	mockMdns.EXPECT().RequestMdnsEntries().Maybe()

	hub := newMdnsTestHub(mockMdns)

	// Step 1
	ski := "0123456789abcdef0123456789abcdefffffffff"
	shipId := "ID1"
	fingerprint := "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF"
	service, err := api.NewServiceDetails("", fingerprint, shipId)
	assert.NoError(t, err)
	service.SetTrusted(true)                                            // Paired
	service.SetIPv4("192.168.1.100")                                    // Set IPv4 address
	service.ConnectionStateDetail().SetState(api.ConnectionStateQueued) // Queued for connection
	hub.remoteServices = append(hub.remoteServices, service)

	originalIP := net.ParseIP("192.168.1.2")
	entries := map[string]*api.MdnsEntry{
		"service1": {
			Name:       "Service 1",
			Ski:        ski,
			Identifier: shipId,
			Register:   false,
			Addresses:  []net.IP{originalIP},
		},
	}

	mockHubReader := mocks.NewHubReaderInterface(t)
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).Once()
	hub.hubReader = mockHubReader

	hub.ReportMdnsEntries(entries, true)

	// Assert the service now that it has the SKI
	if service.SKI() != ski {
		t.Errorf("Expected ski %s trusted and amended to the trust.. Got %s", ski, service.SKI())
	}

	// Step 2: try again with existing SKI but mDNS reports a different value
	ski = "0123456789abcdef0123456789abcdefffffffff"
	ski2 := "0123456789abcdef0123456789abcde000000000"
	entries = map[string]*api.MdnsEntry{
		"service1": {
			Name:       "Service 1",
			Ski:        ski2,
			Identifier: "ID1",
			Register:   false,
			Addresses:  []net.IP{originalIP},
		},
	}
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).Once()
	hub.ReportMdnsEntries(entries, true)

	// Assert the service did not look for our SHIP ID and overwritten our previous SKI
	if service.SKI() != ski || service.SKI() == ski2 {
		t.Errorf("Expected ski not to be overwritten to the trust..")
	}

	// Step 3: invalidate SKI check
	service.SetSKI("")
	ski = "SKI1"
	entries = map[string]*api.MdnsEntry{
		"service1": {
			Name:       "Service 1",
			Ski:        ski,
			Identifier: "ID1",
			Register:   false,
			Addresses:  []net.IP{originalIP},
		},
	}
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).Once()
	hub.ReportMdnsEntries(entries, true)

	// Assert the service is untouched
	if len(service.SKI()) != 0 {
		t.Errorf("Expected ski to be empty.. Got ski %s", ski)
	}

	// Step 4: invalidate/no Fingerprint check
	service.SetSKI("")
	ski = "0123456789abcdef0123456789abcdefffffffff"
	fingerprint = "fp"
	service.SetFingerprint(fingerprint)
	entries = map[string]*api.MdnsEntry{
		"service1": {
			Name:       "Service 1",
			Ski:        ski,
			Identifier: "ID1",
			Register:   false,
			Addresses:  []net.IP{originalIP},
		},
	}
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).Once()
	hub.ReportMdnsEntries(entries, true)

	// Assert the service now is untouched
	if len(service.SKI()) != 0 {
		t.Errorf("Expected ski to be empty.. Got ski %s", ski)
	}

	// Step 5: no Ship ID check
	service.SetSKI("")
	ski = "0123456789abcdef0123456789abcdefffffffff"
	shipId = ""
	service.SetShipID(shipId)
	fingerprint = "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF"
	service.SetFingerprint(fingerprint)
	entries = map[string]*api.MdnsEntry{
		"service1": {
			Name:       "Service 1",
			Ski:        ski,
			Identifier: shipId,
			Register:   false,
			Addresses:  []net.IP{originalIP},
		},
	}
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).Once()
	hub.ReportMdnsEntries(entries, true)

	// Assert the service now is untouched
	if len(service.SKI()) != 0 {
		t.Errorf("Expected ski to be empty.. Got ski %s", ski)
	}

	// Step 6: another Ship ID in mDNS check
	service.SetSKI("")
	ski = "0123456789abcdef0123456789abcdefffffffff"
	shipId = "ID1"
	shipId2 := "ID2"
	service.SetShipID(shipId)
	fingerprint = "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF"
	service.SetFingerprint(fingerprint)
	entries = map[string]*api.MdnsEntry{
		"service1": {
			Name:       "Service 1",
			Ski:        ski,
			Identifier: shipId2,
			Register:   false,
			Addresses:  []net.IP{originalIP},
		},
	}
	mockHubReader.EXPECT().VisibleRemoteMdnsServicesUpdated(mock.AnythingOfType("[]api.RemoteMdnsService")).Once()
	hub.ReportMdnsEntries(entries, true)

	// Assert the service now is untouched
	if len(service.SKI()) != 0 {
		t.Errorf("Expected ski to be empty.. Got ski %s", ski)
	}

	mockHubReader.AssertExpectations(t)
}

func TestReportMdnsEntries_WithNonPairedTrustedService(t *testing.T) {
	mockMdns := mocks.NewMdnsInterface(t)
	mockMdns.EXPECT().AnnounceMdnsEntry().Return(nil).Maybe()
	mockMdns.EXPECT().RequestMdnsEntries().Maybe()

	hub := newMdnsTestHub(mockMdns)

	ski := "SKI1"
	service, err := api.NewServiceDetails(ski, "", "")
	assert.NoError(t, err)
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
	hub := newMdnsTestHub(mockMdns)

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
	hub := newMdnsTestHub(mockMdns)

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
	hub := newMdnsTestHub(mockMdns)

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
	hub := newMdnsTestHub(mockMdns) // no previous mDNS entries

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
