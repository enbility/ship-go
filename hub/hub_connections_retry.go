package hub

import (
	"math/rand"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
)

// coordinateConnectionInitations coordinates connection initiation attempts to a remote service
func (h *Hub) coordinateConnectionInitations(ski string, entry *api.MdnsEntry) {
	if h.isConnectionAttemptRunning(ski) {
		return
	}

	h.setConnectionAttemptRunning(ski, true)

	counter, duration := h.getConnectionInitiationDelayTime(ski)

	logging.Log().Debugf("delaying connection to %s by %s to minimize double connection probability", ski, duration)

	// Create a cancellable timer
	timer := newConnectionDelayTimer(duration, func() {
		h.prepareConnectionInitation(ski, counter, entry)
	})

	// Store the timer so it can be cancelled if needed
	h.storeConnectionDelayTimer(ski, timer)
}

// prepareConnectionInitation is invoked by coordinateConnectionInitations either with a delay or directly
// when initiating a pairing process
func (h *Hub) prepareConnectionInitation(ski string, counter int, entry *api.MdnsEntry) {
	// Hub is shutting down — do nothing. The timer fired between Shutdown's
	// cancel and the timer's Stop, so we must not touch any hub state.
	if h.ctx.Err() != nil {
		return
	}

	// Self-remove our timer entry from connectionDelayTimers. Without this, fired
	// timers leak entries forever (only Stop deletes), causing slow map growth.
	h.cancelConnectionDelayTimer(ski)

	// The running flag was set in coordinateConnectionInitations before scheduling
	// this callback. Clear it on every exit path so a future mDNS event for the
	// same SKI can schedule a new attempt; without this defer, the early returns
	// below would leak the flag and silently block reconnects.
	defer h.setConnectionAttemptRunning(ski, false)

	// check if the current counter is still the same, otherwise this counter is irrelevant
	currentCounter, exists := h.getCurrentConnectionAttemptCounter(ski)
	if !exists || currentCounter != counter {
		return
	}

	// connection attempt is not relevant if the device is no longer paired
	// or it is not queued for pairing
	if !h.IsRemoteServiceForSKIPaired(ski) {
		return
	}

	// connection attempt is not relevant if the device is already connected
	if h.isSkiConnected(ski) {
		return
	}

	// now initiate the connection
	// check if the remoteService still exists
	service := h.ServiceForSKI(ski)

	if success := h.initateConnection(service, entry); !success {
		h.checkAutoReannounce()
	}
}

// increaseConnectionAttemptCounter increases the connection attempt counter for the given ski
func (h *Hub) increaseConnectionAttemptCounter(ski string) int {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	currentCounter := 0
	if counter, exists := h.connectionAttemptCounter[ski]; exists {
		currentCounter = counter + 1

		if currentCounter >= len(connectionInitiationDelayTimeRanges)-1 {
			currentCounter = len(connectionInitiationDelayTimeRanges) - 1
		}
	}

	h.connectionAttemptCounter[ski] = currentCounter

	return currentCounter
}

// removeConnectionAttemptCounter removes the connection attempt counter for the given ski
func (h *Hub) removeConnectionAttemptCounter(ski string) {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	delete(h.connectionAttemptCounter, ski)
}

// getCurrentConnectionAttemptCounter gets the current attempt counter
func (h *Hub) getCurrentConnectionAttemptCounter(ski string) (int, bool) {
	h.muxConAttempt.RLock()
	defer h.muxConAttempt.RUnlock()

	counter, exists := h.connectionAttemptCounter[ski]

	return counter, exists
}

// getConnectionInitiationDelayTime gets the connection initiation delay time range for a given ski
// returns the current counter and the duration
func (h *Hub) getConnectionInitiationDelayTime(ski string) (int, time.Duration) {
	counter := h.increaseConnectionAttemptCounter(ski)

	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	timeRange := connectionInitiationDelayTimeRanges[counter]

	// get range in Milliseconds
	minRange := timeRange.min * 1000
	maxRange := timeRange.max * 1000

	// #nosec G404
	duration := rand.Intn(maxRange-minRange) + minRange

	return counter, time.Duration(duration) * time.Millisecond
}

// setConnectionAttemptRunning sets if a connection attempt is running/in progress
func (h *Hub) setConnectionAttemptRunning(ski string, active bool) {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	h.connectionAttemptRunning[ski] = active
}

// isConnectionAttemptRunning returns if a connection attempt is running/in progress
func (h *Hub) isConnectionAttemptRunning(ski string) bool {
	h.muxConAttempt.RLock()
	defer h.muxConAttempt.RUnlock()

	running, exists := h.connectionAttemptRunning[ski]
	if !exists {
		return false
	}

	return running
}
