package stack

import (
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/github"
)

// syncCache holds the cache during sync operations to track and persist changes
type syncCache struct {
	cache   *config.CacheConfig
	repoDir string
	dirty   bool
	sc      *config.StackConfig // reference to stack config for saving
}

func newSyncCache(stackConfig *config.StackConfig, repoDir string) *syncCache {
	return &syncCache{
		cache:   stackConfig.Cache,
		repoDir: repoDir,
		dirty:   false,
		sc:      stackConfig,
	}
}

func (sc *syncCache) markMerged(branchName string) {
	bc := sc.cache.GetBranchCache(branchName)
	if bc == nil {
		bc = &config.BranchCache{}
	}
	if !bc.IsMerged {
		bc.IsMerged = true
		sc.cache.SetBranchCache(branchName, bc)
		sc.dirty = true
	}
}

func (sc *syncCache) save() error {
	if sc.dirty {
		return sc.sc.Save(sc.repoDir)
	}
	return nil
}

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
	Branch       string
	MergedParent string // Non-empty if parent was merged
	BehindBy     int    // Number of commits behind target
	BehindParent string // Non-empty if behind a non-main parent
	NeedsSync    bool   // True if branch needs to be synced
}

// MergedBranchInfo contains information about a branch whose PR has been merged
type MergedBranchInfo struct {
	Branch       string
	PRNumber     int
	WorktreePath string
	StackName    string
}

// CleanupResult contains information about a branch cleanup operation
type CleanupResult struct {
	Branch              string
	Success             bool
	Error               string
	WorktreeWasDeleted  bool // True if worktree was already deleted before cleanup
	WasCurrentWorktree  bool // True if this was the worktree we were in when cleanup started
}

// AfterRebaseCallback is called after each successful rebase
// It receives the result and the git instance for the worktree
// Returns true if sync should continue, false to stop
type AfterRebaseCallback func(result RebaseResult, g *git.Git) bool

// BeforeRebaseCallback is called before each rebase to ask for confirmation
// It receives the sync info for the branch about to be synced
// Returns true to proceed with rebase, false to skip this branch
type BeforeRebaseCallback func(info SyncInfo) bool

