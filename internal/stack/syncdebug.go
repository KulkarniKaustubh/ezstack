package stack

import (
	"fmt"
	"os"
	"sync"
)

// debugLog prints a structured tag + key=value line to stderr when the
// EZSTACK_DEBUG environment variable is non-empty. Used by sync to record
// which path was taken at each decision point — fast-forward, --onto with
// snapshot, plain rebase fallback — so field bug reports include enough
// detail to debug without re-running.
//
// Cheap when disabled: a single env-var lookup (cached after the first
// call) and a no-op return. The lookup is cached in a sync.Once so we
// don't pay the syscall on every log call inside hot loops.
var (
	debugOnce    sync.Once
	debugEnabled bool
)

func debugLog(tag string, kv ...string) {
	debugOnce.Do(func() {
		debugEnabled = os.Getenv("EZSTACK_DEBUG") != ""
	})
	if !debugEnabled {
		return
	}
	if len(kv)%2 != 0 {
		kv = append(kv, "<missing>")
	}
	out := "[ezstack:" + tag + "]"
	for i := 0; i < len(kv); i += 2 {
		out += " " + kv[i] + "=" + kv[i+1]
	}
	fmt.Fprintln(os.Stderr, out)
}
