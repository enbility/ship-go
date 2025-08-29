package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

// ShipPairingTXTTestSuite contains tests for TXT record validation using SHIP specification data
// Uses exact test vectors from SHIP Pairing Service specification Annex A
type ShipPairingTXTTestSuite struct {
	suite.Suite
}

func TestShipPairingTXTTestSuite(t *testing.T) {
	suite.Run(t, new(ShipPairingTXTTestSuite))
}

// TestTXTRecordValidation_SpecificationTestVectors tests TXT record validation using exact Annex A data
func (suite *ShipPairingTXTTestSuite) TestTXTRecordValidation_SpecificationTestVectors() {
	// Test data from SHIP Pairing Service specification Annex A.3
	validTXTMap := map[string]string{
		"txtvers":    "1",
		"parType":    "fpSha256",
		"forId":      "i:983327_u:C8277H008F-3",                                          // devA SHIP ID from spec
		"forPar":     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943", // devA fingerprint from spec
		"trustId":    "i:46925_u:43652bk-2-gt1",                                          // devZ SHIP ID from spec
		"trustPar":   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4", // devZ fingerprint from spec
		"trustCurve": "secp256r1",                                                        // Curve from spec
		"type":       "addCu",                                                            // Command type from spec
		"trustNonce": "BDCEE427FA7208DF3C1F2A749BA6F4D4",                                 // devZ nonce from spec
		"alg":        "hmacSha256",                                                       // Algorithm from spec
		"digest":     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25", // Calculated digest from spec
	}

	// Test successful parsing
	txtRecord := &ShipPairingTXT{}
	err := txtRecord.FromMap(validTXTMap)
	assert.NoError(suite.T(), err, "parsing should succeed with valid specification data")

	// Verify all fields are correctly parsed
	assert.Equal(suite.T(), "1", txtRecord.TxtVers)
	assert.Equal(suite.T(), "fpSha256", txtRecord.ParType)
	assert.Equal(suite.T(), "i:983327_u:C8277H008F-3", txtRecord.ForId)
	assert.Equal(suite.T(), "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943", txtRecord.ForPar)
	assert.Equal(suite.T(), "i:46925_u:43652bk-2-gt1", txtRecord.TrustId)
	assert.Equal(suite.T(), "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4", txtRecord.TrustPar)
	assert.Equal(suite.T(), "secp256r1", txtRecord.TrustCurve)
	assert.Equal(suite.T(), "addCu", txtRecord.Type)
	assert.Equal(suite.T(), "BDCEE427FA7208DF3C1F2A749BA6F4D4", txtRecord.TrustNonce)
	assert.Equal(suite.T(), "hmacSha256", txtRecord.Alg)
	assert.Equal(suite.T(), "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25", txtRecord.Digest)

	// Test validation passes
	err = txtRecord.Validate()
	assert.NoError(suite.T(), err, "validation should pass for specification data")

	// Test round-trip conversion
	convertedMap := txtRecord.ToMap()
	for key, expectedValue := range validTXTMap {
		actualValue, exists := convertedMap[key]
		assert.True(suite.T(), exists, "key %s should exist after conversion", key)
		assert.Equal(suite.T(), expectedValue, actualValue, "value for key %s should match", key)
	}
}

// TestTXTRecordValidation_MissingMandatoryFields tests validation with missing mandatory fields
func (suite *ShipPairingTXTTestSuite) TestTXTRecordValidation_MissingMandatoryFields() {
	baseTXTMap := map[string]string{
		"txtvers":    "1",
		"parType":    "fpSha256",
		"forId":      "i:983327_u:C8277H008F-3",
		"forPar":     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		"trustId":    "i:46925_u:43652bk-2-gt1",
		"trustPar":   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		"trustCurve": "secp256r1",
		"type":       "addCu",
		"trustNonce": "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		"alg":        "hmacSha256",
		"digest":     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}

	// Test each mandatory field
	mandatoryFields := []string{"txtvers", "parType", "forId", "forPar", "trustId", "trustPar", "trustCurve", "type", "trustNonce", "alg", "digest"}

	for _, field := range mandatoryFields {
		suite.T().Run("missing_"+field, func(t *testing.T) {
			// Create copy without the field
			testMap := make(map[string]string)
			for k, v := range baseTXTMap {
				if k != field {
					testMap[k] = v
				}
			}

			txtRecord := &ShipPairingTXT{}
			err := txtRecord.FromMap(testMap)
			assert.Error(t, err, "parsing should fail when %s is missing", field)
			assert.Contains(t, err.Error(), "missing required TXT field: "+field)
		})
	}
}

