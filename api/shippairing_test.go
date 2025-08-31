package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

// ShipPairingTestSuite contains tests for SHIP pairing service interfaces and types
type ShipPairingTestSuite struct {
	suite.Suite
}

func TestShipPairingTestSuite(t *testing.T) {
	suite.Run(t, new(ShipPairingTestSuite))
}

/* PairingSecret Tests */

func (suite *ShipPairingTestSuite) TestPairingSecret_Clear() {
	// Test that Clear() zeros all bytes
	secret := PairingSecret([]byte{0x01, 0x02, 0x03, 0x04})
	secret.Clear()

	for i, b := range secret {
		assert.Equal(suite.T(), byte(0), b, "byte at index %d should be zero after Clear()", i)
	}
}

func (suite *ShipPairingTestSuite) TestPairingSecret_String() {
	// Test that String() returns [REDACTED] to prevent logging secrets
	secret := PairingSecret([]byte("super-secret-key"))
	assert.Equal(suite.T(), "[REDACTED]", secret.String())
}

func (suite *ShipPairingTestSuite) TestPairingSecret_Equal() {
	tests := []struct {
		name     string
		secret1  PairingSecret
		secret2  PairingSecret
		expected bool
	}{
		{
			name:     "identical secrets",
			secret1:  PairingSecret([]byte("same-secret-1234")),
			secret2:  PairingSecret([]byte("same-secret-1234")),
			expected: true,
		},
		{
			name:     "different secrets",
			secret1:  PairingSecret([]byte("secret-one-1234!")),
			secret2:  PairingSecret([]byte("secret-two-1234!")),
			expected: false,
		},
		{
			name:     "different lengths",
			secret1:  PairingSecret([]byte("short")),
			secret2:  PairingSecret([]byte("much-longer-secret")),
			expected: false,
		},
		{
			name:     "empty secrets",
			secret1:  PairingSecret([]byte{}),
			secret2:  PairingSecret([]byte{}),
			expected: true,
		},
		{
			name:     "one empty one not",
			secret1:  PairingSecret([]byte{}),
			secret2:  PairingSecret([]byte("not-empty")),
			expected: false,
		},
	}

	for _, tt := range tests {
		suite.T().Run(tt.name, func(t *testing.T) {
			result := tt.secret1.Equal(tt.secret2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

/* PairingStateDetail Tests */

func (suite *ShipPairingTestSuite) TestPairingStateDetail_ThreadSafety() {
	// Test thread-safe state management following ConnectionStateDetail pattern
	detail := NewPairingStateDetail(PairingStateNone, nil)

	// Initial state
	assert.Equal(suite.T(), PairingStateNone, detail.State())
	assert.Nil(suite.T(), detail.Error())

	// State change
	detail.SetState(PairingStateAnnouncing)
	assert.Equal(suite.T(), PairingStateAnnouncing, detail.State())

	// Error setting
	testErr := NewPairingValidationError("test error")
	detail.SetError(testErr)
	assert.Equal(suite.T(), testErr, detail.Error())

	// Timestamp updates
	initialTime := detail.Timestamp()
	time.Sleep(1 * time.Millisecond)
	detail.SetState(PairingStateCompleted)
	newTime := detail.Timestamp()
	assert.True(suite.T(), newTime.After(initialTime), "timestamp should update on state change")
}

/* ShipPairingTXT Tests */

func (suite *ShipPairingTestSuite) TestShipPairingTXT_ToMap() {
	// Test TXT record conversion per SHIP spec Table 1
	txt := &ShipPairingTXT{
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

	txtMap := txt.ToMap()

	// Verify all mandatory fields are present per spec Table 1
	requiredFields := []string{"txtvers", "parType", "forId", "forPar", "trustId",
		"trustPar", "trustCurve", "type", "trustNonce", "alg", "digest"}

	for _, field := range requiredFields {
		assert.Contains(suite.T(), txtMap, field, "TXT map must contain field: %s", field)
		assert.NotEmpty(suite.T(), txtMap[field], "TXT field must not be empty: %s", field)
	}

	// Verify specific values match struct
	assert.Equal(suite.T(), txt.TxtVers, txtMap["txtvers"])
	assert.Equal(suite.T(), txt.ParType, txtMap["parType"])
	assert.Equal(suite.T(), txt.ForId, txtMap["forId"])
	assert.Equal(suite.T(), txt.Digest, txtMap["digest"])
}

func (suite *ShipPairingTestSuite) TestShipPairingTXT_FromMap_Valid() {
	// Test parsing valid TXT record
	txtMap := map[string]string{
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

	var txt ShipPairingTXT
	err := txt.FromMap(txtMap)

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), "1", txt.TxtVers)
	assert.Equal(suite.T(), "fpSha256", txt.ParType)
	assert.Equal(suite.T(), "i:983327_u:C8277H008F-3", txt.ForId)
	assert.Equal(suite.T(), "hmacSha256", txt.Alg)
}

func (suite *ShipPairingTestSuite) TestShipPairingTXT_FromMap_MissingFields() {
	// Test error handling for missing required fields
	tests := []struct {
		name         string
		missingField string
	}{
		{"missing txtvers", "txtvers"},
		{"missing parType", "parType"},
		{"missing forId", "forId"},
		{"missing digest", "digest"},
		{"missing alg", "alg"},
	}

	baseTxtMap := map[string]string{
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
			txtMap := make(map[string]string)
			for k, v := range baseTxtMap {
				if k != tt.missingField {
					txtMap[k] = v
				}
			}

			var txt ShipPairingTXT
			err := txt.FromMap(txtMap)

			assert.Error(t, err)
			assert.IsType(t, &PairingValidationError{}, err)
			assert.Contains(t, err.Error(), tt.missingField)
		})
	}
}

func (suite *ShipPairingTestSuite) TestShipPairingTXT_FromMap_InvalidTxtVers() {
	txtMap := map[string]string{
		"txtvers":    "2", // Invalid version
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

	var txt ShipPairingTXT
	err := txt.FromMap(txtMap)

	assert.Error(suite.T(), err)
	assert.Contains(suite.T(), err.Error(), "txtvers")
}

func (suite *ShipPairingTestSuite) TestShipPairingTXT_Validate() {
	// Test validation of TXT record fields
	tests := []struct {
		name      string
		txt       ShipPairingTXT
		expectErr bool
		errMsg    string
	}{
		{
			name: "valid TXT record",
			txt: ShipPairingTXT{
				Alg:        "hmacSha256",
				ParType:    "fpSha256",
				Type:       "addCu",
				TrustCurve: "secp256r1",
			},
			expectErr: false,
		},
		{
			name: "invalid algorithm",
			txt: ShipPairingTXT{
				Alg:        "sha1",
				ParType:    "fpSha256",
				Type:       "addCu",
				TrustCurve: "secp256r1",
			},
			expectErr: true,
			errMsg:    "algorithm",
		},
		{
			name: "invalid parameter type",
			txt: ShipPairingTXT{
				Alg:        "hmacSha256",
				ParType:    "ski",
				Type:       "addCu",
				TrustCurve: "secp256r1",
			},
			expectErr: true,
			errMsg:    "parameter type",
		},
		{
			name: "invalid command type",
			txt: ShipPairingTXT{
				Alg:        "hmacSha256",
				ParType:    "fpSha256",
				Type:       "removeCu",
				TrustCurve: "secp256r1",
			},
			expectErr: true,
			errMsg:    "command type",
		},
		{
			name: "invalid trust curve",
			txt: ShipPairingTXT{
				Alg:        "hmacSha256",
				ParType:    "fpSha256",
				Type:       "addCu",
				TrustCurve: "secp384r1",
			},
			expectErr: true,
			errMsg:    "trust curve",
		},
		{
			name: "valid brainpool curve",
			txt: ShipPairingTXT{
				Alg:        "hmacSha256",
				ParType:    "fpSha256",
				Type:       "addCu",
				TrustCurve: "brainpoolP256r1",
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		suite.T().Run(tt.name, func(t *testing.T) {
			err := tt.txt.Validate()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

/* Custom Error Tests */

func (suite *ShipPairingTestSuite) TestPairingValidationError() {
	// Test custom error type
	err := NewPairingValidationError("test validation error")
	assert.Equal(suite.T(), "pairing validation error: test validation error", err.Error())

	fieldErr := NewPairingFieldValidationError("testField", "field specific error")
	assert.Equal(suite.T(), "pairing validation error [testField]: field specific error", fieldErr.Error())
}

/* Roundtrip Tests */

func (suite *ShipPairingTestSuite) TestShipPairingTXT_Roundtrip() {
	// Test TXT record conversion roundtrip (ToMap -> FromMap)
	original := &ShipPairingTXT{
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
	txtMap := original.ToMap()

	// Convert back to struct
	var reconstructed ShipPairingTXT
	err := reconstructed.FromMap(txtMap)

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), original.TxtVers, reconstructed.TxtVers)
	assert.Equal(suite.T(), original.ParType, reconstructed.ParType)
	assert.Equal(suite.T(), original.ForId, reconstructed.ForId)
	assert.Equal(suite.T(), original.ForPar, reconstructed.ForPar)
	assert.Equal(suite.T(), original.TrustId, reconstructed.TrustId)
	assert.Equal(suite.T(), original.TrustPar, reconstructed.TrustPar)
	assert.Equal(suite.T(), original.TrustCurve, reconstructed.TrustCurve)
	assert.Equal(suite.T(), original.Type, reconstructed.Type)
	assert.Equal(suite.T(), original.TrustNonce, reconstructed.TrustNonce)
	assert.Equal(suite.T(), original.Alg, reconstructed.Alg)
	assert.Equal(suite.T(), original.Digest, reconstructed.Digest)
}
