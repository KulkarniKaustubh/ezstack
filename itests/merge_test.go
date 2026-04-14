package itests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
)

// TestRebaseChildren_Merge tests that RebaseChildren with useMerge=true
// uses merge instead of rebase
func TestRebaseChildren_Merge(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create a stack: main -> parent-branch -> child-branch
	CreateBranchWithCommit(t, env, "parent-branch", "main")
	CreateBranchWithCommit(t, env, "child-branch", "parent-branch")

	childWorktree := filepath.Join(env.WorktreeDir, "child-branch")

	// Add a new commit to parent
	parentWorktree := filepath.Join(env.WorktreeDir, "parent-branch")
	GitCommit(t, parentWorktree, "parent-update.txt", "updated", "Update parent")

	// Sync children using merge
	mgr, err := stack.NewManager(parentWorktree)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	results, err := mgr.RebaseChildren(true) // useMerge=true
	if err != nil {
		t.Fatalf("RebaseChildren(merge) failed: %v", err)
	}

	// Check results
	if len(results) == 0 {
		t.Fatal("Expected at least one result")
	}

	for _, r := range results {
		if r.HasConflict {
			t.Errorf("Unexpected conflict in %s", r.Branch)
		}
		if r.Error != nil {
			t.Errorf("Unexpected error in %s: %v", r.Branch, r.Error)
		}
		if !r.Success {
			t.Errorf("Expected success for %s", r.Branch)
		}
	}

	// Verify merge commit exists (merge creates a merge commit)
	logOutput, err := exec.Command("git", "-C", childWorktree, "log", "--oneline", "-5").Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if !strings.Contains(string(logOutput), "Merge") {
		t.Errorf("Expected merge commit in log, got: %s", string(logOutput))
	}

	// Verify the parent's commit content is in the child
	if _, err := os.Stat(filepath.Join(childWorktree, "parent-update.txt")); os.IsNotExist(err) {
		t.Error("parent-update.txt should exist in child worktree after merge")
	}
}

// TestRebaseChildren_Rebase tests that RebaseChildren with useMerge=false
// uses rebase (default behavior)
func TestRebaseChildren_Rebase(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create a stack: main -> parent-branch -> child-branch
	CreateBranchWithCommit(t, env, "parent-branch", "main")
	CreateBranchWithCommit(t, env, "child-branch", "parent-branch")

	// Add a new commit to parent
	parentWorktree := filepath.Join(env.WorktreeDir, "parent-branch")
	GitCommit(t, parentWorktree, "parent-update.txt", "updated", "Update parent")

	// Sync children using rebase (default)
	mgr, err := stack.NewManager(parentWorktree)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	results, err := mgr.RebaseChildren(false) // useMerge=false
	if err != nil {
		t.Fatalf("RebaseChildren(rebase) failed: %v", err)
	}

	for _, r := range results {
		if r.HasConflict {
			t.Errorf("Unexpected conflict in %s", r.Branch)
		}
		if !r.Success {
			t.Errorf("Expected success for %s", r.Branch)
		}
	}

	// Verify the parent's commit is in the child
	childWorktree := filepath.Join(env.WorktreeDir, "child-branch")
	if _, err := os.Stat(filepath.Join(childWorktree, "parent-update.txt")); os.IsNotExist(err) {
		t.Error("parent-update.txt should exist in child worktree after rebase")
	}
}

// TestReparentBranch_WithMerge tests reparenting a branch using merge
func TestReparentBranch_WithMerge(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create: main -> branch-a, main -> branch-b (separate stacks)
	CreateBranchWithCommit(t, env, "branch-a", "main")
	CreateBranchWithCommit(t, env, "branch-b", "main")

	// Add a unique commit to branch-a
	worktreeA := filepath.Join(env.WorktreeDir, "branch-a")
	GitCommit(t, worktreeA, "a-only.txt", "only in a", "Only in branch-a")

	mgr, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Reparent branch-b onto branch-a using merge
	result, err := mgr.ReparentBranch("branch-b", "branch-a", true, true) // doRebase=true, useMerge=true
	if err != nil {
		t.Fatalf("ReparentBranch with merge failed: %v", err)
	}

	if result.HasConflict {
		t.Fatal("Unexpected conflict during merge reparent")
	}

	// Verify parent was updated
	AssertBranchParent(t, env, "branch-b", "branch-a")

	// Verify branch-a's content is now in branch-b
	worktreeB := filepath.Join(env.WorktreeDir, "branch-b")
	if _, err := os.Stat(filepath.Join(worktreeB, "a-only.txt")); os.IsNotExist(err) {
		t.Error("a-only.txt should exist in branch-b after merge reparent")
	}
}

