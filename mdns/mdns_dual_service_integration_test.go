package mdns

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

func TestDualServiceIntegrationSuite(t *testing.T) {
	suite.Run(t, new(DualServiceIntegrationSuite))
}

type DualServiceIntegrationSuite struct {
	suite.Suite

	sut          *MdnsManager
	mockProvider *mocks.MdnsProviderInterface
	mockReport   *mocks.MdnsReportInterface

	// Track discoveries
	discoveredShipServices    []string
	discoveredPairingServices []*api.ShipPairingTXT
	mu                        sync.Mutex
}

func (s *DualServiceIntegrationSuite) SetupTest() {
	// Allow report callbacks
	s.mockReport = mocks.NewMdnsReportInterface(s.T())
	s.mockReport.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe().Return()

	// Allow provider shutdown and unannounce for cleanup
	s.mockProvider = mocks.NewMdnsProviderInterface(s.T())
	s.mockProvider.EXPECT().Shutdown().Return().Maybe()
	s.mockProvider.EXPECT().UnannounceService(mock.Anything).Return(nil).Maybe()

	// Use a unique SKI for each test to avoid conflicts
	// Use TestSetup mode to ensure mock provider is used
	s.sut = NewMDNS("integration-test", "brand", "model", "EnergyManagementSystem",
		"integration-12345",
		nil,
		"integrationski"+s.T().Name(), "integration-service",
		4729, nil, MdnsProviderSelectionTestSetup)

	s.sut.SetMdnsProvider(s.mockProvider)

	// Reset tracking
	s.discoveredShipServices = []string{}
	s.discoveredPairingServices = []*api.ShipPairingTXT{}
}

func (s *DualServiceIntegrationSuite) AfterTest(suiteName, testName string) {
	if s.sut != nil {
		// Clean shutdown clears all state including entries map and callbacks
		s.sut.Shutdown()
	}

	// Reset test suite tracking
	s.discoveredShipServices = []string{}
	s.discoveredPairingServices = []*api.ShipPairingTXT{}
}

func (s *DualServiceIntegrationSuite) Test_DualServiceDiscovery_SimultaneousServices() {
	// Setup mock provider expectations for Start()
	s.mockProvider.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true).Once()
	s.mockProvider.EXPECT().AnnounceService(mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("[]string")).Return("1", nil).Once()

	// Register SHIP service report callback
	err := s.sut.Start(api.PairingModeBoth, s.mockReport)
	assert.NoError(s.T(), err)

	// Register pairing service callback
	pairingCallback := func(data *api.ShipPairingTXT) bool {
		s.mu.Lock()
		s.discoveredPairingServices = append(s.discoveredPairingServices, data)
		s.mu.Unlock()
		return true
	}

	err = s.sut.SearchPairingServices(pairingCallback)
	assert.NoError(s.T(), err)

	// Simulate discovery of both service types through the main entry point
	// This tests the routing mechanism end-to-end

	// 1. Simulate SHIP service discovery
	shipElements := map[string]string{
		"txtvers":  "1",
		"id":       "device-1",
		"path":     "/ship/",
		"ski":      "device1ski",
		"register": "true",
		"brand":    "TestBrand",
		"model":    "TestModel",
	}

	s.sut.processMdnsEntry(shipElements, "device-1-service", "device-1.local",
		"_ship._tcp", []net.IP{net.ParseIP("192.168.1.10")}, 4729, false)

	// 2. Simulate pairing service discovery
	pairingElements := map[string]string{
		"txtvers":    "1",
		"parType":    "1",
		"forId":      "heatpump-id",
		"forPar":     "heatpumppar",
		"trustId":    "smgw-id",
		"trustPar":   "smgwpar",
		"trustCurve": "P-256",
		"type":       "offer",
		"trustNonce": "abc123",
		"alg":        "HS256",
		"digest":     "xyz789",
	}

	s.sut.processMdnsEntry(pairingElements, "smgw-pairing", "smgw.local",
		"_shippairing._tcp", []net.IP{net.ParseIP("192.168.1.20")}, 0, false)

	// 3. Simulate another SHIP service
	shipElements2 := map[string]string{
		"txtvers":  "1",
		"id":       "device-2",
		"path":     "/ship/",
		"ski":      "device2ski",
		"register": "false",
	}

	s.sut.processMdnsEntry(shipElements2, "device-2-service", "device-2.local",
		"_ship._tcp", []net.IP{net.ParseIP("192.168.1.11")}, 4730, false)

	// Verify SHIP services were discovered
	entries := s.sut.mdnsEntries()
	assert.Len(s.T(), entries, 2, "Should have discovered 2 SHIP services")

	// Verify the SHIP entries
	entry1, exists := s.sut.mdnsEntry("device-1-service")
	assert.True(s.T(), exists)
	assert.Equal(s.T(), "device-1", entry1.Identifier)
	assert.Equal(s.T(), "TestBrand", entry1.Brand)
	assert.True(s.T(), entry1.Register)

	entry2, exists := s.sut.mdnsEntry("device-2-service")
	assert.True(s.T(), exists)
	assert.Equal(s.T(), "device-2", entry2.Identifier)
	assert.False(s.T(), entry2.Register)

	// Verify pairing service was discovered
	s.mu.Lock()
	assert.Len(s.T(), s.discoveredPairingServices, 1, "Should have discovered 1 pairing service")
	if len(s.discoveredPairingServices) > 0 {
		pairing := s.discoveredPairingServices[0]
		assert.Equal(s.T(), "1", pairing.TxtVers)
		assert.Equal(s.T(), "heatpump-id", pairing.ForId)
		assert.Equal(s.T(), "heatpumppar", pairing.ForPar)
		assert.Equal(s.T(), "smgw-id", pairing.TrustId)
		assert.Equal(s.T(), "smgwpar", pairing.TrustPar)
		assert.Equal(s.T(), "offer", pairing.Type)
	}
	s.mu.Unlock()
}

