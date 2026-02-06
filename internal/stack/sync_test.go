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
	_, err := mgr.CreateBranch("feature-a", "main", filepath.Join(worktreeBaseDir, "feature-a"))
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
	_, err = mgr.CreateBranch("feature-b", "feature-a", filepath.Join(worktreeBaseDir, "feature-b"))
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
	_, err := mgr.CreateBranch("parent", "main", filepath.Join(worktreeBaseDir, "parent"))
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
	_, err = mgr.CreateBranch("child-a", "parent", filepath.Join(worktreeBaseDir, "child-a"))
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
	_, err = mgr.CreateBranch("child-b", "parent", filepath.Join(worktreeBaseDir, "child-b"))
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
	_, err := mgr.CreateBranch("feature-a", "main", filepath.Join(worktreeBaseDir, "feature-a"))
	if err != nil {
		t.Fatalf("CreateBranch feature-a failed: %v", err)
	}

	featureAPath := filepath.Join(worktreeBaseDir, "feature-a")
	fileA := filepath.Join(featureAPath, "file-a.txt")
	os.WriteFile(fileA, []byte("file-a content\n"), 0644)
	exec.Command("git", "-C", featureAPath, "add", ".").Run()
	exec.Command("git", "-C", featureAPath, "commit", "-m", "Add file-a").Run()

	mgr, _ = NewManager(repoDir)
	_, err = mgr.CreateBranch("feature-b", "feature-a", filepath.Join(worktreeBaseDir, "feature-b"))
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
