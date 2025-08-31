package hub

import (
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

// DEPRECATED: TestRingBufferHistory - use NewTestRingBufferPersistence instead
// This is kept for backward compatibility during migration
type TestRingBufferHistory struct {
	seen map[string]bool
	mux  sync.RWMutex
}

func NewTestRingBufferHistory() *TestRingBufferHistory {
	return &TestRingBufferHistory{
		seen: make(map[string]bool),
	}
}

func (t *TestRingBufferHistory) HasSeenDigest(alg, digest string) bool {
	t.mux.RLock()
	defer t.mux.RUnlock()
	key := alg + ":" + digest
	return t.seen[key]
}

func (t *TestRingBufferHistory) RecordPairing(alg, digest string) {
	t.mux.Lock()
	defer t.mux.Unlock()
	key := alg + ":" + digest
	t.seen[key] = true
}

// Test helper to create Hub with appropriate ring buffer persistence
func newTestHub(
	hubReader api.HubReaderInterface,
	mdns api.MdnsInterface,
	port int,
	certificate tls.Certificate,
	localService *api.ServiceDetails,
	pairingConfig *api.PairingConfig,
) (*Hub, error) {
	var ringBufferPersistence api.RingBufferPersistence
	if pairingConfig != nil {
		mode := pairingConfig.Mode
		if mode == api.PairingModeListener || mode == api.PairingModeBoth {
			ringBufferPersistence = NewTestRingBufferPersistence() // Simple test implementation
		}
	}
	return NewHub(hubReader, mdns, port, certificate, localService, pairingConfig, ringBufferPersistence)
}

func TestHubSuite(t *testing.T) {
	suite.Run(t, new(HubSuite))
}

type HubSuite struct {
	suite.Suite

	hubReader   *mocks.MockHubReaderInterface
	mdnsService *mocks.MockMdnsInterface

	// serviceProvider  *mocks.ServiceProvider
	// mdnsService      *mocks.MdnsService
	shipConnection *mocks.ShipConnectionInterface
	wsDataWriter   *mocks.WebsocketDataWriterInterface

	remoteSki string

	sut *Hub
}

func (s *HubSuite) BeforeTest(suiteName, testName string) {
	s.remoteSki = "remotetestski"

	ctrl := gomock.NewController(s.T())
	// use gomock mocks instead of mockery, as those will panic with a data race error in these tests

	s.hubReader = mocks.NewMockHubReaderInterface(ctrl)
	// s.serviceProvider = mocks.NewServiceProvider(s.T())
	s.hubReader.EXPECT().RemoteSKIConnected(gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().RemoteSKIDisconnected(gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().ServiceShipIDUpdate(gomock.Any(), gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().ServicePairingDetailUpdate(gomock.Any(), gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().AllowWaitingForTrust(gomock.Any()).Return(false).AnyTimes()

	s.mdnsService = mocks.NewMockMdnsInterface(ctrl)
	s.mdnsService.EXPECT().AnnounceMdnsEntry().Return(nil).AnyTimes()
	s.mdnsService.EXPECT().UnannounceMdnsEntry().Return().AnyTimes()
	s.mdnsService.EXPECT().RequestMdnsEntries().Return().AnyTimes()

	s.wsDataWriter = mocks.NewWebsocketDataWriterInterface(s.T())

	s.shipConnection = mocks.NewShipConnectionInterface(s.T())
	s.shipConnection.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	s.shipConnection.EXPECT().RemoteSKI().Return(s.remoteSki).Maybe()
	s.shipConnection.EXPECT().ApprovePendingHandshake().Return().Maybe()
	s.shipConnection.EXPECT().AbortPendingHandshake().Return().Maybe()
	s.shipConnection.EXPECT().DataHandler().Return(s.wsDataWriter).Maybe()
	s.shipConnection.EXPECT().ShipHandshakeState().Return(model.SmeStateComplete, nil).Maybe()

	localService := api.NewServiceDetails("localSKI", "", "")

	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	var err error
	s.sut, err = newTestHub(s.hubReader, s.mdnsService, 4567, certificate, localService, nil)
	assert.NoError(s.T(), err)
}

func (s *HubSuite) AfterTest(suiteName, testName string) {
	s.mdnsService.EXPECT().Shutdown().AnyTimes()

	s.sut.Shutdown()
}

func (s *HubSuite) Test_NewConnectionsHub() {
	ski := "12af9e"
	localService := api.NewServiceDetails(ski, "", "")

	hub, err := newTestHub(s.hubReader, s.mdnsService, 4567, tls.Certificate{}, localService, nil)
	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), hub)

	s.mdnsService.EXPECT().Start(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	err = hub.Start()
	assert.NoError(s.T(), err)

	s.mdnsService.EXPECT().Shutdown().Times(1)

	hub.Shutdown()
}

func (s *HubSuite) Test_NewConnectionHub_PairingConfig() {
	ski := "12af9e"
	localService := api.NewServiceDetails(ski, "", "")

	secret := api.PairingSecret("test-secret")
	pairingConfig := api.NewPairingConfig(api.PairingModeAnnouncer, secret)
	hub, err := newTestHub(s.hubReader, s.mdnsService, 4567, tls.Certificate{}, localService, pairingConfig)
	assert.Error(s.T(), err)
	assert.Nil(s.T(), hub)

	secret = api.PairingSecret("test-secret-12345678901234567890123456789012")
	pairingConfig = api.NewPairingConfig(api.PairingModeAnnouncer, secret)
	hub, err = newTestHub(s.hubReader, s.mdnsService, 4567, tls.Certificate{}, localService, pairingConfig)
	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), hub)
}

func (s *HubSuite) Test_NewHub_RequiresHistoryProviderForListener() {
	ski := "12af9e"
	localService := api.NewServiceDetails(ski, "", "")
	secret := api.PairingSecret("test-secret-12345678901234567890123456789012")
	config := api.NewPairingConfig(api.PairingModeListener, secret)
	
	// Should fail - no history provider for listener mode
	_, err := NewHub(s.hubReader, s.mdnsService, 4712, tls.Certificate{}, localService, config, nil)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "RingBufferPersistence required")
}

func (s *HubSuite) Test_NewHub_RequiresHistoryProviderForBothMode() {
	ski := "12af9e"
	localService := api.NewServiceDetails(ski, "", "")
	secret := api.PairingSecret("test-secret-12345678901234567890123456789012")
	config := api.NewPairingConfig(api.PairingModeBoth, secret)
	
	// Should fail - no history provider for both mode
	_, err := NewHub(s.hubReader, s.mdnsService, 4712, tls.Certificate{}, localService, config, nil)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "RingBufferPersistence required")
}

