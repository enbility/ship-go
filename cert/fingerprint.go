package cert

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"strings"

	"github.com/enbility/ship-go/api"
)

// FingerprintFromCertificate calculates SHA-256 fingerprint of the given certificate
// Per SHIP spec section 6.2: SHA-256 hash of DER-encoded certificate
// Returns uppercase hex string as required by SHIP spec
func FingerprintFromCertificate(cert *x509.Certificate) (string, error) {
	if cert == nil {
		return "", api.ErrInvalidCertificate
	}

	// Calculate SHA-256 hash of DER-encoded certificate per SHIP spec section 6.2
	hash := sha256.Sum256(cert.Raw)

	// Convert to uppercase hex string per SHIP spec requirements
	fingerprint := strings.ToUpper(hex.EncodeToString(hash[:]))

	return fingerprint, nil
}

// ValidateFingerprint validates that a certificate matches the expected fingerprint
// expectedFingerprint must be a 64-character uppercase hex string per SHIP spec
func ValidateFingerprint(cert *x509.Certificate, expectedFingerprint string) error {
	if cert == nil {
		return api.ErrInvalidCertificate
	}

	if expectedFingerprint == "" {
		return api.ErrInvalidTargetFingerprint
	}

	// Validate hex format (64 characters, uppercase)
	if len(expectedFingerprint) != 64 {
		return api.ErrInvalidTargetFingerprint
	}

	// Check for valid hex characters (uppercase only per SHIP spec)
	for _, char := range expectedFingerprint {
		if (char < '0' || char > '9') && (char < 'A' || char > 'F') {
			return api.ErrInvalidTargetFingerprint
		}
	}

	// Calculate actual fingerprint
	actualFingerprint, err := FingerprintFromCertificate(cert)
	if err != nil {
		return err
	}

	// Compare fingerprints (case-sensitive per SHIP spec)
	if actualFingerprint != expectedFingerprint {
		return api.ErrInvalidTargetFingerprint
	}

	return nil
}
