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

func TestSearchPairingIntegrationSuite(t *testing.T) {
	suite.Run(t, new(SearchPairingIntegrationSuite))
}

type SearchPairingIntegrationSuite struct {
	suite.Suite

	sut          *MdnsManager
	mockProvider *mocks.MdnsProviderInterface
}

func (s *SearchPairingIntegrationSuite) SetupTest() {
	s.mockProvider = mocks.NewMdnsProviderInterface(s.T())

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		nil,
		"testski", "serviceName",
		4729, nil, MdnsProviderSelectionTestSetup)

	// Set the mock provider
	s.sut.SetMdnsProvider(s.mockProvider)
}

func (s *SearchPairingIntegrationSuite) Test_SearchPairingServices_ActiveDiscovery() {
	// SearchPairingServices should ensure the provider is actively browsing for pairing services
	// This test validates that the callback is properly registered and invoked when
	// pairing services are discovered through the provider

	var discoveredServices []*api.ShipPairingTXT
	var mu sync.Mutex

	callback := func(data *api.ShipPairingTXT) bool {
		mu.Lock()
		discoveredServices = append(discoveredServices, data)
		mu.Unlock()
		return true
	}

	// Mock provider Start and Announce
	s.mockProvider.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).
		RunAndReturn(func(pairingMode api.PairingMode, browsing bool, cb api.MdnsResolveCB) bool {
			// Simulate the provider discovering pairing services

			// Simulate discovery of a pairing service after a short delay
			go func() {
				time.Sleep(10 * time.Millisecond)

				// Simulate SMGW announcing pairing for heat pump
				pairingElements := map[string]string{
					"txtvers":    "1",
					"parType":    "1",
					"forId":      "HeatPump-X",
					"forPar":     "heatpump-par-123",
					"trustId":    "SMGW-Pro",
					"trustPar":   "smgw-par-456",
					"trustCurve": "P-256",
					"type":       "offer",
					"trustNonce": "nonce123",
					"alg":        "HS256",
					"digest":     "digest456",
				}
				cb(pairingElements, "smgw-pairing", "smgw.local", "_shippairing._tcp", []net.IP{}, 0, false)
			}()
			return true
		}).
		Once()
	s.mockProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil).Once()

	// Start the manager first
	mockReport := mocks.NewMdnsReportInterface(s.T())
	mockReport.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe().Return()
	err := s.sut.Start(api.PairingModeBoth, mockReport)
	assert.NoError(s.T(), err)

	// Now call SearchPairingServices
	err = s.sut.SearchPairingServices(callback)
	assert.NoError(s.T(), err)

	// Wait for discovery
	time.Sleep(50 * time.Millisecond)

	// Verify service was discovered
	mu.Lock()
	assert.Len(s.T(), discoveredServices, 1)
	if len(discoveredServices) > 0 {
		service := discoveredServices[0]
		assert.Equal(s.T(), "HeatPump-X", service.ForId)
		assert.Equal(s.T(), "heatpump-par-123", service.ForPar)
		assert.Equal(s.T(), "SMGW-Pro", service.TrustId)
		assert.Equal(s.T(), "smgw-par-456", service.TrustPar)
		assert.Equal(s.T(), "offer", service.Type)
	}
	mu.Unlock()
}

func (s *SearchPairingIntegrationSuite) Test_SearchPairingServices_FiltersBySKI() {
	// Test that SearchPairingServices can filter pairing offers by target SKI
	// This simulates a heat pump only accepting pairing offers meant for it

	heatPumpPAR := "heatpump-par-abc"
	var receivedOffer *api.ShipPairingTXT

	// Callback that only accepts offers for this specific heat pump
	callback := func(data *api.ShipPairingTXT) bool {
		if data.ForPar == heatPumpPAR {
			receivedOffer = data
			return true
		}
		return false
	}

	// Start the provider and manager
	s.mockProvider.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true).Once()
	s.mockProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil).Once()

	mockReport := mocks.NewMdnsReportInterface(s.T())
	mockReport.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe().Return()
	err := s.sut.Start(api.PairingModeBoth, mockReport)
	assert.NoError(s.T(), err)

	// Register the callback
	err = s.sut.SearchPairingServices(callback)
	assert.NoError(s.T(), err)

	// Simulate multiple pairing announcements
	pairingForOther := map[string]string{
		"txtvers":    "1",
		"parType":    "1",
		"forId":      "OtherDevice",
		"forPar":     "other-par-xyz", // Different PAR
		"trustId":    "SMGW-1",
		"trustPar":   "smgw-par-1",
		"trustCurve": "P-256",
		"type":       "offer",
		"trustNonce": "nonce123",
		"alg":        "HS256",
		"digest":     "digest123",
	}

	pairingForUs := map[string]string{
		"txtvers":    "1",
		"parType":    "1",
		"forId":      "HeatPump",
		"forPar":     heatPumpPAR, // Our PAR
		"trustId":    "SMGW-2",
		"trustPar":   "smgw-par-2",
		"trustCurve": "P-256",
		"type":       "offer",
		"trustNonce": "nonce456",
		"alg":        "HS256",
		"digest":     "digest456",
	}

	// Process both entries
	s.sut.processShipPairingMdnsEntry(pairingForOther, "servicename1", false)
	s.sut.processShipPairingMdnsEntry(pairingForUs, "servicename2", false)

	// Only the offer for our PAR should be accepted
	assert.NotNil(s.T(), receivedOffer)
	assert.Equal(s.T(), heatPumpPAR, receivedOffer.ForPar)
	assert.Equal(s.T(), "SMGW-2", receivedOffer.TrustId)
}