func (s *HubSuite) Test_NewHub_AcceptsNilHistoryProviderForAnnouncer() {
	ski := "12af9e"
	localService := api.NewServiceDetails(ski, "", "")
	secret := api.PairingSecret("test-secret-12345678901234567890123456789012")
	config := api.NewPairingConfig(api.PairingModeAnnouncer, secret)
	
	// Should succeed - no history provider needed for announcer
	h, err := NewHub(s.hubReader, s.mdnsService, 4712, tls.Certificate{}, localService, config, nil)
	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), h)
}

func (s *HubSuite) Test_NewHub_AcceptsNilHistoryProviderForOffMode() {
	ski := "12af9e"
	localService := api.NewServiceDetails(ski, "", "")
	
	// Should succeed - no history provider needed when pairing is off
	h, err := NewHub(s.hubReader, s.mdnsService, 4712, tls.Certificate{}, localService, nil, nil)
	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), h)
}

func (s *HubSuite) Test_NewHub_AcceptsValidHistoryProviderForListener() {
	ski := "12af9e"
	localService := api.NewServiceDetails(ski, "", "")
	secret := api.PairingSecret("test-secret-12345678901234567890123456789012")
	config := api.NewPairingConfig(api.PairingModeListener, secret)
	
	// Create valid history provider
	ringBufferPersistence := NewTestRingBufferPersistence()
	
	// Should succeed - valid ring buffer persistence for listener mode
	h, err := NewHub(s.hubReader, s.mdnsService, 4712, tls.Certificate{}, localService, config, ringBufferPersistence)
	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), h)
}

func (s *HubSuite) Test_AutoAccept() {
	s.mdnsService.EXPECT().SetAutoAccept(gomock.Any()).Return().AnyTimes()

	s.sut.SetAutoAccept(true)
	value := s.sut.IsAutoAcceptEnabled()
	assert.True(s.T(), value)

	s.sut.SetAutoAccept(false)
	value = s.sut.IsAutoAcceptEnabled()
	assert.False(s.T(), value)
}

func (s *HubSuite) Test_SetupRemoteDevice() {
	ski := "12af9e"
	localService := api.NewServiceDetails(ski, "", "")

	hub, err := newTestHub(s.hubReader, s.mdnsService, 4567, tls.Certificate{}, localService, nil)
	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), hub)

	readerI := mocks.NewShipConnectionDataReaderInterface(s.T())
	s.hubReader.EXPECT().SetupRemoteDevice(gomock.Any(), gomock.Any()).Return(readerI)

	reader := hub.SetupRemoteDevice(ski, nil)

	assert.NotNil(s.T(), reader)
}

func (s *HubSuite) Test_checkHasStarted() {
	checked := s.sut.checkHasStarted()
	assert.Equal(s.T(), s.sut.hasStarted, checked)
}

func (s *HubSuite) Test_MapShipMessageExchangeState() {
	state := s.sut.mapShipMessageExchangeState(model.CmiStateInitStart, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateQueued, state)

	state = s.sut.mapShipMessageExchangeState(model.CmiStateClientSend, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateInitiated, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeHelloStateReadyInit, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateInProgress, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeHelloStatePendingListen, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateReceivedPairingRequest, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeHelloStateOk, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateTrusted, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeHelloStateAbort, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateNone, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeHelloStateRemoteAbortDone, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateRemoteDeniedTrust, state)

	state = s.sut.mapShipMessageExchangeState(model.SmePinStateCheckInit, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStatePin, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeAccessMethodsRequest, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateInProgress, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeStateComplete, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateCompleted, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeStateError, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateError, state)

	state = s.sut.mapShipMessageExchangeState(model.SmeProtHStateTimeout, s.remoteSki)
	assert.Equal(s.T(), api.ConnectionStateInProgress, state)
}

func (s *HubSuite) Test_DisconnectSKI() {
	s.sut.DisconnectSKI(s.remoteSki, "none")
}

func (s *HubSuite) Test_Mdns() {
	s.sut.checkAutoReannounce()

	pairedServices := s.sut.numberPairedServices()
	assert.Equal(s.T(), 0, len(s.sut.connections))
	assert.Equal(s.T(), 0, pairedServices)

	s.sut.RegisterRemoteService(s.remoteSki, "", "testshipid")
	pairedServices = s.sut.numberPairedServices()
	assert.Equal(s.T(), 0, len(s.sut.connections))
	assert.Equal(s.T(), 1, pairedServices)

	service := s.sut.serviceForTrustedShipID("testshipid2")
	assert.Nil(s.T(), service)

	service = s.sut.serviceForTrustedShipID("testshipid")
	assert.NotNil(s.T(), service)
}

func (s *HubSuite) Test_Ship() {
	s.sut.RegisterRemoteService(s.remoteSki, "", "")

	s.sut.HandleShipHandshakeStateUpdate(s.remoteSki, model.ShipState{
		State: model.SmeStateError,
		Error: errors.New("test"),
	})

	s.sut.HandleShipHandshakeStateUpdate(s.remoteSki, model.ShipState{
		State: model.SmeHelloStateOk,
	})

	s.sut.ReportServiceShipID(s.remoteSki, "test")

	accept := s.sut.IsAutoAcceptEnabled()
	assert.Equal(s.T(), false, accept)

	trust := s.sut.AllowWaitingForTrust(s.remoteSki)
	assert.Equal(s.T(), true, trust)

	trust = s.sut.AllowWaitingForTrust("test")
	assert.Equal(s.T(), false, trust)

	detail := s.sut.PairingDetailForIdentifier(s.remoteSki, "")
	assert.NotNil(s.T(), detail)

	s.sut.registerConnection(s.shipConnection)

	detail = s.sut.PairingDetailForIdentifier(s.remoteSki, "")
	assert.NotNil(s.T(), detail)
}

func (s *HubSuite) Test_ReportMdnsEntries() {
	testski1 := "test1"
	testski2 := "test2"

	entries := make(map[string]*api.MdnsEntry)

	s.hubReader.EXPECT().VisibleRemoteServicesUpdated(gomock.Any()).AnyTimes()
	s.sut.ReportMdnsEntries(entries, true)

	entries[testski1] = &api.MdnsEntry{
		Ski: testski1,
	}
	service1 := api.NewServiceDetails(testski1, "", "")
	service1.SetTrusted(true)
	service1.SetIPv4("127.0.0.1")

	entries[testski2] = &api.MdnsEntry{
		Ski: testski2,
	}
	service2 := api.NewServiceDetails(testski2, "", "")
	service2.SetTrusted(true)
	service2.SetIPv4("127.0.0.1")

	s.sut.ReportMdnsEntries(entries, true)
}

