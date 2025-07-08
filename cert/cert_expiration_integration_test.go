package cert

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/enbility/ship-go/logging"
	"github.com/stretchr/testify/assert"
)

// TestLogger captures log output for testing
type TestLogger struct {
	buffer *bytes.Buffer
}

func NewTestLogger() *TestLogger {
	return &TestLogger{
		buffer: &bytes.Buffer{},
	}
}

func (l *TestLogger) Trace(args ...interface{})                 {}
func (l *TestLogger) Tracef(format string, args ...interface{}) {}
func (l *TestLogger) Debug(args ...interface{})                 {}
func (l *TestLogger) Debugf(format string, args ...interface{}) {}

func (l *TestLogger) Info(args ...interface{}) {
	fmt.Fprintln(l.buffer, args...)
}

func (l *TestLogger) Infof(format string, args ...interface{}) {
	fmt.Fprintf(l.buffer, format+"\n", args...)
}

func (l *TestLogger) Error(args ...interface{}) {
	fmt.Fprintln(l.buffer, args...)
}

func (l *TestLogger) Errorf(format string, args ...interface{}) {
	fmt.Fprintf(l.buffer, format+"\n", args...)
}

func (l *TestLogger) GetOutput() string {
	return l.buffer.String()
}

func TestCertificateExpirationIntegration(t *testing.T) {
	// Set up test logger
	logger := NewTestLogger()
	logging.SetLogging(logger)
	defer logging.SetLogging(nil)

	t.Run("logs_expiring_certificate", func(t *testing.T) {
		// Create certificate expiring in 20 days
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "test-device-20days",
			},
			NotAfter: time.Now().Add(20 * 24 * time.Hour),
		}

		LogCertificateExpiration(cert, "test-ski-20days")

		output := logger.GetOutput()
		assert.Contains(t, output, "Certificate for SKI test-ski-20days")
		assert.Contains(t, output, "test-device-20days")
		assert.Contains(t, output, "expires in 20 days")
		logger.buffer.Reset()
	})

	t.Run("logs_expired_certificate", func(t *testing.T) {
		// Create certificate that expired 5 days ago
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "test-device-expired",
			},
			NotAfter: time.Now().Add(-5 * 24 * time.Hour),
		}

		LogCertificateExpiration(cert, "test-ski-expired")

		output := logger.GetOutput()
		assert.Contains(t, output, "Certificate for SKI test-ski-expired")
		assert.Contains(t, output, "test-device-expired")
		assert.Contains(t, output, "expired 5 days ago")
		logger.buffer.Reset()
	})

	t.Run("logs_certificate_expired_today", func(t *testing.T) {
		// Create certificate that expires exactly now (0 days)
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "test-device-expired-today",
			},
			NotAfter: time.Now(),
		}

		LogCertificateExpiration(cert, "test-ski-expired-today")

		output := logger.GetOutput()
		assert.Contains(t, output, "Certificate for SKI test-ski-expired-today")
		assert.Contains(t, output, "test-device-expired-today")
		assert.Contains(t, output, "expired today")
		assert.NotContains(t, output, "days ago") // Should not say "0 days ago"
		logger.buffer.Reset()
	})

	t.Run("no_log_for_valid_certificate", func(t *testing.T) {
		// Create certificate valid for 100 days
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "test-device-valid",
			},
			NotAfter: time.Now().Add(100 * 24 * time.Hour),
		}

		LogCertificateExpiration(cert, "test-ski-valid")

		output := logger.GetOutput()
		assert.Empty(t, strings.TrimSpace(output), "No log output expected for valid certificate")
	})

	t.Run("logs_unknown_for_empty_common_name", func(t *testing.T) {
		// Create certificate with empty common name that expires in 15 days
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "", // Empty common name
			},
			NotAfter: time.Now().Add(15 * 24 * time.Hour),
		}

		LogCertificateExpiration(cert, "test-ski-no-cn")

		output := logger.GetOutput()
		assert.Contains(t, output, "Certificate for SKI test-ski-no-cn")
		assert.Contains(t, output, "(CN: unknown)") // Should show "unknown" instead of empty
		assert.Contains(t, output, "expires in 15 days")
		logger.buffer.Reset()
	})

	t.Run("logs_unknown_for_empty_common_name_expired", func(t *testing.T) {
		// Create expired certificate with empty common name
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "", // Empty common name
			},
			NotAfter: time.Now().Add(-3 * 24 * time.Hour),
		}

		LogCertificateExpiration(cert, "test-ski-no-cn-expired")

		output := logger.GetOutput()
		assert.Contains(t, output, "Certificate for SKI test-ski-no-cn-expired")
		assert.Contains(t, output, "(CN: unknown)") // Should show "unknown" instead of empty
		assert.Contains(t, output, "expired 3 days ago")
		logger.buffer.Reset()
	})
}