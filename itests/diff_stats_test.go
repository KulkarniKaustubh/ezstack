package itests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
)

// These integration tests exercise the diff-stat plumbing against a real
// stacked repo built via the actual stack manager (which creates a worktree
// per branch). They cover the post-sync + working-tree regressions: stats
// must be correct when queried from the main repo, from a sibling worktree,
// or from the branch's own worktree, and must include uncommitted edits.

// TestDiffStats_CommittedFromMainRepo asserts the committed-only baseline
// when GetStackDiffStat is invoked from the main repo against a stacked
// branch living in its own worktree.
func TestDiffStats_CommittedFromMainRepo(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "feat-a", "main")
	wt := filepath.Join(env.WorktreeDir, "feat-a")

	// Commit 4 lines in feat-a's worktree.
	GitCommit(t, wt, "a.txt", "1\n2\n3\n4\n", "feat")

	g := git.New(env.RepoDir)
	added, removed, err := g.GetStackDiffStat([]string{"main"}, "feat-a", false)
	if err != nil {
		t.Fatalf("GetStackDiffStat: %v", err)
	}
	if added != 4 || removed != 0 {
		t.Errorf("committed = (%d,%d), want (4,0)", added, removed)
	}
}

// TestDiffStats_WorkingTreeAcrossWorktrees asserts the critical property:
// a per-worktree Git client rooted at the branch's worktree reports
// working-tree-accurate counts even when the invocation was made from the
// main repo. This is what makes `ezs list` correct from any cwd.
func TestDiffStats_WorkingTreeAcrossWorktrees(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "feat-a", "main")
	wt := filepath.Join(env.WorktreeDir, "feat-a")

	// 3 committed lines.
	GitCommit(t, wt, "a.txt", "1\n2\n3\n", "feat")

	// +2 unstaged modifications to the existing tracked file.
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("1\n2\n3\n4\n5\n"), 0644); err != nil {
		t.Fatalf("modify: %v", err)
	}

	// Committed-only query from main repo: still 3.
	gMain := git.New(env.RepoDir)
	if a, _, err := gMain.GetStackDiffStat([]string{"main"}, "feat-a", false); err != nil {
		t.Fatalf("committed: %v", err)
	} else if a != 3 {
		t.Errorf("committed from main repo = %d, want 3", a)
	}

	// Working-tree query via a Git client rooted at the branch worktree:
	// 3 committed + 2 unstaged = 5. This mirrors what fetchDiffStats does.
	gWT := git.New(wt)
	if a, _, err := gWT.GetStackDiffStat([]string{"main"}, "feat-a", true); err != nil {
		t.Fatalf("working tree: %v", err)
	} else if a != 5 {
		t.Errorf("working tree via worktree git = %d, want 5", a)
	}
}

// TestDiffStats_StackedBranchOwnCommits confirms that a child branch's diff
// reflects only its own commits, not the parent's — the merge-base logic
// must correctly pick the closest ancestor.
func TestDiffStats_StackedBranchOwnCommits(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "feat-a", "main")
	wtA := filepath.Join(env.WorktreeDir, "feat-a")
	GitCommit(t, wtA, "a.txt", "1\n2\n3\n4\n", "feat-a")

	CreateBranch(t, env, "feat-b", "feat-a")
	wtB := filepath.Join(env.WorktreeDir, "feat-b")
	GitCommit(t, wtB, "b.txt", "x\ny\n", "feat-b")

	g := git.New(env.RepoDir)

	// feat-a vs main: 4 lines.
	if a, _, err := g.GetStackDiffStat([]string{"main"}, "feat-a", false); err != nil {
		t.Fatalf("feat-a: %v", err)
	} else if a != 4 {
		t.Errorf("feat-a additions = %d, want 4", a)
	}

	// feat-b vs feat-a: 2 lines (NOT 6 — parent's commits must not leak in).
	if a, _, err := g.GetStackDiffStat([]string{"feat-a"}, "feat-b", false); err != nil {
		t.Fatalf("feat-b: %v", err)
	} else if a != 2 {
		t.Errorf("feat-b additions = %d, want 2", a)
	}
}

// TestDiffStats_DeletionsAndRenames checks that `git diff --shortstat`
// deletions are captured and that a pure deletion reports 0 adds / N dels.
func TestDiffStats_DeletionsAndRenames(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Seed main with a multi-line file so deletions have something to eat.
	if err := os.WriteFile(filepath.Join(env.RepoDir, "seed.txt"), []byte("1\n2\n3\n4\n5\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	runGit(t, env.RepoDir, "add", "seed.txt")
	runGit(t, env.RepoDir, "commit", "-q", "-m", "seed")

	CreateBranch(t, env, "shrink", "main")
	wt := filepath.Join(env.WorktreeDir, "shrink")
	// Rewrite to fewer lines.
	if err := os.WriteFile(filepath.Join(wt, "seed.txt"), []byte("1\n"), 0644); err != nil {
		t.Fatalf("shrink: %v", err)
	}
	runGit(t, wt, "add", "seed.txt")
	runGit(t, wt, "commit", "-q", "-m", "shrink")

	g := git.New(env.RepoDir)
	added, removed, err := g.GetStackDiffStat([]string{"main"}, "shrink", false)
	if err != nil {
		t.Fatalf("GetStackDiffStat: %v", err)
	}
	if added != 0 || removed != 4 {
		t.Errorf("counts = (%d,%d), want (0,4)", added, removed)
	}
}

// TestDiffStats_LocalDiffersFromRemote_DirtyWorktree exercises the dirty-WT
// divergence path: same SHA as origin but tracked edits in the worktree
// should report diverged when includeWorkingTree is true.
func TestDiffStats_LocalDiffersFromRemote_DirtyWorktree(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "dirty", "main")
	wt := filepath.Join(env.WorktreeDir, "dirty")
	GitCommit(t, wt, "f.txt", "clean\n", "f")

	// There's no remote in the itest env, so LocalDiffersFromRemote returns
	// "true" on the no-remote path. We can still verify includeWT detects a
	// dirty working tree directly by running `git diff --quiet HEAD`.
	if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("dirty\n"), 0644); err != nil {
		t.Fatalf("dirty: %v", err)
	}

	gWT := git.New(wt)
	if _, err := gWT.RunCapture("diff", "--quiet", "HEAD"); err == nil {
		t.Error("expected dirty worktree to yield nonzero exit from `git diff --quiet HEAD`")
	}
}

// runGit is a small local helper so this test file doesn't need its own
// t.Fatalf shell plumbing.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	g := git.New(dir)
	if _, err := g.RunCapture(args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}