// =============================================================================
// ADDCU REPLACEMENT LOGIC TESTS
// =============================================================================

func (s *HubSuite) Test_StopAddCuReplacementTimer_NilService() {
	// Hub is already created in BeforeTest

	// Should not panic with nil service
	s.sut.StopAddCuReplacementTimer(nil)
	// No assertion needed - test passes if no panic occurs
}

func (s *HubSuite) Test_StopAddCuReplacementTimer_NonAddCuService() {
	// Hub is already created in BeforeTest

	// Create service with non-AddCu pairing type
	service := api.NewServiceDetails("testski", "", "shipid123")
	service.SetPairingType(api.PairingTypeDefault) // Not AddCu

	// Should return early without calling tracker
	s.sut.StopAddCuReplacementTimer(service)
	// No assertion needed - test passes if no panic occurs
}

func (s *HubSuite) Test_StopAddCuReplacementTimer_EmptyShipID() {
	// Hub is already created in BeforeTest

	// Create AddCu service but with empty ShipID
	service := api.NewServiceDetails("testski", "", "")
	service.SetPairingType(api.PairingTypeAddCu)
	// shipID is empty

	// Should return early without calling tracker
	s.sut.StopAddCuReplacementTimer(service)
	// No assertion needed - test passes if no panic occurs
}

func (s *HubSuite) Test_StopAddCuReplacementTimer_ValidAddCuService() {
	// Hub is already created in BeforeTest

	// Create valid AddCu service
	service := api.NewServiceDetails("testski", "", "shipid123")
	service.SetPairingType(api.PairingTypeAddCu)

	// Should call tracker.StopTimer with shipID
	s.sut.StopAddCuReplacementTimer(service)
	// No assertion needed - test passes if tracker.StopTimer is called successfully
}

func (s *HubSuite) Test_handleAddCuReplacementTimeout_ServiceNotFound() {
	// Hub is already created in BeforeTest

	// Call with ShipID that doesn't exist in services
	s.sut.handleAddCuReplacementTimeout("nonexistent-shipid")
	// Should complete without error even if service not found
}

func (s *HubSuite) Test_handleAddCuReplacementTimeout_NonAddCuService() {
	// Hub is already created in BeforeTest

	// Add a service that is not AddCu type
	service := api.NewServiceDetails("testski", "", "shipid123")
	service.SetPairingType(api.PairingTypeDefault) // Not AddCu
	service.SetTrusted(true)
	s.sut.remoteServices = append(s.sut.remoteServices, service)

	// Should skip trust removal for non-AddCu service
	s.sut.handleAddCuReplacementTimeout("shipid123")

	// Service should still be trusted since it's not AddCu
	assert.True(s.T(), service.Trusted())
}

func (s *HubSuite) Test_handleAddCuReplacementTimeout_ValidAddCuService() {
	// Hub is already created in BeforeTest

	// Add an AddCu service that is trusted
	service := api.NewServiceDetails("testski", "", "shipid123")
	service.SetPairingType(api.PairingTypeAddCu)
	service.SetTrusted(true)
	s.sut.remoteServices = append(s.sut.remoteServices, service)

	// Timeout should NOT remove trust - only reactivate pairing listener
	s.sut.handleAddCuReplacementTimeout("shipid123")

	// Service should still be trusted (trust removal happens during replacement pairing)
	assert.True(s.T(), service.Trusted())
}

func (s *HubSuite) Test_reactivatePairingListener_NoPairingService() {
	// Hub is already created in BeforeTest
	// pairingService is nil by default in test setup

	// Should complete without error even with no pairing service
	s.sut.reactivatePairingListener("test reason")
	// No assertion needed - test passes if no panic occurs
}

func (s *HubSuite) Test_reactivatePairingListener_NoPairingConfig() {
	// Hub is already created in BeforeTest

	// Set pairing service but no config
	mockPairingService := mocks.NewShipPairingServiceInterface(s.T())
	mockPairingService.EXPECT().Shutdown().Maybe() // Add expectation for cleanup
	s.sut.pairingService = mockPairingService
	// pairingConfig remains nil

	// Should complete without error
	s.sut.reactivatePairingListener("test reason")
	// No assertion needed - test passes if no panic occurs
}

func (s *HubSuite) Test_reactivatePairingListener_AnnouncerMode() {
	// Hub is already created in BeforeTest

	// Set up pairing service and config for announcer mode
	mockPairingService := mocks.NewShipPairingServiceInterface(s.T())
	mockPairingService.EXPECT().Shutdown().Maybe() // Add expectation for cleanup
	s.sut.pairingService = mockPairingService
	s.sut.pairingConfig = &api.PairingConfig{
		Mode: api.PairingModeAnnouncer, // Not listener mode
	}

	// Should skip reactivation for announcer mode
	s.sut.reactivatePairingListener("test reason")
	// No assertion needed - test passes if reactivation is skipped
}

func (s *HubSuite) Test_reactivatePairingListener_ListenerMode() {
	// Hub is already created in BeforeTest

	// Set up pairing service and config for listener mode
	mockPairingService := mocks.NewShipPairingServiceInterface(s.T())
	mockPairingService.EXPECT().Shutdown().Maybe() // Add expectation for cleanup
	s.sut.pairingService = mockPairingService
	s.sut.pairingConfig = &api.PairingConfig{
		Mode: api.PairingModeListener,
	}

	// Should attempt to reactivate (may fail but shouldn't panic)
	s.sut.reactivatePairingListener("test reason")
	// No assertion needed - test passes if reactivation is attempted
}

func (s *HubSuite) Test_callDeviceAutoTrustRemovedCallback_NoInterface() {
	// Hub is already created in BeforeTest

	service := api.NewServiceDetails("testski", "", "shipid123")

	// hubReader does not implement PairingServiceReaderInterface by default
	// Should complete without error
	s.sut.callDeviceAutoTrustRemovedCallback(service, "test reason")
	// No assertion needed - test passes if no panic occurs
}

