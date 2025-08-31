package api_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRingBufferPersistence implements RingBufferPersistence for testing
type TestRingBufferPersistence struct {
	entries   []api.DigestEntry
	nextIndex int
	loadError error
	saveError error
	mux       sync.RWMutex
}

// NewTestRingBufferPersistence creates a test implementation
func NewTestRingBufferPersistence() *TestRingBufferPersistence {
	return &TestRingBufferPersistence{
		entries:   make([]api.DigestEntry, 0),
		nextIndex: 0,
	}
}

// NewTestRingBufferPersistenceWithData creates a test implementation with initial data
func NewTestRingBufferPersistenceWithData(entries []api.DigestEntry, nextIndex int) *TestRingBufferPersistence {
	return &TestRingBufferPersistence{
		entries:   entries,
		nextIndex: nextIndex,
	}
}

// NewTestRingBufferPersistenceWithErrors creates a test implementation that returns errors
func NewTestRingBufferPersistenceWithErrors(loadError, saveError error) *TestRingBufferPersistence {
	return &TestRingBufferPersistence{
		entries:   make([]api.DigestEntry, 0),
		nextIndex: 0,
		loadError: loadError,
		saveError: saveError,
	}
}

func (t *TestRingBufferPersistence) LoadRingBuffer() ([]api.DigestEntry, int, error) {
	t.mux.RLock()
	defer t.mux.RUnlock()

	if t.loadError != nil {
		return nil, 0, t.loadError
	}

	// Return a copy to prevent races
	entries := make([]api.DigestEntry, len(t.entries))
	copy(entries, t.entries)
	return entries, t.nextIndex, nil
}

func (t *TestRingBufferPersistence) SaveRingBuffer(entries []api.DigestEntry, nextIndex int) error {
	t.mux.Lock()
	defer t.mux.Unlock()

	if t.saveError != nil {
		return t.saveError
	}

	// Save a copy to prevent races
	t.entries = make([]api.DigestEntry, len(entries))
	copy(t.entries, entries)
	t.nextIndex = nextIndex
	return nil
}

// Test helper methods
func (t *TestRingBufferPersistence) SetLoadError(err error) {
	t.mux.Lock()
	defer t.mux.Unlock()
	t.loadError = err
}

func (t *TestRingBufferPersistence) SetSaveError(err error) {
	t.mux.Lock()
	defer t.mux.Unlock()
	t.saveError = err
}

func (t *TestRingBufferPersistence) GetState() ([]api.DigestEntry, int) {
	t.mux.RLock()
	defer t.mux.RUnlock()
	entries := make([]api.DigestEntry, len(t.entries))
	copy(entries, t.entries)
	return entries, t.nextIndex
}

