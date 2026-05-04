package commands

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/github"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/hooks"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// BuildHookContext returns a hooks.Context populated with the current repo
// root, branch, and (if available) stack hash/name. Errors are swallowed: a
// hook with partial context is better than no hook at all.
func BuildHookContext() *hooks.Context {
	ctx := &hooks.Context{}
	cwd, err := os.Getwd()
	if err != nil {
		return ctx
	}
	g := git.New(cwd)
	if root, rErr := g.GetRepoRoot(); rErr == nil {
		ctx.RepoRoot = root
	}
	if br, bErr := g.CurrentBranch(); bErr == nil {
		ctx.Branch = br
	}
	mgr, mErr := stack.NewReadOnlyManager(cwd)
	if mErr == nil {
		if cs, _, csErr := mgr.GetCurrentStack(); csErr == nil && cs != nil {
			ctx.StackHash = cs.Hash
			ctx.StackName = cs.Name
		}
	}
	return ctx
}

// isShellWrapped returns true if ezs is running through the shell wrapper function.
// When true, stdout "cd <path>" will be eval'd by the shell. When false, the tool
// should print the path to stderr and tell the user to cd manually.
func isShellWrapped() bool {
	return os.Getenv("EZS_SHELL_WRAPPER") == "1"
}

// shellQuote returns a single-quoted shell string, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// EmitCd outputs a cd command to stdout if running through the shell wrapper,
// otherwise prints a message to stderr telling the user to cd manually.
func EmitCd(path string) {
	if isShellWrapped() {
		fmt.Printf("cd %s\n", shellQuote(path))
	} else {
		ui.Info(fmt.Sprintf("Run: cd %s", shellQuote(path)))
		ui.Info("Tip: Add to your shell config for automatic cd: eval \"$(ezs --shell-init)\"")
	}
}

// displayPRState returns a human-friendly label for a cached PRState value.
// "" (legacy entries that never had state recorded) shows as "state unknown"
// rather than empty quotes, which would otherwise read as "()" in error
// messages.
func displayPRState(state string) string {
	if state == "" {
		return "state unknown"
	}
	return state
}

// prStateFromGitHub maps a github.PR onto the canonical cached PRState value.
// The four-way enum ("MERGED" | "CLOSED" | "DRAFT" | "OPEN") matches what
// fetchBranchStatuses writes — keep them aligned so a status-driven refresh
// and an explicit pr-create/update-driven refresh produce the same cache.
func prStateFromGitHub(pr *github.PR) string {
	if pr == nil {
		return ""
	}
	if pr.Merged {
		return "MERGED"
	}
	if pr.State == "CLOSED" {
		return "CLOSED"
	}
	if pr.IsDraft {
		return "DRAFT"
	}
	return "OPEN"
}

// savePRToCache writes the PR-association fields for a branch in one shot.
// Takes the full *github.PR (rather than scattered scalars) so the cache
// can never end up with a stale pr_state / is_merged paired with a fresh
// pr_url — a bug the previous narrower signature actively produced.
//
// Uses the atomic mutator so concurrent peer processes (e.g. an `ezs ls`
// running in another terminal that's writing PRState for unrelated
// branches via fetchBranchStatuses) don't clobber this update — the
// older LoadCacheConfig + SetBranchCache + Save pattern overwrites the
// entire branches map with whatever this process loaded earlier.
func savePRToCache(cacheDir, branchName string, pr *github.PR) {
	if pr == nil {
		return
	}
	err := config.MutateBranchCache(cacheDir, branchName, func(bc *config.BranchCache) (*config.BranchCache, error) {
		if bc == nil {
			bc = &config.BranchCache{}
		}
		bc.PRNumber = pr.Number
		bc.PRUrl = pr.URL
		bc.PRState = prStateFromGitHub(pr)
		bc.IsMerged = pr.Merged
		return bc, nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save PR cache: %v\n", err)
	}
}

// clearPRFromCache zeroes the PR-association fields for a branch (pr_url,
// pr_state, is_merged) while preserving worktree, remote, and is_remote.
// Used by `ezs pr unlink` and by recovery paths that detect a cached PR is
// no longer on GitHub. No-op when the branch has no cache entry.
func clearPRFromCache(cacheDir, branchName string) error {
	return config.MutateBranchCache(cacheDir, branchName, func(bc *config.BranchCache) (*config.BranchCache, error) {
		if bc == nil {
			return nil, nil
		}
		bc.ClearPRFields()
		return bc, nil
	})
}

// prFetcher abstracts the github.Client methods that the refresh path needs.
// Lets unit tests reconcile cache state without exec'ing gh — *github.Client
// satisfies it directly.
type prFetcher interface {
	GetPR(number int) (*github.PR, error)
	GetPRByBranch(branch string) (*github.PR, error)
}

// refreshPRStateFromGitHub queries the live PR for a branch and reconciles
// the local cache with the result. Behavior:
//
//   - Returns (pr, nil) when GitHub has a PR for the branch. Cache and the
//     in-memory branch are updated in lockstep.
//   - Returns (nil, nil) when GitHub confirms no PR exists for the branch
//     (errors.Is(err, github.ErrPRNotFound), or the prFetcher returns
//     (nil, nil) directly). The cached PR fields are cleared so the next
//     `pr create` proceeds without nagging.
//   - Returns (nil, err) on any other error (transient/network/unauthorized).
//     Cache is NOT touched, so an unreachable GitHub never accidentally
//     clears a perfectly good cache.
//
// Prefers GetPR(number) when a number is already cached; this path works for
// fork PRs whose head ref isn't reachable via `gh pr view <branch>`. Falls
// back to GetPRByBranch when the number lookup fails, since a stale cached
// number (e.g., the PR was hard-deleted) would otherwise leave the caller
// blind. If both lookups confirm "not found" we treat that as the
// PR-was-deleted case rather than a transient error.
func refreshPRStateFromGitHub(gh prFetcher, cacheDir string, branch *config.Branch) (*github.PR, error) {
	pr, err := fetchLivePR(gh, branch)
	return applyPRRefresh(cacheDir, branch, pr, err)
}

// fetchLivePR resolves the live PR for a branch without touching the cache or
// mutating the branch. Split from refreshPRStateFromGitHub so callers like
// `pr refresh -s` can fetch many branches in parallel and apply the cache
// updates serially (avoiding the load-modify-save race in CacheConfig.Save
// where each goroutine would otherwise overwrite the others' updates).
func fetchLivePR(gh prFetcher, branch *config.Branch) (*github.PR, error) {
	if branch.PRNumber > 0 {
		pr, err := gh.GetPR(branch.PRNumber)
		if err == nil {
			return pr, nil
		}
		// Stale cached number — try the head-branch lookup. Distinguish
		// the three resolutions: live PR found, confirmed not-found, or
		// transient failure. We only swallow the original GetPR error
		// when the fallback gives an unambiguous answer.
		br, brErr := gh.GetPRByBranch(branch.Name)
		switch {
		case brErr == nil && br != nil:
			return br, nil
		case errors.Is(brErr, github.ErrPRNotFound):
			return nil, nil
		case brErr == nil && br == nil:
			return nil, nil
		default:
			return nil, err
		}
	}
	pr, err := gh.GetPRByBranch(branch.Name)
	if errors.Is(err, github.ErrPRNotFound) {
		return nil, nil
	}
	return pr, err
}

// applyPRRefresh reconciles a single (branch, fetch-result) tuple into the
// on-disk cache and the in-memory branch. Safe to call serially after a batch
// of fetchLivePR calls.
func applyPRRefresh(cacheDir string, branch *config.Branch, pr *github.PR, fetchErr error) (*github.PR, error) {
	if fetchErr != nil {
		return nil, fetchErr
	}
	if pr == nil {
		if cleanErr := clearPRFromCache(cacheDir, branch.Name); cleanErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", cleanErr)
		}
		branch.PRNumber, branch.PRUrl, branch.PRState, branch.IsMerged = 0, "", "", false
		return nil, nil
	}
	branch.PRNumber = pr.Number
	branch.PRUrl = pr.URL
	branch.PRState = prStateFromGitHub(pr)
	branch.IsMerged = pr.Merged
	savePRToCache(cacheDir, branch.Name, pr)
	return pr, nil
}

