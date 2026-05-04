package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

// PRNumberFromURL extracts the PR number from a GitHub PR URL.
// e.g. "https://github.com/owner/repo/pull/123" → 123
// Returns 0 if the URL is empty or unparseable.
func PRNumberFromURL(url string) int {
	if url == "" {
		return 0
	}
	parts := strings.Split(strings.TrimRight(url, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-2] != "pull" {
		return 0
	}
	n, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0
	}
	return n
}

// newViper builds a fresh Viper instance with the EZSTACK_-prefixed env
// binding and built-in defaults. We do NOT share a package-level Viper
// across calls: Viper's Set*/ReadInConfig path mutates internal maps
// without synchronization, so concurrent Load() in the same process
// (e.g. ezs-mcp serving parallel tool calls, or any other goroutine
// driver) would race the global singleton. A per-call instance is
// cheap (a few map allocations) and makes Load() safe to call from
// multiple goroutines.
func newViper() *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("EZSTACK")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	v.SetDefault("default_base_branch", "main")
	return v
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
	UseWorktrees        *bool  `json:"use_worktrees,omitempty"`
	AutoDraftWipCommits *bool  `json:"auto_draft_wip_commits,omitempty"`
	InitSubmodules      *bool  `json:"init_submodules,omitempty"` // Mirror main worktree's initialized submodules into new worktrees (default: true)
	SyncStrategy        string `json:"sync_strategy,omitempty"`   // "rebase" (default) or "merge"
	AgentCommand        string `json:"agent_command,omitempty"`   // AI agent CLI command (default: "claude")
}