// TestRingBufferPersistenceContract tests the interface contract
func TestRingBufferPersistenceContract(t *testing.T) {
	t.Run("EmptyState", func(t *testing.T) {
		persistence := NewTestRingBufferPersistence()
		
		entries, nextIndex, err := persistence.LoadRingBuffer()
		require.NoError(t, err)
		assert.Empty(t, entries)
		assert.Equal(t, 0, nextIndex)
		
		// Should be able to save empty state
		err = persistence.SaveRingBuffer([]api.DigestEntry{}, 0)
		assert.NoError(t, err)
	})

	t.Run("SaveAndLoad", func(t *testing.T) {
		persistence := NewTestRingBufferPersistence()
		
		// Create test entries
		testEntries := []api.DigestEntry{
			{Algorithm: "hmacSha256", Digest: "digest1", Timestamp: time.Now()},
			{Algorithm: "hmacSha256", Digest: "digest2", Timestamp: time.Now()},
		}
		
		// Save data
		err := persistence.SaveRingBuffer(testEntries, 1)
		require.NoError(t, err)
		
		// Load and verify
		entries, nextIndex, err := persistence.LoadRingBuffer()
		require.NoError(t, err)
		assert.Len(t, entries, 2)
		assert.Equal(t, 1, nextIndex)
		assert.Equal(t, "digest1", entries[0].Digest)
		assert.Equal(t, "digest2", entries[1].Digest)
	})

	t.Run("NextIndexBounds", func(t *testing.T) {
		persistence := NewTestRingBufferPersistence()
		
		// Test various nextIndex values
		testEntries := make([]api.DigestEntry, 10)
		for i := range testEntries {
			testEntries[i] = api.DigestEntry{
				Algorithm: "hmacSha256",
				Digest:    "digest" + string(rune('0'+i)),
				Timestamp: time.Now(),
			}
		}
		
		// Test boundary values
		testCases := []int{0, 5, 9} // First, middle, last valid index
		for _, nextIndex := range testCases {
			err := persistence.SaveRingBuffer(testEntries, nextIndex)
			require.NoError(t, err)
			
			entries, loadedIndex, err := persistence.LoadRingBuffer()
			require.NoError(t, err)
			assert.Equal(t, nextIndex, loadedIndex)
			assert.Len(t, entries, 10)
		}
	})

	t.Run("PartialRingBuffer", func(t *testing.T) {
		persistence := NewTestRingBufferPersistence()
		
		// Create ring buffer with some empty entries
		testEntries := make([]api.DigestEntry, 5)
		testEntries[0] = api.DigestEntry{Algorithm: "hmacSha256", Digest: "digest1", Timestamp: time.Now()}
		testEntries[1] = api.DigestEntry{Algorithm: "hmacSha256", Digest: "digest2", Timestamp: time.Now()}
		// entries[2], [3], [4] are empty (zero values)
		
		err := persistence.SaveRingBuffer(testEntries, 2)
		require.NoError(t, err)
		
		entries, nextIndex, err := persistence.LoadRingBuffer()
		require.NoError(t, err)
		assert.Len(t, entries, 5)
		assert.Equal(t, 2, nextIndex)
		
		// Verify filled and empty entries
		assert.Equal(t, "digest1", entries[0].Digest)
		assert.Equal(t, "digest2", entries[1].Digest)
		assert.Empty(t, entries[2].Algorithm) // Empty entry
		assert.Empty(t, entries[3].Algorithm) // Empty entry
		assert.Empty(t, entries[4].Algorithm) // Empty entry
	})

	t.Run("LoadError", func(t *testing.T) {
		loadErr := errors.New("storage system unavailable")
		persistence := NewTestRingBufferPersistenceWithErrors(loadErr, nil)
		
		entries, nextIndex, err := persistence.LoadRingBuffer()
		assert.Error(t, err)
		assert.Equal(t, loadErr, err)
		assert.Nil(t, entries)
		assert.Equal(t, 0, nextIndex)
	})

	t.Run("SaveError", func(t *testing.T) {
		saveErr := errors.New("disk full")
		persistence := NewTestRingBufferPersistenceWithErrors(nil, saveErr)
		
		testEntries := []api.DigestEntry{
			{Algorithm: "hmacSha256", Digest: "digest1", Timestamp: time.Now()},
		}
		
		err := persistence.SaveRingBuffer(testEntries, 1)
		assert.Error(t, err)
		assert.Equal(t, saveErr, err)
	})
}

// TestRingBufferPersistenceThreadSafety tests concurrent access
func TestRingBufferPersistenceThreadSafety(t *testing.T) {
	persistence := NewTestRingBufferPersistence()
	
	// Prepare test data
	testEntries := []api.DigestEntry{
		{Algorithm: "hmacSha256", Digest: "digest1", Timestamp: time.Now()},
		{Algorithm: "hmacSha256", Digest: "digest2", Timestamp: time.Now()},
	}
	
	const numGoroutines = 10
	const numOperations = 100
	
	var wg sync.WaitGroup
	errorChan := make(chan error, numGoroutines*numOperations)
	
	// Launch concurrent save operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				// Alternate between save and load
				if j%2 == 0 {
					err := persistence.SaveRingBuffer(testEntries, j%2)
					if err != nil {
						errorChan <- err
						return
					}
				} else {
					_, _, err := persistence.LoadRingBuffer()
					if err != nil {
						errorChan <- err
						return
					}
				}
			}
		}(i)
	}
	
	wg.Wait()
	close(errorChan)
	
	// Check for any errors
	for err := range errorChan {
		t.Errorf("Concurrent operation failed: %v", err)
	}
}

