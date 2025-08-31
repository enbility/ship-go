package pairing

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPersistence implements RingBufferPersistence for testing
type TestPersistence struct {
	entries   []api.DigestEntry
	nextIndex int
	loadError error
	saveError error
	saveCount int // Track how many times Save was called
	mux       sync.RWMutex
}

func NewTestPersistence() *TestPersistence {
	return &TestPersistence{
		entries:   make([]api.DigestEntry, 0),
		nextIndex: 0,
	}
}

func (t *TestPersistence) LoadRingBuffer() ([]api.DigestEntry, int, error) {
	t.mux.RLock()
	defer t.mux.RUnlock()

	if t.loadError != nil {
		return nil, 0, t.loadError
	}

	entries := make([]api.DigestEntry, len(t.entries))
	copy(entries, t.entries)
	return entries, t.nextIndex, nil
}

func (t *TestPersistence) SaveRingBuffer(entries []api.DigestEntry, nextIndex int) error {
	t.mux.Lock()
	defer t.mux.Unlock()

	t.saveCount++

	if t.saveError != nil {
		return t.saveError
	}

	t.entries = make([]api.DigestEntry, len(entries))
	copy(t.entries, entries)
	t.nextIndex = nextIndex
	return nil
}

func (t *TestPersistence) SetLoadError(err error) {
	t.mux.Lock()
	defer t.mux.Unlock()
	t.loadError = err
}

func (t *TestPersistence) SetSaveError(err error) {
	t.mux.Lock()
	defer t.mux.Unlock()
	t.saveError = err
}

func (t *TestPersistence) GetSaveCount() int {
	t.mux.RLock()
	defer t.mux.RUnlock()
	return t.saveCount
}

func (t *TestPersistence) GetState() ([]api.DigestEntry, int) {
	t.mux.RLock()
	defer t.mux.RUnlock()
	entries := make([]api.DigestEntry, len(t.entries))
	copy(entries, t.entries)
	return entries, t.nextIndex
}

func (t *TestPersistence) SetInitialData(entries []api.DigestEntry, nextIndex int) {
	t.mux.Lock()
	defer t.mux.Unlock()
	t.entries = make([]api.DigestEntry, len(entries))
	copy(t.entries, entries)
	t.nextIndex = nextIndex
}

func TestNewRingBufferHistoryProvider(t *testing.T) {
	t.Run("ValidPersistence", func(t *testing.T) {
		persistence := NewTestPersistence()
		
		provider, err := NewRingBufferHistoryProvider(50, persistence)
		require.NoError(t, err)
		assert.NotNil(t, provider)
		assert.Equal(t, 50, provider.maxSize)
		assert.Equal(t, 0, provider.next)
	})

	t.Run("NilPersistence", func(t *testing.T) {
		provider, err := NewRingBufferHistoryProvider(50, nil)
		assert.Error(t, err)
		assert.Nil(t, provider)
		assert.Contains(t, err.Error(), "persistence cannot be nil")
	})

	t.Run("MinSizeEnforcement", func(t *testing.T) {
		persistence := NewTestPersistence()
		
		provider, err := NewRingBufferHistoryProvider(5, persistence) // Below minimum
		require.NoError(t, err)
		assert.Equal(t, 10, provider.maxSize) // Should be enforced to minimum
	})

	t.Run("LoadError", func(t *testing.T) {
		persistence := NewTestPersistence()
		persistence.SetLoadError(errors.New("storage unavailable"))
		
		provider, err := NewRingBufferHistoryProvider(50, persistence)
		assert.Error(t, err)
		assert.Nil(t, provider)
		assert.Contains(t, err.Error(), "failed to load ring buffer state")
		assert.Contains(t, err.Error(), "storage unavailable")
	})
}

func TestNewRingBufferHistoryProviderLegacy(t *testing.T) {
	provider := NewRingBufferHistoryProviderLegacy(50)
	require.NotNil(t, provider)
	assert.Equal(t, 50, provider.maxSize)
	assert.Equal(t, 0, provider.next)
	assert.Nil(t, provider.persistence)
}

