package pairing

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/enbility/ship-go/api"
)

// QRCodeTestSuite contains tests for QR code parsing and validation
// Uses exact QR code data from SHIP Pairing Service specification Annex A.1
type QRCodeTestSuite struct {
	suite.Suite
}

func TestQRCodeTestSuite(t *testing.T) {
	suite.Run(t, new(QRCodeTestSuite))
}

// TestQRCodeParsing_SpecificationTestVector tests QR code parsing using exact Annex A.1 data
func (suite *QRCodeTestSuite) TestQRCodeParsing_SpecificationTestVector() {
	// QR code string from SHIP Pairing Service specification Annex A.1
	// This is the exact QR code data provided in the specification for devA
	specQRCode := "SHIP;SKI:A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C;ID:i:983327_u:C8277H008F-3;BRAND:EXAMPLEBRAND;TYPE:EMS;MODEL:EEB01devA814;SERIAL:C8277H008F-3;CAT:2;FPH256:C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943;SPSEC:7A37DCF81BDB50F8E92CFA4160CCB3DE;ENDSHIP;"

	// Parse the QR code
	qrData, err := ParseShipQRCode(specQRCode)
	assert.NoError(suite.T(), err, "should parse valid specification QR code")
	assert.NotNil(suite.T(), qrData, "parsed data should not be nil")

	// Verify all fields match specification values
	assert.Equal(suite.T(), "A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C", qrData.SKI, "SKI should match spec")
	assert.Equal(suite.T(), "i:983327_u:C8277H008F-3", qrData.ShipID, "SHIP ID should match spec")
	assert.Equal(suite.T(), "EXAMPLEBRAND", qrData.Brand, "Brand should match spec")
	assert.Equal(suite.T(), "EMS", qrData.Type, "Type should match spec")
	assert.Equal(suite.T(), "EEB01devA814", qrData.Model, "Model should match spec")
	assert.Equal(suite.T(), "C8277H008F-3", qrData.Serial, "Serial should match spec")
	assert.Equal(suite.T(), "2", qrData.Category, "Category should match spec")
	assert.Equal(suite.T(), "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943", qrData.FPH256, "FPH256 should match spec")
	assert.Equal(suite.T(), "7A37DCF81BDB50F8E92CFA4160CCB3DE", qrData.Secret, "SPSEC should match spec")

	// Verify extracted pairing data
	target := qrData.ToPairingTarget()
	assert.NotNil(suite.T(), target, "should create pairing target")
	assert.Equal(suite.T(), "A80556A243F57CA17B0D577A59B711B79EB4856C", target.SKI, "SKI should be normalized (no spaces)")
	assert.Equal(suite.T(), "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943", target.Fingerprint, "fingerprint should match")
	assert.Equal(suite.T(), "i:983327_u:C8277H008F-3", target.ShipID, "SHIP ID should match")
	assert.Len(suite.T(), target.Secret, 16, "secret should be 16 bytes (128-bit)")

	// Verify secret conversion from hex
	expectedSecret, err := hexToBytes("7A37DCF81BDB50F8E92CFA4160CCB3DE")
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), expectedSecret, target.Secret, "secret bytes should match hex conversion")
}

