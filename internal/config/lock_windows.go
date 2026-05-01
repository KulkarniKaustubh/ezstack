//go:build windows

package config

// Windows: no-op flock. ezstack is primarily Unix; the lock is an
// advisory serialization helper, not a correctness requirement, so
// a no-op here is acceptable.
type fileLock struct{}

func acquireFileLock(path string) (*fileLock, error) { return &fileLock{}, nil }

func (l *fileLock) release() {}

// SyncLock is a no-op on Windows — Go's syscall package on Windows doesn't
// expose a non-blocking flock equivalent without pulling in golang.org/x/sys,
// and the existing Save-level lock is itself a no-op here. Practical
// consequences: two concurrent `ezs sync` invocations on Windows can race on
// stacks.json snapshot reads/writes and PR-metadata updates. ezstack is
// primarily Unix; if Windows users hit this, the workaround is to avoid
// running multiple `ezs sync` invocations simultaneously across repos that
// share an EZSTACK_HOME. See DOCUMENTATION.md (sync section) for details.
type SyncLock struct{}

func AcquireSyncLock(stacksJSONPath string) (*SyncLock, error) { return &SyncLock{}, nil }

func (s *SyncLock) Release() {}
