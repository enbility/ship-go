package mdns

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/enbility/go-avahi"
	mocks "github.com/enbility/go-avahi/mocks"
	"github.com/enbility/ship-go/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

func TestAvahi(t *testing.T) {
	suite.Run(t, new(AvahiSuite))
}

type AvahiSuite struct {
	suite.Suite

	sut                 *AvahiProvider
	avahiMock           *mocks.ServerInterface
	serviceBrowserMock  *mocks.ServiceBrowserInterface
	entryGroupMock      *mocks.EntryGroupInterface
	serviceResolverMock *mocks.ServiceResolverInterface
}

func (a *AvahiSuite) BeforeTest(suiteName, testName string) {
	a.sut = NewAvahiProvider([]int32{1})
	assert.NotNil(a.T(), a.sut)

	a.avahiMock = mocks.NewServerInterface(a.T())
	a.serviceBrowserMock = mocks.NewServiceBrowserInterface(a.T())
	a.entryGroupMock = mocks.NewEntryGroupInterface(a.T())
	a.serviceResolverMock = mocks.NewServiceResolverInterface(a.T())

	a.sut.avServer = a.avahiMock
}

func (a *AvahiSuite) AfterTest(suiteName, testName string) {
}

func processMdnsEntry(elements map[string]string, name, host, service string, addresses []net.IP, port int, remove bool) {
}