// TestQRCodeParsing_AllFingerprintTypes tests QR codes with different fingerprint types
func (suite *QRCodeTestSuite) TestQRCodeParsing_AllFingerprintTypes() {
	// Test data for different elliptic curves per SHIP spec Table 2
	testCases := []struct {
		name          string
		fpField       string
		fingerprint   string
		expectedCurve string
	}{
		{
			name:          "secp256r1_fingerprint",
			fpField:       "FPH256",
			fingerprint:   "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
			expectedCurve: "secp256r1",
		},
		{
			name:          "brainpoolP256r1_fingerprint",
			fpField:       "BFPH256",
			fingerprint:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
			expectedCurve: "brainpoolP256r1",
		},
		{
			name:          "brainpoolP384r1_fingerprint",
			fpField:       "B2FPH256",
			fingerprint:   "AAAA7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
			expectedCurve: "brainpoolP384r1",
		},
	}

	for _, tc := range testCases {
		suite.T().Run(tc.name, func(t *testing.T) {
			qrCode := "SHIP;SKI:A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C;ID:i:983327_u:C8277H008F-3;" +
				tc.fpField + ":" + tc.fingerprint + ";SPSEC:7A37DCF81BDB50F8E92CFA4160CCB3DE;ENDSHIP;"

			qrData, err := ParseShipQRCode(qrCode)
			assert.NoError(t, err, "should parse QR code with %s", tc.fpField)
			assert.NotNil(t, qrData, "parsed data should not be nil")

			// Check fingerprint field is set correctly
			switch tc.fpField {
			case "FPH256":
				assert.Equal(t, tc.fingerprint, qrData.FPH256)
				assert.Empty(t, qrData.BFPH256)
				assert.Empty(t, qrData.B2FPH256)
			case "BFPH256":
				assert.Equal(t, tc.fingerprint, qrData.BFPH256)
				assert.Empty(t, qrData.FPH256)
				assert.Empty(t, qrData.B2FPH256)
			case "B2FPH256":
				assert.Equal(t, tc.fingerprint, qrData.B2FPH256)
				assert.Empty(t, qrData.FPH256)
				assert.Empty(t, qrData.BFPH256)
			}

			// Verify GetPreferredFingerprint returns correct value
			fp, curve := qrData.GetPreferredFingerprint()
			assert.Equal(t, tc.fingerprint, fp)
			assert.Equal(t, tc.expectedCurve, curve)
		})
	}
}

// TestQRCodeParsing_MultipleFingerprintTypes tests QR codes with multiple fingerprint types
func (suite *QRCodeTestSuite) TestQRCodeParsing_MultipleFingerprintTypes() {
	// QR code with multiple fingerprint types (device supporting multiple curves)
	qrCodeMultipleFP := "SHIP;SKI:A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C;ID:i:983327_u:C8277H008F-3;" +
		"FPH256:C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943;" +
		"BFPH256:2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4;" +
		"B2FPH256:AAAA7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943;" +
		"SPSEC:7A37DCF81BDB50F8E92CFA4160CCB3DE;ENDSHIP;"

	qrData, err := ParseShipQRCode(qrCodeMultipleFP)
	assert.NoError(suite.T(), err, "should parse QR code with multiple fingerprint types")
	assert.NotNil(suite.T(), qrData)

	// Verify all fingerprint types are parsed
	assert.Equal(suite.T(), "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943", qrData.FPH256)
	assert.Equal(suite.T(), "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4", qrData.BFPH256)
	assert.Equal(suite.T(), "AAAA7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943", qrData.B2FPH256)

	// GetPreferredFingerprint should return secp256r1 (highest priority)
	fp, curve := qrData.GetPreferredFingerprint()
	assert.Equal(suite.T(), "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943", fp)
	assert.Equal(suite.T(), "secp256r1", curve)
}

// TestQRCodeParsing_MissingRequiredFields tests validation of required fields
func (suite *QRCodeTestSuite) TestQRCodeParsing_MissingRequiredFields() {
	// Test cases for missing required fields
	testCases := []struct {
		name        string
		qrCode      string
		expectedErr string
	}{
		{
			name:        "missing_ski",
			qrCode:      "SHIP;ID:i:983327_u:C8277H008F-3;FPH256:C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943;SPSEC:7A37DCF81BDB50F8E92CFA4160CCB3DE;ENDSHIP;",
			expectedErr: "missing required field: SKI",
		},
		{
			name:        "missing_ship_id",
			qrCode:      "SHIP;SKI:A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C;FPH256:C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943;SPSEC:7A37DCF81BDB50F8E92CFA4160CCB3DE;ENDSHIP;",
			expectedErr: "missing required field: ID",
		},
		{
			name:        "missing_fingerprint",
			qrCode:      "SHIP;SKI:A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C;ID:i:983327_u:C8277H008F-3;SPSEC:7A37DCF81BDB50F8E92CFA4160CCB3DE;ENDSHIP;",
			expectedErr: "no fingerprint field found",
		},
		{
			name:        "missing_secret",
			qrCode:      "SHIP;SKI:A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C;ID:i:983327_u:C8277H008F-3;FPH256:C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943;ENDSHIP;",
			expectedErr: "missing required field: SPSEC",
		},
		{
			name:        "missing_ship_wrapper",
			qrCode:      "SKI:A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C;ID:i:983327_u:C8277H008F-3;FPH256:C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943;SPSEC:7A37DCF81BDB50F8E92CFA4160CCB3DE;",
			expectedErr: "QR code must start with 'SHIP;'",
		},
		{
			name:        "missing_endship",
			qrCode:      "SHIP;SKI:A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C;ID:i:983327_u:C8277H008F-3;FPH256:C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943;SPSEC:7A37DCF81BDB50F8E92CFA4160CCB3DE;",
			expectedErr: "QR code must end with 'ENDSHIP;'",
		},
	}

	for _, tc := range testCases {
		suite.T().Run(tc.name, func(t *testing.T) {
			qrData, err := ParseShipQRCode(tc.qrCode)
			assert.Error(t, err, "should fail parsing QR code with %s", tc.name)
			assert.Nil(t, qrData, "parsed data should be nil on error")
			assert.Contains(t, err.Error(), tc.expectedErr, "error message should contain expected text")
		})
	}
}

