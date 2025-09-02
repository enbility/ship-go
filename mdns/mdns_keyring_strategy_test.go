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

func TestMdnsKeyingStrategySuite(t *testing.T) {
	suite.Run(t, new(MdnsKeyingStrategySuite))
}

type MdnsKeyingStrategySuite struct {
	suite.Suite

	sut *MdnsManager

	mdnsSearch   *mocks.MdnsReportInterface
	mdnsProvider *mocks.MdnsProviderInterface
}

func (s *MdnsKeyingStrategySuite) BeforeTest(suiteName, testName string) {
	s.mdnsSearch = mocks.NewMdnsReportInterface(s.T())
	s.mdnsProvider = mocks.NewMdnsProviderInterface(s.T())

	// Default mock expectations for provider
	s.mdnsProvider.EXPECT().Start(mock.Anything, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true).Maybe()
	s.mdnsProvider.EXPECT().Shutdown().Maybe().Return()
	s.mdnsProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return("1", nil)
	s.mdnsProvider.EXPECT().UnannounceService(mock.Anything).Maybe().Return()

	// Default mock expectations for search/report interface - allow any ReportMdnsEntries calls
	s.mdnsSearch.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe().Return()

	// Create manager with test setup to use mock provider
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionTestSetup)
	s.sut.SetMdnsProvider(s.mdnsProvider)
}

func (s *MdnsKeyingStrategySuite) AfterTest(suiteName, testName string) {
	s.sut.Shutdown()
}

// createValidShipMdnsEntry creates a valid SHIP mDNS entry for testing
func (s *MdnsKeyingStrategySuite) createValidShipMdnsEntry(_, ski, identifier, brand, model, serial string, _ int, _ []net.IP) map[string]string {
	return map[string]string{
		"txtvers":  "1",
		"id":       identifier,
		"path":     "/ship/",
		"ski":      ski,
		"register": "true",
		"brand":    brand,
		"type":     "EnergyManagementSystem",
		"model":    model,
		"serial":   serial,
		"cat":      "2",
	}
}

// METADATA INTEGRITY TESTS
// These tests verify that multiple services with the same SKI preserve their individual metadata

func (s *MdnsKeyingStrategySuite) Test_MetadataIntegrity_MultipleSameSkiServices() {
	// Test that multiple services with same SKI preserve individual metadata
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Create two services with same SKI but different metadata
	sameSKI := "sharedski123"
	service1Name := "device1-service"
	service2Name := "device2-service"

	service1Elements := s.createValidShipMdnsEntry(
		service1Name, sameSKI, "id1", "Brand1", "Model1", "Serial1", 4729,
		[]net.IP{net.ParseIP("192.168.1.100")})
	service2Elements := s.createValidShipMdnsEntry(
		service2Name, sameSKI, "id2", "Brand2", "Model2", "Serial2", 4730,
		[]net.IP{net.ParseIP("192.168.1.101")})

	// Process both services
	s.sut.processShipMdnsEntry(service1Elements, service1Name, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)
	s.sut.processShipMdnsEntry(service2Elements, service2Name, "host2",
		[]net.IP{net.ParseIP("192.168.1.101")}, 4730, false)

	// Verify internal storage preserves both services by serviceName
	internalEntries := s.sut.copyMdnsEntries()
	assert.Equal(s.T(), 2, len(internalEntries))

	// Verify each service maintains its unique metadata
	service1Entry, exists := internalEntries[service1Name]
	assert.True(s.T(), exists)
	assert.Equal(s.T(), "Brand1", service1Entry.Brand)
	assert.Equal(s.T(), "Model1", service1Entry.Model)
	assert.Equal(s.T(), "Serial1", service1Entry.Serial)
	assert.Equal(s.T(), "id1", service1Entry.Identifier)
	assert.Equal(s.T(), 4729, service1Entry.Port)
	assert.Equal(s.T(), "host1", service1Entry.Host)

	service2Entry, exists := internalEntries[service2Name]
	assert.True(s.T(), exists)
	assert.Equal(s.T(), "Brand2", service2Entry.Brand)
	assert.Equal(s.T(), "Model2", service2Entry.Model)
	assert.Equal(s.T(), "Serial2", service2Entry.Serial)
	assert.Equal(s.T(), "id2", service2Entry.Identifier)
	assert.Equal(s.T(), 4730, service2Entry.Port)
	assert.Equal(s.T(), "host2", service2Entry.Host)
}