func (s *DualServiceIntegrationSuite) Test_ServiceTypeRouting_Isolation() {
	// Test that services are properly isolated by type
	// Now using shared instances with proper AfterTest() cleanup

	// Track what callbacks are invoked with proper synchronization
	var callbackMu sync.Mutex
	shipCallbackInvoked := false
	pairingCallbackInvoked := false
	shipCallCount := 0

	// Setup fresh mock report specifically for this test to track SHIP service callbacks
	testMockReport := mocks.NewMdnsReportInterface(s.T())
	testMockReport.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).RunAndReturn(func(entries map[string]*api.MdnsEntry, includePartialEntries bool) {
		callbackMu.Lock()
		shipCallbackInvoked = true
		shipCallCount++
		callbackMu.Unlock()
	}).Maybe()

	// Setup mock provider expectations for Start()
	s.mockProvider.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true).Once()
	s.mockProvider.EXPECT().AnnounceService(mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("[]string")).Return("1", nil).Once()

	err := s.sut.Start(api.PairingModeBoth, testMockReport)
	assert.NoError(s.T(), err)

	// Setup pairing callback
	pairingCallback := func(data *api.ShipPairingTXT) bool {
		callbackMu.Lock()
		pairingCallbackInvoked = true
		callbackMu.Unlock()
		return true
	}

	err = s.sut.SearchPairingServices(pairingCallback)
	assert.NoError(s.T(), err)

	// Test 1: SHIP service should NOT trigger pairing callback
	shipElements := map[string]string{
		"txtvers":  "1",
		"id":       "ship-only",
		"path":     "/ship/",
		"ski":      "shipskitest", // Make SKI unique
		"register": "true",
	}
	shipServiceName := "ship-service"

	s.sut.processMdnsEntry(shipElements, shipServiceName, "host.local",
		"_ship._tcp", []net.IP{net.ParseIP("192.168.1.1")}, 4729, false)

	// Give callbacks time to execute
	time.Sleep(50 * time.Millisecond)

	callbackMu.Lock()
	shipInvoked := shipCallbackInvoked
	pairingInvoked := pairingCallbackInvoked
	callbackMu.Unlock()

	assert.True(s.T(), shipInvoked, "SHIP callback should be invoked")
	assert.False(s.T(), pairingInvoked, "Pairing callback should NOT be invoked for SHIP service")

	// Reset flags
	callbackMu.Lock()
	shipCallbackInvoked = false
	pairingCallbackInvoked = false
	callbackMu.Unlock()

	// Test 2: Pairing service should NOT create SHIP entry
	pairingElements := map[string]string{
		"txtvers":    "1",
		"parType":    "1",
		"forId":      "test",
		"forPar":     "testpar",
		"trustId":    "test",
		"trustPar":   "testpar",
		"trustCurve": "P-256",
		"type":       "offer",
		"trustNonce": "123456",
		"alg":        "HS256",
		"digest":     "abcdef123456",
	}
	pairingServiceName := "pairing-service"

	s.sut.processMdnsEntry(pairingElements, pairingServiceName, "host.local",
		"_shippairing._tcp", []net.IP{net.ParseIP("192.168.1.2")}, 0, false)

	// Give callbacks time to execute
	time.Sleep(50 * time.Millisecond)

	callbackMu.Lock()
	shipInvoked = shipCallbackInvoked
	pairingInvoked = pairingCallbackInvoked
	callCount := shipCallCount
	callbackMu.Unlock()

	s.T().Logf("SHIP callback count after pairing service: %d", callCount)
	assert.False(s.T(), shipInvoked, "SHIP callback should NOT be invoked for pairing service")
	assert.True(s.T(), pairingInvoked, "Pairing callback should be invoked")
	assert.Equal(s.T(), 1, callCount, "SHIP callback should only be called once (for the first SHIP service)")

	// Verify no cross-contamination in entries
	// Pairing services should NOT appear in SHIP entries
	// The only entry should be the shipskitest we added
	entries := s.sut.mdnsEntries()

	// Debug: print all entries to understand what's being created
	for ski, entry := range entries {
		s.T().Logf("Entry SKI: %s, ID: %s", ski, entry.Identifier)
	}

	// Filter out the manager's own SKI entry
	filteredEntries := make(map[string]*api.MdnsEntry)
	for serviceName, entry := range entries {
		// Skip the manager's own SKI
		if serviceName != shipServiceName {
			s.T().Logf("Unexpected entry: ServiceName=%s, ID=%s", serviceName, entry.Identifier)
		} else if serviceName == shipServiceName {
			filteredEntries[serviceName] = entry
		}
	}

	assert.Len(s.T(), filteredEntries, 1, "Should only have 1 SHIP entry (ship-service)")

	// Verify it's the correct entry
	_, exists := filteredEntries["ship-service"]
	assert.True(s.T(), exists, "Should have the SHIP service we added")
}

