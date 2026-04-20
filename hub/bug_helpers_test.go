package hub

import (
	"testing"
	"time"
)

// withDeadlockWatchdog runs fn in a goroutine and fails the test if fn does not
// complete within timeout. Used by deadlock tests that race two operations
// against each other and need to detect a hang deterministically.
func withDeadlockWatchdog(t *testing.T, timeout time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("operation did not complete within %s — possible deadlock", timeout)
	}
}
