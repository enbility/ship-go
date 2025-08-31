package pairing

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/enbility/ship-go/api"
)

// RingBufferHistoryProvider implements bounded ring buffer per SHIP spec section 11
// with persistent storage support via RingBufferPersistence interface
type RingBufferHistoryProvider struct {
	persistence api.RingBufferPersistence // Storage interface
	entries     []api.DigestEntry
	maxSize     int
	next        int // Next index to write (SHIP spec terminology)
	mux         sync.RWMutex
}

// NewRingBufferHistoryProvider creates a bounded ring buffer with SHIP spec defaults
// and loads initial state from the persistence layer
func NewRingBufferHistoryProvider(maxSize int, persistence api.RingBufferPersistence) (*RingBufferHistoryProvider, error) {
	if maxSize < 10 {
		maxSize = 10 // SHIP spec minimum
	}
	
	if persistence == nil {
		return nil, errors.New("persistence cannot be nil")
	}

	provider := &RingBufferHistoryProvider{
		persistence: persistence,
		maxSize:     maxSize,
		entries:     make([]api.DigestEntry, maxSize),
		next:        0,
	}
	
	// Load initial state from persistence
	if err := provider.loadFromPersistence(); err != nil {
		return nil, fmt.Errorf("failed to load ring buffer state: %w", err)
	}
	
	return provider, nil
}

// NewRingBufferHistoryProviderLegacy creates a bounded ring buffer with SHIP spec defaults
// without persistence (for backward compatibility during migration)
// DEPRECATED: Use NewRingBufferHistoryProvider with RingBufferPersistence instead
func NewRingBufferHistoryProviderLegacy(maxSize int) *RingBufferHistoryProvider {
	if maxSize < 10 {
		maxSize = 10 // SHIP spec minimum
	}

	return &RingBufferHistoryProvider{
		persistence: nil, // No persistence
		entries:     make([]api.DigestEntry, maxSize),
		maxSize:     maxSize,
		next:        0,
	}
}

// loadFromPersistence loads the ring buffer state from persistent storage
func (r *RingBufferHistoryProvider) loadFromPersistence() error {
	if r.persistence == nil {
		return nil // No persistence configured
	}
	
	entries, nextIndex, err := r.persistence.LoadRingBuffer()
	if err != nil {
		return fmt.Errorf("persistence load failed: %w", err)
	}
	
	// If loaded data is empty, keep default initialized state
	if len(entries) == 0 {
		return nil
	}
	
	// Validate loaded state
	if nextIndex < 0 || nextIndex >= len(entries) {
		return fmt.Errorf("invalid nextIndex %d for buffer size %d", nextIndex, len(entries))
	}
	
	// Resize our buffer if loaded data has different size
	if len(entries) != r.maxSize {
		// Create new buffer with our configured size
		newEntries := make([]api.DigestEntry, r.maxSize)
		
		// Copy as much as possible from loaded data
		copySize := len(entries)
		if copySize > r.maxSize {
			copySize = r.maxSize
		}
		copy(newEntries, entries[:copySize])
		
		// Adjust nextIndex if buffer size changed
		if nextIndex >= r.maxSize {
			nextIndex = nextIndex % r.maxSize
		}
		
		r.entries = newEntries
	} else {
		// Use loaded data directly
		r.entries = entries
	}
	
	r.next = nextIndex
	return nil
}

// saveToPersistence saves current state to persistent storage
func (r *RingBufferHistoryProvider) saveToPersistence() error {
	if r.persistence == nil {
		return nil // No persistence configured
	}
	
	// Create a copy to avoid race conditions during save
	entriesCopy := make([]api.DigestEntry, len(r.entries))
	copy(entriesCopy, r.entries)
	
	return r.persistence.SaveRingBuffer(entriesCopy, r.next)
}

// HasSeenDigest checks if digest was seen before (implements PairingHistoryProviderInterface)
func (r *RingBufferHistoryProvider) HasSeenDigest(alg, digest string) bool {
	r.mux.RLock()
	defer r.mux.RUnlock()

	for i := 0; i < r.maxSize; i++ {
		entry := r.entries[i]
		if entry.Algorithm == alg && entry.Digest == digest {
			return true
		}
	}
	return false
}

// RecordPairing records successful pairing in ring buffer (implements PairingHistoryProviderInterface)
func (r *RingBufferHistoryProvider) RecordPairing(alg, digest string) {
	r.mux.Lock()
	defer r.mux.Unlock()

	// Add entry at current "next" position per SHIP spec section 11.3
	r.entries[r.next] = api.DigestEntry{
		Algorithm: alg,
		Digest:    digest,
		Timestamp: time.Now(),
	}

	// Advance next index with wraparound per SHIP spec
	r.next++
	if r.next >= r.maxSize {
		r.next = 0
	}
	
	// Save to persistence (ignore errors to maintain availability)
	// The pairing succeeds even if persistence fails
	if err := r.saveToPersistence(); err != nil {
		// TODO: Add logging when logging interface is available
		// For now, continue operation - replay protection still works in memory
		_ = err
	}
}

// GetCurrentEntry returns the most recently trusted device per SHIP spec section 11.4
func (r *RingBufferHistoryProvider) GetCurrentEntry() (*api.DigestEntry, error) {
	r.mux.RLock()
	defer r.mux.RUnlock()

	if r.next == 0 && r.entries[0].Algorithm == "" {
		return nil, api.ErrHistoryProviderNotSet // No entries yet
	}

	// Calculate current index per SHIP spec: current = next - 1
	current := r.next - 1
	if current < 0 {
		current = r.maxSize - 1
	}

	entry := r.entries[current]
	if entry.Algorithm == "" {
		return nil, api.ErrHistoryProviderNotSet // Entry not set
	}

	return &entry, nil
}
