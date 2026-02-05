package itests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ezstack/ezstack/internal/stack"
)

// TestNewBranch tests creating a new branch with ezs new
func TestNewBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "feature-a", "main")

	AssertBranchExists(t, env, "feature-a")
	AssertBranchParent(t, env, "feature-a", "main")

	worktreePath := filepath.Join(env.WorktreeDir, "feature-a")
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		t.Error("Worktree directory was not created")
	}
}

// TestNewBranchWithCommit tests creating a branch and adding commits
func TestNewBranchWithCommit(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feature-with-commit", "main")

	AssertCommitsAhead(t, env, "feature-with-commit", "main", 1)
}

// TestNewBranchChain tests creating a chain of stacked branches
func TestNewBranchChain(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create chain: main -> a -> b -> c
	branches := []string{"branch-a", "branch-b", "branch-c"}
	parents := []string{"main", "branch-a", "branch-b"}

	for i, name := range branches {
		CreateBranchWithCommit(t, env, name, parents[i])
	}

	for i, name := range branches {
		AssertBranchExists(t, env, name)
		AssertBranchParent(t, env, name, parents[i])
	}

	mgr, _ := stack.NewManager(env.RepoDir)

	children := mgr.GetChildren("branch-a")
	if len(children) != 1 || children[0].Name != "branch-b" {
		t.Errorf("branch-a should have branch-b as child")
	}

	children = mgr.GetChildren("branch-b")
	if len(children) != 1 || children[0].Name != "branch-c" {
		t.Errorf("branch-b should have branch-c as child")
	}

	children = mgr.GetChildren("branch-c")
	if len(children) != 0 {
		t.Errorf("branch-c should have no children")
	}
}

// TestNewBranchNoCommits tests that a new branch starts with 0 commits ahead
func TestNewBranchNoCommits(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "empty-branch", "main")

	AssertCommitsAhead(t, env, "empty-branch", "main", 0)
}

// TestNewBranchMultipleCommits tests adding multiple commits to a branch
func TestNewBranchMultipleCommits(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "multi-commit", "main")

	worktreePath := filepath.Join(env.WorktreeDir, "multi-commit")
	GitCommitMultiple(t, worktreePath, 3, "feature")

	AssertCommitsAhead(t, env, "multi-commit", "main", 3)
}

// TestNewBranchWorktreePath tests that worktree path is set correctly
func TestNewBranchWorktreePath(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "worktree-test", "main")

	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("worktree-test")

	expectedPath := filepath.Join(env.WorktreeDir, "worktree-test")
	if branch.WorktreePath != expectedPath {
		t.Errorf("WorktreePath = %q, want %q", branch.WorktreePath, expectedPath)
	}
}
