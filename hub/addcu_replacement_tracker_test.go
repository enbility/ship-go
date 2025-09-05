package hub

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// =============================================================================
// ADDCU REPLACEMENT TRACKER TEST SUITE
// =============================================================================

func TestAddCuReplacementTrackerSuite(t *testing.T) {
	suite.Run(t, new(AddCuReplacementTrackerSuite))
}

type AddCuReplacementTrackerSuite struct {
	suite.Suite

	tracker *AddCuReplacementTracker
}

func (s *AddCuReplacementTrackerSuite) BeforeTest(suiteName, testName string) {
	s.tracker = nil // Will be set up in individual tests
}

func (s *AddCuReplacementTrackerSuite) AfterTest(suiteName, testName string) {
	if s.tracker != nil && s.tracker.timer != nil {
		s.tracker.timer.Stop()
	}
}

// =============================================================================
// TESTS - BASIC STRUCTURE AND STATE MANAGEMENT
// =============================================================================

func (s *AddCuReplacementTrackerSuite) TestAddCuReplacementTracker_NewTracker() {
	s.Run("creates tracker with default timeout", func() {
		s.tracker = NewAddCuReplacementTracker()

		require.NotNil(s.T(), s.tracker)
		assert.Equal(s.T(), 15*time.Minute, s.tracker.timeout)
		assert.Empty(s.T(), s.tracker.pairedDeviceShipID)
		assert.True(s.T(), s.tracker.disconnectionTime.IsZero())
		assert.Nil(s.T(), s.tracker.timer)
	})

	s.Run("creates tracker with custom timeout", func() {
		customTimeout := 5 * time.Second
		s.tracker = NewAddCuReplacementTrackerWithTimeout(customTimeout)

		require.NotNil(s.T(), s.tracker)
		assert.Equal(s.T(), customTimeout, s.tracker.timeout)
		assert.Empty(s.T(), s.tracker.pairedDeviceShipID)
		assert.True(s.T(), s.tracker.disconnectionTime.IsZero())
		assert.Nil(s.T(), s.tracker.timer)
	})

	s.Run("tracker has proper mutex initialization", func() {
		s.tracker = NewAddCuReplacementTracker()

		// Test that we can acquire and release locks without deadlock
		// and that protected fields are accessible within critical sections
		s.tracker.mutex.Lock()
		// Access protected fields within write lock
		_ = s.tracker.pairedDeviceShipID
		_ = s.tracker.disconnectionTime
		_ = s.tracker.timer
		s.tracker.mutex.Unlock()

		s.tracker.mutex.RLock()
		// Access protected fields within read lock
		_ = s.tracker.pairedDeviceShipID
		_ = s.tracker.disconnectionTime
		_ = s.tracker.timer
		s.tracker.mutex.RUnlock()
	})
}

func (s *AddCuReplacementTrackerSuite) TestAddCuReplacementTracker_StartTimer() {
	s.Run("starts timer for a device", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		shipID := "test-ship-id-1"
		callbackCalled := false

		callback := func(expiredShipID string) {
			callbackCalled = true
		}

		s.tracker.StartTimer(shipID, callback)

		// Verify timer state
		assert.Equal(s.T(), shipID, s.tracker.pairedDeviceShipID)
		assert.False(s.T(), s.tracker.disconnectionTime.IsZero())
		assert.NotNil(s.T(), s.tracker.timer)

		// Timer should be running but callback shouldn't have fired yet
		assert.False(s.T(), callbackCalled)
	})

	s.Run("sets disconnection time when starting timer", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		shipID := "test-ship-id-2"
		before := time.Now()

		s.tracker.StartTimer(shipID, func(expiredShipID string) {})

		after := time.Now()
		assert.True(s.T(), s.tracker.disconnectionTime.After(before) || s.tracker.disconnectionTime.Equal(before))
		assert.True(s.T(), s.tracker.disconnectionTime.Before(after) || s.tracker.disconnectionTime.Equal(after))
	})

	s.Run("overwrites previous device when starting timer for different device", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		firstShipID := "first-device"
		secondShipID := "second-device"

		// Start timer for first device
		s.tracker.StartTimer(firstShipID, func(expiredShipID string) {})
		firstTimer := s.tracker.timer

		// Start timer for second device - should overwrite
		s.tracker.StartTimer(secondShipID, func(expiredShipID string) {})

		// Verify second device is now tracked
		assert.Equal(s.T(), secondShipID, s.tracker.pairedDeviceShipID)
		require.NotSame(s.T(), firstTimer, s.tracker.timer) // New timer created
		assert.NotNil(s.T(), s.tracker.timer)
	})

	s.Run("handles nil callback gracefully", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		shipID := "test-ship-id-nil"

		// Should not panic
		assert.NotPanics(s.T(), func() {
			s.tracker.StartTimer(shipID, nil)
		})

		assert.Equal(s.T(), shipID, s.tracker.pairedDeviceShipID)
		assert.NotNil(s.T(), s.tracker.timer)
	})
}

