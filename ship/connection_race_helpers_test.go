package ship

import (
	"sync"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/model"
)

// SafeConnectionTracker is a thread-safe implementation of ShipConnectionInfoProviderInterface
// specifically designed for testing concurrent operations without data races.
// It avoids the reflection issues that occur with testify mocks when handling
// objects containing sync primitives.
type SafeConnectionTracker struct {
	mu                    sync.Mutex
	connectionClosedCalls []ClosedCall
	handshakeStateUpdates []HandshakeStateUpdate
	otherMethodCalls      map[string][]interface{}

	// Configurable behaviors
	isRemoteServicePaired bool
	isAutoAcceptEnabled   bool
	allowWaitingForTrust  bool
}

// ClosedCall records a call to HandleConnectionClosed
type ClosedCall struct {
	RemoteSKI          string
	HandshakeCompleted bool
	Timestamp          time.Time
}

// HandshakeStateUpdate records a call to HandleShipHandshakeStateUpdate
type HandshakeStateUpdate struct {
	SKI       string
	State     model.ShipState
	Timestamp time.Time
}

// NewSafeConnectionTracker creates a new thread-safe connection tracker for testing
func NewSafeConnectionTracker() *SafeConnectionTracker {
	return &SafeConnectionTracker{
		connectionClosedCalls: make([]ClosedCall, 0),
		handshakeStateUpdates: make([]HandshakeStateUpdate, 0),
		otherMethodCalls:      make(map[string][]interface{}),
		isRemoteServicePaired: true, // Default to paired
		isAutoAcceptEnabled:   false,
		allowWaitingForTrust:  true,
	}
}

// HandleConnectionClosed is thread-safe and avoids reflection on the connection object
func (s *SafeConnectionTracker) HandleConnectionClosed(conn api.ShipConnectionInterface, handshakeCompleted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Extract only the data we need, avoiding any reflection on sync primitives
	remoteSKI := ""
	if conn != nil {
		remoteSKI = conn.RemoteSKI() // Safe method call
	}

	s.connectionClosedCalls = append(s.connectionClosedCalls, ClosedCall{
		RemoteSKI:          remoteSKI,
		HandshakeCompleted: handshakeCompleted,
		Timestamp:          time.Now(),
	})
}

// GetConnectionClosedCalls returns a copy of all connection closed calls (thread-safe)
func (s *SafeConnectionTracker) GetConnectionClosedCalls() []ClosedCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Return a copy to avoid races
	calls := make([]ClosedCall, len(s.connectionClosedCalls))
	copy(calls, s.connectionClosedCalls)
	return calls
}

// HandleShipHandshakeStateUpdate records handshake state updates (thread-safe)
func (s *SafeConnectionTracker) HandleShipHandshakeStateUpdate(ski string, state model.ShipState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.handshakeStateUpdates = append(s.handshakeStateUpdates, HandshakeStateUpdate{
		SKI:       ski,
		State:     state,
		Timestamp: time.Now(),
	})
}

// GetHandshakeStateUpdates returns a copy of all handshake state updates (thread-safe)
func (s *SafeConnectionTracker) GetHandshakeStateUpdates() []HandshakeStateUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()

	updates := make([]HandshakeStateUpdate, len(s.handshakeStateUpdates))
	copy(updates, s.handshakeStateUpdates)
	return updates
}

// IsRemoteServiceForSKIPaired implements the interface method (thread-safe)
func (s *SafeConnectionTracker) IsRemoteServiceForSKIPaired(ski string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordMethodCall("IsRemoteServiceForSKIPaired", ski)
	return s.isRemoteServicePaired
}

// AllowWaitingForTrust implements the interface method (thread-safe)
func (s *SafeConnectionTracker) AllowWaitingForTrust(ski string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordMethodCall("AllowWaitingForTrust", ski)
	return s.allowWaitingForTrust
}

// IsAutoAcceptEnabled implements the interface method (thread-safe)
func (s *SafeConnectionTracker) IsAutoAcceptEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordMethodCall("IsAutoAcceptEnabled")
	return s.isAutoAcceptEnabled
}

// ReportServiceShipID implements the interface method (thread-safe)
func (s *SafeConnectionTracker) ReportServiceShipID(ski string, shipID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordMethodCall("ReportServiceShipID", ski, shipID)
}

// SetupRemoteService implements the interface method (thread-safe)
func (s *SafeConnectionTracker) SetupRemoteService(ski string, writeI api.ShipConnectionDataWriterInterface) api.ShipConnectionDataReaderInterface {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordMethodCall("SetupRemoteDevice", ski)
	return nil // Return nil for test purposes
}

// recordMethodCall is a helper to record method calls
func (s *SafeConnectionTracker) recordMethodCall(method string, args ...interface{}) {
	if s.otherMethodCalls[method] == nil {
		s.otherMethodCalls[method] = make([]interface{}, 0)
	}
	s.otherMethodCalls[method] = append(s.otherMethodCalls[method], args)
}

// Configure allows setting behaviors for the tracker
func (s *SafeConnectionTracker) Configure(opts ...func(*SafeConnectionTracker)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, opt := range opts {
		opt(s)
	}
}

// Configuration helpers
func WithRemoteServicePaired(paired bool) func(*SafeConnectionTracker) {
	return func(s *SafeConnectionTracker) {
		s.isRemoteServicePaired = paired
	}
}

func WithAutoAcceptEnabled(enabled bool) func(*SafeConnectionTracker) {
	return func(s *SafeConnectionTracker) {
		s.isAutoAcceptEnabled = enabled
	}
}

func WithAllowWaitingForTrust(allow bool) func(*SafeConnectionTracker) {
	return func(s *SafeConnectionTracker) {
		s.allowWaitingForTrust = allow
	}
}

// GetMethodCallCount returns the number of times a method was called (thread-safe)
func (s *SafeConnectionTracker) GetMethodCallCount(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if calls, exists := s.otherMethodCalls[method]; exists {
		return len(calls)
	}
	return 0
}

// Reset clears all recorded calls (thread-safe)
func (s *SafeConnectionTracker) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.connectionClosedCalls = make([]ClosedCall, 0)
	s.handshakeStateUpdates = make([]HandshakeStateUpdate, 0)
	s.otherMethodCalls = make(map[string][]interface{})
}