// updateStackDescriptions updates PR descriptions for all PRs in the given stack.
func updateStackDescriptions(gh *github.Client, s *config.Stack, activeBranch string) error {
	ui.Info("Updating PR stack descriptions...")
	return gh.UpdateStackDescription(s, activeBranch)
}

// updatePRMetadata updates base branches and stack descriptions for all PRs in the stack.
// Called after pushes and stack mutations to keep PR metadata in sync.
// All GitHub API calls are parallelized to avoid serial latency.
func updatePRMetadata(gh *github.Client, s *config.Stack, currentBranch *config.Branch) {
	// Collect branches with PRs
	var prBranches []*config.Branch
	for _, b := range s.Branches {
		if b.PRNumber > 0 {
			prBranches = append(prBranches, b)
		}
	}
	if len(prBranches) == 0 {
		return
	}

	// Fetch all PR data in parallel
	prMap := make(map[int]*github.PR) // PR number -> PR data
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	for _, b := range prBranches {
		wg.Add(1)
		go func(branch *config.Branch) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pr, err := gh.GetPR(branch.PRNumber)
			if err == nil {
				mu.Lock()
				prMap[branch.PRNumber] = pr
				mu.Unlock()
			}
		}(b)
	}
	wg.Wait()

	// Update base branches where needed
	for _, b := range prBranches {
		pr := prMap[b.PRNumber]
		if pr == nil || pr.State == "CLOSED" || pr.Merged {
			continue
		}
		if pr.Base != b.Parent {
			if err := gh.UpdatePRBase(b.PRNumber, b.Parent); err != nil {
				ui.Warn(fmt.Sprintf("Failed to update base branch for PR #%d: %v", b.PRNumber, err))
			}
		}
	}

	// Update stack descriptions, passing pre-fetched PR data to avoid re-fetching
	activeName := ""
	if currentBranch != nil {
		activeName = currentBranch.Name
	}
	if err := gh.UpdateStackDescriptionCached(s, activeName, prMap); err != nil {
		ui.Warn(fmt.Sprintf("Failed to update stack descriptions: %v", err))
	}
}

// OfferForcePush prompts the user to force push a branch with --force-with-lease.
// remote specifies which git remote to push to (empty defaults to "origin").
// Returns true if push was successful, false otherwise.
func OfferForcePush(branchName, worktreePath, remote string) bool {
	if remote == config.RemoteNoPush {
		ui.Warn(fmt.Sprintf("Skipping push for %s (fork does not allow maintainer push)", branchName))
		return true // Don't block sync continuation
	}
	if remote == "" {
		remote = "origin"
	}
	g := git.New(worktreePath)

	needsPush, err := g.IsLocalAheadOfRemote(branchName, remote)
	if err != nil {
		ui.Warn(fmt.Sprintf("Could not check if push is needed: %v", err))
		needsPush = true
	}

	if !needsPush {
		return true
	}

	fmt.Fprintln(os.Stderr)
	ui.Warn("Force push required to update remote branch")
	if ui.ConfirmTUI(fmt.Sprintf("Force push %s (--force-with-lease) to %s", branchName, remote)) {
		ui.Info("Pushing...")
		// PushBranch explicitly names the branch — never trust CurrentBranch()
		// of worktreePath, which can be main if we're pushing from the main
		// repo dir after a syncViaCheckout restored HEAD.
		if err := g.PushBranch(branchName, true, remote); err != nil {
			ui.Error(fmt.Sprintf("Push failed: %v. Check your network connection and remote access", err))
			return false
		}
		ui.Success("Pushed successfully")
		return true
	}

	return false
}

