package stack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
)

// setupTestEnv creates a temporary git repository and config directory for testing
func setupTestEnv(t *testing.T) (repoDir, worktreeBaseDir string, cleanup func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "stack-test-*")
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

	originalHome := os.Getenv("EZSTACK_HOME")
	os.Setenv("EZSTACK_HOME", configDir)

	exec.Command("git", "-C", repoDir, "init").Run()
	exec.Command("git", "-C", repoDir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", repoDir, "config", "user.name", "Test User").Run()

	readmePath := filepath.Join(repoDir, "README.md")
	os.WriteFile(readmePath, []byte("# Test\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Initial commit").Run()

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
		os.Setenv("EZSTACK_HOME", originalHome)
		os.RemoveAll(tmpDir)
	}

	return repoDir, worktreeBaseDir, cleanup
}

func TestNewManager(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, err := NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	if mgr.GetRepoDir() == "" {
		t.Error("GetRepoDir() returned empty string")
	}
}

func TestManager_CreateBranch(t *testing.T) {
	repoDir, worktreeDir, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)

	branch, err := mgr.CreateBranch("feature-a", "main", "")
	if err != nil {
		t.Fatalf("CreateBranch() error = %v", err)
	}

	if branch.Name != "feature-a" {
		t.Errorf("branch.Name = %q, want %q", branch.Name, "feature-a")
	}

	if branch.Parent != "main" {
		t.Errorf("branch.Parent = %q, want %q", branch.Parent, "main")
	}

	expectedPath := filepath.Join(worktreeDir, "feature-a")
	if branch.WorktreePath != expectedPath {
		t.Errorf("branch.WorktreePath = %q, want %q", branch.WorktreePath, expectedPath)
	}

	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Error("Worktree directory was not created")
	}
}

func TestManager_GetBranch(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	branch := mgr.GetBranch("feature-a")
	if branch == nil {
		t.Fatal("GetBranch() returned nil for existing branch")
	}

	if branch.Name != "feature-a" {
		t.Errorf("branch.Name = %q, want %q", branch.Name, "feature-a")
	}

	branch = mgr.GetBranch("nonexistent")
	if branch != nil {
		t.Error("GetBranch() should return nil for non-existing branch")
	}
}

func TestManager_ListStacks(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)

	stacks := mgr.ListStacks()
	if len(stacks) != 0 {
		t.Errorf("len(ListStacks()) = %d, want 0", len(stacks))
	}

	mgr.CreateBranch("feature-a", "main", "")

	stacks = mgr.ListStacks()
	if len(stacks) != 1 {
		t.Errorf("len(ListStacks()) = %d, want 1", len(stacks))
	}
}

func TestManager_IsMainBranch(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)

	if !mgr.IsMainBranch("main") {
		t.Error("IsMainBranch('main') should return true")
	}

	if !mgr.IsMainBranch("master") {
		t.Error("IsMainBranch('master') should return true")
	}

	if mgr.IsMainBranch("feature") {
		t.Error("IsMainBranch('feature') should return false")
	}
}

func TestManager_GetChildren(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("feature-b", "feature-a", "")

	mgr, _ = NewManager(repoDir)
	children := mgr.GetChildren("feature-a")
	if len(children) != 1 {
		t.Fatalf("GetChildren() returned %d children, want 1", len(children))
	}

	if children[0].Name != "feature-b" {
		t.Errorf("child.Name = %q, want %q", children[0].Name, "feature-b")
	}

	children = mgr.GetChildren("main")
	if len(children) != 1 {
		t.Errorf("GetChildren('main') returned %d children, want 1", len(children))
	}
}

func TestManager_DeleteBranch(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	mgr, _ = NewManager(repoDir)

	if err := mgr.DeleteBranch("feature-a", false); err != nil {
		t.Fatalf("DeleteBranch() error = %v", err)
	}

	mgr, _ = NewManager(repoDir)
	branch := mgr.GetBranch("feature-a")
	if branch != nil {
		t.Error("Branch should have been deleted")
	}
}

func TestManager_DeleteBranch_WithChildren_NoForce(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("feature-b", "feature-a", "")

	mgr, _ = NewManager(repoDir)

	if err := mgr.DeleteBranch("feature-a", false); err == nil {
		t.Error("DeleteBranch() should fail when branch has children without force")
	}
}

