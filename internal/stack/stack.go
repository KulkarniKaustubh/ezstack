package stack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
)

// Manager handles stack operations
type Manager struct {
	git         *git.Git
	config      *config.Config
	repoConfig  *config.RepoConfig
	stackConfig *config.StackConfig
	repoDir     string
}

// NewManager creates a new stack manager
func NewManager(repoDir string) (*Manager, error) {
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

	return &Manager{
		git:         g,
		config:      cfg,
		repoConfig:  repoConfig,
		stackConfig: stackCfg,
		repoDir:     mainWorktree,
	}, nil
}

// GetRepoDir returns the main repository directory
func (m *Manager) GetRepoDir() string {
	return m.repoDir
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
	hash := config.GenerateStackHash(branchName)
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

// RegisterRemoteBranch registers a remote branch (someone else's PR) as the root of a new stack
// Remote branches don't have local worktrees - only their child branches do
func (m *Manager) RegisterRemoteBranch(branchName, baseBranch string, prNumber int, prURL string) (*config.Branch, error) {
	// Check if branch is already registered
	if existing := m.GetBranch(branchName); existing != nil {
		return nil, fmt.Errorf("branch '%s' is already registered in a stack", branchName)
	}

	// Create a new stack with this branch as the root
	hash := config.GenerateStackHash(branchName)
	stack := &config.Stack{
		Hash: hash,
		Root: baseBranch,
		Tree: config.BranchTree{
			branchName: config.BranchTree{},
		},
	}
	m.stackConfig.Stacks[hash] = stack

	// Update cache
	cache := m.stackConfig.Cache
	cache.SetBranchCache(branchName, &config.BranchCache{
		PRNumber: prNumber,
		PRUrl:    prURL,
		IsRemote: true,
	})

	// Populate branches from tree with cache
	stack.PopulateBranchesWithCache(cache)

	// Save the config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	// Return the branch from the populated list
	for _, b := range stack.Branches {
		if b.Name == branchName {
			return b, nil
		}
	}
	return nil, fmt.Errorf("failed to create branch")
}

// AddBranchToStack adds an existing branch to a stack (worktree should already exist)
// This is used when the worktree was created externally (e.g., from a remote branch)
func (m *Manager) AddBranchToStack(name, parentBranch, worktreeDir string) (*config.Branch, error) {
	// Find the stack for the parent
	stackKey := m.findStackForBranch(parentBranch)
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

// CreateBranch creates a new branch in the stack
func (m *Manager) CreateBranch(name, parentBranch, worktreeDir string) (*config.Branch, error) {
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

	// Find or create the stack
	stackKey := m.findStackForBranch(parentBranch)
	var stack *config.Stack
	if stackKey == "" {
		// This is a new stack starting from main/master
		hash := config.GenerateStackHash(name)
		stack = &config.Stack{
			Hash: hash,
			Root: parentBranch, // parentBranch is main/master
			Tree: config.BranchTree{
				name: config.BranchTree{},
			},
		}
		m.stackConfig.Stacks[hash] = stack
	} else {
		// Add to existing stack
		stack = m.stackConfig.Stacks[stackKey]
		stack.AddBranch(name, parentBranch)
	}

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

// GetCurrentStack returns the stack for the current branch
func (m *Manager) GetCurrentStack() (*config.Stack, *config.Branch, error) {
	currentBranch, err := m.git.CurrentBranch()
	if err != nil {
		return nil, nil, err
	}

	for _, stack := range m.stackConfig.Stacks {
		for _, branch := range stack.Branches {
			if branch.Name == currentBranch {
				return stack, branch, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("current branch %s is not part of any stack", currentBranch)
}

// ListStacks returns all stacks
func (m *Manager) ListStacks() []*config.Stack {
	var stacks []*config.Stack
	for _, stack := range m.stackConfig.Stacks {
		stacks = append(stacks, stack)
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

// IsMainBranch checks if a branch is the main/master branch
func (m *Manager) IsMainBranch(name string) bool {
	baseBranch := m.config.GetBaseBranch(m.repoDir)
	return name == "main" || name == "master" || name == baseBranch
}

// GetStackByHash finds a stack by hash prefix. Returns error if 0 or >1 stacks match.
func (m *Manager) GetStackByHash(prefix string) (*config.Stack, error) {
	// Strip leading # if present
	prefix = strings.TrimPrefix(prefix, "#")

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

	// Check for child branches
	children := m.GetChildren(branchName)
	if len(children) > 0 && !force {
		childNames := make([]string, len(children))
		for i, c := range children {
			childNames[i] = c.Name
		}
		return fmt.Errorf("cannot delete branch '%s': has child branches: %s. Use --force to delete anyway", branchName, strings.Join(childNames, ", "))
	}

	// Remove the worktree and branch from git (only if not a remote branch)
	if !branch.IsRemote && branch.WorktreePath != "" {
		if err := m.git.RemoveWorktree(branch.WorktreePath, true, branchName); err != nil {
			return fmt.Errorf("failed to remove worktree: %w", err)
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

// ReparentBranch changes the parent of a branch to a new parent
// This handles several cases:
// 1. Branch is already in a stack - just update parent pointer
// 2. Branch is standalone (not in any stack) - add it to the new parent's stack
// 3. New parent is in a different stack - merge stacks or move branch
// If doRebase is true, performs git rebase --onto to move commits
// Returns the updated branch and any error
func (m *Manager) ReparentBranch(branchName, newParentName string, doRebase bool) (*config.Branch, error) {
	// Validate new parent exists (either in a stack or is main/master)
	if !m.IsMainBranch(newParentName) {
		newParentBranch := m.GetBranch(newParentName)
		if newParentBranch == nil {
			// Check if it's a git branch that exists but not in ezstack
			if !m.git.BranchExists(newParentName) {
				return nil, fmt.Errorf("new parent '%s' does not exist", newParentName)
			}
		}
	}

	// Check if branch is already registered in a stack
	existingBranch := m.GetBranch(branchName)

	if existingBranch != nil {
		// Branch is already in a stack - update its parent
		return m.reparentExistingBranch(existingBranch, newParentName, doRebase)
	}

	// Branch is not in any stack - need to add it
	return m.addBranchWithParent(branchName, newParentName, doRebase)
}

// reparentExistingBranch handles reparenting a branch that's already in a stack
func (m *Manager) reparentExistingBranch(branch *config.Branch, newParentName string, doRebase bool) (*config.Branch, error) {
	oldParent := branch.Parent
	oldStackKey := m.findStackForBranch(branch.Name)
	newParentStackKey := m.findStackForBranch(newParentName)

	// Prevent circular dependencies
	if m.wouldCreateCycle(branch.Name, newParentName) {
		return nil, fmt.Errorf("cannot reparent: would create circular dependency")
	}

	// Perform git rebase if requested
	if doRebase && branch.WorktreePath != "" {
		g := git.New(branch.WorktreePath)

		// Get the merge-base between current branch and old parent
		oldParentRef := m.getParentRef(oldParent)
		mergeBase, err := m.git.GetMergeBase(branch.Name, oldParentRef)
		if err != nil {
			mergeBase = oldParentRef
		}

		// Determine new parent ref - prefer origin/ if remote exists, else local
		newParentRef := newParentName
		if m.IsMainBranch(newParentName) {
			// For main/master, use origin/ only if remote exists
			if m.git.RemoteBranchExists(newParentName) {
				newParentRef = "origin/" + newParentName
			}
		} else {
			newParentBranch := m.GetBranch(newParentName)
			if newParentBranch != nil && newParentBranch.IsRemote {
				newParentRef = "origin/" + newParentName
			}
		}

		// Rebase onto new parent
		rebaseResult := g.RebaseOntoNonInteractive(newParentRef, mergeBase)
		if rebaseResult.HasConflict {
			return nil, fmt.Errorf("rebase conflict - resolve conflicts in %s and run: git rebase --continue", branch.WorktreePath)
		} else if rebaseResult.Error != nil {
			return nil, fmt.Errorf("rebase failed: %w", rebaseResult.Error)
		}
	}

	cache := m.stackConfig.Cache

	// Handle stack reorganization using tree methods
	oldStack := m.stackConfig.Stacks[oldStackKey]

	if oldStackKey != newParentStackKey && newParentStackKey != "" {
		// Move branch (and its children) to the new parent's stack
		if err := m.moveBranchToStack(branch.Name, oldStackKey, newParentStackKey); err != nil {
			return nil, fmt.Errorf("failed to move branch to new stack: %w", err)
		}
	} else if newParentStackKey == "" && m.IsMainBranch(newParentName) {
		// New parent is main - use ReparentBranch to move in tree
		// The branch stays in the same stack but is now a root-level branch
		oldStack.ReparentBranch(branch.Name, "") // Empty parent means root level
		oldStack.PopulateBranchesWithCache(cache)
	} else {
		// Same stack, different parent - use ReparentBranch
		oldStack.ReparentBranch(branch.Name, newParentName)
		oldStack.PopulateBranchesWithCache(cache)
	}

	// Save the config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	// Return the updated branch
	return m.GetBranch(branch.Name), nil
}

// addBranchWithParent adds a standalone git branch to a stack with the specified parent
func (m *Manager) addBranchWithParent(branchName, newParentName string, doRebase bool) (*config.Branch, error) {
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

	// Perform git rebase if requested and we have a worktree
	if doRebase && worktreePath != "" {
		g := git.New(worktreePath)

		// Determine new parent ref - prefer origin/ if remote exists, else local
		newParentRef := newParentName
		if m.IsMainBranch(newParentName) {
			// For main/master, use origin/ only if remote exists
			if m.git.RemoteBranchExists(newParentName) {
				newParentRef = "origin/" + newParentName
			}
		} else {
			newParentBranch := m.GetBranch(newParentName)
			if newParentBranch != nil && newParentBranch.IsRemote {
				newParentRef = "origin/" + newParentName
			}
		}

		// Simple rebase onto new parent
		rebaseResult := g.RebaseNonInteractive(newParentRef)
		if rebaseResult.HasConflict {
			return nil, fmt.Errorf("rebase conflict - resolve conflicts in %s and run: git rebase --continue", worktreePath)
		} else if rebaseResult.Error != nil {
			return nil, fmt.Errorf("rebase failed: %w", rebaseResult.Error)
		}
	}

	// Find the stack for the new parent
	newParentStackKey := m.findStackForBranch(newParentName)
	var stack *config.Stack

	if newParentStackKey != "" {
		// Add to existing stack
		stack = m.stackConfig.Stacks[newParentStackKey]
		stack.AddBranch(branchName, newParentName)
	} else if m.IsMainBranch(newParentName) {
		// New parent is main - create a new stack with this branch as root
		hash := config.GenerateStackHash(branchName)
		stack = &config.Stack{
			Hash: hash,
			Root: newParentName,
			Tree: config.BranchTree{
				branchName: config.BranchTree{},
			},
		}
		m.stackConfig.Stacks[hash] = stack
	} else {
		return nil, fmt.Errorf("new parent '%s' is not in any stack and is not main/master", newParentName)
	}

	// Update cache
	cache := m.stackConfig.Cache
	cache.SetBranchCache(branchName, &config.BranchCache{
		WorktreePath: worktreePath,
	})

	// Repopulate branches from tree with cache
	stack.PopulateBranchesWithCache(cache)

	// Save the config
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

// wouldCreateCycle checks if reparenting branchName to newParentName would create a cycle
func (m *Manager) wouldCreateCycle(branchName, newParentName string) bool {
	// Walk up from newParentName to see if we ever reach branchName
	current := newParentName
	visited := make(map[string]bool)

	for !m.IsMainBranch(current) {
		if current == branchName {
			return true
		}
		if visited[current] {
			return false // Already visited, no cycle through branchName
		}
		visited[current] = true

		branch := m.GetBranch(current)
		if branch == nil {
			return false // Not in stack, can't continue
		}
		current = branch.Parent
	}
	return false
}

// moveBranchToStack moves a branch and all its descendants from one stack to another
func (m *Manager) moveBranchToStack(branchName, fromStackName, toStackName string) error {
	fromStack := m.stackConfig.Stacks[fromStackName]
	toStack := m.stackConfig.Stacks[toStackName]

	if fromStack == nil || toStack == nil {
		return fmt.Errorf("invalid stack names")
	}

	// Get the branch to find the new parent
	branch := m.GetBranch(branchName)
	if branch == nil {
		return fmt.Errorf("branch '%s' not found", branchName)
	}
	newParentName := branch.Parent

	cache := m.stackConfig.Cache

	// Extract subtree from old stack (includes all descendants)
	subtree := fromStack.ExtractSubtree(branchName)
	if subtree == nil {
		return fmt.Errorf("branch '%s' not found in source stack", branchName)
	}

	// Repopulate source stack
	fromStack.PopulateBranchesWithCache(cache)

	// If old stack is now empty, remove it
	if len(fromStack.Tree) == 0 {
		delete(m.stackConfig.Stacks, fromStackName)
	}

	// Add subtree to new stack under the new parent
	toStack.AddSubtree(branchName, subtree, newParentName)
	toStack.PopulateBranchesWithCache(cache)

	return nil
}

// GetAllBranchesInAllStacks returns all branches across all stacks
func (m *Manager) GetAllBranchesInAllStacks() []*config.Branch {
	var allBranches []*config.Branch
	for _, stack := range m.stackConfig.Stacks {
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
			// Skip merged branches and remote branches (they don't have local worktrees)
			if branch.IsMerged || branch.IsRemote {
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
		hash := config.GenerateStackHash(branchName)
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

// DeleteStack removes an entire stack from config, cleaning up any remaining worktrees and git branches.
// This is intended for fully merged stacks where all branches have been completed.
func (m *Manager) DeleteStack(stackHash string) error {
	stack := m.stackConfig.Stacks[stackHash]
	if stack == nil {
		return fmt.Errorf("stack '%s' not found", stackHash)
	}

	cache := m.stackConfig.Cache

	// Clean up any remaining worktrees and git branches
	for _, branch := range stack.Branches {
		if branch.IsRemote {
			continue
		}
		// Try to remove worktree if it exists
		if branch.WorktreePath != "" {
			if _, err := os.Stat(branch.WorktreePath); err == nil {
				_ = m.git.RemoveWorktree(branch.WorktreePath, true, branch.Name)
			}
		}
		// Try to delete git branch if it still exists
		if m.git.BranchExists(branch.Name) {
			_ = m.git.DeleteBranch(branch.Name, true)
		}
		// Remove from cache
		delete(cache.Branches, branch.Name)
	}

	// Remove the stack
	delete(m.stackConfig.Stacks, stackHash)

	return m.stackConfig.Save(m.repoDir)
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

	// Remove the worktree and branch from git (only if not a remote branch)
	if !branch.IsRemote && branch.WorktreePath != "" {
		if err := m.git.RemoveWorktree(branch.WorktreePath, true, branchName); err != nil {
			// Log error but continue - worktree might already be gone
			// Don't fail the whole operation
		}
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
