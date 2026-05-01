package stack

import (
	"fmt"
	"os"
	"time"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/github"
)

// resolveBranchWorktree returns the effective worktree path for a branch,
// healing drift between ezstack config and git's actual worktree list.
//
// If branch.WorktreePath is set in config, it's returned as-is. If it's empty,
// we consult `git worktree list`: if git has a dedicated worktree for the
// branch, we return that path and back-fill config. This prevents sync from
// falling into syncViaCheckout when a worktree actually exists (which would
// either collide with "branch is already used by worktree at …" or, on
// success, restore main's HEAD in the main repo and subsequently push main).
//
// Returns empty string if no worktree exists for the branch (legitimate
// checkout-based sync path).
func (m *Manager) resolveBranchWorktree(branch *config.Branch) string {
	if branch == nil {
		return ""
	}
	if branch.WorktreePath != "" {
		return branch.WorktreePath
	}
	worktrees, err := m.git.ListWorktrees()
	if err != nil {
		return ""
	}
	mainRepo := m.repoDir
	for _, wt := range worktrees {
		if wt.Branch != branch.Name {
			continue
		}
		if wt.Path == mainRepo {
			// Branch is checked out in the main repo itself — not a
			// dedicated worktree; checkout-based sync is still appropriate.
			return ""
		}
		// Heal config so future sync calls and persisted state reflect reality.
		branch.WorktreePath = wt.Path
		return wt.Path
	}
	return ""
}

// syncViaCheckout performs a rebase or merge for a branch that has no worktree.
// It checks out the branch in the main repo, performs the operation, then checks
// out the original branch. Returns a git.RebaseResult.
// The caller must ensure no uncommitted changes exist in the main repo.
func syncViaCheckout(mainGit *git.Git, branchName string, doOp func(g *git.Git) git.RebaseResult) git.RebaseResult {
	// Save current branch so we can return to it
	origBranch, err := mainGit.CurrentBranch()
	if err != nil {
		return git.RebaseResult{Error: fmt.Errorf("failed to get current branch: %w", err)}
	}

	// Checkout the target branch
	if err := mainGit.CheckoutBranch(branchName); err != nil {
		return git.RebaseResult{Error: fmt.Errorf("failed to checkout %s: %w", branchName, err)}
	}

	// Perform the rebase/merge operation
	result := doOp(mainGit)

	// If there was a conflict, stay on the branch so the user can resolve it
	if result.HasConflict {
		return result
	}

	// Return to the original branch
	if err := mainGit.CheckoutBranch(origBranch); err != nil {
		// Non-fatal: the sync succeeded but we couldn't switch back
		fmt.Fprintf(os.Stderr, "  Warning: synced %s but failed to switch back to %s: %v\n", branchName, origBranch, err)
	}

	return result
}

// resetViaCheckout performs a git reset --hard for a branch that has no worktree.
// It checks out the branch in the main repo, resets, then checks out back.
func resetViaCheckout(mainGit *git.Git, branchName, ref string) error {
	origBranch, err := mainGit.CurrentBranch()
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}

	if err := mainGit.CheckoutBranch(branchName); err != nil {
		return fmt.Errorf("failed to checkout %s: %w", branchName, err)
	}

	if err := mainGit.ResetHard(ref); err != nil {
		// Try to go back even on failure
		_ = mainGit.CheckoutBranch(origBranch)
		return err
	}

	if err := mainGit.CheckoutBranch(origBranch); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: reset %s but failed to switch back to %s: %v\n", branchName, origBranch, err)
	}

	return nil
}

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

// snapshotPreSyncSHAs records each branch's current HEAD into BranchCache.PreSyncCommit
// so subsequent rebases of children can use `--onto newParent oldParentSHA` instead of
// plain `git rebase newParent`. This persists across process boundaries so
// `ezs sync --continue` (a separate invocation) can use the snapshots when
// re-syncing children of a freshly-rebased parent.
//
// Overwrite policy:
//   - If HEAD already equals the recorded snapshot, no-op (already accurate).
//   - If a prior snapshot exists AND that snapshot is still an ancestor of
//     at least one descendant's tip, PRESERVE the older snapshot. Reason:
//     descendants haven't been re-synced yet and still need the original
//     SHA as their --onto base. Overwriting with the post-rebase HEAD would
//     point future descendant rebases at a SHA that isn't on their history,
//     making `--onto newHead oldSnapshot` walk a wrong commit range.
//   - Otherwise (no prior snapshot, or prior snapshot no longer needed by
//     any descendant), overwrite with the new pre-rewrite SHA.
//
// Also stamps PreSyncCommitAt with the current Unix time so
// cleanupStalePreSyncSHAs can age out abandoned snapshots regardless of
// worktree availability.
func (m *Manager) snapshotPreSyncSHAs(branchNames []string) {
	cache := m.stackConfig.Cache
	dirty := false
	now := time.Now().Unix()
	for _, name := range branchNames {
		head, err := m.git.GetBranchCommit(name)
		if err != nil || head == "" {
			continue
		}
		bc := cache.GetBranchCache(name)
		existing := ""
		if bc != nil {
			existing = bc.PreSyncCommit
		}
		if existing == head {
			// Already snapshotted at this exact SHA — nothing to do.
			continue
		}

		// Preserve an existing snapshot when it's still load-bearing for a
		// descendant. This is the "rebased twice without children being
		// re-synced" case: the older snapshot is exactly the SHA the
		// descendant's tip sits on top of and is still the correct --onto
		// base. RefExists guards against a snapshot that's been GC'd; we
		// don't preserve a SHA git can no longer resolve.
		if existing != "" && m.git.RefExists(existing) && m.snapshotStillNeededByDescendant(name, existing) {
			debugLog("snapshot-preserve-for-descendant", "branch", name, "existing", existing, "head", head)
			continue
		}

		if bc == nil {
			bc = &config.BranchCache{}
		}
		bc.PreSyncCommit = head
		bc.PreSyncCommitAt = now
		cache.SetBranchCache(name, bc)
		dirty = true
	}
	if dirty {
		if err := m.stackConfig.Save(m.repoDir); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: failed to persist pre-sync snapshots: %v\n", err)
		}
	}
}

