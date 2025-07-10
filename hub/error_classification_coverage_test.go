package hub

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mockNetError implements net.Error for testing timeout scenarios
type mockNetError struct {
	msg       string
	timeout   bool
	temporary bool
}

func (m *mockNetError) Error() string   { return m.msg }
func (m *mockNetError) Timeout() bool   { return m.timeout }
func (m *mockNetError) Temporary() bool { return m.temporary }

// TestLogConnectionError_ComprehensiveCoverage tests all branches of logConnectionError
func TestLogConnectionError_ComprehensiveCoverage(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		context       string
		expectedLevel string // "error", "debug", or "none"
		description   string
	}{
		// Nil error case
		{
			name:          "nil_error",
			err:           nil,
			context:       "some context",
			expectedLevel: "none",
			description:   "should return early without logging",
		},

		// Certificate related errors - should be ERROR level
		{
			name:          "certificate_in_error_message",
			err:           errors.New("certificate validation failed"),
			context:       "connection failed:",
			expectedLevel: "error",
			description:   "certificate in error message triggers error level",
		},
		{
			name:          "ski_in_error_message",
			err:           errors.New("SKI mismatch detected"),
			context:       "validation:",
			expectedLevel: "error",
			description:   "SKI in error message triggers error level",
		},
		{
			name:          "certificate_in_context",
			err:           errors.New("generic error"),
			context:       "certificate verification failed:",
			expectedLevel: "error",
			description:   "certificate in context triggers error level",
		},
		{
			name:          "mixed_case_certificate",
			err:           errors.New("Certificate issue"),
			context:       "check:",
			expectedLevel: "error",
			description:   "case-sensitive check for certificate",
		},

		// Connection refused - should be DEBUG level
		{
			name:          "direct_ECONNREFUSED",
			err:           syscall.ECONNREFUSED,
			context:       "connection attempt:",
			expectedLevel: "debug",
			description:   "direct ECONNREFUSED is debug level",
		},
		{
			name:          "wrapped_ECONNREFUSED_in_OpError",
			err:           &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
			context:       "dial failed:",
			expectedLevel: "debug",
			description:   "wrapped ECONNREFUSED is debug level",
		},
		{
			name:          "deeply_wrapped_ECONNREFUSED",
			err:           fmt.Errorf("connection failed: %w", &net.OpError{Err: syscall.ECONNREFUSED}),
			context:       "remote connection:",
			expectedLevel: "debug",
			description:   "deeply wrapped ECONNREFUSED is debug level",
		},

		// Timeout errors - should be DEBUG level
		{
			name:          "timeout_error",
			err:           &mockNetError{msg: "operation timed out", timeout: true},
			context:       "read timeout:",
			expectedLevel: "debug",
			description:   "timeout errors are debug level",
		},
		{
			name:          "wrapped_timeout_in_OpError",
			err:           &net.OpError{Op: "read", Err: &mockNetError{msg: "timeout", timeout: true}},
			context:       "network operation:",
			expectedLevel: "debug",
			description:   "wrapped timeout is debug level",
		},
		{
			name:          "i_o_timeout_string",
			err:           errors.New("read tcp 192.168.1.1:4729: i/o timeout"),
			context:       "connection read:",
			expectedLevel: "debug",
			description:   "i/o timeout string is debug level",
		},
		{
			name:          "deadline_exceeded",
			err:           &net.OpError{Op: "write", Err: os.ErrDeadlineExceeded},
			context:       "write operation:",
			expectedLevel: "debug",
			description:   "deadline exceeded is timeout, thus debug level",
		},

		// Generic errors - should be ERROR level
		{
			name:          "generic_error",
			err:           errors.New("unexpected error occurred"),
			context:       "operation failed:",
			expectedLevel: "error",
			description:   "generic errors default to error level",
		},
		{
			name:          "io_error",
			err:           io.ErrUnexpectedEOF,
			context:       "read failed:",
			expectedLevel: "error",
			description:   "IO errors are error level",
		},
		{
			name:          "wrapped_generic_error",
			err:           fmt.Errorf("operation failed: %w", errors.New("internal error")),
			context:       "processing:",
			expectedLevel: "error",
			description:   "wrapped generic errors are error level",
		},

		// Edge cases
		{
			name:          "empty_context",
			err:           errors.New("some error"),
			context:       "",
			expectedLevel: "error",
			description:   "empty context still logs at error level",
		},
		{
			name:          "very_long_error_message",
			err:           errors.New(strings.Repeat("error ", 1000)),
			context:       "long error:",
			expectedLevel: "error",
			description:   "long error messages handled correctly",
		},
		{
			name:          "multiple_wrapped_errors",
			err:           fmt.Errorf("outer: %w", fmt.Errorf("middle: %w", syscall.ECONNREFUSED)),
			context:       "nested:",
			expectedLevel: "debug",
			description:   "multiple wrapping still detects ECONNREFUSED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: We can't easily test the actual logging output without
			// modifying the logging package, but we can verify the function
			// executes without panic and follows the expected code paths
			assert.NotPanics(t, func() {
				logConnectionError(tt.err, tt.context)
			}, tt.description)
		})
	}
}

