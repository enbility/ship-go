package hub

import (
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Test mDNS cleanup functionality when services disappear
func TestReportMdnsEntries_CleanupRemovedEntries(t *testing.T) {
	mockMdns := mocks.NewMdnsInterface(t)
	hub := &Hub{
		registry:                 newConnectionRegistry(""),
		remoteServices:           make(map[string]*api.ServiceDetails, 0),
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
	mockHubReader.EXPECT().VisibleRemoteServicesUpdated(mock.AnythingOfType("[]api.RemoteService")).Times(3)
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
		registry:                 newConnectionRegistry(""),
		remoteServices:           make(map[string]*api.ServiceDetails, 0),
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
	mockHubReader.EXPECT().VisibleRemoteServicesUpdated(mock.AnythingOfType("[]api.RemoteService")).Times(2)
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
		registry:                 newConnectionRegistry(""),
		remoteServices:           make(map[string]*api.ServiceDetails, 0),
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
	mockHubReader.EXPECT().VisibleRemoteServicesUpdated(mock.AnythingOfType("[]api.RemoteService")).Once()
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
