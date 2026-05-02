//go:build windows

package config

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// fileLock holds an exclusive byte-range lock acquired via LockFileEx on a
// lock file next to the target config file. Serializes concurrent
// load-modify-save sequences from multiple ezstack processes on the same
// machine.
type fileLock struct {
	f *os.File
}

// Lock the entire 64-bit address range. Picking 0xFFFFFFFF/0xFFFFFFFF
// (a.k.a. ULL_MAX in two halves) covers anything LockFileEx will accept.
const lockBytesLow = 0xFFFFFFFF
const lockBytesHigh = 0xFFFFFFFF

func acquireFileLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := lockHandle(windows.Handle(f.Fd()), false); err != nil {
		f.Close()
		return nil, err
	}
	return &fileLock{f: f}, nil
}

// SyncLock is a long-lived non-blocking exclusive lock held on a sentinel
// file for the duration of a `ezs sync` run. Failure to acquire (another
// ezstack process is already syncing) is returned as an error so callers
// surface "another sync is in progress" instead of hanging.
//
// The lock file is `stacks.json.sync.lock` next to stacks.json. Because
// stacks.json itself is global per ezstack install, the lock is also global:
// concurrent syncs across different repos serialize. Distinct from the
// per-Save lock (`stacks.json.lock`) so a sync's atomic saves don't block
// on themselves.
type SyncLock struct {
	lock *fileLock
}

func AcquireSyncLock(stacksJSONPath string) (*SyncLock, error) {
	lockPath := stacksJSONPath + ".sync.lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := lockHandle(windows.Handle(f.Fd()), true); err != nil {
		f.Close()
		// LOCKFILE_FAIL_IMMEDIATELY surfaces ERROR_LOCK_VIOLATION when
		// another process holds the lock. Map to a typed message so the
		// CLI can distinguish "wait" from "broken state".
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, fmt.Errorf("another `ezs sync` is already running (lock at %s)", lockPath)
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
	var ov windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, lockBytesLow, lockBytesHigh, &ov)
	_ = l.f.Close()
}

func lockHandle(h windows.Handle, nonBlocking bool) error {
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if nonBlocking {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	var ov windows.Overlapped
	return windows.LockFileEx(h, flags, 0, lockBytesLow, lockBytesHigh, &ov)
}