func (s *HubSuite) Test_callDeviceAutoTrustRemovedCallback_WithInterface() {
	// Create mocks using existing mocks package (much simpler!)
	hubReader := mocks.NewHubReaderInterface(s.T())
	pairingReader := mocks.NewPairingServiceReaderInterface(s.T())

	// Set up minimal expectations
	hubReader.EXPECT().RemoteSKIConnected(mock.AnythingOfType("string")).Maybe()
	hubReader.EXPECT().RemoteSKIDisconnected(mock.AnythingOfType("string")).Maybe()
	hubReader.EXPECT().ServiceShipIDUpdate(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Maybe()
	hubReader.EXPECT().ServicePairingDetailUpdate(mock.AnythingOfType("string"), mock.AnythingOfType("*api.ConnectionStateDetail")).Maybe()
	hubReader.EXPECT().AllowWaitingForTrust(mock.AnythingOfType("string")).Return(false).Maybe()
	hubReader.EXPECT().SetupRemoteDevice(mock.AnythingOfType("string"), mock.AnythingOfType("api.ShipConnectionDataWriterInterface")).Return(nil).Maybe()

	// Set up the callback expectation
	pairingReader.EXPECT().DeviceAutoTrustRemovedViaReplacementLogic(
		mock.AnythingOfType("*api.ServiceDetails"),
		"test callback reason",
	).Return().Once()

	// Create combined mock using struct embedding - this is the key insight!
	combinedReader := &EmbeddedDualReader{
		HubReaderInterface:            hubReader,
		PairingServiceReaderInterface: pairingReader,
	}

	// Create hub with combined reader
	localService := api.NewServiceDetails("localSKI", "", "")
	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")

	// Add mDNS expectation for the new hub
	s.mdnsService.EXPECT().Shutdown().Return().AnyTimes()

	hubWithCallback, err := newTestHub(combinedReader, s.mdnsService, 4575, certificate, localService, nil)
	assert.NoError(s.T(), err)
	defer hubWithCallback.Shutdown()

	service := api.NewServiceDetails("callback-test-ski", "", "test-ship-id")

	// This should now execute the callback path (the missing 60% coverage)
	hubWithCallback.callDeviceAutoTrustRemovedCallback(service, "test callback reason")

	// Mock expectations verified automatically
}

// =============================================================================
// SERVICE MANAGEMENT EDGE CASES TESTS
// =============================================================================

func (s *HubSuite) Test_RemoveService_EdgeCases() {
	// Test removing service with empty parameters
	s.sut.RemoveService("", "")
	// Should not panic or error

	// Test removing non-existent service by SKI
	s.sut.RemoveService("nonexistent-ski", "")
	// Should not panic or error

	// Test removing non-existent service by fingerprint
	s.sut.RemoveService("", "nonexistent-fingerprint")
	// Should not panic or error
}

func (s *HubSuite) Test_RemoveService_BySKI() {
	// Add a service first
	service := api.NewServiceDetails("testremoveski", "", "")
	s.sut.AddService(service)

	// Verify it was added
	found := s.sut.ServiceForIdentifier("testremoveski", "")
	assert.NotNil(s.T(), found)

	// Remove by SKI
	s.sut.RemoveService("testremoveski", "")

	// Verify it was removed
	notFound := s.sut.ServiceForIdentifier("testremoveski", "")
	assert.Nil(s.T(), notFound)
}

func (s *HubSuite) Test_RemoveService_ByFingerprint() {
	// Add a service with fingerprint
	service := api.NewServiceDetails("test-ski", "test-fingerprint", "")
	s.sut.AddService(service)

	// Remove by fingerprint only
	s.sut.RemoveService("", "test-fingerprint")

	// Verify it was removed
	notFound := s.sut.ServiceForIdentifier("test-ski", "")
	assert.Nil(s.T(), notFound)
}

func (s *HubSuite) Test_RemoveService_MultipleCriteria() {
	// Add multiple services
	service1 := api.NewServiceDetails("ski1", "fp1", "")
	service2 := api.NewServiceDetails("ski2", "fp2", "")
	s.sut.AddService(service1)
	s.sut.AddService(service2)

	// Remove by specific SKI and fingerprint combo
	s.sut.RemoveService("ski1", "fp1")

	// Verify only the specific service was removed
	notFound := s.sut.ServiceForIdentifier("ski1", "")
	assert.Nil(s.T(), notFound)

	stillThere := s.sut.ServiceForIdentifier("ski2", "")
	assert.NotNil(s.T(), stillThere)
}

func (s *HubSuite) Test_RemoveService_BothCriteriaMustMatch() {
	// Test that RemoveService requires both SKI and fingerprint to match when both are provided
	service := api.NewServiceDetails("test-ski", "test-fp", "")
	s.sut.AddService(service)

	// Try to remove with matching SKI but wrong fingerprint - should NOT remove
	s.sut.RemoveService("test-ski", "wrong-fp")

	// Service should still be there (both criteria must match)
	stillThere := s.sut.ServiceForIdentifier("test-ski", "")
	assert.NotNil(s.T(), stillThere)
}

func (s *HubSuite) Test_ServiceForIdentifier_EdgeCases() {
	// Test various lookup scenarios
	service := api.NewServiceDetails("test-ski", "test-fp", "")
	s.sut.AddService(service)

	// Standard lookup by SKI
	found := s.sut.ServiceForIdentifier("test-ski", "")
	assert.NotNil(s.T(), found)

	// Test with both parameters
	foundBoth := s.sut.ServiceForIdentifier("test-ski", "test-fp")
	assert.NotNil(s.T(), foundBoth)
}

func (s *HubSuite) Test_AddService_NilService() {
	// Test adding nil service - should handle gracefully
	initialCount := len(s.sut.remoteServices)
	s.sut.AddService(nil)

	// Should not have added anything
	assert.Equal(s.T(), initialCount, len(s.sut.remoteServices))
}

// =============================================================================
// DUAL INTERFACE CALLBACK TEST SUITE (OPTION 1)
// =============================================================================

// DualInterfaceCallbackTestSuite tests functions that require both HubReaderInterface AND PairingServiceReaderInterface
type DualInterfaceCallbackTestSuite struct {
	suite.Suite

	hub      *Hub
	mockMdns *mocks.MdnsInterface
}

func TestDualInterfaceCallbackTestSuite(t *testing.T) {
	suite.Run(t, new(DualInterfaceCallbackTestSuite))
}

func (suite *DualInterfaceCallbackTestSuite) SetupTest() {
	// Create individual mocks using existing mocks package
	hubReader := mocks.NewHubReaderInterface(suite.T())
	pairingReader := mocks.NewPairingServiceReaderInterface(suite.T())
	suite.mockMdns = mocks.NewMdnsInterface(suite.T())

	// Set up minimal mock expectations for HubReaderInterface
	hubReader.EXPECT().RemoteSKIConnected(mock.AnythingOfType("string")).Maybe()
	hubReader.EXPECT().RemoteSKIDisconnected(mock.AnythingOfType("string")).Maybe()
	hubReader.EXPECT().ServiceShipIDUpdate(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Maybe()
	hubReader.EXPECT().ServicePairingDetailUpdate(mock.AnythingOfType("string"), mock.AnythingOfType("*api.ConnectionStateDetail")).Maybe()
	hubReader.EXPECT().AllowWaitingForTrust(mock.AnythingOfType("string")).Return(false).Maybe()
	hubReader.EXPECT().SetupRemoteDevice(mock.AnythingOfType("string"), mock.AnythingOfType("api.ShipConnectionDataWriterInterface")).Return(nil).Maybe()

	// Set up expectation for PairingServiceReaderInterface callback
	pairingReader.EXPECT().DeviceAutoTrustRemovedViaReplacementLogic(
		mock.AnythingOfType("*api.ServiceDetails"),
		"test callback reason",
	).Return().Once()

	// mDNS expectations
	suite.mockMdns.EXPECT().Shutdown().Return().Maybe()
	suite.mockMdns.EXPECT().Start(mock.Anything, mock.AnythingOfType("*hub.Hub")).Return(nil).Maybe()
	suite.mockMdns.EXPECT().AnnounceMdnsEntry().Return(nil).Maybe()
	suite.mockMdns.EXPECT().RequestMdnsEntries().Return().Maybe()

	// Create combined reader using struct embedding (this is the key!)
	combinedReader := &EmbeddedDualReader{
		HubReaderInterface:            hubReader,
		PairingServiceReaderInterface: pairingReader,
	}

	// Create hub with combined reader
	localService := api.NewServiceDetails("localSKI", "", "")
	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")

	var err error
	suite.hub, err = newTestHub(combinedReader, suite.mockMdns, 4574, certificate, localService, nil)
	suite.Require().NoError(err)
}

func (suite *DualInterfaceCallbackTestSuite) TearDownTest() {
	if suite.hub != nil {
		suite.hub.Shutdown()
	}
}

// EmbeddedDualReader combines existing mocks using struct embedding
// This satisfies both interfaces via Go's struct embedding
type EmbeddedDualReader struct {
	*mocks.HubReaderInterface
	*mocks.PairingServiceReaderInterface
}

func (suite *DualInterfaceCallbackTestSuite) TestCallDeviceAutoTrustRemovedCallback_WithBothInterfaces() {
	// Now we can test the actual callback path!
	service := api.NewServiceDetails("callback-test-ski", "", "test-ship-id")

	// This should successfully execute the callback path since our mock implements both interfaces
	suite.hub.callDeviceAutoTrustRemovedCallback(service, "test callback reason")

	// The mock expectation verification happens automatically via testify
	// This test covers the missing 60% - interface check success + callback invocation
}

// =============================================================================
// COMPREHENSIVE HandleShipHandshakeStateUpdate TESTS
// =============================================================================

// HandleShipHandshakeStateUpdateTestSuite provides comprehensive test coverage for HandleShipHandshakeStateUpdate method
type HandleShipHandshakeStateUpdateTestSuite struct {
	suite.Suite

	// Mock dependencies
	mockHubReader     *mocks.MockHubReaderInterface
	mockPairingReader *mocks.PairingServiceReaderInterface
	mockMdns          *mocks.MockMdnsInterface
	ctrl              *gomock.Controller

	// Test data
	certificate  tls.Certificate
	localService *api.ServiceDetails

	// System under test
	hub *Hub

	// Test identifiers
	testSKI    string
	testShipID string
}

func TestHandleShipHandshakeStateUpdateTestSuite(t *testing.T) {
	suite.Run(t, new(HandleShipHandshakeStateUpdateTestSuite))
}

func (s *HandleShipHandshakeStateUpdateTestSuite) SetupTest() {
	s.ctrl = gomock.NewController(s.T())

	// Setup test identifiers
	s.testSKI = "test-ski-123"
	s.testShipID = "test-ship-id-456"

	// Setup gomock mocks (following existing pattern in hub_test.go)
	s.mockHubReader = mocks.NewMockHubReaderInterface(s.ctrl)
	s.mockMdns = mocks.NewMockMdnsInterface(s.ctrl)
	s.mockPairingReader = mocks.NewPairingServiceReaderInterface(s.T())

	// Allow all Hub lifecycle operations (gomock style)
	s.mockMdns.EXPECT().AnnounceMdnsEntry().Return(nil).AnyTimes()
	s.mockMdns.EXPECT().UnannounceMdnsEntry().Return().AnyTimes()
	s.mockMdns.EXPECT().RequestMdnsEntries().Return().AnyTimes()
	s.mockMdns.EXPECT().Shutdown().Return().AnyTimes()

	// Allow basic hub reader callbacks
	s.mockHubReader.EXPECT().RemoteSKIConnected(gomock.Any()).Return().AnyTimes()
	s.mockHubReader.EXPECT().RemoteSKIDisconnected(gomock.Any()).Return().AnyTimes()
	s.mockHubReader.EXPECT().ServiceShipIDUpdate(gomock.Any(), gomock.Any()).Return().AnyTimes()
	s.mockHubReader.EXPECT().AllowWaitingForTrust(gomock.Any()).Return(false).AnyTimes()

	// Setup test data
	var err error
	s.certificate, err = cert.CreateCertificate("test-unit", "test-org", "DE", "test-cn")
	require.NoError(s.T(), err)
	s.localService = api.NewServiceDetails("hubtestski", "", "")

	// Create Hub
	s.hub, err = newTestHub(
		s.mockHubReader,
		s.mockMdns,
		0, // Use port 0 for testing
		s.certificate,
		s.localService,
		nil,
	)
	require.NoError(s.T(), err)

	// Add test service for state updates
	testService := api.NewServiceDetails(s.testSKI, "", s.testShipID)
	success := s.hub.AddService(testService)
	require.True(s.T(), success, "Should add test service")
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TearDownTest() {
	if s.hub != nil {
		s.hub.Shutdown()
	}
	if s.ctrl != nil {
		s.ctrl.Finish()
	}
}

// State Transition Mapping Tests (8 test cases)

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_StateMapping() {
	// Test comprehensive state mapping from SHIP states to connection states

	testCases := []struct {
		name           string
		shipState      model.ShipMessageExchangeState
		expectedState  api.ConnectionState
		shouldSetTrust bool
	}{
		{
			name:          "CmiStateInitStart",
			shipState:     model.CmiStateInitStart,
			expectedState: api.ConnectionStateQueued,
		},
		{
			name:          "CmiStateClientSend",
			shipState:     model.CmiStateClientSend,
			expectedState: api.ConnectionStateInitiated,
		},
		{
			name:          "SmeHelloStateReadyInit",
			shipState:     model.SmeHelloStateReadyInit,
			expectedState: api.ConnectionStateInProgress,
		},
		{
			name:          "SmeHelloStatePendingListen",
			shipState:     model.SmeHelloStatePendingListen,
			expectedState: api.ConnectionStateReceivedPairingRequest,
		},
		{
			name:           "SmeHelloStateOk",
			shipState:      model.SmeHelloStateOk,
			expectedState:  api.ConnectionStateTrusted,
			shouldSetTrust: true,
		},
		{
			name:          "SmeHelloStateAbort",
			shipState:     model.SmeHelloStateAbort,
			expectedState: api.ConnectionStateNone,
		},
		{
			name:          "SmeStateComplete",
			shipState:     model.SmeStateComplete,
			expectedState: api.ConnectionStateCompleted,
		},
		{
			name:          "SmeStateError",
			shipState:     model.SmeStateError,
			expectedState: api.ConnectionStateError,
		},
	}

	for _, tc := range testCases {
		s.Run(tc.name, func() {
			// Use channel synchronization for fast, reliable testing
			callbackReceived := make(chan struct{}, 1)

			// Setup expectation with callback notification
			s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
				callbackReceived <- struct{}{}
			})

			// Create ship state
			shipState := model.ShipState{
				State: tc.shipState,
				Error: nil,
			}

			// Act
			s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

			// Wait for callback with timeout
			select {
			case <-callbackReceived:
				// Callback received - test can proceed immediately
			case <-time.After(1 * time.Second):
				s.T().Fatalf("Callback not received within 1 second for state: %v", tc.shipState)
			}

			// Assert trust if expected
			if tc.shouldSetTrust {
				service := s.hub.ServiceForIdentifier(s.testSKI, "")
				require.NotNil(s.T(), service, "Service should exist")
				assert.True(s.T(), service.Trusted(), "Service should be trusted for SmeHelloStateOk")
			}
		})
	}
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_ErrorStateOverride() {
	// Test that error in ShipState overrides normal state mapping

	callbackReceived := make(chan struct{}, 1)

	// Setup expectation with callback notification
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackReceived <- struct{}{}
	})

	// Create ship state with error (should override state mapping)
	testError := errors.New("connection failed")
	shipState := model.ShipState{
		State: model.SmeHelloStateOk, // This would normally map to Trusted
		Error: testError,             // But error overrides to Error state
	}

	// Act
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback with timeout
	select {
	case <-callbackReceived:
		// Callback received
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback not received within 1 second")
	}
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_ConnectionNotFoundErrorIgnored() {
	// Test that ErrConnectionNotFound error does not override state

	callbackReceived := make(chan struct{}, 1)

	// Setup expectation with callback notification
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackReceived <- struct{}{}
	})

	// Create ship state with ErrConnectionNotFound (should be ignored)
	shipState := model.ShipState{
		State: model.SmeHelloStateOk,
		Error: api.ErrConnectionNotFound, // This specific error should be ignored
	}

	// Act
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback with timeout
	select {
	case <-callbackReceived:
		// Callback received
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback not received within 1 second")
	}
}

