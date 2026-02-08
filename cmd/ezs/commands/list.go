package commands

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/github"
	"github.com/KulkarniKaustubh/ezstack/internal/helpers"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
)

// debugMode is set by --debug flag to show verbose output
var debugMode bool

// List lists all stacks and branches
func List(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sList all stacks and branches%s

%sUSAGE%s
    ezs list [options]
    ezs ls [options]

%sOPTIONS%s
    -a, --all     Show all stacks
    -d, --debug   Show debug output
    -h, --help    Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	all := fs.Bool("all", false, "Show all stacks")
	debug := fs.Bool("debug", false, "Show debug output")
	allShort := fs.Bool("a", false, "Show all stacks (short)")
	debugShort := fs.Bool("d", false, "Show debug output (short)")
	helpFlag := fs.Bool("h", false, "Show help")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if *helpFlag {
		fs.Usage()
		return nil
	}

	helpers.MergeFlags(allShort, all, debugShort, debug)
	debugMode = *debug

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)
	currentBranch, _ := g.CurrentBranch()

	if debugMode {
		remoteURL, err := g.GetRemote("origin")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[DEBUG] GetRemote error: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG] Remote URL: %s\n", remoteURL)
		}
	}

	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	stacks := mgr.ListStacks()
	if len(stacks) == 0 {
		ui.Info("No stacks found. Create one with: ezs new <branch-name>")
		return nil
	}

	printStack := func(s *config.Stack) {
		ui.PrintStack(s, currentBranch, false, nil)
	}

	if *all {
		for _, s := range stacks {
			printStack(s)
		}
	} else {
		currentStack, _, err := mgr.GetCurrentStack()
		if err != nil {
			// Current branch is not part of any stack - show all stacks
			for _, s := range stacks {
				printStack(s)
			}
		} else {
			printStack(currentStack)
		}
	}

	return nil
}

// Status shows the status of current stack or all stacks with PR and CI info
func Status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sShow status of current stack with PR and CI info%s

%sUSAGE%s
    ezs status [options]

%sDESCRIPTION%s
    Shows the current stack with PR and CI status for each branch.
    When not in a stack, shows a message. Use -a to see all stacks.

%sOPTIONS%s
    -a, --all     Show all stacks
    -d, --debug   Show debug output
    -h, --help    Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	helpFlag := fs.Bool("h", false, "Show help")
	all := fs.Bool("all", false, "Show all stacks")
	allShort := fs.Bool("a", false, "Show all stacks (short)")
	debug := fs.Bool("debug", false, "Show debug output")
	debugShort := fs.Bool("d", false, "Show debug output (short)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if *helpFlag {
		fs.Usage()
		return nil
	}
	helpers.MergeFlags(allShort, all, debugShort, debug)
	debugMode = *debug

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)
	currentBranch, err := g.CurrentBranch()
	if err != nil {
		return err
	}

	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	stacks := mgr.ListStacks()
	if len(stacks) == 0 {
		ui.Info("No stacks found. Create one with: ezs new <branch-name>")
		return nil
	}

	// Helper to fetch and print all stacks with status
	printAllStacksWithStatus := func() {
		spinner := ui.NewDelayedSpinner("Fetching PR and CI status...")
		spinner.Start()
		statusMaps := make([]map[string]*ui.BranchStatus, len(stacks))
		var wg sync.WaitGroup

		for i, s := range stacks {
			wg.Add(1)
			go func(idx int, stack *config.Stack) {
				defer wg.Done()
				statusMaps[idx] = fetchBranchStatuses(g, stack)
			}(i, s)
		}

		wg.Wait()
		spinner.Stop()

		for i, s := range stacks {
			ui.PrintStack(s, currentBranch, true, statusMaps[i])
		}
	}

	if *all {
		printAllStacksWithStatus()
		return nil
	}

	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		// Current branch is not part of any stack - show all stacks
		printAllStacksWithStatus()
		return nil
	}

	spinner := ui.NewDelayedSpinner("Fetching PR and CI status...")
	spinner.Start()
	statusMap := fetchBranchStatuses(g, currentStack)
	spinner.Stop()

	ui.PrintStack(currentStack, currentBranch, true, statusMap)

	parentRef := "origin/" + branch.Parent
	commits, err := g.GetCommitsBetween(parentRef, currentBranch)
	if err == nil && len(commits) > 0 {
		fmt.Fprintf(os.Stderr, "%s%sCommits in this branch:%s\n", ui.Bold, ui.Cyan, ui.Reset)
		for _, c := range commits {
			fmt.Fprintf(os.Stderr, "  %s%.7s%s %s\n", ui.Yellow, c.Hash, ui.Reset, c.Subject)
		}
		fmt.Fprintln(os.Stderr)
	}

	inRebase, _ := g.IsRebaseInProgress()
	if inRebase {
		ui.Warn("Rebase in progress! Resolve conflicts and run: git rebase --continue")
	}

	return nil
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
		stackCfg, err := config.LoadStackConfig(mainWorktree)
		if err == nil {
			for _, existingStack := range stackCfg.Stacks {
				if existingStack.Name == s.Name {
					for _, b := range existingStack.Branches {
						for _, updated := range s.Branches {
							if b.Name == updated.Name && updated.PRNumber > 0 {
								b.PRNumber = updated.PRNumber
								b.PRUrl = updated.PRUrl
							}
						}
					}
				}
			}
			stackCfg.Save(mainWorktree)
		}
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
			stackCfg, err := config.LoadStackConfig(mainWorktree)
			if err == nil {
				for _, existingStack := range stackCfg.Stacks {
					if existingStack.Name == s.Name {
						for _, b := range existingStack.Branches {
							for _, updated := range s.Branches {
								if b.Name == updated.Name && updated.IsMerged {
									b.IsMerged = true
								}
							}
						}
					}
				}
				stackCfg.Save(mainWorktree)
			}
		}
	}

	return statusMap
}
