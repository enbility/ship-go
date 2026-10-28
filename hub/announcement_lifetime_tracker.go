package hub

import (
	"sync"
	"time"

	"github.com/enbility/ship-go/logging"
)

// AnnouncementLifetimeTracker manages the 15-minute announcement lifetime after
// a successful SHIP connection, as required by the SHIP Pairing Service specification.
//
// This tracker supports multiple concurrent timers (one per target ShipID), since devZ
// may have active announcements to multiple devA devices simultaneously.
//
// Key features:
//   - Per-device timer management: independent 15-minute timers per ShipID
//   - Connection interruption handling: cancel timer if connection drops before expiry
//   - Thread-safe: all operations are protected by mutex
//   - Automatic cleanup: timer state cleared on expiry
//
// Example usage:
//
//	tracker := NewAnnouncementLifetimeTracker(time.Duration)
//
//	// On successful SHIP connection: start lifetime timer
//	tracker.StartLifetimeTimer(shipID, func(expiredShipID string) {
//	    hub.StopAnnouncementTo(expiredShipID)
//	})
//
//	// If connection drops before timeout: cancel the timer
//	tracker.CancelLifetimeTimer(shipID)

type AnnouncementLifetimeTracker struct {
	// timeout is the duration the connection must remain uninterrupted
	// before the announcement is stopped (default: 15 minutes per spec)
	timeout time.Duration

	// timers maps ShipID → active timer for that device
	timers map[string]lifetimeTimer

	// mutex protects concurrent access to tracker state
	mutex sync.RWMutex
}

// lifetimeTimer tracks the state of a single announcement lifetime timer
type lifetimeTimer struct {
	shipID string
	timer  *time.Timer
}

// NewAnnouncementLifetimeTracker creates a tracker with the timeout from the provided
// PairingConfig.AnnouncementLifetimeTimeout if set, otherwise defaults to 15 minutes per spec.
func NewAnnouncementLifetimeTracker(timeout time.Duration) *AnnouncementLifetimeTracker {
	// Out of bounds check
	if timeout < time.Minute || timeout > 15*time.Minute {
		timeout = 15 * time.Minute
	}
	return &AnnouncementLifetimeTracker{
		timeout: timeout,
		timers:  make(map[string]lifetimeTimer),
	}
}

// StartLifetimeTimer begins the announcement lifetime countdown for a specific device.
//
// When a SHIP connection is successfully established with a device, this method starts
// a timer. If the connection remains uninterrupted for the full duration (15 minutes),
// the onExpiry callback is invoked to stop the announcement.
//
// If a timer is already running for this ShipID (e.g., reconnection), the existing
// timer is replaced with a new one.
//
// Parameters:
//   - shipID: The SHIP ID of the connected device
//   - onExpiry: Callback invoked when the timer expires (typically calls StopAnnouncementTo)
//
// Thread-safety: This method is thread-safe.
func (t *AnnouncementLifetimeTracker) StartLifetimeTimer(shipID string, onExpiry func(expiredShipID string)) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	// Stop any existing timer for this device
	if _, ok := t.timers[shipID]; ok {
		t.removeLifetimeTimer(shipID)
		logging.Log().Debug("announcement lifetime timer reset", "shipID", shipID)
	}

	// Start new timer
	timer := time.AfterFunc(t.timeout, func() {
		// Capture shipID before clearing state
		expiredShipID := shipID

		// Clear state when timer expires
		t.mutex.Lock()
		t.removeLifetimeTimer(expiredShipID)
		t.mutex.Unlock()

		logging.Log().Debug("announcement lifetime expired, stopping announcement", "shipID", expiredShipID)

		// Call the expiry callback
		if onExpiry != nil {
			onExpiry(expiredShipID)
		}
	})

	t.timers[shipID] = lifetimeTimer{
		shipID: shipID,
		timer:  timer,
	}

	logging.Log().Debug("announcement lifetime timer started", "shipID", shipID, "duration", t.timeout)
}

// CancelLifetimeTimer cancels the announcement lifetime timer for a specific device.
//
// This should be called when a SHIP connection is interrupted (closed) before the
// 15-minute window completes or the announcement was withdrawn by the announcer.
// The announcement will continue to be active since the spec requires
// an uninterrupted connection for the full duration.
//
// Parameters:
//   - shipID: The SHIP ID of the device whose timer should be cancelled
//
// Thread-safety: This method is thread-safe.
func (t *AnnouncementLifetimeTracker) CancelLifetimeTimer(shipID string) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.removeLifetimeTimer(shipID)
	logging.Log().Debug("announcement lifetime timer cancelled (connection interrupted)",
		"shipID", shipID)
}

// IsTimerActive returns true if a lifetime timer is currently running for the
// specified device (i.e., the device is connected and the 15-minute countdown is in progress).
//
// A timer is considered active from the moment StartLifetimeTimer is called until
// either the timer expires (and the callback fires) or CancelLifetimeTimer is called.
//
// Parameters:
//   - shipID: The SHIP ID to check
//
// Thread-safety: This method is thread-safe.
func (t *AnnouncementLifetimeTracker) IsTimerActive(shipID string) bool {
	t.mutex.RLock()
	defer t.mutex.RUnlock()

	if shipID == "" {
		return false
	}

	_, exists := t.timers[shipID]
	return exists
}

// StopAll cancels all active lifetime timers.
// This should be called during Hub shutdown to prevent timer goroutines
// from firing after the Hub is torn down.
func (t *AnnouncementLifetimeTracker) StopAll() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	// First iterate and remove all timers to ensure cancellation and freeing the timers
	for shipID := range t.timers {
		t.removeLifetimeTimer(shipID)
		logging.Log().Debug("announcement lifetime timer stopped during shutdown", "shipID", shipID)
	}

	// Then free the map
	t.timers = make(map[string]lifetimeTimer)
}

func (t *AnnouncementLifetimeTracker) removeLifetimeTimer(shipID string) {
	existing, ok := t.timers[shipID]
	if !ok {
		return
	}

	if existing.timer != nil {
		existing.timer.Stop()
		existing.timer = nil
	}
	delete(t.timers, shipID)
}