// TestReparentBranch_WithRebase tests reparenting a branch using rebase (default)
func TestReparentBranch_WithRebase(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create: main -> branch-a, main -> branch-b
	CreateBranchWithCommit(t, env, "branch-a", "main")
	CreateBranchWithCommit(t, env, "branch-b", "main")

	// Add a unique commit to branch-a
	worktreeA := filepath.Join(env.WorktreeDir, "branch-a")
	GitCommit(t, worktreeA, "a-only.txt", "only in a", "Only in branch-a")

	mgr, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Reparent branch-b onto branch-a using rebase (no useMerge arg = default false)
	result, err := mgr.ReparentBranch("branch-b", "branch-a", true)
	if err != nil {
		t.Fatalf("ReparentBranch with rebase failed: %v", err)
	}

	if result.HasConflict {
		t.Fatal("Unexpected conflict during rebase reparent")
	}

	// Verify parent was updated
	AssertBranchParent(t, env, "branch-b", "branch-a")

	// Verify branch-a's content is now in branch-b
	worktreeB := filepath.Join(env.WorktreeDir, "branch-b")
	if _, err := os.Stat(filepath.Join(worktreeB, "a-only.txt")); os.IsNotExist(err) {
		t.Error("a-only.txt should exist in branch-b after rebase reparent")
	}
}

// TestReparentBranch_MergeConflict tests reparent with merge when there's a conflict
func TestReparentBranch_MergeConflict(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create: main -> branch-a, main -> branch-b
	CreateBranch(t, env, "branch-a", "main")
	CreateBranch(t, env, "branch-b", "main")

	// Create conflicting changes in same file
	worktreeA := filepath.Join(env.WorktreeDir, "branch-a")
	worktreeB := filepath.Join(env.WorktreeDir, "branch-b")
	GitCommit(t, worktreeA, "conflict.txt", "content from A", "Branch A change")
	GitCommit(t, worktreeB, "conflict.txt", "content from B", "Branch B change")

	mgr, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Reparent branch-b onto branch-a using merge — expect conflict
	result, err := mgr.ReparentBranch("branch-b", "branch-a", true, true)
	if err != nil {
		t.Fatalf("ReparentBranch error: %v", err)
	}

	if !result.HasConflict {
		t.Error("Expected conflict during merge reparent with conflicting files")
	}

	// Config should still be saved (parent updated even on conflict)
	AssertBranchParent(t, env, "branch-b", "branch-a")

	// Abort the merge to clean up
	exec.Command("git", "-C", worktreeB, "merge", "--abort").Run()
}

// TestSyncBranch_Merge tests syncing a branch behind its parent using merge
func TestSyncBranch_Merge(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create a stack: main -> parent -> child
	// SyncBranch for non-root branches syncs against parent, not origin
	CreateBranchWithCommit(t, env, "parent-sync", "main")
	CreateBranchWithCommit(t, env, "child-sync", "parent-sync")

	// Add commits to parent
	parentWorktree := filepath.Join(env.WorktreeDir, "parent-sync")
	GitCommit(t, parentWorktree, "parent-update.txt", "parent update", "Update parent")

	mgr, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Sync child branch using merge
	result, err := mgr.SyncBranch("child-sync", nil, true) // useMerge=true
	if err != nil {
		t.Fatalf("SyncBranch(merge) failed: %v", err)
	}

	if result != nil && result.HasConflict {
		t.Error("Unexpected conflict")
	}
	if result != nil && !result.Success {
		t.Errorf("Expected success, got error: %v", result.Error)
	}

	// Verify parent's commit is now in the child branch
	childWorktree := filepath.Join(env.WorktreeDir, "child-sync")
	if _, err := os.Stat(filepath.Join(childWorktree, "parent-update.txt")); os.IsNotExist(err) {
		t.Error("parent-update.txt should exist in child worktree after merge sync")
	}
}

