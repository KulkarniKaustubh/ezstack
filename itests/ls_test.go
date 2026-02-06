package itests

import (
	"testing"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
)

// TestListStacks_Empty tests listing stacks when none exist
func TestListStacks_Empty(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	mgr, _ := stack.NewManager(env.RepoDir)
	stacks := mgr.ListStacks()

	if len(stacks) != 0 {
		t.Errorf("Expected 0 stacks, got %d", len(stacks))
	}
}

// TestListStacks_Single tests listing a single stack
func TestListStacks_Single(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "feature-a", "main")

	mgr, _ := stack.NewManager(env.RepoDir)
	stacks := mgr.ListStacks()

	if len(stacks) != 1 {
		t.Errorf("Expected 1 stack, got %d", len(stacks))
	}
}

// TestListStacks_Multiple tests listing multiple stacks
func TestListStacks_Multiple(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "stack1-a", "main")
	CreateBranch(t, env, "stack1-b", "stack1-a")

	mgr, _ := stack.NewManager(env.RepoDir)
	stacks := mgr.ListStacks()

	if len(stacks) < 1 {
		t.Errorf("Expected at least 1 stack, got %d", len(stacks))
	}
}

// TestListStacks_WithBranches tests that stacks contain the correct branches
func TestListStacks_WithBranches(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "feature-a", "main")
	CreateBranch(t, env, "feature-b", "feature-a")
	CreateBranch(t, env, "feature-c", "feature-b")

	mgr, _ := stack.NewManager(env.RepoDir)
	stacks := mgr.ListStacks()

	if len(stacks) == 0 {
		t.Fatal("Expected at least 1 stack")
	}

	var foundStack *config.Stack
	for _, s := range stacks {
		for _, b := range s.Branches {
			if b.Name == "feature-a" {
				foundStack = s
				break
			}
		}
	}

	if foundStack == nil {
		t.Fatal("Could not find stack containing feature-a")
	}

	branchNames := make(map[string]bool)
	for _, b := range foundStack.Branches {
		branchNames[b.Name] = true
	}

	for _, name := range []string{"feature-a", "feature-b", "feature-c"} {
		if !branchNames[name] {
			t.Errorf("Branch %s not found in stack", name)
		}
	}
}

// TestGetBranch tests getting a specific branch
func TestGetBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "test-branch", "main")

	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("test-branch")

	if branch == nil {
		t.Fatal("GetBranch returned nil")
	}

	if branch.Name != "test-branch" {
		t.Errorf("branch.Name = %q, want %q", branch.Name, "test-branch")
	}

	if branch.Parent != "main" {
		t.Errorf("branch.Parent = %q, want %q", branch.Parent, "main")
	}
}

// TestGetBranch_Nonexistent tests getting a branch that doesn't exist
func TestGetBranch_Nonexistent(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("nonexistent")

	if branch != nil {
		t.Error("Expected nil for nonexistent branch")
	}
}

// TestGetChildren tests getting children of a branch
func TestGetChildren(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "parent", "main")
	CreateBranch(t, env, "child1", "parent")
	CreateBranch(t, env, "child2", "parent")

	mgr, _ := stack.NewManager(env.RepoDir)
	children := mgr.GetChildren("parent")

	if len(children) != 2 {
		t.Errorf("Expected 2 children, got %d", len(children))
	}

	childNames := make(map[string]bool)
	for _, c := range children {
		childNames[c.Name] = true
	}

	if !childNames["child1"] || !childNames["child2"] {
		t.Error("Expected child1 and child2 in children")
	}
}

// TestGetCurrentStack_NotInStack tests GetCurrentStack when on a branch not in any stack
func TestGetCurrentStack_NotInStack(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// We're on main, which is not part of any stack
	mgr, _ := stack.NewManager(env.RepoDir)
	_, _, err := mgr.GetCurrentStack()

	if err == nil {
		t.Error("GetCurrentStack() should return error when not in any stack")
	}
}

// TestGetCurrentStack_InStack tests GetCurrentStack when on a branch in a stack
func TestGetCurrentStack_InStack(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "feature-a", "main")

	// Change to the worktree
	g := NewGitInWorktree(env, "feature-a")
	currentBranch, _ := g.CurrentBranch()

	if currentBranch != "feature-a" {
		t.Skipf("Not in feature-a worktree, skipping (current: %s)", currentBranch)
	}

	mgr, _ := stack.NewManager(env.WorktreeDir + "/feature-a")
	s, branch, err := mgr.GetCurrentStack()

	if err != nil {
		t.Fatalf("GetCurrentStack() error = %v", err)
	}

	if s == nil {
		t.Error("Expected stack to be returned")
	}

	if branch == nil || branch.Name != "feature-a" {
		t.Error("Expected branch to be feature-a")
	}
}