func TestManager_DeleteBranch_WithChildren_Force(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("feature-b", "feature-a", "")

	mgr, _ = NewManager(repoDir)

	if err := mgr.DeleteBranch("feature-a", true); err != nil {
		t.Fatalf("DeleteBranch() with force error = %v", err)
	}

	mgr, _ = NewManager(repoDir)
	if mgr.GetBranch("feature-a") != nil {
		t.Error("feature-a should have been deleted")
	}

	child := mgr.GetBranch("feature-b")
	if child == nil {
		t.Fatal("feature-b should still exist")
	}

	if child.Parent != "main" {
		t.Errorf("child.Parent = %q, want 'main'", child.Parent)
	}
}

func TestManager_RegisterExistingBranch(t *testing.T) {
	repoDir, worktreeDir, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)

	branch, err := mgr.RegisterExistingBranch("existing-branch", worktreeDir+"/existing", "main")
	if err != nil {
		t.Fatalf("RegisterExistingBranch() error = %v", err)
	}

	if branch.Name != "existing-branch" {
		t.Errorf("branch.Name = %q, want %q", branch.Name, "existing-branch")
	}

	_, err = mgr.RegisterExistingBranch("existing-branch", worktreeDir+"/other", "main")
	if err == nil {
		t.Error("RegisterExistingBranch() should fail for already registered branch")
	}
}

func TestManager_RegisterRemoteBranch(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)

	branch, err := mgr.RegisterRemoteBranch("remote-feature", "main", 42, "https://github.com/org/repo/pull/42")
	if err != nil {
		t.Fatalf("RegisterRemoteBranch() error = %v", err)
	}

	if !branch.IsRemote {
		t.Error("IsRemote should be true")
	}

	if branch.PRNumber != 42 {
		t.Errorf("PRNumber = %d, want 42", branch.PRNumber)
	}

	if branch.WorktreePath != "" {
		t.Error("WorktreePath should be empty for remote branches")
	}
}

func TestManager_MarkBranchMerged(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	mgr, _ = NewManager(repoDir)

	err := mgr.MarkBranchMerged("feature-a")
	if err != nil {
		t.Fatalf("MarkBranchMerged() error = %v", err)
	}

	mgr, _ = NewManager(repoDir)
	branch := mgr.GetBranch("feature-a")
	if branch == nil {
		t.Fatal("Branch should still exist in config")
	}

	if !branch.IsMerged {
		t.Error("IsMerged should be true")
	}

	if branch.WorktreePath != "" {
		t.Error("WorktreePath should be cleared")
	}
}