// snapshotStillNeededByDescendant reports whether `sha` is reachable from at
// least one transitive descendant of `parentName`. When true, the descendant's
// tip is built on top of `sha` and a future `--onto newParent sha` rebase of
// that descendant will replay only the descendant's own commits — exactly
// what the snapshot exists for. When false, no descendant is anchored at
// `sha`, so overwriting it loses no useful information.
func (m *Manager) snapshotStillNeededByDescendant(parentName, sha string) bool {
	for _, d := range m.GetDescendants(parentName) {
		isAnc, err := m.git.IsAncestor(sha, d.Name)
		if err == nil && isAnc {
			return true
		}
	}
	return false
}

// ClearPreSyncCommits is the exported wrapper around clearPreSyncSHAs for
// callers in the commands package (specifically `syncContinue` after a fully
// successful resolution of an in-progress sync). The unexported variant is
// used internally by syncStackInternal at end-of-run cleanup.
func (m *Manager) ClearPreSyncCommits(branchNames []string) {
	m.clearPreSyncSHAs(branchNames)
}

// clearPreSyncSHAs removes PreSyncCommit on the given branches. Called when a
// sync run completes cleanly so the next run starts fresh, and on `--continue`
// completion to release snapshots that are no longer needed. Also resets
// PreSyncCommitAt so age-based cleanup doesn't misfire on a future zero-SHA
// + non-zero-timestamp edge state.
func (m *Manager) clearPreSyncSHAs(branchNames []string) {
	cache := m.stackConfig.Cache
	dirty := false
	for _, name := range branchNames {
		bc := cache.GetBranchCache(name)
		if bc == nil || (bc.PreSyncCommit == "" && bc.PreSyncCommitAt == 0) {
			continue
		}
		bc.PreSyncCommit = ""
		bc.PreSyncCommitAt = 0
		dirty = true
	}
	if dirty {
		if err := m.stackConfig.Save(m.repoDir); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: failed to clear pre-sync snapshots: %v\n", err)
		}
	}
}

// preSyncSnapshotMaxAge bounds how long a PreSyncCommit can persist before
// stale-cleanup ages it out. Set to 14 days because:
//
//   - The intended lifetime is one sync run plus an optional `--continue`
//     pass, both of which complete in seconds-to-minutes.
//   - 14 days is far longer than any plausible "I'll come back to this
//     conflict next week" workflow, so we won't drop snapshots a user is
//     still relying on.
//   - Bounded staleness is the only protection for snapshots whose owning
//     branch has no worktree (cleanupStalePreSyncSHAs can't introspect
//     rebase state without a worktree, so the per-run heuristic doesn't
//     apply to them).
const preSyncSnapshotMaxAge = 14 * 24 * time.Hour

// cleanupStalePreSyncSHAs prunes snapshots that no longer carry useful
// information. A snapshot is stale when any of:
//
//  1. (Worktree case) The branch's current HEAD equals the snapshot AND
//     no rebase/merge is in progress in its worktree — the prior sync was
//     interrupted before any rewrite happened, so the snapshot is just
//     noise.
//  2. (Age fallback) PreSyncCommitAt is older than preSyncSnapshotMaxAge.
//     This catches checkout-based branches (no worktree to introspect) and
//     long-abandoned snapshots from crashed runs that never came back.
//
// In-progress rebases/merges are always preserved regardless of age — the
// snapshot is in active use.
func (m *Manager) cleanupStalePreSyncSHAs() {
	cache := m.stackConfig.Cache
	if cache == nil || cache.Branches == nil {
		return
	}
	dirty := false
	now := time.Now()
	for name, bc := range cache.Branches {
		if bc == nil || bc.PreSyncCommit == "" {
			continue
		}

		// Determine the worktree git instance for this branch, if any. A
		// nil g means we can't introspect rebase/merge state cheaply —
		// we'll fall through to age-based cleanup only.
		var g *git.Git
		if bc.WorktreePath != "" {
			if _, err := os.Stat(bc.WorktreePath); err == nil {
				g = git.New(bc.WorktreePath)
			}
		}

		// Always preserve a snapshot whose worktree is mid-rebase/merge —
		// it's actively being used.
		if g != nil {
			rebaseIP, _ := g.IsRebaseInProgress()
			mergeIP, _ := g.IsMergeInProgress()
			if rebaseIP || mergeIP {
				continue
			}
		}

		// Heuristic 1: worktree case — HEAD equals snapshot means the
		// branch was never actually rewritten, so the snapshot is noise.
		if g != nil {
			head, err := m.git.GetBranchCommit(name)
			if err == nil && head != "" && head == bc.PreSyncCommit {
				bc.PreSyncCommit = ""
				bc.PreSyncCommitAt = 0
				dirty = true
				continue
			}
		}

		// Heuristic 2: age fallback — applies regardless of worktree
		// availability. PreSyncCommitAt of 0 means "no timestamp recorded"
		// (older config that predates this field); treat as ancient and
		// clear so we don't carry uninspectable snapshots forever.
		if bc.PreSyncCommitAt == 0 || now.Sub(time.Unix(bc.PreSyncCommitAt, 0)) > preSyncSnapshotMaxAge {
			debugLog("snapshot-cleanup-age", "branch", name, "sha", bc.PreSyncCommit, "age_days", fmt.Sprintf("%.1f", now.Sub(time.Unix(bc.PreSyncCommitAt, 0)).Hours()/24))
			bc.PreSyncCommit = ""
			bc.PreSyncCommitAt = 0
			dirty = true
		}
	}
	if dirty {
		if err := m.stackConfig.Save(m.repoDir); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: failed to cleanup stale pre-sync snapshots: %v\n", err)
		}
	}
}

