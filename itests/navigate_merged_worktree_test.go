package itests

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

// Regression tests for navigating up/down *from* (and through) a branch that
// has been marked merged but whose worktree is still on disk. This is the state
// `ezs ls`/`ezs status` leaves a branch in when it detects the PR merged on
// GitHub: it caches IsMerged=true without deleting the worktree or git ref
// (unlike `ezs pr merge`, which removes both). Before the fix, `ezs down` from
// such a branch reported "Already at stack leaf" because walkTree re-points the
// merged branch's children onto its grandparent, so GetChildren returned
// nothing.

// markMergedCacheOnly marks a branch merged via the cache — the same path
// GetStackStatus takes when polling GitHub — WITHOUT deleting the worktree or
// the local git branch. Use this (not markMergedReload) to reproduce a merge
// that hasn't been pruned yet.
func markMergedCacheOnly(t *testing.T, env *TestEnv, branch string) {
	t.Helper()
	err := config.MutateBranchCache(env.RepoDir, branch, func(bc *config.BranchCache) (*config.BranchCache, error) {
		if bc == nil {
			bc = &config.BranchCache{}
		}
		bc.IsMerged = true
		return bc, nil
	})
	if err != nil {
		t.Fatalf("MutateBranchCache(%s): %v", branch, err)
	}
}

// TestNavigateUp_FromMergedWorktree — `ezs up` run from inside a merged branch's
// own (still-present) worktree lands on its parent.
func TestNavigateUp_FromMergedWorktree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "mw-a", "main")
	CreateBranchWithCommit(t, env, "mw-b", "mw-a") // merged, worktree kept
	CreateBranchWithCommit(t, env, "mw-c", "mw-b")

	markMergedCacheOnly(t, env, "mw-b")

	t.Setenv("EZS_SHELL_WRAPPER", "1")
	changeToWorktreeOf(t, env, "mw-b")

	var err error
	out := captureStdout(t, func() { err = commands.Up(nil) })
	if err != nil {
		t.Fatalf("Up from merged worktree: %v", err)
	}
	assertCd_To(t, out, filepath.Join(env.WorktreeDir, "mw-a"))
}

// TestNavigateDown_FromMergedWorktree is the core regression: `ezs down` from
// inside a merged branch's worktree must reach its child, not report a leaf.
func TestNavigateDown_FromMergedWorktree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "md-a", "main")
	CreateBranchWithCommit(t, env, "md-b", "md-a") // merged, worktree kept
	CreateBranchWithCommit(t, env, "md-c", "md-b")

	markMergedCacheOnly(t, env, "md-b")

	t.Setenv("EZS_SHELL_WRAPPER", "1")
	changeToWorktreeOf(t, env, "md-b")

	var err error
	out := captureStdout(t, func() { err = commands.Down(nil) })
	if err != nil {
		t.Fatalf("Down from merged worktree: %v", err)
	}
	assertCd_To(t, out, filepath.Join(env.WorktreeDir, "md-c"))
}

// TestNavigateDown_ThroughMergedWorktreeChild — a merged child whose worktree is
// still present is itself a valid stop, so `down` from its parent lands on the
// merged child (the first existing worktree) rather than skipping past it.
func TestNavigateDown_ThroughMergedWorktreeChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "tw-a", "main")
	CreateBranchWithCommit(t, env, "tw-b", "tw-a") // merged, worktree kept
	CreateBranchWithCommit(t, env, "tw-c", "tw-b")

	markMergedCacheOnly(t, env, "tw-b")

	t.Setenv("EZS_SHELL_WRAPPER", "1")
	changeToWorktreeOf(t, env, "tw-a")

	var err error
	out := captureStdout(t, func() { err = commands.Down(nil) })
	if err != nil {
		t.Fatalf("Down toward merged-but-present child: %v", err)
	}
	// tw-b's worktree still exists, so it's the first valid stop.
	assertCd_To(t, out, filepath.Join(env.WorktreeDir, "tw-b"))
}