func (s *AddCuReplacementTrackerSuite) TestAddCuReplacementTracker_StopTimer() {
	s.Run("stops timer for correct device", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		shipID := "test-ship-id-stop"

		// Start timer first
		s.tracker.StartTimer(shipID, func(expiredShipID string) {})
		require.NotNil(s.T(), s.tracker.timer)

		// Stop timer
		s.tracker.StopTimer(shipID)

		// Verify timer is stopped and state is cleared
		assert.Empty(s.T(), s.tracker.pairedDeviceShipID)
		assert.True(s.T(), s.tracker.disconnectionTime.IsZero())
		assert.Nil(s.T(), s.tracker.timer)
	})

	s.Run("ignores stop request for different device", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		trackedShipID := "tracked-device"
		differentShipID := "different-device"

		// Start timer for tracked device
		s.tracker.StartTimer(trackedShipID, func(expiredShipID string) {})
		originalTimer := s.tracker.timer
		originalTime := s.tracker.disconnectionTime

		// Try to stop timer for different device
		s.tracker.StopTimer(differentShipID)

		// Verify tracked device is still being tracked
		assert.Equal(s.T(), trackedShipID, s.tracker.pairedDeviceShipID)
		assert.Equal(s.T(), originalTime, s.tracker.disconnectionTime)
		assert.Equal(s.T(), originalTimer, s.tracker.timer)
	})

	s.Run("handles stop when no timer is running", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		shipID := "no-timer-device"

		// Should not panic
		assert.NotPanics(s.T(), func() {
			s.tracker.StopTimer(shipID)
		})

		// State should remain empty
		assert.Empty(s.T(), s.tracker.pairedDeviceShipID)
		assert.True(s.T(), s.tracker.disconnectionTime.IsZero())
		assert.Nil(s.T(), s.tracker.timer)
	})

	s.Run("stops timer multiple times safely", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		shipID := "multi-stop-device"

		// Start and stop timer
		s.tracker.StartTimer(shipID, func(expiredShipID string) {})
		s.tracker.StopTimer(shipID)

		// Stop again - should not panic
		assert.NotPanics(s.T(), func() {
			s.tracker.StopTimer(shipID)
		})

		// State should remain cleared
		assert.Empty(s.T(), s.tracker.pairedDeviceShipID)
		assert.True(s.T(), s.tracker.disconnectionTime.IsZero())
		assert.Nil(s.T(), s.tracker.timer)
	})
}