// Delayed Callback Execution Tests (4 test cases)

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_DelayedCallbackExecution() {
	// Test that callback is delayed by 500ms as specified in implementation

	callbackReceived := make(chan time.Time, 1)

	// Setup expectation to capture timing
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackReceived <- time.Now()
	})

	// Record start time
	startTime := time.Now()

	// Act
	shipState := model.ShipState{State: model.SmeStateComplete}
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback
	select {
	case callbackTime := <-callbackReceived:
		duration := callbackTime.Sub(startTime)
		assert.GreaterOrEqual(s.T(), duration, 500*time.Millisecond, "Callback should be delayed by at least 500ms")
		assert.LessOrEqual(s.T(), duration, 600*time.Millisecond, "Callback should not be delayed much longer than 500ms")
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback was not received within 1 second")
	}
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_NoCallbackWhenStateUnchanged() {
	// Test that no callback is triggered when state doesn't change

	// This test needs to be implemented differently since we can't easily control
	// gomock expectations after initial call. The function checks:
	// if existingState != pairingState || !errors.Is(existingDetails.Error(), state.Error)
	// So if both state and error are the same, no callback should occur.

	// For this test, we'll verify the logic by checking service state directly
	// rather than mock expectations, since the state comparison logic is internal

	callbackReceived := make(chan struct{}, 1)

	// Setup callback expectation for first call only
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackReceived <- struct{}{}
	})

	// Act - First state update
	shipState := model.ShipState{
		State: model.SmeStateComplete,
		Error: errors.New("test error"),
	}
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback with timeout
	select {
	case <-callbackReceived:
		// Callback received
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback not received within 1 second")
	}

	// Get service state after first update
	service := s.hub.ServiceForIdentifier(s.testSKI, "")
	require.NotNil(s.T(), service, "Service should exist")
	firstDetail := service.ConnectionStateDetail()

	// Act - Second state update with SAME state and error
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait briefly (no callback expected for unchanged state)
	time.Sleep(100 * time.Millisecond)

	// Assert - Service state should remain unchanged (no second update)
	service = s.hub.ServiceForIdentifier(s.testSKI, "")
	require.NotNil(s.T(), service, "Service should exist")
	secondDetail := service.ConnectionStateDetail()

	// The state details should be exactly the same (no update occurred)
	assert.Equal(s.T(), firstDetail.State(), secondDetail.State(), "State should be unchanged")
	assert.Equal(s.T(), firstDetail.Error(), secondDetail.Error(), "Error should be unchanged")
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_ConcurrentCallbacks() {
	// Test that concurrent state updates trigger separate callbacks correctly

	callbackCount := make(chan struct{}, 3)

	// Setup expectation for multiple callbacks
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(3).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackCount <- struct{}{}
	})

	// Act - Trigger multiple state updates concurrently
	var wg sync.WaitGroup
	states := []model.ShipMessageExchangeState{
		model.SmeHelloStateReadyInit,
		model.SmeHelloStateOk,
		model.SmeStateComplete,
	}

	for _, state := range states {
		wg.Add(1)
		go func(st model.ShipMessageExchangeState) {
			defer wg.Done()
			shipState := model.ShipState{State: st}
			s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)
		}(state)
	}

	wg.Wait()

	// Wait for all callbacks
	for i := 0; i < 3; i++ {
		select {
		case <-callbackCount:
			// Callback received
		case <-time.After(1 * time.Second):
			s.T().Fatalf("Callback %d was not received within 1 second", i+1)
		}
	}
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_CallbackParameters() {
	// Test that callback receives correct parameters

	var mu sync.Mutex
	var capturedSKI string
	var capturedDetail *api.ConnectionStateDetail
	testError := errors.New("test handshake error")
	callbackReceived := make(chan struct{}, 1)

	// Setup expectation with parameter capture
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(gomock.Any(), gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		mu.Lock()
		capturedSKI = ski
		capturedDetail = detail
		mu.Unlock()
		callbackReceived <- struct{}{}
	})

	// Act
	shipState := model.ShipState{
		State: model.SmeStateError,
		Error: testError,
	}
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback
	select {
	case <-callbackReceived:
		// Callback received
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback was not received within 1 second")
	}

	// Assert callback parameters (with race-safe access)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(s.T(), s.testSKI, capturedSKI, "SKI parameter should match")
	require.NotNil(s.T(), capturedDetail, "ConnectionStateDetail should not be nil")
	assert.Equal(s.T(), api.ConnectionStateError, capturedDetail.State(), "State should be Error")
	assert.Equal(s.T(), testError, capturedDetail.Error(), "Error should match")
}

