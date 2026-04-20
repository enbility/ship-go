package hub

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestHubMutexOrderingDeadlock tests for mutex ordering issues in Hub
func TestHubMutexOrderingDeadlock(t *testing.T) {
	hub := setupTestHub(t)

	// Create test connections
	conn1 := mocks.NewShipConnectionInterface(t)
	conn1.EXPECT().RemoteSKI().Return("ski-1").Maybe()
	conn1.EXPECT().IsAlive().Return(true).Maybe()
	conn1.EXPECT().DataHandler().Return(nil).Maybe()
	conn1.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()

	conn2 := mocks.NewShipConnectionInterface(t)
	conn2.EXPECT().RemoteSKI().Return("ski-2").Maybe()
	conn2.EXPECT().IsAlive().Return(true).Maybe()
	conn2.EXPECT().DataHandler().Return(nil).Maybe()
	conn2.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()

	// Test scenario: operations that might acquire mutexes in different orders
	var wg sync.WaitGroup
	iterations := 100

	for i := 0; i < iterations; i++ {
		wg.Add(3)

		// Operation 1: Register connection (planted directly under registry.mu).
		go func() {
			defer wg.Done()
			hub.registry.mu.Lock()
			hub.registry.connections["ski-1"] = conn1
			hub.registry.mu.Unlock()
		}()

		// Operation 2: Check pairing details (might need muxReg)
		go func() {
			defer wg.Done()
			_ = hub.ServiceForSKI("ski-1")
		}()

		// Operation 3: Connection lookup (needs registry.mu)
		go func() {
			defer wg.Done()
			_ = hub.connectionForSKI("ski-1")
		}()
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success, no deadlock
	case <-time.After(2 * time.Second):
		t.Fatal("Potential deadlock detected in Hub mutex ordering")
	}
}

// TestConnectionRegistrationRace tests the specific race condition in connection registration.
// Post-Phase-3: the racy compare-and-delete pattern is now collapsed into the
// registry's atomic UnregisterConnectionIfMatch (registry.RemoveIfMatches), and
// registration goes through registry.mu directly for these tests (which exercise
// concurrency, not the §12.2.2 rule).
func TestConnectionRegistrationRace(t *testing.T) {
	hub := setupTestHub(t)

	const testSKI = "test-ski"
	const iterations = 1000

	successfulRegistrations := int64(0)
	successfulUnregistrations := int64(0)

	for i := 0; i < iterations; i++ {
		// Create connections for this iteration
		conn1 := mocks.NewShipConnectionInterface(t)
		conn1.EXPECT().RemoteSKI().Return(testSKI).Maybe()
		conn1.EXPECT().IsAlive().Return(true).Maybe()
		conn1.EXPECT().DataHandler().Return(nil).Maybe()
		conn1.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()

		conn2 := mocks.NewShipConnectionInterface(t)
		conn2.EXPECT().RemoteSKI().Return(testSKI).Maybe()
		conn2.EXPECT().IsAlive().Return(true).Maybe()
		conn2.EXPECT().DataHandler().Return(nil).Maybe()
		conn2.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()

		// Plant first connection directly.
		hub.registry.mu.Lock()
		hub.registry.connections[testSKI] = conn1
		hub.registry.mu.Unlock()

		var wg sync.WaitGroup
		wg.Add(2)

		// Goroutine 1: Try to unregister and check it's the right connection
		go func() {
			defer wg.Done()

			// Simulate the pattern from HandleConnectionClosed
			existingConn := hub.connectionForSKI(testSKI)
			if existingConn == conn1 {
				// Small delay to increase race probability
				time.Sleep(time.Microsecond)

				// Atomic compare-and-delete via the registry helper.
				if hub.UnregisterConnectionIfMatch(testSKI, conn1) {
					atomic.AddInt64(&successfulUnregistrations, 1)
				}
			}
		}()

		// Goroutine 2: Try to register new connection (planted directly).
		go func() {
			defer wg.Done()
			hub.registry.mu.Lock()
			hub.registry.connections[testSKI] = conn2
			hub.registry.mu.Unlock()
			atomic.AddInt64(&successfulRegistrations, 1)
		}()

		wg.Wait()

		// Verify final state
		finalConn := hub.connectionForSKI(testSKI)

		// With the race condition, we might have:
		// 1. No connection (conn1 deleted, conn2 not registered due to timing)
		// 2. conn2 (correct behavior)
		// 3. conn1 (if unregister failed and new registration was blocked)

		// Log unexpected states
		switch finalConn {
		case nil:
			t.Logf("Iteration %d: No connection (potential race - connection lost)", i)
		case conn1:
			t.Logf("Iteration %d: Still have conn1 (unregister failed)", i)
		case conn2:
			// Expected outcome - conn2 is registered
		default:
			t.Logf("Iteration %d: Unexpected connection", i)
		}
	}

	t.Logf("Registration race test completed:")
	t.Logf("  Iterations: %d", iterations)
	t.Logf("  Successful registrations: %d", atomic.LoadInt64(&successfulRegistrations))
	t.Logf("  Successful unregistrations: %d", atomic.LoadInt64(&successfulUnregistrations))
}