// OfferForcePushMultiple prompts the user to force push multiple branches.
// getBranchRemote returns the git remote for a given branch (empty defaults to "origin").
// Returns the number of successfully pushed branches.
func OfferForcePushMultiple(branches []string, getBranchWorktree func(string) string, getBranchRemote func(string) string) int {
	if len(branches) == 0 {
		return 0
	}

	fmt.Fprintln(os.Stderr)
	ui.Warn("Force push required to update remote branches")

	pushed := 0
	for _, branchName := range branches {
		worktreePath := getBranchWorktree(branchName)
		if worktreePath == "" {
			continue
		}

		remote := ""
		if getBranchRemote != nil {
			remote = getBranchRemote(branchName)
		}
		if remote == config.RemoteNoPush {
			ui.Warn(fmt.Sprintf("Skipping push for %s (fork does not allow maintainer push)", branchName))
			continue
		}
		if remote == "" {
			remote = "origin"
		}

		g := git.New(worktreePath)
		needsPush, err := g.IsLocalAheadOfRemote(branchName, remote)
		if err == nil && !needsPush {
			continue
		}

		if ui.ConfirmTUI(fmt.Sprintf("Force push %s (--force-with-lease) to %s", branchName, remote)) {
			ui.Info(fmt.Sprintf("Pushing %s...", branchName))
			if err := g.PushBranch(branchName, true, remote); err != nil {
				ui.Error(fmt.Sprintf("Push failed for %s: %v. Check remote access or try: git push --force-with-lease", branchName, err))
			} else {
				ui.Success(fmt.Sprintf("Pushed %s successfully", branchName))
				pushed++
			}
		}
	}

	return pushed
}

// OfferPush prompts the user to push a branch (regular push, not force).
// remote specifies which git remote to push to (empty defaults to "origin").
// Used after merge operations where history is not rewritten.
// Returns true if push was successful or not needed, false if declined.
func OfferPush(branchName, worktreePath, remote string) bool {
	if remote == config.RemoteNoPush {
		ui.Warn(fmt.Sprintf("Skipping push for %s (fork does not allow maintainer push)", branchName))
		return true // Don't block sync continuation
	}
	if remote == "" {
		remote = "origin"
	}
	g := git.New(worktreePath)

	needsPush, err := g.IsLocalAheadOfRemote(branchName, remote)
	if err != nil {
		ui.Warn(fmt.Sprintf("Could not check if push is needed: %v", err))
		needsPush = true
	}

	if !needsPush {
		return true
	}

	fmt.Fprintln(os.Stderr)
	if ui.ConfirmTUI(fmt.Sprintf("Push %s to %s", branchName, remote)) {
		ui.Info("Pushing...")
		// PushBranch explicitly names the branch — never trust CurrentBranch()
		// of worktreePath, which can be main if we're pushing from the main
		// repo dir after a syncViaCheckout restored HEAD.
		if err := g.PushBranch(branchName, false, remote); err != nil {
			// If regular push fails (e.g., diverged history from prior rebase), offer force push
			ui.Warn(fmt.Sprintf("Push failed: %v", err))
			if ui.ConfirmTUI(fmt.Sprintf("Force push %s (--force-with-lease) to %s", branchName, remote)) {
				if err := g.PushBranch(branchName, true, remote); err != nil {
					ui.Error(fmt.Sprintf("Force push failed: %v", err))
					return false
				}
				ui.Success("Pushed successfully")
				return true
			}
			return false
		}
		ui.Success("Pushed successfully")
		return true
	}

	return false
}

// OfferPushMultiple prompts the user to push multiple branches (regular push, not force).
// getBranchRemote returns the git remote for a given branch (empty defaults to "origin").
// Used after merge operations where history is not rewritten.
// Returns the number of successfully pushed branches.
func OfferPushMultiple(branches []string, getBranchWorktree func(string) string, getBranchRemote func(string) string) int {
	if len(branches) == 0 {
		return 0
	}

	fmt.Fprintln(os.Stderr)

	pushed := 0
	for _, branchName := range branches {
		worktreePath := getBranchWorktree(branchName)
		if worktreePath == "" {
			continue
		}

		remote := ""
		if getBranchRemote != nil {
			remote = getBranchRemote(branchName)
		}
		if remote == config.RemoteNoPush {
			ui.Warn(fmt.Sprintf("Skipping push for %s (fork does not allow maintainer push)", branchName))
			continue
		}
		if remote == "" {
			remote = "origin"
		}

		g := git.New(worktreePath)
		needsPush, err := g.IsLocalAheadOfRemote(branchName, remote)
		if err == nil && !needsPush {
			continue
		}

		if ui.ConfirmTUI(fmt.Sprintf("Push %s to %s", branchName, remote)) {
			ui.Info(fmt.Sprintf("Pushing %s...", branchName))
			if err := g.PushBranch(branchName, false, remote); err != nil {
				// Fall back to force push if regular push fails
				ui.Warn(fmt.Sprintf("Push failed: %v. Trying force push...", err))
				if err := g.PushBranch(branchName, true, remote); err != nil {
					ui.Error(fmt.Sprintf("Force push failed for %s: %v", branchName, err))
				} else {
					ui.Success(fmt.Sprintf("Pushed %s successfully (force)", branchName))
					pushed++
				}
			} else {
				ui.Success(fmt.Sprintf("Pushed %s successfully", branchName))
				pushed++
			}
		}
	}

	return pushed
}

// getMainWorktreePath returns the main worktree path, falling back to cwd.
func getMainWorktreePath(g *git.Git) string {
	mainWorktree, _ := g.GetMainWorktree()
	if mainWorktree == "" {
		if cwd, err := os.Getwd(); err == nil {
			return cwd
		}
	}
	return mainWorktree
}

// newGitHubClient creates a GitHub client from the git remote URL.
func newGitHubClient(g *git.Git) (*github.Client, error) {
	remoteURL, err := g.GetRemote("origin")
	if err != nil {
		return nil, fmt.Errorf("failed to get remote: %w", err)
	}
	return github.NewClient(remoteURL)
}