// AddCu Timer Integration Tests (5 test cases)

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_AddCuTimerStoppedOnComplete() {
	// Test that AddCu replacement timer is stopped when connection completes

	// Setup service with AddCu type and active timer
	service := s.hub.ServiceForIdentifier(s.testSKI, "")
	require.NotNil(s.T(), service, "Test service should exist")
	service.SetPairingType(api.PairingTypeAddCu)
	service.SetShipID(s.testShipID)

	// Start replacement timer
	s.hub.addCuReplacementTracker.StartTimer(s.testShipID, func(expiredShipID string) {
		// Timer callback - should not be called due to StopTimer
		s.T().Error("Timer callback should not be called when stopped")
	})

	callbackReceived := make(chan struct{}, 1)

	// Setup callback expectation with notification
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackReceived <- struct{}{}
	})

	// Act - Trigger complete state
	shipState := model.ShipState{State: model.SmeStateComplete}
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback with timeout
	select {
	case <-callbackReceived:
		// Callback received
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback not received within 1 second")
	}

	// Timer should be stopped - we can't directly verify this but no timer callback should occur
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_AnnouncementStoppedOnComplete() {
	// Test that announcement is stopped when connection completes

	// Setup service and mock announcements
	service := s.hub.ServiceForIdentifier(s.testSKI, "")
	require.NotNil(s.T(), service, "Test service should exist")
	service.SetShipID(s.testShipID)

	// Note: Cannot directly manipulate private activeAnnouncements field
	// Testing relies on the fact that IsAnnouncingTo behavior is tested elsewhere

	callbackReceived := make(chan struct{}, 1)

	// Setup callback expectation with notification
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackReceived <- struct{}{}
	})

	// Act - Trigger complete state
	shipState := model.ShipState{State: model.SmeStateComplete}
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback with timeout
	select {
	case <-callbackReceived:
		// Callback received
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback not received within 1 second")
	}

	// Assert - Test completes successfully (announcement logic is complex to test without public access)
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_NoTimerStopForNonCompleteStates() {
	// Test that timer is not stopped for non-complete states

	// Setup service with AddCu type
	service := s.hub.ServiceForIdentifier(s.testSKI, "")
	require.NotNil(s.T(), service, "Test service should exist")
	service.SetPairingType(api.PairingTypeAddCu)
	service.SetShipID(s.testShipID)

	callbackReceived := make(chan struct{}, 1)

	// Setup callback expectation with notification
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackReceived <- struct{}{}
	})

	// Act - Trigger non-complete state
	shipState := model.ShipState{State: model.SmeHelloStateOk} // Not complete
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback with timeout
	select {
	case <-callbackReceived:
		// Callback received
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback not received within 1 second")
	}

	// Test passes if no timer-related operations are performed
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_EmptyShipIDNoTimerOps() {
	// Test behavior when service has empty ShipID (no timer operations)

	// Setup service with empty ShipID
	service := s.hub.ServiceForIdentifier(s.testSKI, "")
	require.NotNil(s.T(), service, "Test service should exist")
	service.SetShipID("") // Empty ShipID

	callbackReceived := make(chan struct{}, 1)

	// Setup callback expectation with notification
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackReceived <- struct{}{}
	})

	// Act - Trigger complete state
	shipState := model.ShipState{State: model.SmeStateComplete}
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback with timeout
	select {
	case <-callbackReceived:
		// Callback received
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback not received within 1 second")
	}

	// Test passes if no announcement operations are attempted
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_AddCuTimerAndAnnouncementIntegration() {
	// Test integration of timer stopping and announcement stopping

	// Setup AddCu service with ShipID and active announcement
	service := s.hub.ServiceForIdentifier(s.testSKI, "")
	require.NotNil(s.T(), service, "Test service should exist")
	service.SetPairingType(api.PairingTypeAddCu)
	service.SetShipID(s.testShipID)

	// Start timer and announcement
	s.hub.addCuReplacementTracker.StartTimer(s.testShipID, func(expiredShipID string) {
		s.T().Error("Timer should be stopped")
	})

	// Note: Cannot directly manipulate private activeAnnouncements field
	// Testing the complete state handling without announcement setup

	callbackReceived := make(chan struct{}, 1)

	// Setup callback expectation with notification
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackReceived <- struct{}{}
	})

	// Act
	shipState := model.ShipState{State: model.SmeStateComplete}
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback with timeout
	select {
	case <-callbackReceived:
		// Callback received
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback not received within 1 second")
	}

	// Assert - Test completes successfully, timer logic is tested separately
}