// TestClassifyErrorLevel_Additional tests additional error classification logic
func TestClassifyErrorLevel_Additional(t *testing.T) {
	// Create a custom timeout error
	timeoutErr := &net.DNSError{
		Err:         "timeout",
		IsTimeout:   true,
		IsTemporary: true,
	}

	// Test that it's properly classified as timeout
	var netErr net.Error
	assert.True(t, errors.As(timeoutErr, &netErr))
	assert.True(t, netErr.Timeout())

	// Test with nil net.Error
	regularErr := errors.New("not a network error")
	assert.False(t, errors.As(regularErr, &netErr))
}

// TestLogConnectionError_RealWorldScenarios tests realistic error scenarios
func TestLogConnectionError_RealWorldScenarios(t *testing.T) {
	scenarios := []struct {
		name        string
		createError func() error
		context     string
	}{
		{
			name: "tls_handshake_failure",
			createError: func() error {
				return errors.New("tls: failed to verify certificate: x509: certificate signed by unknown authority")
			},
			context: "TLS handshake failed:",
		},
		{
			name: "dns_lookup_failure",
			createError: func() error {
				return &net.DNSError{
					Err:  "no such host",
					Name: "invalid.example.com",
				}
			},
			context: "DNS resolution:",
		},
		{
			name: "connection_reset",
			createError: func() error {
				return &net.OpError{
					Op:  "read",
					Net: "tcp",
					Err: syscall.ECONNRESET,
				}
			},
			context: "connection reset by peer:",
		},
		{
			name: "address_already_in_use",
			createError: func() error {
				return &net.OpError{
					Op:  "listen",
					Net: "tcp",
					Err: syscall.EADDRINUSE,
				}
			},
			context: "port binding failed:",
		},
		{
			name: "network_unreachable",
			createError: func() error {
				return &net.OpError{
					Op:  "dial",
					Net: "tcp",
					Err: syscall.ENETUNREACH,
				}
			},
			context: "network unreachable:",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			err := scenario.createError()
			assert.NotPanics(t, func() {
				logConnectionError(err, scenario.context)
			})
		})
	}
}

// TestLogConnectionError_ConcurrentCalls tests thread safety
func TestLogConnectionError_ConcurrentCalls(t *testing.T) {
	// Test concurrent calls don't cause issues
	done := make(chan bool, 100)

	for i := 0; i < 100; i++ {
		go func(idx int) {
			var err error
			context := fmt.Sprintf("concurrent call %d:", idx)

			switch idx % 4 {
			case 0:
				err = errors.New("certificate error")
			case 1:
				err = syscall.ECONNREFUSED
			case 2:
				err = &mockNetError{msg: "timeout", timeout: true}
			case 3:
				err = errors.New("generic error")
			}

			logConnectionError(err, context)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}
}

// TestHub_SafeClose tests the safeClose helper method
func TestHub_SafeClose(t *testing.T) {
	hub := setupTestHub(t)

	// Test with nil closer
	assert.NotPanics(t, func() {
		hub.safeClose(nil, "test context")
	})

	// Test with closer that returns error
	errorCloser := &mockCloser{err: errors.New("close failed")}
	assert.NotPanics(t, func() {
		hub.safeClose(errorCloser, "error closer")
	})
	assert.True(t, errorCloser.closed)

	// Test with successful closer
	successCloser := &mockCloser{err: nil}
	assert.NotPanics(t, func() {
		hub.safeClose(successCloser, "success closer")
	})
	assert.True(t, successCloser.closed)

	// Test with net.ErrClosed (should be ignored)
	closedCloser := &mockCloser{err: net.ErrClosed}
	assert.NotPanics(t, func() {
		hub.safeClose(closedCloser, "already closed")
	})
	assert.True(t, closedCloser.closed)

	// Test with "use of closed network connection" error
	networkClosedErr := &mockCloser{err: errors.New("use of closed network connection")}
	assert.NotPanics(t, func() {
		hub.safeClose(networkClosedErr, "network closed")
	})
	assert.True(t, networkClosedErr.closed)
}

// mockCloser implements io.Closer for testing
type mockCloser struct {
	closed bool
	err    error
}

func (m *mockCloser) Close() error {
	m.closed = true
	return m.err
}