// GetAgentCommand returns the configured agent command, defaulting to "claude".
func (rc *RepoConfig) GetAgentCommand() string {
	if rc != nil && rc.AgentCommand != "" {
		return rc.AgentCommand
	}
	return "claude"
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

// GetCdAfterNew returns whether to cd after creating a new worktree (default: true)
func (c *Config) GetCdAfterNew(repoPath string) bool {
	if repoCfg := c.GetRepoConfig(repoPath); repoCfg != nil && repoCfg.CdAfterNew != nil {
		return *repoCfg.CdAfterNew
	}
	return true
}

// GetUseWorktrees returns whether to use worktrees for new branches (default: true)
func (c *Config) GetUseWorktrees(repoPath string) bool {
	if repoCfg := c.GetRepoConfig(repoPath); repoCfg != nil && repoCfg.UseWorktrees != nil {
		return *repoCfg.UseWorktrees
	}
	return true
}

// GetInitSubmodules returns whether to mirror the main worktree's initialized
// submodules into newly created worktrees (default: true).
func (c *Config) GetInitSubmodules(repoPath string) bool {
	if repoCfg := c.GetRepoConfig(repoPath); repoCfg != nil && repoCfg.InitSubmodules != nil {
		return *repoCfg.InitSubmodules
	}
	return true
}

// GetSyncStrategy returns the sync strategy for a repo ("rebase" or "merge", default "rebase")
func (c *Config) GetSyncStrategy(repoPath string) string {
	if repoCfg := c.GetRepoConfig(repoPath); repoCfg != nil && repoCfg.SyncStrategy != "" {
		return repoCfg.SyncStrategy
	}
	return "rebase"
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

	// origSnapshot captures this repo's data as it existed on disk at load
	// time. Save() uses it as the common ancestor for a three-way merge so
	// that concurrent modifications by another ezstack process don't get
	// silently overwritten. nil for fresh configs.
	origSnapshot *repoData
}

// AgentSessionWorkMode is the mode tag stored alongside an agent session.
// "" (empty) is treated as the work-mode default for entries written before
// mode tracking was added — never write an empty mode for a fresh session.
const (
	AgentSessionWorkMode    = "work"    // `ezs agent` default (work session)
	AgentSessionFeatureMode = "feature" // `ezs agent feature` builder mode
)

// Stack represents a chain of stacked branches as a tree
// Hash is the map key in StackConfig.Stacks and is populated at load time.
type Stack struct {
	Hash             string       `json:"-"`                            // Populated from map key at load time
	Name             string       `json:"name,omitempty"`               // Optional user-given name for the stack
	Root             string       `json:"root"`                         // The base branch (e.g. "main", or a remote branch name)
	RootBase         string       `json:"root_base,omitempty"`          // The branch the root's PR targets (for computing root diff)
	RootPRNumber     int          `json:"-"`                            // Runtime-only: derived from RootPRUrl
	RootPRUrl        string       `json:"root_pr_url,omitempty"`        // PR URL of the root branch (for remote base branches)
	DeleteDeclined   bool         `json:"delete_declined,omitempty"`    // User declined cleanup prompt; don't re-ask
	AgentSessionID   string       `json:"agent_session_id,omitempty"`   // UUID of the AI agent session bound to this stack (used by `ezs agent` to resume)
	AgentSessionMode string       `json:"agent_session_mode,omitempty"` // Mode the session was created in: "work" or "feature". Empty ⇒ legacy entry, treated as "work".
	Tree             BranchTree   `json:"tree"`                         // The tree of branches
	Branches         []*Branch    `json:"-"`                            // Runtime-only: populated from Tree for backward compatibility
	cache            *CacheConfig // Runtime-only: reference to cache for metadata
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
	PRNumber     int    `json:"-"` // Runtime-only: derived from PRUrl via PRNumberFromURL
	PRUrl        string `json:"pr_url,omitempty"`
	PRState      string `json:"pr_state,omitempty"` // Cached: "OPEN", "DRAFT", "MERGED", "CLOSED"
	IsMerged     bool   `json:"is_merged,omitempty"`
	IsRemote     bool   `json:"is_remote,omitempty"`
	Remote       string `json:"remote,omitempty"` // git remote to push to (e.g. fork remote); defaults to "origin"
	// PreSyncCommit is the SHA the branch pointed at before the current sync run
	// began rewriting it. Used as the `oldBase` for `git rebase --onto newParent
	// oldBase` so children of a freshly-rebased parent don't replay the parent's
	// commits and re-encounter conflicts that were already resolved upstream.
	// Persisted across process boundaries so `ezs sync --continue` (a separate
	// invocation) can use it. Cleared when the branch's sync completes cleanly;
	// left set while a rebase/merge is in progress.
	PreSyncCommit string `json:"pre_sync_commit,omitempty"`
	// PreSyncCommitAt is the Unix epoch second at which PreSyncCommit was last
	// recorded. Used by stale-snapshot cleanup to age out snapshots from prior
	// runs that no longer have a worktree to introspect — without this, a
	// crashed checkout-based sync would leave its snapshot in cache forever.
	PreSyncCommitAt int64 `json:"pre_sync_commit_at,omitempty"`
	// AgentSessionID is the UUID of the AI agent session bound to this branch
	// in branch-scoped (`ezs agent --branch`) mode. Used to resume the same
	// session on subsequent `ezs agent` runs against this branch.
	AgentSessionID string `json:"agent_session_id,omitempty"`
	// AgentSessionMode tags how the session was created. Branch-scoped sessions
	// are always work-mode (feature mode requires a stack), so this is set to
	// "work" on write and consumed by `ezs agent ls --feature` to filter rows.
	// Empty on legacy entries written before mode tracking; treated as "work".
	AgentSessionMode string `json:"agent_session_mode,omitempty"`
}

// ClearPRFields zeroes the PR-association fields on this BranchCache while
// preserving worktree, fork-remote, and is_remote metadata. Used by `ezs pr
// unlink` and by recovery paths that detect a cached PR is no longer on
// GitHub.
func (bc *BranchCache) ClearPRFields() {
	bc.PRNumber = 0
	bc.PRUrl = ""
	bc.PRState = ""
	bc.IsMerged = false
}

// CacheConfig holds cached branch metadata for a repo
type CacheConfig struct {
	Branches map[string]*BranchCache `json:"branches"`
	repoDir  string

	// origBranches captures Branches as it existed on disk at load time, so
	// CacheConfig.Save can do a three-way merge against any concurrent
	// peer-process changes instead of wholesale-replacing the on-disk map.
	// Same semantics as StackConfig.origSnapshot.Branches. nil for caches
	// that were not loaded from disk (e.g. tests building CacheConfig
	// in-memory) — Save treats nil as "no changes from us are unmergeable
	// with theirs", which is equivalent to the pre-fix behavior in that
	// edge case.
	origBranches map[string]*BranchCache
}

// Branch represents a single branch in a stack, constructed from the tree and cache at runtime.
type Branch struct {
	Name         string `json:"name"`
	Parent       string `json:"parent"`
	WorktreePath string `json:"worktree_path"`
	PRNumber     int    `json:"-"` // Runtime-only: derived from PRUrl via PRNumberFromURL
	PRUrl        string `json:"pr_url,omitempty"`
	PRState      string `json:"pr_state,omitempty"`  // Cached: "OPEN", "DRAFT", "MERGED", "CLOSED"
	BaseBranch   string `json:"base_branch"`         // original tree parent, used for display ordering
	IsRemote     bool   `json:"is_remote,omitempty"` // branch belongs to another contributor
	IsMerged     bool   `json:"is_merged,omitempty"`
	Remote       string `json:"remote,omitempty"` // Git remote to push to (empty means "origin")
}

// RemoteNoPush is a sentinel value indicating that push is not allowed for this branch
// (e.g., a fork PR where maintainerCanModify is false).
const RemoteNoPush = "_nopush"

// EffectiveRemote returns the remote for this branch, defaulting to "origin".
func (b *Branch) EffectiveRemote() string {
	if b.Remote != "" {
		return b.Remote
	}
	return "origin"
}

// CanPush returns true if push operations are allowed for this branch.
func (b *Branch) CanPush() bool {
	return b.Remote != RemoteNoPush
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
// in the same directory, fsyncing it, renaming over the destination, and then
// fsyncing the parent directory so the rename itself is durable on filesystems
// where rename ordering vs. data persistence isn't otherwise guaranteed
// (APFS, ZFS, NFS).
//
// fsync before rename is what makes "atomic" actually durable: without it, a
// crash between rename and the OS flushing dirty pages can leave the renamed
// file with arbitrary contents (including the empty file the kernel exposes
// when the inode metadata has hit disk but the data block hasn't). For
// stacks.json this means one bad shutdown could surface as "stack vanished"
// rather than "stack reverted to last good save".
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
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	syncDir(dir)
	return nil
}

// syncDir fsyncs a directory so that a recent rename inside it is durable.
// Best-effort: errors are ignored because (a) Windows rejects directory fsync
// with EINVAL and (b) some filesystems return ENOTSUP. The atomic rename has
// already happened; missing the directory sync is a small durability hit, not
// a correctness bug.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	d.Close()
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
						if wErr := atomicWriteFile(cachePath, newCacheData, 0644); wErr != nil {
							fmt.Fprintf(os.Stderr, "Warning: failed to update cache.json: %v\n", wErr)
						}
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
					newStack.RootPRUrl = bc.PRUrl
					newStack.RootPRNumber = PRNumberFromURL(bc.PRUrl)

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
		Version int                  `json:"version"`
		Repos   map[string]*repoData `json:"repos"`
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

	v := newViper()
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

// Save persists the configuration to ~/.ezstack/config.json under the same
// per-process lock model used by StackConfig.Save: acquire flock, reload
// the on-disk file, merge our changes against any concurrent peer's
// changes, then atomicWriteFile.
//
// Without this, two parallel `ezs config set …` runs (or any path that
// auto-saves the global config) silently lost the earlier writer's update.
// The merge is map-level (per-repo): if peer added or modified another
// repo's RepoConfig while we were holding `c` in memory, their entry
// survives. Same-repo concurrent edits resolve last-writer-wins because
// `c.Repos` doesn't carry a load-time snapshot — that's a documented
// limitation, not a regression: pre-PR, every save was last-writer-wins
// across the whole file.
func (c *Config) Save() error {
	configDir, err := ConfigDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	configPath := filepath.Join(configDir, "config.json")

	lock, lockErr := acquireFileLock(configPath + ".lock")
	if lockErr != nil {
		return lockErr
	}
	defer lock.release()

	// Reload disk state under the lock so we can merge against it. If the
	// file doesn't exist (first save), there's nothing to merge.
	merged := c
	if data, readErr := os.ReadFile(configPath); readErr == nil {
		var disk Config
		if err := json.Unmarshal(data, &disk); err == nil {
			merged = mergeGlobalConfig(&disk, c)
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}

	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}

	if err := atomicWriteFile(configPath, data, 0644); err != nil {
		return err
	}

	// Mirror merged result back into the receiver so any subsequent
	// in-process Save() picks up peer additions instead of clobbering them.
	*c = *merged
	return nil
}

// mergeGlobalConfig combines the in-memory config (`mine`) with the disk
// state read under the lock (`theirs`). Scalar fields prefer mine
// (last-writer-wins, preserving the pre-fix behavior); the Repos map is
// merged so a peer's addition of a different repo's config survives our
// save.
func mergeGlobalConfig(theirs, mine *Config) *Config {
	if mine == nil {
		return theirs
	}
	if theirs == nil {
		return mine
	}
	out := &Config{
		DefaultBaseBranch: mine.DefaultBaseBranch,
		GitHubToken:       mine.GitHubToken,
		Repos:             make(map[string]*RepoConfig),
	}
	// Preserve scalar last-writer-wins for fields we directly set.
	if out.DefaultBaseBranch == "" && theirs.DefaultBaseBranch != "" {
		// Don't fight a peer who set a default we never had.
		out.DefaultBaseBranch = theirs.DefaultBaseBranch
	}
	if out.GitHubToken == "" && theirs.GitHubToken != "" {
		out.GitHubToken = theirs.GitHubToken
	}
	for path, rc := range theirs.Repos {
		out.Repos[path] = rc
	}
	for path, rc := range mine.Repos {
		out.Repos[path] = rc
	}
	return out
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
					if err := atomicWriteFile(stackPath, migratedData, 0644); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: failed to persist bootstrap migration: %v\n", err)
					}
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
	if err := json.Unmarshal(data, &versionCheck); err != nil {
		return nil, fmt.Errorf("failed to read stacks.json version: %w", err)
	}

	if versionCheck.Version < currentStackConfigVersion {
		data, err = migrateStackConfig(data, versionCheck.Version, currentStackConfigVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to migrate stacks.json: %w", err)
		}
		// Persist the migrated form under the same lock model used by Save
		// so two processes opening an old config concurrently can't both
		// write at once. The migration is idempotent (re-running it on
		// already-migrated data is a no-op), so worst-case both processes
		// write the same bytes; the lock is purely a "no torn writes"
		// guarantee.
		if lock, lockErr := acquireFileLock(stackPath + ".lock"); lockErr == nil {
			// Re-read disk under the lock — if a peer migrated first, prefer
			// their result (still our target version) rather than rewriting.
			if cur, readErr := os.ReadFile(stackPath); readErr == nil {
				var curVer struct {
					Version int `json:"version"`
				}
				if json.Unmarshal(cur, &curVer) == nil && curVer.Version >= currentStackConfigVersion {
					data = cur
					lock.release()
					goto migrated
				}
			}
			if err := atomicWriteFile(stackPath, data, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to persist migration: %v\n", err)
			}
			lock.release()
		} else if errors.Is(lockErr, ErrLockTimeout) {
			// Peer is actively holding the lock and is also migrating
			// (because they read the same old-version file we did).
			// Migration is idempotent, so our in-memory `data` is already
			// the correct target form — skip the persist step. The peer's
			// write will land; the next Load will pick up the persisted
			// copy. Racing with an unlocked atomicWriteFile here would
			// resurrect exactly the torn-write bug the lock prevents.
			fmt.Fprintf(os.Stderr, "ezs: stacks.json migration: peer process is migrating; skipping our persist step (their write will land)\n")
		} else {
			// Lock subsystem itself is broken (permission denied, FS limit,
			// missing parent dir). Fall back to an unlocked write so a
			// genuinely busted lock backend doesn't block all use of ezs.
			// Only this branch races; the timeout branch above does not.
			if err := atomicWriteFile(stackPath, data, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to persist migration: %v\n", err)
			}
		}
	migrated:
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
			Branches:     rd.Branches,
			origBranches: snapshotBranches(rd.Branches),
			repoDir:      repoDir,
		},
		repoDir:      repoDir,
		origSnapshot: snapshotRepoData(rd),
	}

	for hash, stack := range sc.Stacks {
		stack.Hash = hash
		stack.cache = sc.Cache
		stack.RootPRNumber = PRNumberFromURL(stack.RootPRUrl)
		stack.PopulateBranches()
	}

	return sc, nil
}