// TestManager_ReparentBranch_SameStack tests reparenting within the same stack
func TestManager_ReparentBranch_SameStack(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	// Create a stack: main -> feature-a -> feature-b -> feature-c
	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("feature-b", "feature-a", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("feature-c", "feature-b", "")

	// Reparent feature-c to feature-a (skipping feature-b)
	mgr, _ = NewManager(repoDir)
	branch, err := mgr.ReparentBranch("feature-c", "feature-a", false)
	if err != nil {
		t.Fatalf("ReparentBranch() error = %v", err)
	}

	if branch.Parent != "feature-a" {
		t.Errorf("branch.Parent = %q, want %q", branch.Parent, "feature-a")
	}

	// Verify the parent was updated
	mgr, _ = NewManager(repoDir)
	branch = mgr.GetBranch("feature-c")
	if branch.Parent != "feature-a" {
		t.Errorf("After reload, branch.Parent = %q, want %q", branch.Parent, "feature-a")
	}
}

// TestManager_ReparentBranch_ToMain tests reparenting a branch to main
func TestManager_ReparentBranch_ToMain(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	// Create a stack: main -> feature-a -> feature-b
	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("feature-b", "feature-a", "")

	// Reparent feature-b to main
	mgr, _ = NewManager(repoDir)
	branch, err := mgr.ReparentBranch("feature-b", "main", false)
	if err != nil {
		t.Fatalf("ReparentBranch() error = %v", err)
	}

	if branch.Parent != "main" {
		t.Errorf("branch.Parent = %q, want %q", branch.Parent, "main")
	}
}

// TestManager_ReparentBranch_CycleDetection tests that cycles are prevented
func TestManager_ReparentBranch_CycleDetection(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	// Create a stack: main -> feature-a -> feature-b -> feature-c
	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("feature-b", "feature-a", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("feature-c", "feature-b", "")

	// Try to reparent feature-a to feature-c (would create cycle)
	mgr, _ = NewManager(repoDir)
	_, err := mgr.ReparentBranch("feature-a", "feature-c", false)
	if err == nil {
		t.Error("ReparentBranch() should fail when creating a cycle")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("Error should mention circular dependency, got: %v", err)
	}
}

// TestManager_ReparentBranch_NonExistentParent tests error handling
func TestManager_ReparentBranch_NonExistentParent(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	mgr, _ = NewManager(repoDir)
	_, err := mgr.ReparentBranch("feature-a", "nonexistent", false)
	if err == nil {
		t.Error("ReparentBranch() should fail for non-existent parent")
	}
}

// TestManager_GetAllBranchesInAllStacks tests getting all branches
func TestManager_GetAllBranchesInAllStacks(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("feature-b", "feature-a", "")

	mgr, _ = NewManager(repoDir)
	branches := mgr.GetAllBranchesInAllStacks()
	if len(branches) != 2 {
		t.Errorf("GetAllBranchesInAllStacks() returned %d branches, want 2", len(branches))
	}
}

// TestManager_WouldCreateCycle tests the cycle detection helper
func TestManager_WouldCreateCycle(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	// Create a stack: main -> a -> b -> c
	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("a", "main", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("b", "a", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("c", "b", "")

	mgr, _ = NewManager(repoDir)

	// c -> a would not create cycle (a is not descendant of c)
	if mgr.wouldCreateCycle("c", "a") {
		t.Error("wouldCreateCycle('c', 'a') should be false")
	}

	// a -> c would create cycle (c is descendant of a)
	if !mgr.wouldCreateCycle("a", "c") {
		t.Error("wouldCreateCycle('a', 'c') should be true")
	}

	// b -> c would create cycle (c is child of b)
	if !mgr.wouldCreateCycle("b", "c") {
		t.Error("wouldCreateCycle('b', 'c') should be true")
	}

	// c -> main should not create cycle
	if mgr.wouldCreateCycle("c", "main") {
		t.Error("wouldCreateCycle('c', 'main') should be false")
	}
}

func TestManager_DetectOrphanedBranches(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, err := NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Create a branch with worktree
	_, err = mgr.CreateBranch("feature-a", "main", filepath.Join(worktreeBaseDir, "feature-a"))
	if err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}

	// Initially no orphaned branches
	orphaned := mgr.DetectOrphanedBranches()
	if len(orphaned) != 0 {
		t.Errorf("DetectOrphanedBranches() returned %d, want 0", len(orphaned))
	}

	// Delete the worktree and git branch (simulating manual deletion)
	exec.Command("git", "-C", repoDir, "worktree", "remove", "--force", filepath.Join(worktreeBaseDir, "feature-a")).Run()
	exec.Command("git", "-C", repoDir, "branch", "-D", "feature-a").Run()

	// Reload manager
	mgr, _ = NewManager(repoDir)
	orphaned = mgr.DetectOrphanedBranches()
	if len(orphaned) != 1 {
		t.Errorf("DetectOrphanedBranches() returned %d, want 1", len(orphaned))
	}
	if len(orphaned) > 0 && orphaned[0] != "feature-a" {
		t.Errorf("DetectOrphanedBranches() returned %q, want 'feature-a'", orphaned[0])
	}
}

func TestManager_RemoveOrphanedBranches(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, err := NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Create branches
	_, err = mgr.CreateBranch("feature-a", "main", filepath.Join(worktreeBaseDir, "feature-a"))
	if err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}
	_, err = mgr.CreateBranch("feature-b", "feature-a", filepath.Join(worktreeBaseDir, "feature-b"))
	if err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}

	// Delete feature-a from git
	exec.Command("git", "-C", repoDir, "worktree", "remove", filepath.Join(worktreeBaseDir, "feature-a")).Run()
	exec.Command("git", "-C", repoDir, "branch", "-D", "feature-a").Run()

	// Remove orphaned branches
	mgr, _ = NewManager(repoDir)
	err = mgr.RemoveOrphanedBranches([]string{"feature-a"})
	if err != nil {
		t.Fatalf("RemoveOrphanedBranches failed: %v", err)
	}

	// Verify feature-a is gone and feature-b's parent is now main
	mgr, _ = NewManager(repoDir)
	if mgr.GetBranch("feature-a") != nil {
		t.Error("feature-a should be removed")
	}
	branchB := mgr.GetBranch("feature-b")
	if branchB == nil {
		t.Fatal("feature-b should still exist")
	}
	if branchB.Parent != "main" {
		t.Errorf("feature-b parent = %q, want 'main'", branchB.Parent)
	}
}

func TestManager_AddWorktreeToStack(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, err := NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Create a git branch and worktree manually (simulating user doing it outside ezs)
	wtPath := filepath.Join(worktreeBaseDir, "manual-branch")
	exec.Command("git", "-C", repoDir, "branch", "manual-branch").Run()
	exec.Command("git", "-C", repoDir, "worktree", "add", wtPath, "manual-branch").Run()

	// Add it to stack
	branch, err := mgr.AddWorktreeToStack("manual-branch", wtPath, "main")
	if err != nil {
		t.Fatalf("AddWorktreeToStack failed: %v", err)
	}

	if branch.Name != "manual-branch" {
		t.Errorf("branch.Name = %q, want 'manual-branch'", branch.Name)
	}
	if branch.Parent != "main" {
		t.Errorf("branch.Parent = %q, want 'main'", branch.Parent)
	}

	// Verify it's in a stack
	mgr, _ = NewManager(repoDir)
	found := mgr.GetBranch("manual-branch")
	if found == nil {
		t.Error("manual-branch should be in a stack")
	}
}

func TestManager_UntrackBranch(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	// Verify branch exists
	branch := mgr.GetBranch("feature-a")
	if branch == nil {
		t.Fatal("Branch should exist before untracking")
	}

	// Untrack the branch
	err := mgr.UntrackBranch("feature-a")
	if err != nil {
		t.Fatalf("UntrackBranch() error = %v", err)
	}

	// Verify branch is no longer tracked
	branch = mgr.GetBranch("feature-a")
	if branch != nil {
		t.Error("Branch should not be tracked after untracking")
	}

	// Verify the stack is removed (was only branch)
	stacks := mgr.ListStacks()
	if len(stacks) != 0 {
		t.Errorf("Expected 0 stacks after untracking only branch, got %d", len(stacks))
	}
}

func TestManager_UntrackBranch_NotTracked(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)

	// Try to untrack a branch that doesn't exist
	err := mgr.UntrackBranch("nonexistent")
	if err == nil {
		t.Error("UntrackBranch() should fail for non-tracked branch")
	}
	if !strings.Contains(err.Error(), "not tracked") {
		t.Errorf("Error should mention 'not tracked', got: %v", err)
	}
}