// integrateRemoteForBranch fast-forwards a local branch to its remote tip
// when the remote (e.g. origin/<branch>) has commits the local doesn't AND
// the local has no diverging commits. This is what makes `ezs sync` pick up
// a collaborator's pushes to a branch you also work on:
//
//	You:   parent + a1 (pushed)
//	Them:  parent + a1 + a2 (pushed on top)
//	You:   ezs sync → fast-forwards to parent + a1 + a2, then rebases onto
//	       any new parent state.
//
// Behaviour by case:
//   - No remote tracking ref (origin/<branch> doesn't exist, e.g. branch
//     never pushed) — no-op, returns Success.
//   - Already in sync (behind == 0) — no-op, returns Success.
//   - Strict fast-forward (behind > 0, ahead == 0) → `git merge --ff-only`.
//   - True divergence (both ahead and behind > 0) — skip with a warn line.
//     Auto-pulling is dangerous: when local was just ezstack-rebased but
//     not yet force-pushed, replaying local on top of the stale remote
//     would re-introduce the pre-rebase parent commits and produce a
//     duplicate history. Users with unpushed local commits should
//     `git pull --rebase` themselves.
//
// Runs in the branch's worktree when one exists; otherwise falls through to
// `syncViaCheckout` so checkout-based branches work the same way. The
// fast-forward never creates a merge commit, so it's safe under both rebase
// and merge sync strategies.
func (m *Manager) integrateRemoteForBranch(branch *config.Branch) git.RebaseResult {
	if !branch.CanPush() {
		// _nopush branches have explicitly opted out of remote operations.
		debugLog("integrate-skip-nopush", "branch", branch.Name)
		return git.RebaseResult{Success: true}
	}
	remote := branch.EffectiveRemote()
	// Make sure this remote has been fetched so its tracking refs are
	// up-to-date. Manager.FetchRemote dedupes per remote across the Manager
	// lifetime — origin's fetch is shared with the bulk Fetch() at sync
	// start. Failures here are non-fatal — an absent tracking ref just means
	// we skip the FF below — but a stale tracking ref is still a real risk:
	// the rebase phase will proceed against possibly-outdated origin info,
	// and a subsequent force-push could overwrite teammate commits that
	// landed on origin/<branch> while our fetch was failing. Surface the
	// failure prominently so the user knows their local view of the remote
	// may be stale before they push.
	if err := m.FetchRemote(remote); err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ Warning: failed to fetch %s for %s: %v\n", remote, branch.Name, err)
		fmt.Fprintf(os.Stderr, "  origin/%s may be stale. Skipping remote pull; if you force-push after this sync without re-fetching you may overwrite teammate commits. Re-run `ezs sync` once your network is healthy.\n", branch.Name)
		return git.RebaseResult{Success: true}
	}
	remoteRef := remote + "/" + branch.Name
	// Generalised remote-ref existence check (RemoteBranchExists is hardcoded
	// to "origin/" so it can't speak about fork remotes).
	if !m.git.RefExists(remoteRef) {
		return git.RebaseResult{Success: true}
	}
	ahead, behind, err := m.git.GetAheadBehind(branch.Name, remoteRef)
	if err != nil || behind == 0 {
		// Treat lookup failures as "nothing to do" — the rebase phase will
		// surface any real branch-state issues.
		debugLog("integrate-skip-uptodate", "branch", branch.Name, "ref", remoteRef)
		return git.RebaseResult{Success: true}
	}
	if ahead > 0 {
		debugLog("integrate-skip-diverged", "branch", branch.Name, "ahead", fmt.Sprintf("%d", ahead), "behind", fmt.Sprintf("%d", behind))
		fmt.Fprintf(os.Stderr, "  Note: %s diverged from %s (%d ahead, %d behind). Skipping remote pull; run `git pull --rebase` in the worktree if you want to integrate.\n",
			branch.Name, remoteRef, ahead, behind)
		return git.RebaseResult{Success: true}
	}
	// Strict fast-forward case.
	workDir := m.resolveBranchWorktree(branch)

	// Refuse to fast-forward over a dirty worktree. `git merge --ff-only`
	// itself rejects FFs that would overwrite uncommitted changes ("error:
	// Your local changes ... would be overwritten by merge"), so we'd fail
	// either way; bailing out here keeps the failure scoped to the
	// integrate step (with a clear, actionable message) instead of
	// surfacing as a generic FF failure. The subsequent rebase phase has
	// its own dirty-tree handling — autostash protection in callers that
	// opted in, or git's own complaint elsewhere.
	//
	// Only checked when a dedicated worktree exists. Checkout-based sync
	// already requires the main repo to be clean (an existing constraint
	// of syncViaCheckout), so the same dirty case there manifests at the
	// checkout step rather than here.
	if workDir != "" {
		if hasChanges, _ := git.New(workDir).HasChanges(); hasChanges {
			debugLog("integrate-skip-dirty", "branch", branch.Name, "ref", remoteRef)
			fmt.Fprintf(os.Stderr, "  Note: %s has uncommitted changes; skipping remote pull from %s. Stash or commit your changes and re-run sync to integrate them.\n",
				branch.Name, remoteRef)
			return git.RebaseResult{Success: true}
		}
	}

	debugLog("integrate-fastforward", "branch", branch.Name, "ref", remoteRef, "behind", fmt.Sprintf("%d", behind))
	doFF := func(g *git.Git) git.RebaseResult {
		return g.FastForwardMerge(remoteRef)
	}
	if workDir == "" {
		return syncViaCheckout(m.git, branch.Name, doFF)
	}
	return doFF(git.New(workDir))
}

// lookupPreSyncSHA returns a SHA suitable for use as `oldBase` in
// `git rebase --onto newBase oldBase` when rebasing `descendantName` onto
// (a possibly-rewritten) `parentName`.
//
// Resolution order:
//
//  1. The persisted PreSyncCommit on parentName, if set, still resolves to
//     a real git object, AND — when descendantName is non-empty — is still
//     an ancestor of descendantName's tip. The ancestor check rejects the
//     "parent rebased twice without descendants being touched" case, where
//     the snapshot was overwritten to a SHA that's no longer on the
//     descendant's history; using such a SHA as `--onto`'s `oldBase` would
//     compute a wrong commit range. (The overwrite-prevention in
//     snapshotPreSyncSHAs is the primary line of defence; this is
//     defence-in-depth for cross-process and partial-state scenarios.)
//
//  2. The parent's current HEAD as a fallback. Makes `--onto X X`
//     equivalent to plain `git rebase X` — safe when no rewrite has
//     happened in any tracked session.
//
// Returns empty string only when the parent itself can't be resolved (e.g.
// it was deleted), letting the caller degrade to plain rebase.
//
// Pass an empty descendantName to skip the ancestor check (used by callers
// that don't have a specific descendant in scope, e.g. dry-run inspection).
func (m *Manager) lookupPreSyncSHA(parentName, descendantName string) string {
	if bc := m.stackConfig.Cache.GetBranchCache(parentName); bc != nil && bc.PreSyncCommit != "" {
		if !m.git.RefExists(bc.PreSyncCommit) {
			// Snapshot SHA is gone (GC, or manual repo surgery). Drop it
			// so it doesn't get used in subsequent ops.
			debugLog("lookup-snapshot-stale-gone", "parent", parentName, "sha", bc.PreSyncCommit)
			bc.PreSyncCommit = ""
			bc.PreSyncCommitAt = 0
			_ = m.stackConfig.Save(m.repoDir)
		} else if descendantName != "" {
			// Validate the snapshot is still on the descendant's history.
			isAnc, err := m.git.IsAncestor(bc.PreSyncCommit, descendantName)
			if err == nil && isAnc {
				debugLog("lookup-snapshot-hit", "parent", parentName, "descendant", descendantName, "sha", bc.PreSyncCommit)
				return bc.PreSyncCommit
			}
			debugLog("lookup-snapshot-not-ancestor", "parent", parentName, "descendant", descendantName, "sha", bc.PreSyncCommit)
			// Don't auto-clear here — the snapshot may still be valid for
			// other descendants. Just fall through to the live-HEAD path
			// for this caller.
		} else {
			debugLog("lookup-snapshot-hit-no-descendant", "parent", parentName, "sha", bc.PreSyncCommit)
			return bc.PreSyncCommit
		}
	}
	head, err := m.git.GetBranchCommit(parentName)
	if err != nil || head == "" {
		debugLog("lookup-snapshot-empty", "parent", parentName)
		return ""
	}
	debugLog("lookup-snapshot-fallback-livehead", "parent", parentName, "sha", head)
	return head
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
	StackName    string // Display name of the stack this branch belongs to
	Remote       string // Git remote to push to (empty means "origin")
}

