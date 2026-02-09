package commands

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/github"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
)

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
			ui.Error(fmt.Sprintf("Push failed: %v", err))
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
				ui.Error(fmt.Sprintf("Push failed for %s: %v", branchName, err))
			} else {
				ui.Success(fmt.Sprintf("Pushed %s successfully", branchName))
				pushed++
			}
		}
	}

	return pushed
}

// discoverAndCachePRs discovers PRs from GitHub for branches that don't have PR numbers cached
// and saves them to the config. Returns a GitHub client for further use (or nil if unavailable).
func discoverAndCachePRs(g *git.Git, s *config.Stack) *github.Client {
	remoteURL, err := g.GetRemote("origin")
	if err != nil {
		if debugMode {
			fmt.Fprintf(os.Stderr, "[DEBUG] discoverAndCachePRs: GetRemote error: %v\n", err)
		}
		return nil
	}

	if debugMode {
		fmt.Fprintf(os.Stderr, "[DEBUG] discoverAndCachePRs: remoteURL=%s\n", remoteURL)
	}

	gh, err := github.NewClient(remoteURL)
	if err != nil {
		if debugMode {
			fmt.Fprintf(os.Stderr, "[DEBUG] discoverAndCachePRs: NewClient error: %v\n", err)
		}
		return nil
	}

	discoveredPRs := false
	ghAccessWarningShown := false
	mainWorktree, _ := g.GetMainWorktree()
	if mainWorktree == "" {
		mainWorktree, _ = os.Getwd()
	}

	for _, branch := range s.Branches {
		if debugMode {
			fmt.Fprintf(os.Stderr, "[DEBUG] Checking branch %s (PRNumber=%d)\n", branch.Name, branch.PRNumber)
		}

		if branch.PRNumber == 0 {
			pr, err := gh.GetPRByBranch(branch.Name)
			if err != nil {
				if debugMode {
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
				if debugMode {
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
func fetchBranchStatuses(g *git.Git, s *config.Stack) map[string]*ui.BranchStatus {
	statusMap := make(map[string]*ui.BranchStatus)

	if debugMode {
		fmt.Fprintf(os.Stderr, "[DEBUG] fetchBranchStatuses for stack %s with %d branches\n", s.Name, len(s.Branches))
	}

	gh := discoverAndCachePRs(g, s)
	if gh == nil {
		if debugMode {
			fmt.Fprintf(os.Stderr, "[DEBUG] gh client is nil, returning empty statusMap\n")
		}
		return statusMap
	}

	// Track if we discovered any newly merged branches to save config
	var discoveredMerged bool
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Semaphore to limit concurrent gh CLI calls
	sem := make(chan struct{}, 10)

	for _, branch := range s.Branches {
		if debugMode {
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
					// Cache merged status if not already set
					if !b.IsMerged {
						mu.Lock()
						b.IsMerged = true
						discoveredMerged = true
						mu.Unlock()
						if debugMode {
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
			}

			// Process checks data
			if checksErr == nil && checksData != nil {
				if debugMode {
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

	// Save config if we discovered newly merged branches
	if discoveredMerged {
		mainWorktree, err := g.GetMainWorktree()
		if err == nil {
			cache, err := config.LoadCacheConfig(mainWorktree)
			if err == nil {
				for _, branch := range s.Branches {
					if branch.IsMerged {
						bc := cache.GetBranchCache(branch.Name)
						if bc == nil {
							bc = &config.BranchCache{}
						}
						bc.IsMerged = true
						cache.SetBranchCache(branch.Name, bc)
					}
				}
				cache.Save(mainWorktree)
			}
		}
	}

	return statusMap
}