func TestManager_UntrackBranch_WithChildren(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")
	mgr.CreateBranch("feature-b", "feature-a", "")

	// Verify parent-child relationship
	children := mgr.GetChildren("feature-a")
	if len(children) != 1 {
		t.Fatalf("Expected 1 child, got %d", len(children))
	}

	// Untrack the parent
	err := mgr.UntrackBranch("feature-a")
	if err != nil {
		t.Fatalf("UntrackBranch() error = %v", err)
	}

	// Verify feature-a is no longer tracked
	branch := mgr.GetBranch("feature-a")
	if branch != nil {
		t.Error("feature-a should not be tracked after untracking")
	}

	// Verify feature-b is reparented to main
	branchB := mgr.GetBranch("feature-b")
	if branchB == nil {
		t.Fatal("feature-b should still be tracked")
	}
	if branchB.Parent != "main" {
		t.Errorf("feature-b.Parent = %q, want %q", branchB.Parent, "main")
	}
}

func TestManager_UntrackBranch_MiddleOfStack(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")
	mgr.CreateBranch("feature-b", "feature-a", "")
	mgr.CreateBranch("feature-c", "feature-b", "")

	// Untrack the middle branch
	err := mgr.UntrackBranch("feature-b")
	if err != nil {
		t.Fatalf("UntrackBranch() error = %v", err)
	}

	// Verify feature-b is no longer tracked
	if mgr.GetBranch("feature-b") != nil {
		t.Error("feature-b should not be tracked")
	}

	// Verify feature-c is reparented to feature-a
	branchC := mgr.GetBranch("feature-c")
	if branchC == nil {
		t.Fatal("feature-c should still be tracked")
	}
	if branchC.Parent != "feature-a" {
		t.Errorf("feature-c.Parent = %q, want %q", branchC.Parent, "feature-a")
	}

	// Verify feature-a still exists
	if mgr.GetBranch("feature-a") == nil {
		t.Error("feature-a should still be tracked")
	}
}