func (a *AvahiSuite) Test_Avahi() {
	someError := errors.New("some error")

	a.avahiMock.EXPECT().Setup(mock.Anything).Return(someError).Once()
	success := a.sut.Start(api.PairingModeListener, false, nil)
	assert.False(a.T(), success)

	a.avahiMock.EXPECT().Setup(mock.Anything).Return(nil).Once()
	a.avahiMock.EXPECT().Start().Return().Once()
	a.avahiMock.EXPECT().GetAPIVersion().Return(0, someError).Once()
	a.avahiMock.EXPECT().Shutdown().Return().Once()
	success = a.sut.Start(api.PairingModeListener, false, nil)
	assert.False(a.T(), success)

	a.avahiMock.EXPECT().Setup(mock.Anything).Return(nil).Once()
	a.avahiMock.EXPECT().Start().Return().Once()
	a.avahiMock.EXPECT().GetAPIVersion().Return(0, nil).Once()
	a.avahiMock.EXPECT().ServiceBrowserNew(
		mock.AnythingOfType("chan avahi.Service"),
		mock.AnythingOfType("chan avahi.Service"),
		int32(-1), int32(-1),
		shipZeroConfServiceType, shipZeroConfDomain,
		uint32(0)).Return(nil, someError).Once()
	a.avahiMock.EXPECT().Shutdown().Return().Once()
	success = a.sut.Start(api.PairingModeListener, false, nil)
	assert.False(a.T(), success)

	a.avahiMock.EXPECT().Setup(mock.Anything).Return(nil).Once()
	a.avahiMock.EXPECT().Start().Return().Once()
	a.avahiMock.EXPECT().GetAPIVersion().Return(0, nil).Once()
	// Expect two service browsers - one for _ship._tcp and one for _shippairing._tcp
	a.avahiMock.EXPECT().ServiceBrowserNew(
		mock.AnythingOfType("chan avahi.Service"),
		mock.AnythingOfType("chan avahi.Service"),
		int32(-1), int32(-1),
		shipZeroConfServiceType, shipZeroConfDomain,
		uint32(0)).Return(a.serviceBrowserMock, nil).Once()
	a.avahiMock.EXPECT().ServiceBrowserNew(
		mock.AnythingOfType("chan avahi.Service"),
		mock.AnythingOfType("chan avahi.Service"),
		int32(-1), int32(-1),
		shipPairingZeroConfServiceType, shipZeroConfDomain,
		uint32(0)).Return(a.serviceBrowserMock, nil).Once()
	success = a.sut.Start(api.PairingModeListener, false, nil)
	assert.True(a.T(), success)

	a.avahiMock.EXPECT().EntryGroupNew().Return(a.entryGroupMock, nil).Once()
	a.avahiMock.EXPECT().EntryGroupFree(a.entryGroupMock).Return().Once()
	a.entryGroupMock.EXPECT().AddService(mock.Anything, mock.Anything, mock.Anything, "dummytest", shipZeroConfServiceType, shipZeroConfDomain, "", mock.Anything, mock.Anything).Return(nil).Once()
	a.entryGroupMock.EXPECT().Commit().Return(nil).Once()
	instanceID, err := a.sut.AnnounceService(shipZeroConfServiceType, "dummytest", 4289, []string{"more=more"})
	assert.Nil(a.T(), err)
	assert.NotEmpty(a.T(), instanceID)

	a.sut.UnannounceService(instanceID)

	testService := avahi.Service{
		Interface: 0,
		Protocol:  0,
		Name:      "TestService",
		Type:      "_ship._tcp",
		Domain:    "local",
		Host:      "localhost",
		Aprotocol: -1,
		Flags:     0,
	}
	a.avahiMock.EXPECT().ResolveService(
		int32(1), testService.Protocol,
		testService.Name, testService.Type, testService.Domain,
		testService.Aprotocol, testService.Flags).Return(avahi.Service{}, nil).Once()
	err = a.sut.processService(testService, false, processMdnsEntry)
	assert.NotNil(a.T(), err)

	testService = avahi.Service{
		Interface: 1,
		Protocol:  0,
		Name:      "TestService",
		Type:      "_ship._tcp",
		Domain:    "local",
		Host:      "localhost",
		Flags:     0,
	}
	a.avahiMock.EXPECT().ResolveService(
		testService.Interface, testService.Protocol,
		mock.Anything, testService.Type, testService.Domain,
		testService.Aprotocol, testService.Flags).Return(avahi.Service{}, nil).Maybe()
	err = a.sut.processService(testService, false, processMdnsEntry)
	assert.NotNil(a.T(), err)

	err = a.sut.processService(testService, true, processMdnsEntry)
	assert.Nil(a.T(), err)

	err = a.sut.processAddedService(testService, processMdnsEntry)
	assert.NotNil(a.T(), err)
	err = a.sut.processRemovedService(testService, processMdnsEntry)
	assert.Nil(a.T(), err)

	testService.Address = "2001:db8::68"
	err = a.sut.processAddedService(testService, processMdnsEntry)
	assert.Nil(a.T(), err)

	testService.Address = "127.0.0.1"
	err = a.sut.processAddedService(testService, processMdnsEntry)
	assert.Nil(a.T(), err)

	testService = avahi.Service{
		Interface: 1,
		Protocol:  0,
		Name:      "TestService",
		Type:      "_ship._tcp",
		Domain:    "local",
		Aprotocol: -1,
		Flags:     0,
		Address:   "127.0.0.1",
		Txt:       [][]byte{[]byte("ski=133742247331")},
	}
	result := getServiceUniqueKey(testService)
	assert.Equal(a.T(), "TestService-_ship._tcp-local-0-1", result)
	a.avahiMock.EXPECT().ResolveService(
		testService.Interface, testService.Protocol,
		testService.Name, testService.Type, testService.Domain,
		testService.Aprotocol, testService.Flags).Return(avahi.Service{}, nil).Once()

	err = a.sut.processAddedService(testService, processMdnsEntry)
	assert.Nil(a.T(), err)
	assert.Equal(a.T(), "133742247331", a.sut.serviceElements["TestService-_ship._tcp-local-0-1"]["ski"])

	a.sut.ifaceIndexes = []int32{avahi.InterfaceUnspec}
	err = a.sut.processService(testService, false, processMdnsEntry)
	assert.NotNil(a.T(), err)

	a.avahiMock.EXPECT().Shutdown().Return().Once()
	// Two ServiceBrowserFree calls - one for each browser
	a.avahiMock.EXPECT().ServiceBrowserFree(a.serviceBrowserMock).Return().Times(2)
	a.sut.Shutdown()
}

func (a *AvahiSuite) Test_Shutdown() {
	a.sut.Shutdown()
}

