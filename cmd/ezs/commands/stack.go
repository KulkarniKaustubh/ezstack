package commands

import (
	"flag"
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/github"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
)

// Stack adds a branch to a stack (alias for reparent with standalone branch)
func Stack(args []string) error {
	fs := flag.NewFlagSet("stack", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sAdd a branch to a stack%s

%sUSAGE%s
    ezs stack [branch] [parent] [options]

%sOPTIONS%s
    -b, --branch <name>     Branch to add to stack
    -p, --parent <name>     Parent branch in the stack
    -h, --help              Show this help message

%sDESCRIPTION%s
    Adds an untracked branch/worktree to an existing stack by setting its parent.
    This is equivalent to 'ezs reparent' for standalone branches.

%sEXAMPLES%s
    ezs stack                         Interactive mode
    ezs stack my-branch feature-a     Add my-branch under feature-a
    ezs stack -b my-branch -p main    Add my-branch under main
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	branchFlag := fs.String("branch", "", "Branch to add")
	branchShort := fs.String("b", "", "Branch to add (short)")
	parentFlag := fs.String("parent", "", "Parent branch")
	parentShort := fs.String("p", "", "Parent branch (short)")
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

	// Merge short flags
	if *branchShort != "" {
		*branchFlag = *branchShort
	}
	if *parentShort != "" {
		*parentFlag = *parentShort
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	// Get branch name
	var branchName string
	if *branchFlag != "" {
		branchName = *branchFlag
	} else if fs.NArg() >= 1 {
		branchName = fs.Arg(0)
	}

	// Get parent
	var parentName string
	if *parentFlag != "" {
		parentName = *parentFlag
	} else if fs.NArg() >= 2 {
		parentName = fs.Arg(1)
	}

	// Interactive mode
	if branchName == "" {
		branchName, err = selectUntrackedBranch(mgr)
		if err != nil {
			return err
		}
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	baseBranch := cfg.GetBaseBranch(mgr.GetRepoDir())

	if parentName == "" {
		parentName, err = SelectNewParent(mgr, g, branchName, baseBranch)
		if err != nil {
			return err
		}
	}

	// Use reparent logic (with no rebase by default for adding)
	ui.Info(fmt.Sprintf("Adding '%s' to stack with parent '%s'", branchName, parentName))
	if !ui.ConfirmTUI("Proceed?") {
		ui.Warn("Cancelled")
		return nil
	}

	branch, err := mgr.ReparentBranch(branchName, parentName, false)
	if err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Added '%s' to stack with parent '%s'", branch.Name, branch.Parent))

	// Update PR base branch on GitHub if the branch has a PR
	if branch.PRNumber > 0 {
		remoteURL, err := g.GetRemote("origin")
		if err == nil {
			gh, err := github.NewClient(remoteURL)
			if err == nil {
				ui.Info(fmt.Sprintf("Updating PR #%d base branch to '%s'...", branch.PRNumber, parentName))
				if err := gh.UpdatePRBase(branch.PRNumber, parentName); err != nil {
					ui.Warn(fmt.Sprintf("Failed to update PR base branch: %v", err))
				} else {
					ui.Success(fmt.Sprintf("Updated PR #%d base branch to '%s'", branch.PRNumber, parentName))
				}

				// Also update stack descriptions in all PRs
				var currentStack *config.Stack
				stacks := mgr.ListStacks()
				for _, s := range stacks {
					for _, b := range s.Branches {
						if b.Name == branchName {
							currentStack = s
							break
						}
					}
					if currentStack != nil {
						break
					}
				}
				if currentStack != nil {
					ui.Info("Updating PR stack descriptions...")
					skipBranches := getRemoteBranches(currentStack)
					if err := gh.UpdateStackDescription(currentStack, branchName, skipBranches); err != nil {
						ui.Warn(fmt.Sprintf("Failed to update stack descriptions: %v", err))
					}
				}
			}
		}
	}

	return nil
}

// Unstack removes a branch from ezstack tracking without deleting the git branch
func Unstack(args []string) error {
	fs := flag.NewFlagSet("unstack", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sRemove a branch from stack tracking%s

%sUSAGE%s
    ezs unstack [branch] [options]

%sOPTIONS%s
    -b, --branch <name>     Branch to untrack
    -h, --help              Show this help message

%sDESCRIPTION%s
    Removes a branch from ezstack tracking without deleting the git branch
    or worktree. The branch will become a standalone worktree.

    If the branch has children, they will be reparented to the untracked
    branch's parent.

%sEXAMPLES%s
    ezs unstack                  Interactive mode
    ezs unstack my-branch        Untrack my-branch
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	branchFlag := fs.String("branch", "", "Branch to untrack")
	branchShort := fs.String("b", "", "Branch to untrack (short)")
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

	// Merge short flags
	if *branchShort != "" {
		*branchFlag = *branchShort
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	// Get branch name
	var branchName string
	if *branchFlag != "" {
		branchName = *branchFlag
	} else if fs.NArg() >= 1 {
		branchName = fs.Arg(0)
	}

	// Interactive mode
	if branchName == "" {
		branchName, err = selectTrackedBranch(mgr)
		if err != nil {
			return err
		}
	}

	// Check if branch exists
	branch := mgr.GetBranch(branchName)
	if branch == nil {
		return fmt.Errorf("branch '%s' is not tracked by ezstack", branchName)
	}

	// Show what will happen
	children := mgr.GetChildren(branchName)
	ui.Info(fmt.Sprintf("Removing '%s' from stack tracking", branchName))
	ui.Info("The git branch and worktree will NOT be deleted")
	if len(children) > 0 {
		ui.Warn(fmt.Sprintf("Children will be reparented to '%s':", branch.Parent))
		for _, c := range children {
			fmt.Printf("  • %s\n", c.Name)
		}
	}

	if !ui.ConfirmTUI("Proceed?") {
		ui.Warn("Cancelled")
		return nil
	}

	if err := mgr.UntrackBranch(branchName); err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Removed '%s' from stack tracking", branchName))
	return nil
}

// selectTrackedBranch shows a selection UI for tracked branches
func selectTrackedBranch(mgr *stack.Manager) (string, error) {
	allBranches := mgr.GetAllBranchesInAllStacks()

	if len(allBranches) == 0 {
		return "", fmt.Errorf("no tracked branches found")
	}

	var options []string
	var branchNames []string
	for _, b := range allBranches {
		if mgr.IsMainBranch(b.Name) {
			continue
		}
		children := mgr.GetChildren(b.Name)
		childInfo := ""
		if len(children) > 0 {
			childInfo = fmt.Sprintf(" [%d children]", len(children))
		}
		options = append(options, fmt.Sprintf("%s (%s %s)%s", b.Name, ui.IconArrow, b.Parent, childInfo))
		branchNames = append(branchNames, b.Name)
	}

	if len(options) == 0 {
		return "", fmt.Errorf("no branches available to untrack")
	}

	selected, err := ui.SelectOption(options, "Select branch to remove from tracking")
	if err != nil {
		return "", err
	}

	return branchNames[selected], nil
}

// selectUntrackedBranch shows a selection UI for untracked worktrees
func selectUntrackedBranch(mgr *stack.Manager) (string, error) {
	unregistered, err := mgr.GetUnregisteredWorktrees()
	if err != nil {
		return "", err
	}

	if len(unregistered) == 0 {
		return "", fmt.Errorf("no untracked branches found. All worktrees are already in stacks")
	}

	var options []string
	var branchNames []string
	for _, wt := range unregistered {
		options = append(options, fmt.Sprintf("%s (%s)", wt.Branch, wt.Path))
		branchNames = append(branchNames, wt.Branch)
	}

	selected, err := ui.SelectOption(options, "Select branch to add to stack")
	if err != nil {
		return "", err
	}

	return branchNames[selected], nil
}
