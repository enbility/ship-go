package mdns

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

func TestMdnsSuite(t *testing.T) {
	suite.Run(t, new(MdnsSuite))
}

// findUsableInterfaceName returns the name of a network interface that is UP,
// not loopback, and has at least one address. Skips the test if none is found
// or if running on CI where interface names are not accessible.
func findUsableInterfaceName(t *testing.T) string {
	t.Helper()
	if util.IsRunningOnCI() {
		t.Skip("no access to interface names on CI")
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("cannot list interfaces: %v", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
			addrs, _ := iface.Addrs()
			if len(addrs) > 0 {
				return iface.Name
			}
		}
	}
	t.Skip("no usable network interface found on this system")
	return ""
}

type MdnsSuite struct {
	suite.Suite

	sut *MdnsManager

	mdnsService  *mocks.MdnsInterface
	mdnsSearch   *mocks.MdnsReportInterface
	mdnsProvider *mocks.MdnsProviderInterface
}

func (s *MdnsSuite) BeforeTest(suiteName, testName string) {
	s.mdnsService = mocks.NewMdnsInterface(s.T())

	s.mdnsSearch = mocks.NewMdnsReportInterface(s.T())
	s.mdnsSearch.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe().Return()

	s.mdnsProvider = mocks.NewMdnsProviderInterface(s.T())
	s.mdnsProvider.EXPECT().Start(mock.Anything, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true).Maybe()
	s.mdnsProvider.EXPECT().Shutdown().Maybe().Return()
	s.mdnsProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return("1", nil)
	s.mdnsProvider.EXPECT().UnannounceService(mock.Anything).Maybe().Return(nil)

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(s.mdnsProvider)
}

func (s *MdnsSuite) AfterTest(suiteName, testName string) {
	s.sut.Shutdown()
}

func (s *MdnsSuite) Test_LongStrings() {
	s.sut.Shutdown()

	s.sut = NewMDNS("test",
		"brandbrandbrandbrandbrandbrandbrand",
		"modelmodelmodelmodelmodelmodelmodel",
		"EnergyManagementSystemMoreLongerString",
		"1234567890123456789012345678901234567890",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionGoZeroConfOnly)
	s.sut.SetMdnsProvider(s.mdnsProvider)

	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Verify string truncation works
	assert.Equal(s.T(), "brandbrandbrandbrandbrandbrandbr", s.sut.deviceBrand)
	assert.Equal(s.T(), "modelmodelmodelmodelmodelmodelmo", s.sut.deviceModel)
}

func (s *MdnsSuite) Test_deviceCategoriesString() {
	result := s.sut.deviceCategoriesString([]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem})
	assert.Equal(s.T(), "2", result)

	result = s.sut.deviceCategoriesString([]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem, api.DeviceCategoryTypeEnergyManagementSystem})
	assert.Equal(s.T(), "2,2", result)

	result = s.sut.deviceCategoriesString([]api.DeviceCategoryType{})
	assert.Equal(s.T(), "", result)
}

func (s *MdnsSuite) Test_AvahiOnly() {
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAvahiOnly)

	// Avahi startup will not work on every platform, but we don't need it for this test
	_ = s.sut.Start(api.PairingModeBoth, s.mdnsSearch)

	// Verify the provider selection is correct
	assert.Equal(s.T(), MdnsProviderSelectionAvahiOnly, s.sut.providerSelection)
}

func (s *MdnsSuite) Test_GoZeroConfOnly() {
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionGoZeroConfOnly)

	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)
	assert.False(s.T(), s.sut.autoaccept.Load())

	s.sut.SetAutoAccept(true)
	assert.True(s.T(), s.sut.autoaccept.Load())
}

func (s *MdnsSuite) Test_Start() {
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	assert.Equal(s.T(), true, s.sut.isAnnounced)

	s.sut.UnannounceMdnsEntry()
	assert.Equal(s.T(), false, s.sut.isAnnounced)

	s.sut.UnannounceMdnsEntry()
	assert.Equal(s.T(), false, s.sut.isAnnounced)
}

func (s *MdnsSuite) Test_Start_IFaces() {
	// we don't have access to iface names on CI
	if util.IsRunningOnCI() {
		return
	}

	ifaces, err := net.Interfaces()
	assert.NotEqual(s.T(), 0, len(ifaces))
	assert.Nil(s.T(), err)

	s.sut.ifaces = []string{ifaces[0].Name}
	err = s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)
}

func (s *MdnsSuite) Test_Start_IFaces_Invalid() {
	s.sut.ifaces = []string{"noifacename"}
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	// Start should succeed even with invalid interfaces
	// but should NOT announce (no fallback to all interfaces)
	assert.Nil(s.T(), err)

	// Verify the interface was marked as missing
	assert.Contains(s.T(), s.sut.missingIfaces, "noifacename")

	// Verify the service is NOT announced
	assert.False(s.T(), s.sut.isAnnounced)

	s.sut.SetAutoAccept(true)
	assert.Equal(s.T(), true, s.sut.autoaccept.Load())

	s.sut.Shutdown()
}

func (s *MdnsSuite) Test_Shutdown_Start() {
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	s.sut.Shutdown()
	assert.Nil(s.T(), s.sut.mdnsProvider)

	s.sut.Shutdown()
}

func (s *MdnsSuite) Test_Shutdown_NoStart() {
	s.sut.Shutdown()
	assert.Nil(s.T(), s.sut.mdnsProvider)

	s.sut.Shutdown()
}

// Test_RestartAfterShutdown verifies that Start->Shutdown->Start->Shutdown works
// on the same MdnsManager instance. The second Shutdown must clean up the second
// provider. This is the core lifecycle bug: sync.Once prevents the second Shutdown
// from executing.
func (s *MdnsSuite) Test_RestartAfterShutdown() {
	// Use TestSetup mode to prevent creating real providers
	s.sut.providerSelection = MdnsProviderSelectionTestSetup
	// First cycle: Start and Shutdown
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), s.sut.mdnsProvider)

	s.sut.Shutdown()
	assert.Nil(s.T(), s.sut.mdnsProvider)

	// Second cycle: inject a fresh mock provider and Start again
	secondProvider := mocks.NewMdnsProviderInterface(s.T())
	secondProvider.On("AnnounceService", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", nil).Maybe()
	secondProvider.On("Start", mock.Anything, mock.Anything, mock.Anything).Return(true).Once()
	// These MUST be called during the second Shutdown - this is the core assertion
	secondProvider.EXPECT().UnannounceService(mock.Anything).Once()
	secondProvider.EXPECT().Shutdown().Once()

	s.sut.SetMdnsProvider(secondProvider)
	err = s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), s.sut.mdnsProvider)

	// Second Shutdown must trigger cleanup on secondProvider
	s.sut.Shutdown()
	assert.Nil(s.T(), s.sut.mdnsProvider)
}

// Test_RestartAfterShutdown_Idempotent verifies that multiple Shutdown calls
// after a restart are still safe (only one cleanup per cycle).
func (s *MdnsSuite) Test_RestartAfterShutdown_Idempotent() {
	// Use TestSetup mode to prevent creating real providers
	s.sut.providerSelection = MdnsProviderSelectionTestSetup
	// First cycle
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)
	s.sut.Shutdown()

	// Second cycle with strict expectations
	secondProvider := mocks.NewMdnsProviderInterface(s.T())
	secondProvider.On("AnnounceService", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", nil).Maybe()
	secondProvider.On("Start", mock.Anything, mock.Anything, mock.Anything).Return(true).Once()
	secondProvider.EXPECT().UnannounceService(mock.Anything).Once()
	secondProvider.EXPECT().Shutdown().Once()

	s.sut.SetMdnsProvider(secondProvider)
	err = s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Multiple Shutdown calls on the second cycle - only one should trigger cleanup
	s.sut.Shutdown()
	s.sut.Shutdown()
	s.sut.Shutdown()
}

func (s *MdnsSuite) Test_MdnsEntry() {
	testSki := "test"
	testService := "mdns_service"

	entries := s.sut.mdnsEntries()
	assert.Equal(s.T(), 0, len(entries))

	entry := &api.MdnsEntry{
		Ski: testSki,
	}

	s.sut.setMdnsEntry(testService, entry)
	entries = s.sut.mdnsEntries()
	assert.Equal(s.T(), 1, len(entries))

	theEntry, ok := s.sut.mdnsEntry(testService)
	assert.Equal(s.T(), true, ok)
	assert.NotNil(s.T(), theEntry)

	copyEntries := s.sut.copyMdnsEntries()
	assert.Equal(s.T(), 1, len(copyEntries))

	s.sut.removeMdnsEntry(testService)
	entries = s.sut.mdnsEntries()
	assert.Equal(s.T(), 0, len(entries))
	assert.Equal(s.T(), 1, len(copyEntries))
}

