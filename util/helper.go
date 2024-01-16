package util

import (
	"encoding/json"
	"os"
	"strings"
)

// used in tests
func IsRunningOnCI() bool {
	return os.Getenv("ACTION_ENVIRONMENT") == "CI"
}

func Ptr[T any](v T) *T {
	return &v
}

// quick way to a struct into another
func DeepCopy[A any](source, dest A) {
	byt, _ := json.Marshal(source)
	_ = json.Unmarshal(byt, dest)
}

// check if a provided channel is closed
func IsChannelClosed[T any](ch <-chan T) bool {
	select {
	case <-ch:
		return false
	default:
		return true
	}
}

// standardize the provided SKI strings
func NormalizeSKI(ski string) string {
	ski = strings.ReplaceAll(ski, " ", "")
	ski = strings.ReplaceAll(ski, "-", "")
	ski = strings.ToLower(ski)

	return ski
}
