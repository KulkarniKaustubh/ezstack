package stack

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
)

// Manager handles stack operations
type Manager struct {
	git            *git.Git
	config         *config.Config
	repoConfig     *config.RepoConfig
	stackConfig    *config.StackConfig
	repoDir        string
	fetched        bool
	fetchedRemotes map[string]bool // per-remote fetch dedupe (separate from origin's `fetched`)
	rerereEnabled  bool            // whether ensureRerereEnabled has run for this Manager
}

// Fetch runs git fetch once per Manager lifetime. Subsequent calls are no-ops.
func (m *Manager) Fetch() error {
	if m.fetched {
		return nil
	}
	if err := m.git.Fetch(); err != nil {
		return fmt.Errorf("failed to fetch: %w", err)
	}
	m.fetched = true
	return nil
}

// ensureRerereEnabled wires `rerere.enabled` and `rerere.autoupdate` into
// the repo's local git config at most once per Manager lifetime. The
// underlying `git.EnableRerere` is itself idempotent and respects an
// explicit user `false`, but it forks two `git config` processes per call;
// caching here avoids repeating that on every sync entry point
// (syncStackInternal, SyncBranch, RebaseChildren, etc.).
//
// Best-effort: errors from the underlying git config writes are
// intentionally swallowed because rerere is a safety-net optimization,
// not a correctness requirement — sync proceeds either way.
func (m *Manager) ensureRerereEnabled() {
	if m.rerereEnabled {
		return
	}
	_ = git.EnableRerere(m.repoDir)
	m.rerereEnabled = true
}

// FetchRemote fetches a specific named remote (e.g. a fork) once per Manager
// lifetime. Used by integrateRemoteForBranch when a branch's configured
// remote isn't `origin` — `Fetch()` only handles origin to avoid hanging on
// unreachable remotes by default. Best-effort: failures are returned but the
// caller typically logs and proceeds, since a stale ref is better than a
// failed sync.
func (m *Manager) FetchRemote(remote string) error {
	if remote == "" || remote == "origin" {
		return m.Fetch()
	}
	if m.fetchedRemotes == nil {
		m.fetchedRemotes = map[string]bool{}
	}
	if m.fetchedRemotes[remote] {
		return nil
	}
	if err := m.git.FetchRemote(remote); err != nil {
		return fmt.Errorf("failed to fetch %s: %w", remote, err)
	}
	m.fetchedRemotes[remote] = true
	return nil
}

// NewManager creates a new stack manager and reconciles config with git state.
// Use this for commands that mutate state (new, delete, sync, reparent, stack, etc.).
func NewManager(repoDir string) (*Manager, error) {
	return newManager(repoDir, true)
}

// NewReadOnlyManager creates a stack manager without reconciliation.
// Use this for read-only commands (diff, goto, list, status, up, down, push)
// where the ~100ms reconciliation cost is unnecessary.
func NewReadOnlyManager(repoDir string) (*Manager, error) {
	return newManager(repoDir, false)
}

func newManager(repoDir string, reconcile bool) (*Manager, error) {
	g := git.New(repoDir)

	// Get the main worktree (the actual repo root)
	mainWorktree, err := g.GetMainWorktree()
	if err != nil {
		mainWorktree = repoDir
	}

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Get repo-specific config
	repoConfig := cfg.GetRepoConfig(mainWorktree)

	stackCfg, err := config.LoadStackConfig(mainWorktree)
	if err != nil {
		return nil, fmt.Errorf("failed to load stack config: %w", err)
	}

	mgr := &Manager{
		git:         g,
		config:      cfg,
		repoConfig:  repoConfig,
		stackConfig: stackCfg,
		repoDir:     mainWorktree,
	}
	if reconcile {
		mgr.Reconcile()
	}
	return mgr, nil
}

// GetRepoDir returns the main repository directory
func (m *Manager) GetRepoDir() string {
	return m.repoDir
}

// rebindGitToRepo points the internal git handle at the main worktree.
// Call before destructive commands (delete, stack tear-down) whose current
// cwd might otherwise sit inside a worktree that is about to be removed.
func (m *Manager) rebindGitToRepo() {
	if m.git == nil || m.git.RepoDir != m.repoDir {
		m.git = git.New(m.repoDir)
	}
}

// moveCwdOutOf moves the process's cwd to the main repo if it currently sits
// inside the given path. A best-effort operation: failures are ignored because
// the subsequent worktree removal will still try to proceed.
func (m *Manager) moveCwdOutOf(path string) {
	if path == "" {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		// cwd may have already been deleted under us — move to main repo.
		_ = os.Chdir(m.repoDir)
		return
	}
	cwdResolved, _ := filepath.EvalSymlinks(cwd)
	pathResolved, _ := filepath.EvalSymlinks(path)
	if cwdResolved == "" {
		cwdResolved = cwd
	}
	if pathResolved == "" {
		pathResolved = path
	}
	if cwdResolved == pathResolved || strings.HasPrefix(cwdResolved, pathResolved+string(os.PathSeparator)) {
		_ = os.Chdir(m.repoDir)
	}
}

// GetConfig returns the loaded global config, avoiding redundant config.Load() calls.
func (m *Manager) GetConfig() *config.Config {
	return m.config
}

// Reconcile silently reconciles ezstack config with git reality.
// It detects renamed branches, removes orphaned entries, and cleans up
// missing worktrees. This runs automatically when a Manager is created,
// so commands always see consistent state.
func (m *Manager) Reconcile() {
	// Prune stale git worktree metadata
	m.git.PruneWorktrees()

	// Remove branches whose worktree directories were manually deleted
	missingWorktrees := m.DetectMissingWorktrees()
	if len(missingWorktrees) > 0 {
		m.HandleMissingWorktrees(missingWorktrees)
	}

	// Detect orphaned branches (in config but not in git) and untracked worktrees
	orphaned := m.DetectOrphanedBranches()
	untracked, err := m.GetUnregisteredWorktrees()
	if err != nil {
		return
	}

	// Detect renames (orphaned + untracked at same worktree path)
	renames := m.DetectRenamedBranches(orphaned, untracked)
	if len(renames) > 0 {
		if err := m.ApplyBranchRenames(renames); err != nil {
			return
		}

		// Filter renamed branches out of orphaned list
		renamedOld := make(map[string]bool)
		for _, r := range renames {
			renamedOld[r.OldName] = true
		}
		var remaining []string
		for _, name := range orphaned {
			if !renamedOld[name] {
				remaining = append(remaining, name)
			}
		}
		orphaned = remaining
	}

	// Remove remaining orphaned branches from config
	if len(orphaned) > 0 {
		m.RemoveOrphanedBranches(orphaned)
	}
}

