package cert

import (
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/enbility/ship-go/api"
)

// FingerprintTestSuite contains tests for certificate fingerprint operations
// Uses test certificates from SHIP Pairing Service specification Annex A
type FingerprintTestSuite struct {
	suite.Suite
	devACert *x509.Certificate // devA certificate from spec
	devZCert *x509.Certificate // devZ certificate from spec
}

func TestFingerprintTestSuite(t *testing.T) {
	suite.Run(t, new(FingerprintTestSuite))
}

func (suite *FingerprintTestSuite) SetupSuite() {
	// Load test certificates from SHIP specification Annex A
	var err error

	// devA certificate from SHIP spec Annex A.1
	devAPEM := `-----BEGIN CERTIFICATE-----
MIICJjCCAcugAwIBAgIURCTotMcqCC1Dt8VqkoywEXiXX2QwCgYIKoZIzj0EAwIw
aDELMAkGA1UEBhMCREUxDDAKBgNVBAgMA05SVzEQMA4GA1UEBwwHQ29sb2duZTEV
MBMGA1UECgwMRVhBTVBMRUJSQU5EMSIwIAYDVQQDDBlFRUIwMWRldkE4MTQtQzgy
NzdIMDA4Ri0zMB4XDTI1MDgxMTE2NTQwOVoXDTI5MDgxMTE2NTQwOVowaDELMAkG
A1UEBhMCREUxDDAKBgNVBAgMA05SVzEQMA4GA1UEBwwHQ29sb2duZTEVMBMGA1UE
CgwMRVhBTVBMRUJSQU5EMSIwIAYDVQQDDBlFRUIwMWRldkE4MTQtQzgyNzdIMDA4
Ri0zMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEsKOrknwvjOfO+ihEu9EIIwhM
rJraXone1kWiRA1DMVZK7A1k0rlEmCVL1XBOM7/wcloIrXnYX7iCqN3oBuarSKNT
MFEwHQYDVR0OBBYEFKgFVqJD9Xyhew1Xelm3EbeetIVsMB8GA1UdIwQYMBaAFKgF
VqJD9Xyhew1Xelm3EbeetIVsMA8GA1UdEwEB/wQFMAMBAf8wCgYIKoZIzj0EAwID
SQAwRgIhAIc7Is5jXiphSqXqK0V4yu5H2D+JxHBuLWDb74TnsKXOAiEAi0yLNr4z
YyfuxNevdBQr+PkpeskDrpJaAaUPg81JlQg=
-----END CERTIFICATE-----`

	// devZ certificate from SHIP spec Annex A.2
	devZPEM := `-----BEGIN CERTIFICATE-----
MIICLjCCAdOgAwIBAgIUN8jFQuYTDfjInHKhQQQkNVMkHRAwCgYIKoZIzj0EAwIw
bDELMAkGA1UEBhMCREUxDDAKBgNVBAgMA05SVzEQMA4GA1UEBwwHQ29sb2duZTEa
MBgGA1UECgwRT1RIRVJFWEFNUExFQlJBTkQxITAfBgNVBAMMGEVFQjAyc3RiNTQt
NDM2NTJiay0yLWd0MTAeFw0yNTA4MTExNTQ4MjlaFw0yOTA4MTExNTQ4MjlaMGwx
CzAJBgNVBAYTAkRFMQwwCgYDVQQIDANOUlcxEDAOBgNVBAcMB0NvbG9nbmUxGjAY
BgNVBAoMEU9USEVSRVhBTVBMRUJSQU5EMSEwHwYDVQQDDBhFRUIwMnN0YjU0LTQz
NjUyYmstMi1ndDEwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAARB4qKF7gkB0dpc
p7aGTT3YnAX2Im5HE3mtqKzDOuPoSl+YG19hpumso11J4T6iwjX2oORjrxFPmR3P
Pd1TAOX7o1MwUTAdBgNVHQ4EFgQUvehlF5Rzk0Fr3BVbMzgiQyN3FRcwHwYDVR0j
BBgwFoAUvehlF5Rzk0Fr3BVbMzgiQyN3FRcwDwYDVR0TAQH/BAUwAwEB/zAKBggq
hkjOPQQDAgNJADBGAiEAmFBWkuUyLe9546+5uwpyuOVi7fk6ZI3ahNhAs+FQJJMC
IQCqkzJAxrjNHJ3dcyQGsP5CSskTrVtTbqnTR3DbxvxAQQ==
-----END CERTIFICATE-----`

	suite.devACert, err = parsePEMCertificate(devAPEM)
	assert.NoError(suite.T(), err, "devA certificate should parse successfully")

	suite.devZCert, err = parsePEMCertificate(devZPEM)
	assert.NoError(suite.T(), err, "devZ certificate should parse successfully")
}