func (s *MdnsSuite) Test_MdnsEntries() {
	testSki := "test"
	testService := "mdns_service"

	entry := &api.MdnsEntry{
		Ski: testSki,
	}
	s.sut.setMdnsEntry(testService, entry)
	entries := s.sut.mdnsEntries()
	assert.Equal(s.T(), 1, len(entries))

	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	s.mdnsSearch.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe()

	s.sut.RequestMdnsEntries()

	time.Sleep(time.Millisecond * 500)
}

func (s *MdnsSuite) Test_ProcessMdnsEntry() {
	// Use TestSetup mode to prevent creating real providers
	s.sut.providerSelection = MdnsProviderSelectionTestSetup
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	s.mdnsSearch.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe()

	elements := make(map[string]string, 1)

	name := "name"
	host := "host"
	serviceType := shipZeroConfServiceType
	ips := []net.IP{}
	port := 4567

	s.sut.processMdnsEntry(elements, name, host, serviceType, ips, port, false)
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))

	elements["txtvers"] = "2"
	elements["id"] = "id"
	elements["path"] = "/ship"
	elements["ski"] = "testski"
	elements["register"] = "falsee"
	elements["cat"] = "text"

	s.sut.processMdnsEntry(elements, name, host, serviceType, ips, port, false)
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))

	elements["txtvers"] = "1"
	s.sut.processMdnsEntry(elements, name, host, serviceType, ips, port, false)
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))

	elements["ski"] = s.sut.ski
	s.sut.processMdnsEntry(elements, name, host, serviceType, ips, port, false)
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))

	elements["ski"] = "testski"
	s.sut.processMdnsEntry(elements, name, host, serviceType, ips, port, false)
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))

	elements["register"] = "false"
	s.sut.processMdnsEntry(elements, name, host, serviceType, ips, port, false)
	assert.Equal(s.T(), 1, len(s.sut.mdnsEntries()))

	elements["brand"] = "brand"
	elements["type"] = "type"
	elements["model"] = "model"
	elements["serial"] = "serial"
	elements["cat"] = "2,3"
	s.sut.processMdnsEntry(elements, name, host, serviceType, ips, port, false)
	assert.Equal(s.T(), 1, len(s.sut.mdnsEntries()))

	ips = []net.IP{[]byte("127.0.0.1"), []byte{0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}
	s.sut.processMdnsEntry(elements, name, host, serviceType, ips, port, false)
	assert.Equal(s.T(), 1, len(s.sut.mdnsEntries()))

	s.sut.processMdnsEntry(elements, name, host, serviceType, ips, port, false)
	assert.Equal(s.T(), 1, len(s.sut.mdnsEntries()))

	s.sut.processMdnsEntry(elements, name, host, serviceType, ips, port, true)
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))
}

func (s *MdnsSuite) Test_ProviderSelection() {
	// Test that provider selection configurations are preserved
	testCases := []struct {
		name      string
		selection MdnsProviderSelection
	}{
		{"All", MdnsProviderSelectionAll},
		{"AvahiOnly", MdnsProviderSelectionAvahiOnly},
		{"ZeroconfOnly", MdnsProviderSelectionGoZeroConfOnly},
		{"TestSetup", MdnsProviderSelectionTestSetup},
	}

	for _, tc := range testCases {
		s.T().Run(tc.name, func(t *testing.T) {
			manager := NewMDNS("test", "brand", "model", "type", "serial",
				[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
				"shipid", "serviceName", 4729, nil, tc.selection)
			assert.Equal(t, tc.selection, manager.providerSelection)
		})
	}
}

func (s *MdnsSuite) Test_ProviderFallback_AvahiToZeroconf() {
	// Test automatic fallback from Avahi to Zeroconf when Avahi fails
	s.sut.Shutdown()

	// Create manager with MdnsProviderSelectionAll
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)

	// Create mock providers
	failingAvahi := mocks.NewMdnsProviderInterface(s.T())
	successfulZeroconf := mocks.NewMdnsProviderInterface(s.T())

	// Setup mock expectations
	failingAvahi.EXPECT().Start(api.PairingModeBoth, false, mock.AnythingOfType("api.MdnsResolveCB")).Return(false)
	failingAvahi.EXPECT().Shutdown().Return()

	successfulZeroconf.EXPECT().Start(api.PairingModeBoth, false, mock.AnythingOfType("api.MdnsResolveCB")).Return(true)
	successfulZeroconf.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil)
	successfulZeroconf.EXPECT().UnannounceService(mock.Anything).Return(nil)
	successfulZeroconf.EXPECT().Shutdown().Return()

	// Set custom provider factory
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return failingAvahi },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return successfulZeroconf },
	}
	s.sut.SetProviderFactory(factory)

	// Start should succeed with fallback to Zeroconf
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Verify Zeroconf provider is used
	assert.Equal(s.T(), successfulZeroconf, s.sut.mdnsProvider)

	// Verify Avahi was attempted first and shutdown
	failingAvahi.AssertCalled(s.T(), "Start", api.PairingModeBoth, false, mock.Anything)
	failingAvahi.AssertCalled(s.T(), "Shutdown")

	// Verify Zeroconf was used as fallback
	successfulZeroconf.AssertCalled(s.T(), "Start", api.PairingModeBoth, false, mock.Anything)
}

func (s *MdnsSuite) Test_ProviderFallback_BothFail() {
	// Test error when both Avahi and Zeroconf fail
	s.sut.Shutdown()

	// Create manager with MdnsProviderSelectionAll
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)

	// Create failing mock providers
	failingAvahi := mocks.NewMdnsProviderInterface(s.T())
	failingZeroconf := mocks.NewMdnsProviderInterface(s.T())

	// Setup mock expectations
	failingAvahi.EXPECT().Start(api.PairingModeBoth, false, mock.AnythingOfType("api.MdnsResolveCB")).Return(false)
	failingAvahi.EXPECT().Shutdown().Return()

	failingZeroconf.EXPECT().Start(api.PairingModeBoth, false, mock.AnythingOfType("api.MdnsResolveCB")).Return(false)
	failingZeroconf.EXPECT().Shutdown().Return()

	// Set custom provider factory
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return failingAvahi },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return failingZeroconf },
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail with appropriate error
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "no mDNS provider available - both Avahi and Zeroconf failed to initialize (interfaces: 0)", err.Error())

	// Verify both providers were attempted
	failingAvahi.AssertCalled(s.T(), "Start", api.PairingModeBoth, false, mock.Anything)
	failingAvahi.AssertCalled(s.T(), "Shutdown")
	failingZeroconf.AssertCalled(s.T(), "Start", api.PairingModeBoth, false, mock.Anything)
}

func (s *MdnsSuite) Test_ProviderAvahiOnly_Success() {
	// Test AvahiOnly selection with successful provider
	s.sut.Shutdown()

	// Create manager with MdnsProviderSelectionAvahiOnly
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAvahiOnly)

	// Create successful mock provider
	successfulAvahi := mocks.NewMdnsProviderInterface(s.T())

	// Setup mock expectations
	successfulAvahi.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true)
	successfulAvahi.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil)
	successfulAvahi.EXPECT().UnannounceService(mock.Anything).Return(nil)
	successfulAvahi.EXPECT().Shutdown().Return()

	// Set custom provider factory
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return successfulAvahi },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return nil }, // Should not be called
	}
	s.sut.SetProviderFactory(factory)

	// Start should succeed
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Verify Avahi provider is used
	assert.Equal(s.T(), successfulAvahi, s.sut.mdnsProvider)
	successfulAvahi.AssertCalled(s.T(), "Start", api.PairingModeBoth, true, mock.Anything)
}

func (s *MdnsSuite) Test_ProviderZeroconfOnly_Success() {
	// Test ZeroconfOnly selection with successful provider
	s.sut.Shutdown()

	// Create manager with MdnsProviderSelectionGoZeroConfOnly
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionGoZeroConfOnly)

	// Create successful mock provider
	successfulZeroconf := mocks.NewMdnsProviderInterface(s.T())

	// Setup mock expectations
	successfulZeroconf.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(true)
	successfulZeroconf.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil)
	successfulZeroconf.EXPECT().UnannounceService(mock.Anything).Return(nil)
	successfulZeroconf.EXPECT().Shutdown().Return()

	// Set custom provider factory
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return nil }, // Should not be called
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return successfulZeroconf },
	}
	s.sut.SetProviderFactory(factory)

	// Start should succeed
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Verify Zeroconf provider is used
	assert.Equal(s.T(), successfulZeroconf, s.sut.mdnsProvider)
	successfulZeroconf.AssertCalled(s.T(), "Start", api.PairingModeBoth, true, mock.Anything)
}

