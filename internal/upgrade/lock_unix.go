//go:build !windows

package upgrade

import (
	"errors"
	"os"
	"syscall"
)

// upgradeLock is a non-blocking flock held on a sentinel file inside
// binDir. It serializes the snapshot-and-swap phase of Run() so two
// `ezs upgrade` invocations in different terminals can't clobber each
// other's hard-link backups (the rollback files are a fixed name in
// binDir, so without this lock the loser's stale-backup cleanup would
// nuke the winner's live backup).
type upgradeLock struct {
	f *os.File
}

// errLockHeld signals that another ezstack upgrade already holds the
// lock. Callers should surface a "another upgrade in progress" message
// rather than blocking.
var errLockHeld = errors.New("upgrade lock held by another process")

func acquireUpgradeLock(path string) (*upgradeLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLockHeld
		}
		return nil, err
	}
	return &upgradeLock{f: f}, nil
}

func (l *upgradeLock) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	// Best-effort cleanup. A stale lockfile is harmless on the next
	// run because flock is kernel-managed: it's released automatically
	// on process death even if the file persists.
	_ = os.Remove(l.f.Name())
}
