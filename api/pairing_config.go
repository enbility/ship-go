package api

// PairingConfig defines pairing behavior
type PairingConfig struct {
	// Core configuration (required)
	Mode   PairingMode   // Operating mode: Off, Listener, Announcer, Both
	Secret PairingSecret // 16-byte shared secret for HMAC validation (from QR code SPSEC field)
}

// NewPairingConfig creates a new pairing configuration with the specified mode and secret
func NewPairingConfig(mode PairingMode, secret PairingSecret) *PairingConfig {
	return &PairingConfig{
		Mode:   mode,
		Secret: secret,
	}
}

// Validate validates the pairing configuration
func (c *PairingConfig) Validate() error {
	if c == nil {
		return nil // nil config is valid (no pairing)
	}

	// Validate secret length for HMAC operations
	if c.Mode != PairingModeOff && len(c.Secret) > 0 {
		if len(c.Secret) < 16 {
			return ErrInvalidSecret
		}
		if len(c.Secret) > 128 {
			return ErrInvalidSecret
		}
	}

	return nil
}