// SyncCallbacks contains optional callbacks for sync operations
type SyncCallbacks struct {
	BeforeRebase BeforeRebaseCallback
	AfterRebase  AfterRebaseCallback
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

// DetectSyncNeeded checks for branches that need syncing in the CURRENT stack only:
// - Branches whose parents have been merged to main
// - Branches whose parent is main but are behind origin/main
func (m *Manager) DetectSyncNeeded(gh *github.Client) ([]SyncInfo, error) {
	return m.detectSyncNeededInternal(gh, true, nil)
}

// DetectSyncNeededAllStacks checks for branches that need syncing across ALL stacks:
// - Branches whose parents have been merged to main
// - Branches whose parent is main but are behind origin/main
func (m *Manager) DetectSyncNeededAllStacks(gh *github.Client) ([]SyncInfo, error) {
	return m.detectSyncNeededInternal(gh, false, nil)
}

// DetectSyncNeededForStacks checks for branches that need syncing in specific stacks
func (m *Manager) DetectSyncNeededForStacks(gh *github.Client, stacks []*config.Stack) ([]SyncInfo, error) {
	return m.detectSyncNeededInternal(gh, false, stacks)
}

// detectSyncNeededInternal is the internal implementation that can work on current stack, all stacks, or specific stacks
func (m *Manager) detectSyncNeededInternal(gh *github.Client, currentStackOnly bool, specificStacks []*config.Stack) ([]SyncInfo, error) {
	// Fetch latest
	if err := m.git.Fetch(); err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}

	baseBranch := m.config.DefaultBaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	var results []SyncInfo

	// Get the stacks to check
	var stacksToCheck []*config.Stack
	if specificStacks != nil {
		stacksToCheck = specificStacks
	} else if currentStackOnly {
		currentStack, _, err := m.GetCurrentStack()
		if err != nil {
			return nil, fmt.Errorf("not in a stack: %w", err)
		}
		stacksToCheck = []*config.Stack{currentStack}
	} else {
		for _, stack := range m.stackConfig.Stacks {
			stacksToCheck = append(stacksToCheck, stack)
		}
	}

	// Check branches in selected stacks
	for _, stack := range stacksToCheck {
		for _, branch := range stack.Branches {
			// Skip remote branches (we can't rebase them anyway)
			if branch.IsRemote {
				continue
			}

			// Skip already-merged branches (they don't need syncing)
			if branch.IsMerged {
				continue
			}

			// Parent is main - check if behind origin/main
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

			// Parent is not main - check if parent was merged
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

			// Parent is not main and not merged - check if behind parent
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

// DetectSyncNeededForBranch checks if a specific branch needs syncing
// Returns SyncInfo if the branch needs syncing, nil otherwise
func (m *Manager) DetectSyncNeededForBranch(branchName string, gh *github.Client) *SyncInfo {
	branch := m.GetBranch(branchName)
	if branch == nil || branch.IsRemote || branch.IsMerged {
		return nil
	}

	baseBranch := m.config.DefaultBaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	// Parent is main - check if behind origin/main
	if m.IsMainBranch(branch.Parent) {
		behindBy, err := m.git.GetCommitsBehind(branch.Name, "origin/"+baseBranch)
		if err == nil && behindBy > 0 {
			return &SyncInfo{
				Branch:    branch.Name,
				BehindBy:  behindBy,
				NeedsSync: true,
			}
		}
		return nil
	}

	// Parent is not main - check if parent was merged
	isMerged := false
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
		return &SyncInfo{
			Branch:       branch.Name,
			MergedParent: branch.Parent,
			NeedsSync:    true,
		}
	}

	// Parent is not main and not merged - check if behind parent
	behindBy, err := m.git.GetCommitsBehind(branch.Name, parentRef)
	if err == nil && behindBy > 0 {
		return &SyncInfo{
			Branch:       branch.Name,
			BehindBy:     behindBy,
			BehindParent: branch.Parent,
			NeedsSync:    true,
		}
	}

	return nil
}

// SyncStack syncs branches in the CURRENT stack only that need syncing
// This handles three cases:
// - Branches whose parent is main but are behind origin/main (simple rebase)
// - Branches whose parent was merged (rebase onto main using --onto)
// - Branches whose parent is not merged but has new commits (rebase onto parent)
// Callbacks can be used to ask for confirmation before each rebase and push after
func (m *Manager) SyncStack(gh *github.Client, callbacks *SyncCallbacks) ([]RebaseResult, error) {
	return m.syncStackInternal(gh, callbacks, true, nil)
}

// SyncStackAll syncs branches in ALL stacks that need syncing
func (m *Manager) SyncStackAll(gh *github.Client, callbacks *SyncCallbacks) ([]RebaseResult, error) {
	return m.syncStackInternal(gh, callbacks, false, nil)
}

// SyncSpecificStacks syncs branches in the given stacks
func (m *Manager) SyncSpecificStacks(stacks []*config.Stack, gh *github.Client, callbacks *SyncCallbacks) ([]RebaseResult, error) {
	return m.syncStackInternal(gh, callbacks, false, stacks)
}

// syncStackInternal is the internal implementation that can work on current stack, all stacks, or specific stacks
func (m *Manager) syncStackInternal(gh *github.Client, callbacks *SyncCallbacks, currentStackOnly bool, specificStacks []*config.Stack) ([]RebaseResult, error) {
	// Fetch latest
	if err := m.git.Fetch(); err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}

	baseBranch := m.config.DefaultBaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	var results []RebaseResult

	// Use combined cache from stack config
	sc := newSyncCache(m.stackConfig, m.repoDir)

	// Get the stacks to sync
	var stacksToSync []*config.Stack
	if specificStacks != nil {
		stacksToSync = specificStacks
	} else if currentStackOnly {
		currentStack, _, err := m.GetCurrentStack()
		if err != nil {
			return nil, fmt.Errorf("not in a stack: %w", err)
		}
		stacksToSync = []*config.Stack{currentStack}
	} else {
		for _, stack := range m.stackConfig.Stacks {
			stacksToSync = append(stacksToSync, stack)
		}
	}

	// Record old HEAD commits for branches in selected stacks BEFORE any rebasing
	// When parent is rebased, we need to know the old parent HEAD to correctly rebase children onto the new parent
	oldHeads := make(map[string]string)
	for _, stack := range stacksToSync {
		for _, branch := range stack.Branches {
			if !branch.IsRemote {
				if commit, err := m.git.GetBranchCommit(branch.Name); err == nil {
					oldHeads[branch.Name] = commit
				}
			}
		}
	}

	allStacks := !currentStackOnly

	// Sync branches in selected stacks
	for _, stack := range stacksToSync {
		stackHasConflict := false // Track if this stack hit a conflict
		for _, branch := range stack.Branches {
			if branch.IsRemote {
				continue
			}

			// Skip already-merged branches (they don't need syncing)
			if branch.IsMerged {
				continue
			}

			// If this stack already hit a conflict and we're syncing all stacks, skip rest of this stack
			if stackHasConflict && allStacks {
				continue
			}

			result := RebaseResult{Branch: branch.Name, WorktreePath: branch.WorktreePath}
			g := git.New(branch.WorktreePath)

			// Parent is main - check if behind origin/main
			if m.IsMainBranch(branch.Parent) {
				behindBy, err := m.git.GetCommitsBehind(branch.Name, "origin/"+baseBranch)
				if err != nil || behindBy == 0 {
					continue // Not behind, skip
				}

				result.BehindBy = behindBy
				result.SyncedParent = "origin/" + baseBranch

				// Call beforeRebase callback to ask for confirmation
				if callbacks != nil && callbacks.BeforeRebase != nil {
					syncInfo := SyncInfo{
						Branch:    branch.Name,
						BehindBy:  behindBy,
						NeedsSync: true,
					}
					if !callbacks.BeforeRebase(syncInfo) {
						continue // User chose to skip this branch
					}
				}

				// Use non-interactive rebase for better conflict detection
				rebaseResult := g.RebaseNonInteractive("origin/" + baseBranch)
				if rebaseResult.HasConflict {
					result.HasConflict = true
					result.Error = fmt.Errorf("resolve conflicts in: %s", branch.WorktreePath)
					results = append(results, result)
					if !allStacks {
						// Stop immediately on conflict when syncing single stack
						return results, nil
					}
					// Mark this stack as having a conflict and continue to next stack
					stackHasConflict = true
					continue
				} else if rebaseResult.Error != nil {
					result.Error = rebaseResult.Error
					results = append(results, result)
					if !allStacks {
						// Stop on error when syncing single stack
						return results, nil
					}
					// Mark this stack as having an error and continue to next stack
					stackHasConflict = true
					continue
				}
				result.Success = true
				results = append(results, result)
				// Call afterRebase callback to allow pushing before continuing to children
				if callbacks != nil && callbacks.AfterRebase != nil {
					if !callbacks.AfterRebase(result, g) {
						if !allStacks {
							return results, nil // Callback requested stop for single stack
						}
						// For all stacks, mark this stack as having an issue and continue to next stack
						stackHasConflict = true
						continue
					}
				}
				continue
			}

			// Parent is not main - check if parent was merged
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
				// Parent was merged - mark it in cache but DON'T change tree structure.
				// The tree order is preserved; walkTree computes effective parents at runtime
				// by skipping merged ancestors.
				oldParent := branch.Parent
				oldParentRef := m.getParentRef(oldParent) // Use origin/<name> for remote parents

				sc.markMerged(oldParent)

				// Repopulate branches so walkTree recalculates effective parents
				// (children of merged branches will now have their Parent field
				// pointing to the nearest non-merged ancestor)
				stack.PopulateBranchesWithCache(sc.cache)

				// Re-fetch the branch since Branches slice was rebuilt
				var updatedBranch *config.Branch
				for _, b := range stack.Branches {
					if b.Name == branch.Name {
						updatedBranch = b
						break
					}
				}
				if updatedBranch == nil {
					continue
				}

				newParent := updatedBranch.Parent // effective parent (nearest non-merged ancestor)
				result.SyncedParent = newParent

				// Call beforeRebase callback to ask for confirmation
				if callbacks != nil && callbacks.BeforeRebase != nil {
					syncInfo := SyncInfo{
						Branch:       branch.Name,
						MergedParent: oldParent,
						NeedsSync:    true,
					}
					if !callbacks.BeforeRebase(syncInfo) {
						continue // User chose to skip this branch
					}
				}

				// Find the merge-base between current branch and old parent
				// This is the point where we originally branched from the parent
				mergeBase, err := m.git.GetMergeBase(branch.Name, oldParentRef)
				if err != nil {
					// Fallback to using oldParentRef if we can't get merge-base
					mergeBase = oldParentRef
				}

				// Rebase --onto the effective parent (e.g., origin/main)
				rebaseTarget := "origin/" + newParent
				if m.IsMainBranch(newParent) {
					rebaseTarget = "origin/" + baseBranch
				}

				rebaseResult := g.RebaseOntoNonInteractive(rebaseTarget, mergeBase)
				if rebaseResult.HasConflict {
					result.HasConflict = true
					result.Error = fmt.Errorf("resolve conflicts in: %s", branch.WorktreePath)
					results = append(results, result)
					sc.save()
					m.stackConfig.Save(m.repoDir)
					if !allStacks {
						// Stop immediately on conflict when syncing single stack
						return results, nil
					}
					// Mark this stack as having a conflict and continue to next stack
					stackHasConflict = true
					continue
				} else if rebaseResult.Error != nil {
					result.Error = rebaseResult.Error
					results = append(results, result)
					sc.save()
					m.stackConfig.Save(m.repoDir)
					if !allStacks {
						// Stop on error when syncing single stack
						return results, nil
					}
					// Mark this stack as having an error and continue to next stack
					stackHasConflict = true
					continue
				}
				result.Success = true
				results = append(results, result)
				// Call afterRebase callback to allow pushing before continuing to children
				if callbacks != nil && callbacks.AfterRebase != nil {
					if !callbacks.AfterRebase(result, g) {
						sc.save()
						if !allStacks {
							return results, nil // Callback requested stop for single stack
						}
						// For all stacks, mark this stack as having an issue and continue to next stack
						stackHasConflict = true
						continue
					}
				}
				continue
			}

			// Parent is not main and not merged - check if behind parent
			// This handles the case where parent branch was updated with new commits
			// (e.g., parent was just rebased onto main in this same sync operation)
			behindBy, err := m.git.GetCommitsBehind(branch.Name, parentRef)
			if err != nil || behindBy == 0 {
				continue // Not behind parent, skip
			}

			result.BehindBy = behindBy
			result.SyncedParent = branch.Parent

			// Call beforeRebase callback to ask for confirmation
			if callbacks != nil && callbacks.BeforeRebase != nil {
				syncInfo := SyncInfo{
					Branch:       branch.Name,
					BehindBy:     behindBy,
					BehindParent: branch.Parent,
					NeedsSync:    true,
				}
				if !callbacks.BeforeRebase(syncInfo) {
					continue // User chose to skip this branch
				}
			}

			// Use the OLD parent HEAD (recorded before any rebasing) as the base for --onto
			// This correctly handles the case where parent was rebased in this sync:
			// - Parent was at oldParentHead, child was based on it
			// - Parent got rebased to newParentHead (current parentRef)
			// - We need: git rebase --onto newParentHead oldParentHead
			// This transplants commits from oldParentHead..childHead onto newParentHead
			oldParentHead, hasOldHead := oldHeads[branch.Parent]
			if hasOldHead {
				// Check if child has any commits of its own (beyond the old parent)
				// If child HEAD == oldParentHead, there are no commits to rebase
				// In this case, just fast-forward the child to the new parent HEAD
				childHead, err := m.git.GetBranchCommit(branch.Name)
				if err == nil && childHead == oldParentHead {
					// No commits in child - just reset to new parent HEAD
					if err := g.ResetHard(parentRef); err != nil {
						result.Error = fmt.Errorf("failed to fast-forward: %w", err)
						results = append(results, result)
						continue
					}
					result.Success = true
					results = append(results, result)
					if callbacks != nil && callbacks.AfterRebase != nil {
						if !callbacks.AfterRebase(result, g) {
							if !allStacks {
								return results, nil // Callback requested stop for single stack
							}
							// For all stacks, mark this stack as having an issue and continue to next stack
							stackHasConflict = true
							continue
						}
					}
					continue
				}

				// Use rebase --onto with the old parent HEAD
				rebaseResult := g.RebaseOntoNonInteractive(parentRef, oldParentHead)
				if rebaseResult.HasConflict {
					result.HasConflict = true
					result.Error = fmt.Errorf("resolve conflicts in: %s", branch.WorktreePath)
					results = append(results, result)
					if !allStacks {
						// Stop immediately on conflict when syncing single stack
						return results, nil
					}
					// Mark this stack as having a conflict and continue to next stack
					stackHasConflict = true
					continue
				} else if rebaseResult.Error != nil {
					result.Error = rebaseResult.Error
					results = append(results, result)
					if !allStacks {
						// Stop on error when syncing single stack
						return results, nil
					}
					// Mark this stack as having an error and continue to next stack
					stackHasConflict = true
					continue
				}
				result.Success = true
				results = append(results, result)
				// Call afterRebase callback to allow pushing before continuing to children
				if callbacks != nil && callbacks.AfterRebase != nil {
					if !callbacks.AfterRebase(result, g) {
						if !allStacks {
							return results, nil // Callback requested stop for single stack
						}
						// For all stacks, mark this stack as having an issue and continue to next stack
						stackHasConflict = true
						continue
					}
				}
				continue
			}

			// Fallback: no old HEAD recorded, try simple rebase
			rebaseResult := g.RebaseNonInteractive(parentRef)
			if rebaseResult.HasConflict {
				result.HasConflict = true
				result.Error = fmt.Errorf("resolve conflicts in: %s", branch.WorktreePath)
				results = append(results, result)
				if !allStacks {
					// Stop immediately on conflict when syncing single stack
					return results, nil
				}
				// Mark this stack as having a conflict and continue to next stack
				stackHasConflict = true
				continue
			} else if rebaseResult.Error != nil {
				result.Error = rebaseResult.Error
				results = append(results, result)
				if !allStacks {
					// Stop on error when syncing single stack
					return results, nil
				}
				// Mark this stack as having an error and continue to next stack
				stackHasConflict = true
				continue
			}
			result.Success = true
			results = append(results, result)
			// Call afterRebase callback to allow pushing before continuing to children
			if callbacks != nil && callbacks.AfterRebase != nil {
				if !callbacks.AfterRebase(result, g) {
					if !allStacks {
						return results, nil // Callback requested stop for single stack
					}
					// For all stacks, mark this stack as having an issue and continue to next stack
					stackHasConflict = true
					continue
				}
			}
		}
	}

	// Save cache (tracks merged branches)
	if err := sc.save(); err != nil {
		return results, fmt.Errorf("failed to save cache: %w", err)
	}

	// Save updated config (tree structure)
	if err := m.stackConfig.Save(m.repoDir); err != nil {
		return results, fmt.Errorf("failed to save config: %w", err)
	}

	return results, nil
}

