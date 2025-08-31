package pairing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"

	"github.com/enbility/ship-go/api"
)

// HMACCalculator implements the PairingCryptoInterface for HMAC operations
type HMACCalculator struct {
	// No fields needed for stateless operations
}

// NewHMACCalculator creates a new HMAC calculator
func NewHMACCalculator() *HMACCalculator {
	return &HMACCalculator{}
}

// GenerateNonce generates a cryptographically secure 128-bit nonce
func (h *HMACCalculator) GenerateNonce() ([]byte, error) {
	nonce := make([]byte, 16) // 128 bits
	_, err := rand.Read(nonce)
	if err != nil {
		return nil, api.ErrNonceGenerationFailed
	}
	return nonce, nil
}

// CalculateDigest calculates HMAC-SHA256 digest per SHIP spec section 7
func (h *HMACCalculator) CalculateDigest(secret api.PairingSecret, params *api.HMACParams) ([]byte, error) {
	// Validate inputs
	if len(secret) == 0 {
		return nil, api.ErrInvalidSecret
	}
	if params == nil {
		return nil, api.ErrHMACCalculationFailed
	}
	if params.Algorithm != api.AlgorithmHMACSHA256 {
		return nil, api.ErrUnsupportedAlgorithm
	}
	if params.TxtRecord == nil {
		return nil, api.ErrHMACCalculationFailed
	}

	// Construct key K = devA-secret || devZ-nonce per SHIP spec section 7.3
	key := h.constructKey(secret, params.Nonce)

	// Construct message M per SHIP spec section 7.4
	message := h.constructMessage(params.TxtRecord)

	// Calculate HMAC-SHA256
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	digest := mac.Sum(nil)

	return digest, nil
}

// ValidateDigest validates HMAC digest with constant-time comparison
func (h *HMACCalculator) ValidateDigest(secret api.PairingSecret, params *api.HMACParams, expectedDigest []byte) error {
	// Calculate expected digest
	calculatedDigest, err := h.CalculateDigest(secret, params)
	if err != nil {
		return err
	}

	// Constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare(calculatedDigest, expectedDigest) != 1 {
		return api.ErrInvalidHMACDigest
	}

	return nil
}

// constructKey constructs the HMAC key K = devA-secret || devZ-nonce per SHIP spec section 7.3
func (h *HMACCalculator) constructKey(secret api.PairingSecret, nonce []byte) []byte {
	// Direct binary concatenation as specified in SHIP spec section 7.3
	key := make([]byte, len(secret)+len(nonce))
	copy(key, secret)
	copy(key[len(secret):], nonce)
	return key
}

// constructMessage constructs the HMAC message M per SHIP spec section 7.4
func (h *HMACCalculator) constructMessage(txtRecord *api.ShipPairingTXT) string {
	// Fixed field ordering per SHIP spec section 7.4
	// Note: digest field is NOT included in the message construction
	return fmt.Sprintf("txtvers=%s;parType=%s;forId=%s;forPar=%s;trustId=%s;trustPar=%s;trustCurve=%s;type=%s;trustNonce=%s;alg=%s;",
		txtRecord.TxtVers,
		txtRecord.ParType,
		txtRecord.ForId,
		txtRecord.ForPar,
		txtRecord.TrustId,
		txtRecord.TrustPar,
		txtRecord.TrustCurve,
		txtRecord.Type,
		txtRecord.TrustNonce,
		txtRecord.Alg,
	)
}
