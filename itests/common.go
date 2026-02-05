package itests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ezstack/ezstack/internal/git"
	"github.com/ezstack/ezstack/internal/stack"
)

// Constants for testing
const (
	TestUserEmail = "test@test.com"
	TestUserName  = "Test User"
	TestRepoOrg   = "testorg"
	TestRepoName  = "testrepo"
)

// GitCommit creates a commit in the specified directory
func GitCommit(t *testing.T, dir, filename, content, message string) {
	t.Helper()

	filePath := filepath.Join(dir, filename)
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write file %s: %v", filename, err)
	}

	if err := exec.Command("git", "-C", dir, "add", ".").Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}

	if err := exec.Command("git", "-C", dir, "commit", "-m", message).Run(); err != nil {
		t.Fatalf("Failed to git commit: %v", err)
	}
}

// GitCommitMultiple creates multiple commits in the specified directory
func GitCommitMultiple(t *testing.T, dir string, count int, prefix string) {
	t.Helper()

	for i := 0; i < count; i++ {
		filename := prefix + "_" + string(rune('a'+i)) + ".txt"
		content := prefix + " content " + string(rune('0'+i))
		message := prefix + " commit " + string(rune('1'+i))
		GitCommit(t, dir, filename, content, message)
	}
}

// CreateBranch creates a branch using the stack manager
func CreateBranch(t *testing.T, env *TestEnv, name, parent string) {
	t.Helper()

	mgr, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	_, err = mgr.CreateBranch(name, parent, "")
	if err != nil {
		t.Fatalf("CreateBranch(%s) failed: %v", name, err)
	}
}

// CreateBranchWithCommit creates a branch and adds a commit to it
func CreateBranchWithCommit(t *testing.T, env *TestEnv, name, parent string) {
	t.Helper()

	CreateBranch(t, env, name, parent)

	worktreePath := filepath.Join(env.WorktreeDir, name)
	GitCommit(t, worktreePath, name+".txt", name+" content", "Add "+name)
}

// NewGit creates a new git instance for the repo
func NewGit(env *TestEnv) *git.Git {
	return git.New(env.RepoDir)
}

// NewGitInWorktree creates a new git instance for a worktree
func NewGitInWorktree(env *TestEnv, branchName string) *git.Git {
	return git.New(filepath.Join(env.WorktreeDir, branchName))
}

// RunGhCommand runs a gh command using the stub
func RunGhCommand(t *testing.T, env *TestEnv, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command("gh", args...)
	cmd.Env = append(os.Environ(),
		"STUB_GH_STATE_DIR="+filepath.Join(env.TmpDir, "gh_state"),
		"STUB_GH_REPO="+TestRepoOrg+"/"+TestRepoName,
	)

	output, err := cmd.Output()
	return strings.TrimSpace(string(output)), err
}

// AssertBranchExists asserts that a branch exists in the stack
func AssertBranchExists(t *testing.T, env *TestEnv, name string) {
	t.Helper()

	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch(name)
	if branch == nil {
		t.Errorf("Expected branch %s to exist", name)
	}
}

// AssertBranchNotExists asserts that a branch does not exist in the stack
func AssertBranchNotExists(t *testing.T, env *TestEnv, name string) {
	t.Helper()

	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch(name)
	if branch != nil {
		t.Errorf("Expected branch %s to not exist", name)
	}
}

// AssertBranchParent asserts that a branch has the expected parent
func AssertBranchParent(t *testing.T, env *TestEnv, name, expectedParent string) {
	t.Helper()

	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch(name)
	if branch == nil {
		t.Fatalf("Branch %s not found", name)
	}
	if branch.Parent != expectedParent {
		t.Errorf("Branch %s parent = %q, want %q", name, branch.Parent, expectedParent)
	}
}

// AssertCommitsAhead asserts the number of commits ahead
func AssertCommitsAhead(t *testing.T, env *TestEnv, branch, base string, expected int) {
	t.Helper()

	g := NewGit(env)
	ahead, err := g.GetCommitsAhead(branch, base)
	if err != nil {
		t.Fatalf("GetCommitsAhead failed: %v", err)
	}
	if ahead != expected {
		t.Errorf("Commits ahead = %d, want %d", ahead, expected)
	}
}
