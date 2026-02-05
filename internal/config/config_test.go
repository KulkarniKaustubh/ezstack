package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDir(t *testing.T) {
	// Test with EZSTACK_HOME set
	t.Run("with EZSTACK_HOME", func(t *testing.T) {
		originalHome := os.Getenv("EZSTACK_HOME")
		defer os.Setenv("EZSTACK_HOME", originalHome)

		os.Setenv("EZSTACK_HOME", "/custom/ezstack/home")
		dir, err := ConfigDir()
		if err != nil {
			t.Fatalf("ConfigDir() error = %v", err)
		}
		if dir != "/custom/ezstack/home" {
			t.Errorf("ConfigDir() = %q, want %q", dir, "/custom/ezstack/home")
		}
	})

	// Test without EZSTACK_HOME
	t.Run("without EZSTACK_HOME", func(t *testing.T) {
		originalHome := os.Getenv("EZSTACK_HOME")
		defer os.Setenv("EZSTACK_HOME", originalHome)

		os.Unsetenv("EZSTACK_HOME")
		dir, err := ConfigDir()
		if err != nil {
			t.Fatalf("ConfigDir() error = %v", err)
		}

		homeDir, _ := os.UserHomeDir()
		expected := filepath.Join(homeDir, ".ezstack")
		if dir != expected {
			t.Errorf("ConfigDir() = %q, want %q", dir, expected)
		}
	})
}

func TestConfig_GetBaseBranch(t *testing.T) {
	tests := []struct {
		name       string
		config     *Config
		repoPath   string
		wantBranch string
	}{
		{
			name: "repo-specific override",
			config: &Config{
				DefaultBaseBranch: "main",
				Repos: map[string]*RepoConfig{
					"/path/to/repo": {DefaultBaseBranch: "develop"},
				},
			},
			repoPath:   "/path/to/repo",
			wantBranch: "develop",
		},
		{
			name: "global default",
			config: &Config{
				DefaultBaseBranch: "trunk",
				Repos:             make(map[string]*RepoConfig),
			},
			repoPath:   "/any/repo",
			wantBranch: "trunk",
		},
		{
			name: "fallback to main",
			config: &Config{
				Repos: make(map[string]*RepoConfig),
			},
			repoPath:   "/any/repo",
			wantBranch: "main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.GetBaseBranch(tt.repoPath)
			if got != tt.wantBranch {
				t.Errorf("GetBaseBranch() = %q, want %q", got, tt.wantBranch)
			}
		})
	}
}

func TestConfig_GetWorktreeBaseDir(t *testing.T) {
	config := &Config{
		Repos: map[string]*RepoConfig{
			"/path/to/repo": {WorktreeBaseDir: "/worktrees/repo"},
		},
	}

	// Repo with config
	got := config.GetWorktreeBaseDir("/path/to/repo")
	if got != "/worktrees/repo" {
		t.Errorf("GetWorktreeBaseDir() = %q, want %q", got, "/worktrees/repo")
	}

	// Repo without config
	got = config.GetWorktreeBaseDir("/other/repo")
	if got != "" {
		t.Errorf("GetWorktreeBaseDir() = %q, want empty string", got)
	}
}

