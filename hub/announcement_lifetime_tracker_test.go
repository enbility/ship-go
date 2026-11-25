package hub

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// =============================================================================
// ANNOUNCEMENT LIFETIME TRACKER TEST SUITE
// =============================================================================

func TestAnnouncementLifetimeTrackerSuite(t *testing.T) {
	suite.Run(t, new(AnnouncementLifetimeTrackerSuite))
}

type AnnouncementLifetimeTrackerSuite struct {
	suite.Suite

	tracker *AnnouncementLifetimeTracker
}

func (s *AnnouncementLifetimeTrackerSuite) BeforeTest(suiteName, testName string) {
	s.tracker = nil
}

func (s *AnnouncementLifetimeTrackerSuite) AfterTest(suiteName, testName string) {
	if s.tracker != nil {
		s.tracker.StopAll()
	}
}

func newAnnouncementLifetimeTrackerWithCustomTimeout(timeout time.Duration) *AnnouncementLifetimeTracker {
	return &AnnouncementLifetimeTracker{
		timeout: timeout,
		timers:  make(map[string]lifetimeTimer),
	}
}

// =============================================================================
// TESTS - CONSTRUCTION
// =============================================================================

func (s *AnnouncementLifetimeTrackerSuite) TestNewTracker() {
	s.Run("default timeout is 15 minutes", func() {
		s.tracker = NewAnnouncementLifetimeTracker(15*time.Minute)
		require.NotNil(s.T(), s.tracker)
		assert.Equal(s.T(), 15*time.Minute, s.tracker.timeout)
	})

	s.Run("out of bound test", func() {
		s.tracker = NewAnnouncementLifetimeTracker(20*time.Minute)
		require.NotNil(s.T(), s.tracker)
		assert.Equal(s.T(), 15*time.Minute, s.tracker.timeout)
	})

	s.Run("custom timeout", func() {
		s.tracker = newAnnouncementLifetimeTrackerWithCustomTimeout(100 * time.Millisecond)
		assert.Equal(s.T(), 100*time.Millisecond, s.tracker.timeout)
	})
}

// =============================================================================
// TESTS - START AND EXPIRY
// =============================================================================

func (s *AnnouncementLifetimeTrackerSuite) TestStartLifetimeTimer() {
	s.Run("callback fires on expiry with correct shipID and cleans up", func() {
		s.tracker = newAnnouncementLifetimeTrackerWithCustomTimeout(50 * time.Millisecond)
		shipID := "test-ship-1"

		var callbackShipID string
		var wg sync.WaitGroup
		wg.Add(1)

		s.tracker.StartLifetimeTimer(shipID, func(expiredShipID string) {
			callbackShipID = expiredShipID
			wg.Done()
		})

		assert.True(s.T(), s.tracker.IsTimerActive(shipID))

		wg.Wait()

		assert.Equal(s.T(), shipID, callbackShipID)
		assert.False(s.T(), s.tracker.IsTimerActive(shipID))
	})

	s.Run("replaces existing timer for same device", func() {
		s.tracker = newAnnouncementLifetimeTrackerWithCustomTimeout(5 * time.Second)
		shipID := "test-ship-1"

		s.tracker.StartLifetimeTimer(shipID, func(expiredShipID string) {})
		s.tracker.StartLifetimeTimer(shipID, func(expiredShipID string) {})

		s.tracker.mutex.RLock()
		assert.Len(s.T(), s.tracker.timers, 1)
		s.tracker.mutex.RUnlock()
	})

	s.Run("supports multiple devices simultaneously", func() {
		s.tracker = newAnnouncementLifetimeTrackerWithCustomTimeout(5 * time.Second)

		s.tracker.StartLifetimeTimer("ship-1", func(expiredShipID string) {})
		s.tracker.StartLifetimeTimer("ship-2", func(expiredShipID string) {})

		assert.True(s.T(), s.tracker.IsTimerActive("ship-1"))
		assert.True(s.T(), s.tracker.IsTimerActive("ship-2"))
	})
}

// =============================================================================
// TESTS - CANCEL
// =============================================================================

func (s *AnnouncementLifetimeTrackerSuite) TestCancelLifetimeTimer() {
	s.Run("prevents callback and removes timer", func() {
		s.tracker = newAnnouncementLifetimeTrackerWithCustomTimeout(100 * time.Millisecond)
		shipID := "test-ship-1"

		var callbackCalled int32
		s.tracker.StartLifetimeTimer(shipID, func(expiredShipID string) {
			atomic.AddInt32(&callbackCalled, 1)
		})

		s.tracker.CancelLifetimeTimer(shipID)

		assert.False(s.T(), s.tracker.IsTimerActive(shipID))
		time.Sleep(200 * time.Millisecond)
		assert.Equal(s.T(), int32(0), atomic.LoadInt32(&callbackCalled))
	})

	s.Run("no-op for unknown shipID", func() {
		s.tracker = newAnnouncementLifetimeTrackerWithCustomTimeout(5 * time.Second)
		s.tracker.CancelLifetimeTimer("unknown") // should not panic
	})

	s.Run("only cancels the specified device", func() {
		s.tracker = newAnnouncementLifetimeTrackerWithCustomTimeout(5 * time.Second)

		s.tracker.StartLifetimeTimer("ship-1", func(expiredShipID string) {})
		s.tracker.StartLifetimeTimer("ship-2", func(expiredShipID string) {})

		s.tracker.CancelLifetimeTimer("ship-1")

		assert.False(s.T(), s.tracker.IsTimerActive("ship-1"))
		assert.True(s.T(), s.tracker.IsTimerActive("ship-2"))
	})
}