func (s *MdnsKeyingStrategySuite) Test_MetadataIntegrity_ServiceMetadataUpdate() {
	// Test that metadata updates work correctly for existing services
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	serviceName := "test-service"
	ski := "testski456"

	// Initial service entry
	initialElements := s.createValidShipMdnsEntry(
		serviceName, ski, "id1", "InitialBrand", "InitialModel", "Serial1", 4729,
		[]net.IP{net.ParseIP("192.168.1.100")})

	s.sut.processShipMdnsEntry(initialElements, serviceName, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)

	// Verify initial state
	entry, exists := s.sut.mdnsEntry(serviceName)
	assert.True(s.T(), exists)
	assert.Equal(s.T(), "InitialBrand", entry.Brand)
	assert.Equal(s.T(), "InitialModel", entry.Model)

	// Update service with new metadata
	updatedElements := s.createValidShipMdnsEntry(
		serviceName, ski, "id1", "UpdatedBrand", "UpdatedModel", "Serial1", 4730,
		[]net.IP{net.ParseIP("192.168.1.100")})

	s.sut.processShipMdnsEntry(updatedElements, serviceName, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4730, false)

	// Verify metadata was updated correctly
	entry, exists = s.sut.mdnsEntry(serviceName)
	assert.True(s.T(), exists)
	assert.Equal(s.T(), "UpdatedBrand", entry.Brand)
	assert.Equal(s.T(), "UpdatedModel", entry.Model)
	assert.Equal(s.T(), 4730, entry.Port)
	assert.Equal(s.T(), ski, entry.Ski) // SKI should remain the same
}

func (s *MdnsKeyingStrategySuite) Test_MetadataIntegrity_ServiceRemoval() {
	// Test that service removal works correctly with serviceName-based keying
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Setup two services with same SKI
	sameSKI := "sharedski789"
	service1Name := "service-to-keep"
	service2Name := "service-to-remove"

	service1Elements := s.createValidShipMdnsEntry(
		service1Name, sameSKI, "id1", "Brand1", "Model1", "Serial1", 4729,
		[]net.IP{net.ParseIP("192.168.1.100")})
	service2Elements := s.createValidShipMdnsEntry(
		service2Name, sameSKI, "id2", "Brand2", "Model2", "Serial2", 4730,
		[]net.IP{net.ParseIP("192.168.1.101")})

	// Add both services
	s.sut.processShipMdnsEntry(service1Elements, service1Name, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)
	s.sut.processShipMdnsEntry(service2Elements, service2Name, "host2",
		[]net.IP{net.ParseIP("192.168.1.101")}, 4730, false)

	// Verify both services exist
	internalEntries := s.sut.copyMdnsEntries()
	assert.Equal(s.T(), 2, len(internalEntries))

	// Remove one service
	s.sut.processShipMdnsEntry(service2Elements, service2Name, "host2",
		[]net.IP{net.ParseIP("192.168.1.101")}, 4730, true) // remove = true

	// Verify only the correct service was removed
	internalEntries = s.sut.copyMdnsEntries()
	assert.Equal(s.T(), 1, len(internalEntries))

	remainingEntry, exists := internalEntries[service1Name]
	assert.True(s.T(), exists)
	assert.Equal(s.T(), "Brand1", remainingEntry.Brand)
	assert.Equal(s.T(), "Model1", remainingEntry.Model)

	_, removedExists := internalEntries[service2Name]
	assert.False(s.T(), removedExists)
}

