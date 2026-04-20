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
	s.mdnsSearch.On("ReportMdnsEntries", mock.Anything, mock.Anything).Maybe().Return()

	s.mdnsProvider = mocks.NewMdnsProviderInterface(s.T())
	s.mdnsProvider.On("ResolveEntries", mock.Anything, mock.Anything).Maybe().Return()
	s.mdnsProvider.On("Shutdown").Maybe().Return()
	s.mdnsProvider.On("Announce", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
	s.mdnsProvider.On("Unannounce").Maybe().Return()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	s.sut.SetTestProvider(s.mdnsProvider)
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
		4729, nil, MdnsProviderSelectionAvahiOnly)
	s.sut.SetTestProvider(s.mdnsProvider)

	err := s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Verify string truncation works
	assert.Equal(s.T(), "brandbrandbrandbrandbrandbrandbr", s.sut.deviceBrand)
	assert.Equal(s.T(), "modelmodelmodelmodelmodelmodelmo", s.sut.deviceModel)
}

func (s *MdnsSuite) Test_safeQRCodeKeyValue() {
	result := s.sut.safeQRCodeKeyValue("key", "value")
	assert.Equal(s.T(), "KEY:value;", result)

	result = s.sut.safeQRCodeKeyValue("KEY", "val;ue")
	assert.Equal(s.T(), "KEY:value;", result)

	result = s.sut.safeQRCodeKeyValue("key", "")
	assert.Equal(s.T(), "", result)
}

func (s *MdnsSuite) Test_deviceCategoriesString() {
	result := s.sut.deviceCategoriesString([]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem})
	assert.Equal(s.T(), "2", result)

	result = s.sut.deviceCategoriesString([]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem, api.DeviceCategoryTypeEnergyManagementSystem})
	assert.Equal(s.T(), "2,2", result)

	result = s.sut.deviceCategoriesString([]api.DeviceCategoryType{})
	assert.Equal(s.T(), "", result)
}

func (s *MdnsSuite) Test_QRCodeText() {
	result := s.sut.QRCodeText()
	assert.NotEqual(s.T(), "", result)
}

func (s *MdnsSuite) Test_AvahiOnly() {
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAvahiOnly)
	s.sut.SetTestProvider(s.mdnsProvider)

	err := s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)

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
	s.sut.SetTestProvider(s.mdnsProvider)

	err := s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)
	assert.False(s.T(), s.sut.autoaccept.Load())

	s.sut.SetAutoAccept(true)
	assert.True(s.T(), s.sut.autoaccept.Load())
}

func (s *MdnsSuite) Test_Start() {
	err := s.sut.Start(s.mdnsSearch)
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
	err = s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)
}

func (s *MdnsSuite) Test_Start_IFaces_Invalid() {
	s.sut.ifaces = []string{"noifacename"}
	err := s.sut.Start(s.mdnsSearch)
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
	err := s.sut.Start(s.mdnsSearch)
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
	// First cycle: Start and Shutdown
	err := s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), s.sut.mdnsProvider)

	s.sut.Shutdown()
	assert.Nil(s.T(), s.sut.mdnsProvider)

	// Second cycle: inject a fresh mock provider and Start again
	secondProvider := mocks.NewMdnsProviderInterface(s.T())
	secondProvider.On("Announce", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	// These MUST be called during the second Shutdown - this is the core assertion
	secondProvider.EXPECT().Unannounce().Once()
	secondProvider.EXPECT().Shutdown().Once()

	s.sut.SetTestProvider(secondProvider)

	err = s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), s.sut.mdnsProvider)

	// Second Shutdown must trigger cleanup on secondProvider
	s.sut.Shutdown()
	assert.Nil(s.T(), s.sut.mdnsProvider)
}

// Test_RestartAfterShutdown_Idempotent verifies that multiple Shutdown calls
// after a restart are still safe (only one cleanup per cycle).
func (s *MdnsSuite) Test_RestartAfterShutdown_Idempotent() {
	// First cycle
	err := s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)
	s.sut.Shutdown()

	// Second cycle with strict expectations
	secondProvider := mocks.NewMdnsProviderInterface(s.T())
	secondProvider.On("Announce", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	secondProvider.EXPECT().Unannounce().Once()
	secondProvider.EXPECT().Shutdown().Once()
	s.sut.SetTestProvider(secondProvider)

	err = s.sut.Start(s.mdnsSearch)
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

	err := s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)

	s.mdnsSearch.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe()

	s.sut.RequestMdnsEntries()

	time.Sleep(time.Millisecond * 500)
}