func TestConfig_GetCdAfterNew(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name     string
		config   *Config
		repoPath string
		want     bool
	}{
		{
			name: "explicit true",
			config: &Config{
				Repos: map[string]*RepoConfig{
					"/repo": {CdAfterNew: &trueVal},
				},
			},
			repoPath: "/repo",
			want:     true,
		},
		{
			name: "explicit false",
			config: &Config{
				Repos: map[string]*RepoConfig{
					"/repo": {CdAfterNew: &falseVal},
				},
			},
			repoPath: "/repo",
			want:     false,
		},
		{
			name: "nil defaults to false",
			config: &Config{
				Repos: map[string]*RepoConfig{
					"/repo": {},
				},
			},
			repoPath: "/repo",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.GetCdAfterNew(tt.repoPath)
			if got != tt.want {
				t.Errorf("GetCdAfterNew() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_SetRepoConfig(t *testing.T) {
	config := &Config{}

	repoCfg := &RepoConfig{
		WorktreeBaseDir: "/worktrees",
	}

	config.SetRepoConfig("/my/repo", repoCfg)

	if config.Repos == nil {
		t.Fatal("Repos map should be initialized")
	}

	got := config.GetRepoConfig("/my/repo")
	if got == nil {
		t.Fatal("GetRepoConfig returned nil")
	}

	if got.WorktreeBaseDir != "/worktrees" {
		t.Errorf("WorktreeBaseDir = %q, want %q", got.WorktreeBaseDir, "/worktrees")
	}

	if got.RepoPath != "/my/repo" {
		t.Errorf("RepoPath = %q, want %q", got.RepoPath, "/my/repo")
	}
}

func TestConfig_LoadSave(t *testing.T) {
	// Create a temp directory for config
	tmpDir, err := os.MkdirTemp("", "config-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Set EZSTACK_HOME to temp dir
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Create and save config
	config := &Config{
		DefaultBaseBranch: "develop",
		Repos: map[string]*RepoConfig{
			"/test/repo": {
				WorktreeBaseDir:   "/test/worktrees",
				DefaultBaseBranch: "trunk",
			},
		},
	}

	err = config.Save()
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Load config
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded.DefaultBaseBranch != "develop" {
		t.Errorf("DefaultBaseBranch = %q, want %q", loaded.DefaultBaseBranch, "develop")
	}

	repoCfg := loaded.GetRepoConfig("/test/repo")
	if repoCfg == nil {
		t.Fatal("GetRepoConfig returned nil")
	}

	if repoCfg.WorktreeBaseDir != "/test/worktrees" {
		t.Errorf("WorktreeBaseDir = %q, want %q", repoCfg.WorktreeBaseDir, "/test/worktrees")
	}
}

func TestStackConfig_LoadSave(t *testing.T) {
	// Create a temp directory for config
	tmpDir, err := os.MkdirTemp("", "stack-config-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Set EZSTACK_HOME to temp dir
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	repoDir := "/test/repo"

	// Create stack config
	stackCfg, err := LoadStackConfig(repoDir)
	if err != nil {
		t.Fatalf("LoadStackConfig() error = %v", err)
	}

	// Add a stack using the new tree format
	stack := &Stack{
		Name: "feature-a",
		Root: "main",
		Tree: BranchTree{
			"feature-a": BranchTree{
				"feature-b": BranchTree{},
			},
		},
	}
	stackCfg.Stacks["feature-a"] = stack

	// Create cache with metadata
	cache := &CacheConfig{
		Branches: map[string]*BranchCache{
			"feature-a": {
				WorktreePath: "/worktrees/feature-a",
				PRNumber:     1,
				PRUrl:        "https://github.com/org/repo/pull/1",
			},
			"feature-b": {
				WorktreePath: "/worktrees/feature-b",
				PRNumber:     2,
			},
		},
		repoDir: repoDir,
	}
	err = cache.Save(repoDir)
	if err != nil {
		t.Fatalf("Cache.Save() error = %v", err)
	}

	// Populate branches from tree
	stack.cache = cache
	stack.PopulateBranches()

	// Save
	err = stackCfg.Save(repoDir)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Load again
	loaded, err := LoadStackConfig(repoDir)
	if err != nil {
		t.Fatalf("LoadStackConfig() error = %v", err)
	}

	stack, ok := loaded.Stacks["feature-a"]
	if !ok {
		t.Fatal("Stack 'feature-a' not found")
	}

	if len(stack.Branches) != 2 {
		t.Errorf("len(Branches) = %d, want 2", len(stack.Branches))
	}

	if stack.Branches[0].PRNumber != 1 {
		t.Errorf("PRNumber = %d, want 1", stack.Branches[0].PRNumber)
	}

	if stack.Branches[1].Name != "feature-b" {
		t.Errorf("Branch name = %q, want %q", stack.Branches[1].Name, "feature-b")
	}
}

func TestStackConfig_MultiRepo(t *testing.T) {
	// Test that stack configs are isolated per repo
	tmpDir, err := os.MkdirTemp("", "stack-multi-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Create stack for repo1
	repo1Cfg, _ := LoadStackConfig("/repo1")
	repo1Cfg.Stacks["stack1"] = &Stack{Name: "stack1"}
	repo1Cfg.Save("/repo1")

	// Create stack for repo2
	repo2Cfg, _ := LoadStackConfig("/repo2")
	repo2Cfg.Stacks["stack2"] = &Stack{Name: "stack2"}
	repo2Cfg.Save("/repo2")

	// Verify isolation
	repo1Loaded, _ := LoadStackConfig("/repo1")
	if _, ok := repo1Loaded.Stacks["stack2"]; ok {
		t.Error("repo1 should not have stack2")
	}
	if _, ok := repo1Loaded.Stacks["stack1"]; !ok {
		t.Error("repo1 should have stack1")
	}

	repo2Loaded, _ := LoadStackConfig("/repo2")
	if _, ok := repo2Loaded.Stacks["stack1"]; ok {
		t.Error("repo2 should not have stack1")
	}
	if _, ok := repo2Loaded.Stacks["stack2"]; !ok {
		t.Error("repo2 should have stack2")
	}
}

func TestBranch_Fields(t *testing.T) {
	branch := &Branch{
		Name:         "feature",
		Parent:       "main",
		WorktreePath: "/path/to/worktree",
		PRNumber:     42,
		PRUrl:        "https://github.com/org/repo/pull/42",
		BaseBranch:   "main",
		IsRemote:     true,
		IsMerged:     false,
	}

	if branch.Name != "feature" {
		t.Errorf("Name = %q, want %q", branch.Name, "feature")
	}
	if branch.PRNumber != 42 {
		t.Errorf("PRNumber = %d, want 42", branch.PRNumber)
	}
	if !branch.IsRemote {
		t.Error("IsRemote should be true")
	}
	if branch.IsMerged {
		t.Error("IsMerged should be false")
	}
}