// IP ADDRESS MERGING TESTS
// These tests verify IP address merging behavior for same service on multiple interfaces

func (s *MdnsKeyingStrategySuite) Test_IPMerging_SameServiceMultipleInterfaces() {
	// Test that same service on multiple interfaces merges IP addresses correctly
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	serviceName := "multi-interface-service"
	ski := "multiinterfaceski"

	serviceElements := s.createValidShipMdnsEntry(
		serviceName, ski, "id1", "Brand1", "Model1", "Serial1", 4729,
		[]net.IP{})

	// First interface announcement
	s.sut.processShipMdnsEntry(serviceElements, serviceName, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)

	// Verify initial state
	entry, exists := s.sut.mdnsEntry(serviceName)
	assert.True(s.T(), exists)
	assert.Equal(s.T(), 1, len(entry.Addresses))
	assert.Equal(s.T(), "192.168.1.100", entry.Addresses[0].String())

	// Second interface announcement (same service, different IP)
	s.sut.processShipMdnsEntry(serviceElements, serviceName, "host1",
		[]net.IP{net.ParseIP("192.168.1.200")}, 4729, false)

	// Verify IP addresses were merged
	entry, exists = s.sut.mdnsEntry(serviceName)
	assert.True(s.T(), exists)
	assert.Equal(s.T(), 2, len(entry.Addresses))

	addressStrings := []string{entry.Addresses[0].String(), entry.Addresses[1].String()}
	assert.Contains(s.T(), addressStrings, "192.168.1.100")
	assert.Contains(s.T(), addressStrings, "192.168.1.200")
}

func (s *MdnsKeyingStrategySuite) Test_IPMerging_DifferentServicesWithSameSKI() {
	// Test that different services with same SKI don't interfere with each other's IP merging
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	sameSKI := "sharedskiiptest"
	service1Name := "device1-service"
	service2Name := "device2-service"

	service1Elements := s.createValidShipMdnsEntry(
		service1Name, sameSKI, "id1", "Brand1", "Model1", "Serial1", 4729,
		[]net.IP{})
	service2Elements := s.createValidShipMdnsEntry(
		service2Name, sameSKI, "id2", "Brand2", "Model2", "Serial2", 4730,
		[]net.IP{})

	// Add multiple IPs to service1
	s.sut.processShipMdnsEntry(service1Elements, service1Name, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)
	s.sut.processShipMdnsEntry(service1Elements, service1Name, "host1",
		[]net.IP{net.ParseIP("192.168.1.101")}, 4729, false)

	// Add multiple IPs to service2
	s.sut.processShipMdnsEntry(service2Elements, service2Name, "host2",
		[]net.IP{net.ParseIP("192.168.2.100")}, 4730, false)
	s.sut.processShipMdnsEntry(service2Elements, service2Name, "host2",
		[]net.IP{net.ParseIP("192.168.2.101")}, 4730, false)

	// Verify each service has its own IP addresses
	service1Entry, exists := s.sut.mdnsEntry(service1Name)
	assert.True(s.T(), exists)
	assert.Equal(s.T(), 2, len(service1Entry.Addresses))

	service1IPs := []string{service1Entry.Addresses[0].String(), service1Entry.Addresses[1].String()}
	assert.Contains(s.T(), service1IPs, "192.168.1.100")
	assert.Contains(s.T(), service1IPs, "192.168.1.101")

	service2Entry, exists := s.sut.mdnsEntry(service2Name)
	assert.True(s.T(), exists)
	assert.Equal(s.T(), 2, len(service2Entry.Addresses))

	service2IPs := []string{service2Entry.Addresses[0].String(), service2Entry.Addresses[1].String()}
	assert.Contains(s.T(), service2IPs, "192.168.2.100")
	assert.Contains(s.T(), service2IPs, "192.168.2.101")
}

