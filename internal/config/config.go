package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config holds the global configuration for ezstack
type Config struct {
	// DefaultBaseBranch is the default base branch (usually "main" or "master")
	DefaultBaseBranch string `json:"default_base_branch"`
	// GitHubToken for API access (optional, can use gh cli)
	GitHubToken string `json:"github_token,omitempty"`
	// Repos holds per-repository configuration, keyed by repo path
	Repos map[string]*RepoConfig `json:"repos"`
}

// RepoConfig holds configuration for a specific repository
type RepoConfig struct {
	// RepoPath is the absolute path to the main repository
	RepoPath string `json:"repo_path"`
	// WorktreeBaseDir is where worktrees are created for this repo
	WorktreeBaseDir string `json:"worktree_base_dir"`
	// DefaultBaseBranch overrides the global default for this repo
	DefaultBaseBranch string `json:"default_base_branch,omitempty"`
	// CdAfterNew if true, outputs cd command after creating new worktree
	CdAfterNew *bool `json:"cd_after_new,omitempty"`
	// AutoDraftWipCommits if true, auto-creates draft PRs when commit starts with "wip"
	AutoDraftWipCommits *bool `json:"auto_draft_wip_commits,omitempty"`
}

// GetRepoConfig returns the configuration for a specific repo path
func (c *Config) GetRepoConfig(repoPath string) *RepoConfig {
	if c.Repos == nil {
		return nil
	}
	return c.Repos[repoPath]
}

// SetRepoConfig sets the configuration for a specific repo
func (c *Config) SetRepoConfig(repoPath string, repoCfg *RepoConfig) {
	if c.Repos == nil {
		c.Repos = make(map[string]*RepoConfig)
	}
	repoCfg.RepoPath = repoPath
	c.Repos[repoPath] = repoCfg
}

// GetWorktreeBaseDir returns the worktree base dir for a repo, or empty if not configured
func (c *Config) GetWorktreeBaseDir(repoPath string) string {
	if repoCfg := c.GetRepoConfig(repoPath); repoCfg != nil {
		return repoCfg.WorktreeBaseDir
	}
	return ""
}

// GetBaseBranch returns the base branch for a repo (repo-specific or global default)
func (c *Config) GetBaseBranch(repoPath string) string {
	if repoCfg := c.GetRepoConfig(repoPath); repoCfg != nil && repoCfg.DefaultBaseBranch != "" {
		return repoCfg.DefaultBaseBranch
	}
	if c.DefaultBaseBranch != "" {
		return c.DefaultBaseBranch
	}
	return "main"
}

// GetCdAfterNew returns whether to cd after creating a new worktree (default: false)
func (c *Config) GetCdAfterNew(repoPath string) bool {
	if repoCfg := c.GetRepoConfig(repoPath); repoCfg != nil && repoCfg.CdAfterNew != nil {
		return *repoCfg.CdAfterNew
	}
	return false
}

// stackConfigFile is the on-disk format that stores stacks for all repos
type stackConfigFile struct {
	Repos map[string]*StackConfig `json:"repos"`
}

// StackConfig holds metadata about stacks for a single repo
type StackConfig struct {
	Stacks  map[string]*Stack `json:"stacks"`
	repoDir string            // internal, not serialized - used for saving
}

// Stack represents a chain of stacked branches
type Stack struct {
	Name     string    `json:"name"`
	Branches []*Branch `json:"branches"`
}

// Branch represents a single branch in a stack
type Branch struct {
	Name         string `json:"name"`
	Parent       string `json:"parent"`        // Parent branch name
	WorktreePath string `json:"worktree_path"` // Path to the worktree
	PRNumber     int    `json:"pr_number,omitempty"`
	PRUrl        string `json:"pr_url,omitempty"`
	BaseBranch   string `json:"base_branch"`         // The branch this PR targets
	IsRemote     bool   `json:"is_remote,omitempty"` // True if this is someone else's branch (created via --from-remote)
	IsMerged     bool   `json:"is_merged,omitempty"` // True if this branch's PR has been merged (worktree deleted but kept in config for display)
}