func (s *AddCuReplacementTrackerSuite) TestAddCuReplacementTracker_IsTracking() {
	s.Run("returns true when tracking the device", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		shipID := "tracking-device"

		// Start tracking
		s.tracker.StartTimer(shipID, func(expiredShipID string) {})

		// Should return true for the tracked device
		assert.True(s.T(), s.tracker.IsTracking(shipID))
	})

	s.Run("returns false when not tracking the device", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		trackedShipID := "tracked-device"
		differentShipID := "different-device"

		// Start tracking one device
		s.tracker.StartTimer(trackedShipID, func(expiredShipID string) {})

		// Should return false for different device
		assert.False(s.T(), s.tracker.IsTracking(differentShipID))
	})

	s.Run("returns false when no device is being tracked", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		shipID := "any-device"

		// No device is being tracked
		assert.False(s.T(), s.tracker.IsTracking(shipID))
	})

	s.Run("returns false after timer is stopped", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		shipID := "stopped-device"

		// Start and stop tracking
		s.tracker.StartTimer(shipID, func(expiredShipID string) {})
		s.tracker.StopTimer(shipID)

		// Should return false after stopping
		assert.False(s.T(), s.tracker.IsTracking(shipID))
	})
}

func (s *AddCuReplacementTrackerSuite) TestAddCuReplacementTracker_IsInReplacementWindow() {
	s.Run("returns true when replacement timer is active", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		shipID := "device-with-timer"

		// Start tracking
		s.tracker.StartTimer(shipID, func(expiredShipID string) {})

		// Should return true when timer is active
		assert.True(s.T(), s.tracker.IsInReplacementWindow())
	})

	s.Run("returns false when no replacement timer is active", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)

		// No timer started
		assert.False(s.T(), s.tracker.IsInReplacementWindow())
	})

	s.Run("returns false after timer is stopped", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		shipID := "stopped-timer-device"

		// Start and stop timer
		s.tracker.StartTimer(shipID, func(expiredShipID string) {})
		assert.True(s.T(), s.tracker.IsInReplacementWindow())
		
		s.tracker.StopTimer(shipID)
		assert.False(s.T(), s.tracker.IsInReplacementWindow())
	})

	s.Run("returns false after timer expires", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(50 * time.Millisecond)
		shipID := "expired-timer-device"
		timerFired := make(chan bool, 1)

		// Start timer with short timeout
		s.tracker.StartTimer(shipID, func(expiredShipID string) {
			timerFired <- true
		})

		// Initially should be true
		assert.True(s.T(), s.tracker.IsInReplacementWindow())

		// Wait for timer to expire
		select {
		case <-timerFired:
			// Timer expired successfully
		case <-time.After(200 * time.Millisecond):
			s.T().Fatal("Timer did not fire within expected time")
		}

		// Should now be false
		assert.False(s.T(), s.tracker.IsInReplacementWindow())
	})

	s.Run("works correctly with device replacement scenarios", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(2 * time.Second)
		firstDevice := "first-device"
		secondDevice := "second-device"

		// Start tracking first device
		s.tracker.StartTimer(firstDevice, func(expiredShipID string) {})
		assert.True(s.T(), s.tracker.IsInReplacementWindow())

		// Replace with second device (should still be in window)
		s.tracker.StartTimer(secondDevice, func(expiredShipID string) {})
		assert.True(s.T(), s.tracker.IsInReplacementWindow())

		// Stop timer completely
		s.tracker.StopTimer(secondDevice)
		assert.False(s.T(), s.tracker.IsInReplacementWindow())
	})
}