func TestRingBufferHistoryProviderLoadFromPersistence(t *testing.T) {
	t.Run("EmptyPersistence", func(t *testing.T) {
		persistence := NewTestPersistence()
		
		provider, err := NewRingBufferHistoryProvider(10, persistence)
		require.NoError(t, err)
		assert.Equal(t, 0, provider.next)
		
		// All entries should be empty
		for i := 0; i < provider.maxSize; i++ {
			assert.Empty(t, provider.entries[i].Algorithm)
		}
	})

	t.Run("LoadExistingData", func(t *testing.T) {
		persistence := NewTestPersistence()
		
		// Set up initial data in persistence
		initialEntries := make([]api.DigestEntry, 10)
		initialEntries[0] = api.DigestEntry{Algorithm: "hmacSha256", Digest: "digest1", Timestamp: time.Now()}
		initialEntries[1] = api.DigestEntry{Algorithm: "hmacSha256", Digest: "digest2", Timestamp: time.Now()}
		persistence.SetInitialData(initialEntries, 2)
		
		provider, err := NewRingBufferHistoryProvider(10, persistence)
		require.NoError(t, err)
		assert.Equal(t, 2, provider.next)
		assert.Equal(t, "digest1", provider.entries[0].Digest)
		assert.Equal(t, "digest2", provider.entries[1].Digest)
		assert.Empty(t, provider.entries[2].Algorithm) // Should be empty
	})

	t.Run("LoadDataWithSizeMismatch", func(t *testing.T) {
		persistence := NewTestPersistence()
		
		// Set up data with size 15, but create provider with size 12 (>10 to avoid minimum enforcement)
		initialEntries := make([]api.DigestEntry, 15)
		for i := 0; i < 15; i++ {
			initialEntries[i] = api.DigestEntry{
				Algorithm: "hmacSha256", 
				Digest:    fmt.Sprintf("digest%d", i+1), 
				Timestamp: time.Now(),
			}
		}
		persistence.SetInitialData(initialEntries, 14)
		
		provider, err := NewRingBufferHistoryProvider(12, persistence)
		require.NoError(t, err)
		assert.Equal(t, 12, provider.maxSize)
		assert.Equal(t, 2, provider.next) // 14 % 12 = 2
		
		// Should copy first 12 entries
		assert.Equal(t, "digest1", provider.entries[0].Digest)
		assert.Equal(t, "digest2", provider.entries[1].Digest)
		assert.Equal(t, "digest12", provider.entries[11].Digest)
	})

	t.Run("InvalidNextIndex", func(t *testing.T) {
		persistence := NewTestPersistence()
		
		initialEntries := make([]api.DigestEntry, 5)
		persistence.SetInitialData(initialEntries, 10) // Invalid: nextIndex >= len(entries)
		
		provider, err := NewRingBufferHistoryProvider(10, persistence)
		assert.Error(t, err)
		assert.Nil(t, provider)
		assert.Contains(t, err.Error(), "invalid nextIndex")
	})
}

func TestRingBufferHistoryProviderSaveToPersistence(t *testing.T) {
	t.Run("SuccessfulSave", func(t *testing.T) {
		persistence := NewTestPersistence()
		
		provider, err := NewRingBufferHistoryProvider(10, persistence)
		require.NoError(t, err)
		
		// Record a pairing - should trigger save
		provider.RecordPairing("hmacSha256", "testdigest")
		
		// Verify save was called
		assert.Equal(t, 1, persistence.GetSaveCount())
		
		// Verify data was saved correctly
		savedEntries, savedNext := persistence.GetState()
		require.Len(t, savedEntries, 10)
		assert.Equal(t, 1, savedNext)
		assert.Equal(t, "hmacSha256", savedEntries[0].Algorithm)
		assert.Equal(t, "testdigest", savedEntries[0].Digest)
	})

	t.Run("SaveErrorContinuesOperation", func(t *testing.T) {
		persistence := NewTestPersistence()
		persistence.SetSaveError(errors.New("disk full"))
		
		provider, err := NewRingBufferHistoryProvider(10, persistence)
		require.NoError(t, err)
		
		// Record a pairing - should handle save error gracefully
		provider.RecordPairing("hmacSha256", "testdigest")
		
		// Verify in-memory state was updated despite save error
		assert.True(t, provider.HasSeenDigest("hmacSha256", "testdigest"))
		assert.Equal(t, 1, provider.next)
	})

	t.Run("LegacyProviderNoSave", func(t *testing.T) {
		provider := NewRingBufferHistoryProviderLegacy(10)
		
		// Should work without persistence
		provider.RecordPairing("hmacSha256", "testdigest")
		
		// Verify in-memory state was updated
		assert.True(t, provider.HasSeenDigest("hmacSha256", "testdigest"))
	})
}

