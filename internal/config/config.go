package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

var v *viper.Viper

func init() {
	v = viper.New()
	v.SetEnvPrefix("EZSTACK")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	v.SetDefault("default_base_branch", "main")
}

// Config holds the global configuration for ezstack
type Config struct {
	DefaultBaseBranch string                 `json:"default_base_branch"`
	GitHubToken       string                 `json:"github_token,omitempty"`
	Repos             map[string]*RepoConfig `json:"repos"`
}

// RepoConfig holds configuration for a specific repository
type RepoConfig struct {
	RepoPath            string `json:"repo_path"`
	WorktreeBaseDir     string `json:"worktree_base_dir"`
	DefaultBaseBranch   string `json:"default_base_branch,omitempty"`
	CdAfterNew          *bool  `json:"cd_after_new,omitempty"`
	AutoDraftWipCommits *bool  `json:"auto_draft_wip_commits,omitempty"`
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

// BranchTree is a recursive map representing the stack hierarchy
// Each key is a branch name, and its value is another BranchTree of its children
type BranchTree map[string]BranchTree

// repoData stores all stack and branch data for a single repo on disk
type repoData struct {
	Stacks   map[string]*Stack       `json:"stacks"`
	Branches map[string]*BranchCache `json:"branches"`
}

// currentStackConfigVersion is the latest version of the stacks.json format.
// Bump this when adding a new migration.
const currentStackConfigVersion = 5

// stackConfigFile is the on-disk format that stores stacks for all repos
type stackConfigFile struct {
	Version int                  `json:"version"`
	Repos   map[string]*repoData `json:"repos"`
}

// StackConfig holds metadata about stacks for a single repo
type StackConfig struct {
	Stacks  map[string]*Stack `json:"stacks"`
	Cache   *CacheConfig      `json:"-"` // loaded alongside stacks, not serialized separately
	repoDir string            // internal, not serialized - used for saving
}

// Stack represents a chain of stacked branches as a tree
// Hash is the map key in StackConfig.Stacks and is populated at load time.
type Stack struct {
	Hash            string       `json:"-"`                           // Populated from map key at load time
	Name            string       `json:"name,omitempty"`              // Optional user-given name for the stack
	Root            string       `json:"root"`                       // The base branch (e.g. "main", or a remote branch name)
	RootPRNumber    int          `json:"root_pr_number,omitempty"`    // PR number of the root branch (for remote base branches)
	RootPRUrl       string       `json:"root_pr_url,omitempty"`      // PR URL of the root branch (for remote base branches)
	DeleteDeclined  bool         `json:"delete_declined,omitempty"`   // User declined cleanup prompt; don't re-ask
	Tree            BranchTree   `json:"tree"`                       // The tree of branches
	Branches        []*Branch    `json:"-"`                           // Runtime-only: populated from Tree for backward compatibility
	cache           *CacheConfig // Runtime-only: reference to cache for metadata
}

// DisplayName returns the display string for a stack: "name [hash]" or just hash
func (s *Stack) DisplayName() string {
	if s.Name != "" {
		return fmt.Sprintf("%s [%s]", s.Name, s.Hash)
	}
	return s.Hash
}

// GenerateStackHash generates a 7-char hex hash from a stack name using FNV-32a
func GenerateStackHash(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	return fmt.Sprintf("%07x", h.Sum32())
}

// BranchCache holds cached metadata for a branch
type BranchCache struct {
	WorktreePath string `json:"worktree_path,omitempty"`
	PRNumber     int    `json:"pr_number,omitempty"`
	PRUrl        string `json:"pr_url,omitempty"`
	PRState      string `json:"pr_state,omitempty"` // Cached: "OPEN", "DRAFT", "MERGED", "CLOSED"
	IsMerged     bool   `json:"is_merged,omitempty"`
	IsRemote     bool   `json:"is_remote,omitempty"`
}

// CacheConfig holds cached branch metadata for a repo
type CacheConfig struct {
	Branches map[string]*BranchCache `json:"branches"`
	repoDir  string
}

// Branch represents a single branch in a stack, constructed from the tree and cache at runtime.
type Branch struct {
	Name         string `json:"name"`
	Parent       string `json:"parent"`
	WorktreePath string `json:"worktree_path"`
	PRNumber     int    `json:"pr_number,omitempty"`
	PRUrl        string `json:"pr_url,omitempty"`
	PRState      string `json:"pr_state,omitempty"`  // Cached: "OPEN", "DRAFT", "MERGED", "CLOSED"
	BaseBranch   string `json:"base_branch"`         // original tree parent, used for display ordering
	IsRemote     bool   `json:"is_remote,omitempty"` // branch belongs to another contributor
	IsMerged     bool   `json:"is_merged,omitempty"`
}

// legacyStackConfigFile represents the old config format for backward compatibility
type legacyStackConfigFile struct {
	Repos map[string]*legacyStackConfig `json:"repos"`
}

type legacyStackConfig struct {
	Stacks map[string]*legacyStack `json:"stacks"`
}

type legacyStack struct {
	Name     string          `json:"name"`
	Branches []*legacyBranch `json:"branches"`
}

type legacyBranch struct {
	Name         string `json:"name"`
	Parent       string `json:"parent"`
	WorktreePath string `json:"worktree_path"`
	PRNumber     int    `json:"pr_number,omitempty"`
	PRUrl        string `json:"pr_url,omitempty"`
	BaseBranch   string `json:"base_branch"`
	IsRemote     bool   `json:"is_remote,omitempty"`
	IsMerged     bool   `json:"is_merged,omitempty"`
}

// ConfigDir returns the path to the ezstack config directory.
// Checks EZSTACK_HOME first, then defaults to $HOME/.ezstack.
func ConfigDir() (string, error) {
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

// atomicWriteFile writes data to a file atomically by writing to a temp file
// in the same directory and then renaming it.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ezstack-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// migrateStackConfig migrates stacks.json data from srcVersion to dstVersion.
// Each step runs the migration for that version (e.g., 0→1, then 1→2).
// Returns the migrated JSON bytes.
func migrateStackConfig(data []byte, srcVersion, dstVersion int) ([]byte, error) {
	// migrations[i] migrates from version i to version i+1
	migrations := []func([]byte) ([]byte, error){
		migrateV0ToV1,
		migrateV1ToV2,
		migrateV2ToV3,
		migrateV3ToV4,
		migrateV4ToV5,
	}

	for v := srcVersion; v < dstVersion; v++ {
		if v < 0 || v >= len(migrations) {
			return nil, fmt.Errorf("no migration defined for version %d → %d", v, v+1)
		}
		var err error
		data, err = migrations[v](data)
		if err != nil {
			return nil, fmt.Errorf("migration v%d → v%d failed: %w", v, v+1, err)
		}
	}
	return data, nil
}

// migrateV0ToV1 converts legacy flat-array stacks to tree format.
// v0: stacks have "branches" as a flat array of objects
// v1: stacks have "root" + "tree" structure, branch metadata moves to repo-level "branches"
func migrateV0ToV1(data []byte) ([]byte, error) {
	// v1 intermediate format: stacks keyed by name, with name/root/tree fields
	type v1Stack struct {
		Name string     `json:"name"`
		Root string     `json:"root"`
		Tree BranchTree `json:"tree"`
	}
	type v1RepoData struct {
		Stacks   map[string]*v1Stack     `json:"stacks"`
		Branches map[string]*BranchCache `json:"branches"`
	}
	type v1File struct {
		Version int                    `json:"version"`
		Repos   map[string]*v1RepoData `json:"repos"`
	}

	var legacyFile legacyStackConfigFile
	if err := json.Unmarshal(data, &legacyFile); err != nil {
		return nil, err
	}

	if legacyFile.Repos == nil {
		// Nothing to migrate, just set version
		result := v1File{
			Version: 1,
			Repos:   make(map[string]*v1RepoData),
		}
		return json.MarshalIndent(result, "", "  ")
	}

	result := v1File{
		Version: 1,
		Repos:   make(map[string]*v1RepoData),
	}

	for repoPath, legacySC := range legacyFile.Repos {
		if legacySC == nil {
			continue
		}

		rd := &v1RepoData{
			Stacks:   make(map[string]*v1Stack),
			Branches: make(map[string]*BranchCache),
		}

		for stackName, legacyStack := range legacySC.Stacks {
			if legacyStack == nil {
				continue
			}

			branchSet := make(map[string]bool)
			for _, b := range legacyStack.Branches {
				branchSet[b.Name] = true
			}

			// Find the root: the parent that isn't itself in the stack
			root := "main"
			for _, b := range legacyStack.Branches {
				if !branchSet[b.Parent] {
					root = b.Parent
					break
				}
			}

			children := make(map[string][]string)
			for _, b := range legacyStack.Branches {
				parent := b.Parent
				if !branchSet[parent] {
					parent = root
				}
				children[parent] = append(children[parent], b.Name)
			}

			var buildTree func(parent string) BranchTree
			buildTree = func(parent string) BranchTree {
				tree := make(BranchTree)
				for _, childName := range children[parent] {
					tree[childName] = buildTree(childName)
				}
				return tree
			}

			tree := buildTree(root)

			// Move branch metadata to the repo-level cache
			for _, b := range legacyStack.Branches {
				rd.Branches[b.Name] = &BranchCache{
					WorktreePath: b.WorktreePath,
					PRNumber:     b.PRNumber,
					PRUrl:        b.PRUrl,
					IsMerged:     b.IsMerged,
					IsRemote:     b.IsRemote,
				}
			}

			rd.Stacks[stackName] = &v1Stack{
				Name: legacyStack.Name,
				Root: root,
				Tree: tree,
			}
		}

		result.Repos[repoPath] = rd
	}

	return json.MarshalIndent(result, "", "  ")
}

// migrateV1ToV2 merges cache.json data into stacks.json and generates stack hashes.
// v1: tree format, no hashes, cache may be in separate cache.json
// v2: tree format + hash field on stacks + cache.json merged into branches
func migrateV1ToV2(data []byte) ([]byte, error) {
	// v2 intermediate format: stacks keyed by name, with name/hash/root/tree fields
	type v2Stack struct {
		Name string     `json:"name"`
		Hash string     `json:"hash"`
		Root string     `json:"root"`
		Tree BranchTree `json:"tree"`
	}
	type v2RepoData struct {
		Stacks   map[string]*v2Stack     `json:"stacks"`
		Branches map[string]*BranchCache `json:"branches"`
	}
	type v2File struct {
		Version int                    `json:"version"`
		Repos   map[string]*v2RepoData `json:"repos"`
	}

	var file v2File
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}

	file.Version = 2
	if file.Repos == nil {
		file.Repos = make(map[string]*v2RepoData)
	}

	configDir, err := ConfigDir()
	if err == nil {
		cachePath := filepath.Join(configDir, "cache.json")
		cacheData, err := os.ReadFile(cachePath)
		if err == nil {
			var cacheFile map[string]json.RawMessage
			if json.Unmarshal(cacheData, &cacheFile) == nil {
				emptied := true
				for repoPath, rawCC := range cacheFile {
					var cc CacheConfig
					if json.Unmarshal(rawCC, &cc) != nil || len(cc.Branches) == 0 {
						continue
					}

					rd := file.Repos[repoPath]
					if rd == nil {
						rd = &v2RepoData{
							Stacks:   make(map[string]*v2Stack),
							Branches: make(map[string]*BranchCache),
						}
						file.Repos[repoPath] = rd
					}
					if rd.Branches == nil {
						rd.Branches = make(map[string]*BranchCache)
					}

					for name, bc := range cc.Branches {
						if _, exists := rd.Branches[name]; !exists {
							rd.Branches[name] = bc
						}
					}

					delete(cacheFile, repoPath)
				}

				// Clean up cache.json
				for range cacheFile {
					emptied = false
					break
				}
				if emptied {
					os.Remove(cachePath)
				} else {
					newCacheData, err := json.MarshalIndent(cacheFile, "", "  ")
					if err == nil {
						atomicWriteFile(cachePath, newCacheData, 0644)
					}
				}
			}
		}
	}

	for _, rd := range file.Repos {
		if rd == nil {
			continue
		}
		for name, stack := range rd.Stacks {
			if stack != nil && stack.Hash == "" {
				stack.Hash = GenerateStackHash(name)
			}
		}
		if rd.Branches == nil {
			rd.Branches = make(map[string]*BranchCache)
		}
	}

	return json.MarshalIndent(file, "", "  ")
}

// migrateV2ToV3 re-keys stacks by hash instead of name, and removes name/hash fields from stack objects.
// v2: stacks keyed by name, with name/hash/root/tree fields
// v3: stacks keyed by hash, with only root/tree fields (hash is the map key)
func migrateV2ToV3(data []byte) ([]byte, error) {
	type v2Stack struct {
		Name string     `json:"name"`
		Hash string     `json:"hash"`
		Root string     `json:"root"`
		Tree BranchTree `json:"tree"`
	}
	type v2RepoData struct {
		Stacks   map[string]*v2Stack     `json:"stacks"`
		Branches map[string]*BranchCache `json:"branches"`
	}
	type v2File struct {
		Version int                    `json:"version"`
		Repos   map[string]*v2RepoData `json:"repos"`
	}

	var old v2File
	if err := json.Unmarshal(data, &old); err != nil {
		return nil, err
	}

	// v3 output uses the current Stack struct (no name/hash in JSON)
	type v3RepoData struct {
		Stacks   map[string]*Stack       `json:"stacks"`
		Branches map[string]*BranchCache `json:"branches"`
	}
	type v3File struct {
		Version int                    `json:"version"`
		Repos   map[string]*v3RepoData `json:"repos"`
	}

	newFile := v3File{Version: 3, Repos: make(map[string]*v3RepoData)}
	for repoPath, rd := range old.Repos {
		if rd == nil {
			continue
		}
		newRd := &v3RepoData{
			Stacks:   make(map[string]*Stack),
			Branches: rd.Branches,
		}
		if newRd.Branches == nil {
			newRd.Branches = make(map[string]*BranchCache)
		}
		for name, stack := range rd.Stacks {
			if stack == nil {
				continue
			}
			hash := stack.Hash
			if hash == "" {
				hash = GenerateStackHash(name)
			}
			newRd.Stacks[hash] = &Stack{
				Root: stack.Root,
				Tree: stack.Tree,
			}
		}
		newFile.Repos[repoPath] = newRd
	}

	return json.MarshalIndent(newFile, "", "  ")
}

// migrateV3ToV4 moves remote branches from tree nodes to stack roots.
// v3: remote branches are tree nodes with IsRemote=true in cache
// v4: remote branches become the stack Root with RootPRNumber/RootPRUrl
func migrateV3ToV4(data []byte) ([]byte, error) {
	type v3Stack struct {
		Root string     `json:"root"`
		Tree BranchTree `json:"tree"`
	}
	type v3RepoData struct {
		Stacks   map[string]*v3Stack     `json:"stacks"`
		Branches map[string]*BranchCache `json:"branches"`
	}
	type v3File struct {
		Version int                    `json:"version"`
		Repos   map[string]*v3RepoData `json:"repos"`
	}

	var old v3File
	if err := json.Unmarshal(data, &old); err != nil {
		return nil, err
	}

	type v4Stack struct {
		Root         string     `json:"root"`
		RootPRNumber int        `json:"root_pr_number,omitempty"`
		RootPRUrl    string     `json:"root_pr_url,omitempty"`
		Tree         BranchTree `json:"tree"`
	}
	type v4RepoData struct {
		Stacks   map[string]*v4Stack     `json:"stacks"`
		Branches map[string]*BranchCache `json:"branches"`
	}
	type v4File struct {
		Version int                    `json:"version"`
		Repos   map[string]*v4RepoData `json:"repos"`
	}

	newFile := v4File{Version: 4, Repos: make(map[string]*v4RepoData)}
	for repoPath, rd := range old.Repos {
		if rd == nil {
			continue
		}
		newRd := &v4RepoData{
			Stacks:   make(map[string]*v4Stack),
			Branches: rd.Branches,
		}
		if newRd.Branches == nil {
			newRd.Branches = make(map[string]*BranchCache)
		}

		for hash, stack := range rd.Stacks {
			if stack == nil {
				continue
			}
			newStack := &v4Stack{
				Root: stack.Root,
				Tree: stack.Tree,
			}

			// Find remote branches in the tree (top-level only, since remote branches
			// are always direct children of root in v3)
			for branchName, children := range stack.Tree {
				bc := rd.Branches[branchName]
				if bc != nil && bc.IsRemote {
					// This remote branch becomes the new root
					newStack.Root = branchName
					newStack.RootPRNumber = bc.PRNumber
					newStack.RootPRUrl = bc.PRUrl

					// Promote children to top-level tree nodes
					delete(newStack.Tree, branchName)
					for childName, childTree := range children {
						newStack.Tree[childName] = childTree
					}

					// Remove from branch cache
					delete(newRd.Branches, branchName)
					break // Only one remote branch per stack
				}
			}

			newRd.Stacks[hash] = newStack
		}

		newFile.Repos[repoPath] = newRd
	}

	return json.MarshalIndent(newFile, "", "  ")
}

// migrateV4ToV5 adds the optional Name field to stacks.
// No structural change needed — the Name field is omitempty and defaults to "".
// This migration just bumps the version number.
func migrateV4ToV5(data []byte) ([]byte, error) {
	var file struct {
		Version int                    `json:"version"`
		Repos   map[string]*repoData   `json:"repos"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	file.Version = 5
	return json.MarshalIndent(file, "", "  ")
}

// Load loads the configuration from ~/.ezstack/config.json.
// Top-level scalar values are resolved through Viper so that EZSTACK_-prefixed
// environment variables (e.g. EZSTACK_GITHUB_TOKEN) take precedence over the file.
// The repos map is read directly from JSON because Viper lowercases all keys,
// which would corrupt filesystem-path map keys like /Users/….
func Load() (*Config, error) {
	configDir, err := ConfigDir()
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(configDir, "config.json")

	v.SetConfigFile(configPath)
	v.SetConfigType("json")
	if err := v.ReadInConfig(); err != nil {
		var pathErr *os.PathError
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok && !errors.As(err, &pathErr) {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	cfg := &Config{
		DefaultBaseBranch: v.GetString("default_base_branch"),
		GitHubToken:       v.GetString("github_token"),
		Repos:             make(map[string]*RepoConfig),
	}

	// The repos map is read from raw JSON to preserve case-sensitive path keys.
	data, err := os.ReadFile(configPath)
	if err == nil {
		var raw struct {
			Repos map[string]*RepoConfig `json:"repos"`
		}
		if jsonErr := json.Unmarshal(data, &raw); jsonErr == nil && raw.Repos != nil {
			cfg.Repos = raw.Repos
		}

		// Migrate legacy single-repo config format.
		var legacy legacyConfig
		if jsonErr := json.Unmarshal(data, &legacy); jsonErr == nil {
			if legacy.WorktreeBaseDir != "" && legacy.MainRepoDir != "" && len(cfg.Repos) == 0 {
				cfg.Repos[legacy.MainRepoDir] = &RepoConfig{
					RepoPath:        legacy.MainRepoDir,
					WorktreeBaseDir: legacy.WorktreeBaseDir,
				}
			}
		}
	}

	return cfg, nil
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

	return atomicWriteFile(filepath.Join(configDir, "config.json"), data, 0644)
}

// LoadStackConfig loads stack metadata and branch cache for a specific repo from $HOME/.ezstack/stacks.json
// It handles migration from older formats using a versioned migration chain.
func LoadStackConfig(repoDir string) (*StackConfig, error) {
	configDir, err := ConfigDir()
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, err
	}

	stackPath := filepath.Join(configDir, "stacks.json")
	data, err := os.ReadFile(stackPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No stacks.json yet — bootstrap from cache.json if present
			// by running migration from v1→v2 on an empty v1 file
			emptyV1 := stackConfigFile{Version: 1, Repos: make(map[string]*repoData)}
			emptyData, _ := json.MarshalIndent(emptyV1, "", "  ")
			migratedData, migErr := migrateStackConfig(emptyData, 1, currentStackConfigVersion)
			if migErr == nil {
				var check stackConfigFile
				if json.Unmarshal(migratedData, &check) == nil && len(check.Repos) > 0 {
					atomicWriteFile(stackPath, migratedData, 0644)
					data = migratedData
				}
			}

			if data == nil {
				return &StackConfig{
					Stacks: make(map[string]*Stack),
					Cache: &CacheConfig{
						Branches: make(map[string]*BranchCache),
						repoDir:  repoDir,
					},
					repoDir: repoDir,
				}, nil
			}
		} else {
			return nil, err
		}
	}

	var versionCheck struct {
		Version int `json:"version"`
	}
	json.Unmarshal(data, &versionCheck)

	if versionCheck.Version < currentStackConfigVersion {
		data, err = migrateStackConfig(data, versionCheck.Version, currentStackConfigVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to migrate stacks.json: %w", err)
		}
		atomicWriteFile(stackPath, data, 0644) // write back so migration only runs once
	}

	var file stackConfigFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}

	if file.Repos == nil {
		file.Repos = make(map[string]*repoData)
	}

	rd := file.Repos[repoDir]
	if rd == nil {
		rd = &repoData{
			Stacks:   make(map[string]*Stack),
			Branches: make(map[string]*BranchCache),
		}
	}
	if rd.Stacks == nil {
		rd.Stacks = make(map[string]*Stack)
	}
	if rd.Branches == nil {
		rd.Branches = make(map[string]*BranchCache)
	}

	sc := &StackConfig{
		Stacks: rd.Stacks,
		Cache: &CacheConfig{
			Branches: rd.Branches,
			repoDir:  repoDir,
		},
		repoDir: repoDir,
	}

	for hash, stack := range sc.Stacks {
		stack.Hash = hash
		stack.cache = sc.Cache
		stack.PopulateBranches()
	}

	return sc, nil
}

// IsFullyMerged returns true if every branch in the stack is marked as merged
func (s *Stack) IsFullyMerged(cache *CacheConfig) bool {
	branches := s.GetBranches(cache)
	if len(branches) == 0 {
		return false
	}
	for _, b := range branches {
		if !b.IsMerged {
			return false
		}
	}
	return true
}

// PopulateBranches rebuilds the Branches slice from the Tree structure
// This should be called after loading or after modifying the Tree
func (s *Stack) PopulateBranches() {
	s.Branches = s.GetBranches(s.cache)
}

// SetCache sets the cache for this stack, allowing branch metadata to be loaded
func (s *Stack) SetCache(cache *CacheConfig) {
	s.cache = cache
}

// PopulateBranchesWithCache rebuilds the Branches slice using the provided cache
func (s *Stack) PopulateBranchesWithCache(cache *CacheConfig) {
	s.cache = cache
	s.Branches = s.GetBranches(cache)
}

// Save saves the stack config and cache for this repo to $HOME/.ezstack/stacks.json
func (sc *StackConfig) Save(repoDir string) error {
	configDir, err := ConfigDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	stackPath := filepath.Join(configDir, "stacks.json")

	// Load existing file first to preserve other repos' data
	var file stackConfigFile
	data, err := os.ReadFile(stackPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		file.Repos = make(map[string]*repoData)
	} else {
		if err := json.Unmarshal(data, &file); err != nil {
			return err
		}
		if file.Repos == nil {
			file.Repos = make(map[string]*repoData)
		}
	}

	targetRepo := sc.repoDir
	if targetRepo == "" {
		targetRepo = repoDir
	}

	branches := make(map[string]*BranchCache)
	if sc.Cache != nil {
		branches = sc.Cache.Branches
	}

	file.Version = currentStackConfigVersion
	file.Repos[targetRepo] = &repoData{
		Stacks:   sc.Stacks,
		Branches: branches,
	}

	newData, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}

	return atomicWriteFile(stackPath, newData, 0644)
}

// LoadCacheConfig loads cached branch metadata. This now delegates to the combined stacks file.
// Kept for backward compatibility with callers that load cache separately.
func LoadCacheConfig(repoDir string) (*CacheConfig, error) {
	configDir, err := ConfigDir()
	if err != nil {
		return nil, err
	}

	// First try loading from the combined stacks.json
	stackPath := filepath.Join(configDir, "stacks.json")
	data, err := os.ReadFile(stackPath)
	if err == nil {
		var file stackConfigFile
		if err := json.Unmarshal(data, &file); err == nil && file.Repos != nil {
			if rd, ok := file.Repos[repoDir]; ok && rd != nil && rd.Branches != nil {
				return &CacheConfig{
					Branches: rd.Branches,
					repoDir:  repoDir,
				}, nil
			}
		}
	}

	// Fall back to legacy cache.json
	cachePath := filepath.Join(configDir, "cache.json")
	data, err = os.ReadFile(cachePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &CacheConfig{
				Branches: make(map[string]*BranchCache),
				repoDir:  repoDir,
			}, nil
		}
		return nil, err
	}

	var cacheFile map[string]*CacheConfig
	if err := json.Unmarshal(data, &cacheFile); err != nil {
		return nil, err
	}

	cc := cacheFile[repoDir]
	if cc == nil {
		cc = &CacheConfig{
			Branches: make(map[string]*BranchCache),
		}
	}
	if cc.Branches == nil {
		cc.Branches = make(map[string]*BranchCache)
	}
	cc.repoDir = repoDir

	return cc, nil
}

// GetBranchCache returns cached metadata for a branch
func (cc *CacheConfig) GetBranchCache(branchName string) *BranchCache {
	if cc.Branches == nil {
		return nil
	}
	return cc.Branches[branchName]
}

// SetBranchCache sets cached metadata for a branch
func (cc *CacheConfig) SetBranchCache(branchName string, cache *BranchCache) {
	if cc.Branches == nil {
		cc.Branches = make(map[string]*BranchCache)
	}
	cc.Branches[branchName] = cache
}

// Save writes the cache data back to the combined stacks.json file.
// This loads the current stacks.json, updates the branches for this repo, and writes it back atomically.
func (cc *CacheConfig) Save(repoDir string) error {
	configDir, err := ConfigDir()
	if err != nil {
		return err
	}

	stackPath := filepath.Join(configDir, "stacks.json")
	var file stackConfigFile

	data, err := os.ReadFile(stackPath)
	if err == nil {
		json.Unmarshal(data, &file)
	}
	if file.Repos == nil {
		file.Repos = make(map[string]*repoData)
	}

	rd := file.Repos[repoDir]
	if rd == nil {
		rd = &repoData{
			Stacks: make(map[string]*Stack),
		}
		file.Repos[repoDir] = rd
	}

	file.Version = currentStackConfigVersion
	rd.Branches = cc.Branches

	newData, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}

	return atomicWriteFile(stackPath, newData, 0644)
}

// GetBranches returns a flat list of branches from the tree structure
// Branches are returned in depth-first order with siblings sorted alphabetically
// The cache is used to populate metadata fields
func (s *Stack) GetBranches(cache *CacheConfig) []*Branch {
	var branches []*Branch
	// Both treeParent and effectiveParent start as Root (e.g., "main")
	s.walkTree(s.Root, s.Root, s.Tree, cache, &branches)
	return branches
}

// walkTree recursively walks the tree in depth-first order
// effectiveParent is the nearest non-merged ancestor (used for git operations)
// treeParent is the actual tree parent (used for display hierarchy tracking)
func (s *Stack) walkTree(treeParent, effectiveParent string, tree BranchTree, cache *CacheConfig, branches *[]*Branch) {
	keys := make([]string, 0, len(tree))
	for k := range tree {
		keys = append(keys, k)
	}
	sortStrings(keys)

	for _, branchName := range keys {
		children := tree[branchName]

		isMerged := false
		if cache != nil {
			if bc := cache.GetBranchCache(branchName); bc != nil {
				isMerged = bc.IsMerged
			}
		}

		// Parent is the effective parent (nearest non-merged ancestor) for git operations.
		// BaseBranch is the original tree parent, used for display ordering.
		branch := &Branch{
			Name:       branchName,
			Parent:     effectiveParent,
			BaseBranch: treeParent,
		}

		if cache != nil {
			if bc := cache.GetBranchCache(branchName); bc != nil {
				branch.WorktreePath = bc.WorktreePath
				branch.PRNumber = bc.PRNumber
				branch.PRUrl = bc.PRUrl
				branch.PRState = bc.PRState
				branch.IsMerged = bc.IsMerged
				branch.IsRemote = bc.IsRemote
			}
		}

		*branches = append(*branches, branch)

		// Merged branches pass their effective parent down so children rebase onto the right base.
		childEffectiveParent := branchName
		if isMerged {
			childEffectiveParent = effectiveParent
		}

		s.walkTree(branchName, childEffectiveParent, children, cache, branches)
	}
}

// sortStrings sorts a slice of strings alphabetically (simple bubble sort)
func sortStrings(s []string) {
	for i := 0; i < len(s)-1; i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// AddBranch adds a branch to the stack tree under the specified parent
func (s *Stack) AddBranch(branchName, parentName string) {
	if s.Tree == nil {
		s.Tree = make(BranchTree)
	}

	if parentName == s.Root {
		s.Tree[branchName] = make(BranchTree)
		return
	}

	s.addBranchToTree(s.Tree, branchName, parentName)
}

// addBranchToTree recursively finds the parent and adds the child
func (s *Stack) addBranchToTree(tree BranchTree, branchName, parentName string) bool {
	for name, children := range tree {
		if name == parentName {
			if children == nil {
				tree[name] = make(BranchTree)
			}
			tree[name][branchName] = make(BranchTree)
			return true
		}
		if s.addBranchToTree(children, branchName, parentName) {
			return true
		}
	}
	return false
}

// RemoveBranch removes a branch from the stack tree
// If the branch has children, they are moved up to the branch's parent
func (s *Stack) RemoveBranch(branchName string) {
	s.removeBranchFromTree(s.Tree, branchName)
}

// removeBranchFromTree recursively finds and removes the branch
func (s *Stack) removeBranchFromTree(tree BranchTree, branchName string) bool {
	for name, children := range tree {
		if name == branchName {
			// Move children up to this branch's parent (which is the current tree)
			for childName, childTree := range children {
				tree[childName] = childTree
			}
			delete(tree, branchName)
			return true
		}
		if s.removeBranchFromTree(children, branchName) {
			return true
		}
	}
	return false
}

// ReparentBranch moves a branch to be under a new parent
// If newParent is empty or matches the root, the branch becomes a root-level branch
func (s *Stack) ReparentBranch(branchName, newParent string) {
	// First, find and remove the branch (keeping its children)
	var branchChildren BranchTree
	s.findAndExtractBranch(s.Tree, branchName, &branchChildren)

	// Then add it under the new parent
	if newParent == "" || newParent == s.Root {
		// Make it a root-level branch
		s.Tree[branchName] = branchChildren
	} else {
		s.addBranchWithChildren(s.Tree, branchName, newParent, branchChildren)
	}
}

// findAndExtractBranch finds a branch and extracts it with its children
func (s *Stack) findAndExtractBranch(tree BranchTree, branchName string, children *BranchTree) bool {
	for name, subtree := range tree {
		if name == branchName {
			*children = subtree
			delete(tree, branchName)
			return true
		}
		if s.findAndExtractBranch(subtree, branchName, children) {
			return true
		}
	}
	return false
}

// addBranchWithChildren adds a branch with its existing children under a parent
func (s *Stack) addBranchWithChildren(tree BranchTree, branchName, parentName string, children BranchTree) bool {
	for name, subtree := range tree {
		if name == parentName {
			tree[name][branchName] = children
			return true
		}
		if s.addBranchWithChildren(subtree, branchName, parentName, children) {
			return true
		}
	}
	return false
}

// FindBranch finds a branch in the tree and returns its parent name
func (s *Stack) FindBranch(branchName string) (parent string, found bool) {
	return s.findBranchInTree(s.Tree, branchName, s.Root)
}

// findBranchInTree recursively searches for a branch
func (s *Stack) findBranchInTree(tree BranchTree, branchName, parent string) (string, bool) {
	for name, children := range tree {
		if name == branchName {
			return parent, true
		}
		if p, found := s.findBranchInTree(children, branchName, name); found {
			return p, true
		}
	}
	return "", false
}

// HasBranch returns true if the branch exists in the stack
func (s *Stack) HasBranch(branchName string) bool {
	_, found := s.FindBranch(branchName)
	return found
}

// GetChildren returns the immediate children of a branch
func (s *Stack) GetChildren(branchName string) []string {
	children := s.findChildrenInTree(s.Tree, branchName)
	sortStrings(children)
	return children
}

// findChildrenInTree finds children of a branch in the tree
func (s *Stack) findChildrenInTree(tree BranchTree, branchName string) []string {
	for name, children := range tree {
		if name == branchName {
			result := make([]string, 0, len(children))
			for childName := range children {
				result = append(result, childName)
			}
			return result
		}
		if result := s.findChildrenInTree(children, branchName); result != nil {
			return result
		}
	}
	return nil
}

// ExtractSubtree removes a branch and its entire subtree from the stack and returns the subtree
func (s *Stack) ExtractSubtree(branchName string) BranchTree {
	var subtree BranchTree
	s.extractSubtreeFromTree(s.Tree, branchName, &subtree)
	return subtree
}

// extractSubtreeFromTree recursively finds and extracts a subtree
func (s *Stack) extractSubtreeFromTree(tree BranchTree, branchName string, subtree *BranchTree) bool {
	for name, children := range tree {
		if name == branchName {
			// Found the branch - extract its entire subtree (including itself)
			*subtree = children
			delete(tree, branchName)
			return true
		}
		if s.extractSubtreeFromTree(children, branchName, subtree) {
			return true
		}
	}
	return false
}

// RenameBranchInTree renames a branch in the tree, preserving its children and position
func (s *Stack) RenameBranchInTree(oldName, newName string) bool {
	return s.renameBranchInTree(s.Tree, oldName, newName)
}

// renameBranchInTree recursively finds and renames a branch
func (s *Stack) renameBranchInTree(tree BranchTree, oldName, newName string) bool {
	for name, children := range tree {
		if name == oldName {
			tree[newName] = children
			delete(tree, oldName)
			return true
		}
		if s.renameBranchInTree(children, oldName, newName) {
			return true
		}
	}
	return false
}

// AddSubtree adds a branch with its subtree under a parent
func (s *Stack) AddSubtree(branchName string, subtree BranchTree, parentName string) {
	if parentName == s.Root || parentName == "" {
		// Add as root-level branch
		s.Tree[branchName] = subtree
	} else {
		// Add under parent
		s.addSubtreeUnderParent(s.Tree, branchName, subtree, parentName)
	}
}

// addSubtreeUnderParent recursively finds parent and adds subtree
func (s *Stack) addSubtreeUnderParent(tree BranchTree, branchName string, subtree BranchTree, parentName string) bool {
	for name, children := range tree {
		if name == parentName {
			tree[name][branchName] = subtree
			return true
		}
		if s.addSubtreeUnderParent(children, branchName, subtree, parentName) {
			return true
		}
	}
	return false
}

// SortBranchesTopologically sorts branches so parents come before children
// This ensures the display shows the correct parent -> child order
// IMPORTANT: When a parent branch is merged and its children are reparented to main,
// the merged branch should still appear in its original position (before its former children).
// We use BaseBranch to detect the original parent-child relationships.
func SortBranchesTopologically(branches []*Branch) []*Branch {
	if len(branches) <= 1 {
		return branches
	}

	branchMap := make(map[string]*Branch)
	for _, b := range branches {
		branchMap[b.Name] = b
	}

	// Build children map using both current Parent and original BaseBranch so that
	// reparented (merged) branches stay in their original display position.
	children := make(map[string][]*Branch)
	var roots []*Branch

	for _, b := range branches {
		_, parentInStack := branchMap[b.Parent]
		_, baseInStack := branchMap[b.BaseBranch]

		if parentInStack {
			children[b.Parent] = append(children[b.Parent], b)
		} else if baseInStack && b.BaseBranch != b.Parent {
			// Branch was reparented; keep it under the original parent for display.
			children[b.BaseBranch] = append(children[b.BaseBranch], b)
		} else {
			roots = append(roots, b)
		}
	}

	originalIndex := make(map[string]int)
	for i, b := range branches {
		originalIndex[b.Name] = i
	}

	sortByOriginalIndex := func(slice []*Branch) {
		for i := 0; i < len(slice)-1; i++ {
			for j := i + 1; j < len(slice); j++ {
				if originalIndex[slice[i].Name] > originalIndex[slice[j].Name] {
					slice[i], slice[j] = slice[j], slice[i]
				}
			}
		}
	}
	sortByOriginalIndex(roots)
	for parent := range children {
		sortByOriginalIndex(children[parent])
	}

	// BFS
	var sorted []*Branch
	queue := roots
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		sorted = append(sorted, current)
		for _, child := range children[current.Name] {
			queue = append(queue, child)
		}
	}

	// Safety net: append any branches missed due to unexpected graph structure.
	if len(sorted) < len(branches) {
		inSorted := make(map[string]bool)
		for _, b := range sorted {
			inSorted[b.Name] = true
		}
		for _, b := range branches {
			if !inSorted[b.Name] {
				sorted = append(sorted, b)
			}
		}
	}

	return sorted
}