// TestQRCodeParsing_InvalidHexValues tests validation of hex field values
func (suite *QRCodeTestSuite) TestQRCodeParsing_InvalidHexValues() {
	testCases := []struct {
		name        string
		field       string
		value       string
		expectedErr string
	}{
		{
			name:        "invalid_ski_hex",
			field:       "SKI",
			value:       "G805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C", // G is not hex
			expectedErr: "invalid hex character",
		},
		{
			name:        "ski_too_short",
			field:       "SKI",
			value:       "A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 85", // 19 bytes instead of 20
			expectedErr: "invalid SKI length",
		},
		{
			name:        "invalid_fph256_hex",
			field:       "FPH256",
			value:       "G74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943", // G is not hex
			expectedErr: "invalid hex character",
		},
		{
			name:        "fph256_too_short",
			field:       "FPH256",
			value:       "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD13843389", // 31 bytes instead of 32
			expectedErr: "invalid fingerprint length",
		},
		{
			name:        "invalid_spsec_hex",
			field:       "SPSEC",
			value:       "7A37DCF81BDB50F8E92CFA4160CCB3DG", // G is not hex
			expectedErr: "invalid hex character",
		},
		{
			name:        "spsec_too_short",
			field:       "SPSEC",
			value:       "7A37DCF81BDB50F8E92CFA4160CCB3", // 15 bytes instead of 16
			expectedErr: "invalid secret length",
		},
	}

	for _, tc := range testCases {
		suite.T().Run(tc.name, func(t *testing.T) {
			// Build QR code with invalid field value
			baseFields := map[string]string{
				"SKI":    "A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C",
				"ID":     "i:983327_u:C8277H008F-3",
				"FPH256": "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
				"SPSEC":  "7A37DCF81BDB50F8E92CFA4160CCB3DE",
			}
			baseFields[tc.field] = tc.value

			qrCode := "SHIP;"
			for field, value := range baseFields {
				qrCode += field + ":" + value + ";"
			}
			qrCode += "ENDSHIP;"

			qrData, err := ParseShipQRCode(qrCode)
			assert.Error(t, err, "should fail parsing QR code with invalid %s", tc.field)
			assert.Nil(t, qrData, "parsed data should be nil on error")
			assert.Contains(t, err.Error(), tc.expectedErr, "error message should indicate the problem")
		})
	}
}

