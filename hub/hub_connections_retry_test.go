package hub

import (
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

func TestHubConnectionsRetrySuite(t *testing.T) {
	suite.Run(t, new(HubConnectionsRetrySuite))
}

type HubConnectionsRetrySuite struct {
	suite.Suite

	hubReader   *mocks.MockHubReaderInterface
	mdnsService *mocks.MockMdnsInterface
	shipConnection *mocks.ShipConnectionInterface
	wsDataWriter   *mocks.WebsocketDataWriterInterface
	remoteSki string
	sut *Hub
}

func (s *HubConnectionsRetrySuite) BeforeTest(suiteName, testName string) {
	s.remoteSki = "remotetestski"

	ctrl := gomock.NewController(s.T())

	s.hubReader = mocks.NewMockHubReaderInterface(ctrl)
	s.hubReader.EXPECT().RemoteSKIConnected(gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().RemoteSKIDisconnected(gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().ServiceShipIDUpdate(gomock.Any(), gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().ServicePairingDetailUpdate(gomock.Any(), gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().AllowWaitingForTrust(gomock.Any()).Return(false).AnyTimes()

	s.mdnsService = mocks.NewMockMdnsInterface(ctrl)
	s.mdnsService.EXPECT().AnnounceMdnsEntry().Return(nil).AnyTimes()
	s.mdnsService.EXPECT().UnannounceMdnsEntry().Return().AnyTimes()
	s.mdnsService.EXPECT().RequestMdnsEntries().Return().AnyTimes()

	s.wsDataWriter = mocks.NewWebsocketDataWriterInterface(s.T())

	s.shipConnection = mocks.NewShipConnectionInterface(s.T())
	s.shipConnection.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	s.shipConnection.EXPECT().RemoteSKI().Return(s.remoteSki).Maybe()
	s.shipConnection.EXPECT().ApprovePendingHandshake().Return().Maybe()
	s.shipConnection.EXPECT().AbortPendingHandshake().Return().Maybe()
	s.shipConnection.EXPECT().DataHandler().Return(s.wsDataWriter).Maybe()
	s.shipConnection.EXPECT().ShipHandshakeState().Return(model.SmeStateComplete, nil).Maybe()

	localService := api.NewServiceDetails("localSKI")
	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	s.sut = NewHub(s.hubReader, s.mdnsService, 4567, certificate, localService)
}

func (s *HubConnectionsRetrySuite) AfterTest(suiteName, testName string) {
	s.mdnsService.EXPECT().Shutdown().AnyTimes()
	s.sut.Shutdown()
}

func (s *HubConnectionsRetrySuite) Test_IncreaseConnectionAttemptCounter() {
	// Test that the counter increments properly
	counter1 := s.sut.increaseConnectionAttemptCounter(s.remoteSki)
	assert.Equal(s.T(), 0, counter1)

	counter2 := s.sut.increaseConnectionAttemptCounter(s.remoteSki)
	assert.Equal(s.T(), 1, counter2)

	// Verify it exists
	currentCounter, exists := s.sut.getCurrentConnectionAttemptCounter(s.remoteSki)
	assert.True(s.T(), exists)
	assert.Equal(s.T(), 1, currentCounter)
}

func (s *HubConnectionsRetrySuite) Test_RemoveConnectionAttemptCounter() {
	s.sut.increaseConnectionAttemptCounter(s.remoteSki)
	_, exists := s.sut.getCurrentConnectionAttemptCounter(s.remoteSki)
	assert.Equal(s.T(), true, exists)

	s.sut.removeConnectionAttemptCounter(s.remoteSki)
	_, exists = s.sut.getCurrentConnectionAttemptCounter(s.remoteSki)
	assert.Equal(s.T(), false, exists)
}

func (s *HubConnectionsRetrySuite) Test_GetCurrentConnectionAttemptCounter() {
	s.sut.increaseConnectionAttemptCounter(s.remoteSki)
	_, exists := s.sut.connectionAttemptCounter[s.remoteSki]
	assert.True(s.T(), exists)
	s.sut.increaseConnectionAttemptCounter(s.remoteSki)

	value, exists := s.sut.getCurrentConnectionAttemptCounter(s.remoteSki)
	assert.Equal(s.T(), 1, value)
	assert.Equal(s.T(), true, exists)
}

func (s *HubConnectionsRetrySuite) Test_ConnectionAttemptRunning() {
	_, ok := s.sut.tryBeginConnectionAttempt(s.remoteSki)
	assert.True(s.T(), ok)
	status := s.sut.isConnectionAttemptRunning(s.remoteSki)
	assert.Equal(s.T(), true, status)
	s.sut.forceResetConnectionAttempt(s.remoteSki)
	status = s.sut.isConnectionAttemptRunning(s.remoteSki)
	assert.Equal(s.T(), false, status)
}

// Test_CompareAndResetConnectionAttempt_CleansUpMapEntries verifies that
// compareAndResetConnectionAttempt deletes the map entries for the SKI rather
// than leaving them with a zero/false value. This prevents unbounded map growth
// when many transient SKIs connect and disconnect over the hub's lifetime.
func (s *HubConnectionsRetrySuite) Test_CompareAndResetConnectionAttempt_CleansUpMapEntries() {
	generation, ok := s.sut.tryBeginConnectionAttempt(s.remoteSki)
	assert.True(s.T(), ok)

	// Precondition: keys exist
	s.sut.muxConAttempt.RLock()
	_, runningExists := s.sut.connectionAttemptRunning[s.remoteSki]
	_, genExists := s.sut.connectionAttemptGeneration[s.remoteSki]
	s.sut.muxConAttempt.RUnlock()
	assert.True(s.T(), runningExists, "running key must exist after tryBeginConnectionAttempt")
	assert.True(s.T(), genExists, "generation key must exist after tryBeginConnectionAttempt")

	// Matching generation → should delete both keys
	s.sut.compareAndResetConnectionAttempt(s.remoteSki, generation)

	s.sut.muxConAttempt.RLock()
	_, runningExists = s.sut.connectionAttemptRunning[s.remoteSki]
	_, genExists = s.sut.connectionAttemptGeneration[s.remoteSki]
	s.sut.muxConAttempt.RUnlock()
	assert.False(s.T(), runningExists, "running key must be deleted, not just set to false")
	assert.False(s.T(), genExists, "generation key must be deleted after reset")
}

// Test_CompareAndResetConnectionAttempt_MismatchLeavesState verifies that
// compareAndResetConnectionAttempt with a non-matching generation leaves
// the existing map entries untouched.
func (s *HubConnectionsRetrySuite) Test_CompareAndResetConnectionAttempt_MismatchLeavesState() {
	generation, ok := s.sut.tryBeginConnectionAttempt(s.remoteSki)
	assert.True(s.T(), ok)

	// Call with wrong generation
	s.sut.compareAndResetConnectionAttempt(s.remoteSki, generation+999)

	// Keys must still exist and be unchanged
	assert.True(s.T(), s.sut.isConnectionAttemptRunning(s.remoteSki),
		"running flag must remain true when generation mismatches")

	s.sut.muxConAttempt.RLock()
	storedGen := s.sut.connectionAttemptGeneration[s.remoteSki]
	s.sut.muxConAttempt.RUnlock()
	assert.Equal(s.T(), generation, storedGen,
		"generation must remain unchanged when compareAndReset mismatches")
}

// Test_PrepareConnectionInitation_CounterMismatch_NoCounter_ResetsFlag verifies that
// when prepareConnectionInitation returns early due to a counter mismatch
// (counter was removed or changed), the connectionAttemptRunning flag is
// reset to false.
//
// This covers the race condition where cleanupRemovedMdnsEntries removes the
// counter but the timer callback still fires.
func (s *HubConnectionsRetrySuite) Test_PrepareConnectionInitation_CounterMismatch_NoCounter_ResetsFlag() {
	entry := &api.MdnsEntry{Name: "EVSE", Ski: s.remoteSki, Identifier: "EVSE1"}

	// Simulate: coordinateConnectionInitations set the flag to true
	generation, _ := s.sut.tryBeginConnectionAttempt(s.remoteSki)

	// Counter does NOT exist (was removed by cleanup)
	// Call prepareConnectionInitation with counter=0 — will mismatch since no counter exists
	s.sut.prepareConnectionInitation(s.remoteSki, 0, generation, entry)

	// The flag MUST be reset to false after early return
	assert.False(s.T(), s.sut.isConnectionAttemptRunning(s.remoteSki),
		"connectionAttemptRunning must be reset to false after counter-mismatch early return (no counter exists)")
}

// Test_PrepareConnectionInitation_CounterMismatch_DifferentValue_ResetsFlag
// verifies the flag is reset when the stored counter differs from the one
// passed to prepareConnectionInitation (e.g., a new attempt incremented it).
func (s *HubConnectionsRetrySuite) Test_PrepareConnectionInitation_CounterMismatch_DifferentValue_ResetsFlag() {
	entry := &api.MdnsEntry{Name: "EVSE", Ski: s.remoteSki, Identifier: "EVSE1"}

	// Simulate: coordinateConnectionInitations set the flag to true
	generation, _ := s.sut.tryBeginConnectionAttempt(s.remoteSki)

	// Counter exists but has a different value than what the timer was created with
	s.sut.muxConAttempt.Lock()
	s.sut.connectionAttemptCounter[s.remoteSki] = 5
	s.sut.muxConAttempt.Unlock()

	// Call with stale counter=2 — will mismatch
	s.sut.prepareConnectionInitation(s.remoteSki, 2, generation, entry)

	// The flag MUST be reset to false after early return
	assert.False(s.T(), s.sut.isConnectionAttemptRunning(s.remoteSki),
		"connectionAttemptRunning must be reset to false after counter-mismatch early return (different counter value)")
}

// Test_PrepareConnectionInitation_NotPaired_ResetsFlag verifies that when
// prepareConnectionInitation returns early because the device is no longer
// paired, the connectionAttemptRunning flag is reset to false.
//
// This covers the scenario where a device is unpaired while a connection
// delay timer is pending.
func (s *HubConnectionsRetrySuite) Test_PrepareConnectionInitation_NotPaired_ResetsFlag() {
	entry := &api.MdnsEntry{Name: "EVSE", Ski: s.remoteSki, Identifier: "EVSE1"}

	// Simulate: coordinateConnectionInitations set the flag to true
	generation, _ := s.sut.tryBeginConnectionAttempt(s.remoteSki)

	// Set a matching counter so we pass the counter check
	s.sut.increaseConnectionAttemptCounter(s.remoteSki)

	// Ensure the device is NOT paired (ServiceForSKI creates a new untrusted service)
	// The default ServiceDetails has trusted=false, so IsRemoteServiceForSKIPaired returns false
	service := s.sut.ServiceForSKI(s.remoteSki)
	service.SetTrusted(false)

	// Call prepareConnectionInitation — should return early at the "not paired" check
	s.sut.prepareConnectionInitation(s.remoteSki, 0, generation, entry)

	// The flag MUST be reset to false after early return
	assert.False(s.T(), s.sut.isConnectionAttemptRunning(s.remoteSki),
		"connectionAttemptRunning must be reset to false after not-paired early return")
}

// Test_PrepareConnectionInitation_AlreadyConnected_ResetsFlag verifies that
// when prepareConnectionInitation returns early because the device is already
// connected (e.g., via an inbound connection), the connectionAttemptRunning
// flag is reset to false.
//
// This covers the scenario where the remote device initiates a connection
// to us while our outbound connection delay timer is still pending.
func (s *HubConnectionsRetrySuite) Test_PrepareConnectionInitation_AlreadyConnected_ResetsFlag() {
	entry := &api.MdnsEntry{Name: "EVSE", Ski: s.remoteSki, Identifier: "EVSE1"}

	// Simulate: coordinateConnectionInitations set the flag to true
	generation, _ := s.sut.tryBeginConnectionAttempt(s.remoteSki)

	// Set a matching counter so we pass the counter check
	s.sut.increaseConnectionAttemptCounter(s.remoteSki)

	// Device IS paired (so we pass the pairing check)
	service := s.sut.ServiceForSKI(s.remoteSki)
	service.SetTrusted(true)

	// Device is already connected (inbound connection was established)
	s.sut.muxCon.Lock()
	s.sut.connections[s.remoteSki] = s.shipConnection
	s.sut.muxCon.Unlock()

	// Call prepareConnectionInitation — should return early at the "already connected" check
	s.sut.prepareConnectionInitation(s.remoteSki, 0, generation, entry)

	// The flag MUST be reset to false after early return
	assert.False(s.T(), s.sut.isConnectionAttemptRunning(s.remoteSki),
		"connectionAttemptRunning must be reset to false after already-connected early return")
}

// Test_PrepareConnectionInitation_EarlyReturn_DoesNotBlock_FutureAttempts is an
// integration-style test that verifies the end-to-end scenario: after an early
// return in prepareConnectionInitation, coordinateConnectionInitations must be
// able to start a new connection attempt (not blocked by stale flag).
func (s *HubConnectionsRetrySuite) Test_PrepareConnectionInitation_EarlyReturn_DoesNotBlock_FutureAttempts() {
	entry := &api.MdnsEntry{Name: "EVSE", Ski: s.remoteSki, Identifier: "EVSE1"}

	// Simulate a connection attempt that will hit the counter-mismatch early return
	generation, _ := s.sut.tryBeginConnectionAttempt(s.remoteSki)
	// No counter set — will cause mismatch

	s.sut.prepareConnectionInitation(s.remoteSki, 0, generation, entry)

	// After the early return, coordinateConnectionInitations must NOT be blocked
	// It checks isConnectionAttemptRunning — which must be false now
	assert.False(s.T(), s.sut.isConnectionAttemptRunning(s.remoteSki),
		"isConnectionAttemptRunning must be false so coordinateConnectionInitations can proceed")

	// Verify coordinateConnectionInitations can actually start a new attempt
	// (it should set the flag to true and create a timer)
	s.sut.coordinateConnectionInitations(s.remoteSki, entry)

	assert.True(s.T(), s.sut.isConnectionAttemptRunning(s.remoteSki),
		"coordinateConnectionInitations must be able to start a new attempt after early return")

	// Clean up the timer
	s.sut.cancelConnectionDelayTimer(s.remoteSki)
}

// Test_PrepareConnectionInitation_TimerRace_CounterRemoved simulates the exact
// race condition from the analysis: the timer fires and enters
// prepareConnectionInitation just as cleanupRemovedMdnsEntries removes the
// counter. The flag must still be reset.
func (s *HubConnectionsRetrySuite) Test_PrepareConnectionInitation_TimerRace_CounterRemoved() {
	entry := &api.MdnsEntry{Name: "EVSE", Ski: s.remoteSki, Identifier: "EVSE1"}

	// Step 1: Start a connection attempt (simulating coordinateConnectionInitations)
	generation, _ := s.sut.tryBeginConnectionAttempt(s.remoteSki)
	counter := s.sut.increaseConnectionAttemptCounter(s.remoteSki)

	// Step 2: Simulate cleanup removing the counter (as cleanupRemovedMdnsEntries would)
	s.sut.removeConnectionAttemptCounter(s.remoteSki)

	// Step 3: Timer fires — prepareConnectionInitation runs with the original counter
	s.sut.prepareConnectionInitation(s.remoteSki, counter, generation, entry)

	// The flag MUST be reset despite the race
	assert.False(s.T(), s.sut.isConnectionAttemptRunning(s.remoteSki),
		"connectionAttemptRunning must be reset even when counter was removed by concurrent cleanup")
}

// Test_StaleCallback_Resets_NewAttempt_Flag reproduces the race condition where
// a stale timer callback (T1) from a previous connection attempt incorrectly
// resets the connectionAttemptRunning flag that was set by a new, legitimate
// attempt (T2).
//
// Sequence:
//  1. Device appears → flag=true, counter=0, timer T1 created
//  2. Device disappears → cleanup: removes counter, resets flag
//  3. Device reappears → flag=true, counter=0 (same value!), timer T2 created
//  4. Stale T1 callback runs prepareConnectionInitation(ski, 0, oldEntry):
//     - Counter matches (both 0) → passes the counter guard
//     - Device not paired → early return
//     - defer sets flag=false → INCORRECTLY cancels T2's attempt
//
// This test FAILS with the current code because:
//   - removeConnectionAttemptCounter deletes the key, so the new attempt
//     restarts at 0 — identical to the stale callback's counter
//   - The defer in prepareConnectionInitation unconditionally resets the flag
func (s *HubConnectionsRetrySuite) Test_StaleCallback_Resets_NewAttempt_Flag() {
	ski := s.remoteSki
	entry := &api.MdnsEntry{Name: "EVSE", Ski: ski, Identifier: "EVSE1"}

	// Ensure the device is NOT paired so the stale callback hits the
	// "not paired" early return (avoids actually initiating a connection)
	service := s.sut.ServiceForSKI(ski)
	service.SetTrusted(false)

	// Step 1: First connection attempt (simulates coordinateConnectionInitations)
	staleGeneration, _ := s.sut.tryBeginConnectionAttempt(ski)
	staleCounter := s.sut.increaseConnectionAttemptCounter(ski) // returns 0

	// Step 2: Device disappears — cleanup runs
	// (simulates cleanupRemovedMdnsEntries)
	s.sut.removeConnectionAttemptCounter(ski)
	s.sut.forceResetConnectionAttempt(ski)

	// Step 3: Device reappears — new connection attempt starts
	_, _ = s.sut.tryBeginConnectionAttempt(ski)
	_ = s.sut.increaseConnectionAttemptCounter(ski) // returns 0 again (counter was deleted)

	// Step 4: Stale T1 callback finally executes with old generation
	s.sut.prepareConnectionInitation(ski, staleCounter, staleGeneration, entry)

	// The flag MUST still be true — the new T2 attempt is active and must not
	// be cancelled by a stale callback from a previous attempt.
	assert.True(s.T(), s.sut.isConnectionAttemptRunning(ski),
		"stale callback must NOT reset the flag belonging to a newer connection attempt")
}

// Test_PrepareConnectionInitation_FullLifecycle_TimerFires_EarlyReturn_ResetsFlag verifies the full
// lifecycle through coordinateConnectionInitations: the flag is set, the timer
// fires, prepareConnectionInitation runs (hitting an early return), and the flag
// is properly reset.
func (s *HubConnectionsRetrySuite) Test_PrepareConnectionInitation_FullLifecycle_TimerFires_EarlyReturn_ResetsFlag() {
	entry := &api.MdnsEntry{Name: "EVSE", Ski: s.remoteSki, Identifier: "EVSE1"}

	// Device is NOT paired — so prepareConnectionInitation will return early
	service := s.sut.ServiceForSKI(s.remoteSki)
	service.SetTrusted(false)

	// Start the connection attempt through the real entry point
	s.sut.coordinateConnectionInitations(s.remoteSki, entry)

	// Flag should be true now (set by coordinateConnectionInitations)
	assert.True(s.T(), s.sut.isConnectionAttemptRunning(s.remoteSki),
		"flag should be true immediately after coordinateConnectionInitations")

	// Wait for the timer to fire (connection delay times are in seconds,
	// but the minimum range starts at 0s so we need to wait a reasonable time)
	assert.Eventually(s.T(), func() bool {
		return !s.sut.isConnectionAttemptRunning(s.remoteSki)
	}, 5*time.Second, 100*time.Millisecond,
		"connectionAttemptRunning must be reset to false after timer fires and prepareConnectionInitation returns early (not paired)")
}

// Test_CoordinateConnectionInitations_TOCTOU_OnlyOneAttemptAllowed tests that
// concurrent calls to coordinateConnectionInitations for the same SKI result in
// exactly ONE connection attempt being started.
//
// The TOCTOU vulnerability in coordinateConnectionInitations:
//
//	if h.isConnectionAttemptRunning(ski) {          // RLock, check, RUnlock  ← CHECK
//	    return
//	}
//	generation := h.tryBeginConnectionAttempt(ski)  // Lock, set, Unlock    ← USE
//
// Between CHECK and USE, another goroutine can also see running=false and proceed.
// This results in multiple tryBeginConnectionAttempt calls, observable via generation > 1.
//
// This test FAILS with the current code because the check-then-set is not atomic.
// It should PASS once coordinateConnectionInitations uses an atomic
// tryBeginConnectionAttempt that checks and sets under a single lock.
func (s *HubConnectionsRetrySuite) Test_CoordinateConnectionInitations_TOCTOU_OnlyOneAttemptAllowed() {
	ski := s.remoteSki
	entry := &api.MdnsEntry{Name: "EVSE", Ski: ski, Identifier: "EVSE1"}

	const goroutines = 20
	const iterations = 200

	for iter := 0; iter < iterations; iter++ {
		// Reset state for this iteration
		s.sut.muxConAttempt.Lock()
		delete(s.sut.connectionAttemptRunning, ski)
		delete(s.sut.connectionAttemptGeneration, ski)
		delete(s.sut.connectionAttemptCounter, ski)
		genBefore := s.sut.connectionAttemptGenCounter
		s.sut.muxConAttempt.Unlock()
		s.sut.cancelConnectionDelayTimer(ski)

		// Barrier: all goroutines start at the same instant
		var ready sync.WaitGroup
		ready.Add(goroutines)
		start := make(chan struct{})

		var wg sync.WaitGroup
		wg.Add(goroutines)

		for g := 0; g < goroutines; g++ {
			go func() {
				defer wg.Done()
				ready.Done()
				<-start // all goroutines block here until the barrier is released
				s.sut.coordinateConnectionInitations(ski, entry)
			}()
		}

		ready.Wait() // wait for all goroutines to reach the barrier
		close(start) // release all goroutines simultaneously
		wg.Wait()    // wait for all goroutines to finish

		s.sut.muxConAttempt.RLock()
		genAfter := s.sut.connectionAttemptGenCounter
		s.sut.muxConAttempt.RUnlock()

		// Exactly one goroutine should have started an attempt.
		// A delta > 1 means multiple goroutines called tryBeginConnectionAttempt.
		bumps := genAfter - genBefore
		if bumps > 1 {
			s.sut.cancelConnectionDelayTimer(ski)
			s.T().Fatalf("iteration %d: global counter bumped %d times — %d goroutines passed through "+
				"coordinateConnectionInitations concurrently for the same SKI; "+
				"expected exactly 1", iter, bumps, bumps)
		}

		s.sut.cancelConnectionDelayTimer(ski)
	}
}