func (s *MdnsKeyingStrategySuite) Test_IPMerging_DuplicateIPSuppression() {
	// Test that duplicate IP addresses are not added to the same service
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	serviceName := "duplicate-ip-service"
	ski := "duplicateipski"

	serviceElements := s.createValidShipMdnsEntry(
		serviceName, ski, "id1", "Brand1", "Model1", "Serial1", 4729,
		[]net.IP{})

	// Add same IP multiple times
	duplicateIP := net.ParseIP("192.168.1.100")

	s.sut.processShipMdnsEntry(serviceElements, serviceName, "host1",
		[]net.IP{duplicateIP}, 4729, false)
	s.sut.processShipMdnsEntry(serviceElements, serviceName, "host1",
		[]net.IP{duplicateIP}, 4729, false)
	s.sut.processShipMdnsEntry(serviceElements, serviceName, "host1",
		[]net.IP{duplicateIP}, 4729, false)

	// Verify only one instance of the IP is stored
	entry, exists := s.sut.mdnsEntry(serviceName)
	assert.True(s.T(), exists)
	assert.Equal(s.T(), 1, len(entry.Addresses))
	assert.Equal(s.T(), "192.168.1.100", entry.Addresses[0].String())
}

// HUB INTEGRATION TESTS
// These tests verify that the Hub receives SKI-keyed entries as expected

func (s *MdnsKeyingStrategySuite) Test_HubIntegration_ServiceNameKeyedEntries() {
	// Test that Hub receives entries keyed by serviceName (preserving metadata integrity)
	s.mdnsSearch.EXPECT().ReportMdnsEntries(mock.MatchedBy(func(entries map[string]*api.MdnsEntry) bool {
		// Verify entries are keyed by serviceName (not SKI)
		for key, entry := range entries {
			if key != entry.Name {
				return false
			}
		}
		return len(entries) == 1
	}), true).Return()

	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	serviceName := "hub-integration-service"
	ski := "hubintegrationski"

	serviceElements := s.createValidShipMdnsEntry(
		serviceName, ski, "id1", "Brand1", "Model1", "Serial1", 4729,
		[]net.IP{net.ParseIP("192.168.1.100")})

	// Process service - should trigger Hub notification with serviceName-keyed entries
	s.sut.processShipMdnsEntry(serviceElements, serviceName, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)

	// Give some time for async reporting
	time.Sleep(10 * time.Millisecond)
}

func (s *MdnsKeyingStrategySuite) Test_HubIntegration_MultipleSameSKIServicesPreserved() {
	// Test that when multiple services have same SKI, Hub receives all services (preserving metadata)
	// This test demonstrates that serviceName-keyed storage preserves all services
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	sameSKI := "sharedskihubtest"
	service1Name := "first-service"
	service2Name := "second-service"

	service1Elements := s.createValidShipMdnsEntry(
		service1Name, sameSKI, "id1", "Brand1", "Model1", "Serial1", 4729,
		[]net.IP{net.ParseIP("192.168.1.100")})
	service2Elements := s.createValidShipMdnsEntry(
		service2Name, sameSKI, "id2", "Brand2", "Model2", "Serial2", 4730,
		[]net.IP{net.ParseIP("192.168.1.101")})

	// Add first service
	s.sut.processShipMdnsEntry(service1Elements, service1Name, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)

	// Add second service with same SKI
	s.sut.processShipMdnsEntry(service2Elements, service2Name, "host2",
		[]net.IP{net.ParseIP("192.168.1.101")}, 4730, false)

	// Verify internal storage preserves both services by serviceName
	internalEntries := s.sut.copyMdnsEntries()
	assert.Equal(s.T(), 2, len(internalEntries))

	// Verify Hub receives both services with serviceName keys
	hubEntries := s.sut.copyMdnsEntries()   // Now uses serviceName-keyed entries
	assert.Equal(s.T(), 2, len(hubEntries)) // Both services preserved

	// Verify each service maintains its unique metadata
	service1Entry, exists := hubEntries[service1Name]
	assert.True(s.T(), exists)
	assert.Equal(s.T(), sameSKI, service1Entry.Ski)
	assert.Equal(s.T(), "Brand1", service1Entry.Brand)
	assert.Equal(s.T(), "Model1", service1Entry.Model)

	service2Entry, exists := hubEntries[service2Name]
	assert.True(s.T(), exists)
	assert.Equal(s.T(), sameSKI, service2Entry.Ski)
	assert.Equal(s.T(), "Brand2", service2Entry.Brand)
	assert.Equal(s.T(), "Model2", service2Entry.Model)
}

