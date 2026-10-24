package stack

import (
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/v5/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v5/internal/github"
)

// Rebasing, pushing, and PR linking are done by gh-stack (internal/ghstack).
// What stays here is the part ezstack owns: finding branches whose PRs were
// merged and cleaning up their worktrees and local branches.

// MergedBranchInfo contains information about a branch whose PR has been merged
type MergedBranchInfo struct {
	Branch       string
	PRNumber     int
	WorktreePath string
	StackHash    string
}

// CleanupResult contains information about a branch cleanup operation
type CleanupResult struct {
	Branch             string
	Success            bool
	Error              string
	WorktreeWasDeleted bool // True if worktree was already deleted before cleanup
	WasCurrentWorktree bool // True if this was the worktree we were in when cleanup started
}

// DetectMergedBranchesForStacks finds branches in specific stacks whose PRs have been merged
func (m *Manager) DetectMergedBranchesForStacks(gh *github.Client, stacks []*config.Stack) ([]MergedBranchInfo, error) {
	return m.detectMergedBranchesInternal(gh, false, stacks)
}

// detectMergedBranchesInternal is the internal implementation that can work on current stack, all stacks, or specific stacks
func (m *Manager) detectMergedBranchesInternal(gh *github.Client, currentStackOnly bool, specificStacks []*config.Stack) ([]MergedBranchInfo, error) {
	if gh == nil {
		return nil, nil
	}

	var results []MergedBranchInfo

	// Get the stacks to check
	stacksToCheck := make(map[string]*config.Stack)
	if specificStacks != nil {
		for _, s := range specificStacks {
			stacksToCheck[s.Hash] = s
		}
	} else if currentStackOnly {
		currentStack, _, err := m.GetCurrentStack()
		if err != nil {
			return nil, fmt.Errorf("not in a stack: %w", err)
		}
		stacksToCheck[currentStack.Hash] = currentStack
	} else {
		for stackName, stack := range m.stackConfig.Stacks {
			stacksToCheck[stackName] = stack
		}
	}

	// Check branches in selected stacks for merged PRs
	for stackName, stack := range stacksToCheck {
		for _, branch := range stack.Branches {
			// Skip branches without PRs
			if branch.PRNumber == 0 {
				continue
			}

			// Check if the PR is merged
			pr, err := gh.GetPR(branch.PRNumber)
			if err != nil {
				continue
			}

			if pr.Merged {
				// Check if there's actually something to clean up locally
				// (worktree exists or git branch exists)
				hasWorktree := false
				if branch.WorktreePath != "" {
					if _, err := os.Stat(branch.WorktreePath); err == nil {
						hasWorktree = true
					}
				}

				hasBranch := m.git.BranchExists(branch.Name)
				if !hasWorktree && !hasBranch {
					// Nothing to clean up locally, skip
					continue
				}

				// If branch is already marked as merged in config, silently clean up
				// any remaining git branch and skip prompting (we already confirmed once)
				if branch.IsMerged {
					if hasBranch {
						_ = m.git.DeleteBranch(branch.Name, true)
					}
					continue
				}

				// Make sure this branch has no unmerged children
				hasUnmergedChildren := false
				for _, child := range m.GetChildren(branch.Name) {
					if child.PRNumber == 0 {
						hasUnmergedChildren = true
						break
					}
					childPR, err := gh.GetPR(child.PRNumber)
					if err != nil || !childPR.Merged {
						hasUnmergedChildren = true
						break
					}
				}

				if !hasUnmergedChildren {
					results = append(results, MergedBranchInfo{
						Branch:       branch.Name,
						PRNumber:     branch.PRNumber,
						WorktreePath: branch.WorktreePath,
						StackHash:    stackName,
					})
				}
			}
		}
	}

	return results, nil
}

// FullyMergedStackInfo contains information about a fully merged stack
type FullyMergedStackInfo struct {
	StackHash         string
	Stack             *config.Stack
	HasLocalArtifacts bool // true if worktrees or git branches still exist locally
}

// DetectFullyMergedStacks finds stacks where every branch is merged
func (m *Manager) DetectFullyMergedStacks(stacks []*config.Stack) []FullyMergedStackInfo {
	var results []FullyMergedStackInfo

	for _, stack := range stacks {
		if !stack.IsFullyMerged(m.stackConfig.Cache) {
			continue
		}

		info := FullyMergedStackInfo{
			StackHash: stack.Hash,
			Stack:     stack,
		}

		// Check if any local artifacts (worktrees, git branches) still exist
		for _, branch := range stack.Branches {
			if branch.WorktreePath != "" {
				if _, err := os.Stat(branch.WorktreePath); err == nil {
					info.HasLocalArtifacts = true
					break
				}
			}
			if m.git.BranchExists(branch.Name) {
				info.HasLocalArtifacts = true
				break
			}
		}

		results = append(results, info)
	}

	return results
}

// CleanupMergedBranches marks branches as merged - deletes worktrees and git branches but keeps metadata in config
// This allows merged PRs to still show up in ezs ls/status with strikethrough styling
// Returns detailed results for each branch cleanup operation
func (m *Manager) CleanupMergedBranches(branches []MergedBranchInfo, currentDir string) []CleanupResult {
	var results []CleanupResult

	for _, info := range branches {
		result := CleanupResult{Branch: info.Branch}

		// If we're currently in this worktree, move to the main worktree first
		if info.WorktreePath == currentDir {
			if err := os.Chdir(m.repoDir); err != nil {
				result.Error = fmt.Sprintf("failed to change to main worktree: %v", err)
				results = append(results, result)
				continue
			}
			result.WasCurrentWorktree = true
		}

		// Check if worktree was already deleted before we try to clean up
		if info.WorktreePath != "" {
			if _, err := os.Stat(info.WorktreePath); os.IsNotExist(err) {
				result.WorktreeWasDeleted = true
			}
		}

		// Mark branch as merged (this handles worktree removal, git branch deletion, and marks in config)
		if err := m.MarkBranchMerged(info.Branch); err != nil {
			result.Error = err.Error()
			results = append(results, result)
			continue
		}

		result.Success = true
		results = append(results, result)
	}

	return results
}