// TestManager_CreateWorktreeOnly tests creating a worktree without adding to a stack
func TestManager_CreateWorktreeOnly(t *testing.T) {
	repoDir, worktreeDir, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)

	err := mgr.CreateWorktreeOnly("standalone-branch", "main", "")
	if err != nil {
		t.Fatalf("CreateWorktreeOnly() error = %v", err)
	}

	// Verify worktree was created
	expectedPath := filepath.Join(worktreeDir, "standalone-branch")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Error("Worktree directory was not created")
	}

	// Verify branch is NOT in any stack
	branch := mgr.GetBranch("standalone-branch")
	if branch != nil {
		t.Error("Branch should NOT be tracked in any stack")
	}

	// Verify no stacks were created
	stacks := mgr.ListStacks()
	if len(stacks) != 0 {
		t.Errorf("Expected 0 stacks, got %d", len(stacks))
	}
}

// TestManager_CreateWorktreeOnly_WithExplicitPath tests creating a worktree with explicit path
func TestManager_CreateWorktreeOnly_WithExplicitPath(t *testing.T) {
	repoDir, worktreeDir, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)

	customPath := filepath.Join(worktreeDir, "custom-path")
	err := mgr.CreateWorktreeOnly("standalone-custom", "main", customPath)
	if err != nil {
		t.Fatalf("CreateWorktreeOnly() error = %v", err)
	}

	// Verify worktree was created at custom path
	if _, err := os.Stat(customPath); os.IsNotExist(err) {
		t.Error("Worktree directory was not created at custom path")
	}

	// Verify branch is NOT in any stack
	branch := mgr.GetBranch("standalone-custom")
	if branch != nil {
		t.Error("Branch should NOT be tracked in any stack")
	}
}

// TestManager_CreateWorktreeOnly_NoWorktreeDir tests error when no worktree dir configured
func TestManager_CreateWorktreeOnly_NoWorktreeDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "stack-test-nodir-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	repoDir := filepath.Join(tmpDir, "repo")
	configDir := filepath.Join(tmpDir, "config")

	os.MkdirAll(repoDir, 0755)
	os.MkdirAll(configDir, 0755)

	originalHome := os.Getenv("EZSTACK_HOME")
	os.Setenv("EZSTACK_HOME", configDir)
	defer os.Setenv("EZSTACK_HOME", originalHome)

	exec.Command("git", "-C", repoDir, "init").Run()
	exec.Command("git", "-C", repoDir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", repoDir, "config", "user.name", "Test User").Run()
	os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Test\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Initial commit").Run()

	repoDir, _ = filepath.EvalSymlinks(repoDir)

	// Config without worktree base dir
	cfg := &config.Config{
		DefaultBaseBranch: "main",
		Repos:             map[string]*config.RepoConfig{},
	}
	cfg.Save()

	mgr, _ := NewManager(repoDir)

	// Should fail because no worktree dir is configured
	err = mgr.CreateWorktreeOnly("standalone-branch", "main", "")
	if err == nil {
		t.Error("CreateWorktreeOnly() should fail when no worktree dir is configured")
	}
	if !strings.Contains(err.Error(), "worktree directory not specified") {
		t.Errorf("Error should mention worktree directory, got: %v", err)
	}
}

// createGitBranch creates a git branch in the repo without checking it out.
func createGitBranch(t *testing.T, repoDir, branchName string) {
	t.Helper()
	if err := exec.Command("git", "-C", repoDir, "branch", branchName).Run(); err != nil {
		t.Fatalf("Failed to create git branch %s: %v", branchName, err)
	}
}