// snapshotBranches deep-copies a map of *BranchCache pointers via a JSON
// round-trip. Used by CacheConfig load paths to capture the on-disk state
// for a later three-way merge in Save. Returns an empty (non-nil) map on
// nil input.
//
// Errors from Marshal/Unmarshal are surfaced via stderr (and the returned
// map ends up empty). Both fields involved are JSON-tagged so this should
// never fail in practice — but if it does, the silent path used to leave
// `out` partially populated, which then made the three-way merge see a
// stale "orig" and could drop concurrent peer updates. Loud failure is
// vastly preferable to silent data loss.
func snapshotBranches(branches map[string]*BranchCache) map[string]*BranchCache {
	out := make(map[string]*BranchCache, len(branches))
	if len(branches) == 0 {
		return out
	}
	buf, err := json.Marshal(branches)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ezstack: snapshotBranches: marshal failed (%v); merge will see empty orig — peer updates may be lost\n", err)
		return out
	}
	if err := json.Unmarshal(buf, &out); err != nil {
		fmt.Fprintf(os.Stderr, "ezstack: snapshotBranches: unmarshal failed (%v); merge will see empty orig — peer updates may be lost\n", err)
		return make(map[string]*BranchCache)
	}
	if out == nil {
		out = make(map[string]*BranchCache)
	}
	return out
}

