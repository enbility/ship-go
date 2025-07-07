package ws

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMaxMessageSize verifies that the MaxMessageSize constant is set to the expected value
// to prevent DoS attacks and expensive JSON conversions
func TestMaxMessageSize(t *testing.T) {
	// The constant should be 100KB (100 * 1024 bytes)
	expectedSize := 100 * 1024
	assert.Equal(t, expectedSize, MaxMessageSize, "MaxMessageSize should be 100KB")

	// Verify it's reasonable for SPINE messages
	typicalSPINEMessage := 50 * 1024 // 50KB typical
	maxSPINEMessage := 80 * 1024     // 80KB for complex messages

	assert.Greater(t, MaxMessageSize, typicalSPINEMessage, "MaxMessageSize should be larger than typical SPINE messages")
	assert.Greater(t, MaxMessageSize, maxSPINEMessage, "MaxMessageSize should be larger than complex SPINE messages")
}

// TestMessageSizeValidation tests the logic for validating message sizes
// This is a unit test demonstrating how message size validation should work
func TestMessageSizeValidation(t *testing.T) {
	tests := []struct {
		name        string
		messageSize int
		shouldPass  bool
	}{
		{
			name:        "Small message",
			messageSize: 1024,
			shouldPass:  true,
		},
		{
			name:        "Typical SPINE message",
			messageSize: 50 * 1024,
			shouldPass:  true,
		},
		{
			name:        "Large but valid message",
			messageSize: 99 * 1024,
			shouldPass:  true,
		},
		{
			name:        "Exactly at limit",
			messageSize: MaxMessageSize,
			shouldPass:  true,
		},
		{
			name:        "Over limit",
			messageSize: MaxMessageSize + 1,
			shouldPass:  false,
		},
		{
			name:        "Way over limit",
			messageSize: 1024 * 1024, // 1MB
			shouldPass:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This demonstrates the validation logic that would be used
			// when SetReadLimit is called on the WebSocket connection
			isValid := tt.messageSize <= MaxMessageSize
			assert.Equal(t, tt.shouldPass, isValid, "Message size validation failed for %s", tt.name)
		})
	}
}
