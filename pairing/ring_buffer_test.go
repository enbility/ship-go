package pairing

import (
	"fmt"
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestRingBufferSuite(t *testing.T) {
	suite.Run(t, new(RingBufferSuite))
}

type RingBufferSuite struct {
	suite.Suite
}

func (s *RingBufferSuite) TestNewRingBufferHistoryProviderLegacy() {
	provider := NewRingBufferHistoryProviderLegacy(50)
	assert.NotNil(s.T(), provider)
	assert.Equal(s.T(), 50, provider.maxSize)
	assert.Equal(s.T(), 0, provider.next)
	assert.Len(s.T(), provider.entries, 50)
}

func (s *RingBufferSuite) TestNewRingBufferHistoryProvider_MinimumSize() {
	provider := NewRingBufferHistoryProviderLegacy(5)
	assert.NotNil(s.T(), provider)
	assert.Equal(s.T(), 10, provider.maxSize) // Should be adjusted to minimum
	assert.Len(s.T(), provider.entries, 10)
}

func (s *RingBufferSuite) TestHasSeenDigest() {
	provider := NewRingBufferHistoryProviderLegacy(10)

	// Test empty buffer
	found := provider.HasSeenDigest("SHA256", "digest1")
	assert.False(s.T(), found)

	// Add entry
	provider.RecordPairing("SHA256", "digest1")

	// Test found
	found = provider.HasSeenDigest("SHA256", "digest1")
	assert.True(s.T(), found)

	// Test not found - different algorithm
	found = provider.HasSeenDigest("SHA512", "digest1")
	assert.False(s.T(), found)

	// Test not found - different digest
	found = provider.HasSeenDigest("SHA256", "digest2")
	assert.False(s.T(), found)
}

func (s *RingBufferSuite) TestRecordPairing() {
	provider := NewRingBufferHistoryProviderLegacy(3)

	// Record entries and verify they can be recorded successfully
	provider.RecordPairing("SHA256", "digest1")

	provider.RecordPairing("SHA256", "digest2")

	provider.RecordPairing("SHA256", "digest3")

	// Verify all entries are initially present
	assert.True(s.T(), provider.HasSeenDigest("SHA256", "digest1"))
	assert.True(s.T(), provider.HasSeenDigest("SHA256", "digest2"))
	assert.True(s.T(), provider.HasSeenDigest("SHA256", "digest3"))

	// Record enough additional entries to test wraparound behavior
	for i := 4; i <= 10; i++ {
		provider.RecordPairing("SHA256", fmt.Sprintf("digest%d", i))
	}

	// Verify the latest entry exists
	assert.True(s.T(), provider.HasSeenDigest("SHA256", "digest10"))
}

func (s *RingBufferSuite) TestGetCurrentEntry() {
	provider := NewRingBufferHistoryProviderLegacy(10)

	// Test empty buffer
	entry, err := provider.GetCurrentEntry()
	assert.Error(s.T(), err)
	assert.Nil(s.T(), entry)
	assert.Equal(s.T(), api.ErrHistoryProviderNotSet, err)

	// Add entry
	provider.RecordPairing("SHA256", "digest1")

	// Get current entry
	entry, err = provider.GetCurrentEntry()
	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), entry)
	assert.Equal(s.T(), "SHA256", entry.Algorithm)
	assert.Equal(s.T(), "digest1", entry.Digest)

	// Add second entry
	provider.RecordPairing("SHA256", "digest2")

	// Current should be the latest
	entry, err = provider.GetCurrentEntry()
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "digest2", entry.Digest)
}

func (s *RingBufferSuite) TestGetCurrentEntry_Wraparound() {
	provider := NewRingBufferHistoryProviderLegacy(2)

	// Fill buffer and wrap around
	provider.RecordPairing("SHA256", "digest1")

	provider.RecordPairing("SHA256", "digest2")

	provider.RecordPairing("SHA256", "digest3")

	// Current should be digest3 (latest)
	entry, err := provider.GetCurrentEntry()
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "digest3", entry.Digest)
}

func (s *RingBufferSuite) TestRingBuffer_ConcurrentAccess() {
	provider := NewRingBufferHistoryProviderLegacy(100)

	done := make(chan bool, 3)

	// Concurrent writers
	go func() {
		for i := 0; i < 50; i++ {
			provider.RecordPairing("SHA256", "writer1-digest")
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 50; i++ {
			provider.RecordPairing("SHA256", "writer2-digest")
		}
		done <- true
	}()

	// Concurrent reader
	go func() {
		for i := 0; i < 100; i++ {
			provider.HasSeenDigest("SHA256", "writer1-digest")
			provider.HasSeenDigest("SHA256", "writer2-digest")
		}
		done <- true
	}()

	<-done
	<-done
	<-done

	// Verify both entries are present
	assert.True(s.T(), provider.HasSeenDigest("SHA256", "writer1-digest"))
	assert.True(s.T(), provider.HasSeenDigest("SHA256", "writer2-digest"))
}