func (s *SearchPairingIntegrationSuite) Test_SearchPairingServices_HandlesRemoval() {
	// Test that pairing service removals are handled correctly

	var callbackCount int
	var mu sync.Mutex

	callback := func(data *api.ShipPairingTXT) bool {
		mu.Lock()
		defer mu.Unlock()
		// In a real implementation, we might track whether this is an add or remove
		// For now, we just count callbacks
		callbackCount++
		return true
	}

	// Start the provider and manager
	s.mockProvider.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true).Once()
	s.mockProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil).Once()

	// Start the manager first
	mockReport := mocks.NewMdnsReportInterface(s.T())
	mockReport.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe().Return()
	err := s.sut.Start(api.PairingModeBoth, mockReport)
	assert.NoError(s.T(), err)

	err = s.sut.SearchPairingServices(callback)
	assert.NoError(s.T(), err)

	pairingElements := map[string]string{
		"txtvers":    "1",
		"parType":    "1",
		"forId":      "Device",
		"forPar":     "device-par",
		"trustId":    "SMGW",
		"trustPar":   "smgw-par",
		"trustCurve": "P-256",
		"type":       "offer",
		"trustNonce": "nonce123",
		"alg":        "HS256",
		"digest":     "digest123",
	}

	// Add the service
	s.sut.processShipPairingMdnsEntry(pairingElements, "servicename", false)

	// Remove the service
	s.sut.processShipPairingMdnsEntry(pairingElements, "servicename", true)

	mu.Lock()
	// Only add should trigger callbacks
	assert.Equal(s.T(), 1, callbackCount)
	mu.Unlock()
}

func (s *SearchPairingIntegrationSuite) Test_SearchPairingServices_ReplaceCallback() {
	// Test that calling SearchPairingServices multiple times replaces the callback

	var firstCalled, secondCalled bool

	firstCallback := func(data *api.ShipPairingTXT) bool {
		firstCalled = true
		return true
	}

	secondCallback := func(data *api.ShipPairingTXT) bool {
		secondCalled = true
		return true
	}

	// Start the provider and manager
	s.mockProvider.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true).Once()
	s.mockProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil).Once()

	mockReport := mocks.NewMdnsReportInterface(s.T())
	mockReport.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe().Return()
	err := s.sut.Start(api.PairingModeBoth, mockReport)
	assert.NoError(s.T(), err)

	// Register first callback
	err = s.sut.SearchPairingServices(firstCallback)
	assert.NoError(s.T(), err)

	// Replace with second callback
	err = s.sut.SearchPairingServices(secondCallback)
	assert.NoError(s.T(), err)

	// Trigger a discovery
	pairingElements := map[string]string{
		"txtvers":    "1",
		"parType":    "1",
		"forId":      "Device",
		"forPar":     "device-par",
		"trustId":    "SMGW",
		"trustPar":   "smgw-par",
		"trustCurve": "P-256",
		"type":       "offer",
		"trustNonce": "nonce123",
		"alg":        "HS256",
		"digest":     "digest123",
	}

	s.sut.processShipPairingMdnsEntry(pairingElements, "servicename", false)

	// Only second callback should be called
	assert.False(s.T(), firstCalled)
	assert.True(s.T(), secondCalled)
}

