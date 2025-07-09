package hub

import (
	"time"
)

// connectionDelayTimer manages a cancellable timer for connection delays
type connectionDelayTimer struct {
	timer *time.Timer
	done  chan struct{}
}

// newConnectionDelayTimer creates a new cancellable timer
func newConnectionDelayTimer(duration time.Duration, f func()) *connectionDelayTimer {
	cdt := &connectionDelayTimer{
		done: make(chan struct{}),
	}

	cdt.timer = time.AfterFunc(duration, func() {
		select {
		case <-cdt.done:
			// Timer was cancelled, don't run the function
			return
		default:
			// Timer not cancelled, run the function
			f()
		}
	})

	return cdt
}

// Stop cancels the timer if it hasn't fired yet
func (cdt *connectionDelayTimer) Stop() bool {
	if cdt.timer.Stop() {
		// Timer was stopped before firing
		close(cdt.done)
		return true
	}
	// Timer already fired or was stopped
	return false
}

// storeConnectionDelayTimer stores a timer for a SKI, cancelling any existing timer
func (h *Hub) storeConnectionDelayTimer(ski string, timer *connectionDelayTimer) {
	h.muxTimers.Lock()
	defer h.muxTimers.Unlock()

	// Cancel any existing timer
	if existing, ok := h.connectionDelayTimers[ski]; ok {
		existing.Stop()
	}

	h.connectionDelayTimers[ski] = timer
}

// cancelConnectionDelayTimer cancels and removes a timer for a SKI
func (h *Hub) cancelConnectionDelayTimer(ski string) {
	h.muxTimers.Lock()
	defer h.muxTimers.Unlock()

	if timer, ok := h.connectionDelayTimers[ski]; ok {
		timer.Stop()
		delete(h.connectionDelayTimers, ski)
	}
}