// TestSyncBranch_Rebase tests syncing a branch behind its parent using rebase
func TestSyncBranch_Rebase(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create a stack: main -> parent -> child
	CreateBranchWithCommit(t, env, "parent-sync", "main")
	CreateBranchWithCommit(t, env, "child-sync", "parent-sync")

	// Add commits to parent
	parentWorktree := filepath.Join(env.WorktreeDir, "parent-sync")
	GitCommit(t, parentWorktree, "parent-update.txt", "parent update", "Update parent")

	mgr, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Sync child branch using rebase
	result, err := mgr.SyncBranch("child-sync", nil, false) // useMerge=false
	if err != nil {
		t.Fatalf("SyncBranch(rebase) failed: %v", err)
	}

	if result != nil && result.HasConflict {
		t.Error("Unexpected conflict")
	}
	if result != nil && !result.Success {
		t.Errorf("Expected success, got error: %v", result.Error)
	}

	// Verify parent's commit is now in the child branch
	childWorktree := filepath.Join(env.WorktreeDir, "child-sync")
	if _, err := os.Stat(filepath.Join(childWorktree, "parent-update.txt")); os.IsNotExist(err) {
		t.Error("parent-update.txt should exist in child worktree after rebase sync")
	}
}

// TestSyncStrategy_ConfigPersistence tests that sync_strategy is preserved in config
func TestSyncStrategy_ConfigPersistence(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Load config and set sync_strategy
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load config failed: %v", err)
	}

	repoCfg := cfg.GetRepoConfig(env.RepoDir)
	if repoCfg == nil {
		repoCfg = &config.RepoConfig{}
	}
	repoCfg.SyncStrategy = "merge"
	cfg.SetRepoConfig(env.RepoDir, repoCfg)

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save config failed: %v", err)
	}

	// Reload and verify
	cfg2, err := config.Load()
	if err != nil {
		t.Fatalf("Reload config failed: %v", err)
	}

	strategy := cfg2.GetSyncStrategy(env.RepoDir)
	if strategy != "merge" {
		t.Errorf("GetSyncStrategy() = %q, want %q", strategy, "merge")
	}
}

// TestSyncStrategy_DefaultRebase tests that unset sync_strategy defaults to rebase
func TestSyncStrategy_DefaultRebase(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load config failed: %v", err)
	}

	strategy := cfg.GetSyncStrategy(env.RepoDir)
	if strategy != "rebase" {
		t.Errorf("Default GetSyncStrategy() = %q, want %q", strategy, "rebase")
	}
}

// TestRebaseChildren_MergeConflict tests merge conflict handling during child sync
func TestRebaseChildren_MergeConflict(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create: main -> parent -> child
	CreateBranch(t, env, "parent-branch", "main")
	CreateBranch(t, env, "child-branch", "parent-branch")

	// Create conflicting changes
	parentWorktree := filepath.Join(env.WorktreeDir, "parent-branch")
	childWorktree := filepath.Join(env.WorktreeDir, "child-branch")
	GitCommit(t, parentWorktree, "shared.txt", "parent version", "Parent change")
	GitCommit(t, childWorktree, "shared.txt", "child version", "Child change")

	mgr, err := stack.NewManager(parentWorktree)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	results, err := mgr.RebaseChildren(true) // useMerge=true
	if err != nil {
		t.Fatalf("RebaseChildren(merge) failed: %v", err)
	}

	// Should have conflict
	foundConflict := false
	for _, r := range results {
		if r.HasConflict {
			foundConflict = true
		}
	}
	if !foundConflict {
		t.Error("Expected merge conflict when syncing children with conflicting changes")
	}

	// Abort merge to clean up
	exec.Command("git", "-C", childWorktree, "merge", "--abort").Run()
}

// TestRebaseChildren_MergeMultipleChildren tests merge with multiple children
func TestRebaseChildren_MergeMultipleChildren(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create: main -> parent -> child-a, parent -> child-b
	CreateBranchWithCommit(t, env, "parent", "main")
	CreateBranchWithCommit(t, env, "child-a", "parent")
	CreateBranchWithCommit(t, env, "child-b", "parent")

	// Add commit to parent
	parentWorktree := filepath.Join(env.WorktreeDir, "parent")
	GitCommit(t, parentWorktree, "parent-new.txt", "new content", "New parent commit")

	mgr, err := stack.NewManager(parentWorktree)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	results, err := mgr.RebaseChildren(true)
	if err != nil {
		t.Fatalf("RebaseChildren(merge) failed: %v", err)
	}

	successCount := 0
	for _, r := range results {
		if r.HasConflict {
			t.Errorf("Unexpected conflict in %s", r.Branch)
		}
		if r.Success {
			successCount++
		}
	}

	if successCount < 2 {
		t.Errorf("Expected at least 2 successful syncs, got %d", successCount)
	}

	// Verify both children have the new file
	for _, child := range []string{"child-a", "child-b"} {
		childWorktree := filepath.Join(env.WorktreeDir, child)
		if _, err := os.Stat(filepath.Join(childWorktree, "parent-new.txt")); os.IsNotExist(err) {
			t.Errorf("parent-new.txt should exist in %s after merge", child)
		}
	}
}
