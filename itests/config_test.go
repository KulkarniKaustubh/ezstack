package itests

import (
	"testing"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
)

// TestConfigBaseBranch tests getting the base branch from config
func TestConfigBaseBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	baseBranch := cfg.GetBaseBranch(env.RepoDir)
	if baseBranch != "main" {
		t.Errorf("BaseBranch = %q, want %q", baseBranch, "main")
	}
}

// TestConfigWorktreeBaseDir tests getting the worktree base dir from config
func TestConfigWorktreeBaseDir(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	worktreeDir := cfg.GetWorktreeBaseDir(env.RepoDir)
	if worktreeDir != env.WorktreeDir {
		t.Errorf("WorktreeBaseDir = %q, want %q", worktreeDir, env.WorktreeDir)
	}
}

// TestConfigRepoIsolation tests that configs are isolated per repo
func TestConfigRepoIsolation(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	cfg, _ := config.Load()

	repoCfg := cfg.GetRepoConfig(env.RepoDir)
	if repoCfg == nil {
		t.Fatal("Repo config not found")
	}

	otherRepoCfg := cfg.GetRepoConfig("/some/other/repo")
	if otherRepoCfg != nil {
		t.Error("Other repo should not have config")
	}
}

// TestStackConfigPersistence tests that stack config persists across manager instances
func TestStackConfigPersistence(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "persist-test", "main")

	mgr, _ := stack.NewManager(env.RepoDir)
	branch := mgr.GetBranch("persist-test")
	if branch == nil {
		t.Error("Branch should persist across manager instances")
	}
}

// TestStackConfigMultipleBranches tests stack config with multiple branches
func TestStackConfigMultipleBranches(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	branches := []string{"branch-a", "branch-b", "branch-c"}
	for _, name := range branches {
		CreateBranch(t, env, name, "main")
	}

	mgr, _ := stack.NewManager(env.RepoDir)
	for _, name := range branches {
		if mgr.GetBranch(name) == nil {
			t.Errorf("Branch %s not found", name)
		}
	}
}

// TestIsMainBranch tests the main branch detection
func TestIsMainBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	mgr, _ := stack.NewManager(env.RepoDir)

	tests := []struct {
		branch   string
		expected bool
	}{
		{"main", true},
		{"master", true},
		{"feature", false},
		{"develop", false},
	}

	for _, tt := range tests {
		got := mgr.IsMainBranch(tt.branch)
		if got != tt.expected {
			t.Errorf("IsMainBranch(%q) = %v, want %v", tt.branch, got, tt.expected)
		}
	}
}

// TestRegisterExistingBranch tests registering an existing branch
func TestRegisterExistingBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	mgr, _ := stack.NewManager(env.RepoDir)

	branch, err := mgr.RegisterExistingBranch("existing", env.WorktreeDir+"/existing", "main")
	if err != nil {
		t.Fatalf("RegisterExistingBranch failed: %v", err)
	}

	if branch.Name != "existing" {
		t.Errorf("Name = %q, want %q", branch.Name, "existing")
	}
}

// TestRegisterExistingBranchDuplicate tests registering a duplicate branch
func TestRegisterExistingBranchDuplicate(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	mgr, _ := stack.NewManager(env.RepoDir)

	mgr.RegisterExistingBranch("dup", env.WorktreeDir+"/dup", "main")

	_, err := mgr.RegisterExistingBranch("dup", env.WorktreeDir+"/dup2", "main")
	if err == nil {
		t.Error("Expected error when registering duplicate branch")
	}
}