func (s *MdnsSuite) Test_Start_InterfaceResolutionError() {
	// Test that Start succeeds gracefully even when interface resolution fails
	s.sut.Shutdown()

	// Create manager with invalid interface name
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{"nonexistentinterface"}, MdnsProviderSelectionAll)

	// Start should succeed but NOT announce (no fallback to all interfaces)
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Verify the nonexistent interface was tracked as missing
	assert.Contains(s.T(), s.sut.missingIfaces, "nonexistentinterface")

	// Verify the service is NOT announced
	assert.False(s.T(), s.sut.isAnnounced)

	// Verify the refresh goroutine was started (because we have missing interfaces)
	assert.NotNil(s.T(), s.sut.refreshTicker)
	assert.NotNil(s.T(), s.sut.refreshStopChan)

	s.sut.Shutdown()
}

func (s *MdnsSuite) Test_Start_AnnouncementFailure() {
	// Test error handling when announcement fails during startup
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)

	// Create provider that starts successfully but fails to announce
	successfulProvider := mocks.NewMdnsProviderInterface(s.T())
	successfulProvider.EXPECT().Start(api.PairingModeBoth, false, mock.AnythingOfType("api.MdnsResolveCB")).Return(true)
	successfulProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", errors.New("announcement failed"))
	successfulProvider.EXPECT().Shutdown().Return()

	// Set custom provider factory
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return successfulProvider },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return nil },
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail due to announcement failure
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "announcement failed", err.Error())

	// Verify provider was started but announcement failed
	successfulProvider.AssertCalled(s.T(), "Start", api.PairingModeBoth, false, mock.Anything)
	successfulProvider.AssertCalled(s.T(), "AnnounceService", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *MdnsSuite) Test_ProviderAvahiOnly_Failure() {
	// Test AvahiOnly selection when provider fails to start
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAvahiOnly)

	// Create failing provider
	failingAvahi := mocks.NewMdnsProviderInterface(s.T())
	failingAvahi.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(false)
	failingAvahi.EXPECT().Shutdown().Return()

	// Set custom provider factory
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return failingAvahi },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return nil },
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail because provider fails to start
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "avahi provider failed to start (interfaces: 1, autoReconnect: true)", err.Error())

	// Verify Avahi was attempted
	failingAvahi.AssertCalled(s.T(), "Start", api.PairingModeBoth, true, mock.Anything)
}

func (s *MdnsSuite) Test_ProviderZeroconfOnly_Failure() {
	// Test ZeroconfOnly selection when provider fails to start
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionGoZeroConfOnly)

	// Create failing provider
	failingZeroconf := mocks.NewMdnsProviderInterface(s.T())
	failingZeroconf.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(false)
	failingZeroconf.EXPECT().Shutdown().Return()

	// Set custom provider factory
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return nil },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return failingZeroconf },
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail because provider fails to start
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "zeroconf provider failed to start (interfaces: 0, autoReconnect: true)", err.Error())

	// Verify Zeroconf was attempted
	failingZeroconf.AssertCalled(s.T(), "Start", api.PairingModeBoth, true, mock.Anything)
}

func (s *MdnsSuite) Test_Start_NilProviderFactory() {
	// Test error handling when provider factory is nil
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)

	// Set factory to nil
	s.sut.SetProviderFactory(nil)

	// Start should fail with appropriate error
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "mDNS provider factory not initialized for provider selection 0", err.Error())
}

func (s *MdnsSuite) Test_Start_InvalidProviderSelection() {
	// Test error handling for invalid provider selection
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)

	// Set invalid provider selection
	s.sut.providerSelection = MdnsProviderSelection(999)

	// Start should fail with appropriate error
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Contains(s.T(), err.Error(), "invalid mDNS provider selection")
}

func (s *MdnsSuite) Test_Start_NilProviderCreation() {
	// Test error handling when provider factory returns nil
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAvahiOnly)

	// Set factory that returns nil
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return nil },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return nil },
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail with appropriate error
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "failed to create Avahi provider instance (interfaces: 1)", err.Error())
}

func (s *MdnsSuite) Test_Start_NilFactoryFunction() {
	// Test error handling when factory function is nil
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAvahiOnly)

	// Set factory with nil function
	factory := &ProviderFactory{
		NewAvahi:    nil,
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return nil },
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail with appropriate error
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "avahi provider factory function not available (interfaces: 1)", err.Error())
}

func (s *MdnsSuite) Test_ImprovedFallbackErrorMessages() {
	// Test that improved error messages are returned with fallback
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)

	// Create failing providers
	failingAvahi := mocks.NewMdnsProviderInterface(s.T())
	failingZeroconf := mocks.NewMdnsProviderInterface(s.T())

	failingAvahi.EXPECT().Start(api.PairingModeBoth, false, mock.AnythingOfType("api.MdnsResolveCB")).Return(false)
	failingAvahi.EXPECT().Shutdown().Return()

	failingZeroconf.EXPECT().Start(api.PairingModeBoth, false, mock.AnythingOfType("api.MdnsResolveCB")).Return(false)
	failingZeroconf.EXPECT().Shutdown().Return()

	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return failingAvahi },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return failingZeroconf },
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail with improved error message
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "no mDNS provider available - both Avahi and Zeroconf failed to initialize (interfaces: 0)", err.Error())
}

