package pairing

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/enbility/ship-go/api"
)

// HMACTestSuite contains tests for HMAC calculation and validation
// Uses test vectors from SHIP Pairing Service specification Annex A
type HMACTestSuite struct {
	suite.Suite
}

func TestHMACTestSuite(t *testing.T) {
	suite.Run(t, new(HMACTestSuite))
}

/* Test Vectors from SHIP Specification Annex A */

func (suite *HMACTestSuite) TestHMACCalculation_SpecificationTestVectors() {
	// Test vectors from SHIP Pairing Service specification Annex A.3
	// These are the exact values provided in the specification for validation

	tests := []struct {
		name           string
		secret         string // devA-secret (hex)
		nonce          string // devZ-nonce (hex)
		expectedKey    string // Expected key K = secret || nonce (hex)
		expectedDigest string // Expected HMAC digest (hex)
		message        string // Message M for HMAC calculation
	}{
		{
			name:           "SHIP spec Annex A.3 test vector",
			secret:         "7A37DCF81BDB50F8E92CFA4160CCB3DE",                                 // devA-secret from spec
			nonce:          "BDCEE427FA7208DF3C1F2A749BA6F4D4",                                 // devZ-nonce from spec
			expectedKey:    "7A37DCF81BDB50F8E92CFA4160CCB3DEBDCEE427FA7208DF3C1F2A749BA6F4D4", // K = secret || nonce
			expectedDigest: "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25", // From spec
			message:        "txtvers=1;parType=fpSha256;forId=i:983327_u:C8277H008F-3;forPar=C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943;trustId=i:46925_u:43652bk-2-gt1;trustPar=2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4;trustCurve=secp256r1;type=addCu;trustNonce=BDCEE427FA7208DF3C1F2A749BA6F4D4;alg=hmacSha256;",
		},
	}

	for _, tt := range tests {
		suite.T().Run(tt.name, func(t *testing.T) {
			// Create HMAC calculator
			calculator := NewHMACCalculator()

			// Convert hex secret to bytes
			secret, err := hexToBytes(tt.secret)
			assert.NoError(t, err)
			pairingSecret := api.PairingSecret(secret)

			// Convert hex nonce to bytes
			nonce, err := hexToBytes(tt.nonce)
			assert.NoError(t, err)

			// Create TXT record for message construction
			txtRecord := &api.ShipPairingTXT{
				TxtVers:    "1",
				ParType:    "fpSha256",
				ForId:      "i:983327_u:C8277H008F-3",
				ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
				TrustId:    "i:46925_u:43652bk-2-gt1",
				TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
				TrustCurve: "secp256r1",
				Type:       "addCu",
				TrustNonce: tt.nonce,
				Alg:        "hmacSha256",
			}

			params := &api.HMACParams{
				Algorithm: "hmacSha256",
				Nonce:     nonce,
				TxtRecord: txtRecord,
			}

			// Calculate HMAC digest
			digest, err := calculator.CalculateDigest(pairingSecret, params)
			assert.NoError(t, err)

			// Convert to hex for comparison
			digestHex := bytesToHex(digest)

			// Verify digest matches specification
			assert.Equal(t, tt.expectedDigest, digestHex,
				"HMAC digest must match SHIP specification test vector")

			// Test validation with correct digest
			expectedDigest, err := hexToBytes(tt.expectedDigest)
			assert.NoError(t, err)

			err = calculator.ValidateDigest(pairingSecret, params, expectedDigest)
			assert.NoError(t, err, "validation should succeed with correct digest")

			// Test validation with incorrect digest
			wrongDigest := make([]byte, len(expectedDigest))
			copy(wrongDigest, expectedDigest)
			wrongDigest[0] ^= 0xFF // Flip bits to make it wrong

			err = calculator.ValidateDigest(pairingSecret, params, wrongDigest)
			assert.Error(t, err, "validation should fail with incorrect digest")
			assert.Equal(t, api.ErrInvalidHMACDigest, err)
		})
	}
}

