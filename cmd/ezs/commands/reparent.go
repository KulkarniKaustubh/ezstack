package commands

import (
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
	"github.com/spf13/pflag"
)

// Reparent changes the parent of a branch
func Reparent(args []string) error {
	fs := pflag.NewFlagSet("reparent", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sChange the parent of a branch%s

%sUSAGE%s
    ezs reparent [branch] [new-parent] [options]
    ezs rp [branch] [new-parent] [options]

%sOPTIONS%s
    -b, --branch <name>     Branch to reparent
    -p, --parent <name>     New parent branch
    -h, --help              Show this help message

%sDESCRIPTION%s
    Changes the parent of a branch and rebases commits onto the new parent.
    This can be used to:

    1. Move a branch to a different parent within the same stack
    2. Add a standalone worktree/branch to an existing stack
    3. Split a stack by reparenting branches to different parents

    Reparenting always rebases to keep the stack consistent. If the rebase
    conflicts, the reparent metadata is still updated and you can resolve
    conflicts manually.

%sEXAMPLES%s
    ezs reparent                        Interactive mode
    ezs reparent feature-c feature-a    Reparent feature-c to feature-a
    ezs reparent -b feature-c -p main   Reparent feature-c to main
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	branchFlag := fs.StringP("branch", "b", "", "Branch to reparent")
	parentFlag := fs.StringP("parent", "p", "", "New parent branch")
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
		return reparentInteractive(mgr, g, branchName, newParent)
	}

	// Non-interactive mode
	return doReparent(mgr, branchName, newParent)
}

// reparentInteractive handles interactive branch and parent selection
func reparentInteractive(mgr *stack.Manager, g *git.Git, branchName, newParent string) error {
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

	return doReparent(mgr, branchName, newParent)
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
		return "", fmt.Errorf("no branches available to reparent. Create one with: ezs new <branch-name>")
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

	// Track which branches are already listed
	listed := map[string]bool{branchName: true}

	// Build options list - include main/master and all stack branches
	var options []string
	var parentNames []string

	// Add configured base branch as first option
	options = append(options, fmt.Sprintf("%s (base branch)", baseBranch))
	parentNames = append(parentNames, baseBranch)
	listed[baseBranch] = true

	// Add branches from stacks (with stack preview)
	for _, b := range allBranches {
		if listed[b.Name] || IsDescendantOf(mgr, b.Name, branchName) {
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
					stackName = s.Hash
					break
				}
			}
		}

		options = append(options, fmt.Sprintf("%s (%s %s) [stack: %s]", b.Name, ui.IconArrow, b.Parent, stackName))
		parentNames = append(parentNames, b.Name)
		listed[b.Name] = true
	}

	// Add other local branches as potential stack roots
	localBranches, err := g.ListLocalBranches()
	if err == nil {
		for _, lb := range localBranches {
			if listed[lb] {
				continue
			}
			options = append(options, fmt.Sprintf("%s (local branch)", lb))
			parentNames = append(parentNames, lb)
			listed[lb] = true
		}
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
	for {
		if current == ancestorName {
			return true
		}
		parentBranch := mgr.GetBranch(current)
		if parentBranch == nil {
			return false
		}
		current = parentBranch.Parent
	}
}

// doReparent performs the actual reparent operation
func doReparent(mgr *stack.Manager, branchName, newParent string) error {
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

	ui.Info("Will rebase commits onto new parent")

	if !ui.ConfirmTUI("Proceed with reparent?") {
		ui.Warn("Cancelled")
		return nil
	}

	oldStack := mgr.GetStackForBranch(branchName)
	result, err := mgr.ReparentBranch(branchName, newParent, true)
	if err != nil {
		return err
	}
	if result == nil || result.Branch == nil {
		return fmt.Errorf("reparent succeeded but branch '%s' not found in updated config", branchName)
	}

	branch := result.Branch

	if result.HasConflict {
		ui.Warn(fmt.Sprintf("Reparented '%s' to '%s' (config updated), but rebase has conflicts", branch.Name, branch.Parent))
		ui.Warn(fmt.Sprintf("Resolve conflicts in: %s", result.ConflictDir))
		ui.Info("Then run: git rebase --continue")
	} else {
		ui.Success(fmt.Sprintf("Reparented '%s' to '%s'", branch.Name, branch.Parent))
	}

	currentStack := mgr.GetStackForBranch(branchName)

	cwd, _ := os.Getwd()
	g := git.New(cwd)

	pushSucceeded := true
	if !result.HasConflict && branch.PRNumber > 0 {
		ui.Info("Branch was rebased. Force-push required to update the PR.")
		worktreePath := cwd
		if branch.WorktreePath != "" {
			worktreePath = branch.WorktreePath
		}
		pushSucceeded = OfferForcePush(branchName, worktreePath)
	}

	gh, ghErr := newGitHubClient(g)
	if ghErr == nil {
		// Only update PR base when the push succeeded — otherwise origin
		// still has old history and the PR diff would be misleading.
		if branch.PRNumber > 0 && pushSucceeded {
			ui.Info(fmt.Sprintf("Updating PR #%d base branch to '%s'...", branch.PRNumber, newParent))
			if err := gh.UpdatePRBase(branch.PRNumber, newParent); err != nil {
				ui.Warn(fmt.Sprintf("Failed to update PR base branch: %v", err))
			} else {
				ui.Success(fmt.Sprintf("Updated PR #%d base branch to '%s'", branch.PRNumber, newParent))
			}
		}

		if oldStack != nil && (currentStack == nil || oldStack.Hash != currentStack.Hash) {
			if err := updateStackDescriptions(gh, oldStack, ""); err != nil {
				ui.Warn(fmt.Sprintf("Failed to update old stack descriptions: %v", err))
			}
		}
		if currentStack != nil {
			if err := updateStackDescriptions(gh, currentStack, branchName); err != nil {
				ui.Warn(fmt.Sprintf("Failed to update stack descriptions: %v", err))
			}
		}
	}

	if currentStack != nil {
		ui.PrintStack(currentStack, branchName, false, nil)
	}

	return nil
}