func (s *MdnsSuite) Test_AnnounceMdnsEntry_ValidationErrors() {
	// Test validation errors in AnnounceMdnsEntry
	validProvider := mocks.NewMdnsProviderInterface(s.T())
	validProvider.EXPECT().Shutdown().Return()

	// Test empty identifier
	s.sut.Shutdown()
	s.sut = NewMDNS("", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(validProvider) // Set provider directly to bypass provider check

	err := s.sut.AnnounceMdnsEntry()
	assert.NotNil(s.T(), err)
	assert.Contains(s.T(), err.Error(), "service identifier is empty")

	// Test empty SKI
	s.sut = NewMDNS("", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(validProvider)

	err = s.sut.AnnounceMdnsEntry()
	assert.NotNil(s.T(), err)
	assert.Contains(s.T(), err.Error(), "SKI is empty")

	// Test empty service name
	s.sut = NewMDNS("testski", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "",
		4729, nil, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(validProvider)

	err = s.sut.AnnounceMdnsEntry()
	assert.NotNil(s.T(), err)
	assert.Contains(s.T(), err.Error(), "service name is empty")

	// Test invalid port
	s.sut = NewMDNS("testski", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		0, nil, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(validProvider)

	err = s.sut.AnnounceMdnsEntry()
	assert.NotNil(s.T(), err)
	assert.Contains(s.T(), err.Error(), "invalid port")
}

func (s *MdnsSuite) Test_AnnounceMdnsEntry_NoProvider() {
	// Test announcement when no provider is available
	s.sut.Shutdown()

	s.sut = NewMDNS("testski", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	// Don't set any provider

	err := s.sut.AnnounceMdnsEntry()
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "cannot announce mDNS entry: no provider available (selection: 0)", err.Error())
}

func (s *MdnsSuite) Test_UnannounceMdnsEntry_InvalidInstanceID() {
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	assert.Equal(s.T(), true, s.sut.isAnnounced)
	assert.NotEmpty(s.T(), s.sut.instanceID)

	instanceId := s.sut.instanceID
	s.sut.instanceID = "nonexistent-instanceid"

	s.sut.UnannounceMdnsEntry()
	assert.Equal(s.T(), true, s.sut.isAnnounced)

	s.sut.instanceID = instanceId

	s.sut.UnannounceMdnsEntry()
	assert.Equal(s.T(), false, s.sut.isAnnounced)
}

func (s *MdnsSuite) Test_Shutdown_DefensiveProgramming() {
	// Test that shutdown is defensive against panics and completes gracefully
	validProvider := mocks.NewMdnsProviderInterface(s.T())
	validProvider.EXPECT().UnannounceService(mock.Anything).RunAndReturn(func(serviceType string) error {
		panic("test panic in unannounce")
	}).Maybe()
	validProvider.EXPECT().Shutdown().RunAndReturn(func() {
		panic("test panic in shutdown")
	}).Maybe()

	manager := NewMDNS("testski", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(s.mdnsProvider)

	// Set service as announced directly for testing
	manager.muxAnnounced.Lock()
	manager.isAnnounced = true
	manager.muxAnnounced.Unlock()

	// Shutdown should not panic even if provider methods panic
	assert.NotPanics(s.T(), func() {
		manager.Shutdown()
	})

	// Verify provider is cleaned up (defensive shutdown succeeded)
	assert.Nil(s.T(), manager.mdnsProvider)
}

func (s *MdnsSuite) Test_ProviderSelectionTestSetup_Success() {
	// Test TestSetup selection with pre-set provider
	s.sut.Shutdown()

	// Create manager with MdnsProviderSelectionTestSetup
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionTestSetup)

	// Create test provider
	testProvider := mocks.NewMdnsProviderInterface(s.T())
	testProvider.EXPECT().Start(api.PairingModeBoth, mock.Anything, mock.Anything).Return(true)
	testProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil)
	testProvider.EXPECT().UnannounceService(mock.Anything).Return(nil)
	testProvider.EXPECT().Shutdown().Return()

	// Set test provider before starting
	s.sut.SetMdnsProvider(testProvider)

	// Start should succeed and use the test provider
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Verify test provider is used
	assert.Equal(s.T(), testProvider, s.sut.mdnsProvider)

	// Verify provider factory is not used (no provider creation occurred)
	assert.NotNil(s.T(), s.sut.providerFactory) // Factory still exists but wasn't used
}

func (s *MdnsSuite) Test_getUsableInterface() {
	usableIfaceName := findUsableInterfaceName(s.T())

	// Test with valid interface
	iface, usable := getUsableInterface(usableIfaceName)
	assert.True(s.T(), usable)
	assert.NotNil(s.T(), iface)
	assert.Equal(s.T(), usableIfaceName, iface.Name)

	// Test with invalid interface name
	iface, usable = getUsableInterface("nonexistent_interface_12345")
	assert.False(s.T(), usable)
	assert.Nil(s.T(), iface)
}

func (s *MdnsSuite) Test_Start_PartialInterfaceAvailability() {
	usableIfaceName := findUsableInterfaceName(s.T())

	s.sut.Shutdown()

	// Mix valid and invalid interfaces
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{usableIfaceName, "nonexistent_iface"}, MdnsProviderSelectionAll)

	s.sut.SetMdnsProvider(s.mdnsProvider)

	// Start should succeed and announce on available interface
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.sut.isAnnounced) // Should announce on partial availability

	// Verify valid interface resolved
	assert.Equal(s.T(), 1, len(s.sut.currentIfaces))
	assert.Contains(s.T(), s.sut.currentIfaces, usableIfaceName)

	// Verify invalid interface tracked as missing
	assert.Contains(s.T(), s.sut.missingIfaces, "nonexistent_iface")

	// Verify refresh goroutine started (because one interface missing)
	assert.NotNil(s.T(), s.sut.refreshTicker)
	assert.NotNil(s.T(), s.sut.refreshStopChan)
}

func (s *MdnsSuite) Test_isInterfaceUsable() {
	// Test with interface that is DOWN
	ifaceDown := &net.Interface{
		Name:  "eth0",
		Flags: 0, // No flags set - interface is DOWN
	}
	assert.False(s.T(), isInterfaceUsable(ifaceDown))

	// Test with loopback interface (even if UP, loopback is not usable)
	ifaceLoopback := &net.Interface{
		Name:  "lo",
		Flags: net.FlagUp | net.FlagLoopback,
	}
	assert.False(s.T(), isInterfaceUsable(ifaceLoopback))

	// Note: We cannot fully test the "UP with addresses" success case with mock net.Interface
	// because net.Interface.Addrs() calls the system to get addresses, which requires
	// a real interface with actual network configuration.
	// The function Test_getUsableInterface already covers the full path
	// including address checking on real system interfaces.
}

func (s *MdnsSuite) Test_interfaces_ResetsPreviousState() {
	// Test that interfaces() resets trackers on each call to prevent duplicates
	// This addresses the bug where calling interfaces() multiple times would
	// append duplicates to currentIfaces instead of resetting it
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{"fake_iface_1", "fake_iface_2"}, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(s.mdnsProvider)

	// First call to interfaces() - should initialize trackers
	_, _, _ = s.sut.interfaces()

	// Both interfaces should be in missingIfaces (they don't exist)
	assert.Equal(s.T(), 2, len(s.sut.missingIfaces))
	assert.Contains(s.T(), s.sut.missingIfaces, "fake_iface_1")
	assert.Contains(s.T(), s.sut.missingIfaces, "fake_iface_2")
	assert.Equal(s.T(), 0, len(s.sut.currentIfaces))

	// Second call to interfaces() - should reset trackers, not append
	_, _, _ = s.sut.interfaces()

	// Verify no duplicates - still exactly 2 missing interfaces
	assert.Equal(s.T(), 2, len(s.sut.missingIfaces))
	assert.Equal(s.T(), 0, len(s.sut.currentIfaces))

	// Third call - same result, no accumulation
	_, _, _ = s.sut.interfaces()
	assert.Equal(s.T(), 2, len(s.sut.missingIfaces))
	assert.Equal(s.T(), 0, len(s.sut.currentIfaces))
}

func (s *MdnsSuite) Test_interfaces_ConcurrentWithAttemptResolveMapping() {
	// Test that interfaces() and attemptResolveMapping() can run concurrently
	// without data races on missingIfaces and currentIfaces.
	// This test validates the refreshMux protection added in interfaces().
	// It should be run with -race to detect unsynchronized access.
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{"fake_iface_1", "fake_iface_2"}, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(s.mdnsProvider)

	// Initialize tracking state so attemptResolveMapping has something to work with
	_, _, _ = s.sut.interfaces()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s.sut.interfaces()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s.sut.attemptResolveMapping()
		}
	}()

	wg.Wait()
}

func (s *MdnsSuite) Test_attemptResolveMapping_NoChanges() {
	// Test case: No changes in interface availability
	s.sut.Shutdown()

	// Create manager with specific interfaces that definitely don't exist
	// Use obviously fake names to ensure they won't exist on any system
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{"fake_iface_12345", "nonexistent_iface_67890"}, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(s.mdnsProvider)

	// Set initial state: both interfaces are "missing"
	s.sut.missingIfaces = map[string]struct{}{
		"fake_iface_12345":        {},
		"nonexistent_iface_67890": {},
	}
	s.sut.currentIfaces = []string{}

	// Call attemptResolveMapping - no interfaces should become available
	// (these fake interfaces don't exist on any system)
	s.sut.attemptResolveMapping()

	// Verify no changes: both still missing, no current interfaces
	assert.Contains(s.T(), s.sut.missingIfaces, "fake_iface_12345")
	assert.Contains(s.T(), s.sut.missingIfaces, "nonexistent_iface_67890")
	assert.Equal(s.T(), 0, len(s.sut.currentIfaces))

	// Since no changes occurred, reannounceWithNewInterfaces should NOT have been called
	// We verify this indirectly by checking the state hasn't changed
}

func (s *MdnsSuite) Test_attemptResolveMapping_InterfaceDisappears() {
	// Test case: Interface disappears (simulated by using non-existent interface)
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{"fake_iface"}, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(s.mdnsProvider)

	// Set initial state: interface is in "current" (was available)
	s.sut.currentIfaces = []string{"fake_iface"}
	s.sut.missingIfaces = map[string]struct{}{}

	// Call attemptResolveMapping - interface should now be detected as missing
	s.sut.attemptResolveMapping()

	// Verify interface moved from current to missing
	assert.Contains(s.T(), s.sut.missingIfaces, "fake_iface")
	assert.Equal(s.T(), 0, len(s.sut.currentIfaces))

	// The function calls reannounceWithNewInterfaces() internally
	// We verify state changes rather than mock expectations.
}

func (s *MdnsSuite) Test_attemptResolveMapping_InterfaceReappears() {
	usableIfaceName := findUsableInterfaceName(s.T())

	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{usableIfaceName}, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(s.mdnsProvider)

	// Set initial state: interface is missing (was previously unavailable)
	s.sut.missingIfaces = map[string]struct{}{usableIfaceName: {}}
	s.sut.currentIfaces = []string{}

	// Call attemptResolveMapping - interface should be detected as reappeared
	s.sut.attemptResolveMapping()

	// Verify interface moved from missing to current
	assert.NotContains(s.T(), s.sut.missingIfaces, usableIfaceName)
	assert.Contains(s.T(), s.sut.currentIfaces, usableIfaceName)
}

func (s *MdnsSuite) Test_reannounceWithNewInterfaces_PreservesTrackerState() {
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{"fake_iface_1", "fake_iface_2"}, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(s.mdnsProvider)

	// Set tracker state
	s.sut.missingIfaces = map[string]struct{}{"fake_iface_1": {}}
	s.sut.currentIfaces = []string{"fake_iface_2"}
	s.sut.isAnnounced = true

	// Call reannounceWithNewInterfaces
	s.sut.reannounceWithNewInterfaces()

	// Verify tracker state was NOT reset by the call
	assert.Contains(s.T(), s.sut.missingIfaces, "fake_iface_1")
}

func (s *MdnsSuite) Test_resolveInterfaces() {
	usableIfaceName := findUsableInterfaceName(s.T())

	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{usableIfaceName}, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(s.mdnsProvider)

	// Set initial tracker state
	s.sut.missingIfaces = map[string]struct{}{"some_missing": {}}
	s.sut.currentIfaces = []string{"some_current"}

	// Call resolveInterfaces
	resolvedIfaces, ifaceIndexes, resolveErr := s.sut.resolveInterfaces()
	assert.Nil(s.T(), resolveErr)
	assert.NotNil(s.T(), resolvedIfaces)
	assert.NotNil(s.T(), ifaceIndexes)
	assert.Equal(s.T(), 1, len(resolvedIfaces))
	assert.Equal(s.T(), usableIfaceName, resolvedIfaces[0].Name)

	// Verify tracker state was NOT modified
	assert.Contains(s.T(), s.sut.missingIfaces, "some_missing")
	assert.Equal(s.T(), []string{"some_current"}, s.sut.currentIfaces)
}

