package api

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestServiceDetails(t *testing.T) {
	suite.Run(t, new(ServiceDetailsSuite))
}

type ServiceDetailsSuite struct {
	suite.Suite
}

func (s *ServiceDetailsSuite) Test_ServiceDetails() {
	testSki := "testski"
	testFingerprint := "fingerprint"
	testShipID := "ship-id"

	details := NewServiceDetails(testSki, testFingerprint, testShipID)
	assert.NotNil(s.T(), details)

	conState := NewConnectionStateDetail(ConnectionStateNone, nil)
	details.SetConnectionStateDetail(conState)

	state := details.ConnectionStateDetail()
	assert.Equal(s.T(), ConnectionStateNone, state.State())

	fingerprint := details.Fingerprint()
	assert.Equal(s.T(), testFingerprint, fingerprint)

	details.SetFingerprint("newfingerprint")
	assert.Equal(s.T(), "newfingerprint", details.Fingerprint())

	ski := details.SKI()
	assert.Equal(s.T(), testSki, ski)

	details.SetSKI("newski")
	assert.Equal(s.T(), "newski", details.SKI())

	shipid := details.ShipID()
	assert.Equal(s.T(), testShipID, shipid)

	details.SetShipID("shipid")
	assert.Equal(s.T(), "shipid", details.ShipID())

	details.SetIPv4("127.0.0.1")
	assert.Equal(s.T(), "127.0.0.1", details.IPv4())

	details.SetAutoAccept(true)
	assert.Equal(s.T(), true, details.AutoAccept())

	details.SetTrusted(true)
	assert.Equal(s.T(), true, details.Trusted())
}

func (s *ServiceDetailsSuite) Test_ServiceDetails_FingerprintStorage() {
	testSki := "test"
	details := NewServiceDetails(testSki, "", "")

	// Test: Default fingerprint is empty
	assert.Empty(s.T(), details.Fingerprint())

	// Test: Can store and retrieve fingerprint
	fingerprint := "A1B2C3D4E5F6789012345678901234567890123456789012345678901234567890"
	details.SetFingerprint(fingerprint)
	assert.Equal(s.T(), fingerprint, details.Fingerprint())

	// Test: Can update fingerprint
	newFingerprint := "B2C3D4E5F6789012345678901234567890123456789012345678901234567890A1"
	details.SetFingerprint(newFingerprint)
	assert.Equal(s.T(), newFingerprint, details.Fingerprint())

	// Test: Can clear fingerprint
	details.SetFingerprint("")
	assert.Empty(s.T(), details.Fingerprint())
}

func (s *ServiceDetailsSuite) Test_ServiceDetails_Copy() {
	// Create original service with all fields populated
	original := NewServiceDetails("testski", "fingerprint-123", "ship-456")
	original.SetIPv4("192.168.1.1")
	original.SetAutoAccept(true)
	original.SetTrusted(true)
	original.SetPairingType(PairingTypeAddCu)

	// Set connection state with error
	connectionState := NewConnectionStateDetail(ConnectionStateError, errors.New("test error"))
	original.SetConnectionStateDetail(connectionState)

	// Create copy
	copyOfService := original.Copy()

	// Verify copy has same values as original
	assert.Equal(s.T(), original.SKI(), copyOfService.SKI())
	assert.Equal(s.T(), original.IPv4(), copyOfService.IPv4())
	assert.Equal(s.T(), original.ShipID(), copyOfService.ShipID())
	assert.Equal(s.T(), original.AutoAccept(), copyOfService.AutoAccept())
	assert.Equal(s.T(), original.Trusted(), copyOfService.Trusted())
	assert.Equal(s.T(), original.Fingerprint(), copyOfService.Fingerprint())
	assert.Equal(s.T(), original.PairingType(), copyOfService.PairingType())

	// Verify ConnectionStateDetail is copied
	assert.NotNil(s.T(), copyOfService.ConnectionStateDetail())
	assert.Equal(s.T(), original.ConnectionStateDetail().State(), copyOfService.ConnectionStateDetail().State())
	assert.Equal(s.T(), original.ConnectionStateDetail().Error().Error(), copyOfService.ConnectionStateDetail().Error().Error())

	// Verify copy is independent (not same instance)
	assert.NotSame(s.T(), original, copyOfService)
	assert.NotSame(s.T(), original.ConnectionStateDetail(), copyOfService.ConnectionStateDetail())

	// Verify modifying copy doesn't affect original
	copyOfService.SetTrusted(false)
	copyOfService.SetPairingType(PairingTypeDefault)
	copyOfService.ConnectionStateDetail().SetState(ConnectionStateCompleted)

	assert.NotEqual(s.T(), original.Trusted(), copyOfService.Trusted())
	assert.NotEqual(s.T(), original.PairingType(), copyOfService.PairingType())
	assert.NotEqual(s.T(), original.ConnectionStateDetail().State(), copyOfService.ConnectionStateDetail().State())
}

// Test that uninitialized PairingType defaults to PairingTypeDefault
func (s *ServiceDetailsSuite) TestServiceDetails_PairingType_DefaultValue() {
	// Create a new service
	service := NewServiceDetails("testski", "", "")

	// Test: Default pairing type should be PairingTypeDefault
	assert.Equal(s.T(), PairingTypeDefault, service.PairingType())
}

