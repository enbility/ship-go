package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultPairingConfig(t *testing.T) {
	// TDD Test: DefaultPairingConfig should create valid configuration with sensible defaults

	secret := PairingSecret("test-secret-12345678901234567890123456789012") // 32 chars = 16 bytes
	mode := PairingModeListener

	config := NewPairingConfig(mode, secret)

	// Test core configuration is set correctly
	assert.Equal(t, secret, config.Secret)
	assert.Equal(t, mode, config.Mode)
}

func TestDefaultPairingConfig_DifferentModes(t *testing.T) {
	// TDD Test: DefaultPairingConfig should work with all pairing modes

	secret := PairingSecret("test-secret-12345678901234567890123456789012")

	modes := []PairingMode{
		PairingModeOff,
		PairingModeListener,
		PairingModeAnnouncer,
		PairingModeBoth,
	}

	for _, mode := range modes {
		config := NewPairingConfig(mode, secret)
		require.NotNil(t, config)
		assert.Equal(t, secret, config.Secret)
		assert.Equal(t, mode, config.Mode)
	}
}

func TestPairingConfig_CustomConfiguration(t *testing.T) {
	// TDD Test: PairingConfig should allow custom configuration

	secret := PairingSecret("custom-secret-123456789012345678901234567890")

	config := &PairingConfig{
		Mode:   PairingModeAnnouncer,
		Secret: secret,
	}

	// Test custom values are preserved
	assert.Equal(t, secret, config.Secret)
	assert.Equal(t, PairingModeAnnouncer, config.Mode)
}

func TestPairingConfig_ZeroValues(t *testing.T) {
	// TDD Test: PairingConfig should handle zero values appropriately

	config := &PairingConfig{}

	// Zero values should be valid (Hub will provide defaults where needed)
	assert.Equal(t, PairingSecret(nil), config.Secret)
	assert.Equal(t, PairingModeOff, config.Mode) // Zero value of PairingMode
}

func TestPairingConfig_Validate(t *testing.T) {
	// TDD Test: PairingConfig.Validate should check for valid configuration

	t.Run("ValidConfiguration", func(t *testing.T) {
		secret := PairingSecret("test-secret-12345678901234567890123456789012") // 32 chars = 16 bytes
		config := NewPairingConfig(PairingModeListener, secret)

		err := config.Validate()
		assert.NoError(t, err)
	})

	t.Run("NilConfig", func(t *testing.T) {
		var config *PairingConfig
		err := config.Validate()
		assert.NoError(t, err) // nil config should be valid
	})

	t.Run("SecretTooShort", func(t *testing.T) {
		secret := PairingSecret("short") // Only 5 bytes
		config := NewPairingConfig(PairingModeListener, secret)

		err := config.Validate()
		assert.ErrorIs(t, err, ErrInvalidSecret)
	})

	t.Run("SecretTooLong", func(t *testing.T) {
		secret := make(PairingSecret, 200) // 200 bytes - too long
		config := NewPairingConfig(PairingModeListener, secret)

		err := config.Validate()
		assert.ErrorIs(t, err, ErrInvalidSecret)
	})

	t.Run("InvalidConnectionTiming", func(t *testing.T) {
		secret := PairingSecret("test-secret-12345678901234567890123456789012")
		config := &PairingConfig{
			Mode:   PairingModeListener,
			Secret: secret,
		}

		err := config.Validate()
		assert.Nil(t, err)
	})

	t.Run("EmptySecretWithPairingMode", func(t *testing.T) {
		// Empty secret with pairing mode should be valid (will be configured later)
		config := &PairingConfig{
			Mode:   PairingModeListener,
			Secret: nil,
		}

		err := config.Validate()
		assert.NoError(t, err)
	})
}

func TestNewPairingConfig(t *testing.T) {
	// TDD Test: NewPairingConfig should create valid configuration

	secret := PairingSecret("test-secret-12345678901234567890123456789012")
	mode := PairingModeBoth

	config := NewPairingConfig(mode, secret)

	// Test core configuration is set correctly
	assert.Equal(t, mode, config.Mode)
	assert.Equal(t, secret, config.Secret)
}
