//go:build !test
// +build !test

package ship

import "time"

// Production timer values
func getHelloInitTimeout() time.Duration {
	return tHelloInit
}

func getAbortDelay() time.Duration {
	return tAbortDelay
}

func getCmiTimeout() time.Duration {
	return cmiTimeout
}