// SyncBranch syncs a specific branch, handling all cases:
// - Branch is behind origin/main (parent is main)
// - Parent branch was merged (rebase --onto main)
// - Branch is behind its parent (rebase onto parent)
func (m *Manager) SyncBranch(branchName string, gh *github.Client) (*RebaseResult, error) {
	branch := m.GetBranch(branchName)
	if branch == nil {
		return nil, fmt.Errorf("branch '%s' not found", branchName)
	}

	if branch.IsRemote {
		return nil, fmt.Errorf("cannot sync remote branch '%s'", branchName)
	}

	baseBranch := m.config.DefaultBaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	result := &RebaseResult{Branch: branch.Name, WorktreePath: branch.WorktreePath}
	g := git.New(branch.WorktreePath)

	// Parent is main - check if behind origin/main
	if m.IsMainBranch(branch.Parent) {
		behindBy, err := m.git.GetCommitsBehind(branch.Name, "origin/"+baseBranch)
		if err != nil || behindBy == 0 {
			result.Success = true
			return result, nil // Already up to date
		}

		result.BehindBy = behindBy
		result.SyncedParent = "origin/" + baseBranch

		rebaseResult := g.RebaseNonInteractive("origin/" + baseBranch)
		if rebaseResult.HasConflict {
			result.HasConflict = true
			result.Error = fmt.Errorf("resolve conflicts in: %s", branch.WorktreePath)
			return result, nil
		} else if rebaseResult.Error != nil {
			result.Error = rebaseResult.Error
			return result, nil
		}
		result.Success = true
		return result, nil
	}

	// Parent is not main - check if parent was merged
	isMerged := false
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
		// Parent was merged - mark it in cache and update tree structure
		cache := m.stackConfig.Cache
		bc := cache.GetBranchCache(branch.Parent)
		if bc == nil {
			bc = &config.BranchCache{}
		}
		bc.IsMerged = true
		cache.SetBranchCache(branch.Parent, bc)

		oldParent := branch.Parent
		oldParentRef := m.getParentRef(oldParent)
		result.SyncedParent = baseBranch

		// Update tree structure: reparent branch to main (root level)
		// Find the stack containing this branch
		for _, stack := range m.stackConfig.Stacks {
			if stack.HasBranch(branch.Name) {
				stack.ReparentBranch(branch.Name, "") // Empty = root level (under main)
				stack.PopulateBranchesWithCache(cache)
				break
			}
		}

		mergeBase, err := m.git.GetMergeBase(branch.Name, oldParentRef)
		if err != nil {
			mergeBase = oldParentRef
		}

		rebaseResult := g.RebaseOntoNonInteractive("origin/"+baseBranch, mergeBase)
		if rebaseResult.HasConflict {
			result.HasConflict = true
			result.Error = fmt.Errorf("resolve conflicts in: %s", branch.WorktreePath)
			m.stackConfig.Save(m.repoDir)
			return result, nil
		} else if rebaseResult.Error != nil {
			result.Error = rebaseResult.Error
			m.stackConfig.Save(m.repoDir)
			return result, nil
		}
		result.Success = true
		m.stackConfig.Save(m.repoDir)
		return result, nil
	}

	// Parent is not main and not merged - check if behind parent
	// This handles the case where parent branch was force-pushed (rebased)
	behindBy, err := m.git.GetCommitsBehind(branch.Name, parentRef)
	if err != nil || behindBy == 0 {
		result.Success = true
		return result, nil // Already up to date
	}

	result.BehindBy = behindBy
	result.SyncedParent = branch.Parent

	// Count commits in the child branch that are not in the parent
	// git rev-list --count parent..branch
	commitCount, err := m.git.GetCommitCount(parentRef, branch.Name)
	if err != nil {
		result.Error = fmt.Errorf("failed to count commits: %w", err)
		return result, nil
	}

	if commitCount == 0 {
		// No commits in child - just reset to parent (fast-forward)
		if err := g.ResetHard(parentRef); err != nil {
			result.Error = fmt.Errorf("failed to fast-forward: %w", err)
			return result, nil
		}
		result.Success = true
		return result, nil
	}

	// Has commits - rebase normally, let conflicts bubble up
	rebaseResult := g.RebaseNonInteractive(parentRef)
	if rebaseResult.HasConflict {
		result.HasConflict = true
		result.Error = fmt.Errorf("resolve conflicts in: %s", branch.WorktreePath)
		return result, nil
	} else if rebaseResult.Error != nil {
		result.Error = rebaseResult.Error
		return result, nil
	}
	result.Success = true
	return result, nil
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

		// Count commits in the child branch that are not in the parent
		// git rev-list --count parent..child
		commitCount, err := m.git.GetCommitCount(currentBranch.Name, child.Name)
		if err != nil {
			result.Error = fmt.Errorf("failed to count commits: %w", err)
			results = append(results, result)
			continue
		}

		if commitCount == 0 {
			// No commits in child - just reset to parent (fast-forward)
			if err := g.ResetHard(currentBranch.Name); err != nil {
				result.Error = fmt.Errorf("failed to fast-forward: %w", err)
				results = append(results, result)
				continue
			}
			result.Success = true
			results = append(results, result)
		} else {
			// Has commits - rebase normally, let conflicts bubble up
			rebaseResult := g.RebaseNonInteractive(currentBranch.Name)
			if rebaseResult.HasConflict {
				result.HasConflict = true
				result.Error = fmt.Errorf("resolve conflicts in: %s", child.WorktreePath)
				results = append(results, result)
				// Stop immediately on conflict - user must resolve before continuing
				return results, nil
			} else if rebaseResult.Error != nil {
				result.Error = rebaseResult.Error
				results = append(results, result)
				// Stop on error as well
				return results, nil
			}
			result.Success = true
			results = append(results, result)
		}

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

