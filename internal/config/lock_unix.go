//go:build !windows

package config

import (
	"fmt"
	"os"
	"syscall"
)

// fileLock is an advisory exclusive flock held on a lock file next to the
// target config file. It serializes concurrent load-modify-save sequences
// from multiple ezstack processes running on the same machine.
type fileLock struct {
	f *os.File
}

func acquireFileLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return &fileLock{f: f}, nil
}

// SyncLock acquires an exclusive advisory flock on a long-lived lock file
// for the duration of a sync run. Returns a release function that callers
// must call (typically via defer) when the run completes. Failure to
// acquire the lock (e.g. another ezstack process is already syncing this
// repo) is returned as an error.
//
// The lock file is `stacks.json.sync.lock` next to stacks.json. This is
// distinct from the per-Save lock (`stacks.json.lock`) so a sync's atomic
// saves don't block on themselves.
type SyncLock struct {
	lock *fileLock
}

func AcquireSyncLock(stacksJSONPath string) (*SyncLock, error) {
	lockPath := stacksJSONPath + ".sync.lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	// Non-blocking flock — return EWOULDBLOCK as a typed error so callers
	// can surface "another sync is in progress" clearly instead of
	// hanging.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, fmt.Errorf("another `ezs sync` is already running on this repo (lock at %s)", lockPath)
		}
		return nil, err
	}
	return &SyncLock{lock: &fileLock{f: f}}, nil
}

func (s *SyncLock) Release() {
	if s == nil {
		return
	}
	s.lock.release()
}

func (l *fileLock) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
}
