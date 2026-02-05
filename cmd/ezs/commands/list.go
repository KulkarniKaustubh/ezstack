package commands

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ezstack/ezstack/internal/config"
	"github.com/ezstack/ezstack/internal/git"
	"github.com/ezstack/ezstack/internal/github"
	"github.com/ezstack/ezstack/internal/stack"
	"github.com/ezstack/ezstack/internal/ui"
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
	// Long flags
	all := fs.Bool("all", false, "Show all stacks")
	debug := fs.Bool("debug", false, "Show debug output")
	// Short flags
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

	// Merge short and long flags
	if *allShort {
		*all = true
	}
	if *debugShort {
		*debug = true
	}
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

	// Helper to print stack (discovers PRs but doesn't fetch CI status)
	printStack := func(s *config.Stack) {
		discoverAndCachePRs(g, s)
		ui.PrintStackWithStatus(s, currentBranch, nil)
	}

	if *all {
		for _, s := range stacks {
			printStack(s)
		}
	} else {
		// Show only current stack or all if not in a stack
		currentStack, _, err := mgr.GetCurrentStack()
		if err != nil {
			// Not in a stack, show all
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
    When not in a stack, shows all stacks with their PR statuses.

%sOPTIONS%s
    -h, --help    Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
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

	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		// Not in a stack - show all stacks like ezs ls does
		stacks := mgr.ListStacks()
		if len(stacks) == 0 {
			ui.Info("No stacks found. Create one with: ezs new <branch-name>")
			return nil
		}

		for _, s := range stacks {
			statusMap := fetchBranchStatuses(g, s)
			ui.PrintStackWithStatus(s, currentBranch, statusMap)
		}
		return nil
	}

	// Fetch PR and CI status for all branches
	statusMap := fetchBranchStatuses(g, currentStack)

	ui.PrintStackWithStatus(currentStack, currentBranch, statusMap)

	// Show commits unique to this branch
	commits, err := g.GetCommitsBetween(branch.Parent, currentBranch)
	if err == nil && len(commits) > 0 {
		fmt.Fprintf(os.Stderr, "%s%sCommits in this branch:%s\n", ui.Bold, ui.Cyan, ui.Reset)
		for _, c := range commits {
			fmt.Fprintf(os.Stderr, "  %s%.7s%s %s\n", ui.Yellow, c.Hash, ui.Reset, c.Subject)
		}
		fmt.Fprintln(os.Stderr)
	}

	// Check for rebase status
	inRebase, _ := g.IsRebaseInProgress()
	if inRebase {
		ui.Warn("Rebase in progress! Resolve conflicts and run: git rebase --continue")
	}

	return nil
}

// discoverAndCachePRs discovers PRs from GitHub for branches that don't have PR numbers cached
// and saves them to the config. Returns a GitHub client for further use (or nil if unavailable).
func discoverAndCachePRs(g *git.Git, s *config.Stack) *github.Client {
	// Try to get GitHub client
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

	// Track if we discovered any new PRs so we can save the config
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

		// If PRNumber is 0, try to discover PR from GitHub
		if branch.PRNumber == 0 {
			pr, err := gh.GetPRForBranch(branch.Name)
			if err != nil {
				if debugMode {
					fmt.Fprintf(os.Stderr, "[DEBUG] GetPRForBranch(%s) error: %v\n", branch.Name, err)
				}
				// Show warning once if we can't access GitHub
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
				// Found a PR for this branch - update the branch info
				branch.PRNumber = pr.Number
				branch.PRUrl = pr.URL
				discoveredPRs = true
			}
		}
	}

	// Save discovered PRs to config
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
func fetchBranchStatuses(g *git.Git, s *config.Stack) map[string]*ui.BranchStatus {
	statusMap := make(map[string]*ui.BranchStatus)

	// First discover and cache any missing PRs
	gh := discoverAndCachePRs(g, s)
	if gh == nil {
		return statusMap
	}

	// Now fetch detailed status for each branch with a PR
	for _, branch := range s.Branches {
		if branch.PRNumber == 0 {
			continue
		}

		status := &ui.BranchStatus{}

		// Get PR info
		pr, err := gh.GetPR(branch.PRNumber)
		if err == nil {
			if pr.Merged {
				status.PRState = "MERGED"
			} else if pr.State == "CLOSED" {
				status.PRState = "CLOSED"
			} else if pr.IsDraft {
				status.PRState = "DRAFT"
			} else {
				status.PRState = "OPEN"
			}
			status.Mergeable = pr.Mergeable
			status.ReviewState = pr.ReviewState
		}

		// Get CI status
		checks, err := gh.GetPRChecks(branch.PRNumber)
		if err == nil && checks != nil {
			status.CIState = checks.State
			status.CISummary = checks.Summary
		}

		statusMap[branch.Name] = status
	}

	return statusMap
}