// DetectMergedBranches finds branches in the CURRENT stack whose PRs have been merged to main
// These are candidates for cleanup (deleting local branch and worktree)
func (m *Manager) DetectMergedBranches(gh *github.Client) ([]MergedBranchInfo, error) {
	return m.detectMergedBranchesInternal(gh, true, nil)
}

// DetectMergedBranchesAllStacks finds branches across ALL stacks whose PRs have been merged to main
func (m *Manager) DetectMergedBranchesAllStacks(gh *github.Client) ([]MergedBranchInfo, error) {
	return m.detectMergedBranchesInternal(gh, false, nil)
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

	baseBranch := m.config.DefaultBaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	var results []MergedBranchInfo

	// Get the stacks to check
	stacksToCheck := make(map[string]*config.Stack)
	if specificStacks != nil {
		for _, s := range specificStacks {
			stacksToCheck[s.Name] = s
		}
	} else if currentStackOnly {
		currentStack, _, err := m.GetCurrentStack()
		if err != nil {
			return nil, fmt.Errorf("not in a stack: %w", err)
		}
		stacksToCheck[currentStack.Name] = currentStack
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

			// Skip remote branches (they don't have local worktrees to clean up)
			if branch.IsRemote {
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
						StackName:    stackName,
					})
				}
			}
		}
	}

	return results, nil
}

// FullyMergedStackInfo contains information about a fully merged stack
type FullyMergedStackInfo struct {
	StackName        string
	Stack            *config.Stack
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
			StackName: stack.Name,
			Stack:     stack,
		}

		// Check if any local artifacts (worktrees, git branches) still exist
		for _, branch := range stack.Branches {
			if branch.IsRemote {
				continue
			}
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