func (s *MdnsSuite) Test_ProcessMdnsEntry() {
	err := s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)

	s.mdnsSearch.EXPECT().ReportMdnsEntries(mock.Anything, mock.Anything).Maybe()

	elements := make(map[string]string, 1)

	name := "name"
	host := "host"
	ips := []net.IP{}
	port := 4567

	s.sut.processMdnsEntry(elements, name, host, ips, port, false)
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))

	elements["txtvers"] = "2"
	elements["id"] = "id"
	elements["path"] = "/ship"
	elements["ski"] = "testski"
	elements["register"] = "falsee"
	elements["cat"] = "text"

	s.sut.processMdnsEntry(elements, name, host, ips, port, false)
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))

	elements["txtvers"] = "1"
	s.sut.processMdnsEntry(elements, name, host, ips, port, false)
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))

	elements["ski"] = s.sut.ski
	s.sut.processMdnsEntry(elements, name, host, ips, port, false)
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))

	elements["ski"] = "testski"
	s.sut.processMdnsEntry(elements, name, host, ips, port, false)
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))

	elements["register"] = "false"
	s.sut.processMdnsEntry(elements, name, host, ips, port, false)
	assert.Equal(s.T(), 1, len(s.sut.mdnsEntries()))

	elements["brand"] = "brand"
	elements["type"] = "type"
	elements["model"] = "model"
	elements["serial"] = "serial"
	elements["cat"] = "2,3"
	s.sut.processMdnsEntry(elements, name, host, ips, port, false)
	assert.Equal(s.T(), 1, len(s.sut.mdnsEntries()))

	ips = []net.IP{[]byte("127.0.0.1"), []byte{0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}
	s.sut.processMdnsEntry(elements, name, host, ips, port, false)
	assert.Equal(s.T(), 1, len(s.sut.mdnsEntries()))

	s.sut.processMdnsEntry(elements, name, host, ips, port, false)
	assert.Equal(s.T(), 1, len(s.sut.mdnsEntries()))

	s.sut.processMdnsEntry(elements, name, host, ips, port, true)
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))
}