func (s *AddCuReplacementTrackerSuite) TestAddCuReplacementTracker_SingleDeviceConstraint() {
	s.Run("only tracks one device at a time", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		firstDevice := "first-device"
		secondDevice := "second-device"
		thirdDevice := "third-device"

		// Start tracking first device
		s.tracker.StartTimer(firstDevice, func(expiredShipID string) {})
		assert.True(s.T(), s.tracker.IsTracking(firstDevice))
		assert.False(s.T(), s.tracker.IsTracking(secondDevice))
		assert.False(s.T(), s.tracker.IsTracking(thirdDevice))

		// Start tracking second device - should replace first
		s.tracker.StartTimer(secondDevice, func(expiredShipID string) {})
		assert.False(s.T(), s.tracker.IsTracking(firstDevice))
		assert.True(s.T(), s.tracker.IsTracking(secondDevice))
		assert.False(s.T(), s.tracker.IsTracking(thirdDevice))

		// Start tracking third device - should replace second
		s.tracker.StartTimer(thirdDevice, func(expiredShipID string) {})
		assert.False(s.T(), s.tracker.IsTracking(firstDevice))
		assert.False(s.T(), s.tracker.IsTracking(secondDevice))
		assert.True(s.T(), s.tracker.IsTracking(thirdDevice))
	})

	s.Run("maintains single timer constraint", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		device1 := "device-1"
		device2 := "device-2"

		// Start first timer
		s.tracker.StartTimer(device1, func(expiredShipID string) {})
		firstTimer := s.tracker.timer
		require.NotNil(s.T(), firstTimer)

		// Start second timer - should stop first timer and create new one
		s.tracker.StartTimer(device2, func(expiredShipID string) {})
		secondTimer := s.tracker.timer

		require.NotNil(s.T(), secondTimer)
		require.NotSame(s.T(), firstTimer, secondTimer)

		// Only second device should be tracked
		assert.Equal(s.T(), device2, s.tracker.pairedDeviceShipID)
	})

	s.Run("concurrent access maintains single device constraint", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(5 * time.Second)
		device1 := "concurrent-device-1"
		device2 := "concurrent-device-2"

		var wg sync.WaitGroup
		wg.Add(2)

		// Concurrent timer starts
		go func() {
			defer wg.Done()
			s.tracker.StartTimer(device1, func(expiredShipID string) {})
		}()

		go func() {
			defer wg.Done()
			s.tracker.StartTimer(device2, func(expiredShipID string) {})
		}()

		wg.Wait()

		// One device should be tracked (either device1 or device2)
		trackedDevices := 0
		if s.tracker.IsTracking(device1) {
			trackedDevices++
		}
		if s.tracker.IsTracking(device2) {
			trackedDevices++
		}

		assert.Equal(s.T(), 1, trackedDevices, "exactly one device should be tracked")
		assert.NotNil(s.T(), s.tracker.timer, "timer should be running")
	})
}

// =============================================================================
// TESTS - TIMER LOGIC AND EDGE CASES
// =============================================================================

func (s *AddCuReplacementTrackerSuite) TestAddCuReplacementTracker_TimerTimeout() {
	s.Run("timer fires callback after timeout", func() {
		timeout := 50 * time.Millisecond
		s.tracker = NewAddCuReplacementTrackerWithTimeout(timeout)
		shipID := "timeout-test-device"

		callbackFired := make(chan bool, 1)
		callback := func(expiredShipID string) {
			callbackFired <- true
		}

		// Start timer
		s.tracker.StartTimer(shipID, callback)

		// Wait for callback to fire
		select {
		case <-callbackFired:
			// Callback fired as expected
		case <-time.After(timeout * 2):
			s.T().Fatal("timeout callback did not fire within expected time")
		}

		// After timeout, device should no longer be tracked
		assert.False(s.T(), s.tracker.IsTracking(shipID))
		assert.Empty(s.T(), s.tracker.pairedDeviceShipID)
		assert.True(s.T(), s.tracker.disconnectionTime.IsZero())
		assert.Nil(s.T(), s.tracker.timer)
	})

	s.Run("timer callback execution with nil callback", func() {
		timeout := 50 * time.Millisecond
		s.tracker = NewAddCuReplacementTrackerWithTimeout(timeout)
		shipID := "nil-callback-device"

		// Start timer with nil callback - should not panic when timer fires
		s.tracker.StartTimer(shipID, nil)

		// Wait for timer to expire
		time.Sleep(timeout * 2)

		// Device should no longer be tracked
		assert.False(s.T(), s.tracker.IsTracking(shipID))
		assert.Empty(s.T(), s.tracker.pairedDeviceShipID)
	})

	s.Run("multiple callbacks with different timeouts", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(100 * time.Millisecond)
		device1 := "device-1-timeout"
		device2 := "device-2-timeout"

		callback1Fired := make(chan bool, 1)
		callback2Fired := make(chan bool, 1)

		callback1 := func(expiredShipID string) { callback1Fired <- true }
		callback2 := func(expiredShipID string) { callback2Fired <- true }

		// Start timer for device1
		s.tracker.StartTimer(device1, callback1)

		// Wait 50ms, then start timer for device2 (should cancel device1)
		time.Sleep(50 * time.Millisecond)
		s.tracker.StartTimer(device2, callback2)

		// Callback1 should not fire (timer was cancelled)
		select {
		case <-callback1Fired:
			s.T().Fatal("callback1 should not have fired - timer was cancelled")
		case <-time.After(100 * time.Millisecond):
			// Good, callback1 did not fire
		}

		// Callback2 should fire
		select {
		case <-callback2Fired:
			// Good, callback2 fired
		case <-time.After(200 * time.Millisecond):
			s.T().Fatal("callback2 should have fired")
		}
	})
}

