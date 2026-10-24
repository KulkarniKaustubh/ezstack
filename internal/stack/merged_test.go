package stack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

// setupSyncTestEnv creates a temporary git repository for sync testing
// It creates a main repo and a worktree base directory
func setupSyncTestEnv(t *testing.T) (repoDir, worktreeBaseDir string, cleanup func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "sync-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Resolve symlinks (macOS /tmp -> /private/tmp)
	tmpDir, err = filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatalf("Failed to resolve symlinks: %v", err)
	}

	repoDir = filepath.Join(tmpDir, "repo")
	worktreeBaseDir = filepath.Join(tmpDir, "worktrees")
	configDir := filepath.Join(tmpDir, "config")

	os.MkdirAll(repoDir, 0755)
	os.MkdirAll(worktreeBaseDir, 0755)
	os.MkdirAll(configDir, 0755)

	t.Setenv("EZSTACK_HOME", configDir)

	exec.Command("git", "-C", repoDir, "init", "-b", "main").Run()
	exec.Command("git", "-C", repoDir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", repoDir, "config", "user.name", "Test User").Run()

	// Create initial commit
	readmePath := filepath.Join(repoDir, "README.md")
	os.WriteFile(readmePath, []byte("# Test\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Initial commit").Run()

	// Resolve symlinks again after git init
	repoDir, _ = filepath.EvalSymlinks(repoDir)

	cfg := &config.Config{
		DefaultBaseBranch: "main",
		Repos: map[string]*config.RepoConfig{
			repoDir: {
				WorktreeBaseDir: worktreeBaseDir,
			},
		},
	}
	cfg.Save()

	cleanup = func() {
		os.RemoveAll(tmpDir)
	}

	return repoDir, worktreeBaseDir, cleanup
}

func TestMarkBranchRemote_WithRemote(t *testing.T) {
	repoDir, _, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, err := NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// Create a branch and register it
	exec.Command("git", "-C", repoDir, "checkout", "-b", "fork-branch").Run()
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()

	mgr.RegisterExistingBranch("fork-branch", "", "main")

	// Mark as remote with a specific remote
	err = mgr.MarkBranchRemote("fork-branch", "https://github.com/fork/repo/pull/1", "fork-user")
	if err != nil {
		t.Fatalf("MarkBranchRemote() error = %v", err)
	}

	// Verify the cache has the remote set
	bc := mgr.stackConfig.Cache.GetBranchCache("fork-branch")
	if bc == nil {
		t.Fatal("BranchCache should exist after MarkBranchRemote")
	}
	if !bc.IsRemote {
		t.Error("IsRemote should be true")
	}
	if bc.Remote != "fork-user" {
		t.Errorf("Remote = %q, want %q", bc.Remote, "fork-user")
	}

	// Verify the branch object also has it
	branch := mgr.GetBranch("fork-branch")
	if branch == nil {
		t.Fatal("GetBranch should find fork-branch")
	}
	if branch.Remote != "fork-user" {
		t.Errorf("Branch.Remote = %q, want %q", branch.Remote, "fork-user")
	}
	if branch.EffectiveRemote() != "fork-user" {
		t.Errorf("EffectiveRemote() = %q, want %q", branch.EffectiveRemote(), "fork-user")
	}
}

func TestMarkBranchRemote_NoPush(t *testing.T) {
	repoDir, _, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, err := NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	exec.Command("git", "-C", repoDir, "checkout", "-b", "nopush-branch").Run()
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()

	mgr.RegisterExistingBranch("nopush-branch", "", "main")

	err = mgr.MarkBranchRemote("nopush-branch", "", config.RemoteNoPush)
	if err != nil {
		t.Fatalf("MarkBranchRemote() error = %v", err)
	}

	branch := mgr.GetBranch("nopush-branch")
	if branch == nil {
		t.Fatal("GetBranch should find nopush-branch")
	}
	if branch.CanPush() {
		t.Error("CanPush() should be false for _nopush branch")
	}
	if branch.EffectiveRemote() != config.RemoteNoPush {
		t.Errorf("EffectiveRemote() = %q, want %q", branch.EffectiveRemote(), config.RemoteNoPush)
	}
}

func TestMarkBranchRemote_EmptyRemote(t *testing.T) {
	repoDir, _, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, err := NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	exec.Command("git", "-C", repoDir, "checkout", "-b", "same-repo-branch").Run()
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()

	mgr.RegisterExistingBranch("same-repo-branch", "", "main")

	// Empty remote means same-repo remote branch — defaults to "origin"
	err = mgr.MarkBranchRemote("same-repo-branch", "https://github.com/org/repo/pull/5", "")
	if err != nil {
		t.Fatalf("MarkBranchRemote() error = %v", err)
	}

	branch := mgr.GetBranch("same-repo-branch")
	if branch == nil {
		t.Fatal("GetBranch should find same-repo-branch")
	}
	if branch.Remote != "" {
		t.Errorf("Remote = %q, want empty for same-repo branch", branch.Remote)
	}
	if branch.EffectiveRemote() != "origin" {
		t.Errorf("EffectiveRemote() = %q, want %q", branch.EffectiveRemote(), "origin")
	}
	if !branch.CanPush() {
		t.Error("CanPush() should be true for same-repo branch")
	}
}

// TestCleanupMergedBranches removes the merged branch's worktree and git
// branch but keeps it in the stack model flagged merged (so it still renders
// struck-through and gh-stack keeps it as a merged layer).
func TestCleanupMergedBranches(t *testing.T) {
	repoDir, _, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, err := NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	a, err := mgr.CreateBranch("feat-a", "main", "", "")
	if err != nil {
		t.Fatalf("CreateBranch(a): %v", err)
	}
	if _, err := mgr.CreateBranch("feat-b", "feat-a", "", ""); err != nil {
		t.Fatalf("CreateBranch(b): %v", err)
	}

	results := mgr.CleanupMergedBranches([]MergedBranchInfo{{
		Branch: "feat-a", PRNumber: 7, WorktreePath: a.WorktreePath,
		StackHash: mgr.GetStackForBranch("feat-a").Hash,
	}}, repoDir)
	if len(results) != 1 || !results[0].Success {
		t.Fatalf("CleanupMergedBranches = %+v", results)
	}
	if _, err := os.Stat(a.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("worktree %s still exists", a.WorktreePath)
	}
	if out, _ := exec.Command("git", "-C", repoDir, "branch", "--list", "feat-a").Output(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("git branch feat-a still exists: %q", out)
	}
	b := mgr.GetBranch("feat-a")
	if b == nil || !b.IsMerged {
		t.Fatalf("feat-a should stay tracked and flagged merged, got %+v", b)
	}
	if child := mgr.GetBranch("feat-b"); child == nil || child.Parent != "main" {
		t.Errorf("feat-b's effective parent should skip the merged branch, got %+v", child)
	}
}

// TestDetectFullyMergedStacks reports a stack only once every branch is
// merged, and says whether local worktrees/branches remain.
func TestDetectFullyMergedStacks(t *testing.T) {
	repoDir, _, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, err := NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if _, err := mgr.CreateBranch("one", "main", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CreateBranch("two", "one", "", ""); err != nil {
		t.Fatal(err)
	}
	s := mgr.GetStackForBranch("one")

	if got := mgr.DetectFullyMergedStacks([]*config.Stack{s}); len(got) != 0 {
		t.Fatalf("unmerged stack reported fully merged: %+v", got)
	}
	if err := mgr.MarkBranchMerged("one"); err != nil {
		t.Fatal(err)
	}
	if got := mgr.DetectFullyMergedStacks([]*config.Stack{mgr.GetStackForBranch("one")}); len(got) != 0 {
		t.Fatalf("partially merged stack reported fully merged: %+v", got)
	}
	if err := mgr.MarkBranchMerged("two"); err != nil {
		t.Fatal(err)
	}
	got := mgr.DetectFullyMergedStacks([]*config.Stack{mgr.GetStackForBranch("one")})
	if len(got) != 1 || got[0].StackHash != s.Hash {
		t.Fatalf("DetectFullyMergedStacks = %+v, want stack %s", got, s.Hash)
	}
	if got[0].HasLocalArtifacts {
		t.Error("MarkBranchMerged removed worktrees and branches; HasLocalArtifacts should be false")
	}
}
