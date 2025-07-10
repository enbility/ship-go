package ship

import (
	"errors"
	"testing"

	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

func TestConnectionSerializationSuite(t *testing.T) {
	suite.Run(t, new(ConnectionSerializationSuite))
}

type ConnectionSerializationSuite struct {
	ConnectionSuite
}

func (s *ConnectionSerializationSuite) setupCustomMocks() {
	// Clear existing expectations
	s.wsDataWriter.ExpectedCalls = nil
	s.wsDataWriter.Calls = nil
	s.infoProvider.ExpectedCalls = nil
	s.infoProvider.Calls = nil

	// Re-setup basic mocks without WriteMessageToWebsocketConnection
	s.wsDataWriter.EXPECT().InitDataProcessing(mock.Anything).Return().Maybe()
	s.wsDataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Return().Maybe()

	s.infoProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Return().Maybe()
	s.infoProvider.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(false).Maybe()
	s.infoProvider.EXPECT().AllowWaitingForTrust(mock.Anything).Return(false).Maybe()
}

func (s *ConnectionSerializationSuite) TestTransformSpineDataIntoShipJson() {
	spineTest := `{"datagram":{"header":{"specificationVersion":"1.2.0","addressSource":{"device":"Demo-EVSE-234567890","entity":[0],"feature":0},"addressDestination":{"device":"Demo-HEMS-123456789","entity":[0],"feature":0},"msgCounter":1,"cmdClassifier":"read"},"payload":{"cmd":[{"nodeManagementDetailedDiscoveryData":{}}]}}}`
	jsonExpected := `{"data":[{"header":[{"protocolId":"ee1.0"}]},{"payload":{"datagram":[{"header":[{"specificationVersion":"1.2.0"},{"addressSource":[{"device":"Demo-EVSE-234567890"},{"entity":[0]},{"feature":0}]},{"addressDestination":[{"device":"Demo-HEMS-123456789"},{"entity":[0]},{"feature":0}]},{"msgCounter":1},{"cmdClassifier":"read"}]},{"payload":[{"cmd":[[{"nodeManagementDetailedDiscoveryData":[]}]]}]}]}}]}`

	result, err := s.sut.transformSpineDataIntoShipJson([]byte(spineTest))
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), jsonExpected, string(result))
}

func (s *ConnectionSerializationSuite) TestSendShipModel() {
	err := s.sut.sendShipModel(model.MsgTypeInit, nil)
	assert.NotNil(s.T(), err)

	closeMessage := model.ConnectionClose{
		ConnectionClose: model.ConnectionCloseType{
			Phase: model.ConnectionClosePhaseTypeAnnounce,
		},
	}

	err = s.sut.sendShipModel(model.MsgTypeControl, closeMessage)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), s.sentMessage)
}

func (s *ConnectionSerializationSuite) TestSendSpineData_Success() {
	spineData := []byte(`{"datagram":{"header":{"specificationVersion":"1.2.0"}}}`)

	err := s.sut.sendSpineData(spineData)
	assert.NoError(s.T(), err)

	// Verify message was sent with correct SHIP header
	assert.NotNil(s.T(), s.sentMessage)
	assert.Greater(s.T(), len(s.sentMessage), 1)
	assert.Equal(s.T(), model.MsgTypeData, s.sentMessage[0])

	// Verify the message contains transformed JSON
	messageContent := string(s.sentMessage[1:])
	assert.Contains(s.T(), messageContent, "protocolId")
	assert.Contains(s.T(), messageContent, "ee1.0")
}

func (s *ConnectionSerializationSuite) TestSendSpineData_TransformationError() {
	// Invalid JSON that will cause transformation error
	invalidSpineData := []byte(`{invalid json}`)

	err := s.sut.sendSpineData(invalidSpineData)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "invalid character")
}

func (s *ConnectionSerializationSuite) TestSendSpineData_ConnectionClosed() {
	// Setup custom mocks without default WriteMessageToWebsocketConnection
	s.setupCustomMocks()

	spineData := []byte(`{"datagram":{"header":{"specificationVersion":"1.2.0"}}}`)

	// Clear any previous sent message
	s.sentMessage = nil

	// Mock connection as closed - when isClosed is true, err can be nil
	s.wsDataWriter.EXPECT().IsDataConnectionClosed().Return(true, nil).Once()
	// Expect HandleConnectionClosed to be called when connection is closed
	s.infoProvider.EXPECT().HandleConnectionClosed(s.sut, false).Return().Once()

	err := s.sut.sendSpineData(spineData)
	// When connection is closed but no error from IsDataConnectionClosed, sendSpineData returns nil
	assert.NoError(s.T(), err)

	// Verify no message was sent when connection is closed
	assert.Nil(s.T(), s.sentMessage)
}