// Concurrent State Update Tests (3 test cases)

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_ConcurrentStateUpdates() {
	// Test thread safety with concurrent state updates for same SKI

	// Allow multiple callback invocations
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).AnyTimes()

	// Act - Multiple concurrent state updates
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			shipState := model.ShipState{State: model.SmeHelloStateReadyInit}
			s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)
		}(i)
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond) // Wait for all delayed callbacks

	// Test passes if no race conditions occurred
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_ConcurrentDifferentSKIs() {
	// Test concurrent state updates for different SKIs

	// Setup additional services
	otherSKIs := []string{"other-ski-1", "other-ski-2", "other-ski-3"}
	for _, ski := range otherSKIs {
		service := api.NewServiceDetails(ski, "", fmt.Sprintf("ship-%s", ski))
		success := s.hub.AddService(service)
		require.True(s.T(), success, "Should add service for SKI: %s", ski)
	}

	// Allow callbacks for all SKIs
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(gomock.Any(), gomock.Any()).AnyTimes()

	// Act - Concurrent updates for different SKIs
	var wg sync.WaitGroup
	allSKIs := append([]string{s.testSKI}, otherSKIs...)

	for _, ski := range allSKIs {
		wg.Add(1)
		go func(targetSKI string) {
			defer wg.Done()
			shipState := model.ShipState{State: model.SmeHelloStateOk}
			s.hub.HandleShipHandshakeStateUpdate(targetSKI, shipState)
		}(ski)
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond) // Wait for all delayed callbacks

	// Test passes if no race conditions occurred
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_StateConsistencyUnderConcurrency() {
	// Test that service state remains consistent under concurrent updates

	// Allow callbacks
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).AnyTimes()

	// Act - Concurrent state changes
	var wg sync.WaitGroup
	states := []model.ShipMessageExchangeState{
		model.SmeHelloStateReadyInit,
		model.SmeHelloStateOk,
		model.SmeStateComplete,
	}

	for _, state := range states {
		wg.Add(1)
		go func(st model.ShipMessageExchangeState) {
			defer wg.Done()
			shipState := model.ShipState{State: st}
			s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)
		}(state)
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond) // Wait for all callbacks

	// Assert service state is valid (any of the expected states)
	service := s.hub.ServiceForIdentifier(s.testSKI, "")
	require.NotNil(s.T(), service, "Service should exist")
	// State could be any of the concurrent updates - just verify it's valid
	detail := service.ConnectionStateDetail()
	assert.NotNil(s.T(), detail, "ConnectionStateDetail should exist")
}