func (s *AddCuReplacementTrackerSuite) TestAddCuReplacementTracker_StopBeforeTimeout() {
	s.Run("stopping timer prevents callback execution", func() {
		timeout := 100 * time.Millisecond
		s.tracker = NewAddCuReplacementTrackerWithTimeout(timeout)
		shipID := "stop-before-timeout"

		callbackFired := make(chan bool, 1)
		callback := func(expiredShipID string) {
			callbackFired <- true
		}

		// Start timer
		s.tracker.StartTimer(shipID, callback)

		// Stop timer after half the timeout
		time.Sleep(timeout / 2)
		s.tracker.StopTimer(shipID)

		// Wait beyond original timeout to ensure callback doesn't fire
		select {
		case <-callbackFired:
			s.T().Fatal("callback should not fire after timer is stopped")
		case <-time.After(timeout * 2):
			// Good, callback did not fire
		}

		// Device should no longer be tracked
		assert.False(s.T(), s.tracker.IsTracking(shipID))
	})

	s.Run("stop timer just before timeout", func() {
		timeout := 50 * time.Millisecond
		s.tracker = NewAddCuReplacementTrackerWithTimeout(timeout)
		shipID := "stop-just-before"

		callbackFired := make(chan bool, 1)
		callback := func(expiredShipID string) {
			callbackFired <- true
		}

		// Start timer
		s.tracker.StartTimer(shipID, callback)

		// Wait almost the full timeout, then stop
		time.Sleep(timeout - 10*time.Millisecond)
		s.tracker.StopTimer(shipID)

		// Verify callback doesn't fire
		select {
		case <-callbackFired:
			s.T().Fatal("callback should not fire after timer is stopped")
		case <-time.After(50 * time.Millisecond):
			// Good, callback did not fire
		}
	})

	s.Run("stop timer for wrong device doesn't affect running timer", func() {
		timeout := 100 * time.Millisecond
		s.tracker = NewAddCuReplacementTrackerWithTimeout(timeout)
		trackedDevice := "tracked-device"
		wrongDevice := "wrong-device"

		callbackFired := make(chan bool, 1)
		callback := func(expiredShipID string) {
			callbackFired <- true
		}

		// Start timer for tracked device
		s.tracker.StartTimer(trackedDevice, callback)

		// Try to stop timer for wrong device
		time.Sleep(timeout / 2)
		s.tracker.StopTimer(wrongDevice)

		// Timer should still be running and callback should fire
		assert.True(s.T(), s.tracker.IsTracking(trackedDevice))

		select {
		case <-callbackFired:
			// Good, callback fired as expected
		case <-time.After(timeout * 2):
			s.T().Fatal("callback should have fired - timer should still be running")
		}
	})
}

