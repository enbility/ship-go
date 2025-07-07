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
	assert.False(s.T(), s.sut.autoaccept)

	s.sut.SetAutoAccept(true)
	assert.True(s.T(), s.sut.autoaccept)
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
	assert.NotNil(s.T(), err)

	s.sut.SetAutoAccept(true)
	assert.Equal(s.T(), true, s.sut.autoaccept)
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

func (s *MdnsSuite) Test_MdnsEntry() {
	testSki := "test"

	entries := s.sut.mdnsEntries()
	assert.Equal(s.T(), 0, len(entries))

	entry := &api.MdnsEntry{
		Ski: testSki,
	}

	s.sut.setMdnsEntry(testSki, entry)
	entries = s.sut.mdnsEntries()
	assert.Equal(s.T(), 1, len(entries))

	theEntry, ok := s.sut.mdnsEntry(testSki)
	assert.Equal(s.T(), true, ok)
	assert.NotNil(s.T(), theEntry)

	copyEntries := s.sut.copyMdnsEntries()
	assert.Equal(s.T(), 1, len(copyEntries))

	s.sut.removeMdnsEntry(testSki)
	entries = s.sut.mdnsEntries()
	assert.Equal(s.T(), 0, len(entries))
	assert.Equal(s.T(), 1, len(copyEntries))
}

func (s *MdnsSuite) Test_MdnsEntries() {
	testSki := "test"

	entry := &api.MdnsEntry{
		Ski: testSki,
	}
	s.sut.setMdnsEntry(testSki, entry)
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
	assert.Equal(s.T(), "no mDNS provider available - both Avahi and Zeroconf failed to initialize", err.Error())
	
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
	// Test error handling when interface resolution fails
	s.sut.Shutdown()
	
	// Create manager with invalid interface name
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{"nonexistentinterface"}, MdnsProviderSelectionAll)
	
	// Start should fail due to invalid interface
	err := s.sut.Start(s.mdnsSearch)
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
	assert.Equal(s.T(), "Avahi provider failed to start", err.Error())
	
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
	assert.Equal(s.T(), "Zeroconf provider failed to start", err.Error())
	
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
	assert.Equal(s.T(), "mDNS provider factory not initialized", err.Error())
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
	assert.Equal(s.T(), "failed to create Avahi provider instance", err.Error())
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
	assert.Equal(s.T(), "Avahi provider factory function not available", err.Error())
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
	assert.Equal(s.T(), "no mDNS provider available - both Avahi and Zeroconf failed to initialize", err.Error())
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
	assert.Equal(s.T(), "cannot announce mDNS entry: no provider available", err.Error())
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
