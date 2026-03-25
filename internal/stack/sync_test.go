package stack

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
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

	exec.Command("git", "-C", repoDir, "init").Run()
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
	exec.Command("git", "init", "--bare", bareDir).Run()
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
	exec.Command("git", "init", "--bare", bareDir).Run()
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
	exec.Command("git", "init", "--bare", bareDir).Run()
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
	exec.Command("git", "init", "--bare", bareDir).Run()
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
	exec.Command("git", "init", "--bare", bareDir).Run()
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
	exec.Command("git", "init", "--bare", bareDir).Run()
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