func (suite *FingerprintTestSuite) TestCalculateFingerprint_SpecificationTestVectors() {
	// Test fingerprint calculation with exact values from SHIP spec Annex A

	tests := []struct {
		name                string
		cert                *x509.Certificate
		expectedFingerprint string
	}{
		{
			name:                "devA certificate fingerprint",
			cert:                suite.devACert,
			expectedFingerprint: "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		},
		{
			name:                "devZ certificate fingerprint",
			cert:                suite.devZCert,
			expectedFingerprint: "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		},
	}

	for _, tt := range tests {
		suite.T().Run(tt.name, func(t *testing.T) {
			fingerprint, err := FingerprintFromCertificate(tt.cert)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedFingerprint, fingerprint,
				"fingerprint must match SHIP specification test vector")
		})
	}
}

func (suite *FingerprintTestSuite) TestValidateFingerprint() {
	// Test validation with correct fingerprint
	expectedFingerprint := "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943"
	err := ValidateFingerprint(suite.devACert, expectedFingerprint)
	assert.NoError(suite.T(), err, "validation should succeed with correct fingerprint")

	// Test validation with incorrect fingerprint
	wrongFingerprint := "AAAA7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943"
	err = ValidateFingerprint(suite.devACert, wrongFingerprint)
	assert.Error(suite.T(), err, "validation should fail with incorrect fingerprint")
	assert.Equal(suite.T(), api.ErrInvalidTargetFingerprint, err)

	// Test validation with invalid hex format
	invalidFingerprint := "not-hex-format"
	err = ValidateFingerprint(suite.devACert, invalidFingerprint)
	assert.Error(suite.T(), err, "validation should fail with invalid hex format")
}

func (suite *FingerprintTestSuite) TestFingerprintCalculation_EdgeCases() {
	// Test with nil certificate
	_, err := FingerprintFromCertificate(nil)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrInvalidCertificate, err)

	// Test validation with nil certificate
	err = ValidateFingerprint(nil, "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943")
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrInvalidCertificate, err)

	// Test validation with empty fingerprint
	err = ValidateFingerprint(suite.devACert, "")
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), api.ErrInvalidTargetFingerprint, err)
}

func (suite *FingerprintTestSuite) TestFingerprintHexEncoding() {
	// Calculate fingerprint for devA certificate
	fingerprint, err := FingerprintFromCertificate(suite.devACert)
	assert.NoError(suite.T(), err)

	// Verify format: 64 uppercase hex characters
	assert.Len(suite.T(), fingerprint, 64, "fingerprint should be 64 characters (SHA-256)")

	// Verify all characters are uppercase hex
	for _, char := range fingerprint {
		assert.True(suite.T(),
			(char >= '0' && char <= '9') || (char >= 'A' && char <= 'F'),
			"fingerprint must contain only uppercase hex characters")
	}

	// Verify no lowercase characters
	for _, char := range fingerprint {
		assert.False(suite.T(), char >= 'a' && char <= 'f',
			"fingerprint must not contain lowercase characters")
	}
}

func (suite *FingerprintTestSuite) TestFingerprintConsistency() {
	// Test that multiple calculations of the same certificate produce identical results
	fingerprint1, err := FingerprintFromCertificate(suite.devACert)
	assert.NoError(suite.T(), err)

	fingerprint2, err := FingerprintFromCertificate(suite.devACert)
	assert.NoError(suite.T(), err)

	assert.Equal(suite.T(), fingerprint1, fingerprint2,
		"multiple calculations should produce identical results")
}

func (suite *FingerprintTestSuite) TestValidateFingerprint_FormatValidation() {
	// Test validation with different invalid formats
	invalidFormats := []struct {
		name        string
		fingerprint string
		description string
	}{
		{
			name:        "too_short",
			fingerprint: "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD138433894",
			description: "63 characters instead of 64",
		},
		{
			name:        "too_long",
			fingerprint: "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD13843389433",
			description: "65 characters instead of 64",
		},
		{
			name:        "lowercase",
			fingerprint: "c74b7855d3479415f62cc01e5f6d9a93ebc676057d85417ada16fd1384338943",
			description: "lowercase hex characters",
		},
		{
			name:        "mixed_case",
			fingerprint: "C74B7855d3479415F62CC01E5F6D9A93ebc676057D85417ADA16FD1384338943",
			description: "mixed case hex characters",
		},
		{
			name:        "invalid_chars",
			fingerprint: "G74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
			description: "contains 'G' which is not hex",
		},
		{
			name:        "with_colons",
			fingerprint: "C7:4B:78:55:D3:47:94:15:F6:2C:C0:1E:5F:6D:9A:93:EB:C6:76:05:7D:85:41:7A:DA:16:FD:13:84:33:89:43",
			description: "hex with colons (common format but not SHIP spec)",
		},
	}

	for _, tc := range invalidFormats {
		suite.T().Run(tc.name, func(t *testing.T) {
			err := ValidateFingerprint(suite.devACert, tc.fingerprint)
			assert.Error(t, err, "validation should fail for %s", tc.description)
			assert.Equal(t, api.ErrInvalidTargetFingerprint, err)
		})
	}
}

func (suite *FingerprintTestSuite) TestValidateFingerprint_CaseSensitivity() {
	// SHIP spec requires uppercase hex, verify case sensitivity
	uppercaseFingerprint := "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943"
	lowercaseFingerprint := "c74b7855d3479415f62cc01e5f6d9a93ebc676057d85417ada16fd1384338943"

	// Uppercase should succeed
	err := ValidateFingerprint(suite.devACert, uppercaseFingerprint)
	assert.NoError(suite.T(), err, "validation should succeed with correct uppercase format")

	// Lowercase should fail per SHIP spec requirements
	err = ValidateFingerprint(suite.devACert, lowercaseFingerprint)
	assert.Error(suite.T(), err, "validation should fail with lowercase format")
	assert.Equal(suite.T(), api.ErrInvalidTargetFingerprint, err)
}

/* Helper Functions */

// parsePEMCertificate parses a PEM-encoded certificate
func parsePEMCertificate(pemData string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, api.ErrInvalidCertificate
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, api.ErrInvalidCertificate
	}

	return cert, nil
}