// selectAndRegisterRemotePR fetches open PRs, shows a selection UI,
// prints the remote branch warning, fetches the remote, and registers it as a stack root.
// If identifier is non-empty, it is used to look up the PR directly (by number or branch name)
// instead of showing the interactive selection menu.
// Returns the selected PR info.
// remoteBranchResult holds the resolved remote branch info after selection/lookup.
type remoteBranchResult struct {
	Branch    string // Remote branch name (head)
	Base      string // PR base branch (empty if no PR)
	PRNumber  int    // 0 if no PR
	PRURL     string // empty if no PR
	StackHash string // Hash of the stack this branch was registered to
}

func selectAndRegisterRemoteBranch(g *git.Git, mgr *stack.Manager, identifier string) (remoteBranchResult, error) {
	ui.Info("Fetching remote branch...")
	if err := g.Fetch(); err != nil {
		return remoteBranchResult{}, fmt.Errorf("failed to fetch: %w", err)
	}

	var result remoteBranchResult

	if identifier != "" {
		// Strip "origin/" prefix if the user passed it
		identifier = strings.TrimPrefix(identifier, "origin/")

		if num, parseErr := strconv.Atoi(identifier); parseErr == nil {
			// Numeric identifier — must be a PR number
			gh, err := newGitHubClient(g)
			if err != nil {
				return remoteBranchResult{}, err
			}
			ui.Info(fmt.Sprintf("Looking up PR #%d...", num))
			pr, err := gh.GetPR(num)
			if err != nil {
				return remoteBranchResult{}, fmt.Errorf("failed to find PR #%d: %w", num, err)
			}
			result = remoteBranchResult{Branch: pr.Head, Base: pr.Base, PRNumber: pr.Number, PRURL: pr.URL}
		} else {
			// String identifier — could be a branch with or without a PR
			if !g.RemoteBranchExists(identifier) {
				return remoteBranchResult{}, fmt.Errorf("remote branch '%s' not found (no origin/%s)", identifier, identifier)
			}
			result = remoteBranchResult{Branch: identifier}
			// Try to find a PR for this branch (non-fatal if it fails)
			gh, ghErr := newGitHubClient(g)
			if ghErr == nil {
				pr, prErr := gh.GetPRByBranch(identifier)
				if prErr == nil && pr != nil {
					result.Base = pr.Base
					result.PRNumber = pr.Number
					result.PRURL = pr.URL
				}
			}
		}
	} else {
		// Interactive: show open PRs picker
		gh, err := newGitHubClient(g)
		if err != nil {
			return remoteBranchResult{}, err
		}
		ui.Info("Fetching open PRs...")
		openPRs, err := gh.ListOpenPRs()
		if err != nil {
			return remoteBranchResult{}, fmt.Errorf("failed to list open PRs: %w", err)
		}
		if len(openPRs) == 0 {
			return remoteBranchResult{}, fmt.Errorf("no open PRs found in this repository")
		}

		prOptions := make([]string, len(openPRs))
		for i, pr := range openPRs {
			prOptions[i] = fmt.Sprintf("#%d %s - %s (%s)", pr.Number, pr.Branch, pr.Title, pr.Author)
		}

		selectedIdx, err := ui.SelectOption(prOptions, "Select PR to use as stack base")
		if err != nil {
			return remoteBranchResult{}, err
		}
		selected := openPRs[selectedIdx]
		// Look up full PR to get base branch
		pr, prErr := gh.GetPR(selected.Number)
		if prErr == nil && pr != nil {
			result = remoteBranchResult{Branch: pr.Head, Base: pr.Base, PRNumber: pr.Number, PRURL: pr.URL}
		} else {
			result = remoteBranchResult{Branch: selected.Branch, PRNumber: selected.Number, PRURL: selected.URL}
		}
	}

	// Verify the remote branch actually exists
	if !g.RemoteBranchExists(result.Branch) {
		return remoteBranchResult{}, fmt.Errorf("remote branch '%s' not found after fetch", result.Branch)
	}

	// If no PR base was found, infer from common base branches
	if result.Base == "" {
		for _, candidate := range []string{"main", "master"} {
			if g.RemoteBranchExists(candidate) {
				result.Base = candidate
				break
			}
		}
	}

	printRemoteBranchWarning()

	hash, err := mgr.RegisterRemoteBranch(result.Branch, result.Base, result.PRNumber, result.PRURL)
	if err != nil {
		return remoteBranchResult{}, fmt.Errorf("failed to register remote branch: %w", err)
	}
	result.StackHash = hash

	return result, nil
}

// printRemoteBranchWarning prints the warning about remote branches not being rebased.
func printRemoteBranchWarning() {
	fmt.Fprintln(os.Stderr)
	ui.Warn("Note: This remote branch will never be rebased since it is assumed")
	ui.Warn(fmt.Sprintf("that it does not belong to you. Only %sYOUR%s branches that are stacked", ui.Bold, ui.Reset+ui.Yellow))
	ui.Warn("on this branch will be handled by ezstack.")
	fmt.Fprintln(os.Stderr)
}

