package commands

import (
	"fmt"
	"os"
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
		return
	}
	bc := cache.GetBranchCache(branchName)
	if bc == nil {
		bc = &config.BranchCache{}
	}
	bc.PRNumber = prNum
	bc.PRUrl = prURL
	cache.SetBranchCache(branchName, bc)
	cache.Save(cacheDir)
}

// updateStackDescriptions updates PR descriptions for all PRs in the given stack.
func updateStackDescriptions(gh *github.Client, s *config.Stack, activeBranch string) error {
	ui.Info("Updating PR stack descriptions...")
	return gh.UpdateStackDescription(s, activeBranch)
}

// updatePRMetadata updates base branches and stack descriptions for all PRs in the stack.
// Called after pushes and stack mutations to keep PR metadata in sync.
func updatePRMetadata(gh *github.Client, mgr *stack.Manager, s *config.Stack, currentBranch *config.Branch) {
	// Update base branches for all PRs in the stack
	for _, b := range s.Branches {
		if b.PRNumber == 0 {
			continue
		}
		pr, err := gh.GetPR(b.PRNumber)
		if err != nil {
			continue
		}
		if pr.Base != b.Parent {
			if err := gh.UpdatePRBase(b.PRNumber, b.Parent); err != nil {
				ui.Warn(fmt.Sprintf("Failed to update base branch for PR #%d: %v", b.PRNumber, err))
			}
		}
	}

	// Update stack descriptions
	activeName := ""
	if currentBranch != nil {
		activeName = currentBranch.Name
	}
	if err := gh.UpdateStackDescription(s, activeName); err != nil {
		ui.Warn(fmt.Sprintf("Failed to update stack descriptions: %v", err))
	}
}

// OfferForcePush prompts the user to force push a branch with --force-with-lease
// Returns true if push was successful, false otherwise
func OfferForcePush(branchName, worktreePath string) bool {
	g := git.New(worktreePath)

	needsPush, err := g.IsLocalAheadOfOrigin(branchName)
	if err != nil {
		ui.Warn(fmt.Sprintf("Could not check if push is needed: %v", err))
		needsPush = true
	}

	if !needsPush {
		return true
	}

	fmt.Fprintln(os.Stderr)
	ui.Warn("Force push required to update remote branch")
	if ui.ConfirmTUI(fmt.Sprintf("Force push %s (--force-with-lease)", branchName)) {
		ui.Info("Pushing...")
		if err := g.PushForce(); err != nil {
			ui.Error(fmt.Sprintf("Push failed: %v. Check your network connection and remote access", err))
			return false
		}
		ui.Success("Pushed successfully")
		return true
	}

	return false
}

