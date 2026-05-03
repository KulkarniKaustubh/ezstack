package itests

import (
	"os"
	"testing"
)

// TestMain dispatches to the concurrent-save helper when the parent test
// re-invokes this binary with EZS_CONCURRENT_SAVE_HELPER=1. The "test
// binary as a multi-purpose executable" pattern lets us exercise true
// multi-process file locking without shipping a separate helper binary.
//
// Important: always fall through to m.Run() in the normal case so the
// rest of the itest suite is unaffected.
func TestMain(m *testing.M) {
	if os.Getenv("EZS_CONCURRENT_SAVE_HELPER") == "1" {
		concurrentSaveHelperMain()
		os.Exit(0)
	}
	os.Exit(m.Run())
}