func TestRingBufferHistoryProviderPersistenceIntegration(t *testing.T) {
	t.Run("RoundTripPersistence", func(t *testing.T) {
		persistence := NewTestPersistence()
		
		// Create provider and add some entries
		provider1, err := NewRingBufferHistoryProvider(5, persistence)
		require.NoError(t, err)
		
		provider1.RecordPairing("hmacSha256", "digest1")
		provider1.RecordPairing("hmacSha256", "digest2")
		provider1.RecordPairing("hmacSha256", "digest3")
		
		// Verify saves happened
		assert.Equal(t, 3, persistence.GetSaveCount())
		
		// Create new provider using same persistence (simulates restart)
		provider2, err := NewRingBufferHistoryProvider(5, persistence)
		require.NoError(t, err)
		
		// Verify state was loaded correctly
		assert.Equal(t, 3, provider2.next)
		assert.True(t, provider2.HasSeenDigest("hmacSha256", "digest1"))
		assert.True(t, provider2.HasSeenDigest("hmacSha256", "digest2"))
		assert.True(t, provider2.HasSeenDigest("hmacSha256", "digest3"))
		assert.False(t, provider2.HasSeenDigest("hmacSha256", "digest4"))
	})

	t.Run("RingBufferWrapAround", func(t *testing.T) {
		persistence := NewTestPersistence()
		
		// Use exactly 10 (minimum per SHIP spec) for predictable wraparound test
		provider, err := NewRingBufferHistoryProvider(10, persistence)
		require.NoError(t, err)
		assert.Equal(t, 10, provider.maxSize) // Verify actual size
		
		// Fill buffer beyond capacity to test wraparound
		// Add 11 entries to cause wraparound at position 0
		for i := 1; i <= 11; i++ {
			provider.RecordPairing("hmacSha256", fmt.Sprintf("digest%d", i))
		}
		
		// After 11 entries in size-10 buffer: next should be 1, digest1 should be overwritten
		assert.Equal(t, 1, provider.next, "Expected next=1 after wraparound")
		assert.False(t, provider.HasSeenDigest("hmacSha256", "digest1"), "digest1 should be overwritten")
		assert.True(t, provider.HasSeenDigest("hmacSha256", "digest2"))
		assert.True(t, provider.HasSeenDigest("hmacSha256", "digest11"))
		
		// Create new provider to verify persistence
		provider2, err := NewRingBufferHistoryProvider(10, persistence)
		require.NoError(t, err)
		
		assert.Equal(t, 1, provider2.next)
		assert.False(t, provider2.HasSeenDigest("hmacSha256", "digest1"))
		assert.True(t, provider2.HasSeenDigest("hmacSha256", "digest2"))
		assert.True(t, provider2.HasSeenDigest("hmacSha256", "digest11"))
	})
}

func TestRingBufferHistoryProviderThreadSafety(t *testing.T) {
	persistence := NewTestPersistence()
	
	provider, err := NewRingBufferHistoryProvider(100, persistence)
	require.NoError(t, err)
	
	const numGoroutines = 10
	const numOperations = 50
	
	var wg sync.WaitGroup
	
	// Concurrent record operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				digest := fmt.Sprintf("digest%d_%d", id, j)
				provider.RecordPairing("hmacSha256", digest)
			}
		}(i)
	}
	
	// Concurrent read operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				digest := fmt.Sprintf("digest%d_%d", id, j)
				provider.HasSeenDigest("hmacSha256", digest)
			}
		}(i)
	}
	
	wg.Wait()
	
	// Verify final state is consistent
	totalExpected := numGoroutines * numOperations
	assert.LessOrEqual(t, persistence.GetSaveCount(), totalExpected+10) // Allow some margin
	
	// Verify all operations completed without panic
	assert.NotNil(t, provider)
}