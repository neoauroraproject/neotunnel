//go:build !unix

package config

import "sync"

var mu sync.Mutex

// lock is process-local on platforms without flock (development only).
func lock() (func(), error) {
	mu.Lock()
	return mu.Unlock, nil
}

var applyMu sync.Mutex

// LockApply is process-local on platforms without flock.
func LockApply() (func(), error) {
	applyMu.Lock()
	return applyMu.Unlock, nil
}