// SyncInfo contains information about a branch that needs syncing
type SyncInfo struct {
	Branch       string
	MergedParent string // Non-empty if parent was merged
	BehindBy     int    // Number of commits behind target
	BehindParent string // Non-empty if behind a non-main parent
	StackRoot    string // The root branch of this branch's stack (e.g. "main", "develop")
	NeedsSync    bool   // True if branch needs to be synced
	// BehindRemote, if > 0, signals that origin/<branch> has commits the
	// local doesn't (a teammate pushed). Sync will fast-forward via
	// `git merge --ff-only` before rebasing onto the parent. May be set
	// alongside other Behind* fields when a branch is both behind its parent
	// AND behind its remote.
	BehindRemote int
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
	UseMerge     bool // Use git merge instead of git rebase
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
	if err := m.Fetch(); err != nil {
		return nil, err
	}
	// Fetch any non-origin remotes referenced by branches in scope, so the
	// remote-ahead detection below can see fork-pushed commits. Best-effort:
	// a slow/unreachable fork shouldn't block detection — failures just
	// leave that remote's ahead-behind as 0 (same as no-fetch case).
	scopeForFetch := specificStacks
	if scopeForFetch == nil {
		if currentStackOnly {
			if cs, _, err := m.GetCurrentStack(); err == nil {
				scopeForFetch = []*config.Stack{cs}
			}
		} else {
			scopeForFetch = m.ListStacks()
		}
	}
	seen := map[string]bool{"origin": true, "": true}
	for _, s := range scopeForFetch {
		for _, b := range s.Branches {
			r := b.EffectiveRemote()
			if seen[r] || !b.CanPush() {
				continue
			}
			seen[r] = true
			_ = m.FetchRemote(r)
		}
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

	// remoteBehindFor returns the number of commits origin/<branch> has that
	// the local doesn't, or 0 if the branch has no remote tracking ref or
	// is _nopush. Surfaced via SyncInfo.BehindRemote so dry-run output
	// reflects collaborator-pull work in addition to parent-rebase work.
	remoteBehindFor := func(b *config.Branch) int {
		if !b.CanPush() {
			return 0
		}
		ref := b.EffectiveRemote() + "/" + b.Name
		if !m.git.RefExists(ref) {
			return 0
		}
		_, behind, err := m.git.GetAheadBehind(b.Name, ref)
		if err != nil {
			return 0
		}
		return behind
	}

	for _, stack := range stacksToCheck {
		for _, branch := range stack.Branches {
			if branch.IsMerged {
				continue
			}

			remoteBehind := remoteBehindFor(branch)

			if branch.Parent == stack.Root {
				behindBy, err := m.git.GetCommitsBehind(branch.Name, "origin/"+stack.Root)
				if (err == nil && behindBy > 0) || remoteBehind > 0 {
					results = append(results, SyncInfo{
						Branch:       branch.Name,
						BehindBy:     behindBy,
						StackRoot:    stack.Root,
						NeedsSync:    true,
						BehindRemote: remoteBehind,
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
					BehindRemote: remoteBehind,
				})
				continue
			}

			behindBy, err := m.git.GetCommitsBehind(branch.Name, parentRef)
			if (err == nil && behindBy > 0) || remoteBehind > 0 {
				results = append(results, SyncInfo{
					Branch:       branch.Name,
					BehindBy:     behindBy,
					BehindParent: branch.Parent,
					StackRoot:    stack.Root,
					NeedsSync:    true,
					BehindRemote: remoteBehind,
				})
			}
		}
	}

	return results, nil
}

// DetectSyncNeededForBranch checks if a specific branch needs syncing
// Returns SyncInfo if the branch needs syncing, nil otherwise.
//
// Reasons sync is needed (any of):
//   - Branch is behind origin/<stack.Root> (parent is the stack root).
//   - Parent was merged to origin/<stack.Root>.
//   - Branch is behind its (non-root) parent (parent has new commits).
//   - origin/<branch> has commits the local doesn't (teammate pushed).
func (m *Manager) DetectSyncNeededForBranch(branchName string, gh *github.Client) *SyncInfo {
	branch := m.GetBranch(branchName)
	if branch == nil || branch.IsMerged {
		return nil
	}

	stack := m.GetStackForBranch(branchName)
	if stack == nil {
		return nil
	}

	// Compute remote-ahead count once; surfaced via BehindRemote on whichever
	// SyncInfo we end up returning, so a single "needs sync" reason is enough
	// to capture both parent-relationship and remote-pull work.
	remoteBehind := 0
	if branch.CanPush() {
		remote := branch.EffectiveRemote()
		remoteRef := remote + "/" + branch.Name
		if m.git.RefExists(remoteRef) {
			if _, behind, err := m.git.GetAheadBehind(branch.Name, remoteRef); err == nil {
				remoteBehind = behind
			}
		}
	}

	if branch.Parent == stack.Root {
		behindBy, err := m.git.GetCommitsBehind(branch.Name, "origin/"+stack.Root)
		if (err == nil && behindBy > 0) || remoteBehind > 0 {
			info := &SyncInfo{
				Branch:       branch.Name,
				BehindBy:     behindBy,
				StackRoot:    stack.Root,
				NeedsSync:    true,
				BehindRemote: remoteBehind,
			}
			return info
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
			BehindRemote: remoteBehind,
		}
	}

	behindBy, err := m.git.GetCommitsBehind(branch.Name, parentRef)
	if (err == nil && behindBy > 0) || remoteBehind > 0 {
		return &SyncInfo{
			Branch:       branch.Name,
			BehindBy:     behindBy,
			BehindParent: branch.Parent,
			StackRoot:    stack.Root,
			NeedsSync:    true,
			BehindRemote: remoteBehind,
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
	if err := m.Fetch(); err != nil {
		return nil, err
	}

	var results []RebaseResult

	useMerge := callbacks != nil && callbacks.UseMerge

	// doSync performs a rebase or merge depending on the useMerge flag.
	// For rebase, it uses RebaseNonInteractive(target).
	// For merge, it uses MergeNonInteractive(target).
	doSync := func(g *git.Git, target string) git.RebaseResult {
		if useMerge {
			return g.MergeNonInteractive(target)
		}
		return g.RebaseNonInteractive(target)
	}

	// doSyncOnto performs a rebase --onto or merge depending on the useMerge flag.
	// For rebase, it uses RebaseOntoNonInteractive(newBase, oldBase).
	// For merge, the oldBase is irrelevant — just merge newBase.
	doSyncOnto := func(g *git.Git, newBase, oldBase string) git.RebaseResult {
		if useMerge {
			return g.MergeNonInteractive(newBase)
		}
		return g.RebaseOntoNonInteractive(newBase, oldBase)
	}

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

	// Best-effort: enable git rerere so manually-resolved conflicts get
	// auto-replayed on subsequent rebases (e.g. after --abort, or for hunks
	// that recur across siblings). Cached per Manager to avoid forking two
	// `git config` processes on every sync entry point.
	m.ensureRerereEnabled()

	// Clear stale snapshots from prior crashed/interrupted runs so they don't
	// pollute today's lookups. A snapshot is stale when the branch HEAD hasn't
	// moved since it was recorded AND there's no in-progress rebase/merge.
	m.cleanupStalePreSyncSHAs()

	// Record old HEAD commits for every branch in the selected stacks BEFORE
	// any rebasing. When a parent is rewritten, children must be rebased with
	// `--onto newParent oldParentSHA` to avoid replaying the parent's commits
	// (which would re-encounter conflicts that were already resolved upstream).
	// Persisted into BranchCache.PreSyncCommit so a follow-up `ezs sync
	// --continue` (a separate process invocation) can read them.
	var snapshotNames []string
	for _, stack := range stacksToSync {
		for _, branch := range stack.Branches {
			snapshotNames = append(snapshotNames, branch.Name)
		}
	}
	m.snapshotPreSyncSHAs(snapshotNames)

	// Maintain an in-memory copy of the snapshots taken at the start of this
	// run. Each branch's cache entry can be mutated mid-run (e.g. when its
	// rebase succeeds and we'd want to clear PreSyncCommit), so we read from
	// this map instead of re-querying the cache for downstream --onto bases.
	oldHeads := make(map[string]string, len(snapshotNames))
	for _, name := range snapshotNames {
		if bc := m.stackConfig.Cache.GetBranchCache(name); bc != nil && bc.PreSyncCommit != "" {
			oldHeads[name] = bc.PreSyncCommit
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

			// Determine the working directory for git operations.
			// Branches with worktrees use their own directory; branches without
			// use checkout-based sync in the main repo. resolveBranchWorktree
			// heals config/git drift: if config says no worktree but git has
			// one, we use the git path and back-fill config.
			worktreePath := m.resolveBranchWorktree(branch)
			useCheckout := worktreePath == ""
			var g *git.Git
			if useCheckout {
				g = m.git // will use syncViaCheckout for operations
			} else {
				g = git.New(worktreePath)
			}

			workDir := worktreePath
			if workDir == "" {
				workDir = m.repoDir
			}

			result := RebaseResult{Branch: branch.Name, WorktreePath: workDir, StackName: stack.DisplayName(), Remote: branch.Remote}

			// Create checkout-aware sync functions for this branch
			branchDoSync := func(target string) git.RebaseResult {
				if useCheckout {
					return syncViaCheckout(m.git, branch.Name, func(cg *git.Git) git.RebaseResult {
						return doSync(cg, target)
					})
				}
				return doSync(g, target)
			}
			branchDoSyncOnto := func(newBase, oldBase string) git.RebaseResult {
				if useCheckout {
					return syncViaCheckout(m.git, branch.Name, func(cg *git.Git) git.RebaseResult {
						return doSyncOnto(cg, newBase, oldBase)
					})
				}
				return doSyncOnto(g, newBase, oldBase)
			}

			// Autostash: stash uncommitted changes before rebase
			// Skip autostash for checkout-based sync (no worktree to have uncommitted changes in)
			didStash := false
			if !useCheckout && callbacks != nil && callbacks.Autostash {
				// Check for orphaned ezstack stash from a previous conflicted sync
				if _, found := g.FindEzstackStash(branch.Name); found {
					fmt.Fprintf(os.Stderr, "  Note: existing autostash found for %s (from a previous sync)\n", branch.Name)
					fmt.Fprintf(os.Stderr, "  Skipping autostash. Run 'git stash pop' in the worktree to restore, or 'git stash drop' to discard.\n")
					// Don't create another stash on top — the old one already has the user's changes
				} else if hasChanges, _ := g.HasChanges(); hasChanges {
					if err := g.StashPush(); err != nil {
						// Surface the failure instead of silently proceeding —
						// a failed stash followed by a rebase would clobber
						// uncommitted changes.
						result.Error = fmt.Errorf("autostash failed for %s: %w (refusing to rebase over uncommitted changes)", branch.Name, err)
						results = append(results, result)
						if !allStacks {
							return results, nil
						}
						continue
					}
					didStash = true
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
				msg := fmt.Sprintf("resolve conflicts in: %s", workDir)
				if didStash {
					msg += " (uncommitted changes stashed — will be restored on next successful sync, or run 'git stash pop' manually)"
				}
				return msg
			}

			// Integrate any commits a collaborator pushed to origin/<branch>
			// before we touch the parent. Order matters: pulling first lets
			// the subsequent --onto rebase replay the collaborator's commits
			// onto the new parent in a single, consistent operation.
			ffRes := m.integrateRemoteForBranch(branch)
			if ffRes.Error != nil {
				popStash()
				result.Error = ffRes.Error
				results = append(results, result)
				saveState(sc)
				if !allStacks {
					return results, nil
				}
				stackHasConflict = true
				continue
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

				syncResult := branchDoSync("origin/" + stack.Root)
				if syncResult.HasConflict {
					result.HasConflict = true
					result.Error = fmt.Errorf("%s", conflictMsg())
					results = append(results, result)
					saveState(sc)
					if !allStacks {
						return results, nil
					}
					stackHasConflict = true
					continue
				} else if syncResult.Error != nil {
					popStash()
					result.Error = syncResult.Error
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

				syncResult := branchDoSyncOnto(rebaseTarget, mergeBase)
				if syncResult.HasConflict {
					result.HasConflict = true
					result.Error = fmt.Errorf("%s", conflictMsg())
					results = append(results, result)
					saveState(sc)
					if !allStacks {
						return results, nil
					}
					stackHasConflict = true
					continue
				} else if syncResult.Error != nil {
					popStash()
					result.Error = syncResult.Error
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
					var resetErr error
					if useCheckout {
						resetErr = resetViaCheckout(m.git, branch.Name, parentRef)
					} else {
						resetErr = g.ResetHard(parentRef)
					}
					if resetErr != nil {
						popStash()
						result.Error = fmt.Errorf("failed to fast-forward: %w", resetErr)
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

				syncResult := branchDoSyncOnto(parentRef, oldParentHead)
				if syncResult.HasConflict {
					result.HasConflict = true
					result.Error = fmt.Errorf("%s", conflictMsg())
					results = append(results, result)
					saveState(sc)
					if !allStacks {
						return results, nil
					}
					stackHasConflict = true
					continue
				} else if syncResult.Error != nil {
					popStash()
					result.Error = syncResult.Error
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
						if !allStacks {
							return results, nil
						}
						stackHasConflict = true
						continue
					}
				}
				continue
			}

			// Cross-stack-parent fallback: parent isn't in the snapshotted set
			// (its stack isn't being synced this run). Consult persisted
			// PreSyncCommit; if absent, fall back to the parent's current HEAD,
			// which makes `--onto X X` equivalent to plain `git rebase X` —
			// safe when the parent has not been rewritten in any tracked run.
			// Pass branch.Name as descendantName so the lookup validates the
			// snapshot is still on this branch's history (defence against a
			// stale snapshot whose ancestor relation has been broken).
			fallbackOldBase := m.lookupPreSyncSHA(branch.Parent, branch.Name)
			var syncResult git.RebaseResult
			if fallbackOldBase != "" {
				syncResult = branchDoSyncOnto(parentRef, fallbackOldBase)
			} else {
				syncResult = branchDoSync(parentRef)
			}
			if syncResult.HasConflict {
				result.HasConflict = true
				result.Error = fmt.Errorf("%s", conflictMsg())
				results = append(results, result)
				saveState(sc)
				if !allStacks {
					return results, nil
				}
				stackHasConflict = true
				continue
			} else if syncResult.Error != nil {
				popStash()
				result.Error = syncResult.Error
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

	// If the run completed cleanly — every reported result is a success, no
	// conflicts and no other errors — the snapshots are no longer needed and
	// we clear them so the next sync starts fresh. If anything is mid-conflict
	// or errored (e.g. autostash failure), we keep snapshots so `ezs sync
	// --continue` (a separate invocation) or the user's retry can use them.
	allClean := true
	for _, r := range results {
		if r.HasConflict || r.Error != nil {
			allClean = false
			break
		}
	}
	if allClean {
		m.clearPreSyncSHAs(snapshotNames)
	}

	return results, nil
}

// SyncBranch syncs a specific branch, handling all cases:
// - Branch is behind origin/main (parent is main)
// - Parent branch was merged (rebase --onto main or merge)
// - Branch is behind its parent (rebase onto parent or merge)
// When useMerge is true, git merge is used instead of git rebase.
func (m *Manager) SyncBranch(branchName string, gh *github.Client, useMerge ...bool) (*RebaseResult, error) {
	merge := len(useMerge) > 0 && useMerge[0]
	branch := m.GetBranch(branchName)
	if branch == nil {
		return nil, fmt.Errorf("branch '%s' not found", branchName)
	}

	stack := m.GetStackForBranch(branchName)
	if stack == nil {
		return nil, fmt.Errorf("branch '%s' not found in any stack", branchName)
	}

	// Determine working directory and whether to use checkout-based sync.
	// resolveBranchWorktree heals config/git drift (see syncStackInternal
	// for why — same bug where config says no worktree but git has one).
	worktreePath := m.resolveBranchWorktree(branch)
	useCheckout := worktreePath == ""
	var g *git.Git
	if useCheckout {
		g = m.git
	} else {
		g = git.New(worktreePath)
	}

	workDir := worktreePath
	if workDir == "" {
		workDir = m.repoDir
	}

	// Helper to run sync operation with checkout fallback if needed
	doSyncOp := func(op func(g *git.Git) git.RebaseResult) git.RebaseResult {
		if useCheckout {
			return syncViaCheckout(m.git, branchName, op)
		}
		return op(g)
	}

	result := &RebaseResult{Branch: branch.Name, WorktreePath: workDir, Remote: branch.Remote}

	// Best-effort: enable git rerere so manually-resolved conflicts get
	// auto-replayed on subsequent rebases. Cached per Manager.
	m.ensureRerereEnabled()

	// Snapshot BEFORE integrateRemoteForBranch can move HEAD via fast-forward.
	// Descendants of this branch are anchored at its pre-FF state — they were
	// created when the local matched origin/<branch> at some earlier point and
	// don't have any collaborator commits in their history. If we snapshot
	// post-FF, a later lookupPreSyncSHA(thisBranch, descendant) sees the
	// post-FF SHA, fails the IsAncestor check, and falls back to the live
	// HEAD; then `--onto newParent newParent` reduces to plain `git rebase
	// newParent` which (when this branch's commits conflict with the new
	// upstream) reintroduces the cascading-conflict bug Plan A fixed.
	//
	// In the bulk path (syncStackInternal) snapshots are already taken before
	// per-branch FF; this is the single-branch entry point's equivalent.
	m.snapshotPreSyncSHAs([]string{branch.Name})

	// Pick up any new commits the remote has on origin/<branch> before we
	// rebase onto the parent. See integrateRemoteForBranch for the case
	// matrix; the short version is: strict fast-forward → pull, divergence
	// → skip with a warn line, no remote → no-op.
	if ffRes := m.integrateRemoteForBranch(branch); ffRes.Error != nil {
		result.Error = ffRes.Error
		return result, nil
	}

	if branch.Parent == stack.Root {
		behindBy, err := m.git.GetCommitsBehind(branch.Name, "origin/"+stack.Root)
		if err != nil || behindBy == 0 {
			result.Success = true
			return result, nil
		}

		result.BehindBy = behindBy
		result.SyncedParent = "origin/" + stack.Root

		syncResult := doSyncOp(func(sg *git.Git) git.RebaseResult {
			if merge {
				return sg.MergeNonInteractive("origin/" + stack.Root)
			}
			return sg.RebaseNonInteractive("origin/" + stack.Root)
		})
		if syncResult.HasConflict {
			result.HasConflict = true
			result.Error = fmt.Errorf("resolve conflicts in: %s", workDir)
			return result, nil
		} else if syncResult.Error != nil {
			result.Error = syncResult.Error
			return result, nil
		}
		// Don't clear PreSyncCommit here: descendants of this branch may not
		// have been re-synced yet (single-branch SyncBranch has no visibility
		// into the rest of the stack). A subsequent SyncBranch on a child will
		// look up this snapshot to correctly --onto. Cleanup happens at the
		// end of bulk sync runs and at the end of `ezs sync --continue`.
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

		// Snapshot was already taken at the top of SyncBranch (before any FF),
		// so descendants see this branch's pre-rewrite SHA when they look it up.
		syncResult := doSyncOp(func(sg *git.Git) git.RebaseResult {
			if merge {
				return sg.MergeNonInteractive("origin/" + stack.Root)
			}
			return sg.RebaseOntoNonInteractive("origin/"+stack.Root, mergeBase)
		})
		if syncResult.HasConflict {
			result.HasConflict = true
			result.Error = fmt.Errorf("resolve conflicts in: %s", workDir)
		} else if syncResult.Error != nil {
			result.Error = syncResult.Error
		} else {
			result.Success = true
			// PreSyncCommit retained — see comment in the stack-root branch.
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
		var resetErr error
		if useCheckout {
			resetErr = resetViaCheckout(m.git, branchName, parentRef)
		} else {
			resetErr = g.ResetHard(parentRef)
		}
		if resetErr != nil {
			result.Error = fmt.Errorf("failed to fast-forward: %w", resetErr)
			return result, nil
		}
		result.Success = true
		return result, nil
	}

	// Has commits — rebase the child's own commits onto the (possibly-rewritten)
	// parent. We must use `--onto newParent oldParent` rather than plain
	// `git rebase newParent`: when the parent was just rewritten in this or a
	// recent sync run, the merge-base of `branch` and `newParent` falls back
	// to the old stack root, and plain rebase replays *all* commits since the
	// stack root — including the parent's own commits, re-creating any conflict
	// that was already resolved in the parent's rebase. Looking up the parent's
	// PreSyncCommit and using it as `oldBase` replays only the child's commits.
	//
	// When no PreSyncCommit is recorded, lookupPreSyncSHA falls back to the
	// parent's current HEAD, making `--onto X X` equivalent to plain rebase —
	// safe whenever the parent has not been rewritten in any tracked session.
	// Pass branch.Name so a stale snapshot that no longer sits on this
	// branch's history is rejected in favour of the live-HEAD fallback.
	oldParentSHA := m.lookupPreSyncSHA(branch.Parent, branch.Name)

	// This branch's snapshot was already taken at the top of SyncBranch (before
	// any FF), so a subsequent SyncBranch on a grandchild — or a `--continue`
	// invocation — sees the pre-FF SHA when looking it up via lookupPreSyncSHA.
	syncResult := doSyncOp(func(sg *git.Git) git.RebaseResult {
		if merge {
			return sg.MergeNonInteractive(parentRef)
		}
		if oldParentSHA == "" {
			// No SHA available for either snapshot or live HEAD; degrade to
			// plain rebase rather than passing an empty oldBase to git.
			return sg.RebaseNonInteractive(parentRef)
		}
		return sg.RebaseOntoNonInteractive(parentRef, oldParentSHA)
	})
	if syncResult.HasConflict {
		result.HasConflict = true
		result.Error = fmt.Errorf("resolve conflicts in: %s", workDir)
		return result, nil
	} else if syncResult.Error != nil {
		result.Error = syncResult.Error
		return result, nil
	}
	// PreSyncCommit intentionally retained — see comment in the stack-root branch.
	result.Success = true
	return result, nil
}

// RebaseOnParent syncs the current branch onto its updated parent.
// When useMerge is true, git merge is used instead of git rebase.
func (m *Manager) RebaseOnParent(useMerge ...bool) error {
	merge := len(useMerge) > 0 && useMerge[0]

	currentStack, currentBranch, err := m.GetCurrentStack()
	if err != nil {
		return err
	}

	// If parent is the stack root (not in tree), use origin/<parent>
	parentRef := currentBranch.Parent
	if currentBranch.Parent == currentStack.Root {
		parentRef = "origin/" + currentBranch.Parent
	}

	// Snapshot before rewriting so any later sync of this branch's children
	// can use this branch's pre-rewrite SHA as their --onto oldBase.
	m.snapshotPreSyncSHAs([]string{currentBranch.Name})

	if merge {
		fmt.Fprintf(os.Stderr, "Merging %s into %s\n", parentRef, currentBranch.Name)
		return m.git.Merge(parentRef)
	}
	// Use --onto to avoid replaying the parent's commits when the parent has
	// been rewritten by a recent sync (cascade fix). Falls back to plain rebase
	// when no snapshot is available (oldBase == parentRef ⇒ equivalent).
	// currentBranch is the descendant being rebased; pass its name so a
	// stale snapshot that's no longer in its history degrades to plain rebase.
	oldParentSHA := m.lookupPreSyncSHA(currentBranch.Parent, currentBranch.Name)
	fmt.Fprintf(os.Stderr, "Rebasing %s onto %s\n", currentBranch.Name, parentRef)
	if oldParentSHA == "" || oldParentSHA == parentRef {
		return m.git.Rebase(parentRef)
	}
	return m.git.RebaseOnto(parentRef, oldParentSHA)
}

// RebaseChildren syncs all child branches after updating the current branch.
// When useMerge is true, git merge is used instead of git rebase.
// Returns results for each child branch processed.
func (m *Manager) RebaseChildren(useMerge ...bool) ([]RebaseResult, error) {
	merge := len(useMerge) > 0 && useMerge[0]

	_, currentBranch, err := m.GetCurrentStack()
	if err != nil {
		return nil, err
	}

	var results []RebaseResult
	children := m.GetChildren(currentBranch.Name)

	// Snapshot all children upfront in a single disk write rather than once
	// per loop iteration. Snapshots that turn out to be unused (e.g. for a
	// child that the loop never rewrites because an earlier child failed)
	// are harmless: they just record the current HEAD, which the next sync's
	// stale-cleanup or overwrite handles correctly.
	if len(children) > 0 {
		names := make([]string, 0, len(children))
		for _, c := range children {
			names = append(names, c.Name)
		}
		m.snapshotPreSyncSHAs(names)
	}

	for _, child := range children {
		useCheckout := child.WorktreePath == ""
		var g *git.Git
		if useCheckout {
			g = m.git
		} else {
			g = git.New(child.WorktreePath)
		}

		workDir := child.WorktreePath
		if workDir == "" {
			workDir = m.repoDir
		}

		result := RebaseResult{Branch: child.Name, WorktreePath: workDir, Remote: child.Remote}

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
			var resetErr error
			if useCheckout {
				resetErr = resetViaCheckout(m.git, child.Name, currentBranch.Name)
			} else {
				resetErr = g.ResetHard(currentBranch.Name)
			}
			if resetErr != nil {
				result.Error = fmt.Errorf("failed to fast-forward: %w", resetErr)
				results = append(results, result)
				continue
			}
			result.Success = true
			results = append(results, result)
		} else {
			// Has commits — rebase the child onto the (possibly-rewritten)
			// currentBranch. Use --onto with the parent's PreSyncCommit so we
			// only replay the child's own commits and not the parent's, which
			// would re-encounter conflicts already resolved upstream. Pass
			// child.Name as the descendant so the lookup rejects a stale
			// snapshot that no longer sits on this child's history.
			oldParentSHA := m.lookupPreSyncSHA(currentBranch.Name, child.Name)
			// Per-child snapshot was already taken upfront — see batch above.

			var syncResult git.RebaseResult
			doOp := func(cg *git.Git) git.RebaseResult {
				if merge {
					return cg.MergeNonInteractive(currentBranch.Name)
				}
				if oldParentSHA == "" {
					return cg.RebaseNonInteractive(currentBranch.Name)
				}
				return cg.RebaseOntoNonInteractive(currentBranch.Name, oldParentSHA)
			}
			if useCheckout {
				syncResult = syncViaCheckout(m.git, child.Name, doOp)
			} else {
				syncResult = doOp(g)
			}
			if syncResult.HasConflict {
				result.HasConflict = true
				result.Error = fmt.Errorf("resolve conflicts in: %s", workDir)
				results = append(results, result)
				// Stop immediately on conflict - user must resolve before continuing
				return results, nil
			} else if syncResult.Error != nil {
				result.Error = syncResult.Error
				results = append(results, result)
				// Stop on error as well
				return results, nil
			}
			// Don't clear PreSyncCommit on the child here: the recursive
			// RebaseChildren on this child's grandchildren (below) needs to
			// look up this child's pre-rewrite SHA as the oldBase for *their*
			// --onto. Cleanup happens at end-of-bulk-sync or `--continue`.
			result.Success = true
			results = append(results, result)
		}

		// Recursively sync this child's children
		var childResults []RebaseResult
		if child.WorktreePath != "" {
			childMgr, err := NewManager(child.WorktreePath)
			if err != nil {
				result := RebaseResult{
					Branch:       child.Name,
					WorktreePath: child.WorktreePath,
					Remote:       child.Remote,
					Error:        fmt.Errorf("failed to open manager for %s: %w", child.WorktreePath, err),
				}
				results = append(results, result)
				return results, nil
			}
			cr, err := childMgr.RebaseChildren(merge)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: failed to sync children of %s: %v\n", child.Name, err)
			}
			childResults = cr
		} else {
			// For non-worktree children, checkout the child branch so
			// GetCurrentStack() sees it, then recurse, then checkout back.
			origBranch, err := m.git.CurrentBranch()
			if err != nil {
				result := RebaseResult{
					Branch: child.Name,
					Error:  fmt.Errorf("failed to get current branch before recursing into %s: %w", child.Name, err),
				}
				results = append(results, result)
				return results, nil
			}
			if err := m.git.CheckoutBranch(child.Name); err != nil {
				result := RebaseResult{
					Branch: child.Name,
					Error:  fmt.Errorf("failed to checkout %s for recursive sync: %w", child.Name, err),
				}
				results = append(results, result)
				return results, nil
			}
			childMgr, err := NewManager(m.repoDir)
			if err != nil {
				_ = m.git.CheckoutBranch(origBranch)
				result := RebaseResult{
					Branch: child.Name,
					Error:  fmt.Errorf("failed to open manager after checkout of %s: %w", child.Name, err),
				}
				results = append(results, result)
				return results, nil
			}
			cr, rerr := childMgr.RebaseChildren(merge)
			if rerr != nil {
				fmt.Fprintf(os.Stderr, "  Warning: failed to sync children of %s: %v\n", child.Name, rerr)
			}
			childResults = cr
			if err := m.git.CheckoutBranch(origBranch); err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: synced children of %s but failed to switch back to %s: %v\n", child.Name, origBranch, err)
			}
		}
		results = append(results, childResults...)
		// If any recursive result hit a conflict or error, stop so the user
		// can resolve before syncing further siblings.
		for _, cr := range childResults {
			if cr.HasConflict || cr.Error != nil {
				return results, nil
			}
		}
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