// discoverAndCachePRs discovers PRs from GitHub for branches that don't have PR numbers cached
// and saves them to the config. Returns a GitHub client for further use (or nil if unavailable).
// Also discovers root PR info if missing (for remote base branches).
func discoverAndCachePRs(g *git.Git, s *config.Stack, debug bool) *github.Client {
	remoteURL, err := g.GetRemote("origin")
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] discoverAndCachePRs: GetRemote error: %v\n", err)
		}
		return nil
	}

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] discoverAndCachePRs: remoteURL=%s\n", remoteURL)
	}

	gh, err := github.NewClient(remoteURL)
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] discoverAndCachePRs: NewClient error: %v\n", err)
		}
		return nil
	}

	// Discover root PR if the root is a remote feature branch without PR info
	needsRootDiscovery := s.RootPRNumber == 0 && s.Root != "main" && s.Root != "master"
	if needsRootDiscovery {
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Discovering root PR for %s\n", s.Root)
		}
		pr, prErr := gh.GetPRByBranch(s.Root)
		if prErr == nil && pr != nil {
			if debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] Found root PR #%d for %s\n", pr.Number, s.Root)
			}
			s.RootPRNumber = pr.Number
			s.RootPRUrl = pr.URL
			if s.RootBase == "" {
				s.RootBase = pr.Base
			}
			// Save root PR info to config
			mainWorktree := getMainWorktreePath(g)
			sc, scErr := config.LoadStackConfig(mainWorktree)
			if scErr == nil {
				if existing, ok := sc.Stacks[s.Hash]; ok {
					existing.RootPRUrl = pr.URL
					if existing.RootBase == "" {
						existing.RootBase = pr.Base
					}
					if err := sc.Save(mainWorktree); err != nil {
						fmt.Fprintf(os.Stderr, "  Warning: failed to save stack config after root PR discovery: %v\n", err)
					}
				}
			}
		}
	}

	// Collect branches that need PR discovery
	var uncached []*config.Branch
	for _, branch := range s.Branches {
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Checking branch %s (PRNumber=%d)\n", branch.Name, branch.PRNumber)
		}
		if branch.PRNumber == 0 {
			uncached = append(uncached, branch)
		}
	}

	if len(uncached) == 0 {
		return gh
	}

	// Discover PRs in parallel
	type result struct {
		branch *config.Branch
		pr     *github.PR
		err    error
	}
	results := make([]result, len(uncached))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	for i, branch := range uncached {
		wg.Add(1)
		go func(idx int, b *config.Branch) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pr, err := gh.GetPRByBranch(b.Name)
			results[idx] = result{branch: b, pr: pr, err: err}
		}(i, branch)
	}
	wg.Wait()

	discoveredPRs := false
	ghAccessWarningShown := false
	for _, r := range results {
		if r.err != nil {
			if debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] GetPRByBranch(%s) error: %v\n", r.branch.Name, r.err)
			}
			if !ghAccessWarningShown {
				errStr := r.err.Error()
				if strings.Contains(errStr, "cannot access repository") ||
					strings.Contains(errStr, "authentication") {
					ui.Warn(errStr)
					ghAccessWarningShown = true
				}
			}
			continue
		}
		if r.pr != nil {
			if debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] Found PR #%d for branch %s\n", r.pr.Number, r.branch.Name)
			}
			// Mirror the live PR onto the in-memory branch in lockstep
			// (was previously number/url only — a half-update that paired
			// with savePRToCache's old narrow signature). State and merged
			// are persisted below alongside number/url so the cache stays
			// consistent even if fetchBranchStatuses' later per-PR refresh
			// can't reach GitHub.
			r.branch.PRNumber = r.pr.Number
			r.branch.PRUrl = r.pr.URL
			r.branch.PRState = prStateFromGitHub(r.pr)
			r.branch.IsMerged = r.pr.Merged
			discoveredPRs = true
		}
	}

	if discoveredPRs {
		mainWorktree := getMainWorktreePath(g)
		// Per-branch atomic writes: a peer process running `ezs pr refresh`
		// or `ezs pr update` against the same stacks.json no longer loses its
		// updates to the LoadCacheConfig + SetBranchCache + Save replace-all
		// pattern this used to do.
		for _, r := range results {
			if r.pr == nil {
				continue
			}
			pr := r.pr
			err := config.MutateBranchCache(mainWorktree, r.branch.Name, func(bc *config.BranchCache) (*config.BranchCache, error) {
				if bc == nil {
					bc = &config.BranchCache{}
				}
				bc.PRNumber = pr.Number
				bc.PRUrl = pr.URL
				bc.PRState = prStateFromGitHub(pr)
				bc.IsMerged = pr.Merged
				return bc, nil
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: failed to save PR cache for %s: %v\n", r.branch.Name, err)
			}
		}
	}

	return gh
}

// resolveLocalRef returns the local branch name if it exists, falling back to
// origin/<name> if the branch is only present on the remote. Diff stats should
// always prefer the local ref so they reflect the user's working state.
func resolveLocalRef(g *git.Git, name string) string {
	if name == "" {
		return name
	}
	if g.BranchExists(name) {
		return name
	}
	if g.RemoteBranchExists(name) {
		return "origin/" + name
	}
	return name
}

// upstreamRef returns origin/<name> when the remote-tracking ref exists,
// falling back to resolveLocalRef otherwise. Use this for parents that are
// upstream-tracked (the stack's base branch, or a remote PR target): the
// local copy of such a branch can drift from origin between fetches, so
// diffing against it would either hide upstream changes or, after a sync
// that rebased children onto origin/<base>, include every commit that
// landed upstream since the last local update.
func upstreamRef(g *git.Git, name string) string {
	if name == "" {
		return name
	}
	if g.RemoteBranchExists(name) {
		return "origin/" + name
	}
	return resolveLocalRef(g, name)
}

// fetchDiffStats computes diff stats for all branches in a stack using parallel local git ops.
// This is fast (no network) and safe to call from ezs ls.
func fetchDiffStats(g *git.Git, s *config.Stack) map[string]*ui.BranchStatus {
	statusMap := make(map[string]*ui.BranchStatus)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Compute diff for the root branch against its base.
	// Use RootBase if stored, otherwise infer from common base branches
	// when the root is a remote feature branch (not main/master itself).
	rootBase := s.RootBase
	if rootBase == "" && s.Root != "main" && s.Root != "master" {
		for _, candidate := range []string{"main", "master"} {
			if g.RemoteBranchExists(candidate) {
				rootBase = candidate
				break
			}
		}
	}
	if rootBase != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// rootBase is the PR target of the stack's root branch — always an
			// upstream-tracked branch (e.g. main). Diff against origin/<rootBase>
			// so the stats don't drift with the local copy between fetches.
			parentRef := upstreamRef(g, rootBase)
			branchRef := resolveLocalRef(g, s.Root)
			added, removed, err := g.GetDiffStat(parentRef, branchRef)
			if err != nil {
				return
			}
			mu.Lock()
			statusMap[s.Root] = &ui.BranchStatus{
				Additions: added,
				Deletions: removed,
			}
			mu.Unlock()
		}()
	}

	for _, branch := range s.Branches {
		wg.Add(1)
		go func(b *config.Branch) {
			defer wg.Done()
			if b.IsMerged {
				return
			}
			// When the parent is the stack's base (upstream-tracked: main,
			// master, or a remote PR branch the stack sits on), diff against
			// origin/<parent>. Local <parent> can be stale — sync rebases
			// children onto origin/<base> without moving local <base>, which
			// would otherwise make the diff include every upstream commit
			// landed since the last local update. For non-base parents (other
			// branches inside the stack) keep using the local ref so
			// unpushed local commits aren't misreported as branch content.
			var parentRef string
			if b.Parent != "" && (b.Parent == s.Root || b.Parent == s.RootBase) {
				parentRef = upstreamRef(g, b.Parent)
			} else {
				parentRef = resolveLocalRef(g, b.Parent)
			}
			branchRef := resolveLocalRef(g, b.Name)
			added, removed, err := g.GetDiffStat(parentRef, branchRef)
			if err != nil {
				return
			}
			mu.Lock()
			statusMap[b.Name] = &ui.BranchStatus{
				Additions: added,
				Deletions: removed,
			}
			mu.Unlock()
		}(branch)
	}

	wg.Wait()
	return statusMap
}

