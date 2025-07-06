// Package testhelper provides utilities for testing concurrent code and detecting deadlocks
package testhelper

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// DeadlockTimeout is the default timeout for deadlock detection
const DeadlockTimeout = 500 * time.Millisecond

// RunWithDeadlockDetection runs a function with deadlock detection
// It will fail the test if the function doesn't complete within the timeout
func RunWithDeadlockDetection(t *testing.T, timeout time.Duration, fn func()) {
	t.Helper()
	
	done := make(chan struct{})
	
	go func() {
		defer close(done)
		fn()
	}()
	
	select {
	case <-done:
		// Function completed successfully
	case <-time.After(timeout):
		buf := make([]byte, 1<<16)
		stackSize := runtime.Stack(buf, true)
		t.Fatalf("Deadlock detected! Function did not complete within %v\nGoroutine dump:\n%s", 
			timeout, buf[:stackSize])
	}
}

// ConcurrentTest runs a test function concurrently with specified number of goroutines
type ConcurrentTest struct {
	Goroutines int
	Iterations int
	Test       func(id int)
}

// Run executes the concurrent test
func (ct *ConcurrentTest) Run(t *testing.T) {
	t.Helper()
	
	var wg sync.WaitGroup
	wg.Add(ct.Goroutines)
	
	ctx, cancel := context.WithTimeout(context.Background(), DeadlockTimeout*2)
	defer cancel()
	
	errors := make(chan error, ct.Goroutines*ct.Iterations)
	
	for i := 0; i < ct.Goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			
			for j := 0; j < ct.Iterations; j++ {
				select {
				case <-ctx.Done():
					errors <- fmt.Errorf("goroutine %d timed out", id)
					return
				default:
					ct.Test(id)
				}
			}
		}(i)
	}
	
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	
	select {
	case <-done:
		// All goroutines completed
	case <-ctx.Done():
		t.Fatal("Concurrent test timed out - possible deadlock")
	}
	
	close(errors)
	for err := range errors {
		if err != nil {
			t.Error(err)
		}
	}
}

// LockOrderTracker helps detect lock ordering violations
type LockOrderTracker struct {
	mu     sync.Mutex
	orders map[string][]string // goroutine ID -> lock order
}

// NewLockOrderTracker creates a new lock order tracker
func NewLockOrderTracker() *LockOrderTracker {
	return &LockOrderTracker{
		orders: make(map[string][]string),
	}
}

// RecordLock records a lock acquisition
func (lot *LockOrderTracker) RecordLock(lockName string) {
	lot.mu.Lock()
	defer lot.mu.Unlock()
	
	gid := fmt.Sprintf("%d", getGoroutineID())
	lot.orders[gid] = append(lot.orders[gid], lockName)
}

// RecordUnlock records a lock release
func (lot *LockOrderTracker) RecordUnlock(lockName string) {
	lot.mu.Lock()
	defer lot.mu.Unlock()
	
	gid := fmt.Sprintf("%d", getGoroutineID())
	order := lot.orders[gid]
	if len(order) > 0 && order[len(order)-1] == lockName {
		lot.orders[gid] = order[:len(order)-1]
	}
}

// CheckForViolations checks if there are any lock ordering violations
func (lot *LockOrderTracker) CheckForViolations() error {
	lot.mu.Lock()
	defer lot.mu.Unlock()
	
	// Build a map of all observed lock orderings
	orderings := make(map[string]map[string]bool)
	
	for _, order := range lot.orders {
		for i := 0; i < len(order)-1; i++ {
			lock1 := order[i]
			lock2 := order[i+1]
			
			if orderings[lock1] == nil {
				orderings[lock1] = make(map[string]bool)
			}
			orderings[lock1][lock2] = true
		}
	}
	
	// Check for cycles (simple check for pairs)
	for lock1, successors := range orderings {
		for lock2 := range successors {
			if orderings[lock2] != nil && orderings[lock2][lock1] {
				return fmt.Errorf("lock ordering violation detected: %s -> %s and %s -> %s", 
					lock1, lock2, lock2, lock1)
			}
		}
	}
	
	return nil
}

// getGoroutineID returns the current goroutine ID
// This is a hack and should only be used for testing
func getGoroutineID() uint64 {
	b := make([]byte, 64)
	b = b[:runtime.Stack(b, false)]
	var id uint64
	fmt.Sscanf(string(b), "goroutine %d ", &id)
	return id
}

// WaitGroupWithTimeout is a WaitGroup that can timeout
type WaitGroupWithTimeout struct {
	sync.WaitGroup
}

// WaitWithTimeout waits for the WaitGroup with a timeout
func (wg *WaitGroupWithTimeout) WaitWithTimeout(timeout time.Duration) error {
	done := make(chan struct{})
	
	go func() {
		wg.Wait()
		close(done)
	}()
	
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("WaitGroup timed out after %v", timeout)
	}
}

// AssertNoGoroutineLeaks checks that no goroutines are leaked after a test
func AssertNoGoroutineLeaks(t *testing.T, fn func()) {
	t.Helper()
	
	before := runtime.NumGoroutine()
	
	fn()
	
	// Give goroutines time to clean up
	time.Sleep(100 * time.Millisecond)
	
	after := runtime.NumGoroutine()
	
	if after > before {
		buf := make([]byte, 1<<16)
		stackSize := runtime.Stack(buf, true)
		t.Errorf("Goroutine leak detected! Before: %d, After: %d\nGoroutine dump:\n%s",
			before, after, buf[:stackSize])
	}
}