// TestAtomicUnregisterIfMatch tests the improved atomic unregister method
func TestAtomicUnregisterIfMatch(t *testing.T) {
	hub := setupTestHub(t)

	const testSKI = "test-ski"
	const iterations = 1000

	for i := 0; i < iterations; i++ {
		// Create connections
		conn1 := mocks.NewShipConnectionInterface(t)
		conn1.EXPECT().RemoteSKI().Return(testSKI).Maybe()
		conn1.EXPECT().IsAlive().Return(true).Maybe()
		conn1.EXPECT().DataHandler().Return(nil).Maybe()

		conn2 := mocks.NewShipConnectionInterface(t)
		conn2.EXPECT().RemoteSKI().Return(testSKI).Maybe()
		conn2.EXPECT().IsAlive().Return(true).Maybe()
		conn2.EXPECT().DataHandler().Return(nil).Maybe()

		// Plant first connection directly.
		hub.registry.mu.Lock()
		hub.registry.connections[testSKI] = conn1
		hub.registry.mu.Unlock()

		var wg sync.WaitGroup
		wg.Add(2)

		unregisterSuccess := false
		registerHappened := false

		// Goroutine 1: Atomic unregister
		go func() {
			defer wg.Done()
			// Use the new atomic method
			if hub.UnregisterConnectionIfMatch(testSKI, conn1) {
				unregisterSuccess = true
			}
		}()

		// Goroutine 2: Try to register new connection (planted directly).
		go func() {
			defer wg.Done()
			hub.registry.mu.Lock()
			hub.registry.connections[testSKI] = conn2
			hub.registry.mu.Unlock()
			registerHappened = true
		}()

		wg.Wait()

		// Verify final state is consistent
		finalConn := hub.connectionForSKI(testSKI)

		// With atomic operations, we should have either:
		// 1. conn2 (if unregister succeeded and register happened after)
		// 2. conn2 (if register happened first and replaced conn1)
		// But never nil or conn1

		assert.NotNil(t, finalConn, "Should always have a connection")
		assert.Equal(t, conn2, finalConn, "Should have conn2 as final connection")

		if unregisterSuccess && !registerHappened {
			t.Error("Impossible state: unregister succeeded but register didn't happen")
		}
	}
}

// TestHubStressWithAllOperations performs comprehensive stress testing
func TestHubStressWithAllOperations(t *testing.T) {
	hub := setupTestHub(t)

	// Metrics
	var (
		registrations      int64
		unregistrations    int64
		lookups            int64
		pairingChecks      int64
		contentionDetected int64
	)

	// Create a pool of SKIs and connections
	numSKIs := 20
	connections := make([]api.ShipConnectionInterface, numSKIs)
	skis := make([]string, numSKIs)

	for i := 0; i < numSKIs; i++ {
		ski := string(rune('a'+i)) + "-ski"
		skis[i] = ski

		conn := mocks.NewShipConnectionInterface(t)
		conn.EXPECT().RemoteSKI().Return(ski).Maybe()
		conn.EXPECT().IsAlive().Return(true).Maybe()
		conn.EXPECT().DataHandler().Return(nil).Maybe()
		conn.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()
		connections[i] = conn
	}

	// Monitor function
	monitorOperation := func(op func(), metric *int64) {
		start := time.Now()
		op()
		atomic.AddInt64(metric, 1)
		if time.Since(start) > 5*time.Millisecond {
			atomic.AddInt64(&contentionDetected, 1)
		}
	}

	var wg sync.WaitGroup
	stopChan := make(chan struct{})

	// Spawn workers for different operations
	numWorkers := 5

	// Registration workers (planted directly; this test exercises stress, not the rule)
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case <-stopChan:
					return
				default:
					idx := workerID % numSKIs
					monitorOperation(func() {
						hub.registry.mu.Lock()
						hub.registry.connections[skis[idx]] = connections[idx]
						hub.registry.mu.Unlock()
					}, &registrations)
					time.Sleep(time.Microsecond * 100)
				}
			}
		}(i)
	}

	// Unregistration workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case <-stopChan:
					return
				default:
					idx := workerID % numSKIs
					monitorOperation(func() {
						hub.UnregisterConnectionIfMatch(skis[idx], connections[idx])
					}, &unregistrations)
					time.Sleep(time.Microsecond * 150)
				}
			}
		}(i)
	}

	// Lookup workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case <-stopChan:
					return
				default:
					idx := workerID % numSKIs
					monitorOperation(func() {
						_ = hub.connectionForSKI(skis[idx])
					}, &lookups)
					time.Sleep(time.Microsecond * 50)
				}
			}
		}(i)
	}

	// Pairing check workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case <-stopChan:
					return
				default:
					idx := workerID % numSKIs
					monitorOperation(func() {
						_ = hub.ServiceForSKI(skis[idx])
					}, &pairingChecks)
					time.Sleep(time.Microsecond * 200)
				}
			}
		}(i)
	}

	// Run stress test
	testDuration := 2 * time.Second
	time.Sleep(testDuration)
	close(stopChan)

	// Wait for workers with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Workers did not complete, possible deadlock")
	}

	// Report metrics
	t.Logf("Hub stress test metrics:")
	t.Logf("  Registrations: %d", atomic.LoadInt64(&registrations))
	t.Logf("  Unregistrations: %d", atomic.LoadInt64(&unregistrations))
	t.Logf("  Lookups: %d", atomic.LoadInt64(&lookups))
	t.Logf("  Pairing checks: %d", atomic.LoadInt64(&pairingChecks))
	t.Logf("  Contention events: %d", atomic.LoadInt64(&contentionDetected))

	// Check for high contention
	totalOps := atomic.LoadInt64(&registrations) + atomic.LoadInt64(&unregistrations) +
		atomic.LoadInt64(&lookups) + atomic.LoadInt64(&pairingChecks)
	contentionRate := float64(atomic.LoadInt64(&contentionDetected)) / float64(totalOps)

	assert.Less(t, contentionRate, 0.01, "High contention rate detected: %.2f%%", contentionRate*100)
}

