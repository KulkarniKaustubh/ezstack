package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/github"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
	"github.com/spf13/pflag"
)


// List lists all stacks and branches
func List(args []string) error {
	fs := pflag.NewFlagSet("list", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sList all stacks and branches%s

%sUSAGE%s
    ezs list [options]
    ezs ls [options]

%sOPTIONS%s
    -a, --all     Show all stacks
    --json        Output as JSON (machine-readable)
    -d, --debug   Show debug output
    -h, --help    Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	all := fs.BoolP("all", "a", false, "Show all stacks")
	jsonFlag := fs.Bool("json", false, "Output as JSON")
	debug := fs.BoolP("debug", "d", false, "Show debug output")
	helpFlag := fs.BoolP("help", "h", false, "Show help")

	if err := fs.Parse(args); err != nil {
		if err == pflag.ErrHelp {
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
	currentBranch, _ := g.CurrentBranch()

	if *debug {
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
		if *jsonFlag {
			fmt.Println("[]")
			return nil
		}
		ui.Info("No stacks found. Create one with: ezs new <branch-name>")
		return nil
	}

	var stacksToShow []*config.Stack
	currentStack, _, csErr := mgr.GetCurrentStack()
	if *all || csErr != nil {
		stacksToShow = stacks
	} else {
		stacksToShow = []*config.Stack{currentStack}
	}

	if *jsonFlag {
		return printStacksJSON(stacksToShow, currentBranch)
	}

	for _, s := range stacksToShow {
		ui.PrintStack(s, currentBranch, false, nil)
	}
	return nil
}

// stackJSON represents a stack in JSON output
type stackJSON struct {
	Hash     string       `json:"hash"`
	Name     string       `json:"name,omitempty"`
	Root     string       `json:"root"`
	Branches []branchJSON `json:"branches"`
}

// branchJSON represents a branch in JSON output
type branchJSON struct {
	Name         string `json:"name"`
	Parent       string `json:"parent"`
	IsMerged     bool   `json:"is_merged"`
	IsCurrent    bool   `json:"is_current"`
	PRNumber     int    `json:"pr_number,omitempty"`
	PRUrl        string `json:"pr_url,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
}

// printStacksJSON outputs stacks as JSON to stdout
func printStacksJSON(stacks []*config.Stack, currentBranch string) error {
	result := make([]stackJSON, 0, len(stacks))
	for _, s := range stacks {
		sj := stackJSON{
			Hash:     s.Hash,
			Name:     s.Name,
			Root:     s.Root,
			Branches: make([]branchJSON, 0, len(s.Branches)),
		}
		for _, b := range s.Branches {
			sj.Branches = append(sj.Branches, branchJSON{
				Name:         b.Name,
				Parent:       b.Parent,
				IsMerged:     b.IsMerged,
				IsCurrent:    b.Name == currentBranch,
				PRNumber:     b.PRNumber,
				PRUrl:        b.PRUrl,
				WorktreePath: b.WorktreePath,
			})
		}
		result = append(result, sj)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// Status shows the status of current stack or all stacks with PR and CI info
func Status(args []string) error {
	fs := pflag.NewFlagSet("status", pflag.ContinueOnError)
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
	helpFlag := fs.BoolP("help", "h", false, "Show help")
	all := fs.BoolP("all", "a", false, "Show all stacks")
	debug := fs.BoolP("debug", "d", false, "Show debug output")
	if err := fs.Parse(args); err != nil {
		if err == pflag.ErrHelp {
			return nil
		}
		return err
	}
	if *helpFlag {
		fs.Usage()
		return nil
	}
	ghAvailable := github.CheckAuth() == nil

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
		if ghAvailable {
			spinner := ui.NewDelayedSpinner("Fetching PR and CI status...")
			spinner.Start()
			statusMaps := make([]map[string]*ui.BranchStatus, len(stacks))
			var wg sync.WaitGroup

			for i, s := range stacks {
				wg.Add(1)
				go func(idx int, stack *config.Stack) {
					defer wg.Done()
					statusMaps[idx] = fetchBranchStatuses(g, stack, *debug)
				}(i, s)
			}

			wg.Wait()
			spinner.Stop()

			for i, s := range stacks {
				ui.PrintStack(s, currentBranch, true, statusMaps[i])
			}
		} else {
			for _, s := range stacks {
				ui.PrintStack(s, currentBranch, false, nil)
			}
			ui.Warn("GitHub CLI not authenticated. Run 'gh auth login' for PR/CI status.")
		}
	}

	currentStack, branch, err := mgr.GetCurrentStack()
	if *all || err != nil {
		printAllStacksWithStatus()
		if ghAvailable {
			offerFullyMergedStackCleanup(mgr, stacks)
		}
		return nil
	}

	var statusMap map[string]*ui.BranchStatus
	if ghAvailable {
		spinner := ui.NewDelayedSpinner("Fetching PR and CI status...")
		spinner.Start()
		statusMap = fetchBranchStatuses(g, currentStack, *debug)
		spinner.Stop()
	}

	ui.PrintStack(currentStack, currentBranch, ghAvailable, statusMap)

	parentRef := branch.Parent
	if g.RemoteBranchExists(branch.Parent) {
		parentRef = "origin/" + branch.Parent
	}
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

	if ghAvailable {
		offerFullyMergedStackCleanup(mgr, []*config.Stack{currentStack})
	}

	if !ghAvailable {
		ui.Warn("GitHub CLI not authenticated. Run 'gh auth login' for PR/CI status.")
	}

	return nil
}

// offerFullyMergedStackCleanup checks each stack whose branches are all marked merged
// (updated in-memory by fetchBranchStatuses) and offers to delete them.
func offerFullyMergedStackCleanup(mgr *stack.Manager, stacks []*config.Stack) {
	for _, s := range stacks {
		if len(s.Branches) == 0 || s.DeleteDeclined {
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
		ui.Info(fmt.Sprintf("Stack '%s' is fully merged", s.DisplayName()))
		if ui.ConfirmTUI(fmt.Sprintf("Clean up stack '%s' (delete worktrees, branches, and tracking)?", s.DisplayName())) {
			if err := mgr.DeleteStack(s.Hash); err != nil {
				ui.Warn(fmt.Sprintf("Failed to clean up stack '%s': %v", s.DisplayName(), err))
			} else {
				ui.Success(fmt.Sprintf("Removed fully merged stack '%s'", s.DisplayName()))
			}
		} else {
			mgr.DeclineStackDelete(s.Hash)
		}
	}
}