// RegisterExistingBranch registers an existing branch/worktree as the root of a new stack
func (m *Manager) RegisterExistingBranch(branchName, worktreePath, baseBranch string) (*config.Branch, error) {
	// Check if branch is already registered
	if existing := m.GetBranch(branchName); existing != nil {
		return nil, fmt.Errorf("branch '%s' is already registered in a stack", branchName)
	}

	// Create branch metadata
	branch := &config.Branch{
		Name:         branchName,
		Parent:       baseBranch,
		WorktreePath: worktreePath,
		BaseBranch:   baseBranch,
	}

	// Create a new stack with this branch as the root
	hash := m.generateUniqueHash(branchName)
	stack := &config.Stack{
		Hash: hash,
		Root: baseBranch,
		Tree: config.BranchTree{
			branchName: config.BranchTree{},
		},
	}
	m.stackConfig.Stacks[hash] = stack

	// Update cache with branch metadata
	cache := m.stackConfig.Cache
	cache.SetBranchCache(branchName, &config.BranchCache{
		WorktreePath: worktreePath,
	})

	// Populate branches from tree
	stack.PopulateBranchesWithCache(cache)

	// Save the config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	return branch, nil
}

// RegisterRemoteBranch creates a new stack with a remote branch as its root/base.
// The remote branch is NOT added to the tree — it is the stack root.
// PR info is stored on the Stack struct for display in stack descriptions.
func (m *Manager) RegisterRemoteBranch(branchName, baseBranch string, prNumber int, prURL string) (string, error) {
	hash := m.generateUniqueHash(branchName)
	stack := &config.Stack{
		Hash:         hash,
		Root:         branchName,
		RootBase:     baseBranch,
		RootPRNumber: prNumber,
		RootPRUrl:    prURL,
		Tree:         config.BranchTree{},
	}
	m.stackConfig.Stacks[hash] = stack
	stack.PopulateBranchesWithCache(m.stackConfig.Cache)

	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return "", fmt.Errorf("failed to save stack config: %w", err)
	}
	return hash, nil
}

// AddBranchToStack adds an existing branch to a stack (worktree should already exist)
// This is used when the worktree was created externally (e.g., from a remote branch)
// If targetStackHash is non-empty, the branch is added to that specific stack.
// Otherwise, the stack is found by looking up the parent branch.
func (m *Manager) AddBranchToStack(name, parentBranch, worktreeDir, targetStackHash string) (*config.Branch, error) {
	stackKey := targetStackHash
	if stackKey == "" {
		// Find the stack for the parent (check tree branches first, then roots)
		stackKey = m.findStackForBranch(parentBranch)
		if stackKey == "" {
			stackKey = m.findStackByRoot(parentBranch)
		}
	}
	if stackKey == "" {
		return nil, fmt.Errorf("parent branch '%s' not found in any stack", parentBranch)
	}

	stack := m.stackConfig.Stacks[stackKey]

	// Add branch to the tree
	stack.AddBranch(name, parentBranch)

	// Update cache
	cache := m.stackConfig.Cache
	cache.SetBranchCache(name, &config.BranchCache{
		WorktreePath: worktreeDir,
	})

	// Repopulate branches from tree with cache
	stack.PopulateBranchesWithCache(cache)

	// Save the config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	// Return the branch from the populated list
	for _, b := range stack.Branches {
		if b.Name == name {
			return b, nil
		}
	}
	return nil, fmt.Errorf("failed to add branch")
}

// CreateBranch creates a new branch in the stack.
// targetStackHash controls stack placement: "" for auto-detect, "new" for a new stack,
// or a specific stack hash to add to.
func (m *Manager) CreateBranch(name, parentBranch, worktreeDir, targetStackHash string) (*config.Branch, error) {
	// If no worktree dir specified, use the configured base dir for this repo
	if worktreeDir == "" {
		if m.repoConfig != nil && m.repoConfig.WorktreeBaseDir != "" {
			worktreeDir = filepath.Join(m.repoConfig.WorktreeBaseDir, name)
		} else {
			return nil, fmt.Errorf("worktree directory not specified and no default configured for this repo. Run: ezs config set worktree_base_dir <path>")
		}
	}

	// Create the worktree
	if err := m.git.CreateWorktree(name, worktreeDir, parentBranch); err != nil {
		return nil, fmt.Errorf("failed to create worktree: %w", err)
	}

	stack := m.findOrCreateStack(name, parentBranch, targetStackHash)

	// Update cache
	cache := m.stackConfig.Cache
	cache.SetBranchCache(name, &config.BranchCache{
		WorktreePath: worktreeDir,
	})

	// Repopulate branches from tree with cache
	stack.PopulateBranchesWithCache(cache)

	// Save the config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	// Return the branch from the populated list
	for _, b := range stack.Branches {
		if b.Name == name {
			return b, nil
		}
	}
	return nil, fmt.Errorf("failed to create branch")
}

// CreateBranchNoWorktree creates a new branch without a worktree and adds it to a stack.
// targetStackHash controls stack placement: "" for auto-detect, "new" for a new stack,
// or a specific stack hash to add to.
func (m *Manager) CreateBranchNoWorktree(name, parentBranch, targetStackHash string) (*config.Branch, error) {
	// Create the git branch
	if err := m.git.CreateBranchOnly(name, parentBranch); err != nil {
		return nil, fmt.Errorf("failed to create branch: %w", err)
	}

	stack := m.findOrCreateStack(name, parentBranch, targetStackHash)

	// Update cache (no worktree path)
	cache := m.stackConfig.Cache
	cache.SetBranchCache(name, &config.BranchCache{})

	// Repopulate branches from tree with cache
	stack.PopulateBranchesWithCache(cache)

	// Save the config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	// Return the branch from the populated list
	for _, b := range stack.Branches {
		if b.Name == name {
			return b, nil
		}
	}
	return nil, fmt.Errorf("failed to create branch")
}

// CreateWorktreeOnly creates a worktree without adding it to a stack
// This is used when the user wants to create a standalone worktree from main/master
func (m *Manager) CreateWorktreeOnly(name, parentBranch, worktreeDir string) error {
	// If no worktree dir specified, use the configured base dir for this repo
	if worktreeDir == "" {
		if m.repoConfig != nil && m.repoConfig.WorktreeBaseDir != "" {
			worktreeDir = filepath.Join(m.repoConfig.WorktreeBaseDir, name)
		} else {
			return fmt.Errorf("worktree directory not specified and no default configured for this repo. Run: ezs config set worktree_base_dir <path>")
		}
	}

	// Create the worktree
	if err := m.git.CreateWorktree(name, worktreeDir, parentBranch); err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}

	return nil
}

// findStackForBranch finds which stack a branch belongs to
func (m *Manager) findStackForBranch(branchName string) string {
	for stackName, stack := range m.stackConfig.Stacks {
		for _, b := range stack.Branches {
			if b.Name == branchName {
				return stackName
			}
		}
	}
	return ""
}

// FindStackForBranch finds which stack a branch belongs to (exported)
func (m *Manager) FindStackForBranch(branchName string) *config.Stack {
	for _, stack := range m.stackConfig.Stacks {
		for _, b := range stack.Branches {
			if b.Name == branchName {
				return stack
			}
		}
	}
	return nil
}

// GetCurrentStack returns the stack for the current branch.
// Falls back to .git/rebase-merge/head-name (or rebase-apply/head-name) when
// HEAD is detached due to an in-progress rebase, so that
// `ezs sync -s --continue` (which resolves the "current stack" from inside a
// conflicted worktree) finds the right stack even though the rebase has
// detached HEAD. Only rebase needs this fallback — `git merge` keeps HEAD on
// the branch ref even mid-conflict, so CurrentBranch already returns a usable
// name during a merge in progress.
func (m *Manager) GetCurrentStack() (*config.Stack, *config.Branch, error) {
	currentBranch, err := m.git.CurrentBranch()
	if err != nil {
		return nil, nil, err
	}

	lookup := func(name string) (*config.Stack, *config.Branch, bool) {
		for _, stack := range m.stackConfig.Stacks {
			for _, branch := range stack.Branches {
				if branch.Name == name {
					return stack, branch, true
				}
			}
		}
		return nil, nil, false
	}

	if currentBranch != "HEAD" {
		if s, b, ok := lookup(currentBranch); ok {
			return s, b, nil
		}
	}
	// Detached HEAD or branch not tracked — try the rebase head-name as a
	// fallback. During `git rebase`, HEAD is detached but the original branch
	// name is recorded in rebase-state files.
	if rebaseBranch := m.git.BranchFromRebaseState(); rebaseBranch != "" {
		if s, b, ok := lookup(rebaseBranch); ok {
			return s, b, nil
		}
	}
	return nil, nil, fmt.Errorf("current branch %s is not part of any stack", currentBranch)
}

// ListStacks returns all stacks sorted by name for deterministic ordering
func (m *Manager) ListStacks() []*config.Stack {
	names := make([]string, 0, len(m.stackConfig.Stacks))
	for name := range m.stackConfig.Stacks {
		names = append(names, name)
	}
	sort.Strings(names)

	stacks := make([]*config.Stack, 0, len(names))
	for _, name := range names {
		stacks = append(stacks, m.stackConfig.Stacks[name])
	}
	return stacks
}

// GetBranch returns a branch by name
func (m *Manager) GetBranch(name string) *config.Branch {
	for _, stack := range m.stackConfig.Stacks {
		for _, branch := range stack.Branches {
			if branch.Name == name {
				return branch
			}
		}
	}
	return nil
}

// GetChildren returns all child branches of a given branch
func (m *Manager) GetChildren(branchName string) []*config.Branch {
	var children []*config.Branch
	for _, stack := range m.stackConfig.Stacks {
		for _, branch := range stack.Branches {
			if branch.Parent == branchName {
				children = append(children, branch)
			}
		}
	}
	return children
}

// GetDescendants returns every transitive descendant of branchName across all
// stacks, in topological order (parents before children). Used by `ezs sync
// --continue` to re-sync the entire subtree after a parent's rebase completes,
// not just the immediate children.
func (m *Manager) GetDescendants(branchName string) []*config.Branch {
	visited := make(map[string]bool)
	var out []*config.Branch
	var walk func(name string)
	walk = func(name string) {
		for _, child := range m.GetChildren(name) {
			if visited[child.Name] {
				continue
			}
			visited[child.Name] = true
			out = append(out, child)
			walk(child.Name)
		}
	}
	walk(branchName)
	return out
}

// SetBranchRemote updates the cached `Remote` for a branch and persists.
// Used by tests (and could be wired into a future `ezs config set
// branch-remote` flag) to point ezstack at a fork remote when push/pull
// should target somewhere other than `origin`. Returns nil on success.
func (m *Manager) SetBranchRemote(branchName, remote string) error {
	cache := m.stackConfig.Cache
	bc := cache.GetBranchCache(branchName)
	if bc == nil {
		bc = &config.BranchCache{}
	}
	bc.Remote = remote
	cache.SetBranchCache(branchName, bc)
	return m.stackConfig.Save(m.repoDir)
}

// GetTreeChildren returns child branches based on the original tree structure (BaseBranch),
// not the effective parent. This is used for navigation (up/down) where we want to
// follow the tree hierarchy even when parents have been merged and children reparented.
func (m *Manager) GetTreeChildren(branchName string) []*config.Branch {
	var children []*config.Branch
	for _, stack := range m.stackConfig.Stacks {
		for _, branch := range stack.Branches {
			if branch.BaseBranch == branchName {
				children = append(children, branch)
			}
		}
	}
	return children
}

// IsMainBranch checks if a branch is the main/master branch.
// Use only for protection (e.g. preventing deletion of main). For stack-root
// logic, compare against Stack.Root or use GetStackForBranch instead.
func (m *Manager) IsMainBranch(name string) bool {
	baseBranch := m.config.GetBaseBranch(m.repoDir)
	return name == "main" || name == "master" || name == baseBranch
}

// GetStackForBranch returns the stack containing the given branch, or nil.
func (m *Manager) GetStackForBranch(branchName string) *config.Stack {
	key := m.findStackForBranch(branchName)
	if key == "" {
		return nil
	}
	return m.stackConfig.Stacks[key]
}

