package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// errLockHeld is the sentinel returned by tryAcquireFileLockOnce when the
// lock file exists but another process is holding it. Anything else from
// the platform helper is treated as a real failure (open() error, fd
// exhaustion, ENOENT on the parent dir, etc.) and surfaced verbatim.
var errLockHeld = errors.New("lock held by another process")

// ErrLockTimeout is returned by acquireFileLock when LockTimeout elapses
// while a peer process actively holds the lock. Callers can distinguish
// this from "lock subsystem broken" failures (open errors, permission
// denied) so they can decide whether unlocked-fallback is appropriate.
// In particular, the LoadStackConfig migration path uses this to skip
// the persist step rather than racing the peer with an unlocked write.
var ErrLockTimeout = errors.New("timed out waiting for lock held by another process")

// LockWaitNotice is the duration we'll silently wait for the lock before
// printing a "waiting" message to stderr. Tunable for tests.
var LockWaitNotice = 2 * time.Second

// LockTimeout is the absolute ceiling on how long acquireFileLock will
// wait before giving up. Without this, a wedged ezstack process (debugger,
// SIGSTOP, NFS hang) would hang every future invocation forever. Tunable
// for tests.
var LockTimeout = 60 * time.Second

// LockPollInterval is the gap between non-blocking acquisition attempts
// while waiting. 50ms keeps CPU well under 1% even if many clients are
// queued, and bounds the wakeup latency when the holder releases.
var LockPollInterval = 50 * time.Millisecond

// acquireFileLock opens (creating if necessary) the lock file at path and
// acquires an exclusive lock on it. The first attempt is non-blocking; if
// another process holds the lock, we poll every LockPollInterval until we
// succeed or LockTimeout elapses, printing a one-time notice after
// LockWaitNotice so an interactive caller can tell whether ezs is hung
// vs. waiting on a peer.
//
// On timeout the returned error names the lock path and points the user at
// the recovery action (kill the stale ezs / delete the lock file). This
// is deliberately a hard failure rather than fallback to "best effort save"
// because skipping the lock would resurrect the load-modify-save race the
// lock exists to prevent.
func acquireFileLock(path string) (*fileLock, error) {
	deadline := time.Now().Add(LockTimeout)
	noticeAt := time.Now().Add(LockWaitNotice)
	noticed := false

	for {
		l, err := tryAcquireFileLockOnce(path)
		if err == nil {
			return l, nil
		}
		if !errors.Is(err, errLockHeld) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf(
				"%w: %s after %s — another ezs process appears stuck. "+
					"Check `ps` for hung ezs processes; if none, delete the lock file and retry",
				ErrLockTimeout, path, LockTimeout,
			)
		}
		if !noticed && time.Now().After(noticeAt) {
			noticed = true
			fmt.Fprintf(os.Stderr, "ezs: waiting for stacks.json lock (held by another ezs process)...\n")
		}
		time.Sleep(LockPollInterval)
	}
}