// OfferForcePushMultiple prompts the user to force push multiple branches
// Returns the number of successfully pushed branches
func OfferForcePushMultiple(branches []string, getBranchWorktree func(string) string) int {
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

		g := git.New(worktreePath)
		needsPush, err := g.IsLocalAheadOfOrigin(branchName)
		if err != nil || !needsPush {
			continue
		}

		if ui.ConfirmTUI(fmt.Sprintf("Force push %s (--force-with-lease)", branchName)) {
			ui.Info(fmt.Sprintf("Pushing %s...", branchName))
			if err := g.PushForce(); err != nil {
				ui.Error(fmt.Sprintf("Push failed for %s: %v. Check remote access or try: git push --force-with-lease", branchName, err))
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
// Returns the selected PR info.
func selectAndRegisterRemotePR(g *git.Git, mgr *stack.Manager) (github.OpenPR, error) {
	gh, err := newGitHubClient(g)
	if err != nil {
		return github.OpenPR{}, err
	}

	ui.Info("Fetching open PRs...")
	openPRs, err := gh.ListOpenPRs()
	if err != nil {
		return github.OpenPR{}, fmt.Errorf("failed to list open PRs: %w", err)
	}

	if len(openPRs) == 0 {
		return github.OpenPR{}, fmt.Errorf("no open PRs found in this repository")
	}

	prOptions := make([]string, len(openPRs))
	for i, pr := range openPRs {
		prOptions[i] = fmt.Sprintf("#%d %s - %s (%s)", pr.Number, pr.Branch, pr.Title, pr.Author)
	}

	selectedIdx, err := ui.SelectOption(prOptions, "Select PR to use as stack base")
	if err != nil {
		return github.OpenPR{}, err
	}
	selectedPR := openPRs[selectedIdx]

	printRemoteBranchWarning()

	ui.Info("Fetching remote branch...")
	if err := g.Fetch(); err != nil {
		return github.OpenPR{}, fmt.Errorf("failed to fetch: %w", err)
	}

	if err := mgr.RegisterRemoteBranch(selectedPR.Branch, selectedPR.Number, selectedPR.URL); err != nil {
		return github.OpenPR{}, fmt.Errorf("failed to register remote branch: %w", err)
	}

	return selectedPR, nil
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

	discoveredPRs := false
	ghAccessWarningShown := false
	mainWorktree := getMainWorktreePath(g)

	for _, branch := range s.Branches {
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Checking branch %s (PRNumber=%d)\n", branch.Name, branch.PRNumber)
		}

		if branch.PRNumber == 0 {
			pr, err := gh.GetPRByBranch(branch.Name)
			if err != nil {
				if debug {
					fmt.Fprintf(os.Stderr, "[DEBUG] GetPRByBranch(%s) error: %v\n", branch.Name, err)
				}
				if !ghAccessWarningShown {
					errStr := err.Error()
					if strings.Contains(errStr, "cannot access repository") ||
						strings.Contains(errStr, "authentication") {
						ui.Warn(errStr)
						ghAccessWarningShown = true
					}
				}
				continue
			}
			if pr != nil {
				if debug {
					fmt.Fprintf(os.Stderr, "[DEBUG] Found PR #%d for branch %s\n", pr.Number, branch.Name)
				}
				branch.PRNumber = pr.Number
				branch.PRUrl = pr.URL
				discoveredPRs = true
			}
		}
	}

	if discoveredPRs {
		cache, _ := config.LoadCacheConfig(mainWorktree)
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
		cache.Save(mainWorktree)
	}

	return gh
}

// fetchBranchStatuses fetches PR and CI status for all branches in a stack (used by ezs status)
// Also caches merged status to the config when detected
func fetchBranchStatuses(g *git.Git, s *config.Stack, debug bool) map[string]*ui.BranchStatus {
	statusMap := make(map[string]*ui.BranchStatus)

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] fetchBranchStatuses for stack %s with %d branches\n", s.Hash, len(s.Branches))
	}

	gh := discoverAndCachePRs(g, s, debug)
	if gh == nil {
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] gh client is nil, returning empty statusMap\n")
		}
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

			status := &ui.BranchStatus{}

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

			// Process PR data
			if prErr == nil {
				if prData.Merged {
					status.PRState = "MERGED"
					// Cache merged status if not already set; the read of b.IsMerged
					// must be inside the lock to avoid a data race with other goroutines.
					mu.Lock()
					if !b.IsMerged {
						b.IsMerged = true
						if debug {
							fmt.Fprintf(os.Stderr, "[DEBUG] Marking branch %s as merged\n", b.Name)
						}
					}
					mu.Unlock()
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
				mu.Lock()
				b.PRState = status.PRState
				mu.Unlock()
			}

			// Process checks data
			if checksErr == nil && checksData != nil {
				if debug {
					fmt.Fprintf(os.Stderr, "[DEBUG] GetPRChecks(%d): state=%s summary=%s\n", b.PRNumber, checksData.State, checksData.Summary)
				}
				status.CIState = checksData.State
				status.CISummary = checksData.Summary
			}

			mu.Lock()
			statusMap[b.Name] = status
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
				cache.Save(mainWorktree)
			}
		}
	}

	return statusMap
}