// TestTXTRecordValidation_InvalidValues tests validation with invalid field values
func (suite *ShipPairingTXTTestSuite) TestTXTRecordValidation_InvalidValues() {
	tests := []struct {
		name       string
		field      string
		value      string
		shouldFail bool
	}{
		// txtvers validation
		{"invalid_txtvers", "txtvers", "2", true},
		{"invalid_txtvers_empty", "txtvers", "", true},
		{"valid_txtvers", "txtvers", "1", false},

		// Algorithm validation (via Validate())
		{"invalid_algorithm", "alg", "sha1", false}, // FromMap succeeds, Validate() fails
		{"invalid_algorithm_empty", "alg", "", false},
		{"valid_algorithm", "alg", "hmacSha256", false},

		// Parameter type validation (via Validate())
		{"invalid_partype", "parType", "md5", false}, // FromMap succeeds, Validate() fails
		{"valid_partype", "parType", "fpSha256", false},

		// Command type validation (via Validate())
		{"invalid_type", "type", "removeCu", false}, // FromMap succeeds, Validate() fails
		{"valid_type", "type", "addCu", false},

		// Trust curve validation (via Validate())
		{"invalid_curve", "trustCurve", "invalid", false}, // FromMap succeeds, Validate() fails
		{"valid_curve_secp256r1", "trustCurve", "secp256r1", false},
		{"valid_curve_brainpool256", "trustCurve", "brainpoolP256r1", false},
		{"valid_curve_brainpool384", "trustCurve", "brainpoolP384r1", false},
	}

	baseTXTMap := map[string]string{
		"txtvers":    "1",
		"parType":    "fpSha256",
		"forId":      "i:983327_u:C8277H008F-3",
		"forPar":     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		"trustId":    "i:46925_u:43652bk-2-gt1",
		"trustPar":   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		"trustCurve": "secp256r1",
		"type":       "addCu",
		"trustNonce": "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		"alg":        "hmacSha256",
		"digest":     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}

	for _, tt := range tests {
		suite.T().Run(tt.name, func(t *testing.T) {
			testMap := make(map[string]string)
			for k, v := range baseTXTMap {
				testMap[k] = v
			}
			testMap[tt.field] = tt.value

			txtRecord := &ShipPairingTXT{}
			err := txtRecord.FromMap(testMap)

			if tt.shouldFail {
				assert.Error(t, err, "FromMap should fail for %s", tt.name)
			} else {
				assert.NoError(t, err, "FromMap should succeed for %s", tt.name)

				// For cases that pass FromMap, test Validate()
				if err == nil {
					validationErr := txtRecord.Validate()
					if tt.field == "alg" && tt.value != "hmacSha256" {
						assert.Error(t, validationErr, "Validate should fail for invalid algorithm")
						assert.Contains(t, validationErr.Error(), "unsupported algorithm")
					} else if tt.field == "parType" && tt.value != "fpSha256" {
						assert.Error(t, validationErr, "Validate should fail for invalid parType")
						assert.Contains(t, validationErr.Error(), "unsupported parameter type")
					} else if tt.field == "type" && tt.value != "addCu" {
						assert.Error(t, validationErr, "Validate should fail for invalid type")
						assert.Contains(t, validationErr.Error(), "unsupported command type")
					} else if tt.field == "trustCurve" && tt.value == "invalid" {
						assert.Error(t, validationErr, "Validate should fail for invalid curve")
						assert.Contains(t, validationErr.Error(), "unsupported trust curve")
					} else {
						assert.NoError(t, validationErr, "Validate should succeed for valid %s", tt.field)
					}
				}
			}
		})
	}
}

