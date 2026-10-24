//go:build !windows

package ghstack

import (
	"errors"
	"os"
	"syscall"
)

// tryLock attempts a non-blocking exclusive flock. busy is true when another
// process holds it.
func tryLock(f *os.File) (busy bool, err error) {
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, nil
	}
	return false, err
}

func unlock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
