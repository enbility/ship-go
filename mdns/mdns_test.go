package mdns

import (
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

	s.mdnsProvider = mocks.NewMdnsProviderInterface(s.T())
	s.mdnsProvider.On("ResolveEntries", mock.Anything, mock.Anything).Maybe().Return()
	s.mdnsProvider.On("Shutdown").Maybe().Return()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem", "shipid", "serviceName", 4729, nil, MdnsProviderSelectionAll)
	s.sut.mdnsProvider = s.mdnsProvider
}

func (s *MdnsSuite) AfterTest(suiteName, testName string) {
	s.sut.Shutdown()
}

func (s *MdnsSuite) Test_AvahiOnly() {
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem", "shipid", "serviceName", 4729, nil, MdnsProviderSelectionAvahiOnly)
	s.sut.mdnsProvider = s.mdnsProvider

	_ = s.sut.Start(s.mdnsSearch)
	// Can't do an assertion check, as the result depends on the
	// system this test is being ran on
}

func (s *MdnsSuite) Test_GoZeroConfOnly() {
	s.sut.Shutdown()

	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem", "shipid", "serviceName", 4729, nil, MdnsProviderSelectionGoZeroConfOnly)
	s.sut.mdnsProvider = s.mdnsProvider

	err := s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)
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

	s.mdnsSearch.EXPECT().ReportMdnsEntries(mock.Anything).Maybe()

	s.sut.RequestMdnsEntries()

	time.Sleep(time.Millisecond * 500)
}

func (s *MdnsSuite) Test_ProcessMdnsEntry() {
	err := s.sut.Start(s.mdnsSearch)
	assert.Nil(s.T(), err)

	s.mdnsSearch.EXPECT().ReportMdnsEntries(mock.Anything).Maybe()

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
	s.sut.processMdnsEntry(elements, name, host, ips, port, false)
	assert.Equal(s.T(), 1, len(s.sut.mdnsEntries()))

	ips = []net.IP{[]byte("127.0.0.1")}
	s.sut.processMdnsEntry(elements, name, host, ips, port, false)
	assert.Equal(s.T(), 1, len(s.sut.mdnsEntries()))

	s.sut.processMdnsEntry(elements, name, host, ips, port, false)
	assert.Equal(s.T(), 1, len(s.sut.mdnsEntries()))

	s.sut.processMdnsEntry(elements, name, host, ips, port, true)
	assert.Equal(s.T(), 0, len(s.sut.mdnsEntries()))
}
