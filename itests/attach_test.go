package itests

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// runGit runs `git -C <dir> <args...>` and returns combined output. Used by
// tests that need to set up branch/worktree state directly via git, bypassing
// the stack manager — i.e., simulating a user who used plain git.
func runGit(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	return string(out), err
}

// withYesMode sets ui.YesMode and returns a restore func. Mirrors the pattern
// in delete_cascade_test.go — every Attach test needs this because the
// command always runs a "Proceed?" confirm.
func withYesMode(t *testing.T) func() {
	t.Helper()
	prev := ui.YesMode
	ui.YesMode = true
	return func() { ui.YesMode = prev }
}

// setRepoUseWorktrees flips the per-repo use_worktrees flag so a single test
// can exercise both modes without booting a second repo.
func setRepoUseWorktrees(t *testing.T, env *TestEnv, useWorktrees bool) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	rc := cfg.GetRepoConfig(env.RepoDir)
	if rc == nil {
		rc = &config.RepoConfig{WorktreeBaseDir: env.WorktreeDir}
	}
	v := useWorktrees
	rc.UseWorktrees = &v
	cfg.SetRepoConfig(env.RepoDir, rc)
	if err := cfg.Save(); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}
}

// commitOnBranch runs git commands to create a commit on a bare branch
// without checking it out — used to simulate a user who created a branch via
// `git checkout -b`, made commits, then went back to main without ezs ever
// seeing the branch.
func commitOnBranchInWorktree(t *testing.T, worktreeDir, filename, content string) {
	t.Helper()
	GitCommit(t, worktreeDir, filename, content, "commit on "+filepath.Base(worktreeDir))
}

// TestAttach_BareBranchInWorktreeMode covers the headline case: user has a
// bare local branch (no ezs metadata, no worktree), repo is in worktree
// mode. `ezs attach` should infer the parent, create the worktree, and
// register the branch in the right stack.
func TestAttach_BareBranchInWorktreeMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	defer withYesMode(t)()

	gitBareBranch(t, env.RepoDir, "feature-x", "main")

	chdirForTest(t, env.RepoDir)
	if err := commands.Attach([]string{"feature-x"}); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("feature-x")
	if branch == nil {
		t.Fatalf("feature-x not registered in any stack after attach")
	}
	if branch.Parent != "main" {
		t.Errorf("parent = %q, want %q (auto-detected from merge-base)", branch.Parent, "main")
	}
	expected := filepath.Join(env.WorktreeDir, "feature-x")
	if branch.WorktreePath != expected {
		t.Errorf("worktree path = %q, want %q", branch.WorktreePath, expected)
	}
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("worktree was not materialized at %s: %v", expected, err)
	}
}

// TestAttach_Idempotent verifies the design's central promise: running
// `ezs attach` twice on the same branch is safe and a no-op the second time.
func TestAttach_Idempotent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	defer withYesMode(t)()

	gitBareBranch(t, env.RepoDir, "feature-x", "main")

	chdirForTest(t, env.RepoDir)
	if err := commands.Attach([]string{"feature-x"}); err != nil {
		t.Fatalf("first Attach: %v", err)
	}
	// Snapshot stack state.
	mgrA, _ := stack.NewManager(env.RepoDir)
	stacksBefore := len(mgrA.ListStacks())
	branchBefore := mgrA.GetBranch("feature-x")

	out := captureStderr(t, func() {
		if err := commands.Attach([]string{"feature-x"}); err != nil {
			t.Fatalf("second Attach: %v", err)
		}
	})
	if !strings.Contains(out, "already attached") {
		t.Errorf("second attach should print 'already attached' info; got:\n%s", out)
	}

	mgrB, _ := stack.NewManager(env.RepoDir)
	if got := len(mgrB.ListStacks()); got != stacksBefore {
		t.Errorf("stack count changed: before=%d, after=%d", stacksBefore, got)
	}
	branchAfter := mgrB.GetBranch("feature-x")
	if branchAfter == nil {
		t.Fatalf("branch missing after second attach")
	}
	if branchBefore.Parent != branchAfter.Parent || branchBefore.WorktreePath != branchAfter.WorktreePath {
		t.Errorf("attach mutated state on no-op: before=%+v after=%+v", branchBefore, branchAfter)
	}
}