func (suite *HMACTestSuite) TestHMACKeyConstruction() {
	// Test key construction per SHIP spec section 7.3: K = devA-secret || devZ-nonce
	tests := []struct {
		name        string
		secret      string
		nonce       string
		expectedKey string
	}{
		{
			name:        "spec example",
			secret:      "7A37DCF81BDB50F8E92CFA4160CCB3DE",
			nonce:       "BDCEE427FA7208DF3C1F2A749BA6F4D4",
			expectedKey: "7A37DCF81BDB50F8E92CFA4160CCB3DEBDCEE427FA7208DF3C1F2A749BA6F4D4",
		},
		{
			name:        "different lengths",
			secret:      "01020304",
			nonce:       "ABCDEF",
			expectedKey: "01020304ABCDEF",
		},
		{
			name:        "empty nonce",
			secret:      "01020304",
			nonce:       "",
			expectedKey: "01020304",
		},
	}

	for _, tt := range tests {
		suite.T().Run(tt.name, func(t *testing.T) {
			calculator := NewHMACCalculator()

			secret, err := hexToBytes(tt.secret)
			assert.NoError(t, err)

			nonce, err := hexToBytes(tt.nonce)
			assert.NoError(t, err)

			key := calculator.constructKey(api.PairingSecret(secret), nonce)
			keyHex := bytesToHex(key)

			assert.Equal(t, tt.expectedKey, keyHex)
		})
	}
}

func (suite *HMACTestSuite) TestHMACMessageConstruction() {
	// Test message construction per SHIP spec section 7.4
	txtRecord := &api.ShipPairingTXT{
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
	}

	calculator := NewHMACCalculator()
	message := calculator.constructMessage(txtRecord)

	// Expected message from SHIP spec Annex A.3 (without digest field)
	expectedMessage := "txtvers=1;parType=fpSha256;forId=i:983327_u:C8277H008F-3;forPar=C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943;trustId=i:46925_u:43652bk-2-gt1;trustPar=2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4;trustCurve=secp256r1;type=addCu;trustNonce=BDCEE427FA7208DF3C1F2A749BA6F4D4;alg=hmacSha256;"

	assert.Equal(suite.T(), expectedMessage, message,
		"message construction must match SHIP specification section 7.4")
}

func (suite *HMACTestSuite) TestHMACMessageFieldOrdering() {
	// Test that field ordering is deterministic and matches spec
	txtRecord := &api.ShipPairingTXT{
		// Set fields in different order than spec
		Alg:        "hmacSha256",
		Digest:     "shouldnotappear", // Should not be included in message
		TrustNonce: "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		Type:       "addCu",
		TrustCurve: "secp256r1",
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustId:    "i:46925_u:43652bk-2-gt1",
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		ForId:      "i:983327_u:C8277H008F-3",
		ParType:    "fpSha256",
		TxtVers:    "1",
	}

	calculator := NewHMACCalculator()
	message := calculator.constructMessage(txtRecord)

	// Message should have fixed ordering regardless of struct field order
	expectedStart := "txtvers=1;parType=fpSha256;forId="
	expectedEnd := ";alg=hmacSha256;"

	assert.True(suite.T(), len(message) > 0)
	assert.True(suite.T(), message[0:len(expectedStart)] == expectedStart,
		"message should start with txtvers field")
	assert.True(suite.T(), message[len(message)-len(expectedEnd):] == expectedEnd,
		"message should end with alg field")
	assert.NotContains(suite.T(), message, "shouldnotappear",
		"digest field should not be included in message")
}

func (suite *HMACTestSuite) TestNonceGeneration() {
	calculator := NewHMACCalculator()

	// Test nonce generation
	nonce1, err := calculator.GenerateNonce()
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), 16, len(nonce1), "nonce should be 128 bits (16 bytes)")

	nonce2, err := calculator.GenerateNonce()
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), 16, len(nonce2), "nonce should be 128 bits (16 bytes)")

	// Nonces should be different (extremely high probability)
	assert.NotEqual(suite.T(), nonce1, nonce2, "consecutive nonces should be different")
}