func (a *AvahiSuite) Test_Announce() {
	someError := errors.New("some error")
	a.sut.avServer = a.avahiMock

	a.avahiMock.EXPECT().EntryGroupNew().Return(nil, someError).Once()
	_, err := a.sut.AnnounceService(shipZeroConfServiceType, "dummytest", 4289, []string{"more=more"})
	assert.NotNil(a.T(), err)

	a.avahiMock.EXPECT().EntryGroupNew().Return(a.entryGroupMock, nil).Once()
	a.entryGroupMock.EXPECT().AddService(mock.Anything, mock.Anything, mock.Anything, "dummytest", shipZeroConfServiceType, shipZeroConfDomain, "", mock.Anything, mock.Anything).Return(someError).Once()
	a.avahiMock.EXPECT().EntryGroupFree(a.entryGroupMock).Return().Once() // Cleanup after AddService failure
	_, err = a.sut.AnnounceService(shipZeroConfServiceType, "dummytest", 4289, []string{"more=more"})
	assert.NotNil(a.T(), err)

	a.avahiMock.EXPECT().EntryGroupNew().Return(a.entryGroupMock, nil).Once()
	a.entryGroupMock.EXPECT().AddService(mock.Anything, mock.Anything, mock.Anything, "dummytest", shipZeroConfServiceType, shipZeroConfDomain, "", mock.Anything, mock.Anything).Return(nil).Once()
	a.entryGroupMock.EXPECT().Commit().Return(someError).Once()
	a.avahiMock.EXPECT().EntryGroupFree(a.entryGroupMock).Return().Once() // Cleanup after Commit failure
	_, err = a.sut.AnnounceService(shipZeroConfServiceType, "dummytest", 4289, []string{"more=more"})
	assert.NotNil(a.T(), err)

	a.entryGroupMock.EXPECT().AddService(mock.Anything, mock.Anything, mock.Anything, "dummytest", shipZeroConfServiceType, shipZeroConfDomain, "", mock.Anything, mock.Anything).Return(nil).Once()
	a.entryGroupMock.EXPECT().Commit().Return(nil).Once()
	a.avahiMock.EXPECT().EntryGroupNew().Return(a.entryGroupMock, nil).Once()
	instanceID2, err := a.sut.AnnounceService(shipZeroConfServiceType, "dummytest", 4289, []string{"more=more"})
	assert.Nil(a.T(), err)
	assert.NotEmpty(a.T(), instanceID2)

	a.avahiMock.EXPECT().EntryGroupFree(a.entryGroupMock).Return().Once()
	a.sut.UnannounceService(instanceID2)

	// Test calling UnannounceService on non-existent service
	a.sut.UnannounceService("nonexistent")
}

func (a *AvahiSuite) Test_Announce_Pairing() {
	someError := errors.New("some error")
	a.sut.avServer = a.avahiMock

	a.avahiMock.EXPECT().EntryGroupNew().Return(nil, someError).Once()
	_, err := a.sut.AnnounceService(shipPairingZeroConfServiceType, "dummytest", 4289, []string{"more=more"})
	assert.NotNil(a.T(), err)

	a.avahiMock.EXPECT().EntryGroupNew().Return(a.entryGroupMock, nil).Once()
	a.entryGroupMock.EXPECT().AddService(mock.Anything, mock.Anything, mock.Anything, "dummytest", shipPairingZeroConfServiceType, shipZeroConfDomain, "", mock.Anything, mock.Anything).Return(someError).Once()
	a.avahiMock.EXPECT().EntryGroupFree(a.entryGroupMock).Return().Once() // Cleanup after AddService failure
	_, err = a.sut.AnnounceService(shipPairingZeroConfServiceType, "dummytest", 4289, []string{"more=more"})
	assert.NotNil(a.T(), err)

	a.avahiMock.EXPECT().EntryGroupNew().Return(a.entryGroupMock, nil).Once()
	a.entryGroupMock.EXPECT().AddService(mock.Anything, mock.Anything, mock.Anything, "dummytest", shipPairingZeroConfServiceType, shipZeroConfDomain, "", mock.Anything, mock.Anything).Return(nil).Once()
	a.entryGroupMock.EXPECT().Commit().Return(someError).Once()
	a.avahiMock.EXPECT().EntryGroupFree(a.entryGroupMock).Return().Once() // Cleanup after Commit failure
	_, err = a.sut.AnnounceService(shipPairingZeroConfServiceType, "dummytest", 4289, []string{"more=more"})
	assert.NotNil(a.T(), err)

	a.entryGroupMock.EXPECT().AddService(mock.Anything, mock.Anything, mock.Anything, "dummytest", shipPairingZeroConfServiceType, shipZeroConfDomain, "", mock.Anything, mock.Anything).Return(nil).Once()
	a.entryGroupMock.EXPECT().Commit().Return(nil).Once()
	a.avahiMock.EXPECT().EntryGroupNew().Return(a.entryGroupMock, nil).Once()
	pairingInstanceID, err := a.sut.AnnounceService(shipPairingZeroConfServiceType, "dummytest", 4289, []string{"more=more"})
	assert.Nil(a.T(), err)
	assert.NotEmpty(a.T(), pairingInstanceID)

	a.avahiMock.EXPECT().EntryGroupFree(a.entryGroupMock).Return().Once()
	a.sut.UnannounceService(pairingInstanceID)

	// Test calling UnannounceService on non-existent service
	a.sut.UnannounceService("nonexistent")
}

