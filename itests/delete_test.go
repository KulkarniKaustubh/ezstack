package itests

import (
	"testing"

	"github.com/KulkarniKaustubh/ezstack/internal/stack"
)

// TestDeleteBranch tests deleting a branch
func TestDeleteBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "to-delete", "main")
	AssertBranchExists(t, env, "to-delete")

	mgr, _ := stack.NewManager(env.RepoDir)
	if err := mgr.DeleteBranch("to-delete", false); err != nil {
		t.Fatalf("DeleteBranch failed: %v", err)
	}

	AssertBranchNotExists(t, env, "to-delete")
}

// TestDeleteNonexistentBranch tests deleting a branch that doesn't exist
func TestDeleteNonexistentBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	mgr, _ := stack.NewManager(env.RepoDir)
	err := mgr.DeleteBranch("nonexistent", false)
	if err == nil {
		t.Error("Expected error when deleting nonexistent branch")
	}
}

// TestDeleteBranchWithChildren_NoForce tests deleting a branch with children without force
func TestDeleteBranchWithChildren_NoForce(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "parent-branch", "main")
	CreateBranch(t, env, "child-branch", "parent-branch")

	mgr, _ := stack.NewManager(env.RepoDir)
	if err := mgr.DeleteBranch("parent-branch", false); err == nil {
		t.Error("Expected error when deleting branch with children without force")
	}

	AssertBranchExists(t, env, "parent-branch")
	AssertBranchExists(t, env, "child-branch")
}

// TestDeleteBranchWithChildren_Force tests deleting a branch with children with force
func TestDeleteBranchWithChildren_Force(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "parent-branch", "main")
	CreateBranch(t, env, "child-branch", "parent-branch")

	mgr, _ := stack.NewManager(env.RepoDir)
	if err := mgr.DeleteBranch("parent-branch", true); err != nil {
		t.Fatalf("DeleteBranch with force failed: %v", err)
	}

	AssertBranchNotExists(t, env, "parent-branch")
	AssertBranchExists(t, env, "child-branch")
	AssertBranchParent(t, env, "child-branch", "main")
}

// TestDeleteMiddleBranchInStack tests deleting a branch in the middle of a stack
func TestDeleteMiddleBranchInStack(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "branch-a", "main")
	CreateBranch(t, env, "branch-b", "branch-a")
	CreateBranch(t, env, "branch-c", "branch-b")

	mgr, _ := stack.NewManager(env.RepoDir)
	if err := mgr.DeleteBranch("branch-b", true); err != nil {
		t.Fatalf("DeleteBranch failed: %v", err)
	}

	AssertBranchNotExists(t, env, "branch-b")
	AssertBranchExists(t, env, "branch-c")
	AssertBranchParent(t, env, "branch-c", "branch-a")
}

// TestDeleteLeafBranch tests deleting a leaf branch (no children)
func TestDeleteLeafBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "branch-a", "main")
	CreateBranch(t, env, "branch-b", "branch-a")

	mgr, _ := stack.NewManager(env.RepoDir)
	if err := mgr.DeleteBranch("branch-b", false); err != nil {
		t.Fatalf("DeleteBranch failed: %v", err)
	}

	AssertBranchNotExists(t, env, "branch-b")
	AssertBranchExists(t, env, "branch-a")
}