func (s *MdnsSuite) Test_updateProviderInterfaces() {
	// Test with nil provider
	s.sut.Shutdown()
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	s.sut.mdnsProvider = nil

	// Should not panic with nil provider
	s.sut.updateProviderInterfaces(nil, nil)

	// Test with AvahiProvider
	s.sut.Shutdown()
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAvahiOnly)

	avahiProvider := &AvahiProvider{}
	s.sut.mdnsProvider = avahiProvider

	testIndexes := []int32{1, 2, 3}
	s.sut.updateProviderInterfaces(nil, testIndexes)

	assert.Equal(s.T(), testIndexes, avahiProvider.getIfaceIndexes())

	// Test with ZeroconfProvider
	s.sut.Shutdown()
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionGoZeroConfOnly)

	zeroconfProvider := &ZeroconfProvider{}
	s.sut.mdnsProvider = zeroconfProvider

	testIfaces := []net.Interface{
		{Name: "eth0", Index: 1},
		{Name: "eth1", Index: 2},
	}
	s.sut.updateProviderInterfaces(testIfaces, nil)

	assert.Equal(s.T(), testIfaces, zeroconfProvider.getIfaces())
}

func (s *MdnsSuite) Test_reannounceWithNewInterfaces_Reannouncement() {
	// Test case: Re-announcement (already announced)
	// This is tested via Start() which does initial announcement
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	initialAnnounced := s.sut.isAnnounced
	assert.True(s.T(), initialAnnounced, "Start should have announced")

	// Call reannounceWithNewInterfaces when already announced
	s.sut.reannounceWithNewInterfaces()

	// Verify it handled re-announcement path
	// The manager should remain in announced state
	assert.True(s.T(), s.sut.isAnnounced)
}

func (s *MdnsSuite) Test_reannounceWithNewInterfaces_NoInterfaces() {
	// Test case: No interfaces available during re-announcement
	s.sut.Shutdown()

	// Create with specific interfaces that don't exist
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{"nonexistent_iface"}, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(s.mdnsProvider)
	s.sut.mdnsProvider = s.mdnsProvider

	// Set state: was announced before, and all interfaces are missing
	s.sut.isAnnounced = true
	s.sut.missingIfaces = map[string]struct{}{
		"nonexistent_iface": {},
	}
	s.sut.currentIfaces = []string{} // No current interfaces

	// Call reannounceWithNewInterfaces - should handle no interfaces gracefully
	s.sut.reannounceWithNewInterfaces()

	// When no interfaces are available and we were previously announced:
	// 1. resolveInterfaces() returns ErrNoInterfacesAvailable
	// 2. UnannounceMdnsEntry() is called to clean up since we have no usable interfaces
	// 3. isAnnounced is set to false
	assert.False(s.T(), s.sut.isAnnounced)
}

func (s *MdnsSuite) Test_refreshLoop_StopSignal() {
	// Test case: Stop signal triggers clean exit
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{"fake_test_iface"}, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(s.mdnsProvider)

	// Create channels for the refresh loop
	stopChan := make(chan struct{})
	ticker := time.NewTicker(1 * time.Hour) // Long interval so it doesn't fire
	tickChan := ticker.C

	// Track if goroutine exits
	doneChan := make(chan struct{})
	done := make(chan bool)
	go func() {
		s.sut.refreshLoop(stopChan, tickChan, doneChan)
		done <- true
	}()

	// Send stop signal immediately
	close(stopChan)

	// Wait for goroutine to exit (with timeout)
	select {
	case <-done:
		// Success - goroutine exited cleanly
	case <-time.After(1 * time.Second):
		s.T().Fatal("refreshLoop did not exit after stop signal")
	}

	// Verify cleanup - ticker should have been stopped via defer
	// We can't directly check if ticker.Stop() was called, but we can verify
	// the goroutine exited without panic (defer cleanup executed successfully)
}

func (s *MdnsSuite) Test_AutoAcceptServiceUnannouncedYet() {
	s.sut.mdnsProvider = s.mdnsProvider

	// Create manager with MdnsProviderSelectionTestSetup
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionTestSetup)

	// Create test provider
	testProvider := mocks.NewMdnsProviderInterface(s.T())
	testProvider.EXPECT().Start(api.PairingModeBoth, mock.Anything, mock.Anything).Return(true)
	testProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil)
	testProvider.EXPECT().UnannounceService(mock.Anything).Return(nil)
	testProvider.EXPECT().Shutdown().Return()

	// Set test provider before starting
	s.sut.SetMdnsProvider(testProvider)

	// Start should succeed and use the test provider
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Verify test provider is used
	assert.Equal(s.T(), testProvider, s.sut.mdnsProvider)

	// Verify provider factory is not used (no provider creation occurred)
	assert.NotNil(s.T(), s.sut.providerFactory) // Factory still exists but wasn't used
}

func (s *MdnsSuite) Test_ProviderSelectionTestSetup_NoProviderSet() {
	// Test TestSetup selection when no test provider is set
	s.sut.Shutdown()

	// Create manager with MdnsProviderSelectionTestSetup
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionTestSetup)

	// Don't set test provider - this should cause an error

	// Start should fail with appropriate error
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "test provider must be set before starting with MdnsProviderSelectionTestSetup", err.Error())

	// Verify no provider was set
	assert.Nil(s.T(), s.sut.mdnsProvider)
}

func (s *MdnsSuite) Test_ProviderSelectionTestSetup_SkipsFactoryValidation() {
	// Test that TestSetup selection bypasses provider factory validation
	s.sut.Shutdown()

	// Create manager with MdnsProviderSelectionTestSetup
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionTestSetup)

	// Set factory to nil (would normally cause error for other selections)
	s.sut.SetProviderFactory(nil)

	// Create test provider
	testProvider := mocks.NewMdnsProviderInterface(s.T())
	testProvider.EXPECT().Start(api.PairingModeBoth, mock.Anything, mock.Anything).Return(true)
	testProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil)
	testProvider.EXPECT().UnannounceService(mock.Anything).Return(nil)
	testProvider.EXPECT().Shutdown().Return()

	// Set test provider
	s.sut.SetMdnsProvider(testProvider)

	// Start should succeed despite nil factory (factory check is bypassed)
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Verify test provider is used
	assert.Equal(s.T(), testProvider, s.sut.mdnsProvider)
}

// Tests for SimulatePairingDiscovery - Critical gap: 0.0% coverage

func (s *MdnsSuite) Test_SimulatePairingDiscovery_NilTxtRecord() {
	// Test with nil txtRecord - should return early without processing
	s.sut.SimulatePairingDiscovery(nil)
	// No assertions needed - function should return early and not crash
}

func (s *MdnsSuite) Test_SimulatePairingDiscovery_ValidTxtRecord() {
	// Test with valid txtRecord - should call processShipPairingMdnsEntry
	validTxtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "forDeviceId",
		ForPar:     "1FB322A400E64DC850E5BB0D8B2205B8A7E67FCB7F30DBAFFB34F4EFF56A1573",
		TrustId:    "trustDeviceId",
		TrustPar:   "1DAA60D94A616837F827571AD3B167976FCC090373B8CF6D8CDF380AB367B1C1",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: "3EB1BD439947EB762998E566CCC2E099",
		Alg:        "hmacSha256",
		Digest:     "A8AE6E6EE929ABEA3AFCFC5258C8CCD6F85273E0D4626D26C7279F3250F77C8E",
	}

	// This should call processShipPairingMdnsEntry with the created elements map
	s.sut.SimulatePairingDiscovery(validTxtRecord)
	// Function should complete without errors
}

// Tests for AnnouncePairingService error paths - Critical gap: 59.3% coverage missing error paths

func (s *MdnsSuite) Test_AnnouncePairingService_NilProvider() {
	// Test when provider is nil - should return error
	s.sut.Shutdown()

	// Create new manager without setting any provider
	testManager := NewMDNS("testski", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	// Don't set any provider

	validTxtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "forDeviceId",
		ForPar:     "1FB322A400E64DC850E5BB0D8B2205B8A7E67FCB7F30DBAFFB34F4EFF56A1573",
		TrustId:    "trustDeviceId",
		TrustPar:   "1DAA60D94A616837F827571AD3B167976FCC090373B8CF6D8CDF380AB367B1C1",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: "3EB1BD439947EB762998E566CCC2E099",
		Alg:        "hmacSha256",
		Digest:     "A8AE6E6EE929ABEA3AFCFC5258C8CCD6F85273E0D4626D26C7279F3250F77C8E",
	}

	instanceID, err := testManager.AnnouncePairingService(validTxtRecord)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "", instanceID)
	assert.Equal(s.T(), "cannot announce pairing service: no provider available", err.Error())
}

