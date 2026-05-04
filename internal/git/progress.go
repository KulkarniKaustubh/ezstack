package git

// ProgressStart, when non-nil, is invoked by long-running git operations
// (rebase, merge, fetch, worktree add) to report a delayed progress
// indicator. It returns a stop function the operation calls when work
// completes (success or failure). When nil — the default — every call
// site is a no-op, which is what tests, the MCP binary, and any other
// non-interactive consumer want.
//
// Why a hook here rather than importing internal/ui directly:
// internal/git is a leaf in the package layering — internal/config,
// internal/stack, and the entire CLI depend on it. Importing ui would
// force ui's surface (term, spinner, ANSI codes) onto every transitive
// dependent, including non-interactive callers that have no use for
// spinners. The cmd/ezs binary wires this hook in main; everything else
// stays silent without ceremony.
var ProgressStart func(message string) (stop func())

// startProgress is the internal call site used by long-running git ops.
// Always returns a non-nil stop function so callers can use the
// `defer startProgress(...)()` pattern without nil checks — even if a
// future hook implementation forgets to return a stop func.
func startProgress(message string) func() {
	if ProgressStart == nil {
		return func() {}
	}
	if stop := ProgressStart(message); stop != nil {
		return stop
	}
	return func() {}
}