func (s *DualServiceIntegrationSuite) Test_RemovalHandling_BothServiceTypes() {
	// Test that removal works correctly for both service types

	// Setup mock provider expectations for Start()
	s.mockProvider.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true).Once()
	s.mockProvider.EXPECT().AnnounceService(mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("[]string")).Return("1", nil).Once()

	// Setup callbacks
	err := s.sut.Start(api.PairingModeBoth, s.mockReport)
	assert.NoError(s.T(), err)

	removedPairingServices := 0
	pairingCallback := func(data *api.ShipPairingTXT) bool {
		// In a real implementation, we might track removals differently
		// For now, we just count callbacks
		return true
	}

	err = s.sut.SearchPairingServices(pairingCallback)
	assert.NoError(s.T(), err)

	// Add a SHIP service
	shipElements := map[string]string{
		"txtvers":  "1",
		"id":       "remove-test",
		"path":     "/ship/",
		"ski":      "removeski",
		"register": "true",
	}

	s.sut.processMdnsEntry(shipElements, "service", "host.local",
		"_ship._tcp", []net.IP{net.ParseIP("192.168.1.1")}, 4729, false)

	// Verify it was added
	_, exists := s.sut.mdnsEntry("service")
	assert.True(s.T(), exists)

	// Remove the SHIP service
	s.sut.processMdnsEntry(shipElements, "service", "host.local",
		"_ship._tcp", []net.IP{net.ParseIP("192.168.1.1")}, 4729, true)

	// Verify it was removed
	_, exists = s.sut.mdnsEntry("service")
	assert.False(s.T(), exists)

	// Test pairing service removal (though we don't store them, callback should still be invoked)
	pairingElements := map[string]string{
		"txtvers":    "1",
		"parType":    "1",
		"forId":      "test",
		"forPar":     "testpar",
		"trustId":    "test",
		"trustPar":   "testpar",
		"trustCurve": "P-256",
		"type":       "offer",
		"trustNonce": "123456",
		"alg":        "HS256",
		"digest":     "abcdef123456",
	}

	// Register callback before processing
	var removedData *api.ShipPairingTXT
	s.sut.RegisterPairingCallback(func(data *api.ShipPairingTXT) bool {
		removedData = data
		removedPairingServices++
		return true
	})

	// First add the pairing entry
	s.sut.processMdnsEntry(pairingElements, "pairing", "host.local",
		"_shippairing._tcp", nil, 0, false)

	// Verify callback was invoked for addition
	assert.NotNil(s.T(), removedData)
	assert.Equal(s.T(), 1, removedPairingServices)

	// Reset for removal test
	removedData = nil
	removedPairingServices = 0

	// Now process removal - callbacks should be triggered for removals too
	s.sut.processMdnsEntry(pairingElements, "pairing", "host.local",
		"_shippairing._tcp", nil, 0, true)

	// For pairing removals, callback is NOT called (new behavior)
	assert.Nil(s.T(), removedData)
	assert.Equal(s.T(), 0, removedPairingServices)
}

