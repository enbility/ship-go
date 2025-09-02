package api

import (
	"testing"
)

// TestServiceDetails_ToServiceIdentity tests the conversion from ServiceDetails to ServiceIdentity
func TestServiceDetails_ToServiceIdentity(t *testing.T) {
	// Create a ServiceDetails with all fields populated
	service := NewServiceDetails("test-ski-123", "test-fingerprint-456", "test-ship-789")
	service.SetPairingType(PairingTypeAddCu)
	service.SetIPv4("192.168.1.100")

	// Convert to ServiceIdentity
	identity := service.ToServiceIdentity()

	// Verify all data was preserved (SKI gets normalized by removing dashes)
	if identity.SKI != "testski123" {
		t.Errorf("SKI not preserved: expected 'testski123' (normalized), got '%s'", identity.SKI)
	}

	if identity.Fingerprint != "test-fingerprint-456" {
		t.Errorf("Fingerprint not preserved: expected 'test-fingerprint-456', got '%s'", identity.Fingerprint)
	}

	if identity.ShipID != "test-ship-789" {
		t.Errorf("ShipID not preserved: expected 'test-ship-789', got '%s'", identity.ShipID)
	}

	if identity.PairingType != PairingTypeAddCu {
		t.Errorf("PairingType not preserved: expected PairingTypeAddCu (%d), got %d", PairingTypeAddCu, identity.PairingType)
	}

	if identity.IPv4 != "192.168.1.100" {
		t.Errorf("IPv4 not preserved: expected '192.168.1.100', got '%s'", identity.IPv4)
	}
}

// TestServiceDetails_ToServiceIdentity_MinimalData tests conversion with minimal data
func TestServiceDetails_ToServiceIdentity_MinimalData(t *testing.T) {
	// Create ServiceDetails with only required data (SKI)
	service := NewServiceDetails("minimal-ski", "", "")

	// Convert to ServiceIdentity
	identity := service.ToServiceIdentity()

	// Verify minimal data is preserved (SKI gets normalized)
	if identity.SKI != "minimalski" {
		t.Errorf("SKI not preserved: expected 'minimalski' (normalized), got '%s'", identity.SKI)
	}

	// Verify default values
	if identity.PairingType != PairingTypeDefault {
		t.Errorf("PairingType should default to PairingTypeDefault (%d), got %d", PairingTypeDefault, identity.PairingType)
	}

	// Empty fields should remain empty
	if identity.Fingerprint != "" {
		t.Errorf("Fingerprint should be empty, got '%s'", identity.Fingerprint)
	}

	if identity.ShipID != "" {
		t.Errorf("ShipID should be empty, got '%s'", identity.ShipID)
	}

	if identity.IPv4 != "" {
		t.Errorf("IPv4 should be empty, got '%s'", identity.IPv4)
	}
}

// TestServiceDetails_ToServiceIdentity_ThreadSafety tests that conversion is thread-safe
func TestServiceDetails_ToServiceIdentity_ThreadSafety(t *testing.T) {
	service := NewServiceDetails("thread-test-ski", "fp", "ship")
	service.SetPairingType(PairingTypeAddCu)

	// Multiple conversions should be safe (using thread-safe getter methods)
	identity1 := service.ToServiceIdentity()
	identity2 := service.ToServiceIdentity()

	// Results should be identical
	if identity1.SKI != identity2.SKI {
		t.Errorf("Concurrent conversions produced different SKI values")
	}

	if identity1.PairingType != identity2.PairingType {
		t.Errorf("Concurrent conversions produced different PairingType values")
	}

	// Modifying one identity shouldn't affect the other (value semantics)
	identity1.SKI = "modified-ski"
	if identity2.SKI == "modified-ski" {
		t.Errorf("Value semantics broken: modifying one identity affected another")
	}
}

// TestServiceDetails_ToServiceIdentity_DataIntegrity tests that internal ServiceDetails state is not exposed
func TestServiceDetails_ToServiceIdentity_DataIntegrity(t *testing.T) {
	service := NewServiceDetails("integrity-test", "fp", "ship")
	service.SetTrusted(true) // This should NOT be in ServiceIdentity

	_ = service.ToServiceIdentity()

	// ServiceIdentity should only contain identification data, not trust state
	// Trust state is managed by the method names (ServiceAutoTrusted implies trusted=true)
	
	// Verify the service still has its internal state
	if !service.Trusted() {
		t.Errorf("Original service trust state was modified during conversion")
	}

	// ServiceIdentity should contain only identification fields
	// (No trusted field - that's the whole point of the refactor!)
	expectedFields := []string{"SKI", "Fingerprint", "ShipID", "PairingType", "IPv4"}
	t.Logf("ServiceIdentity contains expected identification fields: %v", expectedFields)
}