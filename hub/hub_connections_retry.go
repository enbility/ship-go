package hub

import (
	"math/rand"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
)

// coordinateConnectionInitations coordinates connection initiation attempts to a remote service
func (h *Hub) coordinateConnectionInitations(ski string, entry *api.MdnsEntry) {
	generation, ok := h.tryBeginConnectionAttempt(ski)
	if !ok {
		return
	}

	counter, duration := h.getConnectionInitiationDelayTime(ski)

	logging.Log().Debugf("delaying connection to %s by %s to minimize double connection probability", ski, duration)

	// Create a cancellable timer
	timer := newConnectionDelayTimer(duration, func() {
		h.prepareConnectionInitation(ski, counter, generation, entry)
	})

	// Store the timer so it can be cancelled if needed
	h.storeConnectionDelayTimer(ski, timer)
}

// prepareConnectionInitation is invoked by coordinateConnectionInitations either with a delay or directly
// when initiating a pairing process
func (h *Hub) prepareConnectionInitation(ski string, counter int, generation uint64, entry *api.MdnsEntry) {
	defer h.compareAndResetConnectionAttempt(ski, generation)

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

// tryBeginConnectionAttempt atomically checks whether a connection attempt is
// already running for the given SKI and, only if not, sets the running flag and
// assigns a globally unique generation. Performing the check and the set under
// a single lock eliminates the TOCTOU window that would exist if the check and
// set were separate lock acquisitions.
//
// The generation is drawn from a hub-wide monotonic counter so that values are
// never reused, even after a SKI's map entries have been deleted by cleanup.
//
// Returns (generation, true) if this caller won the race and should proceed,
// or (0, false) if an attempt is already running.
func (h *Hub) tryBeginConnectionAttempt(ski string) (uint64, bool) {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	if h.connectionAttemptRunning[ski] {
		return 0, false
	}

	h.connectionAttemptGenCounter++
	h.connectionAttemptRunning[ski] = true
	h.connectionAttemptGeneration[ski] = h.connectionAttemptGenCounter
	return h.connectionAttemptGenCounter, true
}

// forceResetConnectionAttempt deletes the running flag and generation for the
// given SKI. Any in-flight stale timer callback (which carries an older,
// non-zero generation) will see generation 0 (missing key) and no-op in
// compareAndResetConnectionAttempt. If the SKI reappears later,
// tryBeginConnectionAttempt draws from the global monotonic counter, so the new
// generation is guaranteed to differ from any stale callback's generation.
func (h *Hub) forceResetConnectionAttempt(ski string) {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	delete(h.connectionAttemptRunning, ski)
	delete(h.connectionAttemptGeneration, ski)
}

// compareAndResetConnectionAttempt atomically checks whether the given
// generation is still current and, only if so, resets the running flag.
// Performing both under a single lock eliminates the TOCTOU window that
// would exist if the check and reset were separate lock acquisitions.
func (h *Hub) compareAndResetConnectionAttempt(ski string, generation uint64) {
	h.muxConAttempt.Lock()
	defer h.muxConAttempt.Unlock()

	if h.connectionAttemptGeneration[ski] == generation {
		delete(h.connectionAttemptRunning, ski)
		delete(h.connectionAttemptGeneration, ski)
	}
}
