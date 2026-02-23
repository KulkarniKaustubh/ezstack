package config

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
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
const currentStackConfigVersion = 2

// stackConfigFile is the on-disk format that stores stacks for all repos
type stackConfigFile struct {
	Version int                      `json:"version"`
	Repos   map[string]*repoData    `json:"repos"`
}

// StackConfig holds metadata about stacks for a single repo
type StackConfig struct {
	Stacks  map[string]*Stack `json:"stacks"`
	Cache   *CacheConfig      `json:"-"` // loaded alongside stacks, not serialized separately
	repoDir string            // internal, not serialized - used for saving
}

// Stack represents a chain of stacked branches as a tree
type Stack struct {
	Name     string       `json:"name"`
	Hash     string       `json:"hash"`           // 7-char hex hash for identification
	Root     string       `json:"root"`            // The base branch (usually "main")
	Tree     BranchTree   `json:"tree"`            // The tree of branches
	Branches []*Branch    `json:"-"`               // Runtime-only: populated from Tree for backward compatibility
	cache    *CacheConfig // Runtime-only: reference to cache for metadata
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
	IsMerged     bool   `json:"is_merged,omitempty"`
	IsRemote     bool   `json:"is_remote,omitempty"`
}

// CacheConfig holds cached branch metadata for a repo
type CacheConfig struct {
	Branches map[string]*BranchCache `json:"branches"`
	repoDir  string
}

