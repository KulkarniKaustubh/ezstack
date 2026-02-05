package itests

import (
	"path/filepath"
	"testing"

	"github.com/ezstack/ezstack/internal/git"
	"github.com/ezstack/ezstack/internal/stack"
)

// TestGotoBranch tests getting worktree path for a branch
func TestGotoBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "goto-test", "main")

	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("goto-test")

	if branch == nil {
		t.Fatal("Branch not found")
	}

	expectedPath := filepath.Join(env.WorktreeDir, "goto-test")
	if branch.WorktreePath != expectedPath {
		t.Errorf("WorktreePath = %q, want %q", branch.WorktreePath, expectedPath)
	}
}

// TestGotoNonexistentBranch tests goto for a branch that doesn't exist
func TestGotoNonexistentBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("nonexistent")

	if branch != nil {
		t.Error("Expected nil for nonexistent branch")
	}
}

// TestGotoMergedBranch tests that merged branches cannot be navigated to
func TestGotoMergedBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "to-merge", "main")

	mgr, _ := stack.NewManager(env.RepoDir)
	if err := mgr.MarkBranchMerged("to-merge"); err != nil {
		t.Fatalf("MarkBranchMerged failed: %v", err)
	}

	mgr, _ = stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("to-merge")

	if branch == nil {
		t.Fatal("Branch should still exist in config")
	}

	if !branch.IsMerged {
		t.Error("Branch should be marked as merged")
	}

	if branch.WorktreePath != "" {
		t.Error("WorktreePath should be empty for merged branch")
	}
}

// TestGotoRemoteBranch tests that remote branches have no worktree
func TestGotoRemoteBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	mgr, _ := stack.NewManager(env.RepoDir)

	branch, err := mgr.RegisterRemoteBranch("remote-feature", "main", 100, "https://github.com/org/repo/pull/100")
	if err != nil {
		t.Fatalf("RegisterRemoteBranch failed: %v", err)
	}

	if !branch.IsRemote {
		t.Error("IsRemote should be true")
	}

	if branch.WorktreePath != "" {
		t.Error("Remote branch should not have worktree path")
	}
}

// TestGotoAllBranches tests listing all branches for goto selection
func TestGotoAllBranches(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	branches := []string{"goto-a", "goto-b", "goto-c"}
	for _, name := range branches {
		CreateBranch(t, env, name, "main")
	}

	mgr, _ := stack.NewManager(env.RepoDir)
	stacks := mgr.ListStacks()

	totalBranches := 0
	for _, s := range stacks {
		totalBranches += len(s.Branches)
	}

	if totalBranches != 3 {
		t.Errorf("Expected 3 branches, got %d", totalBranches)
	}
}

// TestGotoExcludesMergedFromList tests that merged branches are excluded from goto list
func TestGotoExcludesMergedFromList(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "active-branch", "main")
	CreateBranch(t, env, "merged-branch", "main")

	mgr, _ := stack.NewManager(env.RepoDir)
	mgr.MarkBranchMerged("merged-branch")

	mgr, _ = stack.NewManager(env.RepoDir)
	stacks := mgr.ListStacks()

	activeBranches := 0
	for _, s := range stacks {
		for _, b := range s.Branches {
			if !b.IsMerged {
				activeBranches++
			}
		}
	}

	if activeBranches != 1 {
		t.Errorf("Expected 1 active branch, got %d", activeBranches)
	}
}

// TestGotoUnstackedWorktree tests that goto can find unstacked worktrees via git
func TestGotoUnstackedWorktree(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	mgr, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Create a worktree without adding to stack
	worktreePath := filepath.Join(env.WorktreeDir, "unstacked-goto")
	err = mgr.CreateWorktreeOnly("unstacked-goto", "main", worktreePath)
	if err != nil {
		t.Fatalf("CreateWorktreeOnly failed: %v", err)
	}

	// Verify it's not in any stack
	if mgr.GetBranch("unstacked-goto") != nil {
		t.Error("Branch should not be in any stack")
	}

	// But it should be findable via git worktree list
	g := git.New(env.RepoDir)
	worktrees, err := g.ListWorktrees()
	if err != nil {
		t.Fatalf("ListWorktrees failed: %v", err)
	}

	found := false
	for _, wt := range worktrees {
		if wt.Branch == "unstacked-goto" {
			found = true
			if wt.Path != worktreePath {
				t.Errorf("Worktree path = %q, want %q", wt.Path, worktreePath)
			}
			break
		}
	}

	if !found {
		t.Error("Unstacked worktree should be found via git worktree list")
	}
}

// TestGotoMixedStackedAndUnstacked tests goto with both stacked and unstacked worktrees
func TestGotoMixedStackedAndUnstacked(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	mgr, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Create a stacked branch
	CreateBranch(t, env, "stacked-branch", "main")

	// Create an unstacked worktree
	unstackedPath := filepath.Join(env.WorktreeDir, "unstacked-branch")
	err = mgr.CreateWorktreeOnly("unstacked-branch", "main", unstackedPath)
	if err != nil {
		t.Fatalf("CreateWorktreeOnly failed: %v", err)
	}

	// Verify stacked branch is in stack
	mgr, _ = stack.NewManager(env.RepoDir)
	if mgr.GetBranch("stacked-branch") == nil {
		t.Error("stacked-branch should be in a stack")
	}

	// Verify unstacked branch is NOT in stack
	if mgr.GetBranch("unstacked-branch") != nil {
		t.Error("unstacked-branch should NOT be in a stack")
	}

	// Both should be findable via git worktree list
	g := git.New(env.RepoDir)
	worktrees, err := g.ListWorktrees()
	if err != nil {
		t.Fatalf("ListWorktrees failed: %v", err)
	}

	foundStacked := false
	foundUnstacked := false
	for _, wt := range worktrees {
		if wt.Branch == "stacked-branch" {
			foundStacked = true
		}
		if wt.Branch == "unstacked-branch" {
			foundUnstacked = true
		}
	}

	if !foundStacked {
		t.Error("stacked-branch should be found via git worktree list")
	}
	if !foundUnstacked {
		t.Error("unstacked-branch should be found via git worktree list")
	}
}

// TestGotoAllWorktreesIncludesMain tests that ListWorktrees includes the main worktree
func TestGotoAllWorktreesIncludesMain(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	g := git.New(env.RepoDir)
	worktrees, err := g.ListWorktrees()
	if err != nil {
		t.Fatalf("ListWorktrees failed: %v", err)
	}

	// Should have at least the main worktree
	if len(worktrees) < 1 {
		t.Error("Expected at least 1 worktree (main)")
	}

	// Main worktree should be the repo dir
	foundMain := false
	for _, wt := range worktrees {
		if wt.Path == env.RepoDir {
			foundMain = true
			break
		}
	}

	if !foundMain {
		t.Errorf("Main worktree not found. Worktrees: %v", worktrees)
	}
}
