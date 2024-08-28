package ship

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/enbility/ship-go/model"
)

func TestJsonFromEEBUSJson(t *testing.T) {
	jsonTest := `{"datagram":[{"header":[{"specificationVersion":"1.2.0"},{"addressSource":[{"device":"d:_i:3210_EVSE"},{"entity":[1,1]},{"feature":6}]},{"addressDestination":[{"device":"d:_i:3210_HEMS"},{"entity":[1]},{"feature":1}]},{"msgCounter":194},{"msgCounterReference":4890},{"cmdClassifier":"reply"}]},{"payload":[{"cmd":[[{"deviceClassificationManufacturerData":[{"deviceName":""},{"deviceCode":""},{"brandName":""},{"powerSource":"mains3Phase"}]}]]}]}]}`
	jsonExpected := `{"datagram":{"header":{"specificationVersion":"1.2.0","addressSource":{"device":"d:_i:3210_EVSE","entity":[1,1],"feature":6},"addressDestination":{"device":"d:_i:3210_HEMS","entity":[1],"feature":1},"msgCounter":194,"msgCounterReference":4890,"cmdClassifier":"reply"},"payload":{"cmd":[{"deviceClassificationManufacturerData":{"deviceName":"","deviceCode":"","brandName":"","powerSource":"mains3Phase"}}]}}}`

	var json = JsonFromEEBUSJson([]byte(jsonTest))

	if string(json) != jsonExpected {
		t.Errorf("\nExpected:\n  %s\ngot:\n  %s", jsonExpected, json)
	}
}

// The PMCP device mistakenly adds an `0x00` byte at the end of many messages. Test if this is handled correctly
func TestJsonFromEEBUSJsonTrailingZeros(t *testing.T) {
	bytes := []byte(`{"datagram":[{"header":[{"specificationVersion":"1.2.0"},{"addressSource":[{"device":"d:_i:3210_EVSE"},{"entity":[1,1]},{"feature":6}]},{"addressDestination":[{"device":"d:_i:3210_HEMS"},{"entity":[1]},{"feature":1}]},{"msgCounter":194},{"msgCounterReference":4890},{"cmdClassifier":"reply"}]},{"payload":[{"cmd":[[{"deviceClassificationManufacturerData":[{"deviceName":""},{"deviceCode":""},{"brandName":""},{"powerSource":"mains3Phase"}]}]]}]}]}`)
	bytes = append(bytes, 0x00)

	jsonTest := string(bytes[:])
	jsonExpected := `{"datagram":{"header":{"specificationVersion":"1.2.0","addressSource":{"device":"d:_i:3210_EVSE","entity":[1,1],"feature":6},"addressDestination":{"device":"d:_i:3210_HEMS","entity":[1],"feature":1},"msgCounter":194,"msgCounterReference":4890,"cmdClassifier":"reply"},"payload":{"cmd":[{"deviceClassificationManufacturerData":{"deviceName":"","deviceCode":"","brandName":"","powerSource":"mains3Phase"}}]}}}`

	var json = JsonFromEEBUSJson([]byte(jsonTest))

	if string(json) != jsonExpected {
		t.Errorf("\nExpected:\n  %s\ngot:\n  %s", jsonExpected, json)
	}
}

func TestJsonIntoEEBUSJson(t *testing.T) {
	jsonTest := `{"datagram":{"header":{"specificationVersion":"1.2.0","addressSource":{"device":"d:_i:3210_EVSE","entity":[1,1],"feature":6},"addressDestination":{"device":"d:_i:3210_HEMS","entity":[1],"feature":1},"msgCounter":194,"msgCounterReference":4890,"cmdClassifier":"reply"},"payload":{"cmd":[{"deviceClassificationManufacturerData":{"deviceName":"","deviceCode":"","brandName":"","powerSource":"mains3Phase"}}]}}}`
	jsonExpected := `{"datagram":[{"header":[{"specificationVersion":"1.2.0"},{"addressSource":[{"device":"d:_i:3210_EVSE"},{"entity":[1,1]},{"feature":6}]},{"addressDestination":[{"device":"d:_i:3210_HEMS"},{"entity":[1]},{"feature":1}]},{"msgCounter":194},{"msgCounterReference":4890},{"cmdClassifier":"reply"}]},{"payload":[{"cmd":[[{"deviceClassificationManufacturerData":[{"deviceName":""},{"deviceCode":""},{"brandName":""},{"powerSource":"mains3Phase"}]}]]}]}]}`

	var json, err = JsonIntoEEBUSJson([]byte(jsonTest))
	if err != nil {
		println(err.Error())
		t.Errorf("\nExpected:\n  %s\ngot:\n  %s", jsonExpected, json)
	}

	if json != jsonExpected {
		t.Errorf("\nExpected:\n  %s\ngot:\n  %s", jsonExpected, json)
	}
}

func TestShipJsonIntoEEBUSJson(t *testing.T) {
	spineTest := `{"datagram":{"header":{"specificationVersion":"1.2.0","addressSource":{"device":"Demo-EVSE-234567890","entity":[0],"feature":0},"addressDestination":{"device":"Demo-HEMS-123456789","entity":[0],"feature":0},"msgCounter":1,"cmdClassifier":"read"},"payload":{"cmd":[{"nodeManagementDetailedDiscoveryData":{}}]}}}`
	jsonExpected := `{"data":[{"header":[{"protocolId":"ee1.0"}]},{"payload":{"datagram":[{"header":[{"specificationVersion":"1.2.0"},{"addressSource":[{"device":"Demo-EVSE-234567890"},{"entity":[0]},{"feature":0}]},{"addressDestination":[{"device":"Demo-HEMS-123456789"},{"entity":[0]},{"feature":0}]},{"msgCounter":1},{"cmdClassifier":"read"}]},{"payload":[{"cmd":[[{"nodeManagementDetailedDiscoveryData":[]}]]}]}]}}]}`

	// TODO: move this test into connection_test using "transformSpineDataIntoShipJson()"
	spineMsg, err := JsonIntoEEBUSJson([]byte(spineTest))
	if err != nil {
		t.Error(err.Error())
	}
	payload := json.RawMessage([]byte(spineMsg))

	shipMessage := model.ShipData{
		Data: model.DataType{
			Header: model.HeaderType{
				ProtocolId: model.ShipProtocolId,
			},
			Payload: json.RawMessage([]byte(payloadPlaceholder)),
		},
	}

	msg, err := json.Marshal(shipMessage)
	if err != nil {
		t.Error(err.Error())
	}

	json, err := JsonIntoEEBUSJson(msg)
	if err != nil {
		println(err.Error())
		t.Errorf("\nExpected:\n  %s\ngot:\n  %s", jsonExpected, json)
	}

	json = strings.ReplaceAll(json, `[`+payloadPlaceholder+`]`, string(payload))

	if json != jsonExpected {
		t.Errorf("\nExpected:\n  %s\ngot:\n  %s", jsonExpected, json)
	}
}