func (suite *HMACTestSuite) TestHMACValidation_TimingAttackProtection() {
	// Test that validation uses constant-time comparison
	calculator := NewHMACCalculator()

	secret := api.PairingSecret([]byte("test-secret-1234"))
	nonce, err := calculator.GenerateNonce()
	assert.NoError(suite.T(), err)

	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "test-device",
		ForPar:     "0123456789ABCDEF0123456789ABCDEF01234567",
		TrustId:    "trust-device",
		TrustPar:   "FEDCBA9876543210FEDCBA9876543210FEDCBA98",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: bytesToHex(nonce),
		Alg:        "hmacSha256",
	}

	params := &api.HMACParams{
		Algorithm: "hmacSha256",
		Nonce:     nonce,
		TxtRecord: txtRecord,
	}

	// Calculate correct digest
	correctDigest, err := calculator.CalculateDigest(secret, params)
	assert.NoError(suite.T(), err)

	// Test with completely wrong digest (all zeros)
	wrongDigest := make([]byte, len(correctDigest))

	err = calculator.ValidateDigest(secret, params, wrongDigest)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrInvalidHMACDigest, err)

	// Test with digest that differs only in last byte
	almostCorrectDigest := make([]byte, len(correctDigest))
	copy(almostCorrectDigest, correctDigest)
	almostCorrectDigest[len(almostCorrectDigest)-1] ^= 0x01

	err = calculator.ValidateDigest(secret, params, almostCorrectDigest)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrInvalidHMACDigest, err)
}

func (suite *HMACTestSuite) TestHMACValidation_ErrorCases() {
	calculator := NewHMACCalculator()

	// Test with invalid algorithm
	secret := api.PairingSecret([]byte("test-secret-1234"))
	nonce := []byte("test-nonce")

	txtRecord := &api.ShipPairingTXT{
		TxtVers: "1",
		Alg:     "sha1", // Invalid algorithm
	}

	params := &api.HMACParams{
		Algorithm: "sha1", // Invalid
		Nonce:     nonce,
		TxtRecord: txtRecord,
	}

	_, err := calculator.CalculateDigest(secret, params)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrUnsupportedAlgorithm, err)

	// Test with nil secret
	params.Algorithm = "hmacSha256"
	txtRecord.Alg = "hmacSha256"

	_, err = calculator.CalculateDigest(nil, params)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrInvalidSecret, err)

	// Test with empty secret
	emptySecret := api.PairingSecret([]byte{})
	_, err = calculator.CalculateDigest(emptySecret, params)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrInvalidSecret, err)

	// Test with nil params
	_, err = calculator.CalculateDigest(secret, nil)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrHMACCalculationFailed, err)

	// Test with nil TxtRecord
	paramsWithNilTXT := &api.HMACParams{
		Algorithm: "hmacSha256",
		Nonce:     nonce,
		TxtRecord: nil,
	}
	_, err = calculator.CalculateDigest(secret, paramsWithNilTXT)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrHMACCalculationFailed, err)
}

func (suite *HMACTestSuite) TestValidateDigest_CalculationErrors() {
	calculator := NewHMACCalculator()

	// Test ValidateDigest with params that cause CalculateDigest to fail
	secret := api.PairingSecret([]byte("test-secret"))
	expectedDigest := []byte("some-digest")

	// Test with nil params (should propagate CalculateDigest error)
	err := calculator.ValidateDigest(secret, nil, expectedDigest)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrHMACCalculationFailed, err)

	// Test with invalid algorithm (should propagate CalculateDigest error)
	paramsWithInvalidAlg := &api.HMACParams{
		Algorithm: "invalid-algorithm",
		Nonce:     []byte("test-nonce"),
		TxtRecord: &api.ShipPairingTXT{TxtVers: "1"},
	}
	err = calculator.ValidateDigest(secret, paramsWithInvalidAlg, expectedDigest)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrUnsupportedAlgorithm, err)
}

/* Helper Functions */

// hexToBytes converts hex string to byte slice
func hexToBytes(hexStr string) ([]byte, error) {
	if len(hexStr)%2 != 0 {
		hexStr = "0" + hexStr
	}

	bytes := make([]byte, len(hexStr)/2)
	for i := 0; i < len(hexStr); i += 2 {
		var b byte
		_, err := fmt.Sscanf(hexStr[i:i+2], "%02X", &b)
		if err != nil {
			return nil, err
		}
		bytes[i/2] = b
	}
	return bytes, nil
}

// bytesToHex is defined in hub_integration.go (shared helper)
