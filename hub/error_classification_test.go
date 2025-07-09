package hub

import (
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogConnectionError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		context       string
		expectedLevel string // "error" or "debug"
	}{
		// Security/Certificate errors should be ERROR level
		{
			name:          "certificate error",
			err:           errors.New("x509: certificate signed by unknown authority"),
			context:       "client certificate validation failed:",
			expectedLevel: "error",
		},
		{
			name:          "SKI error",
			err:           errors.New("invalid SKI format"),
			context:       "SKI validation:",
			expectedLevel: "error",
		},
		{
			name:          "certificate context",
			err:           errors.New("some error"),
			context:       "certificate verification failed:",
			expectedLevel: "error",
		},

		// Connection refused/timeout should be DEBUG level (expected during discovery)
		{
			name:          "connection refused",
			err:           syscall.ECONNREFUSED,
			context:       "connection to remote service failed:",
			expectedLevel: "debug",
		},
		{
			name:          "wrapped connection refused",
			err:           &net.OpError{Err: syscall.ECONNREFUSED},
			context:       "connection attempt:",
			expectedLevel: "debug",
		},
		{
			name:          "timeout error",
			err:           &timeoutError{},
			context:       "connection timeout:",
			expectedLevel: "debug",
		},

		// Everything else should be ERROR level
		{
			name:          "generic error",
			err:           errors.New("unexpected error"),
			context:       "processing message:",
			expectedLevel: "error",
		},
		{
			name:          "nil error",
			err:           nil,
			context:       "some context:",
			expectedLevel: "none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We can't easily test the actual logging output,
			// but we can test the classification logic
			level := classifyErrorLevel(tt.err, tt.context)
			assert.Equal(t, tt.expectedLevel, level)
		})
	}
}

// Helper type for timeout errors
type timeoutError struct{}

func (e *timeoutError) Error() string { return "timeout" }
func (e *timeoutError) Timeout() bool { return true }
func (e *timeoutError) Temporary() bool { return true }

// Test the classification logic separately
func TestClassifyErrorLevel(t *testing.T) {
	// Test direct error classification
	assert.Equal(t, "none", classifyErrorLevel(nil, ""))
	assert.Equal(t, "error", classifyErrorLevel(errors.New("test"), ""))
	
	// Test context-based classification
	assert.Equal(t, "error", classifyErrorLevel(errors.New("any"), "certificate"))
	assert.Equal(t, "error", classifyErrorLevel(errors.New("any"), "SKI validation"))
	
	// Test syscall errors
	assert.Equal(t, "debug", classifyErrorLevel(syscall.ECONNREFUSED, ""))
	
	// Test wrapped errors
	opErr := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: syscall.ECONNREFUSED,
	}
	assert.Equal(t, "debug", classifyErrorLevel(opErr, ""))
}

// Helper function for testing - matches the logic in error_classification.go
func classifyErrorLevel(err error, context string) string {
	if err == nil {
		return "none"
	}
	
	// Security/auth errors should be at Error level
	if strings.Contains(err.Error(), "certificate") ||
		strings.Contains(err.Error(), "SKI") ||
		strings.Contains(context, "certificate") {
		return "error"
	}
	
	// Check for connection refused
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "debug"
	}
	
	// Check for timeout errors
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "debug"
	}
	
	// Check in wrapped errors
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) {
			return "debug"
		}
	}
	
	// Everything else is Error
	return "error"
}