// TestTXTRecordValidation_HexFieldFormats tests validation of hex field formats
func (suite *ShipPairingTXTTestSuite) TestTXTRecordValidation_HexFieldFormats() {
	baseTXTMap := map[string]string{
		"txtvers":    "1",
		"parType":    "fpSha256",
		"forId":      "i:983327_u:C8277H008F-3",
		"forPar":     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		"trustId":    "i:46925_u:43652bk-2-gt1",
		"trustPar":   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		"trustCurve": "secp256r1",
		"type":       "addCu",
		"trustNonce": "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		"alg":        "hmacSha256",
		"digest":     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}

	// Test hex field variations
	hexTests := []struct {
		name  string
		field string
		value string
		valid bool // Note: FromMap doesn't validate hex format, but the values should be consistent with spec
	}{
		// trustNonce tests (128-bit = 32 hex chars)
		{"trustnonce_correct_length", "trustNonce", "BDCEE427FA7208DF3C1F2A749BA6F4D4", true},
		{"trustnonce_lowercase", "trustNonce", "bdcee427fa7208df3c1f2a749ba6f4d4", true},   // FromMap accepts, validation elsewhere
		{"trustnonce_too_short", "trustNonce", "BDCEE427FA7208DF3C1F2A749BA6F4", true},     // FromMap accepts
		{"trustnonce_too_long", "trustNonce", "BDCEE427FA7208DF3C1F2A749BA6F4D44", true},   // FromMap accepts
		{"trustnonce_invalid_hex", "trustNonce", "GDCEE427FA7208DF3C1F2A749BA6F4D4", true}, // FromMap accepts

		// digest tests (256-bit = 64 hex chars)
		{"digest_correct_length", "digest", "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25", true},
		{"digest_lowercase", "digest", "bcbb62b2176da2cee545784ceb1f2a55e049451b12a549c98e8ca213f001da25", true},
		{"digest_too_short", "digest", "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA2", true},
		{"digest_too_long", "digest", "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA255", true},

		// forPar/trustPar tests (256-bit fingerprints = 64 hex chars)
		{"forpar_spec_value", "forPar", "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943", true},
		{"trustpar_spec_value", "trustPar", "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4", true},
	}

	for _, tt := range hexTests {
		suite.T().Run(tt.name, func(t *testing.T) {
			testMap := make(map[string]string)
			for k, v := range baseTXTMap {
				testMap[k] = v
			}
			testMap[tt.field] = tt.value

			txtRecord := &ShipPairingTXT{}
			err := txtRecord.FromMap(testMap)

			if tt.valid {
				assert.NoError(t, err, "FromMap should accept %s format for %s", tt.name, tt.field)
			} else {
				assert.Error(t, err, "FromMap should reject %s format for %s", tt.name, tt.field)
			}
		})
	}
}

// TestTXTRecordValidation_EdgeCases tests edge cases and boundary conditions
func (suite *ShipPairingTXTTestSuite) TestTXTRecordValidation_EdgeCases() {
	// Test with empty map
	emptyTXT := &ShipPairingTXT{}
	err := emptyTXT.FromMap(map[string]string{})
	assert.Error(suite.T(), err, "should fail with empty map")
	assert.Contains(suite.T(), err.Error(), "missing required TXT field")

	// Test with extra unknown fields (should be ignored per spec)
	extraFieldsMap := map[string]string{
		"txtvers":      "1",
		"parType":      "fpSha256",
		"forId":        "i:983327_u:C8277H008F-3",
		"forPar":       "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		"trustId":      "i:46925_u:43652bk-2-gt1",
		"trustPar":     "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		"trustCurve":   "secp256r1",
		"type":         "addCu",
		"trustNonce":   "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		"alg":          "hmacSha256",
		"digest":       "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
		"unknownField": "shouldBeIgnored",
		"extraData":    "alsoIgnored",
	}

	extraFieldsTXT := &ShipPairingTXT{}
	err = extraFieldsTXT.FromMap(extraFieldsMap)
	assert.NoError(suite.T(), err, "should succeed with extra unknown fields")

	// Verify known fields are parsed correctly despite extra fields
	assert.Equal(suite.T(), "1", extraFieldsTXT.TxtVers)
	assert.Equal(suite.T(), "fpSha256", extraFieldsTXT.ParType)
	assert.Equal(suite.T(), "i:983327_u:C8277H008F-3", extraFieldsTXT.ForId)

	// Test ToMap doesn't include unknown fields
	convertedMap := extraFieldsTXT.ToMap()
	_, hasUnknown := convertedMap["unknownField"]
	assert.False(suite.T(), hasUnknown, "ToMap should not include unknown fields")
	_, hasExtra := convertedMap["extraData"]
	assert.False(suite.T(), hasExtra, "ToMap should not include extra fields")
}