// TestQRCodeValidation_SecretSizeValidation tests SPSEC field validation
func (suite *QRCodeTestSuite) TestQRCodeValidation_SecretSizeValidation() {
	// Test different secret sizes (SHIP pairing allows flexibility but recommends 128-bit)
	testCases := []struct {
		name       string
		secret     string
		shouldPass bool
	}{
		{"valid_128bit_spec", "7A37DCF81BDB50F8E92CFA4160CCB3DE", true},                            // 16 bytes = 128-bit (spec example)
		{"valid_256bit", "7A37DCF81BDB50F8E92CFA4160CCB3DE7A37DCF81BDB50F8E92CFA4160CCB3DE", true}, // 32 bytes = 256-bit
		{"too_short", "7A37DCF81BDB50F8E92CFA4160CCB3D", false},                                    // 15 bytes (too short)
		{"minimum_valid", "7A37DCF81BDB50F8E92CFA4160CCB3DE", true},                                // 16 bytes (minimum)
		{"empty_secret", "", false},                                                                // Empty
		{"odd_hex_chars", "7A37DCF81BDB50F8E92CFA4160CCB3D", false},                                // Odd number of hex chars
		{"maximum_reasonable", "7A37DCF81BDB50F8E92CFA4160CCB3DE7A37DCF81BDB50F8E92CFA4160CCB3DE7A37DCF81BDB50F8E92CFA4160CCB3DE7A37DCF81BDB50F8E92CFA4160CCB3DE", true}, // 64 bytes
	}

	for _, tc := range testCases {
		suite.T().Run(tc.name, func(t *testing.T) {
			qrCode := "SHIP;SKI:A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C;ID:i:983327_u:C8277H008F-3;" +
				"FPH256:C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943;SPSEC:" + tc.secret + ";ENDSHIP;"

			qrData, err := ParseShipQRCode(qrCode)
			if tc.shouldPass {
				assert.NoError(t, err, "should parse QR code with %s secret", tc.name)
				assert.NotNil(t, qrData, "parsed data should not be nil")

				if err == nil {
					target := qrData.ToPairingTarget()
					assert.NotNil(t, target, "should create pairing target")
					expectedLen := len(tc.secret) / 2
					assert.Len(t, target.Secret, expectedLen, "secret should be correct length")
				}
			} else {
				assert.Error(t, err, "should fail parsing QR code with %s secret", tc.name)
				assert.Nil(t, qrData, "parsed data should be nil on error")
			}
		})
	}
}

// TestQRCodeParsing_CaseInsensitivity tests case handling in hex fields
func (suite *QRCodeTestSuite) TestQRCodeParsing_CaseInsensitivity() {
	// Test mixed case hex values (should be accepted and normalized)
	qrCodeMixedCase := "SHIP;SKI:a805 56a2 43f5 7ca1 7b0d 577a 59b7 11b7 9eb4 856c;ID:i:983327_u:C8277H008F-3;" +
		"FPH256:c74b7855d3479415f62cc01e5f6d9a93ebc676057d85417ada16fd1384338943;SPSEC:7a37dcf81bdb50f8e92cfa4160ccb3de;ENDSHIP;"

	qrData, err := ParseShipQRCode(qrCodeMixedCase)
	assert.NoError(suite.T(), err, "should parse QR code with mixed case hex")
	assert.NotNil(suite.T(), qrData)

	// Verify normalized values (should be uppercase)
	assert.Equal(suite.T(), "A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C", qrData.SKI)
	assert.Equal(suite.T(), "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943", qrData.FPH256)
	assert.Equal(suite.T(), "7A37DCF81BDB50F8E92CFA4160CCB3DE", qrData.Secret)

	// Verify pairing target normalization
	target := qrData.ToPairingTarget()
	assert.Equal(suite.T(), "A80556A243F57CA17B0D577A59B711B79EB4856C", target.SKI) // No spaces in target SKI
}

