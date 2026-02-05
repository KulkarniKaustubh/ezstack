package commands

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ezstack/ezstack/internal/config"
	"github.com/ezstack/ezstack/internal/git"
	"github.com/ezstack/ezstack/internal/github"
	"github.com/ezstack/ezstack/internal/helpers"
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
		ui.PrintStackWithStatus(s, currentBranch, nil)
	}

	if *all {
		for _, s := range stacks {
			printStack(s)
		}
	} else {
		currentStack, _, err := mgr.GetCurrentStack()
		if err != nil {
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
    -d, --debug   Show debug output
    -h, --help    Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	helpFlag := fs.Bool("h", false, "Show help")
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
	helpers.MergeFlags(debugShort, debug)
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

	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
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

	spinner := ui.NewDelayedSpinner("Fetching PR and CI status...")
	spinner.Start()
	statusMap := fetchBranchStatuses(g, currentStack)
	spinner.Stop()

	ui.PrintStackWithStatus(currentStack, currentBranch, statusMap)

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

	for _, branch := range s.Branches {
		if debugMode {
			fmt.Fprintf(os.Stderr, "[DEBUG] branch %s PRNumber=%d\n", branch.Name, branch.PRNumber)
		}
		if branch.PRNumber == 0 {
			continue
		}

		status := &ui.BranchStatus{}

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

		checks, err := gh.GetPRChecks(branch.PRNumber)
		if debugMode {
			fmt.Fprintf(os.Stderr, "[DEBUG] GetPRChecks(%d): state=%s summary=%s err=%v\n", branch.PRNumber, checks.State, checks.Summary, err)
		}
		if err == nil && checks != nil {
			status.CIState = checks.State
			status.CISummary = checks.Summary
		}

		statusMap[branch.Name] = status
	}

	return statusMap
}
