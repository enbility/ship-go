package hub

import (
	"errors"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/enbility/ship-go/logging"
	"github.com/stretchr/testify/assert"
)

func TestLogConnectionError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		context       string
		expectedLevel string // "error", "debug" or "none"
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
		{
			// .local hostname resolved to a zone-less link-local IPv6 address;
			// the hub falls back to the advertised IP addresses right after
			name:          "link-local dial",
			err:           &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EINVAL)},
			context:       "connection to ski at host.local. failed:",
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
			logger := &levelLogger{level: "none"}
			logging.SetLogging(logger)
			defer logging.SetLogging(nil)

			logConnectionError(tt.err, tt.context)
			assert.Equal(t, tt.expectedLevel, logger.level)
		})
	}
}

// levelLogger records the level logConnectionError emitted at
type levelLogger struct {
	logging.NoLogging
	level string
}

func (l *levelLogger) Debug(args ...interface{}) { l.level = "debug" }
func (l *levelLogger) Error(args ...interface{}) { l.level = "error" }

// Helper type for timeout errors
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }
