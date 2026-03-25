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
	StackRoot    string // The root branch of this branch's stack (e.g. "main", "develop")
	NeedsSync    bool   // True if branch needs to be synced
}

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
	Autostash    bool // Stash uncommitted changes before rebase, pop after
}

// getParentRef returns the git ref for a parent branch.
// For branches not in the tree (i.e. stack roots like main or remote bases),
// returns origin/<name> if the remote exists, otherwise the local name.
// For branches in the tree, returns the local branch name.
func (m *Manager) getParentRef(parentName string) string {
	parentBranch := m.GetBranch(parentName)
	if parentBranch == nil {
		// Parent is a root or external branch — prefer origin ref
		if m.git.RemoteBranchExists(parentName) {
			return "origin/" + parentName
		}
		return parentName
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
	if err := m.git.Fetch(); err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}

	var results []SyncInfo

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

	for _, stack := range stacksToCheck {
		for _, branch := range stack.Branches {
			if branch.IsMerged {
				continue
			}

			if branch.Parent == stack.Root {
				behindBy, err := m.git.GetCommitsBehind(branch.Name, "origin/"+stack.Root)
				if err == nil && behindBy > 0 {
					results = append(results, SyncInfo{
						Branch:    branch.Name,
						BehindBy:  behindBy,
						StackRoot: stack.Root,
						NeedsSync: true,
					})
				}
				continue
			}

			isMerged := false
			parentRef := m.getParentRef(branch.Parent)

			merged, err := m.git.IsBranchMerged(parentRef, "origin/"+stack.Root)
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
				results = append(results, SyncInfo{
					Branch:       branch.Name,
					MergedParent: branch.Parent,
					StackRoot:    stack.Root,
					NeedsSync:    true,
				})
				continue
			}

			behindBy, err := m.git.GetCommitsBehind(branch.Name, parentRef)
			if err == nil && behindBy > 0 {
				results = append(results, SyncInfo{
					Branch:       branch.Name,
					BehindBy:     behindBy,
					BehindParent: branch.Parent,
					StackRoot:    stack.Root,
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
	if branch == nil || branch.IsMerged {
		return nil
	}

	stack := m.GetStackForBranch(branchName)
	if stack == nil {
		return nil
	}

	if branch.Parent == stack.Root {
		behindBy, err := m.git.GetCommitsBehind(branch.Name, "origin/"+stack.Root)
		if err == nil && behindBy > 0 {
			return &SyncInfo{
				Branch:    branch.Name,
				BehindBy:  behindBy,
				StackRoot: stack.Root,
				NeedsSync: true,
			}
		}
		return nil
	}

	isMerged := false
	parentRef := m.getParentRef(branch.Parent)

	merged, err := m.git.IsBranchMerged(parentRef, "origin/"+stack.Root)
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
			StackRoot:    stack.Root,
			NeedsSync:    true,
		}
	}

	behindBy, err := m.git.GetCommitsBehind(branch.Name, parentRef)
	if err == nil && behindBy > 0 {
		return &SyncInfo{
			Branch:       branch.Name,
			BehindBy:     behindBy,
			BehindParent: branch.Parent,
			StackRoot:    stack.Root,
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
	if err := m.git.Fetch(); err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}

	var results []RebaseResult

	// saveState persists cache and config; logs warnings on failure.
	saveState := func(sc *syncCache) {
		if err := sc.save(); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: failed to save cache: %v\n", err)
		}
		if err := m.stackConfig.Save(m.repoDir); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: failed to save config: %v\n", err)
		}
	}

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
			if commit, err := m.git.GetBranchCommit(branch.Name); err == nil {
				oldHeads[branch.Name] = commit
			}
		}
	}

	allStacks := !currentStackOnly

	// Sync branches in selected stacks
	for _, stack := range stacksToSync {
		stackHasConflict := false // Track if this stack hit a conflict
		for _, branch := range stack.Branches {
			// Skip already-merged branches (they don't need syncing)
			if branch.IsMerged {
				continue
			}

			// If this stack already hit a conflict and we're syncing all stacks, skip rest of this stack
			if stackHasConflict && allStacks {
				continue
			}

			// Skip branches without a worktree path (can't rebase without a working directory)
			if branch.WorktreePath == "" {
				if callbacks != nil && callbacks.BeforeRebase != nil {
					fmt.Fprintf(os.Stderr, "  Skipping %s: no worktree path (use 'ezs goto %s' to set up)\n", branch.Name, branch.Name)
				}
				continue
			}

			result := RebaseResult{Branch: branch.Name, WorktreePath: branch.WorktreePath}
			g := git.New(branch.WorktreePath)

			// Autostash: stash uncommitted changes before rebase
			didStash := false
			if callbacks != nil && callbacks.Autostash {
				if hasChanges, _ := g.HasChanges(); hasChanges {
					if err := g.StashPush(); err == nil {
						didStash = true
					}
				}
			}
			// popStash restores stashed changes after rebase completes.
			// Not called on conflict — user resolves first, then runs 'git stash pop'.
			popStash := func() {
				if didStash {
					if err := g.StashPop(); err != nil {
						fmt.Fprintf(os.Stderr, "  Warning: failed to pop stash for %s: %v\n", branch.Name, err)
					}
				}
			}
			// conflictMsg appends stash info to the conflict error when applicable
			conflictMsg := func() string {
				msg := fmt.Sprintf("resolve conflicts in: %s", branch.WorktreePath)
				if didStash {
					msg += " (uncommitted changes stashed — run 'git stash pop' after resolving)"
				}
				return msg
			}

			if branch.Parent == stack.Root {
				behindBy, err := m.git.GetCommitsBehind(branch.Name, "origin/"+stack.Root)
				if err != nil || behindBy == 0 {
					popStash()
					continue
				}

				result.BehindBy = behindBy
				result.SyncedParent = "origin/" + stack.Root

				if callbacks != nil && callbacks.BeforeRebase != nil {
					syncInfo := SyncInfo{
						Branch:    branch.Name,
						BehindBy:  behindBy,
						StackRoot: stack.Root,
						NeedsSync: true,
					}
					if !callbacks.BeforeRebase(syncInfo) {
						popStash()
						continue
					}
				}

				rebaseResult := g.RebaseNonInteractive("origin/" + stack.Root)
				if rebaseResult.HasConflict {
					result.HasConflict = true
					result.Error = fmt.Errorf("%s", conflictMsg())
					results = append(results, result)
					if !allStacks {
						return results, nil
					}
					stackHasConflict = true
					continue
				} else if rebaseResult.Error != nil {
					popStash()
					result.Error = rebaseResult.Error
					results = append(results, result)
					if !allStacks {
						return results, nil
					}
					stackHasConflict = true
					continue
				}
				popStash()
				result.Success = true
				results = append(results, result)
				if callbacks != nil && callbacks.AfterRebase != nil {
					if !callbacks.AfterRebase(result, g) {
						if !allStacks {
							return results, nil
						}
						stackHasConflict = true
						continue
					}
				}
				continue
			}

			isMerged := false
			parentRef := m.getParentRef(branch.Parent)

			merged, err := m.git.IsBranchMerged(parentRef, "origin/"+stack.Root)
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
					popStash()
					continue
				}

				newParent := updatedBranch.Parent // effective parent (nearest non-merged ancestor)
				result.SyncedParent = newParent

				// Call beforeRebase callback to ask for confirmation
				if callbacks != nil && callbacks.BeforeRebase != nil {
					syncInfo := SyncInfo{
						Branch:       branch.Name,
						MergedParent: oldParent,
						StackRoot:    stack.Root,
						NeedsSync:    true,
					}
					if !callbacks.BeforeRebase(syncInfo) {
						popStash()
						continue
					}
				}

				// Find the merge-base between current branch and old parent
				mergeBase, err := m.git.GetMergeBase(branch.Name, oldParentRef)
				if err != nil {
					mergeBase = oldParentRef
				}

				rebaseTarget := m.getParentRef(newParent)
				if newParent == stack.Root {
					rebaseTarget = "origin/" + stack.Root
				}

				rebaseResult := g.RebaseOntoNonInteractive(rebaseTarget, mergeBase)
				if rebaseResult.HasConflict {
					result.HasConflict = true
					result.Error = fmt.Errorf("%s", conflictMsg())
					results = append(results, result)
					saveState(sc)
					if !allStacks {
						return results, nil
					}
					stackHasConflict = true
					continue
				} else if rebaseResult.Error != nil {
					popStash()
					result.Error = rebaseResult.Error
					results = append(results, result)
					saveState(sc)
					if !allStacks {
						return results, nil
					}
					stackHasConflict = true
					continue
				}
				popStash()
				result.Success = true
				results = append(results, result)
				if callbacks != nil && callbacks.AfterRebase != nil {
					if !callbacks.AfterRebase(result, g) {
						saveState(sc)
						if !allStacks {
							return results, nil
						}
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
				popStash()
				continue
			}

			result.BehindBy = behindBy
			result.SyncedParent = branch.Parent

			if callbacks != nil && callbacks.BeforeRebase != nil {
				syncInfo := SyncInfo{
					Branch:       branch.Name,
					BehindBy:     behindBy,
					BehindParent: branch.Parent,
					StackRoot:    stack.Root,
					NeedsSync:    true,
				}
				if !callbacks.BeforeRebase(syncInfo) {
					popStash()
					continue
				}
			}

			// Use the OLD parent HEAD (recorded before any rebasing) as the base for --onto
			oldParentHead, hasOldHead := oldHeads[branch.Parent]
			if hasOldHead {
				childHead, err := m.git.GetBranchCommit(branch.Name)
				if err == nil && childHead == oldParentHead {
					// No commits in child - just reset to new parent HEAD
					if err := g.ResetHard(parentRef); err != nil {
						popStash()
						result.Error = fmt.Errorf("failed to fast-forward: %w", err)
						results = append(results, result)
						continue
					}
					popStash()
					result.Success = true
					results = append(results, result)
					if callbacks != nil && callbacks.AfterRebase != nil {
						if !callbacks.AfterRebase(result, g) {
							if !allStacks {
								return results, nil
							}
							stackHasConflict = true
							continue
						}
					}
					continue
				}

				rebaseResult := g.RebaseOntoNonInteractive(parentRef, oldParentHead)
				if rebaseResult.HasConflict {
					result.HasConflict = true
					result.Error = fmt.Errorf("%s", conflictMsg())
					results = append(results, result)
					if !allStacks {
						return results, nil
					}
					stackHasConflict = true
					continue
				} else if rebaseResult.Error != nil {
					popStash()
					result.Error = rebaseResult.Error
					results = append(results, result)
					if !allStacks {
						return results, nil
					}
					stackHasConflict = true
					continue
				}
				popStash()
				result.Success = true
				results = append(results, result)
				if callbacks != nil && callbacks.AfterRebase != nil {
					if !callbacks.AfterRebase(result, g) {
						if !allStacks {
							return results, nil
						}
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
				result.Error = fmt.Errorf("%s", conflictMsg())
				results = append(results, result)
				if !allStacks {
					return results, nil
				}
				stackHasConflict = true
				continue
			} else if rebaseResult.Error != nil {
				popStash()
				result.Error = rebaseResult.Error
				results = append(results, result)
				if !allStacks {
					return results, nil
				}
				stackHasConflict = true
				continue
			}
			popStash()
			result.Success = true
			results = append(results, result)
			if callbacks != nil && callbacks.AfterRebase != nil {
				if !callbacks.AfterRebase(result, g) {
					if !allStacks {
						return results, nil
					}
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

	stack := m.GetStackForBranch(branchName)
	if stack == nil {
		return nil, fmt.Errorf("branch '%s' not found in any stack", branchName)
	}

	result := &RebaseResult{Branch: branch.Name, WorktreePath: branch.WorktreePath}
	g := git.New(branch.WorktreePath)

	if branch.Parent == stack.Root {
		behindBy, err := m.git.GetCommitsBehind(branch.Name, "origin/"+stack.Root)
		if err != nil || behindBy == 0 {
			result.Success = true
			return result, nil
		}

		result.BehindBy = behindBy
		result.SyncedParent = "origin/" + stack.Root

		rebaseResult := g.RebaseNonInteractive("origin/" + stack.Root)
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

	isMerged := false
	parentRef := m.getParentRef(branch.Parent)

	merged, err := m.git.IsBranchMerged(parentRef, "origin/"+stack.Root)
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
		// Mark the parent as merged in cache (same approach as bulk sync).
		// Don't modify the tree structure — walkTree computes effective parents
		// at runtime by skipping merged ancestors.
		cache := m.stackConfig.Cache
		oldParent := branch.Parent
		oldParentRef := m.getParentRef(oldParent)
		result.SyncedParent = stack.Root

		bc := cache.GetBranchCache(oldParent)
		if bc == nil {
			bc = &config.BranchCache{}
		}
		bc.IsMerged = true
		cache.SetBranchCache(oldParent, bc)

		// Repopulate so effective parents are recalculated
		for _, s := range m.stackConfig.Stacks {
			if s.HasBranch(branch.Name) {
				s.PopulateBranchesWithCache(cache)
				break
			}
		}

		mergeBase, err := m.git.GetMergeBase(branch.Name, oldParentRef)
		if err != nil {
			mergeBase = oldParentRef
		}

		rebaseResult := g.RebaseOntoNonInteractive("origin/"+stack.Root, mergeBase)
		if rebaseResult.HasConflict {
			result.HasConflict = true
			result.Error = fmt.Errorf("resolve conflicts in: %s", branch.WorktreePath)
		} else if rebaseResult.Error != nil {
			result.Error = rebaseResult.Error
		} else {
			result.Success = true
		}
		if err := m.stackConfig.Save(m.repoDir); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: failed to save config: %v\n", err)
		}
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

	// If parent is the stack root (not in tree), use origin/<parent>
	parentRef := currentBranch.Parent
	if currentBranch.Parent == currentStack.Root {
		parentRef = "origin/" + currentBranch.Parent
	}

	fmt.Fprintf(os.Stderr, "Rebasing %s onto %s\n", currentBranch.Name, parentRef)
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
		childResults, err := childMgr.RebaseChildren()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: failed to rebase children of %s: %v\n", child.Name, err)
		}
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
