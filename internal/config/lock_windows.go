//go:build windows

package config

// Windows: no-op flock. ezstack is primarily Unix; the lock is an
// advisory serialization helper, not a correctness requirement, so
// a no-op here is acceptable.
type fileLock struct{}

func acquireFileLock(path string) (*fileLock, error) { return &fileLock{}, nil }

func (l *fileLock) release() {}

// SyncLock no-op for Windows.
type SyncLock struct{}

func AcquireSyncLock(stacksJSONPath string) (*SyncLock, error) { return &SyncLock{}, nil }

func (s *SyncLock) Release() {}