// snapshotRepoData deep-copies a repoData via a JSON round-trip so that
// later mutations to the live `*Stack` / `*BranchCache` pointers don't
// affect the captured snapshot. Returns an empty (non-nil) repoData on
// nil input so callers can compare without a nil check.
//
// As with snapshotBranches, Marshal/Unmarshal errors are surfaced via
// stderr instead of silently swallowed. A round-trip failure here makes
// the three-way merge see a stale orig and can drop a peer's update.
func snapshotRepoData(rd *repoData) *repoData {
	out := &repoData{
		Stacks:   make(map[string]*Stack),
		Branches: make(map[string]*BranchCache),
	}
	if rd == nil {
		return out
	}
	buf, err := json.Marshal(rd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ezstack: snapshotRepoData: marshal failed (%v); merge will see empty orig — peer updates may be lost\n", err)
		return out
	}
	if err := json.Unmarshal(buf, out); err != nil {
		fmt.Fprintf(os.Stderr, "ezstack: snapshotRepoData: unmarshal failed (%v); merge will see empty orig — peer updates may be lost\n", err)
		return &repoData{
			Stacks:   make(map[string]*Stack),
			Branches: make(map[string]*BranchCache),
		}
	}
	if out.Stacks == nil {
		out.Stacks = make(map[string]*Stack)
	}
	if out.Branches == nil {
		out.Branches = make(map[string]*BranchCache)
	}
	return out
}

