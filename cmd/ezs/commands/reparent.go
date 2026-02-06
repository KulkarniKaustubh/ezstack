package commands

import (
	"flag"
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/github"
	"github.com/KulkarniKaustubh/ezstack/internal/helpers"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
)

// Reparent changes the parent of a branch
func Reparent(args []string) error {
	fs := flag.NewFlagSet("reparent", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sChange the parent of a branch%s

%sUSAGE%s
    ezs reparent [branch] [new-parent] [options]
    ezs rp [branch] [new-parent] [options]

%sOPTIONS%s
    -b, --branch <name>     Branch to reparent
    -p, --parent <name>     New parent branch
    -n, --no-rebase         Don't rebase, just update metadata (default: rebase)
    -h, --help              Show this help message

%sDESCRIPTION%s
    Changes the parent of a branch. This can be used to:
    
    1. Move a branch to a different parent within the same stack
    2. Add a standalone worktree/branch to an existing stack
    3. Split a stack by reparenting branches to different parents

    By default, commits are rebased onto the new parent. Use --no-rebase
    to only update the metadata without rebasing.

%sEXAMPLES%s
    ezs reparent                    Interactive mode
    ezs reparent feature-c feature-a    Reparent feature-c to feature-a
    ezs reparent -b feature-c -p main   Reparent feature-c to main
    ezs reparent --no-rebase            Update metadata only
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	branchFlag := fs.String("branch", "", "Branch to reparent")
	branchShort := fs.String("b", "", "Branch to reparent (short)")
	parentFlag := fs.String("parent", "", "New parent branch")
	parentShort := fs.String("p", "", "New parent branch (short)")
	noRebaseFlag := fs.Bool("no-rebase", false, "Don't rebase")
	noRebaseShort := fs.Bool("n", false, "Don't rebase (short)")
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

	// Merge short flags into long flags
	if *branchShort != "" {
		*branchFlag = *branchShort
	}
	if *parentShort != "" {
		*parentFlag = *parentShort
	}
	helpers.MergeFlags(noRebaseShort, noRebaseFlag)

	// Determine if we should rebase
	doRebase := !*noRebaseFlag

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	// Get branch to reparent
	var branchName string
	if *branchFlag != "" {
		branchName = *branchFlag
	} else if fs.NArg() >= 1 {
		branchName = fs.Arg(0)
	}

	// Get new parent
	var newParent string
	if *parentFlag != "" {
		newParent = *parentFlag
	} else if fs.NArg() >= 2 {
		newParent = fs.Arg(1)
	}

	// Interactive mode if branch or parent not specified
	if branchName == "" || newParent == "" {
		return reparentInteractive(mgr, g, branchName, newParent, doRebase)
	}

	// Non-interactive mode
	return doReparent(mgr, branchName, newParent, doRebase)
}

// reparentInteractive handles interactive branch and parent selection
func reparentInteractive(mgr *stack.Manager, g *git.Git, branchName, newParent string, doRebase bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	baseBranch := cfg.GetBaseBranch(mgr.GetRepoDir())

	// Select branch to reparent if not specified
	if branchName == "" {
		branchName, err = selectBranchToReparent(mgr, g)
		if err != nil {
			return err
		}
	}

	// Select new parent if not specified
	if newParent == "" {
		newParent, err = SelectNewParent(mgr, g, branchName, baseBranch)
		if err != nil {
			return err
		}
	}

	return doReparent(mgr, branchName, newParent, doRebase)
}

// selectBranchToReparent shows a selection UI for choosing which branch to reparent
func selectBranchToReparent(mgr *stack.Manager, g *git.Git) (string, error) {
	// Get all branches in stacks
	allBranches := mgr.GetAllBranchesInAllStacks()

	// Get unregistered worktrees (standalone branches)
	unregisteredWorktrees, _ := mgr.GetUnregisteredWorktrees()

	// Build options list
	var options []string
	var branchNames []string

	// Add branches from stacks
	for _, b := range allBranches {
		if b.IsMerged || mgr.IsMainBranch(b.Name) {
			continue
		}
		options = append(options, fmt.Sprintf("%s (%s %s) [in stack]", b.Name, ui.IconArrow, b.Parent))
		branchNames = append(branchNames, b.Name)
	}

	// Add unregistered worktrees
	for _, wt := range unregisteredWorktrees {
		options = append(options, fmt.Sprintf("%s (%s) [standalone]", wt.Branch, wt.Path))
		branchNames = append(branchNames, wt.Branch)
	}

	if len(options) == 0 {
		return "", fmt.Errorf("no branches available to reparent")
	}

	// Use fzf to select
	selected, err := ui.SelectOption(options, "Select branch to reparent")
	if err != nil {
		return "", err
	}

	return branchNames[selected], nil
}

// SelectNewParent shows a selection UI for choosing the new parent
// Exported for reuse by stack command
func SelectNewParent(mgr *stack.Manager, g *git.Git, branchName, baseBranch string) (string, error) {
	// Get all branches in stacks
	allBranches := mgr.GetAllBranchesInAllStacks()
	stacks := mgr.ListStacks()

	// Build options list - include main/master and all stack branches
	var options []string
	var parentNames []string

	// Add main/master as first option
	options = append(options, fmt.Sprintf("%s (base branch)", baseBranch))
	parentNames = append(parentNames, baseBranch)

	// Add branches from stacks (with stack preview)
	for _, b := range allBranches {
		// Skip the branch being reparented and its descendants
		if b.Name == branchName || IsDescendantOf(mgr, b.Name, branchName) {
			continue
		}
		if b.IsMerged {
			continue
		}

		// Find which stack this branch belongs to
		stackName := ""
		for _, s := range stacks {
			for _, sb := range s.Branches {
				if sb.Name == b.Name {
					stackName = s.Name
					break
				}
			}
		}

		options = append(options, fmt.Sprintf("%s (%s %s) [stack: %s]", b.Name, ui.IconArrow, b.Parent, stackName))
		parentNames = append(parentNames, b.Name)
	}

	if len(options) == 0 {
		return "", fmt.Errorf("no valid parent branches available")
	}

	// Use fzf to select
	selected, err := ui.SelectOption(options, "Select new parent")
	if err != nil {
		return "", err
	}

	return parentNames[selected], nil
}

// IsDescendantOf checks if branchName is a descendant of ancestorName
// Exported for reuse by stack command
func IsDescendantOf(mgr *stack.Manager, branchName, ancestorName string) bool {
	branch := mgr.GetBranch(branchName)
	if branch == nil {
		return false
	}

	current := branch.Parent
	for !mgr.IsMainBranch(current) {
		if current == ancestorName {
			return true
		}
		parentBranch := mgr.GetBranch(current)
		if parentBranch == nil {
			return false
		}
		current = parentBranch.Parent
	}
	return false
}

// doReparent performs the actual reparent operation
func doReparent(mgr *stack.Manager, branchName, newParent string, doRebase bool) error {
	// Get current parent for display
	existingBranch := mgr.GetBranch(branchName)
	oldParent := ""
	if existingBranch != nil {
		oldParent = existingBranch.Parent
	}

	// Show what we're about to do
	if oldParent != "" {
		ui.Info(fmt.Sprintf("Reparenting '%s' from '%s' to '%s'", branchName, oldParent, newParent))
	} else {
		ui.Info(fmt.Sprintf("Adding '%s' to stack with parent '%s'", branchName, newParent))
	}

	if doRebase {
		ui.Info("Will rebase commits onto new parent")
	} else {
		ui.Info("Metadata only (no rebase)")
	}

	// Confirm
	if !ui.ConfirmTUI("Proceed with reparent?") {
		ui.Warn("Cancelled")
		return nil
	}

	// Perform the reparent
	branch, err := mgr.ReparentBranch(branchName, newParent, doRebase)
	if err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Reparented '%s' to '%s'", branch.Name, branch.Parent))

	// Find the stack this branch belongs to
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

	cwd, _ := os.Getwd()
	g := git.New(cwd)

	// If we rebased, offer to force-push the branch
	if doRebase && branch.PRNumber > 0 {
		ui.Info("Branch was rebased. Force-push required to update the PR.")
		if ui.ConfirmTUI(fmt.Sprintf("Force-push '%s' to update PR #%d?", branchName, branch.PRNumber)) {
			// Need to be in the branch's worktree to push
			branchGit := g
			if branch.WorktreePath != "" && branch.WorktreePath != cwd {
				branchGit = git.New(branch.WorktreePath)
			}
			if err := branchGit.PushForce(); err != nil {
				ui.Warn(fmt.Sprintf("Failed to force-push: %v", err))
			} else {
				ui.Success(fmt.Sprintf("Force-pushed '%s'", branchName))
			}
		}
	}

	// Update PR base branch on GitHub if the branch has a PR
	if branch.PRNumber > 0 {
		remoteURL, err := g.GetRemote("origin")
		if err == nil {
			gh, err := github.NewClient(remoteURL)
			if err == nil {
				ui.Info(fmt.Sprintf("Updating PR #%d base branch to '%s'...", branch.PRNumber, newParent))
				if err := gh.UpdatePRBase(branch.PRNumber, newParent); err != nil {
					ui.Warn(fmt.Sprintf("Failed to update PR base branch: %v", err))
				} else {
					ui.Success(fmt.Sprintf("Updated PR #%d base branch to '%s'", branch.PRNumber, newParent))
				}

				// Also update stack descriptions in all PRs
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

	// Show the updated stack
	if currentStack != nil {
		ui.PrintStack(currentStack, branchName, false, nil)
	}

	return nil
}
