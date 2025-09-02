package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestServiceIdentity(t *testing.T) {
	suite.Run(t, new(ServiceIdentitySuite))
}

type ServiceIdentitySuite struct {
	suite.Suite
}

func (s *ServiceIdentitySuite) TestNewServiceIdentity_ValidParameters() {
	// Test: Constructor with all valid parameters
	ski := "test-ski"
	fingerprint := "test-fingerprint"
	shipID := "test-ship-id"

	identity := NewServiceIdentity(ski, fingerprint, shipID)

	assert.False(s.T(), identity.IsZero())
	assert.Equal(s.T(), "testski", identity.SKI) // SKI should be normalized
	assert.Equal(s.T(), fingerprint, identity.Fingerprint)
	assert.Equal(s.T(), shipID, identity.ShipID)
	assert.Equal(s.T(), PairingTypeDefault, identity.PairingType) // Should default to PairingTypeDefault
	assert.Empty(s.T(), identity.IPv4)                            // IPv4 should be empty by default
}

func (s *ServiceIdentitySuite) TestNewServiceIdentity_MinimalParameters() {
	// Test: Constructor with minimal required parameters (SKI only)
	ski := "test-ski"

	identity := NewServiceIdentity(ski, "", "")

	assert.False(s.T(), identity.IsZero())
	assert.Equal(s.T(), "testski", identity.SKI) // SKI should be normalized
	assert.Empty(s.T(), identity.Fingerprint)
	assert.Empty(s.T(), identity.ShipID)
	assert.Equal(s.T(), PairingTypeDefault, identity.PairingType)
	assert.Empty(s.T(), identity.IPv4)
}

func (s *ServiceIdentitySuite) TestNewServiceIdentity_FingerprintOnly() {
	// Test: Constructor with fingerprint and shipID (no SKI)
	fingerprint := "test-fingerprint"
	shipID := "test-ship-id"

	identity := NewServiceIdentity("", fingerprint, shipID)

	assert.False(s.T(), identity.IsZero())
	assert.Empty(s.T(), identity.SKI)
	assert.Equal(s.T(), fingerprint, identity.Fingerprint)
	assert.Equal(s.T(), shipID, identity.ShipID)
	assert.Equal(s.T(), PairingTypeDefault, identity.PairingType)
	assert.Empty(s.T(), identity.IPv4)
}

func (s *ServiceIdentitySuite) TestNewServiceIdentity_InvalidParameters() {
	// Test: Constructor should return nil for invalid parameters
	// Based on current ServiceDetails validation logic

	// Empty SKI and fingerprint should return zero value
	identity := NewServiceIdentity("", "", "")
	assert.True(s.T(), identity.IsZero())

	// Empty SKI with fingerprint but no shipID should return zero value
	identity = NewServiceIdentity("", "fingerprint", "")
	assert.True(s.T(), identity.IsZero())
}

func (s *ServiceIdentitySuite) TestNewServiceIdentity_SKINormalization() {
	// Test: SKI should be normalized (current ServiceDetails behavior)
	unnormalizedSKI := "Test-SKI-123"

	identity := NewServiceIdentity(unnormalizedSKI, "", "")

	assert.False(s.T(), identity.IsZero())
	// We expect the SKI to be normalized (implementation will handle this)
	// The actual normalization logic will be implemented in the constructor
	assert.NotEmpty(s.T(), identity.SKI)
}

func (s *ServiceIdentitySuite) TestServiceIdentity_FieldAccess() {
	// Test: Direct field access (public fields)
	identity := &ServiceIdentity{
		SKI:         "direct-ski",
		Fingerprint: "direct-fingerprint",
		ShipID:      "direct-shipid",
		PairingType: PairingTypeAddCu,
		IPv4:        "192.168.1.1",
	}

	// Should be able to read all fields directly
	assert.Equal(s.T(), "direct-ski", identity.SKI)
	assert.Equal(s.T(), "direct-fingerprint", identity.Fingerprint)
	assert.Equal(s.T(), "direct-shipid", identity.ShipID)
	assert.Equal(s.T(), PairingTypeAddCu, identity.PairingType)
	assert.Equal(s.T(), "192.168.1.1", identity.IPv4)
}

