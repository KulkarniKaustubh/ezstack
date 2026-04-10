package commands

import (
	"fmt"
	"os"

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
    --merge                 Use git merge instead of git rebase
    --rebase                Use git rebase (overrides config)
    --no-rebase             Update tracking only, skip sync
    -h, --help              Show this help message

%sDESCRIPTION%s
    Changes the parent of a branch and syncs commits onto the new parent.
    This can be used to:

    1. Move a branch to a different parent within the same stack
    2. Add a standalone worktree/branch to an existing stack
    3. Split a stack by reparenting branches to different parents

    Uses the configured sync_strategy (default: rebase). Override with
    --merge or --rebase. If the sync conflicts, the reparent metadata is
    still updated and you can resolve conflicts manually.

%sEXAMPLES%s
    ezs reparent                        Interactive mode
    ezs reparent feature-c feature-a    Reparent feature-c to feature-a
    ezs reparent -b feature-c -p main   Reparent feature-c to main
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	branchFlag := fs.StringP("branch", "b", "", "Branch to reparent")
	parentFlag := fs.StringP("parent", "p", "", "New parent branch")
	mergeFlag := fs.Bool("merge", false, "Use git merge instead of git rebase")
	rebaseFlag := fs.Bool("rebase", false, "Use git rebase (overrides config)")
	noRebaseFlag := fs.Bool("no-rebase", false, "Update tracking only, skip sync")
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

	// Resolve merge vs rebase: flags override config
	if *mergeFlag && *rebaseFlag {
		return fmt.Errorf("cannot use both --merge and --rebase")
	}
	useMerge := false
	if *mergeFlag {
		useMerge = true
	} else if *rebaseFlag {
		useMerge = false
	} else {
		useMerge = mgr.GetConfig().GetSyncStrategy(mgr.GetRepoDir()) == "merge"
	}

	// Helper to dispatch based on --no-rebase flag
	doReparent := func(branch, parent string) error {
		if *noRebaseFlag {
			return doReparentNoRebase(mgr, branch, parent)
		}
		return doReparentWithMerge(mgr, branch, parent, useMerge)
	}

	// Interactive mode if branch or parent not specified
	if branchName == "" || newParent == "" {
		return reparentInteractiveWith(mgr, g, branchName, newParent, doReparent)
	}

	// Non-interactive mode
	return doReparent(branchName, newParent)
}

// reparentInteractiveWith handles interactive branch and parent selection,
// then calls the provided doReparent function.
func reparentInteractiveWith(mgr *stack.Manager, g *git.Git, branchName, newParent string, doReparent func(string, string) error) error {
	cfg := mgr.GetConfig()
	baseBranch := cfg.GetBaseBranch(mgr.GetRepoDir())

	// Select branch to reparent if not specified
	if branchName == "" {
		var err error
		branchName, err = selectBranchToReparent(mgr, g)
		if err != nil {
			return err
		}
	}

	// Select new parent if not specified
	if newParent == "" {
		var err error
		newParent, err = SelectNewParent(mgr, g, branchName, baseBranch)
		if err != nil {
			return err
		}
	}

	return doReparent(branchName, newParent)
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

	visited := make(map[string]bool)
	current := branch.Parent
	for {
		if current == ancestorName {
			return true
		}
		if visited[current] {
			return false
		}
		visited[current] = true
		parentBranch := mgr.GetBranch(current)
		if parentBranch == nil {
			return false
		}
		current = parentBranch.Parent
	}
}

// doReparentWithMerge performs reparent with explicit merge/rebase choice
func doReparentWithMerge(mgr *stack.Manager, branchName, newParent string, useMerge bool) error {
	return doReparentCore(mgr, branchName, newParent, true, useMerge)
}

// doReparentNoRebase performs a reparent without rebasing (used by `ezs stack`)
func doReparentNoRebase(mgr *stack.Manager, branchName, newParent string) error {
	return doReparentCore(mgr, branchName, newParent, false, false)
}

// doReparentCore is the shared implementation for reparent and stack commands.
// When rebase=true, commits are synced onto the new parent and push is offered.
// When rebase=false, only the tracking metadata is updated.
func doReparentCore(mgr *stack.Manager, branchName, newParent string, rebase bool, useMerge bool) error {
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

	if rebase {
		if useMerge {
			ui.Info("Will merge new parent into branch")
		} else {
			ui.Info("Will rebase commits onto new parent")
		}
	}

	confirmMsg := "Proceed?"
	if rebase {
		confirmMsg = "Proceed with reparent?"
	}
	if !ui.ConfirmTUI(confirmMsg) {
		ui.Warn("Cancelled")
		return nil
	}

	oldStack := mgr.GetStackForBranch(branchName)
	result, err := mgr.ReparentBranch(branchName, newParent, rebase, useMerge)
	if err != nil {
		return err
	}
	if result == nil || result.Branch == nil {
		return fmt.Errorf("operation succeeded but branch '%s' not found in updated config", branchName)
	}

	branch := result.Branch

	if result.HasConflict {
		syncType := "rebase"
		continueCmd := "git rebase --continue"
		if useMerge {
			syncType = "merge"
			continueCmd = "git merge --continue"
		}
		ui.Warn(fmt.Sprintf("Reparented '%s' to '%s' (config updated), but %s has conflicts", branch.Name, branch.Parent, syncType))
		ui.Warn(fmt.Sprintf("Resolve conflicts in: %s", result.ConflictDir))
		ui.Info(fmt.Sprintf("Then run: %s", continueCmd))
	} else {
		if oldParent != "" {
			ui.Success(fmt.Sprintf("Reparented '%s' to '%s'", branch.Name, branch.Parent))
		} else {
			ui.Success(fmt.Sprintf("Added '%s' to stack with parent '%s'", branch.Name, branch.Parent))
		}
	}

	currentStack := mgr.GetStackForBranch(branchName)

	cwd, _ := os.Getwd()
	g := git.New(cwd)

	// Offer push if synced and branch has a PR
	pushSucceeded := true
	if rebase && !result.HasConflict && branch.PRNumber > 0 {
		worktreePath := cwd
		if branch.WorktreePath != "" {
			worktreePath = branch.WorktreePath
		}
		if useMerge {
			ui.Info("Branch was merged. Push to update the PR.")
			pushSucceeded = OfferPush(branchName, worktreePath)
		} else {
			ui.Info("Branch was rebased. Force-push required to update the PR.")
			pushSucceeded = OfferForcePush(branchName, worktreePath)
		}
	}

	// Update PR metadata on GitHub
	gh, ghErr := newGitHubClient(g)
	if ghErr == nil {
		// Only update PR base when the push succeeded (or no rebase happened)
		if branch.PRNumber > 0 && (pushSucceeded || !rebase) {
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