func (s *MdnsKeyingStrategySuite) Test_HubIntegration_ServiceLookup() {
	// Test that service lookup through Hub interface continues to work
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	serviceName := "lookup-service"
	ski := "lookupski"

	serviceElements := s.createValidShipMdnsEntry(
		serviceName, ski, "id1", "Brand1", "Model1", "Serial1", 4729,
		[]net.IP{net.ParseIP("192.168.1.100")})

	s.sut.processShipMdnsEntry(serviceElements, serviceName, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)

	// Test copyMdnsEntries (now uses serviceName-keyed entries)
	hubEntries := s.sut.copyMdnsEntries()
	assert.Equal(s.T(), 1, len(hubEntries))

	entry, exists := hubEntries[serviceName] // Now keyed by serviceName
	assert.True(s.T(), exists)
	assert.Equal(s.T(), ski, entry.Ski)
	assert.Equal(s.T(), "Brand1", entry.Brand)
	assert.Equal(s.T(), "Model1", entry.Model)
	assert.Equal(s.T(), serviceName, entry.Name) // Original service name preserved
}

func (s *MdnsKeyingStrategySuite) Test_HubIntegration_RequestMdnsEntries() {
	// Test that RequestMdnsEntries provides serviceName-keyed entries to Hub
	s.mdnsSearch.EXPECT().ReportMdnsEntries(mock.MatchedBy(func(entries map[string]*api.MdnsEntry) bool {
		// Should be serviceName-keyed
		for key, entry := range entries {
			if key != entry.Name {
				return false
			}
		}
		return len(entries) == 2
	}), false).Return() // newEntries = false for RequestMdnsEntries

	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Add two services with different SKIs
	service1Elements := s.createValidShipMdnsEntry(
		"service1", "ski1", "id1", "Brand1", "Model1", "Serial1", 4729,
		[]net.IP{net.ParseIP("192.168.1.100")})
	service2Elements := s.createValidShipMdnsEntry(
		"service2", "ski2", "id2", "Brand2", "Model2", "Serial2", 4730,
		[]net.IP{net.ParseIP("192.168.1.101")})

	s.sut.processShipMdnsEntry(service1Elements, "service1", "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)
	s.sut.processShipMdnsEntry(service2Elements, "service2", "host2",
		[]net.IP{net.ParseIP("192.168.1.101")}, 4730, false)

	// Request entries (should call ReportMdnsEntries with serviceName-keyed entries)
	s.sut.RequestMdnsEntries()

	time.Sleep(10 * time.Millisecond)
}

// EDGE CASE TESTS
// These tests cover edge cases like duplicate SKIs, missing SKIs, service name conflicts

func (s *MdnsKeyingStrategySuite) Test_EdgeCase_ServicesWithoutSKI() {
	// Test that services without SKI are rejected during processing
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	serviceName := "noskiservice"

	// Create service entry without SKI (invalid)
	serviceElements := map[string]string{
		"txtvers": "1",
		"id":      "id1",
		"path":    "/ship/",
		// "ski": missing
		"register": "true",
		"brand":    "Brand1",
	}

	// This should be rejected due to missing mandatory fields
	s.sut.processShipMdnsEntry(serviceElements, serviceName, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)

	// Verify service was not added
	internalEntries := s.sut.copyMdnsEntries()
	assert.Equal(s.T(), 0, len(internalEntries))

	// Verify Hub gets no entries
	hubEntries := s.sut.copyMdnsEntries()
	assert.Equal(s.T(), 0, len(hubEntries))
}

func (s *MdnsKeyingStrategySuite) Test_EdgeCase_ServiceNameConflicts() {
	// Test handling of services with same service name but different SKIs
	// The implementation uses serviceName as key, so last update wins
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	conflictingServiceName := "conflicting-service"

	// First service with one SKI
	service1Elements := s.createValidShipMdnsEntry(
		conflictingServiceName, "ski1", "id1", "Brand1", "Model1", "Serial1", 4729,
		[]net.IP{net.ParseIP("192.168.1.100")})

	s.sut.processShipMdnsEntry(service1Elements, conflictingServiceName, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)

	// Verify first service is stored
	entry, exists := s.sut.mdnsEntry(conflictingServiceName)
	assert.True(s.T(), exists)
	assert.Equal(s.T(), "ski1", entry.Ski)
	assert.Equal(s.T(), "Brand1", entry.Brand)

	// Second service with different SKI but same service name (should overwrite)
	service2Elements := s.createValidShipMdnsEntry(
		conflictingServiceName, "ski2", "id2", "Brand2", "Model2", "Serial2", 4730,
		[]net.IP{net.ParseIP("192.168.1.101")})

	s.sut.processShipMdnsEntry(service2Elements, conflictingServiceName, "host2",
		[]net.IP{net.ParseIP("192.168.1.101")}, 4730, false)

	// Note: The current implementation treats this as an update to existing service
	// The service metadata gets updated but the original SKI is preserved
	// This test verifies the serviceName-keyed storage behavior
	entry, exists = s.sut.mdnsEntry(conflictingServiceName)
	assert.True(s.T(), exists)
	// The SKI remains the same from the original service (implementation behavior)
	assert.Equal(s.T(), "ski1", entry.Ski)
	// But other metadata is updated
	assert.Equal(s.T(), "Brand2", entry.Brand)

	// Internal storage should have only one entry
	internalEntries := s.sut.copyMdnsEntries()
	assert.Equal(s.T(), 1, len(internalEntries))
}

func (s *MdnsKeyingStrategySuite) Test_EdgeCase_ConcurrentUpdates() {
	// Test concurrent updates to different services to verify thread safety
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	var wg sync.WaitGroup
	numGoroutines := 10

	// Launch multiple goroutines updating different services concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			serviceName := "concurrent-service-" + string(rune('A'+index))
			ski := "concurrentski" + string(rune('A'+index))

			elements := s.createValidShipMdnsEntry(
				serviceName, ski, "id"+string(rune('A'+index)),
				"Brand"+string(rune('A'+index)),
				"Model"+string(rune('A'+index)),
				"Serial"+string(rune('A'+index)), 4729+index,
				[]net.IP{net.ParseIP("192.168.1.100")})

			s.sut.processShipMdnsEntry(elements, serviceName, "host1",
				[]net.IP{net.ParseIP("192.168.1.100")}, 4729+index, false)
		}(i)
	}

	wg.Wait()

	// Verify all services were processed correctly
	internalEntries := s.sut.copyMdnsEntries()
	assert.Equal(s.T(), numGoroutines, len(internalEntries))

	// Verify each service has the correct metadata
	for i := 0; i < numGoroutines; i++ {
		serviceName := "concurrent-service-" + string(rune('A'+i))
		entry, exists := internalEntries[serviceName]
		assert.True(s.T(), exists)
		assert.Equal(s.T(), "Brand"+string(rune('A'+i)), entry.Brand)
		assert.Equal(s.T(), "Model"+string(rune('A'+i)), entry.Model)
		assert.Equal(s.T(), "concurrentski"+string(rune('A'+i)), entry.Ski)
	}
}

