package ship

import (
	"testing"
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