// TestManager_CreateBranch_NonMainRoot tests creating a stack rooted on a non-main branch
func TestManager_CreateBranch_NonMainRoot(t *testing.T) {
	repoDir, worktreeDir, cleanup := setupTestEnv(t)
	defer cleanup()

	createGitBranch(t, repoDir, "develop")

	mgr, _ := NewManager(repoDir)
	branch, err := mgr.CreateBranch("feature-a", "develop", "")
	if err != nil {
		t.Fatalf("CreateBranch() error = %v", err)
	}

	if branch.Name != "feature-a" {
		t.Errorf("branch.Name = %q, want %q", branch.Name, "feature-a")
	}
	if branch.Parent != "develop" {
		t.Errorf("branch.Parent = %q, want %q", branch.Parent, "develop")
	}

	// Verify the stack root is develop, not main
	mgr, _ = NewManager(repoDir)
	stacks := mgr.ListStacks()
	if len(stacks) != 1 {
		t.Fatalf("Expected 1 stack, got %d", len(stacks))
	}
	if stacks[0].Root != "develop" {
		t.Errorf("stack.Root = %q, want %q", stacks[0].Root, "develop")
	}

	// Verify we can add child branches to a non-main-rooted stack
	mgr, _ = NewManager(repoDir)
	child, err := mgr.CreateBranch("feature-b", "feature-a", "")
	if err != nil {
		t.Fatalf("CreateBranch() child error = %v", err)
	}
	if child.Parent != "feature-a" {
		t.Errorf("child.Parent = %q, want %q", child.Parent, "feature-a")
	}

	// Both branches should be in the same stack
	mgr, _ = NewManager(repoDir)
	stacks = mgr.ListStacks()
	if len(stacks) != 1 {
		t.Fatalf("Expected 1 stack, got %d", len(stacks))
	}
	if len(stacks[0].Branches) != 2 {
		t.Errorf("Expected 2 branches, got %d", len(stacks[0].Branches))
	}

	_ = worktreeDir // used implicitly via config
}

// TestManager_GetStackForBranch tests the GetStackForBranch helper
func TestManager_GetStackForBranch(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	mgr, _ = NewManager(repoDir)
	stack := mgr.GetStackForBranch("feature-a")
	if stack == nil {
		t.Fatal("GetStackForBranch() returned nil for tracked branch")
	}
	if stack.Root != "main" {
		t.Errorf("stack.Root = %q, want %q", stack.Root, "main")
	}

	stack = mgr.GetStackForBranch("nonexistent")
	if stack != nil {
		t.Error("GetStackForBranch() should return nil for untracked branch")
	}

	stack = mgr.GetStackForBranch("main")
	if stack != nil {
		t.Error("GetStackForBranch() should return nil for root branch (not in tree)")
	}
}

// TestManager_ReparentBranch_ToNonMainRoot tests reparenting to a non-main external branch
func TestManager_ReparentBranch_ToNonMainRoot(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	createGitBranch(t, repoDir, "develop")

	// Create a stack: main -> feature-a
	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	// Reparent feature-a from main to develop
	mgr, _ = NewManager(repoDir)
	branch, err := mgr.ReparentBranch("feature-a", "develop", false)
	if err != nil {
		t.Fatalf("ReparentBranch() error = %v", err)
	}
	if branch.Parent != "develop" {
		t.Errorf("branch.Parent = %q, want %q", branch.Parent, "develop")
	}

	// Verify the stack now has develop as root
	mgr, _ = NewManager(repoDir)
	stack := mgr.GetStackForBranch("feature-a")
	if stack == nil {
		t.Fatal("feature-a should be in a stack after reparent")
	}
	if stack.Root != "develop" {
		t.Errorf("stack.Root = %q, want %q", stack.Root, "develop")
	}
}

// TestManager_ReparentBranch_CrossStack_NonMainRoot tests cross-stack reparent with non-main roots
func TestManager_ReparentBranch_CrossStack_NonMainRoot(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	createGitBranch(t, repoDir, "develop")
	createGitBranch(t, repoDir, "staging")

	// Create stack 1: develop -> feature-a
	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "develop", "")

	// Create stack 2: staging -> feature-b
	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("feature-b", "staging", "")

	// Reparent feature-a to feature-b (cross-stack)
	mgr, _ = NewManager(repoDir)
	branch, err := mgr.ReparentBranch("feature-a", "feature-b", false)
	if err != nil {
		t.Fatalf("ReparentBranch() error = %v", err)
	}
	if branch.Parent != "feature-b" {
		t.Errorf("branch.Parent = %q, want %q", branch.Parent, "feature-b")
	}

	// Both branches should now be in the staging-rooted stack
	mgr, _ = NewManager(repoDir)
	stack := mgr.GetStackForBranch("feature-a")
	if stack == nil {
		t.Fatal("feature-a should be in a stack")
	}
	if stack.Root != "staging" {
		t.Errorf("stack.Root = %q, want %q", stack.Root, "staging")
	}

	stackB := mgr.GetStackForBranch("feature-b")
	if stackB == nil || stackB.Hash != stack.Hash {
		t.Error("feature-a and feature-b should be in the same stack")
	}
}