// TestAttach_ParentDetectionPrefersClosestAncestor verifies the design's
// "closest tracked ancestor" rule: a branch forked off feature-a (which is
// itself a child of main) must attach under feature-a, not main.
func TestAttach_ParentDetectionPrefersClosestAncestor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	defer withYesMode(t)()

	// Set up a real stack: main → feature-a (with one commit).
	CreateBranchWithCommit(t, env, "feature-a", "main")

	// Create feature-x off feature-a using plain git, so ezs doesn't know about it.
	featureAPath := filepath.Join(env.WorktreeDir, "feature-a")
	gitBareBranch(t, featureAPath, "feature-x", "feature-a")

	chdirForTest(t, env.RepoDir)
	if err := commands.Attach([]string{"feature-x"}); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("feature-x")
	if branch == nil {
		t.Fatalf("feature-x not registered")
	}
	if branch.Parent != "feature-a" {
		t.Errorf("parent = %q, want %q (closest tracked ancestor)", branch.Parent, "feature-a")
	}
	// Should land in the same stack as feature-a, not a new one.
	stackOfFeatureX := mgr.GetStackForBranch("feature-x")
	stackOfFeatureA := mgr.GetStackForBranch("feature-a")
	if stackOfFeatureX == nil || stackOfFeatureA == nil {
		t.Fatalf("missing stack: x=%v a=%v", stackOfFeatureX, stackOfFeatureA)
	}
	if stackOfFeatureX.Hash != stackOfFeatureA.Hash {
		t.Errorf("feature-x landed in stack %q, expected the same stack as feature-a (%q)",
			stackOfFeatureX.Hash, stackOfFeatureA.Hash)
	}
}

// TestAttach_ParentOverride: -p flag bypasses auto-detection.
func TestAttach_ParentOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	defer withYesMode(t)()

	CreateBranchWithCommit(t, env, "feature-a", "main")
	gitBareBranch(t, env.RepoDir, "feature-x", "main") // forked from main

	chdirForTest(t, env.RepoDir)
	// Force feature-x under feature-a even though merge-base would say main.
	if err := commands.Attach([]string{"feature-x", "-p", "feature-a"}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("feature-x")
	if branch == nil || branch.Parent != "feature-a" {
		t.Errorf("parent override didn't take: branch=%+v", branch)
	}
}

// TestAttach_ExistingWorktreeIsRegisteredNotRecreated covers the "user did
// `git worktree add` manually, ezs doesn't know" case. Attach should pick
// up the existing worktree path rather than try to create a second one.
func TestAttach_ExistingWorktreeIsRegisteredNotRecreated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	defer withYesMode(t)()

	manualPath := filepath.Join(env.TmpDir, "manual-feature-x")
	g := git.New(env.RepoDir)
	if _, err := runGit(env.RepoDir, "branch", "feature-x", "main"); err != nil {
		t.Fatalf("git branch: %v", err)
	}
	if _, err := runGit(env.RepoDir, "worktree", "add", manualPath, "feature-x"); err != nil {
		t.Fatalf("git worktree add: %v", err)
	}

	chdirForTest(t, env.RepoDir)
	if err := commands.Attach([]string{"feature-x"}); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("feature-x")
	if branch == nil {
		t.Fatalf("feature-x not registered")
	}
	if branch.WorktreePath != manualPath {
		t.Errorf("worktree path = %q, want existing path %q", branch.WorktreePath, manualPath)
	}

	// Sanity: there is exactly one worktree for feature-x in git's view —
	// attach must not have spun up a duplicate at the default location.
	wts, err := g.ListWorktrees()
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	count := 0
	for _, w := range wts {
		if w.Branch == "feature-x" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one worktree for feature-x; got %d (%+v)", count, wts)
	}
}

// TestAttach_NoWorktreeFlagRespected: -W forces bare attach in worktree-mode repo.
func TestAttach_NoWorktreeFlagRespected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	defer withYesMode(t)()

	gitBareBranch(t, env.RepoDir, "feature-x", "main")

	chdirForTest(t, env.RepoDir)
	if err := commands.Attach([]string{"feature-x", "-W"}); err != nil {
		t.Fatalf("Attach -W: %v", err)
	}
	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("feature-x")
	if branch == nil {
		t.Fatalf("feature-x not registered")
	}
	if branch.WorktreePath != "" {
		t.Errorf("expected bare attach, but got worktree at %q", branch.WorktreePath)
	}
	if _, err := os.Stat(filepath.Join(env.WorktreeDir, "feature-x")); err == nil {
		t.Errorf("worktree dir was created despite -W")
	}
}

