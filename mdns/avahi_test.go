package mdns

import (
	"net"
	"testing"
	"time"

	"github.com/enbility/go-avahi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestAvahi(t *testing.T) {
	suite.Run(t, new(AvahiSuite))
}

type AvahiSuite struct {
	suite.Suite

	sut *AvahiProvider
}

func (a *AvahiSuite) BeforeTest(suiteName, testName string) {
	a.sut = NewAvahiProvider([]int32{1})
}

func (a *AvahiSuite) AfterTest(suiteName, testName string) {
	a.sut.Shutdown()
}

func processMdnsEntry(elements map[string]string, name, host string, addresses []net.IP, port int, remove bool) {
}

func (a *AvahiSuite) Test_Avahi() {
	// As we do not have an Avahi server running for automated testing
	// these tests are very limited

	available := a.sut.Start(false)

	if available {
		err := a.sut.Announce("dummytest", 4289, []string{"more=more"})
		assert.Nil(a.T(), err)

		a.sut.Unannounce()
	} else {
		err := a.sut.Announce("dummytest", 4289, []string{"more=more"})
		assert.NotNil(a.T(), err)

		a.sut.Unannounce()

		a.sut.ResolveEntries(processMdnsEntry)
	}

	testService := avahi.Service{
		Interface: 0,
	}
	err := a.sut.processService(testService, false, processMdnsEntry)
	assert.NotNil(a.T(), err)

	testService = avahi.Service{
		Interface: 1,
	}
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
		Name:      "TestService",
		Type:      "_ship._tcp",
		Domain:    "local",
		Protocol:  0,
		Interface: 1,
		Address:   "127.0.0.1",
		Txt:       [][]byte{[]byte("ski=133742247331")},
	}
	result := getServiceUniqueKey(testService)
	assert.Equal(a.T(), "TestService-_ship._tcp-local-0-1", result)

	err = a.sut.processAddedService(testService, processMdnsEntry)
	assert.Nil(a.T(), err)
	assert.Equal(a.T(), "133742247331", a.sut.serviceElements["TestService-_ship._tcp-local-0-1"]["ski"])

	a.sut.ifaceIndexes = []int32{avahi.InterfaceUnspec}
	err = a.sut.processService(testService, false, processMdnsEntry)
	assert.NotNil(a.T(), err)
}

func (a *AvahiSuite) Test_Avahi_ResolveEntries() {
	// As we do not have an Avahi server running for automated testing
	// these tests are very limited
	available := a.sut.Start(true)
	if !available {
		a.T().Skip("Avahi not available")
	}

	cb := func(elements map[string]string, name, host string, addresses []net.IP, port int, remove bool) {
		assert.NotEqual(a.T(), "", name)
	}

	go a.sut.ResolveEntries(cb)

	time.Sleep(time.Second * 2)

	a.sut.Shutdown()
}

func (a *AvahiSuite) Test_Avahi_Reconnect() {
	// As we do not have an Avahi server running for automated testing
	// these tests are very limited
	available := a.sut.Start(true)

	if !available {
		a.T().Skip("Avahi not available")
	}

	a.sut.Start(true)

	assert.True(a.T(), available)

	a.sut.avServer.Shutdown()

	// wait, as the cb will be invoked async
	time.Sleep(time.Second * 2)

	a.sut.mux.Lock()
	assert.NotNil(a.T(), a.sut.avServer)
	a.sut.mux.Unlock()

	a.sut.Shutdown()

	available = a.sut.Start(true)
	assert.True(a.T(), available)
	a.sut.mux.Lock()
	assert.NotNil(a.T(), a.sut.avServer)
	a.sut.mux.Unlock()

	a.sut.Shutdown()

	a.sut.Shutdown()
}