// GetStackByHash finds a stack by hash prefix (minimum 3 characters). Returns error if 0 or >1 stacks match.
func (m *Manager) GetStackByHash(prefix string) (*config.Stack, error) {
	// Strip leading # if present
	prefix = strings.TrimPrefix(prefix, "#")

	if len(prefix) < 3 {
		return nil, fmt.Errorf("hash prefix too short: '%s' (minimum 3 characters)", prefix)
	}

	var matches []*config.Stack
	for _, stack := range m.stackConfig.Stacks {
		if strings.HasPrefix(stack.Hash, prefix) {
			matches = append(matches, stack)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("no stack found matching '%s'", prefix)
	}
	if len(matches) > 1 {
		var hashes []string
		for _, s := range matches {
			hashes = append(hashes, s.Hash)
		}
		return nil, fmt.Errorf("ambiguous stack identifier '%s', matches: %s", prefix, strings.Join(hashes, ", "))
	}
	return matches[0], nil
}

// DeleteBranch removes a branch from the stack and deletes its worktree
// Returns an error if the branch has child branches
func (m *Manager) DeleteBranch(branchName string, force bool) error {
	// Check if branch exists
	branch := m.GetBranch(branchName)
	if branch == nil {
		return fmt.Errorf("branch '%s' not found in any stack", branchName)
	}

	// Rebind git handle to the main worktree so we can safely remove any
	// worktree — including one the user was running ezs from. Without this,
	// m.git.RepoDir may point at the worktree we're about to delete, and every
	// subsequent git call will fail with chdir: no such file or directory.
	m.rebindGitToRepo()

	// If the caller's process cwd is inside the worktree we're about to
	// remove, move it out so the OS doesn't hold the directory open.
	m.moveCwdOutOf(branch.WorktreePath)

	// Check for child branches
	children := m.GetChildren(branchName)
	if len(children) > 0 && !force {
		childNames := make([]string, len(children))
		for i, c := range children {
			childNames[i] = c.Name
		}
		return fmt.Errorf("cannot delete branch '%s': has child branches: %s. Use --force to delete anyway", branchName, strings.Join(childNames, ", "))
	}

	// Remove the worktree and branch from git
	worktreePath := branch.WorktreePath
	if worktreePath == "" {
		// WorktreePath missing from config - look it up from git worktree list
		if wts, err := m.git.ListWorktrees(); err == nil {
			for _, wt := range wts {
				if wt.Branch == branchName {
					worktreePath = wt.Path
					break
				}
			}
		}
	}
	if worktreePath != "" {
		if err := m.git.RemoveWorktree(worktreePath, true, branchName); err != nil {
			return fmt.Errorf("failed to remove worktree: %w", err)
		}
	} else if m.git.BranchExists(branchName) {
		// No worktree at all - just delete the git branch directly
		if err := m.git.DeleteBranch(branchName, true); err != nil {
			if !strings.Contains(err.Error(), "not found") {
				return fmt.Errorf("failed to delete branch: %w", err)
			}
		}
	}

	cache := m.stackConfig.Cache

	// Remove from stack config using tree methods
	for stackName, stack := range m.stackConfig.Stacks {
		if stack.HasBranch(branchName) {
			// Remove this branch from the tree (children are moved up automatically)
			stack.RemoveBranch(branchName)

			// Repopulate branches from tree with cache
			stack.PopulateBranchesWithCache(cache)

			// If this was the only branch, remove the entire stack
			if len(stack.Tree) == 0 {
				delete(m.stackConfig.Stacks, stackName)
			}

			break
		}
	}

	// Remove from cache
	delete(cache.Branches, branchName)

	// Save the config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return fmt.Errorf("failed to save stack config: %w", err)
	}

	return nil
}

// UntrackBranch removes a branch from ezstack tracking without deleting the git branch or worktree
// Children of the untracked branch are reparented to the untracked branch's parent
func (m *Manager) UntrackBranch(branchName string) error {
	// Check if branch exists in tracking
	branch := m.GetBranch(branchName)
	if branch == nil {
		return fmt.Errorf("branch '%s' is not tracked by ezstack", branchName)
	}

	cache := m.stackConfig.Cache

	// Remove from stack config using tree methods (children are moved up automatically)
	for stackName, stack := range m.stackConfig.Stacks {
		if stack.HasBranch(branchName) {
			// Remove this branch from the tree (children are moved up automatically)
			stack.RemoveBranch(branchName)

			// Repopulate branches from tree with cache
			stack.PopulateBranchesWithCache(cache)

			// If this was the only branch, remove the entire stack
			if len(stack.Tree) == 0 {
				delete(m.stackConfig.Stacks, stackName)
			}

			break
		}
	}

	// Remove from cache
	delete(cache.Branches, branchName)

	// Save the config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return fmt.Errorf("failed to save stack config: %w", err)
	}

	return nil
}

// ReparentResult holds the result of a reparent operation
type ReparentResult struct {
	Branch      *config.Branch
	HasConflict bool   // true if rebase had conflicts (config was still saved)
	ConflictDir string // worktree path where conflicts need to be resolved
}

// ReparentBranch changes the parent of a branch to a new parent
// This handles several cases:
// 1. Branch is already in a stack - just update parent pointer
// 2. Branch is standalone (not in any stack) - add it to the new parent's stack
// 3. New parent is in a different stack - merge stacks or move branch
// If doRebase is true, performs git rebase --onto to move commits.
// Config changes are always saved first. If rebase has conflicts, the result
// will have HasConflict=true but the branch will still be returned (config is updated).
func (m *Manager) ReparentBranch(branchName, newParentName string, doRebase bool, useMerge ...bool) (*ReparentResult, error) {
	if branchName == newParentName {
		return nil, fmt.Errorf("cannot stack a branch on itself")
	}
	if m.GetBranch(newParentName) == nil && !m.git.BranchExists(newParentName) {
		return nil, fmt.Errorf("new parent '%s' does not exist", newParentName)
	}

	merge := len(useMerge) > 0 && useMerge[0]

	// Check if branch is already registered in a stack
	existingBranch := m.GetBranch(branchName)

	if existingBranch != nil {
		// Branch is already in a stack - update its parent
		return m.reparentExistingBranch(existingBranch, newParentName, doRebase, merge)
	}

	// Branch is not in any stack - need to add it
	return m.addBranchWithParent(branchName, newParentName, doRebase, merge)
}

