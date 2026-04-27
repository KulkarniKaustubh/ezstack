//go:build windows

package upgrade

// Windows: ezstack does not publish Windows release artifacts (see
// supportedPlatform), so Run() exits with "unsupported platform" before
// the lock would be needed. No-op stubs satisfy the build.

import "errors"

type upgradeLock struct{}

var errLockHeld = errors.New("upgrade lock held by another process")

func acquireUpgradeLock(path string) (*upgradeLock, error) { return &upgradeLock{}, nil }

func (l *upgradeLock) release() {}
