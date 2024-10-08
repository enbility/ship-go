// Code generated by mockery v2.45.0. DO NOT EDIT.

package mocks

import mock "github.com/stretchr/testify/mock"

// ShipConnectionDataReaderInterface is an autogenerated mock type for the ShipConnectionDataReaderInterface type
type ShipConnectionDataReaderInterface struct {
	mock.Mock
}

type ShipConnectionDataReaderInterface_Expecter struct {
	mock *mock.Mock
}

func (_m *ShipConnectionDataReaderInterface) EXPECT() *ShipConnectionDataReaderInterface_Expecter {
	return &ShipConnectionDataReaderInterface_Expecter{mock: &_m.Mock}
}

// HandleShipPayloadMessage provides a mock function with given fields: message
func (_m *ShipConnectionDataReaderInterface) HandleShipPayloadMessage(message []byte) {
	_m.Called(message)
}

// ShipConnectionDataReaderInterface_HandleShipPayloadMessage_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'HandleShipPayloadMessage'
type ShipConnectionDataReaderInterface_HandleShipPayloadMessage_Call struct {
	*mock.Call
}

// HandleShipPayloadMessage is a helper method to define mock.On call
//   - message []byte
func (_e *ShipConnectionDataReaderInterface_Expecter) HandleShipPayloadMessage(message interface{}) *ShipConnectionDataReaderInterface_HandleShipPayloadMessage_Call {
	return &ShipConnectionDataReaderInterface_HandleShipPayloadMessage_Call{Call: _e.mock.On("HandleShipPayloadMessage", message)}
}

func (_c *ShipConnectionDataReaderInterface_HandleShipPayloadMessage_Call) Run(run func(message []byte)) *ShipConnectionDataReaderInterface_HandleShipPayloadMessage_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].([]byte))
	})
	return _c
}

func (_c *ShipConnectionDataReaderInterface_HandleShipPayloadMessage_Call) Return() *ShipConnectionDataReaderInterface_HandleShipPayloadMessage_Call {
	_c.Call.Return()
	return _c
}

func (_c *ShipConnectionDataReaderInterface_HandleShipPayloadMessage_Call) RunAndReturn(run func([]byte)) *ShipConnectionDataReaderInterface_HandleShipPayloadMessage_Call {
	_c.Call.Return(run)
	return _c
}

// NewShipConnectionDataReaderInterface creates a new instance of ShipConnectionDataReaderInterface. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewShipConnectionDataReaderInterface(t interface {
	mock.TestingT
	Cleanup(func())
}) *ShipConnectionDataReaderInterface {
	mock := &ShipConnectionDataReaderInterface{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