// jsonEqual compares two values by their JSON serialization. Returns false
// if either side fails to marshal — we prefer "treat as different" over
// "treat as equal" for safety.
func jsonEqual(a, b any) bool {
	aJSON, errA := json.Marshal(a)
	bJSON, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(aJSON, bJSON)
}

// MergeConflictHook is invoked once per (kind, key) where the three-way
// merge encountered a same-target concurrent edit (mine != orig AND
// theirs != orig AND theirs != mine). Kind is "stack" or "branch". The
// merge resolves last-writer-wins (mine), but the hook gives the caller a
// chance to log or surface the fact that a peer's update is being
// overwritten.
//
// Default writes a one-line warning to stderr. Tests override it to
// capture the call. Set to a no-op func to silence (callers that want
// silence: don't use nil — that path is reserved for "default behavior").
var MergeConflictHook func(kind, key string) = func(kind, key string) {
	fmt.Fprintf(os.Stderr,
		"ezs: warning: concurrent edit to %s %q detected during save; "+
			"another ezs process modified the same %s in between our load and save. "+
			"Their changes were overwritten (last-writer-wins).\n",
		kind, key, kind,
	)
}

// mergeBranches performs a three-way merge over branch-cache maps using
// the same semantics as mergeRepoData (mine wins on us-modified, theirs
// fills in on we-didn't-touch, deletions and additions from either side
// survive). Same-target concurrent edits fire MergeConflictHook with kind
// "branch".
func mergeBranches(orig, mine, theirs map[string]*BranchCache) map[string]*BranchCache {
	if orig == nil {
		orig = map[string]*BranchCache{}
	}
	if theirs == nil {
		theirs = map[string]*BranchCache{}
	}
	merged := make(map[string]*BranchCache)
	notify := func(key string) {
		if MergeConflictHook != nil {
			MergeConflictHook("branch", key)
		}
	}
	seen := make(map[string]bool, len(mine))
	for name, mineB := range mine {
		seen[name] = true
		origB, hadOrig := orig[name]
		if hadOrig && jsonEqual(origB, mineB) {
			if theirB, hasTheirs := theirs[name]; hasTheirs {
				merged[name] = theirB
			}
			continue
		}
		if hadOrig {
			if theirB, hasTheirs := theirs[name]; hasTheirs &&
				!jsonEqual(theirB, origB) && !jsonEqual(theirB, mineB) {
				notify(name)
			}
		}
		merged[name] = mineB
	}
	for name, theirB := range theirs {
		if seen[name] {
			continue
		}
		if _, wasOrig := orig[name]; wasOrig {
			continue // we deleted it
		}
		merged[name] = theirB
	}
	return merged
}

