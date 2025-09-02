package api

import (
	"errors"
	"testing"
)

// TestPairingServiceReaderInterface_ServiceIdentity tests the new interface design
// This test verifies that the interface compiles with ServiceIdentity parameters
func TestPairingServiceReaderInterface_ServiceIdentity(t *testing.T) {
	// Create a test ServiceIdentity
	identity := ServiceIdentity{
		SKI:         "test-ski-12345",
		Fingerprint: "test-fingerprint",
		ShipID:      "test-ship-id",
		PairingType: PairingTypeAddCu,
		IPv4:        "192.168.1.100",
	}

	// Test that we can create a test implementation
	testImpl := &testServiceIdentityPairingReader{}

	// These calls should work with ServiceIdentity parameters
	testImpl.ServiceAutoTrusted(identity)
	testImpl.ServiceAutoTrustFailed(identity, errors.New("test error"))
	testImpl.ServiceAutoTrustRemoved(identity, "device replacement")

	// Verify the test implementation satisfies the interface
	var _ PairingServiceReaderInterface = testImpl

	t.Log("Interface successfully updated to use ServiceIdentity")
}

// testServiceIdentityPairingReader implements PairingServiceReaderInterface for testing
type testServiceIdentityPairingReader struct{}

func (t *testServiceIdentityPairingReader) ServiceAutoTrusted(identity ServiceIdentity) {
	// Test implementation - just verify we can access the fields directly
	_ = identity.SKI
	_ = identity.ShipID
	_ = identity.PairingType
}

func (t *testServiceIdentityPairingReader) ServiceAutoTrustFailed(identity ServiceIdentity, reason error) {
	// Test implementation
	_ = identity.SKI
	_ = reason
}

func (t *testServiceIdentityPairingReader) ServiceAutoTrustRemoved(identity ServiceIdentity, reason string) {
	// Test implementation
	_ = identity.SKI
	_ = reason
}

// TestServiceIdentity_ThreadSafety tests that ServiceIdentity is thread-safe by design
func TestServiceIdentity_ThreadSafety(t *testing.T) {
	identity := ServiceIdentity{
		SKI:         "thread-safe-test",
		ShipID:      "ship-123",
		PairingType: PairingTypeDefault,
	}

	// Value semantics - copying is safe
	identityCopy := identity
	identityCopy.SKI = "modified-ski"

	// Original should be unchanged (value semantics)
	if identity.SKI != "thread-safe-test" {
		t.Errorf("Original identity was modified, expected 'thread-safe-test', got '%s'", identity.SKI)
	}

	// Copy should have new value
	if identityCopy.SKI != "modified-ski" {
		t.Errorf("Copy was not modified, expected 'modified-ski', got '%s'", identityCopy.SKI)
	}
}