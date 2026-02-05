package itests

import (
	"testing"

	"github.com/ezstack/ezstack/internal/config"
	"github.com/ezstack/ezstack/internal/stack"
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