// mergeStacks performs a three-way merge over stack maps using the same
// semantics as mergeBranches (kind="stack" for the conflict hook).
func mergeStacks(orig, mine, theirs map[string]*Stack) map[string]*Stack {
	if orig == nil {
		orig = map[string]*Stack{}
	}
	if theirs == nil {
		theirs = map[string]*Stack{}
	}
	merged := make(map[string]*Stack)
	notify := func(key string) {
		if MergeConflictHook != nil {
			MergeConflictHook("stack", key)
		}
	}
	seen := make(map[string]bool, len(mine))
	for hash, mineS := range mine {
		seen[hash] = true
		origS, hadOrig := orig[hash]
		if hadOrig && jsonEqual(origS, mineS) {
			if theirS, hasTheirs := theirs[hash]; hasTheirs {
				merged[hash] = theirS
			}
			continue
		}
		if hadOrig {
			if theirS, hasTheirs := theirs[hash]; hasTheirs &&
				!jsonEqual(theirS, origS) && !jsonEqual(theirS, mineS) {
				notify(hash)
			}
		}
		merged[hash] = mineS
	}
	for hash, theirS := range theirs {
		if seen[hash] {
			continue
		}
		if _, wasOrig := orig[hash]; wasOrig {
			continue // we deleted it
		}
		merged[hash] = theirS
	}
	return merged
}

// mergeRepoData performs a three-way merge between the disk state at load
// time (orig), the in-memory state we want to write (mine), and the disk
// state right now (theirs, which may include another process's changes).
//
// Per stack/branch:
//   - If we modified it (mine != orig) → take mine
//   - If we didn't touch it (mine == orig) → take theirs (preserves another
//     process's concurrent updates, including deletions)
//   - If we added (in mine, not in orig) → take mine
//   - If we deleted (in orig, not in mine) → omit (deletion wins)
//   - If theirs added (in theirs, not in orig, not in mine) → take theirs
//
// Concurrent modifications to the *same* stack/branch from two processes
// resolve last-writer-wins, but we fire MergeConflictHook so the loss
// isn't silent. The common case (two processes touching different stacks
// of the same repo) remains lossless.
//
// IMPORTANT: when adding a new field to repoData, also extend this
// function. TestMergeRepoData_AllFieldsCovered (config_test.go) uses
// reflection to fail loudly if a new repoData field is left unmerged —
// without that guard the new field's peer-process updates would be
// silently overwritten on every Save.
func mergeRepoData(orig, mine, theirs *repoData) *repoData {
	if orig == nil {
		orig = &repoData{Stacks: map[string]*Stack{}, Branches: map[string]*BranchCache{}}
	}
	if theirs == nil {
		theirs = &repoData{Stacks: map[string]*Stack{}, Branches: map[string]*BranchCache{}}
	}
	if mine == nil {
		mine = &repoData{Stacks: map[string]*Stack{}, Branches: map[string]*BranchCache{}}
	}
	return &repoData{
		Stacks:   mergeStacks(orig.Stacks, mine.Stacks, theirs.Stacks),
		Branches: mergeBranches(orig.Branches, mine.Branches, theirs.Branches),
	}
}

// mergedRepoDataFieldCount returns the number of repoData fields that
// mergeRepoData currently knows how to merge. Updated whenever the
// function gains a new field. TestMergeRepoData_AllFieldsCovered
// cross-checks this against reflect.TypeOf((*repoData)(nil)).Elem().NumField()
// so that adding a field to repoData without extending mergeRepoData
// becomes a hard test failure rather than a latent data-loss bug.
const mergedRepoDataFieldCount = 2