// ConfigDir returns the path to the ezstack config directory
// Checks EZSTACK_HOME environment variable first, then defaults to $HOME/.ezstack
func ConfigDir() (string, error) {
	// Check for EZSTACK_HOME environment variable first
	if ezstackHome := os.Getenv("EZSTACK_HOME"); ezstackHome != "" {
		return ezstackHome, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ezstack"), nil
}

// legacyConfig represents the old config format for backward compatibility
type legacyConfig struct {
	WorktreeBaseDir   string `json:"worktree_base_dir"`
	MainRepoDir       string `json:"main_repo_dir"`
	DefaultBaseBranch string `json:"default_base_branch"`
	GitHubToken       string `json:"github_token,omitempty"`
}

// Load loads the configuration from ~/.ezstack/config.json
func Load() (*Config, error) {
	configDir, err := ConfigDir()
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{
				DefaultBaseBranch: "main",
				Repos:             make(map[string]*RepoConfig),
			}, nil
		}
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Initialize repos map if nil
	if cfg.Repos == nil {
		cfg.Repos = make(map[string]*RepoConfig)
	}

	// Check for legacy config format and migrate if needed
	var legacy legacyConfig
	if err := json.Unmarshal(data, &legacy); err == nil {
		if legacy.WorktreeBaseDir != "" && legacy.MainRepoDir != "" && len(cfg.Repos) == 0 {
			// Migrate legacy config: use MainRepoDir as the repo key
			cfg.Repos[legacy.MainRepoDir] = &RepoConfig{
				RepoPath:        legacy.MainRepoDir,
				WorktreeBaseDir: legacy.WorktreeBaseDir,
			}
		}
	}

	return &cfg, nil
}

// Save saves the configuration to ~/.ezstack/config.json
func (c *Config) Save() error {
	configDir, err := ConfigDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(configDir, "config.json"), data, 0644)
}

// LoadStackConfig loads stack metadata for a specific repo from $HOME/.ezstack/stacks.json
func LoadStackConfig(repoDir string) (*StackConfig, error) {
	configDir, err := ConfigDir()
	if err != nil {
		return nil, err
	}

	// Ensure the config directory exists
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, err
	}

	stackPath := filepath.Join(configDir, "stacks.json")
	data, err := os.ReadFile(stackPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &StackConfig{
				Stacks:  make(map[string]*Stack),
				repoDir: repoDir,
			}, nil
		}
		return nil, err
	}

	var file stackConfigFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}

	if file.Repos == nil {
		file.Repos = make(map[string]*StackConfig)
	}

	// Get the stacks for this specific repo
	sc := file.Repos[repoDir]
	if sc == nil {
		sc = &StackConfig{
			Stacks: make(map[string]*Stack),
		}
	}
	if sc.Stacks == nil {
		sc.Stacks = make(map[string]*Stack)
	}
	sc.repoDir = repoDir

	return sc, nil
}

// Save saves the stack config for this repo to $HOME/.ezstack/stacks.json
func (sc *StackConfig) Save(repoDir string) error {
	configDir, err := ConfigDir()
	if err != nil {
		return err
	}

	// Ensure the config directory exists
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	stackPath := filepath.Join(configDir, "stacks.json")

	// Load existing file to preserve other repos
	var file stackConfigFile
	data, err := os.ReadFile(stackPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		file.Repos = make(map[string]*StackConfig)
	} else {
		if err := json.Unmarshal(data, &file); err != nil {
			return err
		}
		if file.Repos == nil {
			file.Repos = make(map[string]*StackConfig)
		}
	}

	// Use the stored repoDir if available, otherwise use the parameter
	targetRepo := sc.repoDir
	if targetRepo == "" {
		targetRepo = repoDir
	}

	// Update this repo's stacks
	file.Repos[targetRepo] = sc

	newData, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(stackPath, newData, 0644)
}
