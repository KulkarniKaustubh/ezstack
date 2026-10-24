//go:build windows

package ghstack

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock attempts a non-blocking exclusive lock on the first byte, matching
// gh-stack's LockFileEx usage. busy is true when another process holds it.
func tryLock(f *os.File) (busy bool, err error) {
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return true, nil
	}
	return false, err
}

func unlock(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
