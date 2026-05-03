package config

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAcquireFileLock_SerialAcquireRelease is the cheapest possible smoke
// test: acquire, release, acquire again. Catches a regression where the
// release path leaks the underlying flock so the second acquire would
// block forever (or hit the timeout path).
func TestAcquireFileLock_SerialAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	for i := 0; i < 3; i++ {
		l, err := acquireFileLock(path)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		l.release()
	}
}

// TestAcquireFileLock_BlocksUntilHolderReleases verifies the polling
// behavior: when the lock is held, a second acquire blocks until the
// first releases (within the polling interval), and then succeeds.
//
// We don't assert exact timing because CI is noisy, but we do assert that
// the second acquire ran *after* the holder released — otherwise the lock
// isn't actually serializing access.
func TestAcquireFileLock_BlocksUntilHolderReleases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	l1, err := acquireFileLock(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// Tighten the polling interval and notice/timeout for the test so we
	// don't spend 60s on the timeout path if something regresses.
	origPoll, origNotice, origTimeout := LockPollInterval, LockWaitNotice, LockTimeout
	LockPollInterval = 5 * time.Millisecond
	LockWaitNotice = 100 * time.Millisecond
	LockTimeout = 5 * time.Second
	defer func() {
		LockPollInterval, LockWaitNotice, LockTimeout = origPoll, origNotice, origTimeout
	}()

	releasedAt := make(chan time.Time, 1)
	acquiredAt := make(chan time.Time, 1)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		l2, err := acquireFileLock(path)
		acquiredAt <- time.Now()
		if err != nil {
			t.Errorf("second acquire: %v", err)
			return
		}
		l2.release()
	}()

	// Hold for ~50ms so the second acquire definitely starts polling.
	time.Sleep(50 * time.Millisecond)
	releasedAt <- time.Now()
	l1.release()

	wg.Wait()

	rel := <-releasedAt
	acq := <-acquiredAt
	if !acq.After(rel) {
		t.Errorf("second acquire returned at %v but holder released at %v — lock didn't serialize", acq, rel)
	}
}

// TestAcquireFileLock_TimesOutOnStuckHolder verifies the hard timeout
// path: if the holder never releases, the second acquire returns a
// timeout error rather than hanging forever. The error message must point
// at the lock path so a user knows what to clean up.
func TestAcquireFileLock_TimesOutOnStuckHolder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stuck.lock")

	holder, err := acquireFileLock(path)
	if err != nil {
		t.Fatalf("holder acquire: %v", err)
	}
	defer holder.release()

	origPoll, origNotice, origTimeout := LockPollInterval, LockWaitNotice, LockTimeout
	LockPollInterval = 5 * time.Millisecond
	LockWaitNotice = 1 * time.Hour // suppress notice line in test output
	LockTimeout = 100 * time.Millisecond
	defer func() {
		LockPollInterval, LockWaitNotice, LockTimeout = origPoll, origNotice, origTimeout
	}()

	start := time.Now()
	_, err = acquireFileLock(path)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed < LockTimeout {
		t.Errorf("returned in %v, want at least %v", elapsed, LockTimeout)
	}
	if elapsed > 5*time.Second {
		t.Errorf("returned in %v, way past timeout %v — polling loop may be unbounded", elapsed, LockTimeout)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("timeout error %q should name the lock path %q", err.Error(), path)
	}
	if !strings.Contains(err.Error(), "stuck") && !strings.Contains(err.Error(), "another ezs") {
		t.Errorf("timeout error %q should hint at stale-lock recovery", err.Error())
	}
}

// TestAcquireFileLock_PropagatesNonHeldErrors verifies that errors
// other than "lock held" (e.g. permission denied on the parent dir) are
// returned immediately rather than being retried — the caller can't fix
// them by waiting.
func TestAcquireFileLock_PropagatesNonHeldErrors(t *testing.T) {
	// Use a path inside a non-existent directory. OpenFile returns an
	// error that isn't errLockHeld, so acquireFileLock must surface it
	// without entering the poll loop.
	bogus := filepath.Join(t.TempDir(), "no", "such", "dir", "test.lock")

	origTimeout := LockTimeout
	LockTimeout = 5 * time.Second // generous; failure should be near-instant
	defer func() { LockTimeout = origTimeout }()

	start := time.Now()
	_, err := acquireFileLock(bogus)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error opening lock file in bogus path, got nil")
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("took %v to fail — non-held errors should not retry, but should return immediately", elapsed)
	}
}