func (s *MdnsSuite) Test_AnnouncePairingService_NilTxtRecord() {
	// Test when txtRecord is nil - should return error
	instanceID, err := s.sut.AnnouncePairingService(nil)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "", instanceID)
	assert.Equal(s.T(), "txtRecord cannot be nil", err.Error())
}

func (s *MdnsSuite) Test_AnnouncePairingService_InvalidTxtRecord() {
	// Test when txtRecord validation fails - should return error
	invalidTxtRecord := &api.ShipPairingTXT{
		TxtVers: "999", // Invalid version
		// Missing required fields to trigger validation error
	}

	instanceID, err := s.sut.AnnouncePairingService(invalidTxtRecord)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "", instanceID)
	assert.Contains(s.T(), err.Error(), "invalid TXT record")
}

func (s *MdnsSuite) Test_AnnouncePairingService_ProviderAnnounceFailure() {
	// Test when provider.AnnounceService fails - should clean up and return error
	failingProvider := mocks.NewMdnsProviderInterface(s.T())
	failingProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", errors.New("provider announcement failed"))
	failingProvider.EXPECT().Shutdown().Return()

	s.sut.SetMdnsProvider(failingProvider)

	validTxtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "forDeviceId",
		ForPar:     "1FB322A400E64DC850E5BB0D8B2205B8A7E67FCB7F30DBAFFB34F4EFF56A1573",
		TrustId:    "trustDeviceId",
		TrustPar:   "1DAA60D94A616837F827571AD3B167976FCC090373B8CF6D8CDF380AB367B1C1",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: "3EB1BD439947EB762998E566CCC2E099",
		Alg:        "hmacSha256",
		Digest:     "A8AE6E6EE929ABEA3AFCFC5258C8CCD6F85273E0D4626D26C7279F3250F77C8E",
	}

	instanceID, err := s.sut.AnnouncePairingService(validTxtRecord)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "", instanceID)
	assert.Equal(s.T(), "failed to announce pairing service: provider announcement failed", err.Error())

	// Verify provider was called
	failingProvider.AssertCalled(s.T(), "AnnounceService", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *MdnsSuite) Test_ProviderInitialization_NilFactoryFunction() {
	// Test error handling when zeroconf factory function is nil
	s.sut.Shutdown()
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionGoZeroConfOnly)

	// Set factory with nil NewZeroconf function
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return nil },
		NewZeroconf: nil, // This should trigger the error path
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail with appropriate error message
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Contains(s.T(), err.Error(), "zeroconf provider factory function not available")
}

func (s *MdnsSuite) Test_ProviderInitialization_ProviderCreationFailure() {
	// Test error handling when zeroconf factory returns nil provider
	s.sut.Shutdown()
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionGoZeroConfOnly)

	// Set factory that returns nil zeroconf provider
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return nil },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return nil }, // This should trigger the error path
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail with appropriate error message
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to create Zeroconf provider instance")
}

func (s *MdnsSuite) Test_ProviderInitialization_StartupFailureWithCleanup() {
	// Test error handling when zeroconf provider fails to start and cleanup is called
	s.sut.Shutdown()
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionGoZeroConfOnly)

	// Create mock provider that fails to start
	failingProvider := mocks.NewMdnsProviderInterface(s.T())
	failingProvider.EXPECT().Start(api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB")).Return(false)
	failingProvider.EXPECT().Shutdown().Return() // Should be called for cleanup

	// Set factory that returns failing provider
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return nil },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return failingProvider },
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail with appropriate error message
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Contains(s.T(), err.Error(), "zeroconf provider failed to start")

	// Verify that Start was called and Shutdown was called for cleanup
	failingProvider.AssertCalled(s.T(), "Start", api.PairingModeBoth, true, mock.AnythingOfType("api.MdnsResolveCB"))
	failingProvider.AssertCalled(s.T(), "Shutdown")
}

// Tests for UnannouncePairingService error paths - Critical gap: 76.2% coverage

func (s *MdnsSuite) Test_UnannouncePairingService_NilProvider() {
	// Test when provider is nil - should return api.ErrPairingNotActive
	s.sut.Shutdown()

	// Create new manager without setting any provider
	testManager := NewMDNS("testski", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	// Don't set any provider

	err := testManager.UnannouncePairingService("1")
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), api.ErrPairingNotActive, err)
}

// Test_UnannouncePairingService_InvalidInstanceID - REMOVED
// This test is no longer relevant since the implementation now uses provider instance IDs
// which can be any string (not just numeric), so there's no "invalid instance ID" validation.

func (s *MdnsSuite) Test_UnannouncePairingService_InstanceNotFound() {
	// Test when instanceID is not found - should return error
	validProvider := mocks.NewMdnsProviderInterface(s.T())
	validProvider.EXPECT().Shutdown().Return()

	s.sut.SetMdnsProvider(validProvider)

	// Try to unannounce a non-existent instance
	err := s.sut.UnannouncePairingService("999")
	assert.NotNil(s.T(), err)
	assert.Contains(s.T(), err.Error(), "999 not found")
}

func (s *MdnsSuite) Test_UnannouncePairingService_ProviderUnannounceFailure() {
	// Test when provider.UnannounceService fails - should return error
	failingProvider := mocks.NewMdnsProviderInterface(s.T())
	failingProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("1", nil)
	failingProvider.EXPECT().UnannounceService(mock.Anything).Return(errors.New("provider unannounce failed"))
	failingProvider.EXPECT().Shutdown().Return()

	s.sut.SetMdnsProvider(failingProvider)

	// First announce a service to set up a pairing instance
	validTxtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "forDeviceId",
		ForPar:     "1FB322A400E64DC850E5BB0D8B2205B8A7E67FCB7F30DBAFFB34F4EFF56A1573",
		TrustId:    "trustDeviceId",
		TrustPar:   "1DAA60D94A616837F827571AD3B167976FCC090373B8CF6D8CDF380AB367B1C1",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: "3EB1BD439947EB762998E566CCC2E099",
		Alg:        "hmacSha256",
		Digest:     "A8AE6E6EE929ABEA3AFCFC5258C8CCD6F85273E0D4626D26C7279F3250F77C8E",
	}
	instanceID, err := s.sut.AnnouncePairingService(validTxtRecord)
	assert.Nil(s.T(), err)
	assert.NotEmpty(s.T(), instanceID)

	// Now test unannounce failure
	err = s.sut.UnannouncePairingService(instanceID)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "provider unannounce failed", err.Error())

	// Verify provider was called
	failingProvider.AssertCalled(s.T(), "UnannounceService", mock.Anything)
}

// Tests for Start restart failure - Critical gap: 90.0% coverage missing restart failure