// TestRingBufferPersistenceDataIntegrity tests data integrity
func TestRingBufferPersistenceDataIntegrity(t *testing.T) {
	persistence := NewTestRingBufferPersistence()
	
	// Create entries with specific timestamps and data
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	testEntries := []api.DigestEntry{
		{
			Algorithm: "hmacSha256",
			Digest:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Timestamp: baseTime.Add(1 * time.Minute),
		},
		{
			Algorithm: "hmacSha256", 
			Digest:    "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
			Timestamp: baseTime.Add(2 * time.Minute),
		},
	}
	
	// Save original data
	err := persistence.SaveRingBuffer(testEntries, 1)
	require.NoError(t, err)
	
	// Load and verify all fields are preserved
	entries, nextIndex, err := persistence.LoadRingBuffer()
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, 1, nextIndex)
	
	// Verify first entry
	assert.Equal(t, testEntries[0].Algorithm, entries[0].Algorithm)
	assert.Equal(t, testEntries[0].Digest, entries[0].Digest)
	assert.True(t, testEntries[0].Timestamp.Equal(entries[0].Timestamp))
	
	// Verify second entry  
	assert.Equal(t, testEntries[1].Algorithm, entries[1].Algorithm)
	assert.Equal(t, testEntries[1].Digest, entries[1].Digest)
	assert.True(t, testEntries[1].Timestamp.Equal(entries[1].Timestamp))
}

// TestRingBufferPersistenceEdgeCases tests edge cases
func TestRingBufferPersistenceEdgeCases(t *testing.T) {
	t.Run("VeryLargeRingBuffer", func(t *testing.T) {
		persistence := NewTestRingBufferPersistence()
		
		// Create large ring buffer (1000 entries)
		largeEntries := make([]api.DigestEntry, 1000)
		for i := range largeEntries {
			largeEntries[i] = api.DigestEntry{
				Algorithm: "hmacSha256",
				Digest:    "digest" + string(rune(i)),
				Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			}
		}
		
		err := persistence.SaveRingBuffer(largeEntries, 999)
		require.NoError(t, err)
		
		entries, nextIndex, err := persistence.LoadRingBuffer()
		require.NoError(t, err)
		assert.Len(t, entries, 1000)
		assert.Equal(t, 999, nextIndex)
	})

	t.Run("ZeroLengthEntries", func(t *testing.T) {
		persistence := NewTestRingBufferPersistence()
		
		// Save zero-length entries array
		err := persistence.SaveRingBuffer([]api.DigestEntry{}, 0)
		require.NoError(t, err)
		
		entries, nextIndex, err := persistence.LoadRingBuffer()
		require.NoError(t, err)
		assert.Empty(t, entries)
		assert.Equal(t, 0, nextIndex)
	})

	t.Run("VeryLongDigestStrings", func(t *testing.T) {
		persistence := NewTestRingBufferPersistence()
		
		// Create entry with very long digest (test string handling)
		longDigest := make([]byte, 10000)
		for i := range longDigest {
			longDigest[i] = byte('a' + (i % 26))
		}
		
		testEntries := []api.DigestEntry{
			{
				Algorithm: "hmacSha256",
				Digest:    string(longDigest),
				Timestamp: time.Now(),
			},
		}
		
		err := persistence.SaveRingBuffer(testEntries, 0)
		require.NoError(t, err)
		
		entries, nextIndex, err := persistence.LoadRingBuffer()
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, 0, nextIndex)
		assert.Equal(t, string(longDigest), entries[0].Digest)
	})
}