// TestQRCodeParsing_OptionalFields tests handling of optional fields
func (suite *QRCodeTestSuite) TestQRCodeParsing_OptionalFields() {
	// Minimal QR code with only required fields
	minimalQRCode := "SHIP;SKI:A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C;ID:i:983327_u:C8277H008F-3;" +
		"FPH256:C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943;SPSEC:7A37DCF81BDB50F8E92CFA4160CCB3DE;ENDSHIP;"

	qrData, err := ParseShipQRCode(minimalQRCode)
	assert.NoError(suite.T(), err, "should parse minimal QR code")
	assert.NotNil(suite.T(), qrData)

	// Verify required fields are present
	assert.NotEmpty(suite.T(), qrData.SKI)
	assert.NotEmpty(suite.T(), qrData.ShipID)
	assert.NotEmpty(suite.T(), qrData.FPH256)
	assert.NotEmpty(suite.T(), qrData.Secret)

	// Verify optional fields are empty
	assert.Empty(suite.T(), qrData.Brand)
	assert.Empty(suite.T(), qrData.Type)
	assert.Empty(suite.T(), qrData.Model)
	assert.Empty(suite.T(), qrData.Serial)
	assert.Empty(suite.T(), qrData.Category)

	// Full QR code with all optional fields
	fullQRCode := "SHIP;SKI:A805 56A2 43F5 7CA1 7B0D 577A 59B7 11B7 9EB4 856C;ID:i:983327_u:C8277H008F-3;" +
		"BRAND:EXAMPLEBRAND;TYPE:EMS;MODEL:EEB01devA814;SERIAL:C8277H008F-3;CAT:2;" +
		"FPH256:C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943;SPSEC:7A37DCF81BDB50F8E92CFA4160CCB3DE;ENDSHIP;"

	fullQRData, err := ParseShipQRCode(fullQRCode)
	assert.NoError(suite.T(), err, "should parse full QR code")
	assert.NotNil(suite.T(), fullQRData)

	// Verify all fields are present
	assert.Equal(suite.T(), "EXAMPLEBRAND", fullQRData.Brand)
	assert.Equal(suite.T(), "EMS", fullQRData.Type)
	assert.Equal(suite.T(), "EEB01devA814", fullQRData.Model)
	assert.Equal(suite.T(), "C8277H008F-3", fullQRData.Serial)
	assert.Equal(suite.T(), "2", fullQRData.Category)
}

/* Helper Functions */

// ShipQRCodeData represents parsed QR code data
type ShipQRCodeData struct {
	SKI      string // Subject Key Identifier (with spaces for readability)
	ShipID   string // SHIP ID
	Brand    string // Device brand (optional)
	Type     string // Device type (optional)
	Model    string // Device model (optional)
	Serial   string // Device serial (optional)
	Category string // Device category (optional)
	FPH256   string // secp256r1 fingerprint
	BFPH256  string // brainpoolP256r1 fingerprint
	B2FPH256 string // brainpoolP384r1 fingerprint
	Secret   string // Pairing secret (SPSEC)
}

// GetPreferredFingerprint returns the preferred fingerprint and its curve
// Priority: secp256r1 > brainpoolP256r1 > brainpoolP384r1
func (q *ShipQRCodeData) GetPreferredFingerprint() (fingerprint, curve string) {
	if q.FPH256 != "" {
		return q.FPH256, "secp256r1"
	}
	if q.BFPH256 != "" {
		return q.BFPH256, "brainpoolP256r1"
	}
	if q.B2FPH256 != "" {
		return q.B2FPH256, "brainpoolP384r1"
	}
	return "", ""
}

// ToPairingTarget converts QR code data to PairingTarget
func (q *ShipQRCodeData) ToPairingTarget() *api.PairingTarget {
	fingerprint, _ := q.GetPreferredFingerprint()

	// Convert secret from hex to bytes
	secret, err := hexToBytes(q.Secret)
	if err != nil {
		return nil
	}

	return &api.PairingTarget{
		SKI:         normalizeHex(q.SKI), // Remove spaces for target
		Fingerprint: fingerprint,
		ShipID:      q.ShipID,
		Secret:      secret,
	}
}