func (a *AvahiSuite) Test_Avahi_Reconnect() {
	// As we do not have an Avahi server running for automated testing
	// these tests are very limited
	cb := func(elements map[string]string, name, host, service string, addresses []net.IP, port int, remove bool) {
		assert.NotEqual(a.T(), "", name)
	}

	a.avahiMock.EXPECT().Setup(mock.Anything).Return(nil).Once()
	a.avahiMock.EXPECT().Start().Return().Once()
	a.avahiMock.EXPECT().GetAPIVersion().Return(0, nil).Once()
	// Expect two service browsers - one for _ship._tcp and one for _shippairing._tcp
	a.avahiMock.EXPECT().ServiceBrowserNew(
		mock.AnythingOfType("chan avahi.Service"),
		mock.AnythingOfType("chan avahi.Service"),
		int32(-1), int32(-1),
		shipZeroConfServiceType, shipZeroConfDomain,
		uint32(0)).Return(a.serviceBrowserMock, nil).Once()
	a.avahiMock.EXPECT().ServiceBrowserNew(
		mock.AnythingOfType("chan avahi.Service"),
		mock.AnythingOfType("chan avahi.Service"),
		int32(-1), int32(-1),
		shipPairingZeroConfServiceType, shipZeroConfDomain,
		uint32(0)).Return(a.serviceBrowserMock, nil).Once()
	available := a.sut.Start(api.PairingModeListener, true, cb)
	assert.True(a.T(), available)

	a.avahiMock.EXPECT().EntryGroupNew().Return(a.entryGroupMock, nil).Once()
	a.entryGroupMock.EXPECT().AddService(mock.Anything, mock.Anything, mock.Anything, "dummytest", shipZeroConfServiceType, shipZeroConfDomain, "", mock.Anything, mock.Anything).Return(nil).Once()
	a.entryGroupMock.EXPECT().Commit().Return(nil).Once()
	_, err := a.sut.AnnounceService(shipZeroConfServiceType, "dummytest", 4289, []string{"more=more"})
	assert.Nil(a.T(), err)

	// a.avahiMock.EXPECT().Shutdown().Return().Once()
	// a.sut.avServer.Shutdown()
	a.avahiMock.EXPECT().Setup(mock.Anything).Return(nil).Once()
	a.avahiMock.EXPECT().Start().Return().Once()
	a.avahiMock.EXPECT().GetAPIVersion().Return(0, nil).Once()
	// Expect two service browsers on reconnect
	a.avahiMock.EXPECT().ServiceBrowserNew(
		mock.AnythingOfType("chan avahi.Service"),
		mock.AnythingOfType("chan avahi.Service"),
		int32(-1), int32(-1),
		shipZeroConfServiceType, shipZeroConfDomain,
		uint32(0)).Return(a.serviceBrowserMock, nil).Once()
	a.avahiMock.EXPECT().ServiceBrowserNew(
		mock.AnythingOfType("chan avahi.Service"),
		mock.AnythingOfType("chan avahi.Service"),
		int32(-1), int32(-1),
		shipPairingZeroConfServiceType, shipZeroConfDomain,
		uint32(0)).Return(a.serviceBrowserMock, nil).Once()
	a.avahiMock.EXPECT().EntryGroupNew().Return(a.entryGroupMock, nil).Once()
	a.entryGroupMock.EXPECT().AddService(mock.Anything, mock.Anything, mock.Anything, "dummytest", shipZeroConfServiceType, shipZeroConfDomain, "", mock.Anything, mock.Anything).Return(nil).Once()
	a.entryGroupMock.EXPECT().Commit().Return(nil).Once()
	a.sut.avahiCallback(avahi.Disconnected)

	// wait, as the cb will be invoked async
	time.Sleep(time.Second * 2)

	a.sut.mux.Lock()
	assert.NotNil(a.T(), a.sut.avServer)
	a.sut.mux.Unlock()

	a.avahiMock.EXPECT().EntryGroupFree(a.entryGroupMock).Return().Once()
	a.avahiMock.EXPECT().Shutdown().Return().Once()
	// Two ServiceBrowserFree calls - one for each browser
	a.avahiMock.EXPECT().ServiceBrowserFree(a.serviceBrowserMock).Return().Times(2)
	a.sut.Shutdown()

	a.avahiMock.EXPECT().Setup(mock.Anything).Return(nil).Once()
	a.avahiMock.EXPECT().Start().Return().Once()
	a.avahiMock.EXPECT().GetAPIVersion().Return(0, nil).Once()
	// Expect two service browsers after shutdown and restart
	a.avahiMock.EXPECT().ServiceBrowserNew(
		mock.AnythingOfType("chan avahi.Service"),
		mock.AnythingOfType("chan avahi.Service"),
		int32(-1), int32(-1),
		shipZeroConfServiceType, shipZeroConfDomain,
		uint32(0)).Return(a.serviceBrowserMock, nil).Once()
	a.avahiMock.EXPECT().ServiceBrowserNew(
		mock.AnythingOfType("chan avahi.Service"),
		mock.AnythingOfType("chan avahi.Service"),
		int32(-1), int32(-1),
		shipPairingZeroConfServiceType, shipZeroConfDomain,
		uint32(0)).Return(a.serviceBrowserMock, nil).Once()
	available = a.sut.Start(api.PairingModeListener, true, cb)
	assert.True(a.T(), available)
	a.sut.mux.Lock()
	assert.NotNil(a.T(), a.sut.avServer)
	a.sut.mux.Unlock()

	a.avahiMock.EXPECT().Shutdown().Return().Once()
	// Two ServiceBrowserFree calls - one for each browser
	a.avahiMock.EXPECT().ServiceBrowserFree(a.serviceBrowserMock).Return().Times(2)
	a.sut.Shutdown()

	a.avahiMock.EXPECT().Shutdown().Return().Once()
	a.sut.Shutdown()
}

