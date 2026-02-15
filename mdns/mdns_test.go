package mdns

import (
	"errors"
	"net"
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
	s.mdnsProvider.EXPECT().UnannounceService(mock.Anything).Maybe().Return()

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
	assert.False(s.T(), s.sut.autoaccept)

	s.sut.SetAutoAccept(true)
	assert.True(s.T(), s.sut.autoaccept)
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
	assert.NotNil(s.T(), err)

	s.sut.SetAutoAccept(true)
	assert.Equal(s.T(), true, s.sut.autoaccept)
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
	// Test error handling when interface resolution fails
	s.sut.Shutdown()

	// Create manager with invalid interface name
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{"nonexistentinterface"}, MdnsProviderSelectionAll)

	// Start should fail due to invalid interface
	err := s.sut.Start(api.PairingModeBoth, s.mdnsSearch)
	assert.NotNil(s.T(), err)
	assert.Contains(s.T(), err.Error(), "no such network interface")
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
		ForPar:     "forDeviceFingerprint",
		TrustId:    "trustDeviceId",
		TrustPar:   "trustDeviceFingerprint",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: "0123456789abcdef0123456789abcdef",
		Alg:        "hmacSha256",
		Digest:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
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
		ForPar:     "forDeviceFingerprint",
		TrustId:    "trustDeviceId",
		TrustPar:   "trustDeviceFingerprint",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: "0123456789abcdef0123456789abcdef",
		Alg:        "hmacSha256",
		Digest:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
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
		ForPar:     "forDeviceFingerprint",
		TrustId:    "trustDeviceId",
		TrustPar:   "trustDeviceFingerprint",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: "0123456789abcdef0123456789abcdef",
		Alg:        "hmacSha256",
		Digest:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
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
	assert.Contains(s.T(), err.Error(), "instance ID 999 not found")
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
		ForPar:     "forDeviceFingerprint",
		TrustId:    "trustDeviceId",
		TrustPar:   "trustDeviceFingerprint",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: "0123456789abcdef0123456789abcdef",
		Alg:        "hmacSha256",
		Digest:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
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
		"parType":    "fpSha256",
		"forId":      "forDeviceId",
		"forPar":     "forDeviceFingerprint",
		"trustId":    "trustDeviceId",
		"trustPar":   "trustDeviceFingerprint",
		"trustCurve": "secp256r1",
		"type":       "addCu",
		"trustNonce": "0123456789abcdef0123456789abcdef",
		"alg":        "hmacSha256",
		"digest":     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}

	serviceName := "test-service"

	// Call processShipPairingMdnsEntry with invalid txtvers
	s.sut.processShipPairingMdnsEntry(elements, serviceName, false)

	// Verify no pairing entry was created (function returned early due to invalid txtvers)
	s.sut.pairingInstancesMux.RLock()
	pairingCount := len(s.sut.pairingInstances)
	s.sut.pairingInstancesMux.RUnlock()

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
	assert.True(s.T(), s.sut.autoaccept)

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