// TestSetAutoAccept_DoesNotBlockServiceForSKI reproduces the stall described
// in auto-connect-issue.md: Hub.SetAutoAccept holds muxReg (write lock)
// while calling h.mdns.SetAutoAccept, which may perform slow network I/O
// (DBus / zeroconf). Any concurrent caller of ServiceForSKI (or any other
// muxReg consumer) is blocked for the entire duration of that I/O.
//
// The test injects a mock MdnsInterface whose SetAutoAccept blocks on a
// channel, simulating a slow provider. A second goroutine attempts
// ServiceForSKI. If muxReg is still held during the mDNS call,
// ServiceForSKI cannot proceed and the test times out — exposing the bug.
//
// EXPECTED (current code): FAIL — timeout, ServiceForSKI blocked.
// EXPECTED (after fix):    PASS — ServiceForSKI completes immediately.
func TestSetAutoAccept_DoesNotBlockServiceForSKI(t *testing.T) {
	hub := setupTestHub(t)

	// Channel that keeps the mock's SetAutoAccept blocked until we close it,
	// simulating a slow mDNS provider (avahi DBus / zeroconf shutdown).
	block := make(chan struct{})

	mdnsMock := mocks.NewMdnsInterface(t)
	mdnsMock.EXPECT().SetAutoAccept(mock.Anything).
		Run(func(_ bool) {
			<-block // block until test releases
		}).Return()

	// Replace the hub's mDNS with our blocking mock.
	hub.mdns = mdnsMock

	// Goroutine 1: call Hub.SetAutoAccept — acquires muxReg, then blocks
	// inside the mock's SetAutoAccept (simulating slow I/O).
	setAutoAcceptDone := make(chan struct{})
	go func() {
		hub.SetAutoAccept(true)
		close(setAutoAcceptDone)
	}()

	// Give goroutine 1 a moment to acquire the lock and enter the mock.
	time.Sleep(20 * time.Millisecond)

	// Goroutine 2: try ServiceForSKI, which also needs muxReg.
	done := make(chan struct{})
	go func() {
		_ = hub.ServiceForSKI("some-ski")
		close(done)
	}()

	select {
	case <-done:
		// PASS: ServiceForSKI was not blocked by the mDNS call.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ServiceForSKI blocked for >500ms — muxReg is held during mDNS SetAutoAccept I/O")
	}

	// Release the blocked goroutine and wait for it to finish,
	// so testify's mock cleanup doesn't race with the in-flight call.
	close(block)
	<-setAutoAcceptDone
}

// TestSetAutoAccept_DoesNotBlockIsAutoAcceptEnabled is a variant that checks
// read-lock consumers of muxReg are also not starved. IsAutoAcceptEnabled
// takes muxReg.RLock — if a write lock is held by SetAutoAccept during mDNS
// I/O, readers are blocked too.
func TestSetAutoAccept_DoesNotBlockIsAutoAcceptEnabled(t *testing.T) {
	hub := setupTestHub(t)

	block := make(chan struct{})

	mdnsMock := mocks.NewMdnsInterface(t)
	mdnsMock.EXPECT().SetAutoAccept(mock.Anything).
		Run(func(_ bool) {
			<-block
		}).Return()

	hub.mdns = mdnsMock

	setAutoAcceptDone := make(chan struct{})
	go func() {
		hub.SetAutoAccept(true)
		close(setAutoAcceptDone)
	}()
	time.Sleep(20 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		_ = hub.IsAutoAcceptEnabled()
		close(done)
	}()

	select {
	case <-done:
		// PASS
	case <-time.After(500 * time.Millisecond):
		t.Fatal("IsAutoAcceptEnabled blocked for >500ms — muxReg is held during mDNS SetAutoAccept I/O")
	}

	// Release the blocked goroutine and wait for it to finish,
	// so testify's mock cleanup doesn't race with the in-flight call.
	close(block)
	<-setAutoAcceptDone
}