func (s *MdnsSuite) Test_Start_RestartAnnouncementFailure() {
	// Test when Start() is called multiple times and AnnounceMdnsEntry fails on restart

	// Start successfully first time
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.sut.isStarted)

	// Now create a failing provider for the second call
	failingProvider := mocks.NewMdnsProviderInterface(s.T())
	failingProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", errors.New("announcement failed on restart"))
	// Also expect UnannounceService and Shutdown since AfterTest will call Shutdown()
	failingProvider.EXPECT().UnannounceService(mock.Anything).Return(nil).Maybe()
	failingProvider.EXPECT().Shutdown().Return()

	// Replace the provider with failing one - this simulates provider failure after initial success
	s.sut.mdnsProvider = failingProvider

	// Reset announced state so the second Start() call will attempt to announce again
	s.sut.setIsServiceAnnounce(false)

	// Call Start again - this should trigger the restart path (isStarted=true) and fail on announcement
	err = s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "announcement failed on restart", err.Error())

	// Verify the failing provider was called for announcement
	failingProvider.AssertCalled(s.T(), "AnnounceService", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// Defensive Programming Tests - Medium Priority Coverage Gaps

func (s *MdnsSuite) Test_SetMdnsProvider_NilProvider() {
	// Test SetMdnsProvider with nil provider - should return early and log debug message
	// This tests the 50.0% coverage gap (lines 444-447)

	// Store original provider to restore later
	originalProvider := s.sut.mdnsProvider

	// Call SetMdnsProvider with nil - should return early
	s.sut.SetMdnsProvider(nil)

	// Verify provider was not changed (nil input was rejected)
	assert.Equal(s.T(), originalProvider, s.sut.mdnsProvider)
	assert.NotNil(s.T(), s.sut.mdnsProvider) // Should still have the original provider
}

func (s *MdnsSuite) Test_RequestMdnsEntries_NilReportInterface() {
	// Test RequestMdnsEntries when report interface is nil - should return early
	// This tests the 80.0% coverage gap (lines 864-866)

	// Clear the report interface to simulate nil condition
	s.sut.setReportInterface(nil)

	// Call RequestMdnsEntries - should return early without calling ReportMdnsEntries
	assert.NotPanics(s.T(), func() {
		s.sut.RequestMdnsEntries()
	})

	// Restore report interface for cleanup
	s.sut.setReportInterface(s.mdnsSearch)
}

func (s *MdnsSuite) Test_Shutdown_PanicRecovery_UnannounceMdnsEntry() {
	// Test panic recovery in UnannounceMdnsEntry during shutdown
	// This tests the 95.2% coverage gap (lines 295-298)

	// Create a provider that panics on UnannounceService
	panicProvider := mocks.NewMdnsProviderInterface(s.T())
	panicProvider.EXPECT().UnannounceService(mock.Anything).RunAndReturn(func(serviceType string) error {
		panic("test panic in UnannounceMdnsEntry")
	}).Maybe()
	panicProvider.EXPECT().Shutdown().Return().Maybe()

	// Create a new manager to avoid interfering with other tests
	testManager := NewMDNS("testski", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	testManager.SetMdnsProvider(panicProvider)

	// Set service as announced to trigger unannounce during shutdown
	testManager.muxAnnounced.Lock()
	testManager.isAnnounced = true
	testManager.muxAnnounced.Unlock()

	// Shutdown should not panic even if UnannounceMdnsEntry panics
	assert.NotPanics(s.T(), func() {
		testManager.Shutdown()
	})

	// Verify provider is cleaned up (defensive shutdown succeeded)
	assert.Nil(s.T(), testManager.mdnsProvider)
}

func (s *MdnsSuite) Test_Shutdown_PanicRecovery_ProviderShutdown() {
	// Test panic recovery in provider.Shutdown() during shutdown
	// This tests the 95.2% coverage gap (lines 306-309)

	// Create a provider that panics on Shutdown
	panicProvider := mocks.NewMdnsProviderInterface(s.T())
	panicProvider.EXPECT().UnannounceService(mock.Anything).Return(nil).Maybe()
	panicProvider.EXPECT().Shutdown().RunAndReturn(func() {
		panic("test panic in provider shutdown")
	}).Maybe()

	// Create a new manager to avoid interfering with other tests
	testManager := NewMDNS("testski", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	testManager.SetMdnsProvider(panicProvider)

	// Set service as announced to ensure full shutdown path is taken
	testManager.muxAnnounced.Lock()
	testManager.isAnnounced = true
	testManager.muxAnnounced.Unlock()

	// Shutdown should not panic even if provider.Shutdown() panics
	assert.NotPanics(s.T(), func() {
		testManager.Shutdown()
	})

	// Verify provider is cleaned up despite panic (defensive shutdown succeeded)
	assert.Nil(s.T(), testManager.mdnsProvider)
}

// Tests for missing edge cases in processShipPairingMdnsEntry

func (s *MdnsSuite) Test_ProcessShipPairingMdnsEntry_InvalidTxtvers() {
	// Test invalid txtvers validation - when txtvers != "1", should return early and log debug message
	// This tests the missing edge case in processShipPairingMdnsEntry (lines 614-617)

	// Create elements map with all mandatory fields but invalid txtvers
	elements := map[string]string{
		"txtvers":    "2", // Invalid version (not "1")
		"partype":    "fpSha256",
		"forid":      "forDeviceId",
		"forpar":     "1FB322A400E64DC850E5BB0D8B2205B8A7E67FCB7F30DBAFFB34F4EFF56A1573",
		"trustid":    "trustDeviceId",
		"trustpar":   "1DAA60D94A616837F827571AD3B167976FCC090373B8CF6D8CDF380AB367B1C1",
		"trustcurve": "secp256r1",
		"type":       "addCu",
		"trustnonce": "3EB1BD439947EB762998E566CCC2E099",
		"alg":        "hmacSha256",
		"digest":     "A8AE6E6EE929ABEA3AFCFC5258C8CCD6F85273E0D4626D26C7279F3250F77C8E",
	}

	serviceName := "test-service"

	// Call processShipPairingMdnsEntry with invalid txtvers
	s.sut.processShipPairingMdnsEntry(elements, serviceName, false)

	// Verify no pairing entry was created (function returned early due to invalid txtvers)
	s.sut.announcedPairingsMux.RLock()
	pairingCount := len(s.sut.announcedPairings)
	s.sut.announcedPairingsMux.RUnlock()

	assert.Equal(s.T(), 0, pairingCount, "No pairing entry should be created with invalid txtvers")
}

// Tests for missing edge cases in SetAutoAccept

func (s *MdnsSuite) Test_SetAutoAccept_ServiceNotAnnounced() {
	// Test when service is not announced - should return early without calling AnnounceMdnsEntry
	// This tests the missing edge case in SetAutoAccept (lines 431-433)

	// Ensure service is not announced by setting isAnnounced to false
	s.sut.muxAnnounced.Lock()
	s.sut.isAnnounced = false
	s.sut.muxAnnounced.Unlock()

	// Create a provider that should NOT be called for announcement
	strictProvider := mocks.NewMdnsProviderInterface(s.T())
	// Do NOT expect AnnounceService to be called - it should return early
	strictProvider.EXPECT().Shutdown().Return()

	s.sut.SetMdnsProvider(strictProvider)

	// Call SetAutoAccept - should return early without calling AnnounceMdnsEntry
	s.sut.SetAutoAccept(true)

	// Verify autoaccept was set
	assert.True(s.T(), s.sut.autoaccept.Load())

	// The test passes if no unexpected calls were made to the provider
	// (i.e., AnnounceService was not called because service is not announced)
}

func (s *MdnsSuite) Test_DeviceGetters() {
	// Test all device getter methods return the expected values from NewMDNS constructor
	assert.Equal(s.T(), "brand", s.sut.DeviceBrand())
	assert.Equal(s.T(), "model", s.sut.DeviceModel())
	assert.Equal(s.T(), "12345", s.sut.DeviceSerial())
	assert.Equal(s.T(), "EnergyManagementSystem", s.sut.DeviceType())
	assert.Equal(s.T(), []api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem}, s.sut.DeviceCategories())
}

// validPairingTXTRecord returns a minimal valid ShipPairingTXT suitable for use in tests.
// All four fields checked by ShipPairingTXT.Validate() are set to accepted values.
func validPairingTXTRecord() *api.ShipPairingTXT {
	return &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		ForId:      "for-device-id",
		ForPar:     "0000000000000000000000000000000000000000000000000000000000000000",
		TrustId:    "trust-device-id",
		TrustPar:   "1111111111111111111111111111111111111111111111111111111111111111",
		TrustCurve: api.CurveSecp256r1,
		Type:       api.CommandTypeAddCU,
		TrustNonce: "22222222222222222222222222222222",
		Alg:        api.AlgorithmHMACSHA256,
		Digest:     "3333333333333333333333333333333333333333333333333333333333333333",
	}
}

// Test_reannounceWithNewInterfaces_PairingReannouncement verifies that when interfaces
// change, reannounceWithNewInterfaces re-announces OUR announced pairing services
// (from pairingInstances) and does NOT touch discovered remote services (pairingEntries).
// Also verifies create-then-swap: new announcement is live before old is torn down.
func (s *MdnsSuite) Test_reannounceWithNewInterfaces_PairingReannouncement() {
	provider := mocks.NewMdnsProviderInterface(s.T())
	provider.EXPECT().Shutdown().Return().Maybe()

	// SHIP service re-announcement (reannounceWithNewInterfaces always calls AnnounceMdnsEntry)
	provider.EXPECT().
		AnnounceService(shipZeroConfServiceType, mock.Anything, mock.Anything, mock.Anything).
		Return("new-ship-id", nil).Once()

	// Pairing service re-announcement — one call expected for our one announced instance
	provider.EXPECT().
		AnnounceService(shipPairingZeroConfServiceType, mock.Anything, mock.Anything, mock.Anything).
		Return("new-pairing-id", nil).Once()

	// Old pairing instance must be torn down after the new one is live (create-then-swap)
	provider.EXPECT().
		UnannounceService("old-pairing-id").
		Return(nil).Once()

	// Shutdown (AfterTest) will call UnannounceMdnsEntry with the new ship instance ID
	provider.EXPECT().
		UnannounceService("new-ship-id").
		Return(nil).Maybe()

	s.sut.Shutdown()
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	s.sut.SetMdnsProvider(provider)

	// Simulate state that AnnouncePairingService would produce: one logical entry
	// with a stable service name and the current provider-side instance ID.
	txtRecord := validPairingTXTRecord()
	s.sut.announcedPairingsMux.Lock()
	s.sut.announcedPairings["logical-1"] = &announcedPairing{
		serviceName: "serviceName-pairing#1",
		txtRecord:   txtRecord,
		providerID:  "old-pairing-id",
	}
	s.sut.announcedPairingsMux.Unlock()

	// Pre-populate pairingEntries — these are DISCOVERED remote services; must not be re-announced
	s.sut.pairingEntries["discovered-remote"] = &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		Type:       api.CommandTypeAddCU,
		TrustCurve: api.CurveSecp256r1,
		Alg:        api.AlgorithmHMACSHA256,
		ForId:      "autofill-for-id",
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		TrustId:    "autofill-trust-id",
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustNonce: "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		Digest:     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}

	s.sut.reannounceWithNewInterfaces()

	// After re-announcement: the logical entry must still exist but the provider ID is updated.
	s.sut.announcedPairingsMux.RLock()
	entry, hasEntry := s.sut.announcedPairings["logical-1"]
	var updatedProviderID string
	if hasEntry {
		updatedProviderID = entry.providerID
	}
	count := len(s.sut.announcedPairings)
	s.sut.announcedPairingsMux.RUnlock()

	assert.True(s.T(), hasEntry, "logical entry should persist across re-announcement")
	assert.Equal(s.T(), "new-pairing-id", updatedProviderID, "provider ID should be updated to the new instance")
	assert.Equal(s.T(), 1, count, "announcedPairings should still have exactly one entry")

	// Discovered remote entries must be untouched
	assert.Contains(s.T(), s.sut.pairingEntries, "discovered-remote",
		"pairingEntries (discovered) must not be modified during re-announcement")
}

// Test_reannounceWithNewInterfaces_NoInterfaces_WithPairing verifies that when no interfaces
// are available and pairing services were announced, all pairing instances are unannounced
// using mutex-safe iteration (no data race on pairingInstances).
func (s *MdnsSuite) Test_reannounceWithNewInterfaces_NoInterfaces_WithPairing() {
	s.sut.Shutdown()
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{"nonexistent_iface"}, MdnsProviderSelectionAll)

	provider := mocks.NewMdnsProviderInterface(s.T())
	provider.EXPECT().Shutdown().Return().Maybe()
	// Both pairing instances must be unannounced (map iteration order is non-deterministic)
	provider.EXPECT().UnannounceService("p1").Return(nil).Once()
	provider.EXPECT().UnannounceService("p2").Return(nil).Once()
	// The SHIP instance must be released as well, otherwise its provider-side
	// object stays allocated with no interface left to serve it
	provider.EXPECT().UnannounceService("ship-1").Return(nil).Once()

	s.sut.SetMdnsProvider(provider)

	// Simulate state that AnnouncePairingService would produce: two logical entries
	// each with a stable service name and a current provider-side instance ID.
	txtRecord := validPairingTXTRecord()
	s.sut.announcedPairingsMux.Lock()
	s.sut.announcedPairings["1"] = &announcedPairing{serviceName: "serviceName-pairing#1", txtRecord: txtRecord, providerID: "p1"}
	s.sut.announcedPairings["2"] = &announcedPairing{serviceName: "serviceName-pairing#2", txtRecord: txtRecord, providerID: "p2"}
	s.sut.announcedPairingsMux.Unlock()

	// Mark SHIP service as announced so the wasAnnounced path is exercised.
	s.sut.instanceID = "ship-1"
	s.sut.muxAnnounced.Lock()
	s.sut.isAnnounced = true
	s.sut.muxAnnounced.Unlock()

	s.sut.reannounceWithNewInterfaces()

	// Logical entries must be preserved (caller-held IDs remain valid).
	// Provider IDs are cleared (marked as not currently live on the provider).
	s.sut.announcedPairingsMux.RLock()
	count := len(s.sut.announcedPairings)
	p1ProviderID := s.sut.announcedPairings["1"].providerID
	p2ProviderID := s.sut.announcedPairings["2"].providerID
	s.sut.announcedPairingsMux.RUnlock()

	assert.Equal(s.T(), 2, count, "logical pairing entries should be preserved when no interfaces available")
	assert.Equal(s.T(), "", p1ProviderID, "provider ID should be cleared when no interfaces available")
	assert.Equal(s.T(), "", p2ProviderID, "provider ID should be cleared when no interfaces available")

	// IsPairingServiceAnnounced remains true because the logical entries still exist
	// and will be re-announced when interfaces reappear.
	assert.True(s.T(), s.sut.IsPairingServiceAnnounced(),
		"IsPairingServiceAnnounced should remain true: logical entries are preserved for later re-announcement")

	assert.False(s.T(), s.sut.isServiceAnnounced(),
		"SHIP service should be marked unannounced once no interfaces are available")
}