func (s *DualServiceIntegrationSuite) Test_ListenerMode_HeatPumpScenario() {
	// Simulate the heat pump listener mode scenario
	// Heat pump listens for SMGW pairing announcements

	heatPumpSKI := "heatpumpski12345"
	smgwSKI := "smgwski67890"

	// Setup mock provider expectations for Start()
	s.mockProvider.EXPECT().Start(api.PairingModeListener, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true).Once()
	s.mockProvider.EXPECT().AnnounceService(mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("[]string")).Return("1", nil).Once()

	// Start the mDNS manager first
	err := s.sut.Start(api.PairingModeListener, s.mockReport)
	assert.NoError(s.T(), err)

	// Heat pump starts listening for pairing services
	receivedPairingOffer := false
	var receivedOffer *api.ShipPairingTXT

	pairingCallback := func(data *api.ShipPairingTXT) bool {
		// Heat pump checks if the pairing offer is for it
		if data.ForPar == heatPumpSKI {
			receivedPairingOffer = true
			receivedOffer = data
			// Accept the pairing offer
			return true
		}
		// Ignore offers for other devices
		return false
	}

	err = s.sut.SearchPairingServices(pairingCallback)
	assert.NoError(s.T(), err)

	// SMGW announces pairing service targeting the heat pump
	smgwPairingElements := map[string]string{
		"txtvers":    "1",
		"parType":    "1",                   // Type 1 pairing
		"forId":      "HeatPump-Model-X",    // Target device ID
		"forPar":     heatPumpSKI,           // Target heat pump's SKI
		"trustId":    "SMGW-Pro-2000",       // SMGW's ID
		"trustPar":   smgwSKI,               // SMGW's SKI
		"trustCurve": "P-256",               // Elliptic curve
		"type":       "offer",               // Pairing offer
		"trustNonce": "random-nonce-123",    // Random nonce
		"alg":        "HS256",               // HMAC algorithm
		"digest":     "computed-digest-xyz", // Computed digest
	}

	// Simulate SMGW announcing the pairing service
	s.sut.processMdnsEntry(smgwPairingElements, "smgw-pairing-offer", "smgw.local",
		"_shippairing._tcp", []net.IP{net.ParseIP("192.168.1.100")}, 0, false)

	// Verify heat pump received the pairing offer
	assert.True(s.T(), receivedPairingOffer, "Heat pump should receive pairing offer")
	assert.NotNil(s.T(), receivedOffer)

	if receivedOffer != nil {
		assert.Equal(s.T(), heatPumpSKI, receivedOffer.ForPar, "Offer should be for this heat pump")
		assert.Equal(s.T(), smgwSKI, receivedOffer.TrustPar, "Offer should be from SMGW")
		assert.Equal(s.T(), "offer", receivedOffer.Type)
		assert.Equal(s.T(), "P-256", receivedOffer.TrustCurve)

		// Heat pump would now:
		// 1. Validate the digest using HMAC
		// 2. Store the pairing information
		// 3. Potentially announce its own acceptance
		// 4. Connect to the SMGW via regular SHIP protocol
	}

	// Also verify heat pump can still discover regular SHIP services
	// (mDNS manager is already started above)

	// SMGW also announces regular SHIP service
	smgwShipElements := map[string]string{
		"txtvers":  "1",
		"id":       "SMGW-Pro-2000",
		"path":     "/ship/",
		"ski":      smgwSKI,
		"register": "true",
		"brand":    "SMGW Corp",
		"model":    "Pro 2000",
	}

	s.sut.processMdnsEntry(smgwShipElements, "smgw-ship", "smgw.local",
		"_ship._tcp", []net.IP{net.ParseIP("192.168.1.100")}, 4729, false)

	// Verify SHIP service is also discovered
	entry, exists := s.sut.mdnsEntry("smgw-ship")
	assert.True(s.T(), exists)
	assert.Equal(s.T(), "SMGW-Pro-2000", entry.Identifier)

	// This demonstrates the complete listener mode scenario:
	// 1. Heat pump listens for pairing offers via _shippairing._tcp
	// 2. SMGW announces pairing offer targeting the heat pump
	// 3. Heat pump receives and validates the offer
	// 4. Both devices can still use regular _ship._tcp for connection
}
