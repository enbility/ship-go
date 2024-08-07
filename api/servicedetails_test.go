package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestServiceDetails(t *testing.T) {
	suite.Run(t, new(ServiceDetailsSuite))
}

type ServiceDetailsSuite struct {
	suite.Suite
}

func (s *ServiceDetailsSuite) Test_ServiceDetails() {
	testSki := "test"

	details := NewServiceDetails(testSki)
	assert.NotNil(s.T(), details)

	conState := NewConnectionStateDetail(ConnectionStateNone, nil)
	details.SetConnectionStateDetail(conState)

	state := details.ConnectionStateDetail()
	assert.Equal(s.T(), ConnectionStateNone, state.State())

	ski := details.SKI()
	assert.Equal(s.T(), testSki, ski)

	details.SetIPv4("127.0.0.1")
	assert.Equal(s.T(), "127.0.0.1", details.IPv4())

	details.SetShipID("shipid")
	assert.Equal(s.T(), "shipid", details.ShipID())

	details.SetDeviceType("devicetype")
	assert.Equal(s.T(), "devicetype", details.DeviceType())

	details.SetAutoAccept(true)
	assert.Equal(s.T(), true, details.AutoAccept())

	details.SetTrusted(true)
	assert.Equal(s.T(), true, details.Trusted())
}
