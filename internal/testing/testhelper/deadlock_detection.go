//go:build deadlock
// +build deadlock

// Package testhelper provides deadlock detection support using go-deadlock
// To enable deadlock detection, run tests with: go test -tags=deadlock
package testhelper

import (
	"sync"
	"time"

	"github.com/sasha-s/go-deadlock"
)

func init() {
	// Configure go-deadlock
	deadlock.Opts.DeadlockTimeout = 30 * time.Second
	deadlock.Opts.OnPotentialDeadlock = func() {
		panic("Potential deadlock detected!")
	}
}

// Mutex is a drop-in replacement for sync.Mutex with deadlock detection
type Mutex struct {
	deadlock.Mutex
}

// RWMutex is a drop-in replacement for sync.RWMutex with deadlock detection
type RWMutex struct {
	deadlock.RWMutex
}

// NewMutex creates a new mutex with deadlock detection when built with -tags=deadlock
func NewMutex() sync.Locker {
	return &Mutex{}
}

// NewRWMutex creates a new RWMutex with deadlock detection when built with -tags=deadlock
func NewRWMutex() interface {
	sync.Locker
	RLock()
	RUnlock()
} {
	return &RWMutex{}
}