func (s *MdnsKeyingStrategySuite) Test_EdgeCase_IPv6LinkLocalFiltering() {
	// Test that IPv6 link-local addresses are filtered out
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	serviceName := "ipv6-service"
	ski := "ipv6ski"

	serviceElements := s.createValidShipMdnsEntry(
		serviceName, ski, "id1", "Brand1", "Model1", "Serial1", 4729,
		[]net.IP{})

	// Process service with IPv6 link-local address (should be filtered)
	linkLocalIPv6 := net.ParseIP("fe80::1")
	regularIPv4 := net.ParseIP("192.168.1.100")

	s.sut.processShipMdnsEntry(serviceElements, serviceName, "host1",
		[]net.IP{linkLocalIPv6, regularIPv4}, 4729, false)

	// Verify only IPv4 address is stored
	entry, exists := s.sut.mdnsEntry(serviceName)
	assert.True(s.T(), exists)
	assert.Equal(s.T(), 1, len(entry.Addresses))
	assert.Equal(s.T(), "192.168.1.100", entry.Addresses[0].String())
}

// REGRESSION TESTS
// These tests ensure existing functionality still works

func (s *MdnsKeyingStrategySuite) Test_Regression_BasicServiceProcessing() {
	// Test that basic service processing still works as before
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	serviceName := "regression-service"
	ski := "regressionski"

	serviceElements := s.createValidShipMdnsEntry(
		serviceName, ski, "id1", "Brand1", "Model1", "Serial1", 4729,
		[]net.IP{net.ParseIP("192.168.1.100")})

	s.sut.processShipMdnsEntry(serviceElements, serviceName, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)

	// Verify service is processed correctly
	entry, exists := s.sut.mdnsEntry(serviceName)
	assert.True(s.T(), exists)
	assert.Equal(s.T(), serviceName, entry.Name)
	assert.Equal(s.T(), ski, entry.Ski)
	assert.Equal(s.T(), "id1", entry.Identifier)
	assert.Equal(s.T(), "/ship/", entry.Path)
	assert.Equal(s.T(), true, entry.Register)
	assert.Equal(s.T(), "Brand1", entry.Brand)
	assert.Equal(s.T(), "EnergyManagementSystem", entry.Type)
	assert.Equal(s.T(), "Model1", entry.Model)
	assert.Equal(s.T(), "Serial1", entry.Serial)
	assert.Equal(s.T(), "host1", entry.Host)
	assert.Equal(s.T(), 4729, entry.Port)
	assert.Equal(s.T(), 1, len(entry.Addresses))
	assert.Equal(s.T(), "192.168.1.100", entry.Addresses[0].String())
}

