package itests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
)

// TestNewBranch tests creating a new branch with ezs new
func TestNewBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "feature-a", "main")

	AssertBranchExists(t, env, "feature-a")
	AssertBranchParent(t, env, "feature-a", "main")

	worktreePath := filepath.Join(env.WorktreeDir, "feature-a")
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		t.Error("Worktree directory was not created")
	}
}

// TestNewBranchWithCommit tests creating a branch and adding commits
func TestNewBranchWithCommit(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feature-with-commit", "main")

	AssertCommitsAhead(t, env, "feature-with-commit", "main", 1)
}

// TestNewBranchChain tests creating a chain of stacked branches
func TestNewBranchChain(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create chain: main -> a -> b -> c
	branches := []string{"branch-a", "branch-b", "branch-c"}
	parents := []string{"main", "branch-a", "branch-b"}

	for i, name := range branches {
		CreateBranchWithCommit(t, env, name, parents[i])
	}

	for i, name := range branches {
		AssertBranchExists(t, env, name)
		AssertBranchParent(t, env, name, parents[i])
	}

	mgr, _ := stack.NewManager(env.RepoDir)

	children := mgr.GetChildren("branch-a")
	if len(children) != 1 || children[0].Name != "branch-b" {
		t.Errorf("branch-a should have branch-b as child")
	}

	children = mgr.GetChildren("branch-b")
	if len(children) != 1 || children[0].Name != "branch-c" {
		t.Errorf("branch-b should have branch-c as child")
	}

	children = mgr.GetChildren("branch-c")
	if len(children) != 0 {
		t.Errorf("branch-c should have no children")
	}
}

// TestNewBranchNoCommits tests that a new branch starts with 0 commits ahead
func TestNewBranchNoCommits(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "empty-branch", "main")

	AssertCommitsAhead(t, env, "empty-branch", "main", 0)
}

// TestNewBranchMultipleCommits tests adding multiple commits to a branch
func TestNewBranchMultipleCommits(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "multi-commit", "main")

	worktreePath := filepath.Join(env.WorktreeDir, "multi-commit")
	GitCommitMultiple(t, worktreePath, 3, "feature")

	AssertCommitsAhead(t, env, "multi-commit", "main", 3)
}

// TestNewBranchWorktreePath tests that worktree path is set correctly
func TestNewBranchWorktreePath(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "worktree-test", "main")

	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("worktree-test")

	expectedPath := filepath.Join(env.WorktreeDir, "worktree-test")
	if branch.WorktreePath != expectedPath {
		t.Errorf("WorktreePath = %q, want %q", branch.WorktreePath, expectedPath)
	}
}

// TestValidateWorktreeBaseDirIntegration tests the validation of worktree base dir
func TestValidateWorktreeBaseDirIntegration(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	tests := []struct {
		name            string
		worktreeBaseDir string
		wantErr         bool
	}{
		{
			name:            "valid - configured worktree dir",
			worktreeBaseDir: env.WorktreeDir,
			wantErr:         false,
		},
		{
			name:            "valid - sibling of repo",
			worktreeBaseDir: filepath.Join(filepath.Dir(env.RepoDir), "other-worktrees"),
			wantErr:         false,
		},
		{
			name:            "invalid - inside repo",
			worktreeBaseDir: filepath.Join(env.RepoDir, "worktrees"),
			wantErr:         true,
		},
		{
			name:            "invalid - same as repo",
			worktreeBaseDir: env.RepoDir,
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := commands.ValidateWorktreeBaseDir(tt.worktreeBaseDir, env.RepoDir)
			if tt.wantErr && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

// TestWorktreeBaseDirConfigSaved tests that worktree base dir is saved to config
func TestWorktreeBaseDirConfigSaved(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Verify the config was saved correctly during setup
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	worktreeDir := cfg.GetWorktreeBaseDir(env.RepoDir)
	if worktreeDir != env.WorktreeDir {
		t.Errorf("WorktreeBaseDir = %q, want %q", worktreeDir, env.WorktreeDir)
	}
}

// TestNewBranchWithoutWorktreeConfig tests behavior when worktree config is missing
// Note: This tests the validation logic, not the interactive prompt
func TestNewBranchWithoutWorktreeConfig(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Clear the worktree base dir from config
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	repoCfg := cfg.GetRepoConfig(env.RepoDir)
	if repoCfg != nil {
		repoCfg.WorktreeBaseDir = ""
		cfg.SetRepoConfig(env.RepoDir, repoCfg)
		cfg.Save()
	}

	// Verify config is cleared
	cfg, _ = config.Load()
	if cfg.GetWorktreeBaseDir(env.RepoDir) != "" {
		t.Fatal("WorktreeBaseDir should be empty")
	}

	// Test that validation rejects paths inside the repo
	insideRepo := filepath.Join(env.RepoDir, "worktrees")
	err = commands.ValidateWorktreeBaseDir(insideRepo, env.RepoDir)
	if err == nil {
		t.Error("Expected error for worktree dir inside repo")
	}

	// Test that validation accepts paths outside the repo
	outsideRepo := filepath.Join(filepath.Dir(env.RepoDir), "worktrees")
	err = commands.ValidateWorktreeBaseDir(outsideRepo, env.RepoDir)
	if err != nil {
		t.Errorf("Unexpected error for worktree dir outside repo: %v", err)
	}
}

// TestCreateWorktreeOnly tests creating a worktree without adding to a stack
func TestCreateWorktreeOnly(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	mgr, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Create a worktree without adding to stack
	err = mgr.CreateWorktreeOnly("standalone-branch", "main", "")
	if err != nil {
		t.Fatalf("CreateWorktreeOnly failed: %v", err)
	}

	// Verify worktree was created
	worktreePath := filepath.Join(env.WorktreeDir, "standalone-branch")
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
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

// TestCreateWorktreeOnly_ThenAddToStack tests creating a standalone worktree then adding it to a stack
func TestCreateWorktreeOnly_ThenAddToStack(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	mgr, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Create a worktree without adding to stack
	worktreePath := filepath.Join(env.WorktreeDir, "standalone-then-stack")
	err = mgr.CreateWorktreeOnly("standalone-then-stack", "main", worktreePath)
	if err != nil {
		t.Fatalf("CreateWorktreeOnly failed: %v", err)
	}

	// Verify not in stack
	if mgr.GetBranch("standalone-then-stack") != nil {
		t.Error("Branch should not be in stack initially")
	}

	// Now add it to a stack using AddWorktreeToStack
	branch, err := mgr.AddWorktreeToStack("standalone-then-stack", worktreePath, "main")
	if err != nil {
		t.Fatalf("AddWorktreeToStack failed: %v", err)
	}

	if branch.Name != "standalone-then-stack" {
		t.Errorf("branch.Name = %q, want %q", branch.Name, "standalone-then-stack")
	}

	// Verify now in stack
	mgr, _ = stack.NewManager(env.RepoDir)
	if mgr.GetBranch("standalone-then-stack") == nil {
		t.Error("Branch should now be in stack")
	}
}
