package ship

import (
	"testing"

	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestConnectionSerializationSuite(t *testing.T) {
	suite.Run(t, new(ConnectionSerializationSuite))
}

type ConnectionSerializationSuite struct {
	ConnectionSuite
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