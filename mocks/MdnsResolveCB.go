// Code generated by mockery v2.40.1. DO NOT EDIT.

package mocks

import (
	net "net"

	mock "github.com/stretchr/testify/mock"
)

// MdnsResolveCB is an autogenerated mock type for the MdnsResolveCB type
type MdnsResolveCB struct {
	mock.Mock
}

type MdnsResolveCB_Expecter struct {
	mock *mock.Mock
}

func (_m *MdnsResolveCB) EXPECT() *MdnsResolveCB_Expecter {
	return &MdnsResolveCB_Expecter{mock: &_m.Mock}
}

// Execute provides a mock function with given fields: elements, name, host, addresses, port, remove
func (_m *MdnsResolveCB) Execute(elements map[string]string, name string, host string, addresses []net.IP, port int, remove bool) {
	_m.Called(elements, name, host, addresses, port, remove)
}

// MdnsResolveCB_Execute_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Execute'
type MdnsResolveCB_Execute_Call struct {
	*mock.Call
}

// Execute is a helper method to define mock.On call
//   - elements map[string]string
//   - name string
//   - host string
//   - addresses []net.IP
//   - port int
//   - remove bool
func (_e *MdnsResolveCB_Expecter) Execute(elements interface{}, name interface{}, host interface{}, addresses interface{}, port interface{}, remove interface{}) *MdnsResolveCB_Execute_Call {
	return &MdnsResolveCB_Execute_Call{Call: _e.mock.On("Execute", elements, name, host, addresses, port, remove)}
}

func (_c *MdnsResolveCB_Execute_Call) Run(run func(elements map[string]string, name string, host string, addresses []net.IP, port int, remove bool)) *MdnsResolveCB_Execute_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(map[string]string), args[1].(string), args[2].(string), args[3].([]net.IP), args[4].(int), args[5].(bool))
	})
	return _c
}

func (_c *MdnsResolveCB_Execute_Call) Return() *MdnsResolveCB_Execute_Call {
	_c.Call.Return()
	return _c
}

func (_c *MdnsResolveCB_Execute_Call) RunAndReturn(run func(map[string]string, string, string, []net.IP, int, bool)) *MdnsResolveCB_Execute_Call {
	_c.Call.Return(run)
	return _c
}

// NewMdnsResolveCB creates a new instance of MdnsResolveCB. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewMdnsResolveCB(t interface {
	mock.TestingT
	Cleanup(func())
}) *MdnsResolveCB {
	mock := &MdnsResolveCB{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