// fetchBranchStatuses fetches PR and CI status for all branches in a stack (used by ezs status)
// Also caches merged status to the config when detected
func fetchBranchStatuses(g *git.Git, s *config.Stack, debug bool) map[string]*ui.BranchStatus {
	statusMap := fetchDiffStats(g, s)

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] fetchBranchStatuses for stack %s with %d branches\n", s.Hash, len(s.Branches))
	}

	gh := discoverAndCachePRs(g, s, debug)
	if gh == nil {
		return statusMap
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	// Semaphore to limit concurrent gh CLI calls
	sem := make(chan struct{}, 10)

	for _, branch := range s.Branches {
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] branch %s PRNumber=%d\n", branch.Name, branch.PRNumber)
		}
		if branch.PRNumber == 0 {
			continue
		}

		wg.Add(1)
		go func(b *config.Branch) {
			defer wg.Done()

			// Fetch PR and checks in parallel for this branch
			var prData *github.PR
			var checksData *github.CheckStatus
			var prErr, checksErr error
			var innerWg sync.WaitGroup

			innerWg.Add(2)

			// Fetch PR details
			go func() {
				defer innerWg.Done()
				sem <- struct{}{}        // Acquire semaphore
				defer func() { <-sem }() // Release semaphore
				prData, prErr = gh.GetPR(b.PRNumber)
			}()

			// Fetch PR checks
			go func() {
				defer innerWg.Done()
				sem <- struct{}{}        // Acquire semaphore
				defer func() { <-sem }() // Release semaphore
				checksData, checksErr = gh.GetPRChecks(b.PRNumber)
			}()

			innerWg.Wait()

			mu.Lock()
			status := statusMap[b.Name]
			if status == nil {
				status = &ui.BranchStatus{}
				statusMap[b.Name] = status
			}

			// Process PR data
			if prErr == nil {
				if prData.Merged {
					status.PRState = "MERGED"
					// Cache merged status if not already set
					if !b.IsMerged {
						b.IsMerged = true
						if debug {
							fmt.Fprintf(os.Stderr, "[DEBUG] Marking branch %s as merged\n", b.Name)
						}
					}
				} else if prData.State == "CLOSED" {
					status.PRState = "CLOSED"
				} else if prData.IsDraft {
					status.PRState = "DRAFT"
				} else {
					status.PRState = "OPEN"
				}
				status.Mergeable = prData.Mergeable
				status.ReviewState = prData.ReviewState

				// Cache PR state on the branch for ezs ls
				b.PRState = status.PRState
			}

			// Process checks data
			if checksErr == nil && checksData != nil {
				if debug {
					fmt.Fprintf(os.Stderr, "[DEBUG] GetPRChecks(%d): state=%s summary=%s\n", b.PRNumber, checksData.State, checksData.Summary)
				}
				status.CIState = checksData.State
				status.CISummary = checksData.Summary
			}
			mu.Unlock()
		}(branch)
	}

	wg.Wait()

	// Save cached PR state for all branches with PR data. Each branch is
	// written atomically so a concurrent `ezs pr update` running in another
	// terminal doesn't lose its writes to a stale in-memory branches map
	// here. We pre-load the cache once for the cheap "did this entry actually
	// change" check — that read can be racy without consequence because the
	// MutateBranchCache call inside the loop is the authoritative write.
	mainWorktree, err := g.GetMainWorktree()
	if err == nil {
		preCache, _ := config.LoadCacheConfig(mainWorktree)
		for _, branch := range s.Branches {
			if branch.PRState == "" {
				continue
			}
			if preCache != nil {
				if bc := preCache.GetBranchCache(branch.Name); bc != nil &&
					bc.PRState == branch.PRState && bc.IsMerged == branch.IsMerged {
					continue
				}
			}
			brName := branch.Name
			brState := branch.PRState
			brMerged := branch.IsMerged
			err := config.MutateBranchCache(mainWorktree, brName, func(bc *config.BranchCache) (*config.BranchCache, error) {
				if bc == nil {
					bc = &config.BranchCache{}
				}
				// Reconcile cached IsMerged with live PR state. Without this,
				// once a branch was cached as merged it stayed merged forever
				// (e.g., a force-pushed-and-reopened PR would never sync again).
				bc.PRState = brState
				bc.IsMerged = brMerged
				return bc, nil
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: failed to save status cache for %s: %v\n", brName, err)
			}
		}
	}

	return statusMap
}