// =============================================================================
// TESTS - IS TIMER ACTIVE
// =============================================================================

func (s *AnnouncementLifetimeTrackerSuite) TestIsTimerActive() {
	s.Run("returns false for empty shipID", func() {
		s.tracker = NewAnnouncementLifetimeTracker(time.Minute)
		assert.False(s.T(), s.tracker.IsTimerActive(""))
	})
}

// =============================================================================
// TESTS - STOP ALL
// =============================================================================

func (s *AnnouncementLifetimeTrackerSuite) TestStopAll() {
	s.Run("stops all timers and prevents callbacks", func() {
		s.tracker = newAnnouncementLifetimeTrackerWithCustomTimeout(100 * time.Millisecond)

		var callbackCount int32
		s.tracker.StartLifetimeTimer("ship-1", func(expiredShipID string) {
			atomic.AddInt32(&callbackCount, 1)
		})
		s.tracker.StartLifetimeTimer("ship-2", func(expiredShipID string) {
			atomic.AddInt32(&callbackCount, 1)
		})

		s.tracker.StopAll()

		assert.False(s.T(), s.tracker.IsTimerActive("ship-1"))
		assert.False(s.T(), s.tracker.IsTimerActive("ship-2"))
		time.Sleep(200 * time.Millisecond)
		assert.Equal(s.T(), int32(0), atomic.LoadInt32(&callbackCount))
	})
}

// =============================================================================
// TESTS - CONCURRENT ACCESS
// =============================================================================

func (s *AnnouncementLifetimeTrackerSuite) TestConcurrentAccess() {
	s.Run("no race conditions under concurrent operations", func() {
		s.tracker = newAnnouncementLifetimeTrackerWithCustomTimeout(1 * time.Second)

		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(4)
			go func() { defer wg.Done(); s.tracker.StartLifetimeTimer("ship", func(string) {}) }()
			go func() { defer wg.Done(); _ = s.tracker.IsTimerActive("ship") }()
			go func() { defer wg.Done(); s.tracker.CancelLifetimeTimer("ship") }()
			go func() { defer wg.Done(); s.tracker.StopAll() }()
		}
		wg.Wait()
	})
}

// =============================================================================
// TESTS - SPEC COMPLIANCE
// =============================================================================

func (s *AnnouncementLifetimeTrackerSuite) TestSpecCompliance() {
	s.Run("reconnection restarts the timer", func() {
		s.tracker = newAnnouncementLifetimeTrackerWithCustomTimeout(100 * time.Millisecond)
		shipID := "devA-ship-id"

		var callbackCount int32

		// First connection
		s.tracker.StartLifetimeTimer(shipID, func(expiredShipID string) {
			atomic.AddInt32(&callbackCount, 1)
		})

		// Connection drops early
		time.Sleep(30 * time.Millisecond)
		s.tracker.CancelLifetimeTimer(shipID)

		// Reconnection — new timer
		var wg sync.WaitGroup
		wg.Add(1)
		s.tracker.StartLifetimeTimer(shipID, func(expiredShipID string) {
			atomic.AddInt32(&callbackCount, 1)
			wg.Done()
		})

		wg.Wait()

		// Only the second timer's callback should have fired
		assert.Equal(s.T(), int32(1), atomic.LoadInt32(&callbackCount))
	})
}

func (s *AnnouncementLifetimeTrackerSuite) TestStaleCallback() {
	// The tracker state is changed under the lock, so the first callback can only run afterwards,
	// as it does when it fired but had not yet taken the lock.
	s.Run("callback of a replaced timer keeps the new timer", func() {
		s.tracker = newAnnouncementLifetimeTrackerWithCustomTimeout(time.Millisecond)
		shipID := "devA-ship-id"
		fired := make(chan string, 1)
		s.tracker.StartLifetimeTimer(shipID, func(expiredShipID string) { fired <- expiredShipID })

		s.tracker.mutex.Lock()
		newTimer := time.AfterFunc(time.Hour, func() {})
		defer newTimer.Stop()
		s.tracker.timers[shipID] = lifetimeTimer{shipID: shipID, timer: newTimer}
		s.tracker.mutex.Unlock()

		select {
		case id := <-fired:
			s.T().Fatalf("callback of replaced timer fired for %s", id)
		case <-time.After(100 * time.Millisecond):
		}
		assert.True(s.T(), s.tracker.IsTimerActive(shipID))
	})

	s.Run("callback of a cancelled timer does not expire", func() {
		s.tracker = newAnnouncementLifetimeTrackerWithCustomTimeout(time.Millisecond)
		shipID := "devA-ship-id"
		fired := make(chan string, 1)
		s.tracker.StartLifetimeTimer(shipID, func(expiredShipID string) { fired <- expiredShipID })

		s.tracker.mutex.Lock()
		delete(s.tracker.timers, shipID)
		s.tracker.mutex.Unlock()

		select {
		case id := <-fired:
			s.T().Fatalf("callback of cancelled timer fired for %s", id)
		case <-time.After(100 * time.Millisecond):
		}
	})
}