// mergedGlobalConfigFieldCount is the same tripwire for the top-level
// Config struct. mergeGlobalConfig handles three fields today:
// DefaultBaseBranch (scalar, peer-fill-if-empty), GitHubToken (same), and
// Repos (per-repo map merge). If a future contributor adds a fourth field
// — say "Telemetry" or "Notifications" — and forgets to extend
// mergeGlobalConfig, two parallel `ezs config set` calls (or any peer
// process) would silently drop one writer's update of the new field. The
// reflection test in config_audit_test.go catches that at build time.
const mergedGlobalConfigFieldCount = 3

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

	// Serialize concurrent load-modify-save sequences from multiple ezstack
	// processes (e.g. `ezs sync` running in two terminals on different stacks
	// in the same repo). atomicWriteFile makes the write atomic, but without
	// this lock the RMW window is racy and one process's updates can
	// silently overwrite another's.
	lock, lockErr := acquireFileLock(stackPath + ".lock")
	if lockErr != nil {
		return lockErr
	}
	defer lock.release()

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

	mine := &repoData{
		Stacks:   sc.Stacks,
		Branches: branches,
	}

	// Three-way merge against any concurrent on-disk changes since we loaded.
	// Without this, two parallel ezs invocations on different stacks of the
	// same repo silently lose one process's writes.
	merged := mergeRepoData(sc.origSnapshot, mine, file.Repos[targetRepo])

	file.Version = currentStackConfigVersion
	file.Repos[targetRepo] = merged

	newData, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}

	if err := atomicWriteFile(stackPath, newData, 0644); err != nil {
		return err
	}

	// Refresh our snapshot so a subsequent Save in the same process treats the
	// just-written data as the new common ancestor for any further changes.
	sc.origSnapshot = snapshotRepoData(merged)
	return nil
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
					Branches:     rd.Branches,
					origBranches: snapshotBranches(rd.Branches),
					repoDir:      repoDir,
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

// MutateBranchCache atomically loads, modifies, and saves a single branch's
// cache entry under the stacks.json file lock. The mutator receives the
// current entry (or nil if absent) and returns the next value (or nil to
// delete the entry).
//
// Use this instead of LoadCacheConfig + SetBranchCache + Save when only a
// few branches need updating: the load-modify-save pattern is racy across
// processes because Save replaces the whole branches map with the in-memory
// copy, which silently discards updates to other branches that landed
// between the load and the save. MutateBranchCache loads inside the lock
// and only ever rewrites the named branch (plus delete-on-nil), so peer
// writes to other branches survive.
//
// A non-nil error returned by fn aborts the save. The pointer fn returns
// may but need not alias the pointer it received — both work.
func MutateBranchCache(repoDir, branchName string, fn func(current *BranchCache) (next *BranchCache, err error)) error {
	configDir, err := ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}
	stackPath := filepath.Join(configDir, "stacks.json")

	lock, lockErr := acquireFileLock(stackPath + ".lock")
	if lockErr != nil {
		return lockErr
	}
	defer lock.release()

	var file stackConfigFile
	data, err := os.ReadFile(stackPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to read stacks.json: %w", err)
		}
	} else if len(data) > 0 {
		if uErr := json.Unmarshal(data, &file); uErr != nil {
			return fmt.Errorf("failed to parse stacks.json: %w", uErr)
		}
	}
	if file.Repos == nil {
		file.Repos = make(map[string]*repoData)
	}

	rd := file.Repos[repoDir]
	if rd == nil {
		rd = &repoData{Stacks: make(map[string]*Stack)}
		file.Repos[repoDir] = rd
	}
	if rd.Branches == nil {
		rd.Branches = make(map[string]*BranchCache)
	}

	current := rd.Branches[branchName]
	next, mutErr := fn(current)
	if mutErr != nil {
		return mutErr
	}

	// "There was nothing, fn says still nothing" is a real no-op — used by
	// clearPRFromCache against a branch with no cache entry. Skip the file
	// rewrite so callers can call this freely without paying for I/O.
	if next == nil && current == nil {
		return nil
	}

	if next == nil {
		delete(rd.Branches, branchName)
	} else {
		rd.Branches[branchName] = next
	}

	file.Version = currentStackConfigVersion

	newData, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}

	return atomicWriteFile(stackPath, newData, 0644)
}

