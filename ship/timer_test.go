package ship

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTimerValues(t *testing.T) {
	// Get current timer values
	helloTimeout := getHelloInitTimeout()
	abortDelay := getAbortDelay()
	cmiTimeout := getCmiTimeout()

	t.Logf("Hello timeout: %v", helloTimeout)
	t.Logf("Abort delay: %v", abortDelay)
	t.Logf("CMI timeout: %v", cmiTimeout)

	// Check if we're running with test build tags
	if helloTimeout == tHelloInit {
		// Production values - skip the test
		t.Skip("Test requires -tags=test build flag for short timer values")
	}

	// With test tag, these should be short durations
	assert.Equal(t, 500*time.Millisecond, helloTimeout, "Hello timeout should be 500ms in tests")
	assert.Equal(t, 100*time.Millisecond, abortDelay, "Abort delay should be 100ms in tests")
	assert.Equal(t, 500*time.Millisecond, cmiTimeout, "CMI timeout should be 500ms in tests")
}
