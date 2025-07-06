// Package tests provides integration and stress tests for the SHIP implementation.
// These tests are separated from the main packages to allow for comprehensive
// system-level testing without circular dependencies.
//
// The stress tests use build tags to avoid running during normal test cycles:
//   go test -tags=stress ./tests
//
// For more information, see the README.md in this directory.
package tests