// detectForkRemote inspects a PR and ensures a git remote exists for its head fork.
// Returns the remote name to push to, "" for same-repo PRs, or config.RemoteNoPush
// when push is forbidden (fork disallows maintainer push, no access, etc.).
func detectForkRemote(g *git.Git, gh *github.Client, pr *github.PR) string {
	if gh == nil || pr == nil {
		return ""
	}
	forkInfo, err := gh.GetPRForkInfo(pr.Number)
	if err != nil || !forkInfo.IsFork {
		return ""
	}
	if !forkInfo.MaintainerCanModify {
		ui.Warn(fmt.Sprintf("PR #%d is from a fork (%s) but maintainer push is not allowed — push will be skipped during sync", pr.Number, forkInfo.HeadRepoOwner))
		return config.RemoteNoPush
	}
	currentUser, userErr := github.GetCurrentUser()
	canPush := false
	if userErr == nil {
		canPush = github.CanPushToRepo(forkInfo.HeadRepoOwner, forkInfo.HeadRepoName, currentUser)
	}
	if !canPush {
		ui.Warn(fmt.Sprintf("PR #%d is from a fork (%s) — you don't have push access to the fork, push will be skipped during sync", pr.Number, forkInfo.HeadRepoOwner))
		return config.RemoteNoPush
	}
	if remoteName, _, _ := g.FindRemoteByOwner(forkInfo.HeadRepoOwner); remoteName != "" {
		return remoteName
	}
	remoteName := forkInfo.HeadRepoOwner
	forkURL := fmt.Sprintf("https://github.com/%s/%s.git", forkInfo.HeadRepoOwner, forkInfo.HeadRepoName)
	if addErr := g.AddRemote(remoteName, forkURL); addErr != nil {
		ui.Warn(fmt.Sprintf("Could not add git remote '%s': %v — push will be skipped during sync", remoteName, addErr))
		return config.RemoteNoPush
	}
	ui.Info(fmt.Sprintf("Added git remote '%s' for fork (%s)", remoteName, forkURL))
	if fetchErr := g.FetchRemote(remoteName); fetchErr != nil {
		ui.Warn(fmt.Sprintf("Failed to fetch from '%s': %v", remoteName, fetchErr))
	}
	return remoteName
}

// ResolveBranchRemote returns the git remote that should receive pushes for branchName.
// For branches that were registered as remote (fork) PRs but don't yet have a fork remote
// recorded — typically because they predate fork detection or detection failed at register
// time — this lazily re-runs detection, persists the result, and returns it.
//
// Ambiguous outcomes (GitHub client init failed, no PR found yet) fall back to "origin"
// without persisting anything: those signals are transient and must NOT latch the branch
// into config.RemoteNoPush forever. Only a confirmed fork that forbids maintainer push
// can produce a persisted _nopush sentinel, via detectForkRemote.
func ResolveBranchRemote(g *git.Git, mgr *stack.Manager, branchName string) string {
	if mgr == nil {
		return "origin"
	}
	b := mgr.GetBranch(branchName)
	if b == nil {
		return "origin"
	}
	if b.Remote != "" {
		return b.Remote
	}
	if !b.IsRemote {
		return "origin"
	}
	ui.Warn(fmt.Sprintf("Branch '%s' is tracked from a remote PR but has no fork remote configured — running fork detection now", branchName))
	gh, ghErr := newGitHubClient(g)
	if ghErr != nil {
		ui.Warn(fmt.Sprintf("Could not init GitHub client for fork detection: %v — pushing to origin", ghErr))
		return "origin"
	}
	pr, prErr := gh.GetPRByBranch(branchName)
	if prErr != nil || pr == nil {
		ui.Warn(fmt.Sprintf("No PR found for '%s' yet — pushing to origin", branchName))
		return "origin"
	}
	forkRemote := detectForkRemote(g, gh, pr)
	prURL := b.PRUrl
	if prURL == "" {
		prURL = pr.URL
	}
	persisted := forkRemote
	if persisted == "" {
		// Same-repo PR: legitimate origin push. Persist a sentinel so we don't re-detect.
		persisted = "origin"
	}
	_ = mgr.MarkBranchRemote(branchName, prURL, persisted)
	return persisted
}

// commandExamples maps a command name to a list of example invocations with descriptions.
var commandExamples = map[string][][2]string{
	"commit": {
		{"ezs commit -m \"Add feature\"", "Commit staged changes and auto-sync children"},
		{"ezs commit -a -m \"Fix typo\"", "Stage tracked changes and commit"},
		{"ezs commit --amend --no-edit", "Amend last commit without editing message"},
	},
	"sync": {
		{"ezs sync", "Interactive sync of current stack"},
		{"ezs sync -a", "Auto-sync ALL stacks"},
		{"ezs sync --stats", "Show summary of commits rebased per child"},
		{"ezs sync --squash", "Squash each child's commits into one during sync"},
		{"ezs sync --dry-run", "Preview sync without making changes"},
	},
	"push": {
		{"ezs push", "Push current branch to its remote"},
		{"ezs push -s", "Push every branch in the current stack"},
		{"ezs push --verify", "Run pre-push hook before pushing"},
		{"ezs push --all-remotes", "Push to origin and the configured fork"},
	},
	"pr": {
		{"ezs pr create -t \"Add login\"", "Create a PR with the given title"},
		{"ezs pr create -s", "Create PRs for every branch in the stack"},
		{"ezs pr create --auto", "Let the AI agent draft the PR title and body"},
		{"ezs pr create -s --auto", "AI-draft PRs for every branch in the stack"},
		{"ezs pr --draft-all", "Create all stack PRs as drafts"},
		{"ezs pr merge", "Merge the current branch's PR"},
		{"ezs pr create --force", "Create a fresh PR even if one is already cached"},
		{"ezs pr refresh -s", "Reconcile cached PR state for the whole stack"},
		{"ezs pr unlink", "Forget the cached PR for the current branch"},
	},
	"new": {
		{"ezs new feature-x", "Create a new branch off the current branch"},
		{"ezs new feature-x -p main", "Create a branch off main"},
		{"ezs new feature-x --template fastapi", "Branch from a template in ~/.ezstack/templates"},
	},
	"delete": {
		{"ezs delete feature-x", "Delete a branch and its worktree"},
		{"ezs delete feature-x --cascade", "Delete branch and all orphaned children"},
	},
	"goto": {
		{"ezs goto feature-x", "Jump to a branch worktree"},
		{"ezs goto --search auth", "Fuzzy-find a branch by substring"},
	},
	"agent": {
		{"ezs agent", "Launch the agent on the current stack"},
		{"ezs agent feature \"add JWT auth\"", "Build a feature as stacked branches"},
		{"ezs agent ls", "List tracked AI sessions in this repo"},
		{"ezs agent ls --branch", "Show only the current branch's session"},
		{"ezs agent ls --stack", "Show only the current stack's sessions"},
		{"ezs agent ls --feature", "Show only feature-mode sessions"},
		{"ezs agent ls --json", "Emit tracked sessions as JSON for scripting"},
		{"ezs agent --preset reviewer", "Use a saved prompt preset"},
		{"ezs agent --no-push", "Block any auto-push during the agent run"},
		{"ezs agent --no-resume", "Force a fresh agent session, ignoring any saved one"},
		{"ezs agent -- --debug", "Forward extra flags to the agent CLI"},
		{"ezs agent --dry-run --save-prompt /tmp/p.md", "Write the composed prompt to a file"},
	},
	"config": {
		{"ezs config show", "Show current configuration"},
		{"ezs config set sync_strategy merge", "Set the sync strategy for this repo"},
		{"ezs config export ~/ezs-backup.json", "Backup the global config to a file"},
		{"ezs config import ~/ezs-backup.json", "Restore config from a backup file"},
	},
	"status": {
		{"ezs status", "Show current stack with PR/CI info"},
		{"ezs status --watch", "Auto-refresh status every 5 seconds"},
	},
	"doctor": {
		{"ezs doctor", "Check git/gh/fzf and config validity"},
	},
}