// Test_reannounceWithNewInterfaces_ReleasesOldShipInstance verifies create-then-swap
// for the SHIP service: the previous provider instance is released only after the new
// one is live. Leaking it allocates a provider-side object (an avahi EntryGroup) per
// interface change, which eventually exhausts the daemon's per-client object limit.
func (s *MdnsSuite) Test_reannounceWithNewInterfaces_ReleasesOldShipInstance() {
	s.sut.Shutdown()
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)

	provider := mocks.NewMdnsProviderInterface(s.T())
	provider.EXPECT().Shutdown().Return().Maybe()
	provider.EXPECT().
		AnnounceService(shipZeroConfServiceType, mock.Anything, mock.Anything, mock.Anything).
		Return("new-ship-id", nil).Once()
	provider.EXPECT().UnannounceService("old-ship-id").Return(nil).Once()
	// AfterTest shutdown releases the new instance
	provider.EXPECT().UnannounceService("new-ship-id").Return(nil).Maybe()

	s.sut.SetMdnsProvider(provider)

	s.sut.instanceID = "old-ship-id"
	s.sut.muxAnnounced.Lock()
	s.sut.isAnnounced = true
	s.sut.muxAnnounced.Unlock()

	s.sut.reannounceWithNewInterfaces()

	assert.Equal(s.T(), "new-ship-id", s.sut.instanceID)
	assert.True(s.T(), s.sut.isServiceAnnounced())
}

// Test_reannounceWithNewInterfaces_KeepsStateOnAnnounceFailure verifies that a failed
// re-announcement leaves the still-live previous announcement reflected in the state,
// so the next AnnounceMdnsEntry does not allocate a second instance on top of it.
func (s *MdnsSuite) Test_reannounceWithNewInterfaces_KeepsStateOnAnnounceFailure() {
	s.sut.Shutdown()
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)

	provider := mocks.NewMdnsProviderInterface(s.T())
	provider.EXPECT().Shutdown().Return().Maybe()
	provider.EXPECT().
		AnnounceService(shipZeroConfServiceType, mock.Anything, mock.Anything, mock.Anything).
		Return("", errors.New("announce failed")).Once()
	provider.EXPECT().UnannounceService("old-ship-id").Return(nil).Maybe()

	s.sut.SetMdnsProvider(provider)

	s.sut.instanceID = "old-ship-id"
	s.sut.muxAnnounced.Lock()
	s.sut.isAnnounced = true
	s.sut.muxAnnounced.Unlock()

	s.sut.reannounceWithNewInterfaces()

	assert.Equal(s.T(), "old-ship-id", s.sut.instanceID, "previous instance must be retained")
	assert.True(s.T(), s.sut.isServiceAnnounced(), "state must still reflect the live announcement")
}

func (s *MdnsSuite) Test_AutoAcceptServiceAlreadyAnnounced() {
	mdnsProvider := mocks.NewMdnsProviderInterface(s.T())
	s.sut.mdnsProvider = mdnsProvider

	// something has already been announced
	s.sut.isAnnounced = true
	s.sut.instanceID = "old-id-1"

	// Create-then-swap: new AnnounceService is called first, then the old
	// instance is unannounced. No goodbye-gap visible to remote devices.
	mdnsProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("new-id-1", nil).Once()
	mdnsProvider.EXPECT().UnannounceService("old-id-1").Return(nil).Once()
	s.sut.SetAutoAccept(true)
	assert.True(s.T(), s.sut.autoaccept.Load())

	mdnsProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("new-id-2", nil).Once()
	mdnsProvider.EXPECT().UnannounceService("new-id-1").Return(nil).Once()
	s.sut.SetAutoAccept(false)
	assert.False(s.T(), s.sut.autoaccept.Load())

	assert.True(s.T(), s.sut.isAnnounced)

	// AfterTest → Shutdown → UnannounceMdnsEntry tears down the current instance
	mdnsProvider.EXPECT().UnannounceService("new-id-2").Return(nil).Once()
	mdnsProvider.EXPECT().Shutdown()
}

func (s *MdnsSuite) Test_AutoAcceptMdnsProviderReannounceFails() {
	mdnsProvider := mocks.NewMdnsProviderInterface(s.T())
	s.sut.mdnsProvider = mdnsProvider

	// something has already been announced
	s.sut.isAnnounced = true
	s.sut.instanceID = "old-id"

	// New announcement fails: isAnnounced must reflect that we no longer
	// have a known-good announcement so callers can retry.
	mdnsProvider.EXPECT().AnnounceService(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", fmt.Errorf("myError")).Once()
	s.sut.SetAutoAccept(true)
	assert.True(s.T(), s.sut.autoaccept.Load())
	assert.False(s.T(), s.sut.isAnnounced)

	mdnsProvider.EXPECT().Shutdown()
}