// ParseShipQRCode parses a SHIP QR code string
func ParseShipQRCode(qrCode string) (*ShipQRCodeData, error) {
	// Validate wrapper
	if !strings.HasPrefix(qrCode, "SHIP;") {
		return nil, api.NewPairingValidationError("QR code must start with 'SHIP;'")
	}
	if !strings.HasSuffix(qrCode, "ENDSHIP;") {
		return nil, api.NewPairingValidationError("QR code must end with 'ENDSHIP;'")
	}

	// Extract content between SHIP; and ENDSHIP;
	content := strings.TrimPrefix(qrCode, "SHIP;")
	content = strings.TrimSuffix(content, "ENDSHIP;")

	// Split into fields
	fields := strings.Split(content, ";")
	fieldMap := make(map[string]string)

	for _, field := range fields {
		if field == "" {
			continue
		}
		parts := strings.SplitN(field, ":", 2)
		if len(parts) == 2 {
			fieldMap[parts[0]] = parts[1]
		}
	}

	// Parse required fields
	data := &ShipQRCodeData{}

	// SKI (required)
	ski, exists := fieldMap["SKI"]
	if !exists {
		return nil, api.NewPairingValidationError("missing required field: SKI")
	}
	if err := validateSKI(ski); err != nil {
		return nil, err
	}
	data.SKI = normalizeHexWithSpaces(ski)

	// SHIP ID (required)
	shipID, exists := fieldMap["ID"]
	if !exists {
		return nil, api.NewPairingValidationError("missing required field: ID")
	}
	data.ShipID = shipID

	// At least one fingerprint is required
	if fpsecp, exists := fieldMap["FPH256"]; exists {
		if err := validateFingerprint(fpsecp); err != nil {
			return nil, err
		}
		data.FPH256 = strings.ToUpper(fpsecp)
	}
	if fpbp256, exists := fieldMap["BFPH256"]; exists {
		if err := validateFingerprint(fpbp256); err != nil {
			return nil, err
		}
		data.BFPH256 = strings.ToUpper(fpbp256)
	}
	if fpbp384, exists := fieldMap["B2FPH256"]; exists {
		if err := validateFingerprint(fpbp384); err != nil {
			return nil, err
		}
		data.B2FPH256 = strings.ToUpper(fpbp384)
	}

	if data.FPH256 == "" && data.BFPH256 == "" && data.B2FPH256 == "" {
		return nil, api.NewPairingValidationError("no fingerprint field found (FPH256, BFPH256, or B2FPH256 required)")
	}

	// Secret (required)
	secret, exists := fieldMap["SPSEC"]
	if !exists {
		return nil, api.NewPairingValidationError("missing required field: SPSEC")
	}
	if err := validateSecret(secret); err != nil {
		return nil, err
	}
	data.Secret = strings.ToUpper(secret)

	// Optional fields
	data.Brand = fieldMap["BRAND"]
	data.Type = fieldMap["TYPE"]
	data.Model = fieldMap["MODEL"]
	data.Serial = fieldMap["SERIAL"]
	data.Category = fieldMap["CAT"]

	return data, nil
}

// validateSKI validates SKI format (20 bytes hex with optional spaces)
func validateSKI(ski string) error {
	normalized := strings.ReplaceAll(ski, " ", "")
	if len(normalized) != 40 { // 20 bytes * 2 hex chars
		return api.NewPairingValidationError("invalid SKI length (expected 40 hex characters)")
	}
	if !isValidHex(normalized) {
		return api.NewPairingValidationError("invalid hex character in SKI")
	}
	return nil
}

// validateFingerprint validates fingerprint format (32 bytes hex)
func validateFingerprint(fp string) error {
	if len(fp) != 64 { // 32 bytes * 2 hex chars
		return api.NewPairingValidationError("invalid fingerprint length (expected 64 hex characters)")
	}
	if !isValidHex(fp) {
		return api.NewPairingValidationError("invalid hex character in fingerprint")
	}
	return nil
}

// validateSecret validates secret format and length
func validateSecret(secret string) error {
	if len(secret) == 0 {
		return api.NewPairingValidationError("secret cannot be empty")
	}
	if len(secret)%2 != 0 {
		return api.NewPairingValidationError("secret must have even number of hex characters")
	}
	if len(secret) < 32 { // Minimum 16 bytes
		return api.NewPairingValidationError("invalid secret length (minimum 32 hex characters for 128-bit)")
	}
	if !isValidHex(secret) {
		return api.NewPairingValidationError("invalid hex character in secret")
	}
	return nil
}

// isValidHex checks if string contains only valid hex characters
func isValidHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'A' || c > 'F') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// normalizeHex converts hex to uppercase without spaces
func normalizeHex(s string) string {
	return strings.ToUpper(strings.ReplaceAll(s, " ", ""))
}

// normalizeHexWithSpaces formats hex with spaces every 4 characters
func normalizeHexWithSpaces(s string) string {
	normalized := normalizeHex(s)
	var result strings.Builder
	for i, c := range normalized {
		if i > 0 && i%4 == 0 {
			result.WriteByte(' ')
		}
		result.WriteRune(c)
	}
	return result.String()
}
