package commands

import (
	"errors"
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ghstack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
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
    --no-rebase             Update tracking only, skip the restack
    -h, --help              Show this help message

%sDESCRIPTION%s
    Changes the parent of a branch and restacks its commits (and every
    branch above it) onto the new parent with gh stack rebase.
    This can be used to:

    1. Move a branch to a different parent within the same stack
    2. Add a standalone worktree/branch to an existing stack
    3. Split a stack by reparenting branches to different parents

    If the restack conflicts, the reparent is still recorded: resolve the
    conflict, then run "ezs sync --continue" (or "ezs sync --abort").

%sEXAMPLES%s
    ezs reparent                        Interactive mode
    ezs reparent feature-c feature-a    Reparent feature-c to feature-a
    ezs reparent -b feature-c -p main   Reparent feature-c to main
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	branchFlag := fs.StringP("branch", "b", "", "Branch to reparent")
	parentFlag := fs.StringP("parent", "p", "", "New parent branch")
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

	doReparent := func(branch, parent string) error {
		return doReparentCore(mgr, branch, parent, !*noRebaseFlag)
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

// doReparentNoRebase performs a reparent without restacking (used by `ezs stack`)
func doReparentNoRebase(mgr *stack.Manager, branchName, newParent string) error {
	return doReparentCore(mgr, branchName, newParent, false)
}

// doReparentCore is the shared implementation for reparent and stack commands.
// It records the new parent, then (when restack is true) lets gh-stack move
// the branch's own commits — and every branch above it — onto the new parent.
// When restack is false, only the tracking metadata is updated.
func doReparentCore(mgr *stack.Manager, branchName, newParent string, restack bool) error {
	existingBranch := mgr.GetBranch(branchName)
	oldParent := ""
	if existingBranch != nil {
		oldParent = existingBranch.Parent
	}

	if oldParent != "" {
		ui.Info(fmt.Sprintf("Reparenting '%s' from '%s' to '%s'", branchName, oldParent, newParent))
	} else {
		ui.Info(fmt.Sprintf("Adding '%s' to stack with parent '%s'", branchName, newParent))
	}
	if restack {
		ui.Info("Will rebase its commits onto the new parent")
		if err := requireGHStack(); err != nil {
			return err
		}
	}

	confirmMsg := "Proceed?"
	if restack {
		confirmMsg = "Proceed with reparent?"
	}
	if !ui.ConfirmTUI(confirmMsg) {
		ui.Warn("Cancelled")
		return nil
	}

	if restack {
		// Record the branch's current base before the model changes: gh-stack
		// moves exactly the commits above that base onto the new parent.
		if _, err := reconcileGHStack(mgr); err != nil {
			return err
		}
	}

	oldStack := mgr.GetStackForBranch(branchName)
	result, err := mgr.ReparentBranch(branchName, newParent)
	if err != nil {
		return err
	}
	if result == nil || result.Branch == nil {
		return fmt.Errorf("operation succeeded but branch '%s' not found in updated config", branchName)
	}
	branch := result.Branch
	if oldParent != "" {
		ui.Success(fmt.Sprintf("Reparented '%s' to '%s'", branch.Name, branch.Parent))
	} else {
		ui.Success(fmt.Sprintf("Added '%s' to stack with parent '%s'", branch.Name, branch.Parent))
	}

	cwd, _ := os.Getwd()
	g := git.New(cwd)

	if !restack {
		refreshGHStack(mgr)
	} else {
		// Move the branch's own commits, then let gh-stack restack the
		// branches above it (their recorded bases still point at the
		// branch's old tip, so they follow it with --onto).
		if err := moveOntoParent(mgr, branchName, oldParent, newParent); err != nil {
			return err
		}
		chains, err := reconcileGHStack(mgr)
		if err != nil {
			return err
		}
		target := &syncTarget{mode: modeChildren, branch: branch, stacks: []*config.Stack{mgr.GetStackForBranch(branchName)}}
		steps, err := restackSteps(g, mgr, chains, target)
		if err != nil {
			return err
		}
		p := newPusher(g, mgr, pushAsk, chains)
		p.onlyPublished = true
		p.limitTo(branchName)
		if err := runRestack(g, mgr, steps, p); err != nil {
			if errors.Is(err, errSyncConflict) {
				savePausedScope(mgr, target)
			}
			return err
		}
		// The moved branch itself is pushed with the chain that owns it.
		if c, ok := ghstack.ChainFor(chains, branchName); ok {
			if err := p.pushChain(c); err != nil {
				ui.Warn(err.Error())
			}
		}
	}

	// Retarget PR bases and relink both stacks — but not while the branch's
	// rewritten history is still unpushed, or its PR would briefly show the
	// old parent's commits against the new base.
	currentStack := mgr.GetStackForBranch(branchName)
	if gh, ghErr := newGitHubClient(g); ghErr == nil {
		if restack && branch.PRNumber > 0 && branchDiffersFromRemote(g, branchName, branch.EffectiveRemote()) {
			ui.Info(fmt.Sprintf("Push '%s' (ezs push), then run 'ezs pr update' to retarget PR #%d", branchName, branch.PRNumber))
		} else {
			if oldStack != nil && (currentStack == nil || oldStack.Hash != currentStack.Hash) {
				if s := mgr.GetStackByHashExact(oldStack.Hash); s != nil {
					updatePRMetadata(gh, mgr, s)
				}
			}
			if currentStack != nil {
				updatePRMetadata(gh, mgr, currentStack)
			}
		}
	}

	if currentStack != nil {
		ui.PrintStack(currentStack, branchName, false, nil)
	}
	return nil
}