// TestTXTRecordValidation_AllSupportedCurves tests all supported elliptic curves
func (suite *ShipPairingTXTTestSuite) TestTXTRecordValidation_AllSupportedCurves() {
	supportedCurves := []string{
		"secp256r1",       // SHIP 1.0.x mandatory
		"brainpoolP256r1", // SHIP 1.1.x
		"brainpoolP384r1", // SHIP 1.1.x
	}

	baseTXTMap := map[string]string{
		"txtvers":    "1",
		"parType":    "fpSha256",
		"forId":      "i:983327_u:C8277H008F-3",
		"forPar":     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		"trustId":    "i:46925_u:43652bk-2-gt1",
		"trustPar":   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		"trustCurve": "secp256r1", // Will be overridden
		"type":       "addCu",
		"trustNonce": "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		"alg":        "hmacSha256",
		"digest":     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}

	for _, curve := range supportedCurves {
		suite.T().Run("curve_"+curve, func(t *testing.T) {
			testMap := make(map[string]string)
			for k, v := range baseTXTMap {
				testMap[k] = v
			}
			testMap["trustCurve"] = curve

			txtRecord := &ShipPairingTXT{}
			err := txtRecord.FromMap(testMap)
			assert.NoError(t, err, "should parse curve %s", curve)

			err = txtRecord.Validate()
			assert.NoError(t, err, "should validate curve %s", curve)

			assert.Equal(t, curve, txtRecord.TrustCurve)
		})
	}
}

// TestTXTRecordToMapRoundTrip tests bidirectional conversion consistency
func (suite *ShipPairingTXTTestSuite) TestTXTRecordToMapRoundTrip() {
	originalTXT := &ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "i:983327_u:C8277H008F-3",
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		TrustId:    "i:46925_u:43652bk-2-gt1",
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		Alg:        "hmacSha256",
		Digest:     "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25",
	}

	// Convert to map
	txtMap := originalTXT.ToMap()

	// Convert back to struct
	roundTripTXT := &ShipPairingTXT{}
	err := roundTripTXT.FromMap(txtMap)
	assert.NoError(suite.T(), err, "round trip conversion should succeed")

	// Compare all fields
	assert.Equal(suite.T(), originalTXT.TxtVers, roundTripTXT.TxtVers)
	assert.Equal(suite.T(), originalTXT.ParType, roundTripTXT.ParType)
	assert.Equal(suite.T(), originalTXT.ForId, roundTripTXT.ForId)
	assert.Equal(suite.T(), originalTXT.ForPar, roundTripTXT.ForPar)
	assert.Equal(suite.T(), originalTXT.TrustId, roundTripTXT.TrustId)
	assert.Equal(suite.T(), originalTXT.TrustPar, roundTripTXT.TrustPar)
	assert.Equal(suite.T(), originalTXT.TrustCurve, roundTripTXT.TrustCurve)
	assert.Equal(suite.T(), originalTXT.Type, roundTripTXT.Type)
	assert.Equal(suite.T(), originalTXT.TrustNonce, roundTripTXT.TrustNonce)
	assert.Equal(suite.T(), originalTXT.Alg, roundTripTXT.Alg)
	assert.Equal(suite.T(), originalTXT.Digest, roundTripTXT.Digest)

	// Convert back to map and compare
	finalMap := roundTripTXT.ToMap()
	for key, expectedValue := range txtMap {
		actualValue, exists := finalMap[key]
		assert.True(suite.T(), exists, "key %s should exist after round trip", key)
		assert.Equal(suite.T(), expectedValue, actualValue, "value for key %s should match after round trip", key)
	}
}