func (s *SearchPairingIntegrationSuite) Test_SearchPairingServices_WithProviderRestart() {
	// Test that SearchPairingServices works correctly when provider needs to be started

	callback := func(data *api.ShipPairingTXT) bool {
		return true
	}

	// Start the provider and manager
	s.mockProvider.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true).Once()
	s.mockProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil).Once()

	mockReport := mocks.NewMdnsReportInterface(s.T())
	mockReport.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe().Return()
	err := s.sut.Start(api.PairingModeBoth, mockReport)
	assert.NoError(s.T(), err)

	// SearchPairingServices should work after manager is started
	err = s.sut.SearchPairingServices(callback)
	assert.NoError(s.T(), err)

	// Verify callback is registered
	s.sut.mux.Lock()
	assert.NotNil(s.T(), s.sut.pairingCallback)
	s.sut.mux.Unlock()
}

func (s *SearchPairingIntegrationSuite) Test_SearchPairingServices_ValidationErrors() {
	// Test error cases

	// Nil callback
	err := s.sut.SearchPairingServices(nil)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "callback cannot be nil")

	// Manager not started
	err = s.sut.SearchPairingServices(func(*api.ShipPairingTXT) bool { return true })
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "not started")

	// Start the manager first
	s.mockProvider.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true).Once()
	s.mockProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil).Once()

	mockReport := mocks.NewMdnsReportInterface(s.T())
	mockReport.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe().Return()
	err = s.sut.Start(api.PairingModeBoth, mockReport)
	assert.NoError(s.T(), err)

	// Now test with no provider
	s.sut.mdnsProvider = nil
	err = s.sut.SearchPairingServices(func(*api.ShipPairingTXT) bool { return true })
	assert.Error(s.T(), err)
	assert.Equal(s.T(), api.ErrMDNSSearchFailed, err)
}

func (s *SearchPairingIntegrationSuite) Test_SearchPairingServices_RealWorldScenario() {
	// Simulate a real-world heat pump listening for SMGW pairing announcements

	heatPumpPAR := "HP-PAR-12345"
	smgwPAR := "SMGW-PAR-67890"

	// Track all discovered pairing offers
	var offers []*api.ShipPairingTXT
	var acceptedOffer *api.ShipPairingTXT
	var mu sync.Mutex

	// Heat pump's pairing callback
	heatPumpCallback := func(data *api.ShipPairingTXT) bool {
		mu.Lock()
		defer mu.Unlock()

		offers = append(offers, data)

		// Accept only offers targeting this heat pump
		if data.ForPar == heatPumpPAR {
			// Validate the offer (in real implementation, would check digest, etc.)
			if data.Type == "offer" && data.TrustPar != "" {
				acceptedOffer = data
				return true
			}
		}
		return false
	}

	// Start the provider and manager
	s.mockProvider.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true).Once()
	s.mockProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil).Once()

	mockReport := mocks.NewMdnsReportInterface(s.T())
	mockReport.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe().Return()
	err := s.sut.Start(api.PairingModeBoth, mockReport)
	assert.NoError(s.T(), err)

	// Heat pump starts listening
	err = s.sut.SearchPairingServices(heatPumpCallback)
	assert.NoError(s.T(), err)

	// Simulate SMGW broadcasting pairing offers
	// First, an offer for a different device
	otherOffer := map[string]string{
		"txtvers":    "1",
		"parType":    "1",
		"forId":      "WashingMachine-Model-Z",
		"forPar":     "WM-PAR-99999",
		"trustId":    "SMGW-Pro-2000",
		"trustPar":   smgwPAR,
		"trustCurve": "P-256",
		"type":       "offer",
		"trustNonce": "random-123",
		"alg":        "HS256",
		"digest":     "calculated-digest",
	}

	s.sut.processShipPairingMdnsEntry(otherOffer, "servicename1", false)

	// Then, an offer for our heat pump
	ourOffer := map[string]string{
		"txtvers":    "1",
		"parType":    "1",
		"forId":      "HeatPump-Model-X",
		"forPar":     heatPumpPAR,
		"trustId":    "SMGW-Pro-2000",
		"trustPar":   smgwPAR,
		"trustCurve": "P-256",
		"type":       "offer",
		"trustNonce": "random-456",
		"alg":        "HS256",
		"digest":     "verified-digest",
	}

	s.sut.processShipPairingMdnsEntry(ourOffer, "servicename2", false)

	// Verify results
	mu.Lock()
	assert.Len(s.T(), offers, 2, "Should have seen both offers")
	assert.NotNil(s.T(), acceptedOffer, "Should have accepted the offer for our heat pump")
	if acceptedOffer != nil {
		assert.Equal(s.T(), heatPumpPAR, acceptedOffer.ForPar)
		assert.Equal(s.T(), smgwPAR, acceptedOffer.TrustPar)
		assert.Equal(s.T(), "HeatPump-Model-X", acceptedOffer.ForId)
	}
	mu.Unlock()
}