func (s *ConnectionSerializationSuite) TestSendSpineData_ConnectionClosedWithError() {
	// Setup custom mocks without default WriteMessageToWebsocketConnection
	s.setupCustomMocks()

	spineData := []byte(`{"datagram":{"header":{"specificationVersion":"1.2.0"}}}`)

	// Clear any previous sent message
	s.sentMessage = nil

	// Mock connection as closed with an error
	closedErr := errors.New("connection closed")
	s.wsDataWriter.EXPECT().IsDataConnectionClosed().Return(true, closedErr).Once()
	// Expect HandleConnectionClosed to be called when connection is closed
	s.infoProvider.EXPECT().HandleConnectionClosed(s.sut, false).Return().Once()

	err := s.sut.sendSpineData(spineData)
	assert.Error(s.T(), err)
	assert.Equal(s.T(), closedErr, err)

	// Verify no message was sent when connection is closed
	assert.Nil(s.T(), s.sentMessage)
}

func (s *ConnectionSerializationSuite) TestSendSpineData_WriteError() {
	// Setup custom mocks
	s.setupCustomMocks()

	spineData := []byte(`{"datagram":{"header":{"specificationVersion":"1.2.0"}}}`)

	// Clear any previous sent message
	s.sentMessage = nil

	// Mock connection as open
	s.wsDataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Once()

	// Mock write error
	expectedErr := errors.New("write failed")
	s.wsDataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(expectedErr).Once()

	err := s.sut.sendSpineData(spineData)
	assert.Error(s.T(), err)
	assert.Equal(s.T(), expectedErr, err)
}

func (s *ConnectionSerializationSuite) TestShipMessage_Success() {
	// Test with a valid model
	testModel := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase: model.ConnectionHelloPhaseTypeReady,
		},
	}

	result, err := s.sut.shipMessage(model.MsgTypeInit, testModel)
	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), result)
	assert.Greater(s.T(), len(result), 1)
	assert.Equal(s.T(), model.MsgTypeInit, result[0])

	// Verify the message contains EEBUS JSON format
	messageContent := string(result[1:])
	assert.Contains(s.T(), messageContent, "connectionHello")
}

func (s *ConnectionSerializationSuite) TestShipMessage_NilModel() {
	result, err := s.sut.shipMessage(model.MsgTypeControl, nil)
	assert.Error(s.T(), err)
	assert.Nil(s.T(), result)
	assert.Contains(s.T(), err.Error(), "model is nil")
	assert.Contains(s.T(), err.Error(), s.sut.remoteSKI)
}

func (s *ConnectionSerializationSuite) TestShipMessage_ConnectionClosed() {
	// Setup custom mocks
	s.setupCustomMocks()

	testModel := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase: model.ConnectionHelloPhaseTypeReady,
		},
	}

	// Mock connection as closed
	s.wsDataWriter.EXPECT().IsDataConnectionClosed().Return(true, nil).Once()
	s.infoProvider.EXPECT().HandleConnectionClosed(s.sut, false).Return().Once()

	result, err := s.sut.shipMessage(model.MsgTypeInit, testModel)
	assert.NoError(s.T(), err) // Returns nil error when connection is closed without error
	assert.Nil(s.T(), result)
}

func (s *ConnectionSerializationSuite) TestShipMessage_ConnectionClosedWithError() {
	// Setup custom mocks
	s.setupCustomMocks()

	testModel := model.ConnectionHello{
		ConnectionHello: model.ConnectionHelloType{
			Phase: model.ConnectionHelloPhaseTypeReady,
		},
	}

	// Mock connection as closed with error
	closedErr := errors.New("connection error")
	s.wsDataWriter.EXPECT().IsDataConnectionClosed().Return(true, closedErr).Once()
	s.infoProvider.EXPECT().HandleConnectionClosed(s.sut, false).Return().Once()

	result, err := s.sut.shipMessage(model.MsgTypeInit, testModel)
	assert.Error(s.T(), err)
	assert.Equal(s.T(), closedErr, err)
	assert.Nil(s.T(), result)
}

// Create a type that cannot be marshaled to JSON
type unmarshalableType struct {
	Channel chan int
}

func (s *ConnectionSerializationSuite) TestShipMessage_MarshalError() {
	// Use a type that cannot be marshaled to JSON
	badModel := unmarshalableType{
		Channel: make(chan int),
	}

	result, err := s.sut.shipMessage(model.MsgTypeControl, badModel)
	assert.Error(s.T(), err)
	assert.Nil(s.T(), result)
	assert.Contains(s.T(), err.Error(), "json: unsupported type")
}

func (s *ConnectionSerializationSuite) TestShipMessage_JsonIntoEEBUSJsonError() {
	// Create a model that will marshal to invalid JSON for EEBUS conversion
	// We need to create a scenario where json.Marshal succeeds but JsonIntoEEBUSJson fails
	// This is tricky because JsonIntoEEBUSJson expects valid JSON
	// One way is to use a string that contains invalid JSON when unmarshaled
	type badJSONModel string

	// This will marshal to a JSON string, but when JsonIntoEEBUSJson tries to unmarshal it,
	// it will fail because the content is not a valid JSON object
	badModel := badJSONModel("not a json object")

	result, err := s.sut.shipMessage(model.MsgTypeControl, badModel)
	assert.Error(s.T(), err)
	assert.Nil(s.T(), result)
}