// Save writes the cache data back to the combined stacks.json file under
// the per-process file lock and a three-way merge against any concurrent
// peer-process changes to the branch cache. Without the merge, a parallel
// `ezs sync` (which writes via StackConfig.Save / MutateBranchCache) could
// add a new branch entry between our load and save, and our wholesale-
// replace of `rd.Branches` would silently delete it.
//
// Prefer MutateBranchCache for narrow updates — it scopes the RMW window to
// a single branch under the same lock and avoids carrying stale state. This
// path remains for callers that need to rewrite multiple entries together.
func (cc *CacheConfig) Save(repoDir string) error {
	configDir, err := ConfigDir()
	if err != nil {
		return err
	}

	stackPath := filepath.Join(configDir, "stacks.json")

	// Serialize with other writers of stacks.json. Without this, a concurrent
	// StackConfig.Save or CacheConfig.Save can clobber our updates in the
	// read-modify-write window below.
	lock, lockErr := acquireFileLock(stackPath + ".lock")
	if lockErr != nil {
		return lockErr
	}
	defer lock.release()

	var file stackConfigFile

	data, err := os.ReadFile(stackPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to read stacks.json: %w", err)
		}
	} else {
		if uErr := json.Unmarshal(data, &file); uErr != nil {
			return fmt.Errorf("failed to parse stacks.json: %w", uErr)
		}
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
	// Three-way merge against the disk's branches: orig is what we read at
	// load time, mine is the in-memory map we want to write, theirs is the
	// branches map currently on disk under this lock. Same semantics as
	// StackConfig.Save's mergeRepoData call.
	rd.Branches = mergeBranches(cc.origBranches, cc.Branches, rd.Branches)

	newData, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}

	if err := atomicWriteFile(stackPath, newData, 0644); err != nil {
		return err
	}

	// Refresh snapshot so a subsequent Save in the same process treats this
	// as the new common ancestor.
	cc.origBranches = snapshotBranches(rd.Branches)
	return nil
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
	sort.Strings(keys)

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
				branch.PRUrl = bc.PRUrl
				branch.PRNumber = PRNumberFromURL(bc.PRUrl)
				branch.PRState = bc.PRState
				branch.IsMerged = bc.IsMerged
				branch.IsRemote = bc.IsRemote
				branch.Remote = bc.Remote
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

// removeBranchFromTree recursively finds and removes the branch.
// When the removed branch has children, they are reparented one level up.
// If a child has the same name as a sibling already at the parent level
// (rare but possible after a rename or import), we merge that child's
// subtree into the existing sibling rather than overwriting it — losing
// the existing subtree would silently drop branches from config.
func (s *Stack) removeBranchFromTree(tree BranchTree, branchName string) bool {
	for name, children := range tree {
		if name == branchName {
			for childName, childTree := range children {
				if existing, collides := tree[childName]; collides {
					mergeBranchTrees(existing, childTree)
				} else {
					tree[childName] = childTree
				}
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

// mergeBranchTrees recursively merges src into dst, preserving any
// non-overlapping subtrees on both sides. When the same name appears in
// both, descend and merge.
func mergeBranchTrees(dst, src BranchTree) {
	for name, srcChildren := range src {
		if dstChildren, ok := dst[name]; ok {
			mergeBranchTrees(dstChildren, srcChildren)
		} else {
			dst[name] = srcChildren
		}
	}
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
		if s.Tree == nil {
			s.Tree = make(BranchTree)
		}
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
			if tree[name] == nil {
				tree[name] = make(BranchTree)
			}
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
	sort.Strings(children)
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

// AddSubtree adds a branch with its subtree under a parent.
// Returns false if parentName was not found in the tree (non-root case).
func (s *Stack) AddSubtree(branchName string, subtree BranchTree, parentName string) bool {
	if parentName == s.Root || parentName == "" {
		// Add as root-level branch
		s.Tree[branchName] = subtree
		return true
	}
	// Add under parent
	return s.addSubtreeUnderParent(s.Tree, branchName, subtree, parentName)
}

// addSubtreeUnderParent recursively finds parent and adds subtree
func (s *Stack) addSubtreeUnderParent(tree BranchTree, branchName string, subtree BranchTree, parentName string) bool {
	for name, children := range tree {
		if name == parentName {
			if tree[name] == nil {
				tree[name] = make(BranchTree)
			}
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