// reparentExistingBranch handles reparenting a branch that's already in a stack.
// Config changes are saved first, then rebase is attempted if requested.
func (m *Manager) reparentExistingBranch(branch *config.Branch, newParentName string, doRebase bool, useMerge bool) (*ReparentResult, error) {
	oldParent := branch.Parent
	oldStackKey := m.findStackForBranch(branch.Name)
	newParentStackKey := m.findStackForBranch(newParentName)

	// Prevent circular dependencies
	if m.wouldCreateCycle(branch.Name, newParentName) {
		return nil, fmt.Errorf("cannot reparent: would create circular dependency")
	}

	cache := m.stackConfig.Cache

	// Handle stack reorganization using tree methods — BEFORE rebase
	oldStack := m.stackConfig.Stacks[oldStackKey]
	if oldStack == nil {
		return nil, fmt.Errorf("branch '%s' not found in any stack", branch.Name)
	}

	if oldStackKey != newParentStackKey && newParentStackKey != "" {
		if err := m.moveBranchToStack(branch.Name, oldStackKey, newParentStackKey, newParentName); err != nil {
			return nil, fmt.Errorf("failed to move branch to new stack: %w", err)
		}
	} else if newParentStackKey == "" {
		if oldStack.Root == newParentName {
			oldStack.ReparentBranch(branch.Name, "")
			oldStack.PopulateBranchesWithCache(cache)
		} else {
			// Use the unique variant — if multiple stacks share this root,
			// findStackByRoot would pick one non-deterministically via map iteration.
			// Returning "" forces the fallback path to create a fresh stack instead.
			mainStackKey := m.findUniqueStackByRoot(newParentName)
			if mainStackKey != "" {
				if err := m.moveBranchToStack(branch.Name, oldStackKey, mainStackKey, newParentName); err != nil {
					return nil, fmt.Errorf("failed to move branch to main stack: %w", err)
				}
			} else {
				subtree := oldStack.ExtractSubtree(branch.Name)
				oldStack.PopulateBranchesWithCache(cache)
				if len(oldStack.Tree) == 0 {
					delete(m.stackConfig.Stacks, oldStackKey)
				}
				hash := m.generateUniqueHash(branch.Name)
				newStack := &config.Stack{
					Hash: hash,
					Root: newParentName,
					Tree: config.BranchTree{
						branch.Name: subtree,
					},
				}
				m.stackConfig.Stacks[hash] = newStack
				newStack.PopulateBranchesWithCache(cache)
			}
		}
	} else {
		oldStack.ReparentBranch(branch.Name, newParentName)
		oldStack.PopulateBranchesWithCache(cache)
	}

	// Save the config before attempting rebase
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	result := &ReparentResult{Branch: m.GetBranch(branch.Name)}

	// Perform git rebase/merge if requested (after config is saved)
	if doRebase {
		newParentRef := m.getParentRef(newParentName)
		workDir := branch.WorktreePath
		if workDir == "" {
			workDir = m.repoDir
		}

		if branch.WorktreePath != "" {
			g := git.New(branch.WorktreePath)
			if useMerge {
				syncResult := g.MergeNonInteractive(newParentRef)
				if syncResult.HasConflict {
					result.HasConflict = true
					result.ConflictDir = branch.WorktreePath
				} else if syncResult.Error != nil {
					return result, fmt.Errorf("merge failed: %w", syncResult.Error)
				}
			} else {
				oldParentRef := m.getParentRef(oldParent)
				mergeBase, err := m.git.GetMergeBase(branch.Name, oldParentRef)
				if err != nil {
					mergeBase = oldParentRef
				}
				rebaseResult := g.RebaseOntoNonInteractive(newParentRef, mergeBase)
				if rebaseResult.HasConflict {
					result.HasConflict = true
					result.ConflictDir = branch.WorktreePath
				} else if rebaseResult.Error != nil {
					return result, fmt.Errorf("rebase failed: %w", rebaseResult.Error)
				}
			}
		} else {
			// No worktree — use checkout-based sync in the main repo
			syncResult := syncViaCheckout(m.git, branch.Name, func(g *git.Git) git.RebaseResult {
				if useMerge {
					return g.MergeNonInteractive(newParentRef)
				}
				oldParentRef := m.getParentRef(oldParent)
				mergeBase, err := m.git.GetMergeBase(branch.Name, oldParentRef)
				if err != nil {
					mergeBase = oldParentRef
				}
				return g.RebaseOntoNonInteractive(newParentRef, mergeBase)
			})
			if syncResult.HasConflict {
				result.HasConflict = true
				result.ConflictDir = workDir
			} else if syncResult.Error != nil {
				return result, fmt.Errorf("sync failed: %w", syncResult.Error)
			}
		}
	}

	return result, nil
}

// addBranchWithParent adds a standalone git branch to a stack with the specified parent.
// Config changes are saved first, then rebase is attempted if requested.
func (m *Manager) addBranchWithParent(branchName, newParentName string, doRebase bool, useMerge bool) (*ReparentResult, error) {
	// Check if the git branch exists
	if !m.git.BranchExists(branchName) {
		return nil, fmt.Errorf("git branch '%s' does not exist", branchName)
	}

	// Find the worktree for this branch (if any)
	worktreePath := ""
	worktrees, err := m.git.ListWorktrees()
	if err == nil {
		for _, wt := range worktrees {
			if wt.Branch == branchName {
				worktreePath = wt.Path
				break
			}
		}
	}

	// Find the stack for the new parent.
	// Only match if parent is a tracked branch in a stack — NOT just a root.
	// This ensures "ezs stack -b X -p main" always creates a new stack
	// instead of randomly picking an existing one when multiple stacks share the same root.
	newParentStackKey := m.findStackForBranch(newParentName)
	var targetStack *config.Stack

	if newParentStackKey != "" {
		// Add to existing stack (parent is a tracked branch)
		targetStack = m.stackConfig.Stacks[newParentStackKey]
		targetStack.AddBranch(branchName, newParentName)
	} else {
		hash := m.generateUniqueHash(branchName)
		targetStack = &config.Stack{
			Hash: hash,
			Root: newParentName,
			Tree: config.BranchTree{
				branchName: config.BranchTree{},
			},
		}
		m.stackConfig.Stacks[hash] = targetStack
	}

	// Update cache
	cache := m.stackConfig.Cache
	cache.SetBranchCache(branchName, &config.BranchCache{
		WorktreePath: worktreePath,
	})

	// Repopulate branches from tree with cache
	targetStack.PopulateBranchesWithCache(cache)

	// Save config BEFORE attempting rebase
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	result := &ReparentResult{Branch: m.GetBranch(branchName)}

	// Perform git rebase/merge if requested (after config is saved)
	if doRebase {
		newParentRef := m.getParentRef(newParentName)

		if worktreePath != "" {
			g := git.New(worktreePath)
			if useMerge {
				syncResult := g.MergeNonInteractive(newParentRef)
				if syncResult.HasConflict {
					result.HasConflict = true
					result.ConflictDir = worktreePath
				} else if syncResult.Error != nil {
					return result, fmt.Errorf("merge failed: %w", syncResult.Error)
				}
			} else {
				rebaseResult := g.RebaseNonInteractive(newParentRef)
				if rebaseResult.HasConflict {
					result.HasConflict = true
					result.ConflictDir = worktreePath
				} else if rebaseResult.Error != nil {
					return result, fmt.Errorf("rebase failed: %w", rebaseResult.Error)
				}
			}
		} else {
			// No worktree — use checkout-based sync in the main repo
			syncResult := syncViaCheckout(m.git, branchName, func(g *git.Git) git.RebaseResult {
				if useMerge {
					return g.MergeNonInteractive(newParentRef)
				}
				return g.RebaseNonInteractive(newParentRef)
			})
			if syncResult.HasConflict {
				result.HasConflict = true
				result.ConflictDir = m.repoDir
			} else if syncResult.Error != nil {
				return result, fmt.Errorf("sync failed: %w", syncResult.Error)
			}
		}
	}

	return result, nil
}

