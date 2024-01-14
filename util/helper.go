package util

import (
	"os"
)

// used in tests
func IsRunningOnCI() bool {
	return os.Getenv("ACTION_ENVIRONMENT") == "CI"
}

func Ptr[T any](v T) *T {
	return &v
}
