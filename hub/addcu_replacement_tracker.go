package hub

import (
	"sync"
	"time"

	"github.com/enbility/ship-go/logging"
)

// AddCuReplacementTracker manages the Device Replacement Timing Logic for AddCu devices.
//
// This tracker implements the 15-minute replacement detection window specified in the
// SHIP Pairing Service specification. It ensures that when an AddCu device disconnects,
// the system waits for a reasonable period before assuming the device has been replaced
// and reactivating the pairing listener.
//
// Key features:
// - Single device tracking: Only one device can be tracked at a time
// - 15-minute default timeout: Configurable via NewAddCuReplacementTrackerWithTimeout
// - Automatic cleanup: Timer resources are properly managed
// - Thread-safe: All operations are protected by mutex
//
// The tracker enforces a single device constraint because:
// - Only one control unit is typically present in a home
// - Device replacement is a sequential operation
// - This simplifies timer management and prevents resource leaks
//
// Example usage:
//
//	tracker := NewAddCuReplacementTracker()
//
//	// Start tracking when device disconnects
//	tracker.StartTimer(shipID, func(expiredShipID string) {
//	    log.Printf("Device %s timed out - assuming replacement", expiredShipID)
//	    removeDeviceTrust(expiredShipID)
//	    reactivatePairingListener()
//	})
//
//	// Cancel timer if device reconnects
//	if deviceReconnected {
//	    tracker.StopTimer(shipID)
//	}
//
//	// Check if currently tracking a device
//	if tracker.IsTracking(shipID) {
//	    log.Printf("Waiting for device %s to reconnect", shipID)
//	}
type AddCuReplacementTracker struct {
	// timeout is the duration to wait for replacement detection
	timeout time.Duration

	// pairedDeviceShipID identifies the currently tracked device
	pairedDeviceShipID string

	// disconnectionTime records when the timer was started
	disconnectionTime time.Time

	// timer handles the timeout callback
	timer *time.Timer

	// mutex protects concurrent access to tracker state
	mutex sync.RWMutex
}

// StartTimer begins tracking the specified device with a timeout callback.
//
// This method starts a 15-minute timer for the specified device. When the timer
// expires, the provided callback function is invoked with the shipID of the device
// that timed out, typically to remove trust and reactivate the pairing listener.
//
// Parameters:
// - shipID: The SHIP ID of the device to track
// - onTimeout: Callback function invoked when timer expires with the shipID (can be nil)
//
// Behavior:
// - If another device is already being tracked, it will be replaced
// - The timer runs for the configured timeout duration (default: 15 minutes)
// - The callback is invoked exactly once when the timer expires
// - The callback receives the shipID as a parameter for type-safe handling
// - Timer state is automatically cleaned up after expiry
//
// Thread-safety: This method is thread-safe and can be called concurrently.
func (t *AddCuReplacementTracker) StartTimer(shipID string, onTimeout func(expiredShipID string)) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	// Stop any existing timer first
	if t.timer != nil {
		t.timer.Stop()
	}

	// Set up new tracking state
	t.pairedDeviceShipID = shipID
	t.disconnectionTime = time.Now()

	// Create new timer with callback
	t.timer = time.AfterFunc(t.timeout, func() {
		// Capture shipID before clearing state to pass to callback
		expiredShipID := shipID

		// Clear state when timer expires
		t.mutex.Lock()
		defer t.mutex.Unlock()

		t.pairedDeviceShipID = ""
		t.disconnectionTime = time.Time{}
		t.timer = nil

		// Call the timeout callback if provided, passing the expired shipID
		if onTimeout != nil {
			onTimeout(expiredShipID)
		}
	})
}