func (s *ServiceIdentitySuite) TestServiceIdentity_FieldModification() {
	// Test: Direct field modification (public fields)
	identity := NewServiceIdentity("original-ski", "original-fingerprint", "original-shipid")

	// Should be able to modify all fields directly
	identity.SKI = "modified-ski"
	identity.Fingerprint = "modified-fingerprint"
	identity.ShipID = "modified-shipid"
	identity.PairingType = PairingTypeAddCu
	identity.IPv4 = "10.0.0.1"

	// Verify modifications
	assert.Equal(s.T(), "modified-ski", identity.SKI)
	assert.Equal(s.T(), "modified-fingerprint", identity.Fingerprint)
	assert.Equal(s.T(), "modified-shipid", identity.ShipID)
	assert.Equal(s.T(), PairingTypeAddCu, identity.PairingType)
	assert.Equal(s.T(), "10.0.0.1", identity.IPv4)
}

func (s *ServiceIdentitySuite) TestServiceIdentity_ZeroValue() {
	// Test: Zero value behavior
	var identity ServiceIdentity

	assert.Empty(s.T(), identity.SKI)
	assert.Empty(s.T(), identity.Fingerprint)
	assert.Empty(s.T(), identity.ShipID)
	assert.Equal(s.T(), PairingTypeDefault, identity.PairingType) // Should be zero value (0)
	assert.Empty(s.T(), identity.IPv4)
}