// wouldCreateCycle checks if reparenting branchName to newParentName would create a cycle
func (m *Manager) wouldCreateCycle(branchName, newParentName string) bool {
	// Walk up from newParentName to see if we ever reach branchName
	current := newParentName
	visited := make(map[string]bool)

	for {
		if current == branchName {
			return true
		}
		if visited[current] {
			return false
		}
		visited[current] = true

		branch := m.GetBranch(current)
		if branch == nil {
			return false // Reached a root or external branch
		}
		current = branch.Parent
	}
}

func (m *Manager) moveBranchToStack(branchName, fromStackName, toStackName, newParentName string) error {
	fromStack := m.stackConfig.Stacks[fromStackName]
	toStack := m.stackConfig.Stacks[toStackName]

	if fromStack == nil || toStack == nil {
		return fmt.Errorf("invalid stack names")
	}

	cache := m.stackConfig.Cache

	// Validate the target can accept the subtree BEFORE mutating the source
	// stack. If we extracted first and AddSubtree then failed, the children
	// would be silently dropped from the source stack with no rollback path.
	targetAccepts := newParentName == toStack.Root || newParentName == "" || toStack.HasBranch(newParentName)
	if !targetAccepts {
		return fmt.Errorf("parent '%s' not found in target stack", newParentName)
	}

	if !fromStack.HasBranch(branchName) {
		return fmt.Errorf("branch '%s' not found in source stack", branchName)
	}

	subtree := fromStack.ExtractSubtree(branchName)
	if subtree == nil {
		return fmt.Errorf("branch '%s' not found in source stack", branchName)
	}

	if !toStack.AddSubtree(branchName, subtree, newParentName) {
		// Should be unreachable because we pre-validated, but roll back
		// defensively rather than orphan the subtree.
		fromStack.AddSubtree(branchName, subtree, "")
		fromStack.PopulateBranchesWithCache(cache)
		return fmt.Errorf("parent '%s' not found in target stack", newParentName)
	}

	fromStack.PopulateBranchesWithCache(cache)
	if len(fromStack.Tree) == 0 {
		delete(m.stackConfig.Stacks, fromStackName)
	}
	toStack.PopulateBranchesWithCache(cache)

	return nil
}

// findOrCreateStack locates or creates a stack for a new branch.
// targetStackHash controls which stack to use:
//   - "" (empty): auto-detect from parent branch or parent root
//   - "new": always create a new stack
//   - any other value: use the stack with that hash
func (m *Manager) findOrCreateStack(branchName, parentBranch, targetStackHash string) *config.Stack {
	if targetStackHash != "new" {
		var stackKey string
		if targetStackHash != "" {
			stackKey = targetStackHash
		} else {
			stackKey = m.findStackForBranch(parentBranch)
			if stackKey == "" {
				// Parent is a root, not a tracked branch. Only use an existing
				// stack if exactly one matches — when multiple stacks share the
				// same root, picking one at random would be wrong.
				stackKey = m.findUniqueStackByRoot(parentBranch)
			}
		}
		if stackKey != "" {
			s := m.stackConfig.Stacks[stackKey]
			if s != nil {
				s.AddBranch(branchName, parentBranch)
				return s
			}
		}
	}

	hash := m.generateUniqueHash(branchName)
	s := &config.Stack{
		Hash: hash,
		Root: parentBranch,
		Tree: config.BranchTree{
			branchName: config.BranchTree{},
		},
	}
	m.stackConfig.Stacks[hash] = s
	return s
}

func (m *Manager) generateUniqueHash(seed string) string {
	for i := 0; ; i++ {
		candidate := seed
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", seed, i)
		}
		hash := config.GenerateStackHash(candidate)
		if _, exists := m.stackConfig.Stacks[hash]; !exists {
			return hash
		}
	}
}

// HasStackWithRoot checks if any stack uses rootName as its root
func (m *Manager) HasStackWithRoot(rootName string) bool {
	return m.findStackByRoot(rootName) != ""
}

// GetStacksWithRoot returns all stacks that use rootName as their root
func (m *Manager) GetStacksWithRoot(rootName string) []*config.Stack {
	var result []*config.Stack
	for _, s := range m.ListStacks() {
		if s.Root == rootName {
			result = append(result, s)
		}
	}
	return result
}

func (m *Manager) findStackByRoot(rootName string) string {
	for stackName, stack := range m.stackConfig.Stacks {
		if stack.Root == rootName {
			return stackName
		}
	}
	return ""
}

// findUniqueStackByRoot returns the stack key if exactly one stack uses rootName
// as its root. Returns "" if zero or multiple stacks match, avoiding non-deterministic
// selection when multiple stacks share the same root.
func (m *Manager) findUniqueStackByRoot(rootName string) string {
	var found string
	for stackName, stack := range m.stackConfig.Stacks {
		if stack.Root == rootName {
			if found != "" {
				return "" // ambiguous — multiple stacks share this root
			}
			found = stackName
		}
	}
	return found
}

// GetAllBranchesInAllStacks returns all branches across all stacks
func (m *Manager) GetAllBranchesInAllStacks() []*config.Branch {
	var allBranches []*config.Branch
	for _, stack := range m.ListStacks() {
		allBranches = append(allBranches, stack.Branches...)
	}
	return allBranches
}

// GetUnregisteredWorktrees returns worktrees that exist but are not registered in any stack
func (m *Manager) GetUnregisteredWorktrees() ([]git.Worktree, error) {
	worktrees, err := m.git.ListWorktrees()
	if err != nil {
		return nil, err
	}

	var unregistered []git.Worktree
	for _, wt := range worktrees {
		// Skip main worktree and branches already in stacks
		if m.IsMainBranch(wt.Branch) {
			continue
		}
		if m.GetBranch(wt.Branch) != nil {
			continue
		}
		unregistered = append(unregistered, wt)
	}
	return unregistered, nil
}

// UpdateResult contains the results of an update operation
type UpdateResult struct {
	// Branches that were removed from config because they no longer exist in git
	RemovedBranches []string
	// Worktrees that were discovered and added to stacks
	AddedBranches []*config.Branch
	// Branches whose parent was updated based on merge-base analysis
	ReparentedBranches []ReparentInfo
	// Branches that were detected as renames and updated
	RenamedBranches []RenamedBranchInfo
}

// ReparentInfo contains info about a branch that was reparented
type ReparentInfo struct {
	BranchName string
	OldParent  string
	NewParent  string
}

// DetectOrphanedBranches finds branches in config that no longer exist in git
func (m *Manager) DetectOrphanedBranches() []string {
	var orphaned []string
	for _, stack := range m.stackConfig.Stacks {
		for _, branch := range stack.Branches {
			// Skip merged branches - they're expected to not exist
			if branch.IsMerged {
				continue
			}
			// Check if branch exists in git
			if !m.git.BranchExists(branch.Name) {
				orphaned = append(orphaned, branch.Name)
			}
		}
	}
	return orphaned
}

