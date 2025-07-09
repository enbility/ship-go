package hub

import (
	"errors"
	"net"
	"strings"
	"syscall"

	"github.com/enbility/ship-go/logging"
)

// logConnectionError logs connection errors with appropriate severity levels
// Security/auth errors are logged at Error level
// Expected connection failures (refused, timeout) are logged at Debug level
func logConnectionError(err error, context string) {
	if err == nil {
		return
	}

	// Security/auth errors should be at Error level
	if strings.Contains(err.Error(), "certificate") ||
		strings.Contains(err.Error(), "SKI") ||
		strings.Contains(context, "certificate") {
		logging.Log().Error(context, err)
		return
	}

	// Connection refused/timeout can be Debug (expected during discovery)
	if errors.Is(err, syscall.ECONNREFUSED) {
		logging.Log().Debug(context, err)
		return
	}

	// Check for timeout errors
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		logging.Log().Debug(context, err)
		return
	}

	// Check in wrapped errors
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) {
			logging.Log().Debug(context, err)
			return
		}
	}

	// Everything else is Error
	logging.Log().Error(context, err)
}

// safeClose closes a connection and logs any real errors
// It filters out expected errors like "already closed"
func (h *Hub) safeClose(closer interface{ Close() error }, context string) {
	if closer == nil {
		return
	}

	if err := closer.Close(); err != nil {
		// Only log if it's not an already-closed error
		if !errors.Is(err, net.ErrClosed) &&
			!strings.Contains(err.Error(), "use of closed") {
			logging.Log().Debug("close error in", context, ":", err)
		}
	}
}