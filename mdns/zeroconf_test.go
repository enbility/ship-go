package mdns

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestZeroconf(t *testing.T) {
	suite.Run(t, new(ZeroconfSuite))
}

type ZeroconfSuite struct {
	suite.Suite

	sut *ZeroconfProvider

	mux sync.Mutex
}

func (z *ZeroconfSuite) BeforeTest(suiteName, testName string) {
	z.sut = NewZeroconfProvider([]net.Interface{})
}

func (z *ZeroconfSuite) AfterTest(suiteName, testName string) {
	z.sut.Shutdown()
}

func (z *ZeroconfSuite) Test_ZeroConf() {
	boolV := z.sut.CheckAvailability()
	assert.Equal(z.T(), true, boolV)

	cancelChan := make(chan bool)

	testEntryFound := false
	dummyTestEntryFound := false

	cb := func(elements map[string]string, name, host string, addresses []net.IP, port int, remove bool) {
		// we expect at least one entry
		assert.NotEqual(z.T(), "", name)

		// additional checks for our test announcement entry
		if name == "test" {
			assert.Equal(z.T(), 4289, port)
			assert.Equal(z.T(), map[string]string{"test": "test"}, elements)
			z.mux.Lock()
			testEntryFound = true
			z.mux.Unlock()
		}

		if name == "dummytest" {
			assert.Equal(z.T(), 4289, port)
			assert.Equal(z.T(), map[string]string{}, elements)
			z.mux.Lock()
			dummyTestEntryFound = true
			z.mux.Unlock()
		}
	}
	go z.sut.ResolveEntries(cancelChan, cb)

	err := z.sut.Announce("dummytest", 4289, []string{})
	assert.Nil(z.T(), err)

	time.Sleep(time.Second * 3)

	z.mux.Lock()
	assert.Equal(z.T(), true, dummyTestEntryFound)
	dummyTestEntryFound = false
	z.mux.Unlock()

	z.sut.Unannounce()

	time.Sleep(time.Second * 3)

	z.mux.Lock()
	assert.Equal(z.T(), true, dummyTestEntryFound)
	z.mux.Unlock()

	err = z.sut.Announce("test", 4289, []string{"test=test"})
	assert.Nil(z.T(), err)

	time.Sleep(time.Second * 3)

	z.mux.Lock()
	assert.Equal(z.T(), true, testEntryFound)
	z.mux.Unlock()

	z.sut.Unannounce()

	err = z.sut.Announce("", 4289, []string{"test=test"})
	assert.NotNil(z.T(), err)

	z.sut.Unannounce()

	cancelChan <- true
}