func (s *MdnsSuite) Test_SetTestProvider() {
	// Test that SetTestProvider allows injection of mock provider
	mockProvider := s.mdnsProvider
	mockProvider.On("Announce", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockProvider.On("Unannounce").Return()

	s.sut.SetTestProvider(mockProvider)

	err := s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Verify the injected provider is used
	assert.Equal(s.T(), mockProvider, s.sut.testProvider)
	assert.Equal(s.T(), mockProvider, s.sut.mdnsProvider)
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
	failingAvahi.On("Start", false, mock.Anything).Return(false)
	failingAvahi.On("Shutdown").Return()

	successfulZeroconf.On("Start", false, mock.Anything).Return(true)
	successfulZeroconf.On("Announce", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	successfulZeroconf.On("Unannounce").Return()
	successfulZeroconf.On("Shutdown").Return()

	// Set custom provider factory
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return failingAvahi },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return successfulZeroconf },
	}
	s.sut.SetProviderFactory(factory)

	// Start should succeed with fallback to Zeroconf
	err := s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Verify Zeroconf provider is used
	assert.Equal(s.T(), successfulZeroconf, s.sut.mdnsProvider)

	// Verify Avahi was attempted first and shutdown
	failingAvahi.AssertCalled(s.T(), "Start", false, mock.Anything)
	failingAvahi.AssertCalled(s.T(), "Shutdown")

	// Verify Zeroconf was used as fallback
	successfulZeroconf.AssertCalled(s.T(), "Start", false, mock.Anything)
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
	failingAvahi.On("Start", false, mock.Anything).Return(false)
	failingAvahi.On("Shutdown").Return()

	failingZeroconf.On("Start", false, mock.Anything).Return(false)
	failingZeroconf.On("Shutdown").Return()

	// Set custom provider factory
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return failingAvahi },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return failingZeroconf },
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail with appropriate error
	err := s.sut.Start(s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "no mDNS provider available - both Avahi and Zeroconf failed to initialize (interfaces: 0)", err.Error())

	// Verify both providers were attempted
	failingAvahi.AssertCalled(s.T(), "Start", false, mock.Anything)
	failingAvahi.AssertCalled(s.T(), "Shutdown")
	failingZeroconf.AssertCalled(s.T(), "Start", false, mock.Anything)
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
	successfulAvahi.On("Start", true, mock.Anything).Return(true)
	successfulAvahi.On("Announce", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	successfulAvahi.On("Unannounce").Return()
	successfulAvahi.On("Shutdown").Return()

	// Set custom provider factory
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return successfulAvahi },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return nil }, // Should not be called
	}
	s.sut.SetProviderFactory(factory)

	// Start should succeed
	err := s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Verify Avahi provider is used
	assert.Equal(s.T(), successfulAvahi, s.sut.mdnsProvider)
	successfulAvahi.AssertCalled(s.T(), "Start", true, mock.Anything)
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
	successfulZeroconf.On("Start", true, mock.Anything).Return(true)
	successfulZeroconf.On("Announce", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	successfulZeroconf.On("Unannounce").Return()
	successfulZeroconf.On("Shutdown").Return()

	// Set custom provider factory
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return nil }, // Should not be called
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return successfulZeroconf },
	}
	s.sut.SetProviderFactory(factory)

	// Start should succeed
	err := s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)

	// Verify Zeroconf provider is used
	assert.Equal(s.T(), successfulZeroconf, s.sut.mdnsProvider)
	successfulZeroconf.AssertCalled(s.T(), "Start", true, mock.Anything)
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
	err := s.sut.Start(s.mdnsSearch)
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
	successfulProvider.On("Start", false, mock.Anything).Return(true)
	successfulProvider.On("Announce", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("announcement failed"))
	successfulProvider.On("Shutdown").Return()

	// Set custom provider factory
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return successfulProvider },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return nil },
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail due to announcement failure
	err := s.sut.Start(s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "announcement failed", err.Error())

	// Verify provider was started but announcement failed
	successfulProvider.AssertCalled(s.T(), "Start", false, mock.Anything)
	successfulProvider.AssertCalled(s.T(), "Announce", mock.Anything, mock.Anything, mock.Anything)
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
	failingAvahi.On("Start", true, mock.Anything).Return(false)
	failingAvahi.On("Shutdown").Return()

	// Set custom provider factory
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return failingAvahi },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return nil },
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail because provider fails to start
	err := s.sut.Start(s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "avahi provider failed to start (interfaces: 1, autoReconnect: true)", err.Error())

	// Verify Avahi was attempted
	failingAvahi.AssertCalled(s.T(), "Start", true, mock.Anything)
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
	failingZeroconf.On("Start", true, mock.Anything).Return(false)
	failingZeroconf.On("Shutdown").Return()

	// Set custom provider factory
	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return nil },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return failingZeroconf },
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail because provider fails to start
	err := s.sut.Start(s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "zeroconf provider failed to start (interfaces: 0, autoReconnect: true)", err.Error())

	// Verify Zeroconf was attempted
	failingZeroconf.AssertCalled(s.T(), "Start", true, mock.Anything)
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
	err := s.sut.Start(s.mdnsSearch)
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
	err := s.sut.Start(s.mdnsSearch)
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
	err := s.sut.Start(s.mdnsSearch)
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
	err := s.sut.Start(s.mdnsSearch)
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

	failingAvahi.On("Start", false, mock.Anything).Return(false)
	failingAvahi.On("Shutdown").Return()

	failingZeroconf.On("Start", false, mock.Anything).Return(false)
	failingZeroconf.On("Shutdown").Return()

	factory := &ProviderFactory{
		NewAvahi:    func([]int32) api.MdnsProviderInterface { return failingAvahi },
		NewZeroconf: func([]net.Interface) api.MdnsProviderInterface { return failingZeroconf },
	}
	s.sut.SetProviderFactory(factory)

	// Start should fail with improved error message
	err := s.sut.Start(s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), "no mDNS provider available - both Avahi and Zeroconf failed to initialize (interfaces: 0)", err.Error())
}

