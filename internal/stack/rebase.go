package stack

import (
	"fmt"
	"os"

	"github.com/ezstack/ezstack/internal/git"
)

// RebaseResult represents the result of a rebase operation
type RebaseResult struct {
	Branch      string
	Success     bool
	HasConflict bool
	Error       error
}

// RebaseStack rebases all branches in a stack starting from the given branch
func (m *Manager) RebaseStack(startFromCurrent bool) ([]RebaseResult, error) {
	stack, currentBranch, err := m.GetCurrentStack()
	if err != nil {
		return nil, err
	}

	var results []RebaseResult
	var startIndex int

	if startFromCurrent {
		// Find the index of the current branch
		for i, b := range stack.Branches {
			if b.Name == currentBranch.Name {
				startIndex = i
				break
			}
		}
	}

	// Rebase each branch onto its parent
	for i := startIndex; i < len(stack.Branches); i++ {
		branch := stack.Branches[i]

		// Create git wrapper for this worktree
		g := git.New(branch.WorktreePath)

		result := RebaseResult{Branch: branch.Name}

		// Rebase onto parent
		err := g.Rebase(branch.Parent)
		if err != nil {
			// Check if it's a conflict
			hasConflict, _ := g.IsRebaseInProgress()
			if hasConflict {
				result.HasConflict = true
				result.Error = fmt.Errorf("rebase conflict in %s - resolve and run 'git rebase --continue'", branch.Name)
				results = append(results, result)
				// Stop here - user needs to resolve conflicts
				return results, nil
			}
			result.Error = err
		} else {
			result.Success = true
		}
		results = append(results, result)
	}

	return results, nil
}

// MergedBranchInfo contains information about a branch with a merged parent
type MergedBranchInfo struct {
	Branch       string
	MergedParent string
}

// DetectMergedParents checks for branches whose parents have been merged to main
func (m *Manager) DetectMergedParents() ([]MergedBranchInfo, error) {
	// Fetch latest
	if err := m.git.Fetch(); err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}

	baseBranch := m.config.DefaultBaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	var results []MergedBranchInfo

	// Check each stack for merged branches
	for _, stack := range m.stackConfig.Stacks {
		for _, branch := range stack.Branches {
			// Check if this branch's parent was merged
			if m.IsMainBranch(branch.Parent) {
				continue
			}

			isMerged, err := m.git.IsBranchMerged(branch.Parent, "origin/"+baseBranch)
			if err != nil {
				continue
			}

			if isMerged {
				results = append(results, MergedBranchInfo{
					Branch:       branch.Name,
					MergedParent: branch.Parent,
				})
			}
		}
	}

	return results, nil
}

// SyncWithMain syncs the stack when a parent branch has been merged
func (m *Manager) SyncWithMain() error {
	// Fetch latest
	if err := m.git.Fetch(); err != nil {
		return fmt.Errorf("failed to fetch: %w", err)
	}

	baseBranch := m.config.DefaultBaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	// Check each stack for merged branches
	for _, stack := range m.stackConfig.Stacks {
		for i, branch := range stack.Branches {
			// Check if this branch's parent was merged
			if m.IsMainBranch(branch.Parent) {
				continue
			}

			isMerged, err := m.git.IsBranchMerged(branch.Parent, "origin/"+baseBranch)
			if err != nil {
				continue
			}

			if isMerged {
				fmt.Printf("Branch %s's parent (%s) has been merged to %s\n", branch.Name, branch.Parent, baseBranch)

				// Update this branch to target the new parent
				newParent := baseBranch
				if i > 0 && !m.IsMainBranch(stack.Branches[i-1].Parent) {
					// Check if the previous branch in stack is also affected
					prevMerged, _ := m.git.IsBranchMerged(stack.Branches[i-1].Name, "origin/"+baseBranch)
					if !prevMerged {
						newParent = stack.Branches[i-1].Name
					}
				}

				oldParent := branch.Parent
				branch.Parent = newParent
				branch.BaseBranch = newParent

				// Rebase this branch onto its new parent
				g := git.New(branch.WorktreePath)
				fmt.Printf("Rebasing %s onto %s (was %s)\n", branch.Name, newParent, oldParent)

				// Use rebase --onto to transplant commits
				if err := g.RebaseOnto(newParent, oldParent); err != nil {
					fmt.Printf("Rebase failed for %s. Please resolve conflicts manually in %s\n", branch.Name, branch.WorktreePath)
					fmt.Printf("After resolving, run: cd %s && git rebase --continue\n", branch.WorktreePath)
					return err
				}
			}
		}
	}

	// Save updated config
	return m.stackConfig.Save(m.repoDir)
}

// RebaseOnParent rebases the current branch onto its updated parent
func (m *Manager) RebaseOnParent() error {
	_, currentBranch, err := m.GetCurrentStack()
	if err != nil {
		return err
	}

	fmt.Printf("Rebasing %s onto %s\n", currentBranch.Name, currentBranch.Parent)
	return m.git.Rebase(currentBranch.Parent)
}

// RebaseChildren rebases all child branches after updating the current branch
func (m *Manager) RebaseChildren() error {
	_, currentBranch, err := m.GetCurrentStack()
	if err != nil {
		return err
	}

	children := m.GetChildren(currentBranch.Name)
	for _, child := range children {
		fmt.Printf("Rebasing child branch %s onto %s\n", child.Name, currentBranch.Name)

		g := git.New(child.WorktreePath)
		if err := g.Rebase(currentBranch.Name); err != nil {
			hasConflict, _ := g.IsRebaseInProgress()
			if hasConflict {
				fmt.Printf("Conflict in %s. Resolve in: %s\n", child.Name, child.WorktreePath)
				fmt.Fprintf(os.Stderr, "cd %s && git status\n", child.WorktreePath)
				return fmt.Errorf("conflict in child branch %s", child.Name)
			}
			return err
		}

		// Recursively rebase this child's children
		childMgr, err := NewManager(child.WorktreePath)
		if err != nil {
			return err
		}
		if err := childMgr.RebaseChildren(); err != nil {
			return err
		}
	}

	return nil
}
