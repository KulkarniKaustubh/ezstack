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

	for i, branch := range stack.Branches {
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
