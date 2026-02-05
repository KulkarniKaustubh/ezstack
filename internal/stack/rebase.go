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

		if branch.IsRemote {
			// Remote branch - skip entirely (no local worktree, belongs to someone else)
			// Don't add to results since we didn't do anything
			continue
		}

		result := RebaseResult{Branch: branch.Name}
		g := git.New(branch.WorktreePath)

		// Check if parent is a remote branch - if so, use origin/<parent>
		parentRef := branch.Parent
		for _, b := range stack.Branches {
			if b.Name == branch.Parent && b.IsRemote {
				parentRef = "origin/" + branch.Parent
				break
			}
		}

		// Local branch - rebase onto parent
		if err := g.Rebase(parentRef); err != nil {
			hasConflict, _ := g.IsRebaseInProgress()
			if hasConflict {
				result.HasConflict = true
				result.Error = fmt.Errorf("rebase conflict in %s - resolve and run 'git rebase --continue'", branch.Name)
				results = append(results, result)
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
			// Skip remote branches (we can't rebase them anyway)
			if branch.IsRemote {
				continue
			}

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
			if branch.IsRemote {
				// Remote branch - skip entirely (no local worktree, belongs to someone else)
				continue
			}

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
					prevMerged, _ := m.git.IsBranchMerged(stack.Branches[i-1].Name, "origin/"+baseBranch)
					if !prevMerged {
						newParent = stack.Branches[i-1].Name
					}
				}

				oldParent := branch.Parent
				branch.Parent = newParent
				branch.BaseBranch = newParent

				g := git.New(branch.WorktreePath)
				fmt.Printf("Rebasing %s onto %s (was %s)\n", branch.Name, newParent, oldParent)
				if err := g.RebaseOnto(newParent, oldParent); err != nil {
					fmt.Printf("Rebase failed for %s. Resolve in: %s\n", branch.Name, branch.WorktreePath)
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
	currentStack, currentBranch, err := m.GetCurrentStack()
	if err != nil {
		return err
	}

	if currentBranch.IsRemote {
		// Remote branch - cannot rebase (no local worktree, belongs to someone else)
		return fmt.Errorf("cannot rebase remote branch '%s' - it has no local worktree", currentBranch.Name)
	}

	// Check if parent is a remote branch - if so, use origin/<parent>
	parentRef := currentBranch.Parent
	for _, b := range currentStack.Branches {
		if b.Name == currentBranch.Parent && b.IsRemote {
			parentRef = "origin/" + currentBranch.Parent
			break
		}
	}

	fmt.Printf("Rebasing %s onto %s\n", currentBranch.Name, parentRef)
	return m.git.Rebase(parentRef)
}

// RebaseChildren rebases all child branches after updating the current branch
func (m *Manager) RebaseChildren() error {
	_, currentBranch, err := m.GetCurrentStack()
	if err != nil {
		return err
	}

	children := m.GetChildren(currentBranch.Name)
	for _, child := range children {
		if child.IsRemote {
			// Remote branch - skip entirely (no local worktree, belongs to someone else)
			// Note: It's unusual for a remote branch to be a child, but handle it gracefully
			fmt.Printf("Skipping remote branch %s (no local worktree)\n", child.Name)
			continue
		}

		// Local branch - rebase onto parent
		g := git.New(child.WorktreePath)
		fmt.Printf("Rebasing child branch %s onto %s\n", child.Name, currentBranch.Name)
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
