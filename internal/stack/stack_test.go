package stack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ezstack/ezstack/internal/config"
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

func TestManager_GetStackDescription(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("feature-a", "main", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("feature-b", "feature-a", "")

	mgr, _ = NewManager(repoDir)
	stacks := mgr.ListStacks()
	if len(stacks) == 0 {
		t.Fatal("No stacks found")
	}

	desc := mgr.GetStackDescription(stacks[0], "feature-a")

	if desc == "" {
		t.Error("GetStackDescription() returned empty string")
	}

	if !strings.Contains(desc, "PR Stack") {
		t.Error("Description should contain 'PR Stack'")
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

// TestManager_CollectDescendants tests collecting all descendants
func TestManager_CollectDescendants(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	// Create a tree: main -> a -> b -> c
	//                       \-> d
	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("a", "main", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("b", "a", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("c", "b", "")

	mgr, _ = NewManager(repoDir)
	mgr.CreateBranch("d", "a", "")

	mgr, _ = NewManager(repoDir)
	descendants := mgr.collectDescendants("a")

	// Should have b, c, d as descendants
	if len(descendants) != 3 {
		t.Errorf("collectDescendants('a') returned %d, want 3", len(descendants))
	}

	// Check all expected descendants are present
	names := make(map[string]bool)
	for _, d := range descendants {
		names[d.Name] = true
	}
	for _, expected := range []string{"b", "c", "d"} {
		if !names[expected] {
			t.Errorf("collectDescendants('a') missing %q", expected)
		}
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