// RemoveOrphanedBranches removes branches from config that no longer exist in git
func (m *Manager) RemoveOrphanedBranches(branchNames []string) error {
	cache := m.stackConfig.Cache

	for _, branchName := range branchNames {
		// Find and remove from stack using tree methods
		for stackName, stack := range m.stackConfig.Stacks {
			if stack.HasBranch(branchName) {
				// Remove branch (children are automatically reparented in tree)
				stack.RemoveBranch(branchName)

				// Repopulate branches from tree
				stack.PopulateBranchesWithCache(cache)

				// If stack is now empty, remove it
				if len(stack.Tree) == 0 {
					delete(m.stackConfig.Stacks, stackName)
				}

				// Remove from cache
				delete(cache.Branches, branchName)

				break
			}
		}
	}

	return m.stackConfig.Save(m.repoDir)
}

// RenamedBranchInfo contains info about a branch that was renamed outside of ezstack
type RenamedBranchInfo struct {
	OldName      string
	NewName      string
	WorktreePath string
}

// DetectRenamedBranches correlates orphaned branches (in config but not in git) with
// untracked worktrees (in git but not in config) by matching worktree paths.
// If an orphaned branch's cached worktree path matches an untracked worktree's path,
// it's almost certainly a rename (git branch -m old new).
func (m *Manager) DetectRenamedBranches(orphaned []string, untracked []git.Worktree) []RenamedBranchInfo {
	// Build a map of worktree path -> untracked worktree
	untrackedByPath := make(map[string]git.Worktree)
	for _, wt := range untracked {
		untrackedByPath[wt.Path] = wt
	}

	var renames []RenamedBranchInfo
	cache := m.stackConfig.Cache

	for _, oldName := range orphaned {
		bc := cache.GetBranchCache(oldName)
		if bc == nil || bc.WorktreePath == "" {
			continue
		}
		if wt, ok := untrackedByPath[bc.WorktreePath]; ok {
			renames = append(renames, RenamedBranchInfo{
				OldName:      oldName,
				NewName:      wt.Branch,
				WorktreePath: wt.Path,
			})
		}
	}
	return renames
}

// ApplyBranchRenames updates the config to reflect branch renames.
// For each rename, it updates the tree key, moves the cache entry, and preserves all metadata.
func (m *Manager) ApplyBranchRenames(renames []RenamedBranchInfo) error {
	cache := m.stackConfig.Cache

	for _, rename := range renames {
		// Find the stack containing the old branch and rename it in the tree
		for _, stack := range m.stackConfig.Stacks {
			if !stack.HasBranch(rename.OldName) {
				continue
			}

			// Rename in tree (hash stays stable - stack identity doesn't change)
			stack.RenameBranchInTree(rename.OldName, rename.NewName)

			// Move cache entry: copy old metadata to new key, delete old key
			oldCache := cache.GetBranchCache(rename.OldName)
			if oldCache != nil {
				cache.SetBranchCache(rename.NewName, oldCache)
			} else {
				cache.SetBranchCache(rename.NewName, &config.BranchCache{
					WorktreePath: rename.WorktreePath,
				})
			}
			delete(cache.Branches, rename.OldName)

			// Repopulate branches from tree
			stack.PopulateBranchesWithCache(cache)
			break
		}
	}

	return m.stackConfig.Save(m.repoDir)
}

// MissingWorktreeInfo contains info about a branch whose worktree was removed
type MissingWorktreeInfo struct {
	BranchName   string
	WorktreePath string
}

// DetectMissingWorktrees finds branches whose worktree directories no longer exist on disk
// This can happen when a user manually removes a worktree with `rm -rf`
func (m *Manager) DetectMissingWorktrees() []MissingWorktreeInfo {
	var missing []MissingWorktreeInfo
	for _, stack := range m.stackConfig.Stacks {
		for _, branch := range stack.Branches {
			// Skip merged branches (they don't have local worktrees)
			if branch.IsMerged {
				continue
			}
			// Skip branches without a worktree path
			if branch.WorktreePath == "" {
				continue
			}
			// Check if the worktree directory exists
			if _, err := os.Stat(branch.WorktreePath); os.IsNotExist(err) {
				missing = append(missing, MissingWorktreeInfo{
					BranchName:   branch.Name,
					WorktreePath: branch.WorktreePath,
				})
			}
		}
	}
	return missing
}

// HandleMissingWorktrees cleans up branches whose worktrees were manually removed
// It removes the branches from the stack config (git worktree prune should be called first)
func (m *Manager) HandleMissingWorktrees(branches []MissingWorktreeInfo) error {
	if len(branches) == 0 {
		return nil
	}

	cache := m.stackConfig.Cache

	for _, info := range branches {
		// Remove the branch from the stack config
		for stackName, stack := range m.stackConfig.Stacks {
			if stack.HasBranch(info.BranchName) {
				// Remove branch (children are automatically reparented in tree)
				stack.RemoveBranch(info.BranchName)

				// Repopulate branches from tree
				stack.PopulateBranchesWithCache(cache)

				// If stack is now empty, remove it
				if len(stack.Tree) == 0 {
					delete(m.stackConfig.Stacks, stackName)
				}

				// Remove from cache
				delete(cache.Branches, info.BranchName)

				break
			}
		}
	}

	return m.stackConfig.Save(m.repoDir)
}

// AddWorktreeToStack adds an unregistered worktree to a stack with the specified parent
func (m *Manager) AddWorktreeToStack(branchName, worktreePath, parentName string) (*config.Branch, error) {
	// Check if already registered
	if existing := m.GetBranch(branchName); existing != nil {
		return nil, fmt.Errorf("branch '%s' is already registered", branchName)
	}

	// Find or create the stack
	stackKey := m.findStackForBranch(parentName)
	var stack *config.Stack
	if stackKey == "" {
		// Parent is main/master - create new stack
		hash := m.generateUniqueHash(branchName)
		stack = &config.Stack{
			Hash: hash,
			Root: parentName,
			Tree: config.BranchTree{
				branchName: config.BranchTree{},
			},
		}
		m.stackConfig.Stacks[hash] = stack
	} else {
		// Add to existing stack
		stack = m.stackConfig.Stacks[stackKey]
		stack.AddBranch(branchName, parentName)
	}

	// Update cache
	cache := m.stackConfig.Cache
	cache.SetBranchCache(branchName, &config.BranchCache{
		WorktreePath: worktreePath,
	})

	// Repopulate branches from tree with cache
	stack.PopulateBranchesWithCache(cache)

	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	// Return the branch from the populated list
	for _, b := range stack.Branches {
		if b.Name == branchName {
			return b, nil
		}
	}
	return nil, fmt.Errorf("failed to add branch")
}