func (s *ServiceIdentitySuite) TestServiceIdentity_JSONSerialization() {
	// Test: JSON marshal and unmarshal with all fields
	original := &ServiceIdentity{
		SKI:         "json-ski",
		Fingerprint: "json-fingerprint",
		ShipID:      "json-shipid",
		PairingType: PairingTypeAddCu,
		IPv4:        "203.0.113.1",
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(original)
	assert.NoError(s.T(), err)
	assert.NotEmpty(s.T(), jsonData)

	// Verify JSON contains expected fields with correct names
	expectedJSON := `{"ski":"json-ski","fingerprint":"json-fingerprint","shipID":"json-shipid","pairingType":1,"ipv4":"203.0.113.1"}`
	assert.JSONEq(s.T(), expectedJSON, string(jsonData))

	// Unmarshal back to struct
	var unmarshaled ServiceIdentity
	err = json.Unmarshal(jsonData, &unmarshaled)
	assert.NoError(s.T(), err)

	// Verify all fields match
	assert.Equal(s.T(), original.SKI, unmarshaled.SKI)
	assert.Equal(s.T(), original.Fingerprint, unmarshaled.Fingerprint)
	assert.Equal(s.T(), original.ShipID, unmarshaled.ShipID)
	assert.Equal(s.T(), original.PairingType, unmarshaled.PairingType)
	assert.Equal(s.T(), original.IPv4, unmarshaled.IPv4)
}

func (s *ServiceIdentitySuite) TestServiceIdentity_JSONSerialization_OmitEmpty() {
	// Test: JSON serialization with omitempty for IPv4 field
	identity := &ServiceIdentity{
		SKI:         "omit-ski",
		Fingerprint: "omit-fingerprint",
		ShipID:      "omit-shipid",
		PairingType: PairingTypeDefault,
		IPv4:        "", // Empty IPv4 should be omitted
	}

	jsonData, err := json.Marshal(identity)
	assert.NoError(s.T(), err)

	// Verify IPv4 is omitted when empty
	expectedJSON := `{"ski":"omit-ski","fingerprint":"omit-fingerprint","shipID":"omit-shipid","pairingType":0}`
	assert.JSONEq(s.T(), expectedJSON, string(jsonData))
}

func (s *ServiceIdentitySuite) TestServiceIdentity_JSONDeserialization_PartialData() {
	// Test: Deserialize JSON with missing optional fields
	jsonData := `{"ski":"partial-ski","fingerprint":"partial-fingerprint","shipID":"partial-shipid"}`

	var identity ServiceIdentity
	err := json.Unmarshal([]byte(jsonData), &identity)
	assert.NoError(s.T(), err)

	assert.Equal(s.T(), "partial-ski", identity.SKI)
	assert.Equal(s.T(), "partial-fingerprint", identity.Fingerprint)
	assert.Equal(s.T(), "partial-shipid", identity.ShipID)
	assert.Equal(s.T(), PairingTypeDefault, identity.PairingType) // Should default to 0
	assert.Empty(s.T(), identity.IPv4)                            // Should be empty
}

func (s *ServiceIdentitySuite) TestServiceIdentity_Equals() {
	// Test: Equality method
	identity1 := &ServiceIdentity{
		SKI:         "equal-ski",
		Fingerprint: "equal-fingerprint",
		ShipID:      "equal-shipid",
		PairingType: PairingTypeAddCu,
		IPv4:        "192.0.2.1",
	}

	identity2 := &ServiceIdentity{
		SKI:         "equal-ski",
		Fingerprint: "equal-fingerprint",
		ShipID:      "equal-shipid",
		PairingType: PairingTypeAddCu,
		IPv4:        "192.0.2.1",
	}

	identity3 := &ServiceIdentity{
		SKI:         "different-ski",
		Fingerprint: "equal-fingerprint",
		ShipID:      "equal-shipid",
		PairingType: PairingTypeAddCu,
		IPv4:        "192.0.2.1",
	}

	// Should be equal (use Go's built-in struct equality)
	assert.Equal(s.T(), *identity1, *identity2)
	assert.Equal(s.T(), *identity2, *identity1)

	// Should not be equal
	assert.NotEqual(s.T(), *identity1, *identity3)
	assert.NotEqual(s.T(), *identity3, *identity1)

	// Test with zero value
	var zeroIdentity ServiceIdentity
	assert.NotEqual(s.T(), *identity1, zeroIdentity)
}

func (s *ServiceIdentitySuite) TestServiceIdentity_Copy() {
	// Test: Copy method
	original := &ServiceIdentity{
		SKI:         "copy-ski",
		Fingerprint: "copy-fingerprint",
		ShipID:      "copy-shipid",
		PairingType: PairingTypeAddCu,
		IPv4:        "203.0.113.1",
	}

	// Create copy using Go's built-in assignment
	copied := *original

	// Should have same values (use Go's built-in struct equality)
	assert.Equal(s.T(), *original, copied)

	// Modifying copy should not affect original (value semantics)
	copied.SKI = "modified-ski"
	assert.NotEqual(s.T(), original.SKI, copied.SKI)
	assert.Equal(s.T(), "copy-ski", original.SKI)
	assert.Equal(s.T(), "modified-ski", copied.SKI)
}

func (s *ServiceIdentitySuite) TestServiceIdentity_String() {
	// Test: String representation method
	identity := &ServiceIdentity{
		SKI:         "string-ski",
		Fingerprint: "string-fingerprint",
		ShipID:      "string-shipid",
		PairingType: PairingTypeAddCu,
		IPv4:        "198.51.100.1",
	}

	str := identity.String()
	assert.NotEmpty(s.T(), str)

	// Should contain key identifying information
	assert.Contains(s.T(), str, "string-ski")
	assert.Contains(s.T(), str, "string-shipid")

	// Test with minimal data
	minimalIdentity := &ServiceIdentity{
		SKI: "minimal-ski",
	}

	minimalStr := minimalIdentity.String()
	assert.NotEmpty(s.T(), minimalStr)
	assert.Contains(s.T(), minimalStr, "minimal-ski")
}