func (s *AddCuReplacementTrackerSuite) TestAddCuReplacementTracker_ReplaceExistingTimer() {
	s.Run("starting new timer cancels previous timer", func() {
		timeout := 100 * time.Millisecond
		s.tracker = NewAddCuReplacementTrackerWithTimeout(timeout)
		device1 := "first-timer-device"
		device2 := "second-timer-device"

		callback1Fired := make(chan bool, 1)
		callback2Fired := make(chan bool, 1)

		callback1 := func(expiredShipID string) { callback1Fired <- true }
		callback2 := func(expiredShipID string) { callback2Fired <- true }

		// Start first timer - avoid race by not capturing timer reference
		s.tracker.StartTimer(device1, callback1)
		assert.True(s.T(), s.tracker.IsTracking(device1))

		// Wait half timeout, then start second timer
		time.Sleep(timeout / 2)
		s.tracker.StartTimer(device2, callback2)

		// Second device should now be tracked, first should not
		assert.False(s.T(), s.tracker.IsTracking(device1))
		assert.True(s.T(), s.tracker.IsTracking(device2))

		// First callback should not fire
		select {
		case <-callback1Fired:
			s.T().Fatal("first callback should not fire - timer was replaced")
		case <-time.After(timeout):
			// Good, first callback did not fire
		}

		// Second callback should fire
		select {
		case <-callback2Fired:
			// Good, second callback fired
		case <-time.After(timeout * 2):
			s.T().Fatal("second callback should have fired")
		}

		// After timeout, device should no longer be tracked
		assert.False(s.T(), s.tracker.IsTracking(device1))
		assert.False(s.T(), s.tracker.IsTracking(device2)) // Should be cleared after timeout
	})

	s.Run("rapid timer replacements", func() {
		timeout := 200 * time.Millisecond
		s.tracker = NewAddCuReplacementTrackerWithTimeout(timeout)

		callbacksFired := make(chan string, 5)

		// Start multiple timers rapidly
		for i := 0; i < 5; i++ {
			deviceID := fmt.Sprintf("rapid-device-%d", i)
			callback := func(id string) func(expiredShipID string) {
				return func(expiredShipID string) {
					callbacksFired <- id
				}
			}(deviceID)

			s.tracker.StartTimer(deviceID, callback)
			time.Sleep(20 * time.Millisecond) // 20ms between starts
		}

		// Only the last callback should fire
		var firedCallbacks []string
		timeoutChan := time.After(timeout + 100*time.Millisecond)

	CollectCallbacks:
		for {
			select {
			case deviceID := <-callbacksFired:
				firedCallbacks = append(firedCallbacks, deviceID)
			case <-timeoutChan:
				break CollectCallbacks
			}
		}

		// Only one callback should have fired (the last one)
		assert.Len(s.T(), firedCallbacks, 1, "only the last timer should fire")
		if len(firedCallbacks) > 0 {
			assert.Equal(s.T(), "rapid-device-4", firedCallbacks[0])
		}
	})
}

func (s *AddCuReplacementTrackerSuite) TestAddCuReplacementTracker_ThreadSafety() {
	s.Run("concurrent start and stop operations", func() {
		timeout := 200 * time.Millisecond
		s.tracker = NewAddCuReplacementTrackerWithTimeout(timeout)

		var wg sync.WaitGroup
		numGoroutines := 10
		callbackCount := int32(0)

		// Concurrent operations
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				deviceID := fmt.Sprintf("concurrent-device-%d", id)

				callback := func(expiredShipID string) {
					atomic.AddInt32(&callbackCount, 1)
				}

				// Start timer
				s.tracker.StartTimer(deviceID, callback)

				// Sometimes stop it quickly
				if id%2 == 0 {
					time.Sleep(10 * time.Millisecond)
					s.tracker.StopTimer(deviceID)
				}
			}(i)
		}

		wg.Wait()

		// Wait for any remaining timers to fire
		time.Sleep(timeout + 100*time.Millisecond)

		// Should not panic and should maintain consistency
		// At most one callback should fire (from the last timer that wasn't stopped)
		finalCount := atomic.LoadInt32(&callbackCount)
		assert.True(s.T(), finalCount <= 1, "at most one callback should fire, got %d", finalCount)
	})

	s.Run("concurrent access during timer execution", func() {
		timeout := 50 * time.Millisecond
		s.tracker = NewAddCuReplacementTrackerWithTimeout(timeout)
		shipID := "concurrent-execution-device"

		callbackStarted := make(chan bool, 1)
		callbackCompleted := make(chan bool, 1)

		callback := func(expiredShipID string) {
			callbackStarted <- true
			// Simulate some work in callback
			time.Sleep(20 * time.Millisecond)
			callbackCompleted <- true
		}

		// Start timer
		s.tracker.StartTimer(shipID, callback)

		// Wait for callback to start
		select {
		case <-callbackStarted:
			// Callback started
		case <-time.After(timeout * 2):
			s.T().Fatal("callback should have started")
		}

		// While callback is running, try concurrent operations
		var wg sync.WaitGroup
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				otherDevice := fmt.Sprintf("other-device-%d", id)

				// These should not panic or deadlock
				s.tracker.IsTracking(otherDevice)
				s.tracker.StartTimer(otherDevice, func(expiredShipID string) {})
				s.tracker.StopTimer(otherDevice)
			}(i)
		}

		wg.Wait()

		// Wait for original callback to complete
		select {
		case <-callbackCompleted:
			// Callback completed
		case <-time.After(100 * time.Millisecond):
			s.T().Fatal("callback should have completed")
		}
	})
}