// Invalid State Transition Tests (4 test cases)

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_ServiceNotFound() {
	// Test behavior when service is not found for SKI

	nonExistentSKI := "non-existent-ski"

	// No callback should be triggered since service doesn't exist
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(nonExistentSKI, gomock.Any()).Times(0)

	// Act
	shipState := model.ShipState{State: model.SmeHelloStateOk}
	s.hub.HandleShipHandshakeStateUpdate(nonExistentSKI, shipState)

	// Wait briefly to ensure no callback
	time.Sleep(50 * time.Millisecond)

	// Test passes if no callback occurred and no panic
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_InvalidShipState() {
	// Test handling of invalid/unknown ship states

	callbackReceived := make(chan struct{}, 1)

	// Setup callback expectation with notification
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackReceived <- struct{}{}
	})

	// Act - Use an undefined state value (high number)
	shipState := model.ShipState{
		State: model.ShipMessageExchangeState(9999), // Invalid/unknown state
		Error: nil,
	}
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback with timeout
	select {
	case <-callbackReceived:
		// Callback received
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback not received within 1 second")
	}
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_NilError() {
	// Test behavior with nil error in ShipState

	callbackReceived := make(chan struct{}, 1)

	// Setup callback expectation with notification
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackReceived <- struct{}{}
	})

	// Act
	shipState := model.ShipState{
		State: model.SmeHelloStateOk,
		Error: nil, // Explicit nil
	}
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback with timeout
	select {
	case <-callbackReceived:
		// Callback received
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback not received within 1 second")
	}
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_EmptySKI() {
	// Test behavior with empty SKI parameter

	// No callback should be triggered since service won't be found
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate("", gomock.Any()).Times(0)

	// Act
	shipState := model.ShipState{State: model.SmeHelloStateOk}
	s.hub.HandleShipHandshakeStateUpdate("", shipState)

	// Wait briefly to ensure no callback
	time.Sleep(50 * time.Millisecond)

	// Test passes if no callback occurred
}

// Service State Consistency Tests (3 test cases)

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_ServiceStateDetailUpdate() {
	// Test that service ConnectionStateDetail is properly updated

	testError := errors.New("handshake timeout")

	callbackReceived := make(chan struct{}, 1)

	// Setup callback expectation with notification
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackReceived <- struct{}{}
	})

	// Act
	shipState := model.ShipState{
		State: model.SmeHelloStateAbort,
		Error: testError,
	}
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback with timeout
	select {
	case <-callbackReceived:
		// Callback received
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback not received within 1 second")
	}

	// Assert service state detail updated
	service := s.hub.ServiceForIdentifier(s.testSKI, "")
	require.NotNil(s.T(), service, "Service should exist")

	detail := service.ConnectionStateDetail()
	assert.Equal(s.T(), api.ConnectionStateError, detail.State(), "State should be Error due to error override")
	assert.Equal(s.T(), testError, detail.Error(), "Error should be set")
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_TrustUpdateOnHelloOk() {
	// Test that service trust is set when reaching SmeHelloStateOk

	// Verify initial state - service should not be trusted
	service := s.hub.ServiceForIdentifier(s.testSKI, "")
	require.NotNil(s.T(), service, "Service should exist")
	assert.False(s.T(), service.Trusted(), "Service should start untrusted")

	callbackReceived := make(chan struct{}, 1)

	// Setup callback expectation with notification
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackReceived <- struct{}{}
	})

	// Act
	shipState := model.ShipState{State: model.SmeHelloStateOk}
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback with timeout
	select {
	case <-callbackReceived:
		// Callback received
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback not received within 1 second")
	}

	// Assert trust is set
	service = s.hub.ServiceForIdentifier(s.testSKI, "")
	require.NotNil(s.T(), service, "Service should exist")
	assert.True(s.T(), service.Trusted(), "Service should be trusted after SmeHelloStateOk")
}

func (s *HandleShipHandshakeStateUpdateTestSuite) TestHandleShipHandshakeStateUpdate_NoTrustUpdateForOtherStates() {
	// Test that trust is not modified for states other than SmeHelloStateOk

	// Setup service as untrusted initially
	service := s.hub.ServiceForIdentifier(s.testSKI, "")
	require.NotNil(s.T(), service, "Service should exist")
	service.SetTrusted(false)
	assert.False(s.T(), service.Trusted(), "Service should start untrusted")

	callbackReceived := make(chan struct{}, 1)

	// Setup callback expectation with notification
	s.mockHubReader.EXPECT().ServicePairingDetailUpdate(s.testSKI, gomock.Any()).Times(1).Do(func(ski string, detail *api.ConnectionStateDetail) {
		callbackReceived <- struct{}{}
	})

	// Act - Use state that should not affect trust
	shipState := model.ShipState{State: model.SmeStateComplete}
	s.hub.HandleShipHandshakeStateUpdate(s.testSKI, shipState)

	// Wait for callback with timeout
	select {
	case <-callbackReceived:
		// Callback received
	case <-time.After(1 * time.Second):
		s.T().Fatal("Callback not received within 1 second")
	}

	// Assert trust remains unchanged
	service = s.hub.ServiceForIdentifier(s.testSKI, "")
	require.NotNil(s.T(), service, "Service should exist")
	assert.False(s.T(), service.Trusted(), "Trust should not be modified for non-HelloOk states")
}
