package stack

import (
	"fmt"

	"github.com/ezstack/ezstack/internal/git"
	"github.com/ezstack/ezstack/internal/github"
)

// RebaseResult represents the result of a rebase operation
type RebaseResult struct {
	Branch       string
	Success      bool
	HasConflict  bool
	Error        error
	SyncedParent string // If non-empty, parent was merged and we synced to this new parent
	WorktreePath string // Path to the worktree (useful for conflict resolution)
	BehindBy     int    // Number of commits behind (for branches that need sync with origin/main)
}



// SyncInfo contains information about a branch that needs syncing
type SyncInfo struct {
	Branch        string
	MergedParent  string // Non-empty if parent was merged
	BehindBy      int    // Number of commits behind target
	BehindParent  string // Non-empty if behind a non-main parent
	NeedsSync     bool   // True if branch needs to be synced
}

// getParentRef returns the git ref for a parent branch
// For remote branches (IsRemote=true), returns origin/<name>
// For local branches, returns the branch name
func (m *Manager) getParentRef(parentName string) string {
	parentBranch := m.GetBranch(parentName)
	if parentBranch != nil && parentBranch.IsRemote {
		return "origin/" + parentName
	}
	return parentName
}

// DetectSyncNeeded checks for branches that need syncing:
// 1. Branches whose parents have been merged to main
// 2. Branches whose parent is main but are behind origin/main
func (m *Manager) DetectSyncNeeded(gh *github.Client) ([]SyncInfo, error) {
	// Fetch latest
	if err := m.git.Fetch(); err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}

	baseBranch := m.config.DefaultBaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	var results []SyncInfo

	// Check each stack for branches needing sync
	for _, stack := range m.stackConfig.Stacks {
		for _, branch := range stack.Branches {
			// Skip remote branches (we can't rebase them anyway)
			if branch.IsRemote {
				continue
			}

			// Case 1: Parent is main - check if behind origin/main
			if m.IsMainBranch(branch.Parent) {
				behindBy, err := m.git.GetCommitsBehind(branch.Name, "origin/"+baseBranch)
				if err == nil && behindBy > 0 {
					results = append(results, SyncInfo{
						Branch:    branch.Name,
						BehindBy:  behindBy,
						NeedsSync: true,
					})
				}
				continue
			}

			// Case 2: Parent is not main - check if parent was merged
			isMerged := false

			// Get the correct ref for the parent (origin/<name> for remote branches)
			parentRef := m.getParentRef(branch.Parent)

			// First try git merge-base (works for true merge commits)
			merged, err := m.git.IsBranchMerged(parentRef, "origin/"+baseBranch)
			if err == nil && merged {
				isMerged = true
			}

			// If git check didn't find it merged, try GitHub API (for squash/rebase merges)
			if !isMerged && gh != nil {
				parentBranch := m.GetBranch(branch.Parent)
				if parentBranch != nil && parentBranch.PRNumber > 0 {
					pr, err := gh.GetPR(parentBranch.PRNumber)
					if err == nil && pr.Merged {
						isMerged = true
					}
				}
			}

			if isMerged {
				results = append(results, SyncInfo{
					Branch:       branch.Name,
					MergedParent: branch.Parent,
					NeedsSync:    true,
				})
				continue
			}

			// Case 3: Parent is not main and not merged - check if behind parent
			// This handles the case where parent branch was updated with new commits
			behindBy, err := m.git.GetCommitsBehind(branch.Name, parentRef)
			if err == nil && behindBy > 0 {
				results = append(results, SyncInfo{
					Branch:       branch.Name,
					BehindBy:     behindBy,
					BehindParent: branch.Parent,
					NeedsSync:    true,
				})
			}
		}
	}

	return results, nil
}

