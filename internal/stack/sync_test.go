package stack

import (
	"os"
	"os/exec"
	"path/filepath"
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

	originalHome := os.Getenv("EZSTACK_HOME")
	os.Setenv("EZSTACK_HOME", configDir)

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
		os.Setenv("EZSTACK_HOME", originalHome)
		os.RemoveAll(tmpDir)
	}

	return repoDir, worktreeBaseDir, cleanup
}

// TestSyncStack_StopsOnConflict verifies that SyncStack stops when a conflict is encountered
// rather than continuing to the next branch
func TestSyncStack_StopsOnConflict(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// Create a stack: main -> feature-a -> feature-b
	mgr, _ := NewManager(repoDir)
	_, err := mgr.CreateBranch("feature-a", "main", filepath.Join(worktreeBaseDir, "feature-a"), "")
	if err != nil {
		t.Fatalf("CreateBranch feature-a failed: %v", err)
	}

	// Add a commit to feature-a that will conflict
	featureAPath := filepath.Join(worktreeBaseDir, "feature-a")
	conflictFile := filepath.Join(featureAPath, "conflict.txt")
	os.WriteFile(conflictFile, []byte("feature-a content\n"), 0644)
	exec.Command("git", "-C", featureAPath, "add", ".").Run()
	exec.Command("git", "-C", featureAPath, "commit", "-m", "Add conflict.txt in feature-a").Run()

	mgr, _ = NewManager(repoDir)
	_, err = mgr.CreateBranch("feature-b", "feature-a", filepath.Join(worktreeBaseDir, "feature-b"), "")
	if err != nil {
		t.Fatalf("CreateBranch feature-b failed: %v", err)
	}

	// Add a commit to feature-b
	featureBPath := filepath.Join(worktreeBaseDir, "feature-b")
	featureBFile := filepath.Join(featureBPath, "feature-b.txt")
	os.WriteFile(featureBFile, []byte("feature-b content\n"), 0644)
	exec.Command("git", "-C", featureBPath, "add", ".").Run()
	exec.Command("git", "-C", featureBPath, "commit", "-m", "Add file in feature-b").Run()

	// Now add a conflicting change to main
	mainConflictFile := filepath.Join(repoDir, "conflict.txt")
	os.WriteFile(mainConflictFile, []byte("main content - different!\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Add conflict.txt in main").Run()

	// Create a bare repo to simulate origin
	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()

	// Now try to sync - feature-a should fail with conflict, feature-b should NOT be attempted
	mgr, _ = NewManager(featureAPath)
	results, err := mgr.SyncStack(nil, nil)
	if err != nil {
		t.Fatalf("SyncStack returned error: %v", err)
	}

	// Should have exactly 1 result (feature-a with conflict)
	if len(results) != 1 {
		t.Errorf("SyncStack returned %d results, want 1 (should stop on first conflict)", len(results))
	}

	if len(results) > 0 {
		if !results[0].HasConflict {
			t.Errorf("First result should have HasConflict=true, got %v", results[0].HasConflict)
		}
		if results[0].Branch != "feature-a" {
			t.Errorf("First result branch = %q, want 'feature-a'", results[0].Branch)
		}
	}

	// Verify feature-b was NOT touched (no result for it)
	for _, r := range results {
		if r.Branch == "feature-b" {
			t.Error("feature-b should NOT have been processed when feature-a had conflicts")
		}
	}

	// Abort the rebase in feature-a worktree so cleanup works
	exec.Command("git", "-C", featureAPath, "rebase", "--abort").Run()
}

// TestRebaseChildren_StopsOnConflict verifies that RebaseChildren stops on first conflict
func TestRebaseChildren_StopsOnConflict(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// Create parent branch
	mgr, _ := NewManager(repoDir)
	_, err := mgr.CreateBranch("parent", "main", filepath.Join(worktreeBaseDir, "parent"), "")
	if err != nil {
		t.Fatalf("CreateBranch parent failed: %v", err)
	}

	parentPath := filepath.Join(worktreeBaseDir, "parent")

	// Add a commit to parent with a file
	conflictFile := filepath.Join(parentPath, "shared.txt")
	os.WriteFile(conflictFile, []byte("parent version 1\n"), 0644)
	exec.Command("git", "-C", parentPath, "add", ".").Run()
	exec.Command("git", "-C", parentPath, "commit", "-m", "Add shared.txt in parent").Run()

	// Create first child that will have conflicts
	mgr, _ = NewManager(repoDir)
	_, err = mgr.CreateBranch("child-a", "parent", filepath.Join(worktreeBaseDir, "child-a"), "")
	if err != nil {
		t.Fatalf("CreateBranch child-a failed: %v", err)
	}

	// Add conflicting content in child-a
	childAPath := filepath.Join(worktreeBaseDir, "child-a")
	childAFile := filepath.Join(childAPath, "shared.txt")
	os.WriteFile(childAFile, []byte("child-a conflicting content\n"), 0644)
	exec.Command("git", "-C", childAPath, "add", ".").Run()
	exec.Command("git", "-C", childAPath, "commit", "-m", "Modify shared.txt in child-a").Run()

	// Create second child
	mgr, _ = NewManager(repoDir)
	_, err = mgr.CreateBranch("child-b", "parent", filepath.Join(worktreeBaseDir, "child-b"), "")
	if err != nil {
		t.Fatalf("CreateBranch child-b failed: %v", err)
	}

	// Add content in child-b
	childBPath := filepath.Join(worktreeBaseDir, "child-b")
	childBFile := filepath.Join(childBPath, "child-b.txt")
	os.WriteFile(childBFile, []byte("child-b content\n"), 0644)
	exec.Command("git", "-C", childBPath, "add", ".").Run()
	exec.Command("git", "-C", childBPath, "commit", "-m", "Add file in child-b").Run()

	// Now update parent with different content in the same file (creates conflict for child-a)
	os.WriteFile(conflictFile, []byte("parent version 2 - different!\n"), 0644)
	exec.Command("git", "-C", parentPath, "add", ".").Run()
	exec.Command("git", "-C", parentPath, "commit", "-m", "Update shared.txt in parent").Run()

	// Try to rebase children - child-a should fail, child-b should NOT be attempted
	mgr, _ = NewManager(parentPath)
	results, err := mgr.RebaseChildren()
	if err != nil {
		t.Fatalf("RebaseChildren returned error: %v", err)
	}

	// Should have exactly 1 result (child-a with conflict)
	if len(results) != 1 {
		t.Errorf("RebaseChildren returned %d results, want 1 (should stop on first conflict)", len(results))
	}

	if len(results) > 0 {
		if !results[0].HasConflict {
			t.Errorf("First result should have HasConflict=true, got %v", results[0].HasConflict)
		}
		if results[0].Branch != "child-a" {
			t.Errorf("First result branch = %q, want 'child-a'", results[0].Branch)
		}
	}

	// Verify child-b was NOT processed
	for _, r := range results {
		if r.Branch == "child-b" {
			t.Error("child-b should NOT have been processed when child-a had conflicts")
		}
	}

	// Abort rebase in child-a for cleanup
	exec.Command("git", "-C", childAPath, "rebase", "--abort").Run()
}

// TestSyncStack_ContinuesWithoutConflict verifies normal operation works
func TestSyncStack_ContinuesWithoutConflict(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// Create a simple stack without conflicts
	mgr, _ := NewManager(repoDir)
	_, err := mgr.CreateBranch("feature-a", "main", filepath.Join(worktreeBaseDir, "feature-a"), "")
	if err != nil {
		t.Fatalf("CreateBranch feature-a failed: %v", err)
	}

	featureAPath := filepath.Join(worktreeBaseDir, "feature-a")
	fileA := filepath.Join(featureAPath, "file-a.txt")
	os.WriteFile(fileA, []byte("file-a content\n"), 0644)
	exec.Command("git", "-C", featureAPath, "add", ".").Run()
	exec.Command("git", "-C", featureAPath, "commit", "-m", "Add file-a").Run()

	mgr, _ = NewManager(repoDir)
	_, err = mgr.CreateBranch("feature-b", "feature-a", filepath.Join(worktreeBaseDir, "feature-b"), "")
	if err != nil {
		t.Fatalf("CreateBranch feature-b failed: %v", err)
	}

	featureBPath := filepath.Join(worktreeBaseDir, "feature-b")
	fileB := filepath.Join(featureBPath, "file-b.txt")
	os.WriteFile(fileB, []byte("file-b content\n"), 0644)
	exec.Command("git", "-C", featureBPath, "add", ".").Run()
	exec.Command("git", "-C", featureBPath, "commit", "-m", "Add file-b").Run()

	// Add non-conflicting change to main
	mainFile := filepath.Join(repoDir, "main-file.txt")
	os.WriteFile(mainFile, []byte("main content\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Add main-file").Run()

	// Create bare repo for origin
	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()

	// Sync should succeed for both branches
	mgr, _ = NewManager(featureAPath)
	results, err := mgr.SyncStack(nil, nil)
	if err != nil {
		t.Fatalf("SyncStack returned error: %v", err)
	}

	// Both branches should be synced successfully
	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
		if r.HasConflict {
			t.Errorf("Unexpected conflict in %s", r.Branch)
		}
	}

	if successCount != 2 {
		t.Errorf("Expected 2 successful syncs, got %d", successCount)
	}
}

// TestDetectSyncNeeded_NonMainRoot verifies that sync detection for a stack rooted
// on a non-main branch (e.g. develop) checks against origin/<root>, not origin/main.
func TestDetectSyncNeeded_NonMainRoot(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// Create a "develop" branch with its own commit
	exec.Command("git", "-C", repoDir, "branch", "develop").Run()
	exec.Command("git", "-C", repoDir, "checkout", "develop").Run()
	developFile := filepath.Join(repoDir, "develop.txt")
	os.WriteFile(developFile, []byte("develop base\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Develop base commit").Run()
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()

	// Create bare repo as origin and push both branches
	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "develop").Run()

	// Create a stack rooted on develop
	mgr, _ := NewManager(repoDir)
	_, err := mgr.CreateBranch("feature-x", "develop", filepath.Join(worktreeBaseDir, "feature-x"), "")
	if err != nil {
		t.Fatalf("CreateBranch feature-x failed: %v", err)
	}

	// Add a commit to feature-x
	featureXPath := filepath.Join(worktreeBaseDir, "feature-x")
	fxFile := filepath.Join(featureXPath, "fx.txt")
	os.WriteFile(fxFile, []byte("feature-x content\n"), 0644)
	exec.Command("git", "-C", featureXPath, "add", ".").Run()
	exec.Command("git", "-C", featureXPath, "commit", "-m", "Add fx.txt").Run()

	// Advance develop on origin (simulate someone else pushing to develop)
	exec.Command("git", "-C", repoDir, "checkout", "develop").Run()
	developFile2 := filepath.Join(repoDir, "develop2.txt")
	os.WriteFile(developFile2, []byte("develop update\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Update develop").Run()
	exec.Command("git", "-C", repoDir, "push", "origin", "develop").Run()
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()

	// Detect sync needed — should find feature-x behind origin/develop
	mgr, _ = NewManager(featureXPath)
	stack := mgr.GetStackForBranch("feature-x")
	if stack == nil {
		t.Fatal("feature-x should be in a stack")
	}
	if stack.Root != "develop" {
		t.Fatalf("stack.Root = %q, want %q", stack.Root, "develop")
	}

	syncNeeded, err := mgr.DetectSyncNeeded(nil)
	if err != nil {
		t.Fatalf("DetectSyncNeeded error: %v", err)
	}

	if len(syncNeeded) != 1 {
		t.Fatalf("DetectSyncNeeded returned %d results, want 1", len(syncNeeded))
	}

	info := syncNeeded[0]
	if info.Branch != "feature-x" {
		t.Errorf("SyncInfo.Branch = %q, want %q", info.Branch, "feature-x")
	}
	if info.StackRoot != "develop" {
		t.Errorf("SyncInfo.StackRoot = %q, want %q", info.StackRoot, "develop")
	}
	if info.BehindBy != 1 {
		t.Errorf("SyncInfo.BehindBy = %d, want 1", info.BehindBy)
	}
}

// TestDetectSyncNeeded_NonMainRoot_NotBehindMain verifies that a stack rooted on
// develop does NOT report branches as needing sync when main is updated but develop is not.
func TestDetectSyncNeeded_NonMainRoot_NotBehindMain(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// Create develop branch
	exec.Command("git", "-C", repoDir, "branch", "develop").Run()

	// Create bare repo and push
	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "develop").Run()

	// Create a stack rooted on develop
	mgr, _ := NewManager(repoDir)
	_, err := mgr.CreateBranch("feature-y", "develop", filepath.Join(worktreeBaseDir, "feature-y"), "")
	if err != nil {
		t.Fatalf("CreateBranch feature-y failed: %v", err)
	}

	featureYPath := filepath.Join(worktreeBaseDir, "feature-y")
	fyFile := filepath.Join(featureYPath, "fy.txt")
	os.WriteFile(fyFile, []byte("feature-y content\n"), 0644)
	exec.Command("git", "-C", featureYPath, "add", ".").Run()
	exec.Command("git", "-C", featureYPath, "commit", "-m", "Add fy.txt").Run()

	// Advance MAIN on origin (but NOT develop)
	mainFile := filepath.Join(repoDir, "main-update.txt")
	os.WriteFile(mainFile, []byte("main update\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Update main").Run()
	exec.Command("git", "-C", repoDir, "push", "origin", "main").Run()

	// Detect sync needed — should find NOTHING because develop hasn't changed
	mgr, _ = NewManager(featureYPath)
	syncNeeded, err := mgr.DetectSyncNeeded(nil)
	if err != nil {
		t.Fatalf("DetectSyncNeeded error: %v", err)
	}

	if len(syncNeeded) != 0 {
		t.Errorf("DetectSyncNeeded returned %d results, want 0 (main updated but develop didn't)", len(syncNeeded))
		for _, info := range syncNeeded {
			t.Logf("  unexpected: branch=%s stackRoot=%s behindBy=%d", info.Branch, info.StackRoot, info.BehindBy)
		}
	}
}

// TestSyncStack_NonMainRoot verifies that syncing a stack rooted on develop
// rebases against origin/develop, not origin/main.
func TestSyncStack_NonMainRoot(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// Create develop branch with a commit
	exec.Command("git", "-C", repoDir, "branch", "develop").Run()
	exec.Command("git", "-C", repoDir, "checkout", "develop").Run()
	devFile := filepath.Join(repoDir, "dev.txt")
	os.WriteFile(devFile, []byte("develop v1\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Develop v1").Run()
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()

	// Set up origin
	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "develop").Run()

	// Create stack: develop -> feature-z
	mgr, _ := NewManager(repoDir)
	_, err := mgr.CreateBranch("feature-z", "develop", filepath.Join(worktreeBaseDir, "feature-z"), "")
	if err != nil {
		t.Fatalf("CreateBranch feature-z failed: %v", err)
	}

	featureZPath := filepath.Join(worktreeBaseDir, "feature-z")
	fzFile := filepath.Join(featureZPath, "fz.txt")
	os.WriteFile(fzFile, []byte("feature-z content\n"), 0644)
	exec.Command("git", "-C", featureZPath, "add", ".").Run()
	exec.Command("git", "-C", featureZPath, "commit", "-m", "Add fz.txt").Run()

	// Update develop on origin
	exec.Command("git", "-C", repoDir, "checkout", "develop").Run()
	devFile2 := filepath.Join(repoDir, "dev2.txt")
	os.WriteFile(devFile2, []byte("develop v2\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Develop v2").Run()
	exec.Command("git", "-C", repoDir, "push", "origin", "develop").Run()
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()

	// Sync should rebase feature-z onto origin/develop
	mgr, _ = NewManager(featureZPath)
	results, err := mgr.SyncStack(nil, nil)
	if err != nil {
		t.Fatalf("SyncStack error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("SyncStack returned %d results, want 1", len(results))
	}

	r := results[0]
	if r.Branch != "feature-z" {
		t.Errorf("result.Branch = %q, want %q", r.Branch, "feature-z")
	}
	if !r.Success {
		t.Errorf("result.Success = false, want true (error: %v)", r.Error)
	}
	if r.SyncedParent != "origin/develop" {
		t.Errorf("result.SyncedParent = %q, want %q", r.SyncedParent, "origin/develop")
	}

	// Verify feature-z now has develop v2's content
	dev2InWorktree := filepath.Join(featureZPath, "dev2.txt")
	if _, err := os.Stat(dev2InWorktree); os.IsNotExist(err) {
		t.Error("feature-z should have dev2.txt after rebasing onto origin/develop")
	}
}

// TestDetectSyncNeededForBranch_StackRoot verifies the per-branch sync detection
// populates StackRoot correctly.
func TestDetectSyncNeededForBranch_StackRoot(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// Create staging branch
	exec.Command("git", "-C", repoDir, "branch", "staging").Run()

	// Set up origin
	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "staging").Run()

	// Create stack rooted on staging
	mgr, _ := NewManager(repoDir)
	_, err := mgr.CreateBranch("hotfix-1", "staging", filepath.Join(worktreeBaseDir, "hotfix-1"), "")
	if err != nil {
		t.Fatalf("CreateBranch hotfix-1 failed: %v", err)
	}

	hotfixPath := filepath.Join(worktreeBaseDir, "hotfix-1")
	hfFile := filepath.Join(hotfixPath, "fix.txt")
	os.WriteFile(hfFile, []byte("hotfix\n"), 0644)
	exec.Command("git", "-C", hotfixPath, "add", ".").Run()
	exec.Command("git", "-C", hotfixPath, "commit", "-m", "Hotfix").Run()

	// Update staging on origin
	exec.Command("git", "-C", repoDir, "checkout", "staging").Run()
	stagingFile := filepath.Join(repoDir, "staging-update.txt")
	os.WriteFile(stagingFile, []byte("staging update\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Update staging").Run()
	exec.Command("git", "-C", repoDir, "push", "origin", "staging").Run()
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()

	// Detect for specific branch
	mgr, _ = NewManager(hotfixPath)
	info := mgr.DetectSyncNeededForBranch("hotfix-1", nil)
	if info == nil {
		t.Fatal("DetectSyncNeededForBranch returned nil, want sync info")
	}

	if info.StackRoot != "staging" {
		t.Errorf("SyncInfo.StackRoot = %q, want %q", info.StackRoot, "staging")
	}
	if info.BehindBy != 1 {
		t.Errorf("SyncInfo.BehindBy = %d, want 1", info.BehindBy)
	}
	if info.BehindParent != "" {
		t.Errorf("SyncInfo.BehindParent = %q, want empty (behind root, not parent)", info.BehindParent)
	}
}

func TestRebaseResult_Remote(t *testing.T) {
	// Test that RebaseResult carries the Remote field correctly
	tests := []struct {
		name     string
		remote   string
		expected string
	}{
		{"empty remote", "", ""},
		{"custom remote", "fork-remote", "fork-remote"},
		{"nopush", config.RemoteNoPush, config.RemoteNoPush},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RebaseResult{
				Branch:       "feature",
				WorktreePath: "/tmp/wt",
				Remote:       tt.remote,
			}
			if result.Remote != tt.expected {
				t.Errorf("Remote = %q, want %q", result.Remote, tt.expected)
			}
		})
	}
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

// TestSyncStack_NoWorktree verifies that SyncStack works for branches without worktrees
// by using the checkout-based fallback.
func TestSyncStack_NoWorktree(t *testing.T) {
	repoDir, _, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// Set up origin
	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()

	// Create a no-worktree branch: main -> feature-nwt
	mgr, _ := NewManager(repoDir)
	_, err := mgr.CreateBranchNoWorktree("feature-nwt", "main", "")
	if err != nil {
		t.Fatalf("CreateBranchNoWorktree failed: %v", err)
	}

	// Add a commit to feature-nwt
	exec.Command("git", "-C", repoDir, "checkout", "feature-nwt").Run()
	nwtFile := filepath.Join(repoDir, "nwt.txt")
	os.WriteFile(nwtFile, []byte("no-worktree content\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Add nwt.txt").Run()
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()

	// Advance main on origin
	mainFile := filepath.Join(repoDir, "main-update.txt")
	os.WriteFile(mainFile, []byte("main update\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Update main").Run()
	exec.Command("git", "-C", repoDir, "push", "origin", "main").Run()

	// Switch to the branch so SyncStack can find the current stack
	exec.Command("git", "-C", repoDir, "checkout", "feature-nwt").Run()

	// Sync — should rebase feature-nwt via checkout
	mgr, _ = NewManager(repoDir)
	results, err := mgr.SyncStack(nil, nil)
	if err != nil {
		t.Fatalf("SyncStack error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("SyncStack returned %d results, want 1", len(results))
	}

	r := results[0]
	if r.Branch != "feature-nwt" {
		t.Errorf("result.Branch = %q, want %q", r.Branch, "feature-nwt")
	}
	if !r.Success {
		t.Errorf("result.Success = false, want true (error: %v)", r.Error)
	}

	// Verify the branch now has the main update
	// syncViaCheckout should have returned us to feature-nwt (we were on it)
	mainUpdatePath := filepath.Join(repoDir, "main-update.txt")
	exec.Command("git", "-C", repoDir, "checkout", "feature-nwt").Run()
	if _, err := os.Stat(mainUpdatePath); os.IsNotExist(err) {
		t.Error("feature-nwt should have main-update.txt after sync")
	}
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()
}

// TestRebaseChildren_NoWorktree verifies that RebaseChildren works when child branches
// don't have worktrees.
func TestRebaseChildren_NoWorktree(t *testing.T) {
	repoDir, _, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// Create a stack: main -> parent-nwt -> child-nwt (all no-worktree)
	mgr, _ := NewManager(repoDir)
	_, err := mgr.CreateBranchNoWorktree("parent-nwt", "main", "")
	if err != nil {
		t.Fatalf("CreateBranchNoWorktree parent-nwt failed: %v", err)
	}

	// Add a commit to parent-nwt
	exec.Command("git", "-C", repoDir, "checkout", "parent-nwt").Run()
	pFile := filepath.Join(repoDir, "parent.txt")
	os.WriteFile(pFile, []byte("parent content\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Add parent.txt").Run()

	// Create child-nwt from parent-nwt
	mgr, _ = NewManager(repoDir)
	_, err = mgr.CreateBranchNoWorktree("child-nwt", "parent-nwt", "")
	if err != nil {
		t.Fatalf("CreateBranchNoWorktree child-nwt failed: %v", err)
	}

	// Add a commit to child-nwt
	exec.Command("git", "-C", repoDir, "checkout", "child-nwt").Run()
	cFile := filepath.Join(repoDir, "child.txt")
	os.WriteFile(cFile, []byte("child content\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Add child.txt").Run()

	// Go back to parent-nwt and add a new commit (simulating amend/rebase)
	exec.Command("git", "-C", repoDir, "checkout", "parent-nwt").Run()
	pFile2 := filepath.Join(repoDir, "parent2.txt")
	os.WriteFile(pFile2, []byte("parent extra content\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Add parent2.txt").Run()

	// RebaseChildren should sync child-nwt onto parent-nwt
	mgr, _ = NewManager(repoDir)
	results, err := mgr.RebaseChildren()
	if err != nil {
		t.Fatalf("RebaseChildren error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("RebaseChildren returned %d results, want 1", len(results))
	}

	r := results[0]
	if r.Branch != "child-nwt" {
		t.Errorf("result.Branch = %q, want %q", r.Branch, "child-nwt")
	}
	if !r.Success {
		t.Errorf("result.Success = false, want true (error: %v)", r.Error)
	}

	// Verify child-nwt now has parent2.txt
	exec.Command("git", "-C", repoDir, "checkout", "child-nwt").Run()
	parent2Path := filepath.Join(repoDir, "parent2.txt")
	if _, err := os.Stat(parent2Path); os.IsNotExist(err) {
		t.Error("child-nwt should have parent2.txt after rebase")
	}
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()
}

// TestSyncBranch_NoWorktree verifies that SyncBranch works for a specific branch
// without a worktree.
func TestSyncBranch_NoWorktree(t *testing.T) {
	repoDir, _, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// Set up origin
	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()

	// Create a no-worktree branch
	mgr, _ := NewManager(repoDir)
	_, err := mgr.CreateBranchNoWorktree("sync-nwt", "main", "")
	if err != nil {
		t.Fatalf("CreateBranchNoWorktree failed: %v", err)
	}

	// Add a commit to the branch
	exec.Command("git", "-C", repoDir, "checkout", "sync-nwt").Run()
	sFile := filepath.Join(repoDir, "sync-file.txt")
	os.WriteFile(sFile, []byte("sync content\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Add sync-file.txt").Run()
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()

	// Advance main on origin
	mainFile := filepath.Join(repoDir, "new-on-main.txt")
	os.WriteFile(mainFile, []byte("new on main\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "New on main").Run()
	exec.Command("git", "-C", repoDir, "push", "origin", "main").Run()

	// SyncBranch
	mgr, _ = NewManager(repoDir)
	result, err := mgr.SyncBranch("sync-nwt", nil)
	if err != nil {
		t.Fatalf("SyncBranch error: %v", err)
	}

	if !result.Success {
		t.Errorf("SyncBranch result.Success = false, want true (error: %v)", result.Error)
	}

	// Verify the branch now has the main update
	exec.Command("git", "-C", repoDir, "checkout", "sync-nwt").Run()
	mainUpdatePath := filepath.Join(repoDir, "new-on-main.txt")
	if _, err := os.Stat(mainUpdatePath); os.IsNotExist(err) {
		t.Error("sync-nwt should have new-on-main.txt after sync")
	}
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()
}

// TestReparentBranch_NoWorktree verifies that ReparentBranch with doRebase=true works
// for branches without worktrees.
func TestReparentBranch_NoWorktree(t *testing.T) {
	repoDir, _, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// Create two branches from main
	mgr, _ := NewManager(repoDir)
	_, err := mgr.CreateBranchNoWorktree("branch-a", "main", "")
	if err != nil {
		t.Fatalf("CreateBranchNoWorktree branch-a failed: %v", err)
	}

	exec.Command("git", "-C", repoDir, "checkout", "branch-a").Run()
	aFile := filepath.Join(repoDir, "a.txt")
	os.WriteFile(aFile, []byte("branch-a content\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Add a.txt").Run()
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()

	mgr, _ = NewManager(repoDir)
	_, err = mgr.CreateBranchNoWorktree("branch-b", "main", "new")
	if err != nil {
		t.Fatalf("CreateBranchNoWorktree branch-b failed: %v", err)
	}

	exec.Command("git", "-C", repoDir, "checkout", "branch-b").Run()
	bFile := filepath.Join(repoDir, "b.txt")
	os.WriteFile(bFile, []byte("branch-b content\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Add b.txt").Run()
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()

	// Reparent branch-b onto branch-a with rebase
	mgr, _ = NewManager(repoDir)
	result, err := mgr.ReparentBranch("branch-b", "branch-a", true)
	if err != nil {
		t.Fatalf("ReparentBranch error: %v", err)
	}

	if result.HasConflict {
		t.Errorf("ReparentBranch had unexpected conflict in: %s", result.ConflictDir)
	}

	// Verify branch-b now has branch-a's content
	exec.Command("git", "-C", repoDir, "checkout", "branch-b").Run()
	aFilePath := filepath.Join(repoDir, "a.txt")
	if _, err := os.Stat(aFilePath); os.IsNotExist(err) {
		t.Error("branch-b should have a.txt after reparenting onto branch-a")
	}
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()
}

// TestRebaseChildren_StopsOnRecursiveConflict verifies that when a conflict
// surfaces deep in the recursion (a grandchild, not a direct child),
// RebaseChildren stops processing sibling subtrees of the failing branch.
// Regression for a bug where the outer loop appended childResults but did
// not check them for HasConflict before continuing to the next sibling.
func TestRebaseChildren_StopsOnRecursiveConflict(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	_, err := mgr.CreateBranch("parent", "main", filepath.Join(worktreeBaseDir, "parent"), "")
	if err != nil {
		t.Fatalf("CreateBranch parent failed: %v", err)
	}
	parentPath := filepath.Join(worktreeBaseDir, "parent")

	sharedInParent := filepath.Join(parentPath, "shared.txt")
	os.WriteFile(sharedInParent, []byte("parent v1\n"), 0644)
	exec.Command("git", "-C", parentPath, "add", ".").Run()
	exec.Command("git", "-C", parentPath, "commit", "-m", "parent: add shared.txt").Run()

	mgr, _ = NewManager(repoDir)
	_, err = mgr.CreateBranch("child-a", "parent", filepath.Join(worktreeBaseDir, "child-a"), "")
	if err != nil {
		t.Fatalf("CreateBranch child-a failed: %v", err)
	}
	childAPath := filepath.Join(worktreeBaseDir, "child-a")
	os.WriteFile(filepath.Join(childAPath, "child-a.txt"), []byte("child-a\n"), 0644)
	exec.Command("git", "-C", childAPath, "add", ".").Run()
	exec.Command("git", "-C", childAPath, "commit", "-m", "child-a: add file").Run()

	mgr, _ = NewManager(repoDir)
	_, err = mgr.CreateBranch("grandchild-a", "child-a", filepath.Join(worktreeBaseDir, "grandchild-a"), "")
	if err != nil {
		t.Fatalf("CreateBranch grandchild-a failed: %v", err)
	}
	gcPath := filepath.Join(worktreeBaseDir, "grandchild-a")
	os.WriteFile(filepath.Join(gcPath, "shared.txt"), []byte("grandchild rewrite\n"), 0644)
	exec.Command("git", "-C", gcPath, "add", ".").Run()
	exec.Command("git", "-C", gcPath, "commit", "-m", "grandchild-a: rewrite shared.txt").Run()

	mgr, _ = NewManager(repoDir)
	_, err = mgr.CreateBranch("sibling-b", "parent", filepath.Join(worktreeBaseDir, "sibling-b"), "")
	if err != nil {
		t.Fatalf("CreateBranch sibling-b failed: %v", err)
	}
	sbPath := filepath.Join(worktreeBaseDir, "sibling-b")
	os.WriteFile(filepath.Join(sbPath, "sibling-b.txt"), []byte("sibling-b\n"), 0644)
	exec.Command("git", "-C", sbPath, "add", ".").Run()
	exec.Command("git", "-C", sbPath, "commit", "-m", "sibling-b: add file").Run()

	sbHead, err := exec.Command("git", "-C", sbPath, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse sibling-b: %v", err)
	}

	// Parent bumps shared.txt → conflict with grandchild-a
	os.WriteFile(sharedInParent, []byte("parent v2\n"), 0644)
	exec.Command("git", "-C", parentPath, "add", ".").Run()
	exec.Command("git", "-C", parentPath, "commit", "-m", "parent: bump shared.txt").Run()

	mgr, _ = NewManager(parentPath)
	results, err := mgr.RebaseChildren()
	if err != nil {
		t.Fatalf("RebaseChildren returned error: %v", err)
	}

	hasConflict := false
	for _, r := range results {
		if r.HasConflict || r.Error != nil {
			hasConflict = true
		}
		if r.Branch == "sibling-b" && r.Success {
			t.Error("sibling-b must not be synced after grandchild-a conflict")
		}
	}
	if !hasConflict {
		t.Fatalf("expected a conflict to be reported; got results: %+v", results)
	}

	sbHeadAfter, err := exec.Command("git", "-C", sbPath, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse sibling-b after: %v", err)
	}
	if string(sbHead) != string(sbHeadAfter) {
		t.Errorf("sibling-b HEAD changed after a conflict in grandchild subtree:\n  before=%s  after=%s",
			string(sbHead), string(sbHeadAfter))
	}

	exec.Command("git", "-C", gcPath, "rebase", "--abort").Run()
	exec.Command("git", "-C", childAPath, "rebase", "--abort").Run()
}

// TestSyncStack_PersistsMergedCacheOnConflict verifies that when the walk
// marks a parent as merged and a later branch conflicts in the non-merged-
// parent path, the merged-cache update is still persisted. Regression for
// a bug where saveState was skipped in the non-merged-parent conflict path.
func TestSyncStack_PersistsMergedCacheOnConflict(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()

	mgr, _ := NewManager(repoDir)
	if _, err := mgr.CreateBranch("A", "main", filepath.Join(worktreeBaseDir, "A"), ""); err != nil {
		t.Fatalf("create A: %v", err)
	}
	aPath := filepath.Join(worktreeBaseDir, "A")
	os.WriteFile(filepath.Join(aPath, "a.txt"), []byte("a\n"), 0644)
	exec.Command("git", "-C", aPath, "add", ".").Run()
	exec.Command("git", "-C", aPath, "commit", "-m", "A: add a.txt").Run()

	mgr, _ = NewManager(repoDir)
	if _, err := mgr.CreateBranch("B", "A", filepath.Join(worktreeBaseDir, "B"), ""); err != nil {
		t.Fatalf("create B: %v", err)
	}
	bPath := filepath.Join(worktreeBaseDir, "B")
	os.WriteFile(filepath.Join(bPath, "b.txt"), []byte("b\n"), 0644)
	exec.Command("git", "-C", bPath, "add", ".").Run()
	exec.Command("git", "-C", bPath, "commit", "-m", "B: add b.txt").Run()

	mgr, _ = NewManager(repoDir)
	if _, err := mgr.CreateBranch("C", "B", filepath.Join(worktreeBaseDir, "C"), ""); err != nil {
		t.Fatalf("create C: %v", err)
	}
	cPath := filepath.Join(worktreeBaseDir, "C")
	os.WriteFile(filepath.Join(cPath, "shared.txt"), []byte("C version\n"), 0644)
	exec.Command("git", "-C", cPath, "add", ".").Run()
	exec.Command("git", "-C", cPath, "commit", "-m", "C: add shared.txt").Run()

	// Fast-forward origin/main to A, then add a conflicting commit on top.
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()
	aHead, _ := exec.Command("git", "-C", aPath, "rev-parse", "HEAD").Output()
	exec.Command("git", "-C", repoDir, "reset", "--hard", string(aHead[:len(aHead)-1])).Run()
	os.WriteFile(filepath.Join(repoDir, "shared.txt"), []byte("main version\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "main: add shared.txt").Run()
	exec.Command("git", "-C", repoDir, "push", "origin", "main").Run()
	exec.Command("git", "-C", repoDir, "fetch", "origin").Run()

	mgr, _ = NewManager(cPath)
	_, err := mgr.SyncStack(nil, nil)
	if err != nil {
		t.Fatalf("SyncStack error: %v", err)
	}

	// Re-open and check the cache was persisted.
	freshMgr, err := NewManager(repoDir)
	if err != nil {
		t.Fatalf("reopen manager: %v", err)
	}
	bc := freshMgr.stackConfig.Cache.GetBranchCache("A")
	if bc == nil || !bc.IsMerged {
		t.Errorf("expected A to be persisted as merged in cache after conflict; got %+v", bc)
	}

	exec.Command("git", "-C", cPath, "rebase", "--abort").Run()
}

// TestResolveBranchWorktree_DetectsDrift verifies that resolveBranchWorktree
// finds the actual git worktree for a branch when ezstack's config has drifted
// to an empty WorktreePath. Regression test for the bug where sync fell into
// syncViaCheckout in this case, colliding with the existing worktree or (on
// success) pushing main to origin after restoring HEAD.
func TestResolveBranchWorktree_DetectsDrift(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// Create a branch with a dedicated worktree.
	mgr, _ := NewManager(repoDir)
	expectedWorktree := filepath.Join(worktreeBaseDir, "feat-x")
	if _, err := mgr.CreateBranch("feat-x", "main", expectedWorktree, ""); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	expectedWorktree, _ = filepath.EvalSymlinks(expectedWorktree)

	// Simulate config drift: the worktree exists in git but ezstack config
	// forgot about it. This can happen if a user manually edits config, or
	// after certain error paths that leave config half-written.
	mgr, _ = NewManager(repoDir)
	branch := mgr.GetBranch("feat-x")
	if branch == nil {
		t.Fatalf("branch not found after create")
	}
	branch.WorktreePath = "" // drift

	got := mgr.resolveBranchWorktree(branch)
	if got == "" {
		t.Fatalf("resolveBranchWorktree returned empty string; expected %q (git has the worktree)", expectedWorktree)
	}
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != expectedWorktree {
		t.Errorf("resolveBranchWorktree = %q, want %q", gotResolved, expectedWorktree)
	}

	// Config should be healed in-memory so subsequent callers use the right
	// path without having to re-resolve via git.
	if branch.WorktreePath == "" {
		t.Error("resolveBranchWorktree did not back-fill branch.WorktreePath; config remained drifted")
	}
}

// TestResolveBranchWorktree_NoWorktree verifies the helper returns empty
// string when there's truly no worktree (legitimate checkout-based sync path).
func TestResolveBranchWorktree_NoWorktree(t *testing.T) {
	repoDir, _, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// Create a branch without a worktree via CreateBranchNoWorktree
	mgr, _ := NewManager(repoDir)
	if _, err := mgr.CreateBranchNoWorktree("no-wt", "main", ""); err != nil {
		t.Fatalf("CreateBranchNoWorktree: %v", err)
	}

	branch := mgr.GetBranch("no-wt")
	if branch == nil {
		t.Fatalf("branch not found")
	}
	// Belt-and-suspenders: force-clear WorktreePath in case the factory
	// populated something.
	branch.WorktreePath = ""

	if got := mgr.resolveBranchWorktree(branch); got != "" {
		t.Errorf("resolveBranchWorktree with no git worktree = %q, want empty", got)
	}
}

// TestResolveBranchWorktree_IgnoresMainRepo verifies we don't misidentify the
// main repo's own checkout as a dedicated worktree. If a branch happens to be
// checked out in the main repo (e.g. by a user manually), checkout-based sync
// is still the right path.
func TestResolveBranchWorktree_IgnoresMainRepo(t *testing.T) {
	repoDir, _, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// Create a branch without an ezstack-managed worktree, then manually
	// check it out in the main repo.
	mgr, _ := NewManager(repoDir)
	if _, err := mgr.CreateBranchNoWorktree("in-main", "main", ""); err != nil {
		t.Fatalf("CreateBranchNoWorktree: %v", err)
	}
	if err := exec.Command("git", "-C", repoDir, "checkout", "in-main").Run(); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	// Switch back so the main repo is on main for the next assertion.
	defer exec.Command("git", "-C", repoDir, "checkout", "main").Run()

	mgr, _ = NewManager(repoDir)
	branch := mgr.GetBranch("in-main")
	branch.WorktreePath = ""

	if got := mgr.resolveBranchWorktree(branch); got != "" {
		t.Errorf("resolveBranchWorktree when branch is checked out in main repo = %q, want empty (should fall through to checkout sync)", got)
	}
}

// TestSyncBranch_NoCascadingConflictAfterParentRebase reproduces the bug where
// `ezs sync --continue`'s child re-sync (which calls SyncBranch) re-encounters
// a conflict that was already resolved when the parent was rebased earlier in
// the same sync session.
//
// Setup: main(M) → A(a1) → B(b1) → C(c1). M and a1 both modify the same region
// of the same file so rebasing A onto main hits a conflict. B and C edit
// different (well-separated) regions of the same file, so they should NOT
// conflict against the resolved A.
//
// Edits are deliberately separated by more than git's default 3-line diff
// context so adjacent-edit hunk overlap doesn't masquerade as a real cascade.
// Without the fix (plain `git rebase A` from B's worktree), git computes
// merge-base(B, new_A) = old_main, so it replays a1+b1 onto new_A and
// re-encounters a1's conflict against the resolved a1' that's already in
// new_A. With the fix, SyncBranch uses `--onto new_A old_A_SHA` so only b1 is
// replayed.
func TestSyncBranch_NoCascadingConflictAfterParentRebase(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	// 12-line file gives every branch its own region with ≥3 lines of context.
	baseLines := []string{
		"line-1\n", "line-2\n", "line-3\n", "line-4\n",
		"line-5\n", "line-6\n", "line-7\n", "line-8\n",
		"line-9\n", "line-10\n", "line-11\n", "line-12\n",
	}
	join := func(lines []string) string {
		out := ""
		for _, l := range lines {
			out += l
		}
		return out
	}

	sharedMain := filepath.Join(repoDir, "shared.txt")
	if err := os.WriteFile(sharedMain, []byte(join(baseLines)), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "seed shared.txt").Run()

	// Bare origin so origin/main exists.
	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()

	mgr, _ := NewManager(repoDir)
	mustCreate := func(name, parent string) string {
		path := filepath.Join(worktreeBaseDir, name)
		if _, err := mgr.CreateBranch(name, parent, path, ""); err != nil {
			t.Fatalf("CreateBranch %s: %v", name, err)
		}
		mgr, _ = NewManager(repoDir)
		return path
	}

	mutate := func(lines []string, idx int, newVal string) []string {
		out := make([]string, len(lines))
		copy(out, lines)
		out[idx] = newVal + "\n"
		return out
	}

	aPath := mustCreate("a", "main")
	aLines := mutate(baseLines, 0, "line-1-A") // a1 edits line 1.
	if err := os.WriteFile(filepath.Join(aPath, "shared.txt"), []byte(join(aLines)), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", aPath, "add", ".").Run()
	exec.Command("git", "-C", aPath, "commit", "-m", "a1: edit line 1").Run()

	bPath := mustCreate("b", "a")
	bLines := mutate(aLines, 5, "line-6-B") // b1 edits line 6 (≥3 lines of context away from line 1).
	if err := os.WriteFile(filepath.Join(bPath, "shared.txt"), []byte(join(bLines)), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", bPath, "add", ".").Run()
	exec.Command("git", "-C", bPath, "commit", "-m", "b1: edit line 6").Run()

	cPath := mustCreate("c", "b")
	cLines := mutate(bLines, 10, "line-11-C") // c1 edits line 11.
	if err := os.WriteFile(filepath.Join(cPath, "shared.txt"), []byte(join(cLines)), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", cPath, "add", ".").Run()
	exec.Command("git", "-C", cPath, "commit", "-m", "c1: edit line 11").Run()

	// Update main to conflict with a1 on line 1.
	mainLines := mutate(baseLines, 0, "line-1-MAIN")
	if err := os.WriteFile(sharedMain, []byte(join(mainLines)), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "main: edit line 1").Run()
	exec.Command("git", "-C", repoDir, "push", "origin", "main").Run()

	// Sync the stack from a's worktree. SyncStack walks branches and stops on
	// a's conflict (a1 vs M). Subsequent branches are skipped pending a's
	// resolution.
	mgr, _ = NewManager(aPath)
	results, err := mgr.SyncStack(nil, nil)
	if err != nil {
		t.Fatalf("SyncStack: %v", err)
	}
	if len(results) == 0 || !results[0].HasConflict || results[0].Branch != "a" {
		t.Fatalf("expected a's rebase to conflict; got results=%+v", results)
	}

	// Simulate the user resolving a's conflict and continuing the rebase.
	resolvedLines := mutate(baseLines, 0, "line-1-RESOLVED")
	if err := os.WriteFile(filepath.Join(aPath, "shared.txt"), []byte(join(resolvedLines)), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", aPath, "add", ".").Run()
	contCmd := exec.Command("git", "-C", aPath, "rebase", "--continue")
	contCmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	if out, err := contCmd.CombinedOutput(); err != nil {
		t.Fatalf("rebase --continue failed: %v\n%s", err, string(out))
	}

	// Sanity: a's PreSyncCommit must still be set because the bulk sync left it
	// for --continue's child re-sync to consume.
	mgr, _ = NewManager(repoDir)
	bcA := mgr.stackConfig.Cache.GetBranchCache("a")
	if bcA == nil || bcA.PreSyncCommit == "" {
		t.Fatalf("expected PreSyncCommit on 'a' to be persisted after conflict; got %+v", bcA)
	}

	// Now invoke SyncBranch on b — exactly what `ezs sync --continue` does for
	// children of a continued branch. The bug would cause b to re-conflict on
	// line-a; the fix uses --onto with a's PreSyncCommit so only b1 is replayed.
	bMgr, _ := NewManager(bPath)
	bResult, err := bMgr.SyncBranch("b", nil)
	if err != nil {
		t.Fatalf("SyncBranch(b): %v", err)
	}
	if bResult.HasConflict {
		// Make rebase --abort so cleanup doesn't leave the worktree wedged.
		exec.Command("git", "-C", bPath, "rebase", "--abort").Run()
		t.Fatalf("BUG: SyncBranch(b) hit a cascading conflict — fix did not prevent re-replay of a's commits. Result: %+v", bResult)
	}
	if !bResult.Success {
		t.Fatalf("SyncBranch(b) did not succeed: %+v", bResult)
	}

	// And c — same logic, two levels deep.
	cMgr, _ := NewManager(cPath)
	cResult, err := cMgr.SyncBranch("c", nil)
	if err != nil {
		t.Fatalf("SyncBranch(c): %v", err)
	}
	if cResult.HasConflict {
		exec.Command("git", "-C", cPath, "rebase", "--abort").Run()
		t.Fatalf("BUG: SyncBranch(c) hit a cascading conflict. Result: %+v", cResult)
	}
	if !cResult.Success {
		t.Fatalf("SyncBranch(c) did not succeed: %+v", cResult)
	}

	// After every branch has rebased, b and c's worktrees should hold the
	// resolved a1' content for line 1, plus their own edits.
	wantB := join(mutate(resolvedLines, 5, "line-6-B"))
	bShared, _ := os.ReadFile(filepath.Join(bPath, "shared.txt"))
	if got := string(bShared); got != wantB {
		t.Errorf("b's shared.txt mismatch.\n got: %q\nwant: %q", got, wantB)
	}
	wantC := join(mutate(mutate(resolvedLines, 5, "line-6-B"), 10, "line-11-C"))
	cShared, _ := os.ReadFile(filepath.Join(cPath, "shared.txt"))
	if got := string(cShared); got != wantC {
		t.Errorf("c's shared.txt mismatch.\n got: %q\nwant: %q", got, wantC)
	}

	// PreSyncCommit is intentionally retained on individual SyncBranch
	// successes — descendants might still need it. Cleanup is handled by bulk
	// sync runs (end-of-run if no conflicts) and by `--continue`.
}

// TestSnapshotAndClearPreSyncSHAs verifies the persistence helpers' contract:
// snapshot writes the current HEAD, clear removes it, and stale-cleanup removes
// snapshots whose branch HEAD hasn't moved.
func TestSnapshotAndClearPreSyncSHAs(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	if _, err := mgr.CreateBranch("feat-x", "main", filepath.Join(worktreeBaseDir, "feat-x"), ""); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	xPath := filepath.Join(worktreeBaseDir, "feat-x")
	if err := os.WriteFile(filepath.Join(xPath, "x.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", xPath, "add", ".").Run()
	exec.Command("git", "-C", xPath, "commit", "-m", "x1").Run()

	mgr, _ = NewManager(repoDir)
	beforeSHA, _ := mgr.git.GetBranchCommit("feat-x")

	mgr.snapshotPreSyncSHAs([]string{"feat-x"})
	bc := mgr.stackConfig.Cache.GetBranchCache("feat-x")
	if bc == nil || bc.PreSyncCommit != beforeSHA {
		t.Fatalf("snapshot did not record HEAD: bc=%+v, want PreSyncCommit=%s", bc, beforeSHA)
	}

	// stale-cleanup should clear it because HEAD == snapshot and no rebase in progress.
	mgr.cleanupStalePreSyncSHAs()
	bc = mgr.stackConfig.Cache.GetBranchCache("feat-x")
	if bc != nil && bc.PreSyncCommit != "" {
		t.Errorf("cleanupStalePreSyncSHAs should clear unchanged-HEAD snapshot, got %q", bc.PreSyncCommit)
	}

	// snapshot, advance HEAD, then verify stale-cleanup leaves snapshot alone (HEAD moved).
	mgr.snapshotPreSyncSHAs([]string{"feat-x"})
	if err := os.WriteFile(filepath.Join(xPath, "x.txt"), []byte("x2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", xPath, "add", ".").Run()
	exec.Command("git", "-C", xPath, "commit", "-m", "x2").Run()

	mgr.cleanupStalePreSyncSHAs()
	bc = mgr.stackConfig.Cache.GetBranchCache("feat-x")
	if bc == nil || bc.PreSyncCommit != beforeSHA {
		t.Errorf("cleanupStalePreSyncSHAs should leave snapshot when HEAD has moved; bc=%+v want PreSyncCommit=%s", bc, beforeSHA)
	}

	// Explicit clear works.
	mgr.clearPreSyncSHAs([]string{"feat-x"})
	bc = mgr.stackConfig.Cache.GetBranchCache("feat-x")
	if bc != nil && bc.PreSyncCommit != "" {
		t.Errorf("clearPreSyncSHAs should clear, got %q", bc.PreSyncCommit)
	}

	// lookupPreSyncSHA should fall back to the live HEAD when no snapshot is set.
	curSHA, _ := mgr.git.GetBranchCommit("feat-x")
	if got := mgr.lookupPreSyncSHA("feat-x"); got != curSHA {
		t.Errorf("lookupPreSyncSHA fallback = %q, want live HEAD %q", got, curSHA)
	}
}