// HasExamplesFlag returns true if args contain --examples in a flag position
// (not as a value to a preceding flag like -m) and prints the registered examples
// for the given command. Returns true when it handled the flag (caller should return).
//
// The naive "is --examples anywhere in args" check is unsafe: `ezs commit -m
// "--examples"` would match on the commit-message value and silently print help
// instead of committing. We walk args left-to-right and skip the argument
// immediately after a flag that takes a string value, so a --examples token
// consumed as a value never triggers the handler.
func HasExamplesFlag(cmdName string, args []string) bool {
	skipNext := false
	for _, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		// Both bare `--examples` and the `--examples=anything` form trigger
		// the help path. The =-form was previously slipping through to the
		// command's own pflag parser as an unknown flag.
		if a == "--examples" || strings.HasPrefix(a, "--examples=") {
			PrintExamples(cmdName)
			return true
		}
		if takesStringValue(a) {
			skipNext = true
		}
	}
	return false
}

// takesStringValue reports whether the given arg is a short- or long-form flag
// that consumes the next positional as its value. It's deliberately liberal:
// any flag listed here protects subsequent `--examples` tokens from being
// misread, and the worst case of over-listing is that a legitimate standalone
// `--examples` gets consumed — which users can always work around with `--`.
// Covers the union of git commit passthrough flags and ezs's own string flags.
func takesStringValue(arg string) bool {
	// `--flag=value` already bundles its value; don't skip the next arg.
	if strings.HasPrefix(arg, "--") && strings.Contains(arg, "=") {
		return false
	}
	switch arg {
	// git commit message/body/metadata flags that ezs commit passes through:
	case "-m", "--message",
		"-F", "--file",
		"-c", "--reedit-message",
		"-C", "--reuse-message",
		"--fixup", "--squash",
		"--author", "--date",
		"--cleanup",
		"--trailer",
		"-S", "--gpg-sign":
		return true
	// ezs flags across commands that take a string argument:
	case "-b", "--branch",
		"-p", "--parent",
		"-w", "--worktree",
		"-s", "--stack",
		"--title",
		"--body",
		"--cmd",
		"--preset",
		"--save-prompt",
		"-t", "--template",
		"--search":
		return true
	}
	return false
}

// PrintExamples prints the example invocations for a command. Output goes to
// stdout so users can pipe/grep it; `--examples` is an explicit info request.
func PrintExamples(cmdName string) {
	examples, ok := commandExamples[cmdName]
	if !ok {
		fmt.Fprintf(os.Stderr, "No examples available for '%s'\n", cmdName)
		return
	}
	fmt.Fprintf(os.Stdout, "%sExamples for 'ezs %s'%s\n\n", ui.Bold, cmdName, ui.Reset)
	for _, ex := range examples {
		fmt.Fprintf(os.Stdout, "  %s# %s%s\n", ui.Gray, ex[1], ui.Reset)
		fmt.Fprintf(os.Stdout, "  %s%s%s\n\n", ui.Cyan, ex[0], ui.Reset)
	}
}

// NavigateToBranch navigates to a branch by cd-ing to its worktree or checking out the branch.
// If a worktreePath is recorded, we cd into it — but we also verify the branch is actually
// checked out there. Secondary git worktrees are pinned to a branch (cd is sufficient), but
// the main worktree is not: a recorded WorktreePath pointing at the main repo means `cd`
// alone leaves whatever branch was previously checked out. In that case we also `git switch`
// to the target branch. Switching to the already-checked-out branch is a no-op, so this is
// safe for secondary worktrees too.
func NavigateToBranch(g *git.Git, branchName, worktreePath string) error {
	if worktreePath != "" {
		wtGit := git.New(worktreePath)
		if cur, err := wtGit.CurrentBranch(); err == nil && cur != branchName {
			if err := wtGit.CheckoutBranch(branchName); err != nil {
				return fmt.Errorf("failed to switch to branch '%s' in %s: %w", branchName, worktreePath, err)
			}
			ui.Success(fmt.Sprintf("Switched to branch '%s'", branchName))
		}
		EmitCd(worktreePath)
		return nil
	}
	if err := g.CheckoutBranch(branchName); err != nil {
		return fmt.Errorf("failed to switch to branch '%s': %w", branchName, err)
	}
	ui.Success(fmt.Sprintf("Switched to branch '%s'", branchName))
	return nil
}