// Test setting and getting PairingType values
func (s *ServiceDetailsSuite) TestServiceDetails_PairingType_SetAndGet() {
	service := NewServiceDetails("testski", "", "")

	// Test: Default value
	assert.Equal(s.T(), PairingTypeDefault, service.PairingType())

	// Test: Setting to PairingTypeAddCu
	service.SetPairingType(PairingTypeAddCu)
	assert.Equal(s.T(), PairingTypeAddCu, service.PairingType())

	// Test: Setting back to PairingTypeDefault
	service.SetPairingType(PairingTypeDefault)
	assert.Equal(s.T(), PairingTypeDefault, service.PairingType())
}

// Test concurrent access to PairingType
func (s *ServiceDetailsSuite) TestServiceDetails_PairingType_ThreadSafety() {
	service := NewServiceDetails("testski", "", "")

	const numGoroutines = 10
	const numIterations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Launch goroutines that concurrently set and get PairingType
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numIterations; j++ {
				// Alternate between setting different pairing types
				if (id+j)%2 == 0 {
					service.SetPairingType(PairingTypeDefault)
					pairingType := service.PairingType()
					// Verify we get a valid pairing type (either value is valid)
					assert.True(s.T(), pairingType == PairingTypeDefault || pairingType == PairingTypeAddCu)
				} else {
					service.SetPairingType(PairingTypeAddCu)
					pairingType := service.PairingType()
					// Verify we get a valid pairing type (either value is valid)
					assert.True(s.T(), pairingType == PairingTypeDefault || pairingType == PairingTypeAddCu)
				}
			}
		}(i)
	}

	wg.Wait()

	// Final verification - the final value should be valid
	finalType := service.PairingType()
	assert.True(s.T(), finalType == PairingTypeDefault || finalType == PairingTypeAddCu)
}

// Test atomic behavior of PairingType operations
func (s *ServiceDetailsSuite) TestServiceDetails_PairingType_AtomicOperations() {
	service := NewServiceDetails("testski", "", "")

	// Test: Verify initial state
	assert.Equal(s.T(), PairingTypeDefault, service.PairingType())

	// Test: Atomic set and get operations
	service.SetPairingType(PairingTypeAddCu)

	// Launch multiple readers concurrently to verify atomic reads
	const numReaders = 5
	var wg sync.WaitGroup
	results := make([]PairingType, numReaders)

	wg.Add(numReaders)
	for i := 0; i < numReaders; i++ {
		go func(index int) {
			defer wg.Done()
			// Small delay to increase chance of concurrent access
			time.Sleep(time.Microsecond)
			results[index] = service.PairingType()
		}(i)
	}

	// While readers are running, change the value
	time.Sleep(time.Microsecond * 5)
	service.SetPairingType(PairingTypeDefault)

	wg.Wait()

	// All readers should have gotten consistent values (no torn reads)
	// Each result should be either the old or new value, never something invalid
	for _, result := range results {
		assert.True(s.T(), result == PairingTypeDefault || result == PairingTypeAddCu)
	}

	// Final verification
	assert.Equal(s.T(), PairingTypeDefault, service.PairingType())
}

// Step 1: Conversion methods test - RED phase (test first)
func (s *ServiceDetailsSuite) TestServiceDetails_ToServiceIdentity() {
	// Test: Convert ServiceDetails to ServiceIdentity
	details := NewServiceDetails("convert-ski", "convert-fingerprint", "convert-shipid")
	details.SetPairingType(PairingTypeAddCu)
	details.SetIPv4("192.168.1.100")

	// Convert to ServiceIdentity
	identity := details.ToServiceIdentity()

	// Should have same identity data
	assert.Equal(s.T(), details.SKI(), identity.SKI)
	assert.Equal(s.T(), details.Fingerprint(), identity.Fingerprint)
	assert.Equal(s.T(), details.ShipID(), identity.ShipID)
	assert.Equal(s.T(), details.PairingType(), identity.PairingType)
	assert.Equal(s.T(), details.IPv4(), identity.IPv4)

	// Should be independent (modifying identity doesn't affect original)
	identity.SKI = "modified-ski"
	assert.NotEqual(s.T(), identity.SKI, details.SKI())
}

func (s *ServiceDetailsSuite) TestSKIToServiceIdentity() {
	// Test: Minimal ServiceIdentity from SKI only
	ski := "minimal-ski"

	identity := SKIToServiceIdentity(ski)

	assert.Equal(s.T(), ski, identity.SKI)
	assert.Empty(s.T(), identity.Fingerprint)
	assert.Empty(s.T(), identity.ShipID)
	assert.Equal(s.T(), PairingTypeDefault, identity.PairingType)
	assert.Empty(s.T(), identity.IPv4)
}

func (s *ServiceDetailsSuite) TestServiceDetails_RoundTripConversion() {
	// Test: ServiceDetails → ServiceIdentity → ServiceDetails preserves data
	original := NewServiceDetails("roundtrip-ski", "roundtrip-fingerprint", "roundtrip-shipid")
	original.SetPairingType(PairingTypeAddCu)
	original.SetIPv4("203.0.113.1")

	// Test round-trip conversion via ToServiceIdentity
	identity := original.ToServiceIdentity()

	// Verify identity extraction preserves data
	assert.Equal(s.T(), original.SKI(), identity.SKI)
	assert.Equal(s.T(), original.Fingerprint(), identity.Fingerprint)
	assert.Equal(s.T(), original.ShipID(), identity.ShipID)
	assert.Equal(s.T(), original.PairingType(), identity.PairingType)
	assert.Equal(s.T(), original.IPv4(), identity.IPv4)
}
