//go:build !deadlock
// +build !deadlock

// Package testhelper provides standard mutex support when deadlock detection is disabled
package testhelper

import (
	"sync"
)

// Mutex is a standard sync.Mutex when deadlock detection is disabled
type Mutex struct {
	sync.Mutex
}

// RWMutex is a standard sync.RWMutex when deadlock detection is disabled
type RWMutex struct {
	sync.RWMutex
}

// NewMutex creates a standard mutex when not built with -tags=deadlock
func NewMutex() sync.Locker {
	return &Mutex{}
}

// NewRWMutex creates a standard RWMutex when not built with -tags=deadlock
func NewRWMutex() interface {
	sync.Locker
	RLock()
	RUnlock()
} {
	return &RWMutex{}
}