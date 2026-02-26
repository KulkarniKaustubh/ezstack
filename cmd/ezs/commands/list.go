package commands

import (
	"flag"
	"fmt"
	"os"
	"sync"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
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
			// Current branch is not part of any stack
			ui.Info("Current branch is not part of any stack. Use -a to show all stacks.")
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
		offerFullyMergedStackCleanup(mgr, stacks)
		return nil
	}

	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		// Current branch is not part of any stack
		ui.Info("Current branch is not part of any stack. Use -a to show all stacks.")
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

	offerFullyMergedStackCleanup(mgr, []*config.Stack{currentStack})

	return nil
}

// offerFullyMergedStackCleanup checks each stack whose branches are all marked merged
// (updated in-memory by fetchBranchStatuses) and offers to delete them.
func offerFullyMergedStackCleanup(mgr *stack.Manager, stacks []*config.Stack) {
	for _, s := range stacks {
		if len(s.Branches) == 0 {
			continue
		}
		allMerged := true
		for _, b := range s.Branches {
			if !b.IsMerged {
				allMerged = false
				break
			}
		}
		if !allMerged {
			continue
		}
		fmt.Fprintln(os.Stderr)
		ui.Info(fmt.Sprintf("Stack '#%s' is fully merged", s.Hash))
		if ui.ConfirmTUI(fmt.Sprintf("Clean up stack '#%s' (delete worktrees, branches, and tracking)?", s.Hash)) {
			if err := mgr.DeleteStack(s.Hash); err != nil {
				ui.Warn(fmt.Sprintf("Failed to clean up stack '#%s': %v", s.Hash, err))
			} else {
				ui.Success(fmt.Sprintf("Removed fully merged stack '#%s'", s.Hash))
			}
		}
	}
}
