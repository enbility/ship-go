//go:build test
// +build test

package ship

import "time"

// Test timer values - much shorter to prevent long-running tests
func getHelloInitTimeout() time.Duration {
	return 500 * time.Millisecond // Production: 60s
}

func getAbortDelay() time.Duration {
	return 100 * time.Millisecond // Production: 1s
}

func getCmiTimeout() time.Duration {
	return 500 * time.Millisecond // Production: 10s
}

// Override timer variables for tests
func init() {
	tAbortDelay = 100 * time.Millisecond
	tHelloProlongMin = 100 * time.Millisecond    // Production: 1s
	tHelloProlongThrInc = 200 * time.Millisecond // Production: 30s
}