// SyncStack syncs all branches in the stack that need syncing
// This handles three cases:
// 1. Branches whose parent is main but are behind origin/main (simple rebase)
// 2. Branches whose parent was merged (rebase onto main using --onto)
// 3. Branches whose parent is not merged but has new commits (rebase onto parent)
func (m *Manager) SyncStack(gh *github.Client) ([]RebaseResult, error) {
	// Fetch latest
	if err := m.git.Fetch(); err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}

	baseBranch := m.config.DefaultBaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	var results []RebaseResult

	// Check each stack for branches needing sync
	for _, stack := range m.stackConfig.Stacks {
		for i, branch := range stack.Branches {
			if branch.IsRemote {
				continue
			}

			result := RebaseResult{Branch: branch.Name, WorktreePath: branch.WorktreePath}
			g := git.New(branch.WorktreePath)

			// Case 1: Parent is main - check if behind origin/main
			if m.IsMainBranch(branch.Parent) {
				behindBy, err := m.git.GetCommitsBehind(branch.Name, "origin/"+baseBranch)
				if err != nil || behindBy == 0 {
					continue // Not behind, skip
				}

				result.BehindBy = behindBy
				result.SyncedParent = "origin/" + baseBranch

				// Use non-interactive rebase for better conflict detection
				rebaseResult := g.RebaseNonInteractive("origin/" + baseBranch)
				if rebaseResult.HasConflict {
					result.HasConflict = true
					result.Error = fmt.Errorf("resolve conflicts in: %s", branch.WorktreePath)
					results = append(results, result)
					continue
				} else if rebaseResult.Error != nil {
					result.Error = rebaseResult.Error
					results = append(results, result)
					continue
				}
				result.Success = true
				results = append(results, result)
				continue
			}

			// Case 2: Parent is not main - check if parent was merged
			isMerged := false

			// Get the correct ref for the parent (origin/<name> for remote branches)
			parentRef := m.getParentRef(branch.Parent)

			merged, err := m.git.IsBranchMerged(parentRef, "origin/"+baseBranch)
			if err == nil && merged {
				isMerged = true
			}

			if !isMerged && gh != nil {
				parentBranch := m.GetBranch(branch.Parent)
				if parentBranch != nil && parentBranch.PRNumber > 0 {
					pr, err := gh.GetPR(parentBranch.PRNumber)
					if err == nil && pr.Merged {
						isMerged = true
					}
				}
			}

			if isMerged {
				// Parent was merged - determine new parent
				newParent := baseBranch
				if i > 0 && !m.IsMainBranch(stack.Branches[i-1].Parent) {
					prevMerged, _ := m.git.IsBranchMerged(stack.Branches[i-1].Name, "origin/"+baseBranch)
					if !prevMerged {
						newParent = stack.Branches[i-1].Name
					}
				}

				oldParent := branch.Parent
				oldParentRef := m.getParentRef(oldParent) // Use origin/<name> for remote parents
				branch.Parent = newParent
				branch.BaseBranch = newParent
				result.SyncedParent = newParent

				// Find the merge-base between current branch and old parent
				// This is the point where we originally branched from the parent
				// We need to use this as the oldBase for rebase --onto, not the branch name
				// Use m.git (main repo) to get merge-base since branch names are repo-wide
				mergeBase, err := m.git.GetMergeBase(branch.Name, oldParentRef)
				if err != nil {
					// Fallback to using oldParentRef if we can't get merge-base
					mergeBase = oldParentRef
				}

				// Use non-interactive rebase --onto for better conflict detection
				// git rebase --onto origin/main <merge-base>
				// This takes commits from merge-base..HEAD and replays onto origin/main
				rebaseResult := g.RebaseOntoNonInteractive("origin/"+newParent, mergeBase)
				if rebaseResult.HasConflict {
					result.HasConflict = true
					result.Error = fmt.Errorf("resolve conflicts in: %s", branch.WorktreePath)
					results = append(results, result)
					m.stackConfig.Save(m.repoDir)
					continue
				} else if rebaseResult.Error != nil {
					result.Error = rebaseResult.Error
					results = append(results, result)
					m.stackConfig.Save(m.repoDir)
					continue
				}
				result.Success = true
				results = append(results, result)
				continue
			}

			// Case 3: Parent is not main and not merged - check if behind parent
			// This handles the case where parent branch was updated with new commits
			behindBy, err := m.git.GetCommitsBehind(branch.Name, parentRef)
			if err != nil || behindBy == 0 {
				continue // Not behind parent, skip
			}

			result.BehindBy = behindBy
			result.SyncedParent = branch.Parent

			// Simple rebase onto parent
			rebaseResult := g.RebaseNonInteractive(parentRef)
			if rebaseResult.HasConflict {
				result.HasConflict = true
				result.Error = fmt.Errorf("resolve conflicts in: %s", branch.WorktreePath)
				results = append(results, result)
				continue
			} else if rebaseResult.Error != nil {
				result.Error = rebaseResult.Error
				results = append(results, result)
				continue
			}
			result.Success = true
			results = append(results, result)
		}
	}

	// Save updated config
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return results, fmt.Errorf("failed to save config: %w", err)
	}

	return results, nil
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
// Returns results for each child branch processed
func (m *Manager) RebaseChildren() ([]RebaseResult, error) {
	_, currentBranch, err := m.GetCurrentStack()
	if err != nil {
		return nil, err
	}

	var results []RebaseResult
	children := m.GetChildren(currentBranch.Name)

	for _, child := range children {
		if child.IsRemote {
			continue
		}

		result := RebaseResult{Branch: child.Name, WorktreePath: child.WorktreePath}
		g := git.New(child.WorktreePath)

		rebaseResult := g.RebaseNonInteractive(currentBranch.Name)
		if rebaseResult.HasConflict {
			result.HasConflict = true
			result.Error = fmt.Errorf("resolve conflicts in: %s", child.WorktreePath)
			results = append(results, result)
			continue
		} else if rebaseResult.Error != nil {
			result.Error = rebaseResult.Error
			results = append(results, result)
			continue
		}
		result.Success = true
		results = append(results, result)

		// Recursively rebase this child's children
		childMgr, err := NewManager(child.WorktreePath)
		if err != nil {
			continue
		}
		childResults, _ := childMgr.RebaseChildren()
		results = append(results, childResults...)
	}

	return results, nil
}