// SetStackName sets or updates the name of a stack
func (m *Manager) SetStackName(stackHash, name string) error {
	stack := m.stackConfig.Stacks[stackHash]
	if stack == nil {
		return fmt.Errorf("stack '%s' not found", stackHash)
	}
	stack.Name = name
	return m.stackConfig.Save(m.repoDir)
}

// DeclineStackDelete marks a stack so cleanup prompts are not repeated.
func (m *Manager) DeclineStackDelete(stackHash string) error {
	stack := m.stackConfig.Stacks[stackHash]
	if stack == nil {
		return nil
	}
	stack.DeleteDeclined = true
	return m.stackConfig.Save(m.repoDir)
}

// GetStackByHashExact returns a stack by its exact hash (no prefix matching).
func (m *Manager) GetStackByHashExact(hash string) *config.Stack {
	return m.stackConfig.Stacks[hash]
}

// DeleteStack removes an entire stack from config, cleaning up any remaining worktrees and git branches.
// This is intended for fully merged stacks where all branches have been completed.
// Returns (needsCd, error) — needsCd is true when the process had to leave a worktree
// that was deleted, so the caller can emit a cd to the main repo.
func (m *Manager) DeleteStack(stackHash string) (bool, error) {
	stack := m.stackConfig.Stacks[stackHash]
	if stack == nil {
		return false, fmt.Errorf("stack '%s' not found", stackHash)
	}

	cache := m.stackConfig.Cache

	// Rebind git handle to the main worktree up-front so destructive calls
	// below cannot fail because their cwd is a worktree we just removed.
	m.rebindGitToRepo()

	// Track whether we had to move the process cwd out of a removed worktree.
	needsCd := false

	// Determine the current branch so we don't try to delete it (git refuses)
	currentBranch, _ := m.git.CurrentBranch()

	// Clean up any remaining worktrees and git branches
	for _, branch := range stack.Branches {
		// Try to remove worktree if it exists
		if branch.WorktreePath != "" {
			if _, err := os.Stat(branch.WorktreePath); err == nil {
				// If our process cwd is inside this worktree, move out first.
				cwd, _ := os.Getwd()
				cwdResolved, _ := filepath.EvalSymlinks(cwd)
				wtResolved, _ := filepath.EvalSymlinks(branch.WorktreePath)
				if cwdResolved == wtResolved || strings.HasPrefix(cwdResolved, wtResolved+string(os.PathSeparator)) {
					m.moveCwdOutOf(branch.WorktreePath)
					needsCd = true
				}
				if err := m.git.RemoveWorktree(branch.WorktreePath, true, branch.Name); err != nil {
					fmt.Fprintf(os.Stderr, "  Warning: failed to remove worktree %s: %v\n", branch.WorktreePath, err)
				}
			}
		}
		// Try to delete git branch if it still exists (skip current branch — git won't allow it)
		if branch.Name != currentBranch && m.git.BranchExists(branch.Name) {
			if err := m.git.DeleteBranch(branch.Name, true); err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: failed to delete branch %s: %v\n", branch.Name, err)
			}
		}
		// Remove from cache
		delete(cache.Branches, branch.Name)
	}

	// Remove the stack
	delete(m.stackConfig.Stacks, stackHash)

	return needsCd, m.stackConfig.Save(m.repoDir)
}

// MarkBranchMerged marks a branch as merged - deletes worktree and git branch but keeps metadata in config
// This allows merged branches to still show up in ezs ls/status with strikethrough
// The tree structure is NOT modified - children stay under the merged parent for display order.
// The effective git parent for children is computed at runtime by skipping merged ancestors.
func (m *Manager) MarkBranchMerged(branchName string) error {
	// Check if branch exists
	branch := m.GetBranch(branchName)
	if branch == nil {
		return fmt.Errorf("branch '%s' not found in any stack", branchName)
	}

	// Rebind git to the main worktree and hop cwd out of the branch worktree
	// before we remove it — otherwise `git worktree remove` / `git branch -D`
	// run from a vanished directory and fail with chdir errors.
	m.rebindGitToRepo()
	m.moveCwdOutOf(branch.WorktreePath)

	// Remove the worktree and branch from git
	worktreePath := branch.WorktreePath
	if worktreePath == "" {
		// WorktreePath missing from config - look it up from git worktree list
		if wts, err := m.git.ListWorktrees(); err == nil {
			for _, wt := range wts {
				if wt.Branch == branchName {
					worktreePath = wt.Path
					break
				}
			}
		}
	}
	if worktreePath != "" {
		// RemoveWorktree handles both worktree removal and branch deletion
		_ = m.git.RemoveWorktree(worktreePath, true, branchName)
	} else if m.git.BranchExists(branchName) {
		// No worktree at all - just delete the git branch
		_ = m.git.DeleteBranch(branchName, true)
	}

	// Update cache
	cache := m.stackConfig.Cache
	bc := cache.GetBranchCache(branchName)
	if bc == nil {
		bc = &config.BranchCache{}
	}
	bc.IsMerged = true
	bc.WorktreePath = ""
	cache.SetBranchCache(branchName, bc)

	// Update runtime branch object
	branch.IsMerged = true
	branch.WorktreePath = ""

	// Repopulate branches to update effective parents for children
	// (children's Parent field will now point to this branch's parent since this branch is merged)
	for _, stack := range m.stackConfig.Stacks {
		if stack.HasBranch(branchName) {
			stack.PopulateBranchesWithCache(cache)
			break
		}
	}

	// Save the config (tree structure unchanged, just cache updated)
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return fmt.Errorf("failed to save stack config: %w", err)
	}

	return nil
}

// MarkBranchRemote marks a branch as belonging to another contributor.
// Remote branches are not rebased during sync. Optionally sets PR URL.
// If remote is non-empty, it specifies the git remote to push to (for fork PRs).
func (m *Manager) MarkBranchRemote(branchName, prURL, remote string) error {
	cache := m.stackConfig.Cache
	bc := cache.GetBranchCache(branchName)
	if bc == nil {
		bc = &config.BranchCache{}
	}
	bc.IsRemote = true
	if prURL != "" {
		bc.PRUrl = prURL
	}
	if remote != "" {
		bc.Remote = remote
	}
	cache.SetBranchCache(branchName, bc)

	// Update runtime branch objects
	for _, stack := range m.stackConfig.Stacks {
		if stack.HasBranch(branchName) {
			stack.PopulateBranchesWithCache(cache)
			break
		}
	}

	return m.stackConfig.Save(m.repoDir)
}