func (s *MdnsSuite) Test_AnnounceMdnsEntry_ValidationErrors() {
	// Test validation errors in AnnounceMdnsEntry
	validProvider := mocks.NewMdnsProviderInterface(s.T())
	validProvider.On("Shutdown").Return()

	// Test empty identifier
	s.sut.Shutdown()
	s.sut = NewMDNS("", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	s.sut.mdnsProvider = validProvider // Set provider directly to bypass provider check

	err := s.sut.AnnounceMdnsEntry()
	assert.NotNil(s.T(), err)
	assert.Contains(s.T(), err.Error(), "service identifier is empty")

	// Test empty SKI
	s.sut = NewMDNS("", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	s.sut.mdnsProvider = validProvider

	err = s.sut.AnnounceMdnsEntry()
	assert.NotNil(s.T(), err)
	assert.Contains(s.T(), err.Error(), "SKI is empty")

	// Test empty service name
	s.sut = NewMDNS("testski", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "",
		4729, nil, MdnsProviderSelectionAll)
	s.sut.mdnsProvider = validProvider

	err = s.sut.AnnounceMdnsEntry()
	assert.NotNil(s.T(), err)
	assert.Contains(s.T(), err.Error(), "service name is empty")

	// Test invalid port
	s.sut = NewMDNS("testski", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		0, nil, MdnsProviderSelectionAll)
	s.sut.mdnsProvider = validProvider

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

func (s *MdnsSuite) Test_Shutdown_DefensiveProgramming() {
	// Test that shutdown is defensive against panics and completes gracefully
	validProvider := mocks.NewMdnsProviderInterface(s.T())
	validProvider.On("Unannounce").Maybe().Run(func(args mock.Arguments) {
		panic("test panic in unannounce")
	})
	validProvider.On("Shutdown").Maybe().Run(func(args mock.Arguments) {
		panic("test panic in shutdown")
	})

	manager := NewMDNS("testski", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	manager.SetTestProvider(validProvider)

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
	s.sut.SetTestProvider(s.mdnsProvider)

	// Start should succeed and announce on available interface
	err := s.sut.Start(s.mdnsSearch)
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
	s.sut.SetTestProvider(s.mdnsProvider)

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
	s.sut.SetTestProvider(s.mdnsProvider)

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
	s.sut.SetTestProvider(s.mdnsProvider)

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
	s.sut.SetTestProvider(s.mdnsProvider)

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
	s.sut.SetTestProvider(s.mdnsProvider)

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
	s.sut.SetTestProvider(s.mdnsProvider)

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
	s.sut.SetTestProvider(s.mdnsProvider)

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
	err := s.sut.Start(s.mdnsSearch)
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
	s.sut.SetTestProvider(s.mdnsProvider)
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
	s.sut.SetTestProvider(s.mdnsProvider)

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
	s.sut.mdnsProvider = s.sut.testProvider

	// nothing has been announced yet
	s.sut.isAnnounced = false

	s.sut.SetAutoAccept(true)
	assert.True(s.T(), s.sut.autoaccept.Load())

	s.sut.SetAutoAccept(false)
	assert.False(s.T(), s.sut.autoaccept.Load())

	assert.False(s.T(), s.sut.isAnnounced)

	// no unnancounce and no announce expected
	s.mdnsProvider.AssertNotCalled(s.T(), "Unannounce")
	s.mdnsProvider.AssertNotCalled(s.T(), "Announce", mock.Anything, mock.Anything, mock.Anything)
}

func (s *MdnsSuite) Test_AutoAcceptMdnsProviderNil() {
	s.sut.mdnsProvider = nil

	// something has already been announced
	s.sut.isAnnounced = true

	s.sut.SetAutoAccept(true)
	assert.True(s.T(), s.sut.autoaccept.Load())

	s.sut.SetAutoAccept(false)
	assert.False(s.T(), s.sut.autoaccept.Load())

	assert.True(s.T(), s.sut.isAnnounced)

	// no unnancounce and no announce expected
	s.mdnsProvider.AssertNotCalled(s.T(), "Unannounce")
	s.mdnsProvider.AssertNotCalled(s.T(), "Announce", mock.Anything, mock.Anything, mock.Anything)
}

func (s *MdnsSuite) Test_AutoAcceptServiceAlreadyAnnounced() {
	mdnsProvider := mocks.NewMdnsProviderInterface(s.T())
	s.sut.mdnsProvider = mdnsProvider

	// something has already been announced
	s.sut.isAnnounced = true

	mdnsProvider.EXPECT().Unannounce()
	mdnsProvider.EXPECT().Announce(mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.sut.SetAutoAccept(true)
	assert.True(s.T(), s.sut.autoaccept.Load())

	mdnsProvider.EXPECT().Unannounce()
	mdnsProvider.EXPECT().Announce(mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.sut.SetAutoAccept(false)
	assert.False(s.T(), s.sut.autoaccept.Load())

	assert.True(s.T(), s.sut.isAnnounced)
	mdnsProvider.EXPECT().Shutdown()
}

func (s *MdnsSuite) Test_AutoAcceptMdnsProviderReannounceFails() {
	mdnsProvider := mocks.NewMdnsProviderInterface(s.T())
	s.sut.mdnsProvider = mdnsProvider

	// something has already been announced
	s.sut.isAnnounced = true

	mdnsProvider.EXPECT().Announce(mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("myError"))
	s.sut.SetAutoAccept(true)
	assert.True(s.T(), s.sut.autoaccept.Load())
	assert.False(s.T(), s.sut.isAnnounced)

	mdnsProvider.EXPECT().Shutdown()
}
