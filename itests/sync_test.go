package itests

import (
	"path/filepath"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/internal/git"
)

// TestCommitsAhead tests counting commits ahead of base
func TestCommitsAhead(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "ahead-branch", "main")

	g := NewGit(env)
	ahead, err := g.GetCommitsAhead("ahead-branch", "main")
	if err != nil {
		t.Fatalf("GetCommitsAhead failed: %v", err)
	}

	if ahead != 1 {
		t.Errorf("Expected 1 commit ahead, got %d", ahead)
	}
}

// TestCommitsBehind tests counting commits behind base
func TestCommitsBehind(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "behind-branch", "main")
	GitCommitMultiple(t, env.RepoDir, 3, "main")

	g := NewGitInWorktree(env, "behind-branch")
	behind, err := g.GetCommitsBehind("behind-branch", "main")
	if err != nil {
		t.Fatalf("GetCommitsBehind failed: %v", err)
	}

	if behind != 3 {
		t.Errorf("Expected 3 commits behind, got %d", behind)
	}
}

// TestIsBranchMerged_SameCommit tests merge detection when at same commit
func TestIsBranchMerged_SameCommit(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "same-commit", "main")

	g := NewGit(env)
	merged, err := g.IsBranchMerged("same-commit", "main")
	if err != nil {
		t.Fatalf("IsBranchMerged failed: %v", err)
	}

	if !merged {
		t.Error("Branch at same commit should be considered merged")
	}
}

// TestIsBranchMerged_WithCommits tests merge detection with unmerged commits
func TestIsBranchMerged_WithCommits(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "unmerged-feature", "main")

	g := NewGit(env)
	merged, err := g.IsBranchMerged("unmerged-feature", "main")
	if err != nil {
		t.Fatalf("IsBranchMerged failed: %v", err)
	}

	if merged {
		t.Error("Branch with unmerged commits should not be merged")
	}
}

// TestBranchExists tests checking if a branch exists
func TestBranchExists(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	g := NewGit(env)

	if !g.BranchExists("main") {
		t.Error("main branch should exist")
	}

	if g.BranchExists("nonexistent-branch") {
		t.Error("nonexistent-branch should not exist")
	}

	CreateBranch(t, env, "new-branch", "main")
	if !g.BranchExists("new-branch") {
		t.Error("new-branch should exist after creation")
	}
}

// TestGetBranchCommit tests getting the commit hash of a branch
func TestGetBranchCommit(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	g := NewGit(env)
	commit, err := g.GetBranchCommit("main")
	if err != nil {
		t.Fatalf("GetBranchCommit failed: %v", err)
	}

	if len(commit) != 40 {
		t.Errorf("Invalid commit hash length: %d", len(commit))
	}
}

// TestGetLastCommitMessage tests getting the last commit message
func TestGetLastCommitMessage(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "msg-test", "main")

	worktreePath := filepath.Join(env.WorktreeDir, "msg-test")
	GitCommit(t, worktreePath, "test.txt", "content", "Test commit message")

	g := git.New(worktreePath)
	msg, err := g.GetLastCommitMessage()
	if err != nil {
		t.Fatalf("GetLastCommitMessage failed: %v", err)
	}

	if msg != "Test commit message" {
		t.Errorf("Message = %q, want %q", msg, "Test commit message")
	}
}