// TestManager_AddBranchWithParent_NonMainRoot tests adding a standalone branch to a non-main parent
func TestManager_AddBranchWithParent_NonMainRoot(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	createGitBranch(t, repoDir, "develop")
	createGitBranch(t, repoDir, "standalone")

	// Add standalone to develop — should create a new stack with root=develop
	mgr, _ := NewManager(repoDir)
	branch, err := mgr.ReparentBranch("standalone", "develop", false)
	if err != nil {
		t.Fatalf("ReparentBranch() error = %v", err)
	}
	if branch.Parent != "develop" {
		t.Errorf("branch.Parent = %q, want %q", branch.Parent, "develop")
	}

	mgr, _ = NewManager(repoDir)
	stack := mgr.GetStackForBranch("standalone")
	if stack == nil {
		t.Fatal("standalone should be in a stack")
	}
	if stack.Root != "develop" {
		t.Errorf("stack.Root = %q, want %q", stack.Root, "develop")
	}
}

// TestManager_CycleDetection_NonMainRoot tests cycle detection with non-main roots
func TestManager_CycleDetection_NonMainRoot(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	createGitBranch(t, repoDir, "develop")

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "develop", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("feature-b", "feature-a", "")

	// Try to reparent feature-a to feature-b (cycle)
	mgr, _ = NewManager(repoDir)
	_, err := mgr.ReparentBranch("feature-a", "feature-b", false)
	if err == nil {
		t.Error("ReparentBranch() should fail when creating a cycle")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("Error should mention circular dependency, got: %v", err)
	}
}

// TestManager_GetRebaseRef tests the getRebaseRef helper
func TestManager_GetRebaseRef(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	mgr, _ = NewManager(repoDir)

	// Tracked branch should return the local branch name
	ref := mgr.getRebaseRef("feature-a")
	if ref != "feature-a" {
		t.Errorf("getRebaseRef('feature-a') = %q, want %q", ref, "feature-a")
	}

	// main is not tracked in any stack (it's a root), no remote, so returns local
	ref = mgr.getRebaseRef("main")
	if ref != "main" {
		t.Errorf("getRebaseRef('main') = %q, want %q", ref, "main")
	}
}

// TestManager_DeleteBranch_NonMainRoot tests deleting branches from non-main-rooted stacks
func TestManager_DeleteBranch_NonMainRoot(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	createGitBranch(t, repoDir, "develop")

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "develop", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("feature-b", "feature-a", "")

	// Delete feature-a (has children, needs force)
	mgr, _ = NewManager(repoDir)
	err := mgr.DeleteBranch("feature-a", true)
	if err != nil {
		t.Fatalf("DeleteBranch() error = %v", err)
	}

	mgr, _ = NewManager(repoDir)
	if mgr.GetBranch("feature-a") != nil {
		t.Error("feature-a should be deleted")
	}

	child := mgr.GetBranch("feature-b")
	if child == nil {
		t.Fatal("feature-b should still exist")
	}
	// After parent deletion, child should be reparented to stack root
	if child.Parent != "develop" {
		t.Errorf("child.Parent = %q, want %q", child.Parent, "develop")
	}
}

// TestManager_MultipleStacksSameRoot tests having multiple stacks with the same non-main root
func TestManager_MultipleStacksSameRoot(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	createGitBranch(t, repoDir, "develop")

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "develop", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("feature-b", "develop", "")

	mgr, _ = NewManager(repoDir)
	stacks := mgr.ListStacks()
	// Each CreateBranch from develop creates a new stack since develop is a root (not in any tree)
	if len(stacks) != 2 {
		t.Errorf("Expected 2 stacks, got %d", len(stacks))
	}

	for _, s := range stacks {
		if s.Root != "develop" {
			t.Errorf("stack.Root = %q, want %q", s.Root, "develop")
		}
	}
}
