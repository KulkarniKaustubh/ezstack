package itests

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
)

// Integration tests for `ezs up` and `ezs down` exercising the merged-branch
// regression that motivated this PR. The bug: `up` walked via BaseBranch and
// landed on merged ancestors whose git branch was deleted by MarkBranchMerged,
// causing `git checkout` to fail mid-traversal; `down` filtered direct merged
// children but never walked through them, so non-merged grandchildren of a
// merged child were unreachable.
//
// Each test sets up a real stack with worktrees, marks selected branches
// merged via the manager (the same path the merge command takes), chdirs
// into a worktree, and invokes commands.Up/Down. With EZS_SHELL_WRAPPER set,
// successful navigation emits a `cd <path>` line on stdout we can pin against
// the expected destination.

// changeToWorktreeOf chdirs the process into a branch's worktree for the
// duration of t. Restores the prior cwd via t.Cleanup so test ordering can't
// strand subsequent tests in a removed directory.
func changeToWorktreeOf(t *testing.T, env *TestEnv, branch string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	target := filepath.Join(env.WorktreeDir, branch)
	if err := os.Chdir(target); err != nil {
		t.Fatalf("chdir to %s: %v", target, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prev)
	})
}

// markMergedReload marks a branch merged through the manager and returns a
// freshly-loaded view. Mirrors what `ezs pr merge` does internally; we don't
// go through the CLI because `pr merge` requires the gh stub to coordinate a
// PR lifecycle and that is orthogonal to the navigation bug under test.
func markMergedReload(t *testing.T, env *TestEnv, branch string) {
	t.Helper()
	mgr, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := mgr.MarkBranchMerged(branch); err != nil {
		t.Fatalf("MarkBranchMerged(%s): %v", branch, err)
	}
}

// assertCd_To verifies the captured shell-wrapper output contains a `cd`
// directive pointing at the expected worktree path. We use Contains rather
// than equality because EmitCd may include shell quoting.
func assertCd_To(t *testing.T, output, expectedPath string) {
	t.Helper()
	if !strings.Contains(output, "cd ") {
		t.Fatalf("expected cd line in output; got:\n%s", output)
	}
	if !strings.Contains(output, expectedPath) {
		t.Errorf("expected cd target %q; got:\n%s", expectedPath, output)
	}
}

// assertNoCd verifies no `cd` directive was emitted — the navigation either
// ran into a "top of stack"/"already at leaf" case or surfaced an error
// before the cd step. Either way, the user's shell should not move.
func assertNoCd(t *testing.T, output string) {
	t.Helper()
	if strings.Contains(output, "cd ") {
		t.Errorf("expected no cd line; got:\n%s", output)
	}
}

// TestNavigateUp_NormalStack — sanity check that ordinary up navigation in an
// unmerged stack still works after the BaseBranch → Parent switch. If this
// regressed we'd take down every existing user, so it's the first gate.
func TestNavigateUp_NormalStack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "lvl-a", "main")
	CreateBranchWithCommit(t, env, "lvl-b", "lvl-a")
	CreateBranchWithCommit(t, env, "lvl-c", "lvl-b")

	t.Setenv("EZS_SHELL_WRAPPER", "1")
	changeToWorktreeOf(t, env, "lvl-c")

	var err error
	out := captureStdout(t, func() { err = commands.Up(nil) })
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	assertCd_To(t, out, filepath.Join(env.WorktreeDir, "lvl-b"))
}

// TestNavigateUp_SkipsMergedAncestor is the regression gate for the original
// bug: A → B(merged) → C, `ezs up` from C must land on A. Before the fix it
// landed on B and `git checkout` of the deleted B ref errored out.
func TestNavigateUp_SkipsMergedAncestor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "anc-a", "main")
	CreateBranchWithCommit(t, env, "anc-b", "anc-a")
	CreateBranchWithCommit(t, env, "anc-c", "anc-b")

	markMergedReload(t, env, "anc-b")

	t.Setenv("EZS_SHELL_WRAPPER", "1")
	changeToWorktreeOf(t, env, "anc-c")

	var err error
	out := captureStdout(t, func() { err = commands.Up(nil) })
	if err != nil {
		t.Fatalf("Up across merged ancestor: %v", err)
	}
	// Critical: cd target is anc-a (the next live ancestor), NOT anc-b.
	assertCd_To(t, out, filepath.Join(env.WorktreeDir, "anc-a"))
	if strings.Contains(out, filepath.Join(env.WorktreeDir, "anc-b")) {
		t.Errorf("up landed on merged anc-b; output:\n%s", out)
	}
}