// Branch represents a single branch in a stack (used internally for compatibility)
// This is constructed from the tree structure and cache at runtime
type Branch struct {
	Name         string `json:"name"`
	Parent       string `json:"parent"`        // Parent branch name
	WorktreePath string `json:"worktree_path"` // Path to the worktree
	PRNumber     int    `json:"pr_number,omitempty"`
	PRUrl        string `json:"pr_url,omitempty"`
	BaseBranch   string `json:"base_branch"`         // The branch this PR targets (same as Parent for display)
	IsRemote     bool   `json:"is_remote,omitempty"` // True if this is someone else's branch
	IsMerged     bool   `json:"is_merged,omitempty"` // True if this branch's PR has been merged
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
	var legacyFile legacyStackConfigFile
	if err := json.Unmarshal(data, &legacyFile); err != nil {
		return nil, err
	}

	if legacyFile.Repos == nil {
		// Nothing to migrate, just set version
		result := stackConfigFile{
			Version: 1,
			Repos:   make(map[string]*repoData),
		}
		return json.MarshalIndent(result, "", "  ")
	}

	result := stackConfigFile{
		Version: 1,
		Repos:   make(map[string]*repoData),
	}

	for repoPath, legacySC := range legacyFile.Repos {
		if legacySC == nil {
			continue
		}

		rd := &repoData{
			Stacks:   make(map[string]*Stack),
			Branches: make(map[string]*BranchCache),
		}

		for stackName, legacyStack := range legacySC.Stacks {
			if legacyStack == nil {
				continue
			}

			// Build set of branch names in this stack
			branchSet := make(map[string]bool)
			for _, b := range legacyStack.Branches {
				branchSet[b.Name] = true
			}

			// Find the root (parent not in the stack)
			root := "main"
			for _, b := range legacyStack.Branches {
				if !branchSet[b.Parent] {
					root = b.Parent
					break
				}
			}

			// Build children map
			children := make(map[string][]string)
			for _, b := range legacyStack.Branches {
				parent := b.Parent
				if !branchSet[parent] {
					parent = root
				}
				children[parent] = append(children[parent], b.Name)
			}

			// Build tree recursively
			var buildTree func(parent string) BranchTree
			buildTree = func(parent string) BranchTree {
				tree := make(BranchTree)
				for _, childName := range children[parent] {
					tree[childName] = buildTree(childName)
				}
				return tree
			}

			tree := buildTree(root)

			// Move branch metadata to repo-level cache
			for _, b := range legacyStack.Branches {
				rd.Branches[b.Name] = &BranchCache{
					WorktreePath: b.WorktreePath,
					PRNumber:     b.PRNumber,
					PRUrl:        b.PRUrl,
					IsMerged:     b.IsMerged,
					IsRemote:     b.IsRemote,
				}
			}

			rd.Stacks[stackName] = &Stack{
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
	var file stackConfigFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}

	file.Version = 2
	if file.Repos == nil {
		file.Repos = make(map[string]*repoData)
	}

	// Merge cache.json if it exists
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
						rd = &repoData{
							Stacks:   make(map[string]*Stack),
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

	// Generate hashes for stacks that don't have one
	for _, rd := range file.Repos {
		if rd == nil {
			continue
		}
		for _, stack := range rd.Stacks {
			if stack != nil && stack.Hash == "" {
				stack.Hash = GenerateStackHash(stack.Name)
			}
		}
		if rd.Branches == nil {
			rd.Branches = make(map[string]*BranchCache)
		}
	}

	return json.MarshalIndent(file, "", "  ")
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

	return atomicWriteFile(filepath.Join(configDir, "config.json"), data, 0644)
}

// LoadStackConfig loads stack metadata and branch cache for a specific repo from $HOME/.ezstack/stacks.json
// It handles migration from older formats using a versioned migration chain.
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
			// No stacks.json yet — check if there's a cache.json to bootstrap from
			// by running migration from v1→v2 on an empty v1 file
			emptyV1 := stackConfigFile{Version: 1, Repos: make(map[string]*repoData)}
			emptyData, _ := json.MarshalIndent(emptyV1, "", "  ")
			migratedData, migErr := migrateStackConfig(emptyData, 1, currentStackConfigVersion)
			if migErr == nil {
				// Check if migration produced any data worth saving
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

	// Check file version and run migrations if needed
	var versionCheck struct {
		Version int `json:"version"`
	}
	json.Unmarshal(data, &versionCheck)

	if versionCheck.Version < currentStackConfigVersion {
		data, err = migrateStackConfig(data, versionCheck.Version, currentStackConfigVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to migrate stacks.json: %w", err)
		}
		// Write migrated data back so migration only runs once
		atomicWriteFile(stackPath, data, 0644)
	}

	// Parse the (possibly migrated) data
	var file stackConfigFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}

	if file.Repos == nil {
		file.Repos = make(map[string]*repoData)
	}

	// Get the data for this specific repo
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

	// Populate Branches slice for each stack using the combined cache
	for _, stack := range sc.Stacks {
		stack.cache = sc.Cache
		stack.PopulateBranches()
	}

	return sc, nil
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
		file.Repos = make(map[string]*repoData)
	} else {
		if err := json.Unmarshal(data, &file); err != nil {
			return err
		}
		if file.Repos == nil {
			file.Repos = make(map[string]*repoData)
		}
	}

	// Use the stored repoDir if available, otherwise use the parameter
	targetRepo := sc.repoDir
	if targetRepo == "" {
		targetRepo = repoDir
	}

	// Build the combined repo data
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
	// Get sorted keys for consistent ordering
	keys := make([]string, 0, len(tree))
	for k := range tree {
		keys = append(keys, k)
	}
	sortStrings(keys)

	for _, branchName := range keys {
		children := tree[branchName]

		// Check if this branch is merged
		isMerged := false
		if cache != nil {
			if bc := cache.GetBranchCache(branchName); bc != nil {
				isMerged = bc.IsMerged
			}
		}

		// Create branch object
		// Parent is the effective parent (nearest non-merged ancestor) for git operations
		// BaseBranch tracks the original tree parent for display purposes
		branch := &Branch{
			Name:       branchName,
			Parent:     effectiveParent,
			BaseBranch: treeParent,
		}

		// Populate from cache if available
		if cache != nil {
			if bc := cache.GetBranchCache(branchName); bc != nil {
				branch.WorktreePath = bc.WorktreePath
				branch.PRNumber = bc.PRNumber
				branch.PRUrl = bc.PRUrl
				branch.IsMerged = bc.IsMerged
				branch.IsRemote = bc.IsRemote
			}
		}

		*branches = append(*branches, branch)

		// For children: if this branch is merged, they inherit our effective parent
		// Otherwise, this branch becomes the effective parent
		childEffectiveParent := branchName
		if isMerged {
			childEffectiveParent = effectiveParent
		}

		// Recurse into children
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

	// If parent is the root (main), add directly to the tree
	if parentName == s.Root {
		s.Tree[branchName] = make(BranchTree)
		return
	}

	// Find the parent in the tree and add the child
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
	s.removeBranchFromTree(s.Tree, branchName, s.Root)
}

// removeBranchFromTree recursively finds and removes the branch
func (s *Stack) removeBranchFromTree(tree BranchTree, branchName, parent string) bool {
	for name, children := range tree {
		if name == branchName {
			// Move children up to this branch's parent (which is the current tree)
			for childName, childTree := range children {
				tree[childName] = childTree
			}
			delete(tree, branchName)
			return true
		}
		if s.removeBranchFromTree(children, branchName, name) {
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

	// Build a map of branch name -> branch for quick lookup
	branchMap := make(map[string]*Branch)
	for _, b := range branches {
		branchMap[b.Name] = b
	}

	// Build children map using BOTH current Parent AND original BaseBranch
	// This preserves the original hierarchy even after reparenting
	children := make(map[string][]*Branch)
	var roots []*Branch

	for _, b := range branches {
		// Check if parent is in this stack
		_, parentInStack := branchMap[b.Parent]
		// Also check if original base branch is in this stack (for reparented branches)
		_, baseInStack := branchMap[b.BaseBranch]

		if parentInStack {
			// Current parent is in stack - use it
			children[b.Parent] = append(children[b.Parent], b)
		} else if baseInStack && b.BaseBranch != b.Parent {
			// Branch was reparented (BaseBranch != Parent), but original parent is in stack
			// Keep it as a child of the original parent for display purposes
			children[b.BaseBranch] = append(children[b.BaseBranch], b)
		} else {
			// Parent is external (main) - this is a root
			roots = append(roots, b)
		}
	}

	// Build a map of branch name -> original index for stable sorting
	originalIndex := make(map[string]int)
	for i, b := range branches {
		originalIndex[b.Name] = i
	}

	// Sort roots by original index to maintain stable order
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

	// Sort children by original index too
	for parent := range children {
		sortByOriginalIndex(children[parent])
	}

	// BFS to build sorted list
	var sorted []*Branch
	queue := roots

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		sorted = append(sorted, current)

		// Add children to queue
		for _, child := range children[current.Name] {
			queue = append(queue, child)
		}
	}

	// If we didn't get all branches (shouldn't happen), append remaining
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
