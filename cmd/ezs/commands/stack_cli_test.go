package commands

import (
	"strings"
	"testing"
)

// TestStack_AddsUntrackedBranchToStack confirms `ezs stack` can pull an
// existing git branch into ezstack tracking under a chosen parent. This
// is the primary advertised use case ("Add a branch to a stack") and
// regressed silently in the past because doReparentNoRebase is shared
// with `ezs reparent` — changes made for one path can break the other.
//
// Setup: tracked branch `parent` rooted on main; an untracked git branch
// `loose` created via plain git. Calling `Stack -b loose -p parent` must
// promote `loose` into the same stack as `parent`.
func TestStack_AddsUntrackedBranchToStack(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)

	mgr := reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("parent", "main", "new"); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	// Plain git branch — not tracked by ezstack yet.
	mustGit(t, repoDir, "branch", "loose", "main")

	if err := Stack([]string{"-b", "loose", "-p", "parent"}); err != nil {
		t.Fatalf("Stack: %v", err)
	}

	mgr = reloadManager(t, repoDir)
	got := mgr.GetBranch("loose")
	if got == nil {
		t.Fatal("loose was not added to tracking")
	}
	if got.Parent != "parent" {
		t.Errorf("loose.Parent = %q, want parent", got.Parent)
	}
}

// TestStack_BaseAndParentMutuallyExclusive locks down the validation that
// both a base branch (start a new stack) and parent (add to existing stack)
// can't be specified at once. Without this check the user-supplied parent
// would be silently overridden by the base flag.
func TestStack_BaseAndParentMutuallyExclusive(t *testing.T) {
	setupCLITestEnv(t)
	err := Stack([]string{"-b", "loose", "-p", "main", "-B", "develop"})
	if err == nil {
		t.Fatal("Stack accepted both --parent and --base; want refusal")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error %q should explain that --parent and --base conflict", err.Error())
	}
}

// TestUnstack_RemovesFromTracking exercises the happy path: a tracked
// branch with no children gets dropped from ezstack but the git branch
// itself is preserved. The contract is non-destructive — Unstack is the
// "I changed my mind" command.
func TestUnstack_RemovesFromTracking(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	mgr := reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("ephemeral", "main", "new"); err != nil {
		t.Fatalf("create ephemeral: %v", err)
	}

	if err := Unstack([]string{"ephemeral"}); err != nil {
		t.Fatalf("Unstack: %v", err)
	}

	mgr = reloadManager(t, repoDir)
	if b := mgr.GetBranch("ephemeral"); b != nil {
		t.Errorf("ephemeral still tracked after Unstack: %+v", b)
	}
}

// TestUnstack_NonexistentBranch_Errors guards the input-validation path:
// if the user types an unknown branch name, surface a clear error rather
// than crashing or silently no-op'ing.
func TestUnstack_NonexistentBranch_Errors(t *testing.T) {
	setupCLITestEnv(t)
	err := Unstack([]string{"never-existed"})
	if err == nil {
		t.Fatal("Unstack of unknown branch returned nil; want error")
	}
	if !strings.Contains(err.Error(), "not tracked") {
		t.Errorf("error %q should mention 'not tracked' to be actionable", err.Error())
	}
}

// TestUnstack_ChildrenReparentedToGrandparent verifies the documented
// behavior at stack.go:307-308 ("If the branch has children, they will
// be reparented to the untracked branch's parent"). Without this, kids
// would be orphaned with a dangling Parent pointer.
//
// Tree before: main → mid → leaf
// Unstack mid.
// Expected: leaf.Parent == "main", and mid is no longer tracked.
func TestUnstack_ChildrenReparentedToGrandparent(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	mgr := reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("mid", "main", "new"); err != nil {
		t.Fatalf("create mid: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("leaf", "mid", ""); err != nil {
		t.Fatalf("create leaf: %v", err)
	}

	if err := Unstack([]string{"mid"}); err != nil {
		t.Fatalf("Unstack: %v", err)
	}

	mgr = reloadManager(t, repoDir)
	if b := mgr.GetBranch("mid"); b != nil {
		t.Errorf("mid still tracked: %+v", b)
	}
	leaf := mgr.GetBranch("leaf")
	if leaf == nil {
		t.Fatal("leaf was lost during Unstack — children must survive")
	}
	if leaf.Parent != "main" {
		t.Errorf("leaf.Parent = %q after Unstack(mid); want main (grandparent)", leaf.Parent)
	}
}