// StopTimer stops tracking the specified device if it matches the currently tracked device.
//
// This method cancels the replacement timer for a device, preventing the timeout
// callback from being invoked. It should be called when a device reconnects
// within the 15-minute window.
//
// Parameters:
// - shipID: The SHIP ID of the device whose timer should be stopped
//
// Behavior:
// - Only stops the timer if shipID matches the currently tracked device
// - If shipID doesn't match, this method does nothing (no-op)
// - Clears all tracking state for the device
// - Prevents the timeout callback from being invoked
//
// Thread-safety: This method is thread-safe and can be called concurrently.
func (t *AddCuReplacementTracker) StopTimer(shipID string) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	// Only stop if this is the device we're tracking
	if t.pairedDeviceShipID != shipID {
		return
	}

	// Stop the timer if it exists
	if t.timer != nil {
		t.timer.Stop()
		logging.Log().Debug("AddCu replacement timer stopped", "shipID", shipID, "disconnectedFor", time.Since(t.disconnectionTime))
	}

	// Clear tracking state
	t.pairedDeviceShipID = ""
	t.disconnectionTime = time.Time{}
	t.timer = nil
}

// IsTracking returns true if the tracker is currently tracking the specified device.
//
// This method checks whether a timer is active for the given device, useful for
// debugging and monitoring the replacement detection state.
//
// Parameters:
// - shipID: The SHIP ID of the device to check
//
// Returns:
// - true if the device is being tracked with an active timer
// - false if the device is not being tracked or timer has expired
//
// Thread-safety: This method is thread-safe and can be called concurrently.
func (t *AddCuReplacementTracker) IsTracking(shipID string) bool {
	t.mutex.RLock()
	defer t.mutex.RUnlock()

	// Empty ShipID is never considered as "being tracked"
	// Even if tracker's pairedDeviceShipID is also empty (initial state)
	if shipID == "" {
		return false
	}

	return t.pairedDeviceShipID == shipID && t.pairedDeviceShipID != ""
}

// IsInReplacementWindow returns true if any AddCu device is currently being tracked.
//
// This method checks if there is an active replacement timer running, which indicates
// that SHIP pairing announcements should be ignored during the 15-minute window.
// When the timer expires, queued announcements will be processed via mDNS polling.
//
// Returns:
// - true if there is an active replacement timer (announcements should be ignored)
// - false if no replacement timer is active (process announcements normally)
//
// Thread-safety: This method is thread-safe and can be called concurrently.
func (t *AddCuReplacementTracker) IsInReplacementWindow() bool {
	t.mutex.RLock()
	defer t.mutex.RUnlock()

	// Check if we have an active timer for any device
	return t.pairedDeviceShipID != "" && t.timer != nil
}

// NewAddCuReplacementTracker creates a tracker with default 15-minute timeout.
//
// The 15-minute timeout is specified in the SHIP Pairing Service specification
// as a reasonable balance between user experience and security.
//
// Returns:
// - *AddCuReplacementTracker: A new tracker instance with 15-minute timeout
//
// Example:
//
//	tracker := NewAddCuReplacementTracker()
//	tracker.StartTimer(shipID, func(expiredShipID string) {
//	    log.Printf("Device %s timed out", expiredShipID)
//	})
func NewAddCuReplacementTracker() *AddCuReplacementTracker {
	return &AddCuReplacementTracker{
		timeout: 15 * time.Minute,
		mutex:   sync.RWMutex{},
	}
}

// NewAddCuReplacementTrackerWithTimeout creates a tracker with custom timeout.
//
// This constructor allows customization of the timeout duration, useful for
// testing or special deployment scenarios.
//
// Parameters:
// - timeout: Duration to wait before considering device replaced
//
// Returns:
// - *AddCuReplacementTracker: A new tracker instance with specified timeout
//
// Example:
//
//	// Create tracker with 5-minute timeout for testing
//	tracker := NewAddCuReplacementTrackerWithTimeout(5 * time.Minute)
//	tracker.StartTimer(shipID, func(expiredShipID string) {
//	    handleTimeout(expiredShipID)
//	})
func NewAddCuReplacementTrackerWithTimeout(timeout time.Duration) *AddCuReplacementTracker {
	return &AddCuReplacementTracker{
		timeout: timeout,
		mutex:   sync.RWMutex{},
	}
}
