package commands

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/github"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
)

// IsShellWrapped returns true if ezs is running through the shell wrapper function.
// When true, stdout "cd <path>" will be eval'd by the shell. When false, the tool
// should print the path to stderr and tell the user to cd manually.
func IsShellWrapped() bool {
	return os.Getenv("EZS_SHELL_WRAPPER") == "1"
}

// ShellQuote returns a single-quoted shell string, escaping any embedded single quotes.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// EmitCd outputs a cd command to stdout if running through the shell wrapper,
// otherwise prints a message to stderr telling the user to cd manually.
func EmitCd(path string) {
	if IsShellWrapped() {
		fmt.Printf("cd %s\n", ShellQuote(path))
	} else {
		ui.Info(fmt.Sprintf("Run: cd %s", ShellQuote(path)))
		ui.Info("Tip: Add to your shell config for automatic cd: eval \"$(ezs --shell-init)\"")
	}
}

// savePRToCache saves a single branch's PR number and URL to the cache.
func savePRToCache(cacheDir, branchName string, prNum int, prURL string) {
	cache, err := config.LoadCacheConfig(cacheDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load cache for PR save: %v\n", err)
		return
	}
	bc := cache.GetBranchCache(branchName)
	if bc == nil {
		bc = &config.BranchCache{}
	}
	bc.PRNumber = prNum
	bc.PRUrl = prURL
	cache.SetBranchCache(branchName, bc)
	if err := cache.Save(cacheDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save PR cache: %v\n", err)
	}
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
		if err := g.PushForce(remote); err != nil {
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
			if err := g.PushForce(remote); err != nil {
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
		if err := g.Push(false, remote); err != nil {
			// If regular push fails (e.g., diverged history from prior rebase), offer force push
			ui.Warn(fmt.Sprintf("Push failed: %v", err))
			if ui.ConfirmTUI(fmt.Sprintf("Force push %s (--force-with-lease) to %s", branchName, remote)) {
				if err := g.PushForce(remote); err != nil {
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
			if err := g.Push(false, remote); err != nil {
				// Fall back to force push if regular push fails
				ui.Warn(fmt.Sprintf("Push failed: %v. Trying force push...", err))
				if err := g.PushForce(remote); err != nil {
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
			r.branch.PRNumber = r.pr.Number
			r.branch.PRUrl = r.pr.URL
			discoveredPRs = true
		}
	}

	if discoveredPRs {
		mainWorktree := getMainWorktreePath(g)
		cache, err := config.LoadCacheConfig(mainWorktree)
		if err == nil {
			for _, branch := range s.Branches {
				if branch.PRNumber > 0 {
					bc := cache.GetBranchCache(branch.Name)
					if bc == nil {
						bc = &config.BranchCache{}
					}
					bc.PRNumber = branch.PRNumber
					bc.PRUrl = branch.PRUrl
					cache.SetBranchCache(branch.Name, bc)
				}
			}
			if err := cache.Save(mainWorktree); err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: failed to save PR cache: %v\n", err)
			}
		}
	}

	return gh
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
			parentRef := rootBase
			if g.RemoteBranchExists(rootBase) {
				parentRef = "origin/" + rootBase
			}
			branchRef := s.Root
			if g.RemoteBranchExists(s.Root) {
				branchRef = "origin/" + s.Root
			}
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
			// Use origin/ refs when available for both parent and branch
			// to get accurate stats matching what PRs show
			parentRef := b.Parent
			if g.RemoteBranchExists(b.Parent) {
				parentRef = "origin/" + b.Parent
			}
			branchRef := b.Name
			if g.RemoteBranchExists(b.Name) {
				branchRef = "origin/" + b.Name
			}
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

	// Save cached PR state for all branches with PR data
	mainWorktree, err := g.GetMainWorktree()
	if err == nil {
		cache, err := config.LoadCacheConfig(mainWorktree)
		if err == nil {
			changed := false
			for _, branch := range s.Branches {
				if branch.PRState == "" {
					continue
				}
				bc := cache.GetBranchCache(branch.Name)
				if bc == nil {
					bc = &config.BranchCache{}
				}
				if bc.PRState != branch.PRState || (branch.IsMerged && !bc.IsMerged) {
					bc.PRState = branch.PRState
					if branch.IsMerged {
						bc.IsMerged = true
					}
					cache.SetBranchCache(branch.Name, bc)
					changed = true
				}
			}
			if changed {
				if err := cache.Save(mainWorktree); err != nil {
					fmt.Fprintf(os.Stderr, "  Warning: failed to save status cache: %v\n", err)
				}
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
// time — this lazily re-runs detection, persists the result, and returns it. Returns
// config.RemoteNoPush when detection fails or push is forbidden so callers will skip the
// push instead of silently sending it to origin.
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
		ui.Warn(fmt.Sprintf("Could not init GitHub client for fork detection: %v — push will be skipped", ghErr))
		_ = mgr.MarkBranchRemote(branchName, b.PRUrl, config.RemoteNoPush)
		return config.RemoteNoPush
	}
	pr, prErr := gh.GetPRByBranch(branchName)
	if prErr != nil || pr == nil {
		ui.Warn(fmt.Sprintf("Could not look up PR for '%s': %v — push will be skipped", branchName, prErr))
		_ = mgr.MarkBranchRemote(branchName, b.PRUrl, config.RemoteNoPush)
		return config.RemoteNoPush
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

// NavigateToBranch navigates to a branch by cd-ing to its worktree or checking out the branch.
func NavigateToBranch(g *git.Git, branchName, worktreePath string) error {
	if worktreePath != "" {
		EmitCd(worktreePath)
		return nil
	}
	if err := g.CheckoutBranch(branchName); err != nil {
		return fmt.Errorf("failed to switch to branch '%s': %w", branchName, err)
	}
	ui.Success(fmt.Sprintf("Switched to branch '%s'", branchName))
	return nil
}