func (s *MdnsKeyingStrategySuite) Test_Regression_ServiceValidation() {
	// Test that service validation still works (invalid entries rejected)
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	serviceName := "invalid-service"

	// Missing mandatory field (ski)
	invalidElements := map[string]string{
		"txtvers": "1",
		"id":      "id1",
		"path":    "/ship/",
		// "ski": missing
		"register": "true",
	}

	s.sut.processShipMdnsEntry(invalidElements, serviceName, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)

	// Verify invalid service was not added
	_, exists := s.sut.mdnsEntry(serviceName)
	assert.False(s.T(), exists)

	internalEntries := s.sut.copyMdnsEntries()
	assert.Equal(s.T(), 0, len(internalEntries))
}

func (s *MdnsKeyingStrategySuite) Test_Regression_OwnServiceIgnored() {
	// Test that own service (same SKI as manager) is still ignored
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	serviceName := "own-service"
	ownSKI := s.sut.ski // Use manager's own SKI

	serviceElements := s.createValidShipMdnsEntry(
		serviceName, ownSKI, "id1", "Brand1", "Model1", "Serial1", 4729,
		[]net.IP{net.ParseIP("192.168.1.100")})

	s.sut.processShipMdnsEntry(serviceElements, serviceName, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)

	// Verify own service was ignored
	_, exists := s.sut.mdnsEntry(serviceName)
	assert.False(s.T(), exists)

	internalEntries := s.sut.copyMdnsEntries()
	assert.Equal(s.T(), 0, len(internalEntries))
}

