package mdns

import (
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestPairingProcessorSuite(t *testing.T) {
	suite.Run(t, new(PairingProcessorSuite))
}

type PairingProcessorSuite struct {
	suite.Suite

	sut                 *MdnsManager
	pairingCallback     func(*api.ShipPairingTXT) bool
	receivedPairingData *api.ShipPairingTXT
}

func (s *PairingProcessorSuite) SetupTest() {
	s.sut = NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		nil,
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)

	// Reset callback tracking
	s.receivedPairingData = nil
	s.pairingCallback = func(data *api.ShipPairingTXT) bool {
		s.receivedPairingData = data
		return true
	}
}

func (s *PairingProcessorSuite) Test_processPairingEntry_ValidEntry() {
	// Prepare valid pairing TXT elements
	elements := map[string]string{
		"txtvers":    "1",
		"parType":    "1",
		"forId":      "target-device-id",
		"forPar":     "target-device-par",
		"trustId":    "source-device-id",
		"trustPar":   "source-device-par",
		"trustCurve": "P-256",
		"type":       "offer",
		"trustNonce": "123456",
		"alg":        "HS256",
		"digest":     "abcdef123456",
	}

	remove := false

	// Register callback
	s.sut.RegisterPairingCallback(s.pairingCallback)

	// Process the pairing entry
	s.sut.processShipPairingMdnsEntry(elements, "servicename", remove)

	// Verify callback was called with correct data
	assert.NotNil(s.T(), s.receivedPairingData)
	assert.Equal(s.T(), "1", s.receivedPairingData.TxtVers)
	assert.Equal(s.T(), "1", s.receivedPairingData.ParType)
	assert.Equal(s.T(), "target-device-id", s.receivedPairingData.ForId)
	assert.Equal(s.T(), "target-device-par", s.receivedPairingData.ForPar)
	assert.Equal(s.T(), "source-device-id", s.receivedPairingData.TrustId)
	assert.Equal(s.T(), "source-device-par", s.receivedPairingData.TrustPar)
	assert.Equal(s.T(), "P-256", s.receivedPairingData.TrustCurve)
	assert.Equal(s.T(), "offer", s.receivedPairingData.Type)
	assert.Equal(s.T(), "123456", s.receivedPairingData.TrustNonce)
	assert.Equal(s.T(), "HS256", s.receivedPairingData.Alg)
	assert.Equal(s.T(), "abcdef123456", s.receivedPairingData.Digest)
}

func (s *PairingProcessorSuite) Test_processPairingEntry_MissingMandatoryField() {
	// Missing txtvers (mandatory field)
	elements := map[string]string{
		"parType": "1",
		"forId":   "target-device-id",
		"trustId": "source-device-id",
	}

	remove := false

	// Register callback
	s.sut.RegisterPairingCallback(s.pairingCallback)

	// Process the pairing entry
	s.sut.processShipPairingMdnsEntry(elements, "servicename", remove)

	// Verify callback was NOT called due to missing mandatory field
	assert.Nil(s.T(), s.receivedPairingData)
}

func (s *PairingProcessorSuite) Test_processPairingEntry_InvalidTxtVers() {
	// Invalid txtvers value (must be "1")
	elements := map[string]string{
		"txtvers": "2",
		"parType": "1",
		"forId":   "target-device-id",
		"trustId": "source-device-id",
	}

	remove := false

	// Register callback
	s.sut.RegisterPairingCallback(s.pairingCallback)

	// Process the pairing entry
	s.sut.processShipPairingMdnsEntry(elements, "servicename", remove)

	// Verify callback was NOT called due to invalid txtvers
	assert.Nil(s.T(), s.receivedPairingData)
}

func (s *PairingProcessorSuite) Test_processPairingEntry_NoCallback() {
	// Valid pairing data but no callback registered
	elements := map[string]string{
		"txtvers": "1",
		"parType": "1",
		"forId":   "target-device-id",
		"trustId": "source-device-id",
	}

	remove := false

	// Don't register callback

	// Process the pairing entry - should not panic
	s.sut.processShipPairingMdnsEntry(elements, "servicename", remove)

	// No callback to verify, just ensure no panic
	assert.Nil(s.T(), s.receivedPairingData)
}

func (s *PairingProcessorSuite) Test_processPairingEntry_RemoveEntry() {
	// Test removal of pairing entry
	elements := map[string]string{
		"txtvers":    "1",
		"parType":    "1",
		"forId":      "target-device-id",
		"forPar":     "target-device-par",
		"trustId":    "source-device-id",
		"trustPar":   "source-device-par",
		"trustCurve": "P-256",
		"type":       "offer",
		"trustNonce": "123456",
		"alg":        "HS256",
		"digest":     "abcdef123456",
	}

	remove := true // Set remove flag

	// Register callback
	s.sut.RegisterPairingCallback(s.pairingCallback)

	// First add an entry so we can remove it
	s.sut.processShipPairingMdnsEntry(elements, "servicename", false) // Add the entry first
	assert.NotNil(s.T(), s.receivedPairingData)                       // Should be called for addition

	// Reset tracking
	s.receivedPairingData = nil

	// Process the pairing entry with remove flag
	s.sut.processShipPairingMdnsEntry(elements, "servicename", remove)

	// For removals, callback should be NOT called (new behavior)
	assert.Nil(s.T(), s.receivedPairingData)
}

func (s *PairingProcessorSuite) Test_RegisterPairingCallback() {
	// Test callback registration
	callbackCalled := false
	callback := func(data *api.ShipPairingTXT) bool {
		callbackCalled = true
		return true
	}

	// Register callback
	s.sut.RegisterPairingCallback(callback)

	// Process valid pairing entry
	elements := map[string]string{
		"txtvers":    "1",
		"parType":    "1",
		"forId":      "target-device-id",
		"forPar":     "target-device-par",
		"trustId":    "source-device-id",
		"trustPar":   "source-device-par",
		"trustCurve": "P-256",
		"type":       "offer",
		"trustNonce": "123456",
		"alg":        "HS256",
		"digest":     "abcdef123456",
	}

	s.sut.processShipPairingMdnsEntry(elements, "servicename", false)

	// Verify callback was called
	assert.True(s.T(), callbackCalled)
}

func (s *PairingProcessorSuite) Test_UnregisterPairingCallback() {
	// Test callback unregistration
	callbackCalled := false
	callback := func(data *api.ShipPairingTXT) bool {
		callbackCalled = true
		return true
	}

	// Register then unregister callback
	s.sut.RegisterPairingCallback(callback)
	s.sut.UnregisterPairingCallback()

	// Process valid pairing entry
	elements := map[string]string{
		"txtvers":    "1",
		"parType":    "1",
		"forId":      "target-device-id",
		"forPar":     "target-device-par",
		"trustId":    "source-device-id",
		"trustPar":   "source-device-par",
		"trustCurve": "P-256",
		"type":       "offer",
		"trustNonce": "123456",
		"alg":        "HS256",
		"digest":     "abcdef123456",
	}

	s.sut.processShipPairingMdnsEntry(elements, "servicename", false)

	// Verify callback was NOT called after unregistration
	assert.False(s.T(), callbackCalled)
}
