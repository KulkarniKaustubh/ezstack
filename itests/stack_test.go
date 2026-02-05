package itests

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ezstack/ezstack/internal/git"
	"github.com/ezstack/ezstack/internal/stack"
)

// TestUnstackBranch tests removing a branch from tracking without deleting it
func TestUnstackBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "to-unstack", "main")
	AssertBranchExists(t, env, "to-unstack")

	mgr, _ := stack.NewManager(env.RepoDir)
	if err := mgr.UntrackBranch("to-unstack"); err != nil {
		t.Fatalf("UntrackBranch failed: %v", err)
	}

	// Branch should no longer be tracked
	AssertBranchNotExists(t, env, "to-unstack")

	// But the git branch should still exist
	g := git.New(env.RepoDir)
	if !g.BranchExists("to-unstack") {
		t.Error("Git branch should still exist after unstack")
	}

	// And the worktree should still exist
	worktreePath := filepath.Join(env.WorktreeDir, "to-unstack")
	if !dirExists(worktreePath) {
		t.Error("Worktree directory should still exist after unstack")
	}
}

// TestUnstackBranchWithChildren tests unstack reparents children
func TestUnstackBranchWithChildren(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "parent-branch", "main")
	CreateBranch(t, env, "child-branch", "parent-branch")

	mgr, _ := stack.NewManager(env.RepoDir)
	if err := mgr.UntrackBranch("parent-branch"); err != nil {
		t.Fatalf("UntrackBranch failed: %v", err)
	}

	// Parent should no longer be tracked
	AssertBranchNotExists(t, env, "parent-branch")

	// Child should be reparented to main
	AssertBranchExists(t, env, "child-branch")
	AssertBranchParent(t, env, "child-branch", "main")
}

// TestUnstackMiddleBranch tests unstack in the middle of a stack
func TestUnstackMiddleBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "branch-a", "main")
	CreateBranch(t, env, "branch-b", "branch-a")
	CreateBranch(t, env, "branch-c", "branch-b")

	mgr, _ := stack.NewManager(env.RepoDir)
	if err := mgr.UntrackBranch("branch-b"); err != nil {
		t.Fatalf("UntrackBranch failed: %v", err)
	}

	// branch-b should no longer be tracked
	AssertBranchNotExists(t, env, "branch-b")

	// branch-c should be reparented to branch-a
	AssertBranchExists(t, env, "branch-c")
	AssertBranchParent(t, env, "branch-c", "branch-a")

	// branch-a should still exist
	AssertBranchExists(t, env, "branch-a")
}

// TestStackBranch tests adding an untracked branch to a stack
func TestStackBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create a branch manually (not through ezstack)
	g := git.New(env.RepoDir)
	worktreePath := filepath.Join(env.WorktreeDir, "manual-branch")
	if err := g.CreateWorktree("manual-branch", worktreePath, "main"); err != nil {
		t.Fatalf("CreateWorktree failed: %v", err)
	}

	// Verify it's not tracked
	mgr, _ := stack.NewManager(env.RepoDir)
	if mgr.GetBranch("manual-branch") != nil {
		t.Fatal("Branch should not be tracked initially")
	}

	// Add it to a stack using ReparentBranch (which is what Stack command uses)
	_, err := mgr.ReparentBranch("manual-branch", "main", false)
	if err != nil {
		t.Fatalf("ReparentBranch failed: %v", err)
	}

	// Now it should be tracked
	AssertBranchExists(t, env, "manual-branch")
	AssertBranchParent(t, env, "manual-branch", "main")
}

// TestStackBranchToExistingStack tests adding a branch to an existing stack
func TestStackBranchToExistingStack(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create an existing stack
	CreateBranch(t, env, "feature-a", "main")

	// Create a branch manually
	g := git.New(env.RepoDir)
	worktreePath := filepath.Join(env.WorktreeDir, "feature-b")
	if err := g.CreateWorktree("feature-b", worktreePath, "main"); err != nil {
		t.Fatalf("CreateWorktree failed: %v", err)
	}

	// Add it as a child of feature-a
	mgr, _ := stack.NewManager(env.RepoDir)
	_, err := mgr.ReparentBranch("feature-b", "feature-a", false)
	if err != nil {
		t.Fatalf("ReparentBranch failed: %v", err)
	}

	// Verify it's in the stack with correct parent
	AssertBranchExists(t, env, "feature-b")
	AssertBranchParent(t, env, "feature-b", "feature-a")
}

// dirExists checks if a directory exists
func dirExists(path string) bool {
	cmd := exec.Command("test", "-d", path)
	return cmd.Run() == nil
}