// TestAttach_NonexistentBranchErrors: clear error and signpost to ezs new
// when the branch doesn't actually exist.
func TestAttach_NonexistentBranchErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	defer withYesMode(t)()

	chdirForTest(t, env.RepoDir)
	err := commands.Attach([]string{"feature-x"})
	if err == nil {
		t.Fatalf("expected error for missing branch")
	}
	if !strings.Contains(err.Error(), "does not exist") || !strings.Contains(err.Error(), "ezs new") {
		t.Errorf("error should signpost `ezs new`; got: %s", err.Error())
	}
}

// TestAttach_BareBranchInBranchMode covers the no-worktree-config path:
// repo configured for branch mode should attach as bare (no worktree
// materialized) and remain a no-op on a second run.
func TestAttach_BareBranchInBranchMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	defer withYesMode(t)()

	setRepoUseWorktrees(t, env, false)

	gitBareBranch(t, env.RepoDir, "feature-x", "main")

	chdirForTest(t, env.RepoDir)
	if err := commands.Attach([]string{"feature-x"}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("feature-x")
	if branch == nil {
		t.Fatalf("feature-x not registered")
	}
	if branch.WorktreePath != "" {
		t.Errorf("expected bare attach in branch-mode repo, got worktree at %q", branch.WorktreePath)
	}

	// Second run is a no-op.
	out := captureStderr(t, func() {
		if err := commands.Attach([]string{"feature-x"}); err != nil {
			t.Fatalf("second Attach: %v", err)
		}
	})
	if !strings.Contains(out, "already attached") {
		t.Errorf("second attach in branch mode should be a no-op; got:\n%s", out)
	}
}

// TestAttach_TrackedNoWorktreeMaterializesOnSecondRun: simulates the
// "switch the repo from no-worktree mode to worktree mode, then run
// `ezs attach` to upgrade existing branches" flow. Tracked branch with no
// worktree + use_worktrees=true => attach materializes the worktree and
// updates the cache, leaving the stack tree unchanged.
func TestAttach_TrackedNoWorktreeMaterializesOnSecondRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	defer withYesMode(t)()

	// Create the branch in branch-mode (no worktree).
	setRepoUseWorktrees(t, env, false)
	mgr0, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, err := mgr0.CreateBranchNoWorktree("feature-x", "main", ""); err != nil {
		t.Fatalf("CreateBranchNoWorktree: %v", err)
	}
	stackHashBefore := mgr0.GetStackForBranch("feature-x").Hash

	// Flip to worktree mode and attach to materialize.
	setRepoUseWorktrees(t, env, true)

	chdirForTest(t, env.RepoDir)
	if err := commands.Attach([]string{"feature-x"}); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("feature-x")
	if branch == nil {
		t.Fatalf("feature-x missing after attach")
	}
	expected := filepath.Join(env.WorktreeDir, "feature-x")
	if branch.WorktreePath != expected {
		t.Errorf("worktree path = %q, want %q", branch.WorktreePath, expected)
	}
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("worktree was not materialized at %s: %v", expected, err)
	}
	// Stack identity is preserved — attach only filled in the worktree path.
	if got := mgr.GetStackForBranch("feature-x").Hash; got != stackHashBefore {
		t.Errorf("stack hash changed: before=%q after=%q", stackHashBefore, got)
	}
}

// TestAttach_PlanIsShownBeforeChanges: the plan preview is part of the UX
// promise — the user must be able to see what's about to happen. Captured
// stderr should include the Plan/Branch/Parent/Stack/Worktree banner.
func TestAttach_PlanIsShownBeforeChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	defer withYesMode(t)()

	gitBareBranch(t, env.RepoDir, "feature-x", "main")
	chdirForTest(t, env.RepoDir)

	out := captureStderr(t, func() {
		if err := commands.Attach([]string{"feature-x"}); err != nil {
			t.Fatalf("Attach: %v", err)
		}
	})
	for _, want := range []string{"Plan:", "Branch:", "Parent:", "Stack:", "Worktree:"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan output missing %q; got:\n%s", want, out)
		}
	}
}
