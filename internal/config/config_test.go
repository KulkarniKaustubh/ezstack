package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
			name: "nil defaults to true",
			config: &Config{
				Repos: map[string]*RepoConfig{
					"/repo": {},
				},
			},
			repoPath: "/repo",
			want:     true,
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

func TestConfig_GetInitSubmodules(t *testing.T) {
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
					"/repo": {InitSubmodules: &trueVal},
				},
			},
			repoPath: "/repo",
			want:     true,
		},
		{
			name: "explicit false",
			config: &Config{
				Repos: map[string]*RepoConfig{
					"/repo": {InitSubmodules: &falseVal},
				},
			},
			repoPath: "/repo",
			want:     false,
		},
		{
			name: "nil defaults to true",
			config: &Config{
				Repos: map[string]*RepoConfig{
					"/repo": {},
				},
			},
			repoPath: "/repo",
			want:     true,
		},
		{
			name:     "missing repo defaults to true",
			config:   &Config{Repos: map[string]*RepoConfig{}},
			repoPath: "/missing",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.GetInitSubmodules(tt.repoPath)
			if got != tt.want {
				t.Errorf("GetInitSubmodules() = %v, want %v", got, tt.want)
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

// TestStackConfig_ConcurrentSave_NoLostUpdates exercises the flock that
// serializes load-modify-save sequences. Without the lock, two goroutines
// saving disjoint stacks could interleave read/modify/write and one
// would silently overwrite the other.
func TestStackConfig_ConcurrentSave_NoLostUpdates(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "stack-concurrent-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	const repoA = "/test/repoA"
	const repoB = "/test/repoB"

	// Seed the shared stacks.json by saving an empty config for each repo.
	for _, r := range []string{repoA, repoB} {
		sc, err := LoadStackConfig(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := sc.Save(r); err != nil {
			t.Fatal(err)
		}
	}

	// Two goroutines repeatedly add disjoint stacks to their respective
	// repos' section of the same shared stacks.json.
	const iters = 30
	done := make(chan error, 2)
	worker := func(repo, prefix string) {
		for i := 0; i < iters; i++ {
			sc, err := LoadStackConfig(repo)
			if err != nil {
				done <- err
				return
			}
			name := prefix + "-" + string(rune('a'+i))
			hash := GenerateStackHash(name)
			sc.Stacks[hash] = &Stack{
				Hash: hash,
				Root: "main",
				Tree: BranchTree{name: BranchTree{}},
			}
			if err := sc.Save(repo); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}
	go worker(repoA, "a")
	go worker(repoB, "b")
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("worker error: %v", err)
		}
	}

	// Both repos must have kept all their iterations. Without the lock,
	// late writers could overwrite earlier writers' additions to the other
	// repo's section.
	scA, _ := LoadStackConfig(repoA)
	scB, _ := LoadStackConfig(repoB)
	if len(scA.Stacks) != iters {
		t.Errorf("repoA has %d stacks, want %d — concurrent save lost updates", len(scA.Stacks), iters)
	}
	if len(scB.Stacks) != iters {
		t.Errorf("repoB has %d stacks, want %d — concurrent save lost updates", len(scB.Stacks), iters)
	}
}

// TestStackConfig_ConcurrentSave_SameRepo_NoLostUpdates covers the harder
// race that the existing flock alone could not prevent: two processes
// touching *different stacks of the same repo*. Before the three-way merge
// in Save(), the second writer would replace the first writer's stack
// updates because the in-memory snapshot was stale.
func TestStackConfig_ConcurrentSave_SameRepo_NoLostUpdates(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "stack-same-repo-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	const repo = "/test/sharedRepo"

	// Two writers add disjoint stacks to the same repo's section. With the
	// pre-merge code, late writers' Save() would clobber early writers' new
	// stacks because the in-memory map didn't include them.
	const iters = 20
	done := make(chan error, 2)
	worker := func(prefix string) {
		for i := 0; i < iters; i++ {
			sc, err := LoadStackConfig(repo)
			if err != nil {
				done <- err
				return
			}
			name := prefix + "-" + string(rune('a'+i))
			hash := GenerateStackHash(name)
			sc.Stacks[hash] = &Stack{
				Hash: hash,
				Root: "main",
				Tree: BranchTree{name: BranchTree{}},
			}
			if err := sc.Save(repo); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}
	go worker("alpha")
	go worker("beta")
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("worker error: %v", err)
		}
	}

	final, _ := LoadStackConfig(repo)
	if len(final.Stacks) != iters*2 {
		t.Errorf("repo has %d stacks, want %d — concurrent same-repo save lost updates", len(final.Stacks), iters*2)
	}
}

// TestMergeRepoData_ThreeWayMerge spot-checks the merge logic in isolation
// since same-repo concurrency relies on it being correct for additions,
// modifications, deletions, and "we didn't touch it" cases.
func TestMergeRepoData_ThreeWayMerge(t *testing.T) {
	mkStack := func(hash, root string) *Stack {
		return &Stack{Hash: hash, Root: root, Tree: BranchTree{}}
	}

	t.Run("we add a stack — keep it", func(t *testing.T) {
		orig := &repoData{Stacks: map[string]*Stack{}, Branches: map[string]*BranchCache{}}
		mine := &repoData{Stacks: map[string]*Stack{"aaa": mkStack("aaa", "main")}, Branches: map[string]*BranchCache{}}
		theirs := &repoData{Stacks: map[string]*Stack{}, Branches: map[string]*BranchCache{}}
		got := mergeRepoData(orig, mine, theirs)
		if _, ok := got.Stacks["aaa"]; !ok {
			t.Fatal("our addition was dropped")
		}
	})

	t.Run("they added a stack we never saw — keep theirs", func(t *testing.T) {
		orig := &repoData{Stacks: map[string]*Stack{}, Branches: map[string]*BranchCache{}}
		mine := &repoData{Stacks: map[string]*Stack{}, Branches: map[string]*BranchCache{}}
		theirs := &repoData{Stacks: map[string]*Stack{"bbb": mkStack("bbb", "main")}, Branches: map[string]*BranchCache{}}
		got := mergeRepoData(orig, mine, theirs)
		if _, ok := got.Stacks["bbb"]; !ok {
			t.Fatal("their addition was overwritten by our empty state")
		}
	})

	t.Run("we modified, they unchanged — keep ours", func(t *testing.T) {
		orig := &repoData{Stacks: map[string]*Stack{"x": mkStack("x", "main")}, Branches: map[string]*BranchCache{}}
		modified := mkStack("x", "develop")
		mine := &repoData{Stacks: map[string]*Stack{"x": modified}, Branches: map[string]*BranchCache{}}
		theirs := &repoData{Stacks: map[string]*Stack{"x": mkStack("x", "main")}, Branches: map[string]*BranchCache{}}
		got := mergeRepoData(orig, mine, theirs)
		if got.Stacks["x"].Root != "develop" {
			t.Fatalf("our modification was lost; got root=%q", got.Stacks["x"].Root)
		}
	})

	t.Run("we unchanged, they modified — keep theirs", func(t *testing.T) {
		orig := &repoData{Stacks: map[string]*Stack{"x": mkStack("x", "main")}, Branches: map[string]*BranchCache{}}
		mine := &repoData{Stacks: map[string]*Stack{"x": mkStack("x", "main")}, Branches: map[string]*BranchCache{}}
		theirs := &repoData{Stacks: map[string]*Stack{"x": mkStack("x", "develop")}, Branches: map[string]*BranchCache{}}
		got := mergeRepoData(orig, mine, theirs)
		if got.Stacks["x"].Root != "develop" {
			t.Fatalf("theirs modification was clobbered by our untouched copy; got root=%q", got.Stacks["x"].Root)
		}
	})

	t.Run("we deleted, they unchanged — stays deleted", func(t *testing.T) {
		orig := &repoData{Stacks: map[string]*Stack{"x": mkStack("x", "main")}, Branches: map[string]*BranchCache{}}
		mine := &repoData{Stacks: map[string]*Stack{}, Branches: map[string]*BranchCache{}}
		theirs := &repoData{Stacks: map[string]*Stack{"x": mkStack("x", "main")}, Branches: map[string]*BranchCache{}}
		got := mergeRepoData(orig, mine, theirs)
		if _, ok := got.Stacks["x"]; ok {
			t.Fatal("our deletion was reverted by their unchanged copy")
		}
	})

	t.Run("nil orig is treated as empty (fresh config)", func(t *testing.T) {
		mine := &repoData{Stacks: map[string]*Stack{"x": mkStack("x", "main")}, Branches: map[string]*BranchCache{}}
		theirs := &repoData{Stacks: map[string]*Stack{"y": mkStack("y", "main")}, Branches: map[string]*BranchCache{}}
		got := mergeRepoData(nil, mine, theirs)
		if _, ok := got.Stacks["x"]; !ok {
			t.Fatal("our addition was lost when orig was nil")
		}
		if _, ok := got.Stacks["y"]; !ok {
			t.Fatal("their addition was lost when orig was nil")
		}
	})
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
	hash := GenerateStackHash("feature-a")
	stack := &Stack{
		Hash: hash,
		Root: "main",
		Tree: BranchTree{
			"feature-a": BranchTree{
				"feature-b": BranchTree{},
			},
		},
	}
	stackCfg.Stacks[hash] = stack

	// Set up cache with metadata on the stack config
	stackCfg.Cache = &CacheConfig{
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

	// Populate branches from tree
	stack.cache = stackCfg.Cache
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

	stack, ok := loaded.Stacks[hash]
	if !ok {
		t.Fatalf("Stack with hash '%s' not found", hash)
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
	stack1Hash := GenerateStackHash("stack1")
	repo1Cfg.Stacks[stack1Hash] = &Stack{Hash: stack1Hash, Root: "main", Tree: BranchTree{}}
	repo1Cfg.Save("/repo1")

	// Create stack for repo2
	repo2Cfg, _ := LoadStackConfig("/repo2")
	stack2Hash := GenerateStackHash("stack2")
	repo2Cfg.Stacks[stack2Hash] = &Stack{Hash: stack2Hash, Root: "main", Tree: BranchTree{}}
	repo2Cfg.Save("/repo2")

	// Verify isolation
	repo1Loaded, _ := LoadStackConfig("/repo1")
	if _, ok := repo1Loaded.Stacks[stack2Hash]; ok {
		t.Error("repo1 should not have stack2")
	}
	if _, ok := repo1Loaded.Stacks[stack1Hash]; !ok {
		t.Error("repo1 should have stack1")
	}

	repo2Loaded, _ := LoadStackConfig("/repo2")
	if _, ok := repo2Loaded.Stacks[stack1Hash]; ok {
		t.Error("repo2 should not have stack1")
	}
	if _, ok := repo2Loaded.Stacks[stack2Hash]; !ok {
		t.Error("repo2 should have stack2")
	}
}

func TestStack_DisplayName(t *testing.T) {
	tests := []struct {
		name  string
		stack Stack
		want  string
	}{
		{
			name:  "with name",
			stack: Stack{Hash: "a1b2c3d", Name: "my-feature"},
			want:  "my-feature [a1b2c3d]",
		},
		{
			name:  "without name",
			stack: Stack{Hash: "a1b2c3d"},
			want:  "a1b2c3d",
		},
		{
			name:  "empty name",
			stack: Stack{Hash: "x1y2z3w", Name: ""},
			want:  "x1y2z3w",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.stack.DisplayName()
			if got != tt.want {
				t.Errorf("DisplayName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStackConfig_NamePersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "stack-name-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	repoDir := "/test/repo"

	// Create stack with a name
	stackCfg, err := LoadStackConfig(repoDir)
	if err != nil {
		t.Fatalf("LoadStackConfig() error = %v", err)
	}

	hash := GenerateStackHash("test-stack")
	s := &Stack{
		Hash: hash,
		Name: "my-feature",
		Root: "main",
		Tree: BranchTree{
			"feature-a": BranchTree{},
		},
	}
	stackCfg.Stacks[hash] = s
	stackCfg.Cache = &CacheConfig{
		Branches: map[string]*BranchCache{
			"feature-a": {WorktreePath: "/wt/feature-a"},
		},
		repoDir: repoDir,
	}
	s.PopulateBranchesWithCache(stackCfg.Cache)

	if err := stackCfg.Save(repoDir); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Reload and verify name persists
	loaded, err := LoadStackConfig(repoDir)
	if err != nil {
		t.Fatalf("LoadStackConfig() error = %v", err)
	}

	loadedStack := loaded.Stacks[hash]
	if loadedStack == nil {
		t.Fatalf("Stack not found after reload")
	}
	if loadedStack.Name != "my-feature" {
		t.Errorf("Name = %q, want %q", loadedStack.Name, "my-feature")
	}
	if loadedStack.DisplayName() != "my-feature ["+hash+"]" {
		t.Errorf("DisplayName() = %q, want %q", loadedStack.DisplayName(), "my-feature ["+hash+"]")
	}

	// Update name to empty and verify
	loadedStack.Name = ""
	loaded.Save(repoDir)

	reloaded, _ := LoadStackConfig(repoDir)
	if reloaded.Stacks[hash].Name != "" {
		t.Errorf("Name should be empty after clearing, got %q", reloaded.Stacks[hash].Name)
	}
}

func TestMigrateV4ToV5(t *testing.T) {
	// v4 data — stacks have no Name field
	v4Data := []byte(`{
		"version": 4,
		"repos": {
			"/test/repo": {
				"stacks": {
					"abc1234": {
						"root": "main",
						"tree": {"feature-a": {}}
					}
				},
				"branches": {
					"feature-a": {"worktree_path": "/wt/a"}
				}
			}
		}
	}`)

	migrated, err := migrateV4ToV5(v4Data)
	if err != nil {
		t.Fatalf("migrateV4ToV5() error = %v", err)
	}

	// Verify version was bumped
	var result struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(migrated, &result); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}
	if result.Version != 5 {
		t.Errorf("Version = %d, want 5", result.Version)
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

func TestBranch_EffectiveRemote(t *testing.T) {
	tests := []struct {
		name     string
		remote   string
		expected string
	}{
		{"empty defaults to origin", "", "origin"},
		{"custom remote", "upstream", "upstream"},
		{"fork remote", "jason-nexthop", "jason-nexthop"},
		{"nopush sentinel", RemoteNoPush, RemoteNoPush},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Branch{Name: "test", Remote: tt.remote}
			if got := b.EffectiveRemote(); got != tt.expected {
				t.Errorf("EffectiveRemote() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestBranch_CanPush(t *testing.T) {
	tests := []struct {
		name     string
		remote   string
		expected bool
	}{
		{"empty remote can push", "", true},
		{"origin can push", "origin", true},
		{"custom remote can push", "upstream", true},
		{"nopush cannot push", RemoteNoPush, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Branch{Name: "test", Remote: tt.remote}
			if got := b.CanPush(); got != tt.expected {
				t.Errorf("CanPush() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestBranchCache_Remote_Persistence(t *testing.T) {
	// Verify Remote field is correctly stored and retrieved from BranchCache
	bc := &BranchCache{
		WorktreePath: "/tmp/wt",
		IsRemote:     true,
		Remote:       "jason-nexthop",
	}

	if bc.Remote != "jason-nexthop" {
		t.Errorf("Remote = %q, want %q", bc.Remote, "jason-nexthop")
	}

	// Verify _nopush sentinel
	bc2 := &BranchCache{
		IsRemote: true,
		Remote:   RemoteNoPush,
	}
	if bc2.Remote != RemoteNoPush {
		t.Errorf("Remote = %q, want %q", bc2.Remote, RemoteNoPush)
	}
}

func TestRemoteField_PopulatedFromCache(t *testing.T) {
	// Create a stack with cache that has Remote set
	cache := &CacheConfig{
		Branches: map[string]*BranchCache{
			"feature": {
				WorktreePath: "/tmp/wt/feature",
				IsRemote:     true,
				Remote:       "fork-remote",
				PRUrl:        "https://github.com/org/repo/pull/1",
			},
			"local-branch": {
				WorktreePath: "/tmp/wt/local",
			},
		},
	}

	stack := &Stack{
		Root: "main",
		Tree: BranchTree{
			"feature":      {},
			"local-branch": {},
		},
	}

	branches := stack.GetBranches(cache)

	for _, b := range branches {
		switch b.Name {
		case "feature":
			if b.Remote != "fork-remote" {
				t.Errorf("feature.Remote = %q, want %q", b.Remote, "fork-remote")
			}
			if !b.IsRemote {
				t.Error("feature.IsRemote should be true")
			}
			if b.EffectiveRemote() != "fork-remote" {
				t.Errorf("feature.EffectiveRemote() = %q, want %q", b.EffectiveRemote(), "fork-remote")
			}
		case "local-branch":
			if b.Remote != "" {
				t.Errorf("local-branch.Remote = %q, want empty", b.Remote)
			}
			if b.EffectiveRemote() != "origin" {
				t.Errorf("local-branch.EffectiveRemote() = %q, want %q", b.EffectiveRemote(), "origin")
			}
		}
	}
}

func TestRemoteNoPush_PopulatedFromCache(t *testing.T) {
	cache := &CacheConfig{
		Branches: map[string]*BranchCache{
			"nopush-branch": {
				IsRemote: true,
				Remote:   RemoteNoPush,
			},
		},
	}

	stack := &Stack{
		Root: "main",
		Tree: BranchTree{
			"nopush-branch": {},
		},
	}

	branches := stack.GetBranches(cache)
	if len(branches) != 1 {
		t.Fatalf("expected 1 branch, got %d", len(branches))
	}

	b := branches[0]
	if b.Remote != RemoteNoPush {
		t.Errorf("Remote = %q, want %q", b.Remote, RemoteNoPush)
	}
	if b.CanPush() {
		t.Error("CanPush() should be false for _nopush branch")
	}
	if b.EffectiveRemote() != RemoteNoPush {
		t.Errorf("EffectiveRemote() = %q, want %q", b.EffectiveRemote(), RemoteNoPush)
	}
}

func TestConfig_GetSyncStrategy(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		repoPath string
		want     string
	}{
		{
			name: "repo-specific merge",
			config: &Config{
				Repos: map[string]*RepoConfig{
					"/repo": {SyncStrategy: "merge"},
				},
			},
			repoPath: "/repo",
			want:     "merge",
		},
		{
			name: "repo-specific rebase",
			config: &Config{
				Repos: map[string]*RepoConfig{
					"/repo": {SyncStrategy: "rebase"},
				},
			},
			repoPath: "/repo",
			want:     "rebase",
		},
		{
			name: "empty defaults to rebase",
			config: &Config{
				Repos: map[string]*RepoConfig{
					"/repo": {},
				},
			},
			repoPath: "/repo",
			want:     "rebase",
		},
		{
			name: "no repo config defaults to rebase",
			config: &Config{
				Repos: make(map[string]*RepoConfig),
			},
			repoPath: "/unknown/repo",
			want:     "rebase",
		},
		{
			name:     "nil repos defaults to rebase",
			config:   &Config{},
			repoPath: "/repo",
			want:     "rebase",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.GetSyncStrategy(tt.repoPath)
			if got != tt.want {
				t.Errorf("GetSyncStrategy() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestConfig_InitSubmodules_Persistence verifies that the init_submodules
// pointer-bool round-trips through Save/Load — covering the absent (nil),
// explicit-true, and explicit-false cases. Pointer-bools are easy to lose
// when JSON tags drift, so the round-trip is the contract.
func TestConfig_InitSubmodules_Persistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ezstack-config-init-sub-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	trueVal := true
	falseVal := false

	cfg := &Config{
		DefaultBaseBranch: "main",
		Repos: map[string]*RepoConfig{
			"/repo/explicit-true":  {RepoPath: "/repo/explicit-true", InitSubmodules: &trueVal},
			"/repo/explicit-false": {RepoPath: "/repo/explicit-false", InitSubmodules: &falseVal},
			"/repo/unset":          {RepoPath: "/repo/unset"},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := loaded.GetInitSubmodules("/repo/explicit-true"); !got {
		t.Errorf("explicit-true: GetInitSubmodules() = false, want true")
	}
	if got := loaded.GetInitSubmodules("/repo/explicit-false"); got {
		t.Errorf("explicit-false: GetInitSubmodules() = true, want false")
	}
	if got := loaded.GetInitSubmodules("/repo/unset"); !got {
		t.Errorf("unset: GetInitSubmodules() = false, want true (default)")
	}
	if got := loaded.GetInitSubmodules("/repo/unknown"); !got {
		t.Errorf("unknown repo: GetInitSubmodules() = false, want true (default)")
	}

	// The explicit-false pointer must round-trip as a real false (not nil),
	// because nil would make the getter fall back to the default of true and
	// silently flip the user's setting.
	rc := loaded.GetRepoConfig("/repo/explicit-false")
	if rc == nil || rc.InitSubmodules == nil {
		t.Fatalf("explicit-false: InitSubmodules pointer lost on reload (rc=%+v)", rc)
	}
	if *rc.InitSubmodules {
		t.Errorf("explicit-false: *InitSubmodules = true, want false")
	}
}

func TestConfig_SyncStrategy_Persistence(t *testing.T) {
	// Test that sync_strategy survives save/load cycle
	tmpDir, err := os.MkdirTemp("", "ezstack-config-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Create config with sync_strategy
	cfg := &Config{
		DefaultBaseBranch: "main",
		Repos: map[string]*RepoConfig{
			"/test/repo": {
				RepoPath:     "/test/repo",
				SyncStrategy: "merge",
			},
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Load it back
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	got := loaded.GetSyncStrategy("/test/repo")
	if got != "merge" {
		t.Errorf("After save/load, GetSyncStrategy() = %q, want %q", got, "merge")
	}

	// Verify empty sync_strategy is also preserved correctly
	got = loaded.GetSyncStrategy("/other/repo")
	if got != "rebase" {
		t.Errorf("Unknown repo GetSyncStrategy() = %q, want %q", got, "rebase")
	}
}

// setupTempConfig points EZSTACK_HOME at a fresh temp dir for the duration
// of t. Returns a stable repo path suitable as a stacks.json key.
func setupTempConfig(t *testing.T) string {
	t.Helper()
	t.Setenv("EZSTACK_HOME", t.TempDir())
	return "/fake/repo"
}

func TestMutateBranchCache_CreatesEntry(t *testing.T) {
	repo := setupTempConfig(t)
	err := MutateBranchCache(repo, "feature", func(bc *BranchCache) (*BranchCache, error) {
		if bc != nil {
			t.Errorf("expected nil current, got %+v", bc)
		}
		return &BranchCache{PRUrl: "https://github.com/o/r/pull/1", PRState: "OPEN"}, nil
	})
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}

	cc, err := LoadCacheConfig(repo)
	if err != nil {
		t.Fatal(err)
	}
	bc := cc.GetBranchCache("feature")
	if bc == nil || bc.PRUrl != "https://github.com/o/r/pull/1" || bc.PRState != "OPEN" {
		t.Errorf("entry not persisted: %+v", bc)
	}
}

func TestMutateBranchCache_UpdatesExistingEntry(t *testing.T) {
	repo := setupTempConfig(t)
	if err := MutateBranchCache(repo, "feature", func(bc *BranchCache) (*BranchCache, error) {
		return &BranchCache{PRUrl: "u1", PRState: "OPEN"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := MutateBranchCache(repo, "feature", func(bc *BranchCache) (*BranchCache, error) {
		if bc == nil || bc.PRState != "OPEN" {
			t.Errorf("expected to receive existing entry, got %+v", bc)
		}
		bc.PRState = "MERGED"
		bc.IsMerged = true
		return bc, nil
	}); err != nil {
		t.Fatal(err)
	}

	cc, _ := LoadCacheConfig(repo)
	bc := cc.GetBranchCache("feature")
	if bc.PRState != "MERGED" || !bc.IsMerged || bc.PRUrl != "u1" {
		t.Errorf("update lost fields: %+v", bc)
	}
}

func TestMutateBranchCache_DeletesByReturningNil(t *testing.T) {
	repo := setupTempConfig(t)
	if err := MutateBranchCache(repo, "feature", func(bc *BranchCache) (*BranchCache, error) {
		return &BranchCache{PRUrl: "u"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := MutateBranchCache(repo, "feature", func(bc *BranchCache) (*BranchCache, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	cc, _ := LoadCacheConfig(repo)
	if bc := cc.GetBranchCache("feature"); bc != nil {
		t.Errorf("entry should have been deleted, still got: %+v", bc)
	}
}

func TestMutateBranchCache_NilCurrentNilNext_DoesNotWriteFile(t *testing.T) {
	// Calling MutateBranchCache against a missing branch with a closure
	// that also returns nil is a real no-op (clearPRFromCache hits this
	// path). It must not write the stacks.json file: doing so would create
	// an empty file from nothing on every "no entry to clear" call.
	repo := setupTempConfig(t)
	configDir, _ := ConfigDir()
	stackPath := filepath.Join(configDir, "stacks.json")

	if _, err := os.Stat(stackPath); !os.IsNotExist(err) {
		t.Fatalf("expected no stacks.json initially, got err=%v", err)
	}

	if err := MutateBranchCache(repo, "ghost", func(bc *BranchCache) (*BranchCache, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(stackPath); !os.IsNotExist(err) {
		t.Errorf("stacks.json should not have been created for nil/nil mutate, but it exists")
	}
}

func TestMutateBranchCache_FnErrorAbortsSave(t *testing.T) {
	repo := setupTempConfig(t)
	if err := MutateBranchCache(repo, "feature", func(bc *BranchCache) (*BranchCache, error) {
		return &BranchCache{PRUrl: "original"}, nil
	}); err != nil {
		t.Fatal(err)
	}

	wantErr := errors.New("intentional")
	got := MutateBranchCache(repo, "feature", func(bc *BranchCache) (*BranchCache, error) {
		bc.PRUrl = "modified"
		return bc, wantErr
	})
	if !errors.Is(got, wantErr) {
		t.Errorf("expected fn error to surface, got %v", got)
	}

	cc, _ := LoadCacheConfig(repo)
	bc := cc.GetBranchCache("feature")
	if bc == nil || bc.PRUrl != "original" {
		t.Errorf("aborted save still mutated: %+v", bc)
	}
}

func TestMutateBranchCache_PreservesOtherBranchesAcrossCalls(t *testing.T) {
	// Sequential writes to different branches in the same repo must
	// preserve each other's data — this is the in-process flavor of the
	// concurrent test below and protects against the easy regression of
	// a closure that overwrites the whole branches map.
	repo := setupTempConfig(t)
	if err := MutateBranchCache(repo, "alpha", func(bc *BranchCache) (*BranchCache, error) {
		return &BranchCache{PRUrl: "alpha-url", PRState: "OPEN"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := MutateBranchCache(repo, "beta", func(bc *BranchCache) (*BranchCache, error) {
		return &BranchCache{PRUrl: "beta-url", PRState: "MERGED"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := MutateBranchCache(repo, "alpha", func(bc *BranchCache) (*BranchCache, error) {
		bc.PRState = "MERGED"
		return bc, nil
	}); err != nil {
		t.Fatal(err)
	}

	cc, _ := LoadCacheConfig(repo)
	a := cc.GetBranchCache("alpha")
	b := cc.GetBranchCache("beta")
	if a == nil || a.PRState != "MERGED" || a.PRUrl != "alpha-url" {
		t.Errorf("alpha lost fields: %+v", a)
	}
	if b == nil || b.PRState != "MERGED" || b.PRUrl != "beta-url" {
		t.Errorf("beta clobbered by later alpha mutate: %+v", b)
	}
}

func TestMutateBranchCache_ConcurrentDifferentBranches_NoLostUpdates(t *testing.T) {
	// The cross-process race fix: parallel goroutines writing to DIFFERENT
	// branches must not clobber each other. With the old LoadCacheConfig +
	// SetBranchCache + Save pattern, each Save replaced the whole branches
	// map with the in-memory copy, so a writer that loaded before its peer
	// would silently revert the peer's update.
	repo := setupTempConfig(t)
	const iters = 50
	const workers = 4

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				name := fmt.Sprintf("w%d-%d", wid, i)
				url := fmt.Sprintf("https://github.com/o/r/pull/%d%d", wid, i)
				err := MutateBranchCache(repo, name, func(bc *BranchCache) (*BranchCache, error) {
					return &BranchCache{PRUrl: url, PRState: "OPEN"}, nil
				})
				if err != nil {
					t.Errorf("worker %d iter %d: %v", wid, i, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	cc, err := LoadCacheConfig(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := workers * iters
	got := len(cc.Branches)
	if got != want {
		t.Errorf("after concurrent mutates: %d branches, want %d (lost %d updates)", got, want, want-got)
	}
}

func TestMutateBranchCache_ConcurrentSameBranch_LastWriteWins(t *testing.T) {
	// Parallel writes to the SAME branch serialize through the file lock
	// — last writer wins, but no entry should be lost or corrupted. We
	// only assert "the final value is one of the writers' values, and
	// there's exactly one entry" — strict ordering isn't promised.
	repo := setupTempConfig(t)
	const writers = 8

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			url := fmt.Sprintf("https://github.com/o/r/pull/%d", wid+100)
			err := MutateBranchCache(repo, "shared", func(bc *BranchCache) (*BranchCache, error) {
				return &BranchCache{PRUrl: url, PRState: "OPEN"}, nil
			})
			if err != nil {
				t.Errorf("writer %d: %v", wid, err)
			}
		}(w)
	}
	wg.Wait()

	cc, _ := LoadCacheConfig(repo)
	bc := cc.GetBranchCache("shared")
	if bc == nil {
		t.Fatal("entry vanished under contention")
	}
	if bc.PRState != "OPEN" {
		t.Errorf("PRState corrupted: %q", bc.PRState)
	}
	// PRUrl should be one of the writers' values (we don't care which).
	matched := false
	for w := 0; w < writers; w++ {
		if bc.PRUrl == fmt.Sprintf("https://github.com/o/r/pull/%d", w+100) {
			matched = true
			break
		}
	}
	if !matched {
		t.Errorf("PRUrl is not from any writer: %q", bc.PRUrl)
	}
}

func TestMutateBranchCache_DoesNotTouchOtherRepos(t *testing.T) {
	// Two repos share the same stacks.json but live under different keys.
	// MutateBranchCache against repoA must never change repoB's section.
	t.Setenv("EZSTACK_HOME", t.TempDir())
	const repoA = "/test/repoA"
	const repoB = "/test/repoB"

	if err := MutateBranchCache(repoA, "alpha", func(bc *BranchCache) (*BranchCache, error) {
		return &BranchCache{PRUrl: "alpha-url"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := MutateBranchCache(repoB, "beta", func(bc *BranchCache) (*BranchCache, error) {
		return &BranchCache{PRUrl: "beta-url"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := MutateBranchCache(repoA, "alpha", func(bc *BranchCache) (*BranchCache, error) {
		bc.PRState = "MERGED"
		return bc, nil
	}); err != nil {
		t.Fatal(err)
	}

	ccA, _ := LoadCacheConfig(repoA)
	ccB, _ := LoadCacheConfig(repoB)
	if a := ccA.GetBranchCache("alpha"); a == nil || a.PRUrl != "alpha-url" || a.PRState != "MERGED" {
		t.Errorf("repoA.alpha not as expected: %+v", a)
	}
	if b := ccB.GetBranchCache("beta"); b == nil || b.PRUrl != "beta-url" {
		t.Errorf("repoB.beta lost: %+v", b)
	}
	if ccA.GetBranchCache("beta") != nil {
		t.Errorf("repoA leaked beta from repoB")
	}
	if ccB.GetBranchCache("alpha") != nil {
		t.Errorf("repoB leaked alpha from repoA")
	}
}

func TestMutateBranchCache_PreservesNonPRFields(t *testing.T) {
	// clearPRFromCache uses MutateBranchCache to wipe PR fields while
	// preserving WorktreePath/IsRemote/Remote. Lock that contract in.
	repo := setupTempConfig(t)
	if err := MutateBranchCache(repo, "feature", func(bc *BranchCache) (*BranchCache, error) {
		return &BranchCache{
			WorktreePath: "/wt/feature",
			IsRemote:     true,
			Remote:       "fork",
			PRUrl:        "https://github.com/o/r/pull/42",
			PRState:      "OPEN",
		}, nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := MutateBranchCache(repo, "feature", func(bc *BranchCache) (*BranchCache, error) {
		bc.ClearPRFields()
		return bc, nil
	}); err != nil {
		t.Fatal(err)
	}

	cc, _ := LoadCacheConfig(repo)
	bc := cc.GetBranchCache("feature")
	if bc == nil {
		t.Fatal("entry vanished — should still hold worktree/remote")
	}
	if bc.PRUrl != "" || bc.PRState != "" || bc.IsMerged {
		t.Errorf("PR fields not cleared: %+v", bc)
	}
	if bc.WorktreePath != "/wt/feature" || !bc.IsRemote || bc.Remote != "fork" {
		t.Errorf("non-PR fields touched: %+v", bc)
	}
}

// TestRemoveBranchFromTree_PreservesSiblingSubtree covers the case where the
// branch we remove has a child whose name collides with an existing sibling
// at the parent level. Without the merge, the existing sibling's subtree
// would be silently overwritten and any branches under it dropped.
func TestRemoveBranchFromTree_PreservesSiblingSubtree(t *testing.T) {
	s := &Stack{
		Hash: "abc1234",
		Root: "main",
		Tree: BranchTree{
			"feat-A": BranchTree{
				"feat-shared": BranchTree{
					"feat-A-grandchild": BranchTree{},
				},
			},
			"feat-shared": BranchTree{
				"feat-shared-grandchild": BranchTree{},
			},
		},
	}

	s.RemoveBranch("feat-A")

	shared, ok := s.Tree["feat-shared"]
	if !ok {
		t.Fatal("feat-shared was removed entirely; expected the merged subtree to survive")
	}
	if _, ok := shared["feat-shared-grandchild"]; !ok {
		t.Error("original feat-shared-grandchild was lost; the existing subtree was clobbered")
	}
	if _, ok := shared["feat-A-grandchild"]; !ok {
		t.Error("feat-A-grandchild was not reparented under feat-shared")
	}
	if _, ok := s.Tree["feat-A"]; ok {
		t.Error("feat-A is still in the tree")
	}
}
