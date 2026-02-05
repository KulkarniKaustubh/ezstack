package stack

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ezstack/ezstack/internal/config"
	"github.com/ezstack/ezstack/internal/git"
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
	m.stackConfig.Stacks[branchName] = &config.Stack{
		Name:     branchName,
		Branches: []*config.Branch{branch},
	}

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

	// Create branch metadata - no worktree path for remote branches
	branch := &config.Branch{
		Name:         branchName,
		Parent:       baseBranch,
		WorktreePath: "", // Remote branches don't have local worktrees
		BaseBranch:   baseBranch,
		IsRemote:     true,
		PRNumber:     prNumber,
		PRUrl:        prURL,
	}

	// Create a new stack with this branch as the root
	m.stackConfig.Stacks[branchName] = &config.Stack{
		Name:     branchName,
		Branches: []*config.Branch{branch},
	}

	// Save the config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	return branch, nil
}

// AddBranchToStack adds an existing branch to a stack (worktree should already exist)
// This is used when the worktree was created externally (e.g., from a remote branch)
func (m *Manager) AddBranchToStack(name, parentBranch, worktreeDir string) (*config.Branch, error) {
	// Create branch metadata
	branch := &config.Branch{
		Name:         name,
		Parent:       parentBranch,
		WorktreePath: worktreeDir,
		BaseBranch:   parentBranch,
	}

	// Find the stack for the parent
	stackName := m.findStackForBranch(parentBranch)
	if stackName == "" {
		return nil, fmt.Errorf("parent branch '%s' not found in any stack", parentBranch)
	}

	// Add to existing stack
	m.stackConfig.Stacks[stackName].Branches = append(
		m.stackConfig.Stacks[stackName].Branches,
		branch,
	)

	// Save the config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	return branch, nil
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

	// Create branch metadata
	branch := &config.Branch{
		Name:         name,
		Parent:       parentBranch,
		WorktreePath: worktreeDir,
		BaseBranch:   parentBranch,
	}

	// Find or create the stack
	stackName := m.findStackForBranch(parentBranch)
	if stackName == "" {
		// This is a new stack starting from main/master
		stackName = name
		m.stackConfig.Stacks[stackName] = &config.Stack{
			Name:     stackName,
			Branches: []*config.Branch{branch},
		}
	} else {
		// Add to existing stack
		m.stackConfig.Stacks[stackName].Branches = append(
			m.stackConfig.Stacks[stackName].Branches,
			branch,
		)
	}

	// Save the config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	return branch, nil
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

// GetStackDescription generates a markdown description of the stack
func (m *Manager) GetStackDescription(stack *config.Stack, currentBranch string) string {
	var sb strings.Builder
	sb.WriteString("## PR Stack\n\n")

	// Sort branches topologically (parent -> child order)
	sortedBranches := config.SortBranchesTopologically(stack.Branches)

	for i, branch := range sortedBranches {
		prefix := ""
		if branch.Name == currentBranch {
			prefix = "➡️ "
		}
		if branch.PRUrl != "" {
			sb.WriteString(fmt.Sprintf("%s%d. [%s](%s)\n", prefix, i+1, branch.Name, branch.PRUrl))
		} else {
			sb.WriteString(fmt.Sprintf("%s%d. %s\n", prefix, i+1, branch.Name))
		}
	}

	return sb.String()
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

	// Remove from stack config
	for stackName, stack := range m.stackConfig.Stacks {
		for i, b := range stack.Branches {
			if b.Name == branchName {
				// Remove this branch from the stack
				stack.Branches = append(stack.Branches[:i], stack.Branches[i+1:]...)

				// If this was the only branch, remove the entire stack
				if len(stack.Branches) == 0 {
					delete(m.stackConfig.Stacks, stackName)
				}

				// Update children to point to this branch's parent
				for _, child := range children {
					child.Parent = branch.Parent
					child.BaseBranch = branch.Parent
				}

				break
			}
		}
	}

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

	// Get children before removing
	children := m.GetChildren(branchName)

	// Remove from stack config (but don't touch git)
	for stackName, stack := range m.stackConfig.Stacks {
		for i, b := range stack.Branches {
			if b.Name == branchName {
				// Remove this branch from the stack
				stack.Branches = append(stack.Branches[:i], stack.Branches[i+1:]...)

				// If this was the only branch, remove the entire stack
				if len(stack.Branches) == 0 {
					delete(m.stackConfig.Stacks, stackName)
				}

				// Update children to point to this branch's parent
				for _, child := range children {
					child.Parent = branch.Parent
					child.BaseBranch = branch.Parent
				}

				break
			}
		}
	}

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
	oldStackName := m.findStackForBranch(branch.Name)
	newParentStackName := m.findStackForBranch(newParentName)

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

	// Update the branch's parent
	branch.Parent = newParentName
	branch.BaseBranch = newParentName

	// Handle stack reorganization if needed
	if oldStackName != newParentStackName && newParentStackName != "" {
		// Move branch (and its children) to the new parent's stack
		if err := m.moveBranchToStack(branch.Name, oldStackName, newParentStackName); err != nil {
			return nil, fmt.Errorf("failed to move branch to new stack: %w", err)
		}
	} else if newParentStackName == "" && m.IsMainBranch(newParentName) {
		// New parent is main - branch becomes root of its own stack (or stays in current)
		// Just update the parent pointer, stack structure stays the same
	}

	// Save the config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	return branch, nil
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

	// Create branch metadata
	branch := &config.Branch{
		Name:         branchName,
		Parent:       newParentName,
		WorktreePath: worktreePath,
		BaseBranch:   newParentName,
	}

	// Find the stack for the new parent
	newParentStackName := m.findStackForBranch(newParentName)

	if newParentStackName != "" {
		// Add to existing stack
		m.stackConfig.Stacks[newParentStackName].Branches = append(
			m.stackConfig.Stacks[newParentStackName].Branches,
			branch,
		)
	} else if m.IsMainBranch(newParentName) {
		// New parent is main - create a new stack with this branch as root
		m.stackConfig.Stacks[branchName] = &config.Stack{
			Name:     branchName,
			Branches: []*config.Branch{branch},
		}
	} else {
		return nil, fmt.Errorf("new parent '%s' is not in any stack and is not main/master", newParentName)
	}

	// Save the config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	return branch, nil
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

	// Collect all branches to move (the branch and all its descendants)
	branchesToMove := m.collectDescendants(branchName)
	branchesToMove = append([]*config.Branch{m.GetBranch(branchName)}, branchesToMove...)

	// Remove from old stack
	var remainingBranches []*config.Branch
	for _, b := range fromStack.Branches {
		shouldRemove := false
		for _, toMove := range branchesToMove {
			if b.Name == toMove.Name {
				shouldRemove = true
				break
			}
		}
		if !shouldRemove {
			remainingBranches = append(remainingBranches, b)
		}
	}
	fromStack.Branches = remainingBranches

	// If old stack is now empty, remove it
	if len(fromStack.Branches) == 0 {
		delete(m.stackConfig.Stacks, fromStackName)
	}

	// Add to new stack
	toStack.Branches = append(toStack.Branches, branchesToMove...)

	return nil
}

// collectDescendants returns all descendant branches of a given branch
func (m *Manager) collectDescendants(branchName string) []*config.Branch {
	var descendants []*config.Branch
	children := m.GetChildren(branchName)
	for _, child := range children {
		descendants = append(descendants, child)
		descendants = append(descendants, m.collectDescendants(child.Name)...)
	}
	return descendants
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
	for _, branchName := range branchNames {
		// Find and remove from stack
		for stackName, stack := range m.stackConfig.Stacks {
			for i, branch := range stack.Branches {
				if branch.Name == branchName {
					// Update children to point to this branch's parent
					children := m.GetChildren(branchName)
					for _, child := range children {
						child.Parent = branch.Parent
						child.BaseBranch = branch.Parent
					}
					// Remove from slice
					stack.Branches = append(stack.Branches[:i], stack.Branches[i+1:]...)
					// If stack is now empty, remove it
					if len(stack.Branches) == 0 {
						delete(m.stackConfig.Stacks, stackName)
					}
					break
				}
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

	// Create branch metadata
	branch := &config.Branch{
		Name:         branchName,
		Parent:       parentName,
		WorktreePath: worktreePath,
		BaseBranch:   parentName,
	}

	// Find or create the stack
	stackName := m.findStackForBranch(parentName)
	if stackName == "" {
		// Parent is main/master - create new stack
		stackName = branchName
		m.stackConfig.Stacks[stackName] = &config.Stack{
			Name:     stackName,
			Branches: []*config.Branch{branch},
		}
	} else {
		// Add to existing stack
		m.stackConfig.Stacks[stackName].Branches = append(
			m.stackConfig.Stacks[stackName].Branches,
			branch,
		)
	}

	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return nil, fmt.Errorf("failed to save stack config: %w", err)
	}

	return branch, nil
}

// MarkBranchMerged marks a branch as merged - deletes worktree and git branch but keeps metadata in config
// This allows merged branches to still show up in ezs ls/status with strikethrough
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

	// Mark as merged and clear worktree path
	branch.IsMerged = true
	branch.WorktreePath = ""

	// Update children to point to this branch's parent (they need to rebase onto main now)
	children := m.GetChildren(branchName)
	for _, child := range children {
		child.Parent = branch.Parent
		child.BaseBranch = branch.Parent
	}

	// Save the config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return fmt.Errorf("failed to save stack config: %w", err)
	}

	return nil
}
