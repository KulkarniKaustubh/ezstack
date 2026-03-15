package commands

import (
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/github"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
	"github.com/spf13/pflag"
)

// Stack adds a branch to a stack (alias for reparent with standalone branch)
func Stack(args []string) error {
	fs := pflag.NewFlagSet("stack", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sAdd a branch to a stack%s

%sUSAGE%s
    ezs stack [branch] [parent] [options]

%sOPTIONS%s
    -b, --branch <name>     Branch to add to stack
    -p, --parent <name>     Parent branch in the stack
    -B, --base <name>       Base branch for a new stack (e.g. develop, staging)
    -h, --help              Show this help message

%sDESCRIPTION%s
    Adds an untracked branch/worktree to an existing stack by setting its parent.
    This is equivalent to 'ezs reparent' for standalone branches.

    Use --base to start a new stack rooted on a branch other than the default
    base branch (e.g. develop or staging).

%sEXAMPLES%s
    ezs stack                             Interactive mode
    ezs stack my-branch feature-a         Add my-branch under feature-a
    ezs stack -b my-branch -p main        Add my-branch under main
    ezs stack -b my-branch --base develop Start a new stack on develop
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	branchFlag := fs.StringP("branch", "b", "", "Branch to add")
	parentFlag := fs.StringP("parent", "p", "", "Parent branch")
	baseFlag := fs.StringP("base", "B", "", "Base branch for a new stack")
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

	// --base and --parent are mutually exclusive
	if *baseFlag != "" && *parentFlag != "" {
		return fmt.Errorf("--base and --parent are mutually exclusive")
	}

	// Get parent
	var parentName string
	if *baseFlag != "" {
		parentName = *baseFlag
	} else if *parentFlag != "" {
		parentName = *parentFlag
	} else if fs.NArg() >= 2 {
		parentName = fs.Arg(1)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	baseBranch := cfg.GetBaseBranch(mgr.GetRepoDir())

	// Fully interactive mode: no branch or parent specified
	if branchName == "" && parentName == "" {
		choice, err := ui.SelectOptionWithBack([]string{
			"Add branch to an existing stack",
			"Start a new stack (choose base branch)",
		}, "What would you like to do?")
		if err != nil {
			if err == ui.ErrBack {
				return ui.ErrBack
			}
			return err
		}

		if choice == 0 {
			// Add to existing stack: pick branch first, then parent
			branchName, err = selectUntrackedBranch(mgr)
			if err != nil {
				return err
			}
			parentName, err = SelectNewParent(mgr, g, branchName, baseBranch)
			if err != nil {
				return err
			}
		} else {
			// Start a new stack: pick base branch first, then branch to add
			parentName, err = selectBaseBranch(mgr, g, "", baseBranch)
			if err != nil {
				return err
			}
			branchName, err = selectUntrackedBranch(mgr)
			if err != nil {
				return err
			}
		}
	} else {
		if branchName == "" {
			branchName, err = selectUntrackedBranch(mgr)
			if err != nil {
				return err
			}
		}
		if parentName == "" {
			parentName, err = SelectNewParent(mgr, g, branchName, baseBranch)
			if err != nil {
				return err
			}
		}
	}

	// Use reparent logic (with no rebase by default for adding)
	ui.Info(fmt.Sprintf("Adding '%s' to stack with parent '%s'", branchName, parentName))
	if !ui.ConfirmTUI("Proceed?") {
		ui.Warn("Cancelled")
		return nil
	}

	result, err := mgr.ReparentBranch(branchName, parentName, false)
	if err != nil {
		return err
	}

	branch := result.Branch
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
				currentStack := findStackForBranch(mgr, branchName)
				if currentStack != nil {
					if err := updateStackDescriptions(gh, currentStack, branchName); err != nil {
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
	fs := pflag.NewFlagSet("unstack", pflag.ContinueOnError)
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

	branchFlag := fs.StringP("branch", "b", "", "Branch to untrack")
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

	// Capture children and parent before untracking (they change after)
	oldParent := branch.Parent
	childrenWithPRs := []*config.Branch{}
	for _, c := range children {
		if c.PRNumber > 0 {
			childrenWithPRs = append(childrenWithPRs, c)
		}
	}

	if err := mgr.UntrackBranch(branchName); err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Removed '%s' from stack tracking", branchName))

	// Update children's PR base branches to point to the new parent
	if len(childrenWithPRs) > 0 {
		remoteURL, err := g.GetRemote("origin")
		if err == nil {
			gh, err := github.NewClient(remoteURL)
			if err == nil {
				for _, c := range childrenWithPRs {
					if err := gh.UpdatePRBase(c.PRNumber, oldParent); err != nil {
						ui.Warn(fmt.Sprintf("Failed to update PR #%d base branch: %v", c.PRNumber, err))
					} else {
						ui.Success(fmt.Sprintf("Updated PR #%d base branch to '%s'", c.PRNumber, oldParent))
					}
				}
			}
		}
	}

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

// selectBaseBranch shows a selection UI for choosing the base branch of a new stack
func selectBaseBranch(mgr *stack.Manager, g *git.Git, branchName, baseBranch string) (string, error) {
	var options []string
	var branchNames []string
	listed := map[string]bool{branchName: true}

	// Add configured base branch first
	options = append(options, fmt.Sprintf("%s (default base)", baseBranch))
	branchNames = append(branchNames, baseBranch)
	listed[baseBranch] = true

	// Add other local branches
	localBranches, err := g.ListLocalBranches()
	if err == nil {
		for _, lb := range localBranches {
			if listed[lb] {
				continue
			}
			// Skip branches that are tracked in stacks (they're not bases)
			if mgr.GetBranch(lb) != nil {
				continue
			}
			options = append(options, lb)
			branchNames = append(branchNames, lb)
			listed[lb] = true
		}
	}

	if len(options) == 0 {
		return "", fmt.Errorf("no branches available as stack base")
	}

	selected, err := ui.SelectOption(options, "Select base branch for the new stack")
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