func (s *AddCuReplacementTrackerSuite) TestAddCuReplacementTracker_ConfigurableTimeout() {
	s.Run("different timeout durations work correctly", func() {
		timeouts := []time.Duration{
			10 * time.Millisecond,
			50 * time.Millisecond,
			100 * time.Millisecond,
		}

		for _, timeout := range timeouts {
			s.tracker = NewAddCuReplacementTrackerWithTimeout(timeout)
			shipID := fmt.Sprintf("timeout-%s-device", timeout.String())

			callbackFired := make(chan bool, 1)
			callback := func(expiredShipID string) {
				callbackFired <- true
			}

			startTime := time.Now()
			s.tracker.StartTimer(shipID, callback)

			// Wait for callback
			select {
			case <-callbackFired:
				elapsed := time.Since(startTime)
				// Allow some tolerance for timing
				minExpected := timeout
				maxExpected := timeout + 50*time.Millisecond

				assert.True(s.T(), elapsed >= minExpected,
					"callback fired too early: elapsed=%s, timeout=%s", elapsed, timeout)
				assert.True(s.T(), elapsed <= maxExpected,
					"callback fired too late: elapsed=%s, timeout=%s", elapsed, timeout)
			case <-time.After(timeout * 3):
				s.T().Fatalf("callback did not fire for timeout %s", timeout)
			}

			// Clean up
			s.tracker.StopTimer(shipID)
		}
	})

	s.Run("zero timeout fires immediately", func() {
		s.tracker = NewAddCuReplacementTrackerWithTimeout(0)
		shipID := "zero-timeout-device"

		callbackFired := make(chan bool, 1)
		callback := func(expiredShipID string) {
			callbackFired <- true
		}

		s.tracker.StartTimer(shipID, callback)

		// Should fire very quickly
		select {
		case <-callbackFired:
			// Good, fired immediately
		case <-time.After(50 * time.Millisecond):
			s.T().Fatal("zero timeout callback should fire immediately")
		}
	})

	s.Run("very long timeout can be stopped", func() {
		longTimeout := 10 * time.Second
		s.tracker = NewAddCuReplacementTrackerWithTimeout(longTimeout)
		shipID := "long-timeout-device"

		callbackFired := make(chan bool, 1)
		callback := func(expiredShipID string) {
			callbackFired <- true
		}

		// Start long timer
		s.tracker.StartTimer(shipID, callback)
		assert.True(s.T(), s.tracker.IsTracking(shipID))

		// Stop it quickly
		time.Sleep(10 * time.Millisecond)
		s.tracker.StopTimer(shipID)

		// Should be stopped
		assert.False(s.T(), s.tracker.IsTracking(shipID))

		// Callback should not fire
		select {
		case <-callbackFired:
			s.T().Fatal("callback should not fire for stopped long timeout")
		case <-time.After(100 * time.Millisecond):
			// Good, callback did not fire
		}
	})
}