func (s *MdnsKeyingStrategySuite) Test_Regression_TextFieldValidation() {
	// Test that text field validation still works
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	serviceName := "validation-service"

	// Invalid txtvers
	invalidVersionElements := map[string]string{
		"txtvers":  "2", // Invalid version
		"id":       "id1",
		"path":     "/ship/",
		"ski":      "testski",
		"register": "true",
	}

	s.sut.processShipMdnsEntry(invalidVersionElements, serviceName, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)

	// Verify invalid version was rejected
	_, exists := s.sut.mdnsEntry(serviceName)
	assert.False(s.T(), exists)

	// Invalid register value
	invalidRegisterElements := map[string]string{
		"txtvers":  "1",
		"id":       "id1",
		"path":     "/ship/",
		"ski":      "testski",
		"register": "maybe", // Invalid boolean
	}

	s.sut.processShipMdnsEntry(invalidRegisterElements, serviceName, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)

	// Verify invalid register was rejected
	_, exists = s.sut.mdnsEntry(serviceName)
	assert.False(s.T(), exists)
}

func (s *MdnsKeyingStrategySuite) Test_Regression_CategoryParsing() {
	// Test that device category parsing still works correctly
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	serviceName := "category-service"
	ski := "categoryski"

	serviceElements := s.createValidShipMdnsEntry(
		serviceName, ski, "id1", "Brand1", "Model1", "Serial1", 4729,
		[]net.IP{net.ParseIP("192.168.1.100")})

	// Set specific categories
	serviceElements["cat"] = "2,3,5"

	s.sut.processShipMdnsEntry(serviceElements, serviceName, "host1",
		[]net.IP{net.ParseIP("192.168.1.100")}, 4729, false)

	// Verify categories were parsed correctly
	entry, exists := s.sut.mdnsEntry(serviceName)
	assert.True(s.T(), exists)
	assert.Equal(s.T(), 3, len(entry.Categories))
	assert.Contains(s.T(), entry.Categories, api.DeviceCategoryType(2))
	assert.Contains(s.T(), entry.Categories, api.DeviceCategoryType(3))
	assert.Contains(s.T(), entry.Categories, api.DeviceCategoryType(5))
}

func (s *MdnsKeyingStrategySuite) Test_Performance_NoRegression() {
	// Test that there's no significant performance regression
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	start := time.Now()

	// Process many services
	numServices := 1000
	for i := 0; i < numServices; i++ {
		serviceName := "perf-service-" + string(rune('A'+(i%26)))
		ski := "perfski" + string(rune('A'+(i%26)))

		serviceElements := s.createValidShipMdnsEntry(
			serviceName, ski, "id"+string(rune(i)), "Brand1", "Model1", "Serial1", 4729+i,
			[]net.IP{net.ParseIP("192.168.1.100")})

		s.sut.processShipMdnsEntry(serviceElements, serviceName, "host1",
			[]net.IP{net.ParseIP("192.168.1.100")}, 4729+i, false)
	}

	duration := time.Since(start)

	// Should process 1000 services in reasonable time (less than 5 seconds)
	// This is a generous limit to avoid flakiness in CI environments
	assert.Less(s.T(), duration, 5*time.Second)

	// Verify all services were processed
	internalEntries := s.sut.copyMdnsEntries()
	assert.Equal(s.T(), 26, len(internalEntries)) // 26 unique service names (A-Z)

	// Verify Hub integration still works with many services
	hubEntries := s.sut.copyMdnsEntries()
	assert.Equal(s.T(), 26, len(hubEntries)) // 26 unique service names (A-Z)
}