// TestNavigateUp_MultiStep_AcrossMergedChain pins multi-step semantics:
// `ezs up 2` from D in main → A → B(merged) → C(merged) → D should reach A
// in the requested two steps (D → C-skipped-to-A is one step's worth via
// effective Parent; the second step from A reaches "top of stack" and stops
// without error). We verify it lands on A with the no-further-movement
// indicator rather than crashing.
func TestNavigateUp_MultiStep_AcrossMergedChain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "ms-a", "main")
	CreateBranchWithCommit(t, env, "ms-b", "ms-a")
	CreateBranchWithCommit(t, env, "ms-c", "ms-b")
	CreateBranchWithCommit(t, env, "ms-d", "ms-c")

	markMergedReload(t, env, "ms-b")
	markMergedReload(t, env, "ms-c")

	t.Setenv("EZS_SHELL_WRAPPER", "1")
	changeToWorktreeOf(t, env, "ms-d")

	var err error
	out := captureStdout(t, func() { err = commands.Up([]string{"2"}) })
	if err != nil {
		t.Fatalf("Up 2 across merged chain: %v", err)
	}
	// First step: ms-d → ms-a (skipping merged ms-c, ms-b).
	// Second step: ms-a → top of stack (Parent is "main", not in stack).
	// Loop breaks; cd target is ms-a.
	assertCd_To(t, out, filepath.Join(env.WorktreeDir, "ms-a"))
}

// TestNavigateUp_TopOfStackEmitsNoCd — at the top, no cd is emitted. The
// user's shell must not move.
func TestNavigateUp_TopOfStackEmitsNoCd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "top-only", "main")

	t.Setenv("EZS_SHELL_WRAPPER", "1")
	changeToWorktreeOf(t, env, "top-only")

	var err error
	out := captureStdout(t, func() { err = commands.Up(nil) })
	if err != nil {
		t.Fatalf("Up at top: %v", err)
	}
	assertNoCd(t, out)
}

// TestNavigateUp_AllAncestorsMerged_StopsAtTop — every ancestor between the
// current branch and the trunk is merged; `up` reports "top of stack" instead
// of trying to checkout a deleted branch.
func TestNavigateUp_AllAncestorsMerged_StopsAtTop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "all-a", "main")
	CreateBranchWithCommit(t, env, "all-b", "all-a")

	markMergedReload(t, env, "all-a")

	t.Setenv("EZS_SHELL_WRAPPER", "1")
	changeToWorktreeOf(t, env, "all-b")

	var err error
	out := captureStdout(t, func() { err = commands.Up(nil) })
	if err != nil {
		t.Fatalf("Up with all merged ancestors: %v", err)
	}
	assertNoCd(t, out)
}

// TestNavigateDown_NormalStack — single-child down navigation in an unmerged
// chain still works. Regression gate.
func TestNavigateDown_NormalStack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "dn-a", "main")
	CreateBranchWithCommit(t, env, "dn-b", "dn-a")

	t.Setenv("EZS_SHELL_WRAPPER", "1")
	changeToWorktreeOf(t, env, "dn-a")

	var err error
	out := captureStdout(t, func() { err = commands.Down(nil) })
	if err != nil {
		t.Fatalf("Down: %v", err)
	}
	assertCd_To(t, out, filepath.Join(env.WorktreeDir, "dn-b"))
}

// TestNavigateDown_SkipsMergedDirectChild is the regression gate for `down`:
// A → B(merged) → C should let `down` from A reach C. Before the fix, the
// merged-direct-child filter saw B, dropped it, and reported "no child
// branches" — leaving C unreachable from above.
func TestNavigateDown_SkipsMergedDirectChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "skip-a", "main")
	CreateBranchWithCommit(t, env, "skip-b", "skip-a")
	CreateBranchWithCommit(t, env, "skip-c", "skip-b")

	markMergedReload(t, env, "skip-b")

	t.Setenv("EZS_SHELL_WRAPPER", "1")
	changeToWorktreeOf(t, env, "skip-a")

	var err error
	out := captureStdout(t, func() { err = commands.Down(nil) })
	if err != nil {
		t.Fatalf("Down past merged child: %v", err)
	}
	assertCd_To(t, out, filepath.Join(env.WorktreeDir, "skip-c"))
}

