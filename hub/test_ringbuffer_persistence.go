package hub

import (
	"sync"

	"github.com/enbility/ship-go/api"
)

// TestRingBufferPersistence implements RingBufferPersistence for testing
// This is a simple in-memory implementation suitable for Hub unit tests
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

// NewTestRingBufferPersistenceWithErrors creates a test implementation that returns errors
func NewTestRingBufferPersistenceWithErrors(loadError, saveError error) *TestRingBufferPersistence {
	return &TestRingBufferPersistence{
		entries:   make([]api.DigestEntry, 0),
		nextIndex: 0,
		loadError: loadError,
		saveError: saveError,
	}
}

// LoadRingBuffer implements RingBufferPersistence
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

// SaveRingBuffer implements RingBufferPersistence
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

func (t *TestRingBufferPersistence) SetInitialData(entries []api.DigestEntry, nextIndex int) {
	t.mux.Lock()
	defer t.mux.Unlock()
	t.entries = make([]api.DigestEntry, len(entries))
	copy(t.entries, entries)
	t.nextIndex = nextIndex
}