func (a *AvahiSuite) Test_chanListener() {
	// As we do not have an Avahi server running for automated testing
	// these tests are very limited
	cb := func(elements map[string]string, name, host, service string, addresses []net.IP, port int, remove bool) {
		assert.NotEqual(a.T(), "", name)
	}

	a.avahiMock.EXPECT().Setup(mock.Anything).Return(nil).Once()
	a.avahiMock.EXPECT().Start().Return().Once()
	a.avahiMock.EXPECT().GetAPIVersion().Return(0, nil).Once()
	// Expect two service browsers - one for _ship._tcp and one for _shippairing._tcp
	a.avahiMock.EXPECT().ServiceBrowserNew(
		mock.AnythingOfType("chan avahi.Service"),
		mock.AnythingOfType("chan avahi.Service"),
		int32(-1), int32(-1),
		shipZeroConfServiceType, shipZeroConfDomain,
		uint32(0)).Return(a.serviceBrowserMock, nil).Once()
	a.avahiMock.EXPECT().ServiceBrowserNew(
		mock.AnythingOfType("chan avahi.Service"),
		mock.AnythingOfType("chan avahi.Service"),
		int32(-1), int32(-1),
		shipPairingZeroConfServiceType, shipZeroConfDomain,
		uint32(0)).Return(a.serviceBrowserMock, nil).Once()
	available := a.sut.Start(api.PairingModeListener, true, cb)
	assert.True(a.T(), available)

	time.Sleep(time.Second * 1)

	testService := avahi.Service{
		Interface: 1,
		Protocol:  0,
		Name:      "TestService",
		Type:      "_ship._tcp",
		Domain:    "local",
		Host:      "localhost",
		Aprotocol: -1,
		Flags:     0,
		Address:   "127.0.0.1",
	}
	a.avahiMock.EXPECT().ResolveService(
		testService.Interface, testService.Protocol,
		testService.Name, testService.Type, testService.Domain,
		testService.Aprotocol, testService.Flags).Return(testService, nil).Once()
	a.sut.addServiceChan <- testService
	time.Sleep(time.Second * 1)
	a.sut.muxEl.Lock()
	assert.Equal(a.T(), 1, len(a.sut.serviceElements))
	a.sut.muxEl.Unlock()

	a.sut.removeServiceChan <- testService
	time.Sleep(time.Second * 1)

	a.avahiMock.EXPECT().Shutdown().Return().Once()
	// Two ServiceBrowserFree calls - one for each browser
	a.avahiMock.EXPECT().ServiceBrowserFree(a.serviceBrowserMock).Return().Times(2)
	a.sut.Shutdown()
}