// TestNavigateDown_OnlyMergedChild_StopsAtLeaf — when the only child is
// merged AND has no descendants, `down` reports "stack leaf" rather than
// pretending to navigate into a deleted branch.
func TestNavigateDown_OnlyMergedChild_StopsAtLeaf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "leaf-a", "main")
	CreateBranchWithCommit(t, env, "leaf-b", "leaf-a")

	markMergedReload(t, env, "leaf-b")

	t.Setenv("EZS_SHELL_WRAPPER", "1")
	changeToWorktreeOf(t, env, "leaf-a")

	var err error
	out := captureStdout(t, func() { err = commands.Down(nil) })
	if err != nil {
		t.Fatalf("Down with only merged child: %v", err)
	}
	assertNoCd(t, out)
}

// TestNavigateDown_MultiStepThroughMerged exercises a multi-step down that
// crosses a merged intermediate. Stack: A → B → C(merged) → D.
// `down 2` from A should land on D — step 1 picks B (only non-merged
// direct child), step 2 picks D (B's effective children are {C, D}, C is
// merged so D is the sole candidate).
func TestNavigateDown_MultiStepThroughMerged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "mt-a", "main")
	CreateBranchWithCommit(t, env, "mt-b", "mt-a")
	CreateBranchWithCommit(t, env, "mt-c", "mt-b")
	CreateBranchWithCommit(t, env, "mt-d", "mt-c")

	markMergedReload(t, env, "mt-c")

	t.Setenv("EZS_SHELL_WRAPPER", "1")
	changeToWorktreeOf(t, env, "mt-a")

	var err error
	out := captureStdout(t, func() { err = commands.Down([]string{"2"}) })
	if err != nil {
		t.Fatalf("Down 2 through merged: %v", err)
	}
	assertCd_To(t, out, filepath.Join(env.WorktreeDir, "mt-d"))
}

// TestNavigateUpThenDown_RoundTrip verifies the symmetric behavior: after
// up'ing past a merged ancestor and then down'ing again, you return to the
// original branch. This guards against asymmetric Parent/Child filtering
// where one direction sees the tree differently than the other.
func TestNavigateUpThenDown_RoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "rt-a", "main")
	CreateBranchWithCommit(t, env, "rt-b", "rt-a")
	CreateBranchWithCommit(t, env, "rt-c", "rt-b")

	markMergedReload(t, env, "rt-b")

	t.Setenv("EZS_SHELL_WRAPPER", "1")

	// Up from rt-c lands on rt-a.
	changeToWorktreeOf(t, env, "rt-c")
	var err error
	upOut := captureStdout(t, func() { err = commands.Up(nil) })
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	assertCd_To(t, upOut, filepath.Join(env.WorktreeDir, "rt-a"))

	// We have to physically chdir into rt-a now since the captureStdout
	// path doesn't actually evaluate the emitted cd — that's the shell
	// wrapper's job at runtime. The test process must do it manually so
	// the next Down() invocation reads the correct cwd.
	if err := os.Chdir(filepath.Join(env.WorktreeDir, "rt-a")); err != nil {
		t.Fatalf("chdir rt-a: %v", err)
	}

	downOut := captureStdout(t, func() { err = commands.Down(nil) })
	if err != nil {
		t.Fatalf("Down after up: %v", err)
	}
	// The only effective child of rt-a is rt-c (rt-b is merged), so Down
	// resolves uniquely back to rt-c without prompting.
	assertCd_To(t, downOut, filepath.Join(env.WorktreeDir, "rt-c"))
}

// TestNavigateDown_FiltersMergedSibling — when one direct child is merged
// and another is non-merged, the unmerged sibling is the unique candidate so
// `down` resolves to it without prompting.
func TestNavigateDown_FiltersMergedSibling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "sib-a", "main")
	CreateBranchWithCommit(t, env, "sib-merged", "sib-a")
	CreateBranchWithCommit(t, env, "sib-live", "sib-a")

	markMergedReload(t, env, "sib-merged")

	t.Setenv("EZS_SHELL_WRAPPER", "1")
	changeToWorktreeOf(t, env, "sib-a")

	var err error
	out := captureStdout(t, func() { err = commands.Down(nil) })
	if err != nil {
		t.Fatalf("Down with merged sibling: %v", err)
	}
	assertCd_To(t, out, filepath.Join(env.WorktreeDir, "sib-live"))
	if strings.Contains(out, "sib-merged") {
		t.Errorf("down surfaced the merged sibling; output:\n%s", out)
	}
}
