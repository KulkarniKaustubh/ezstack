package commands

import (
	"flag"
	"fmt"
	"os"

	"github.com/ezstack/ezstack/internal/config"
	"github.com/ezstack/ezstack/internal/git"
	"github.com/ezstack/ezstack/internal/github"
	"github.com/ezstack/ezstack/internal/stack"
	"github.com/ezstack/ezstack/internal/ui"
)

// Sync syncs the stack with remote - handles merged parents and branches behind origin/main
func Sync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sSync stack with remote%s

%sUSAGE%s
    ezs sync [options]

%sOPTIONS%s
    -a, --all         Sync entire stack (auto-detect what needs syncing)
    -p, --parent      Rebase current branch onto its parent
    -c, --children    Rebase child branches onto current branch
    -h, --help        Show this help message

%sDESCRIPTION%s
    Syncs your stack branches with the remote. Without flags, shows an
    interactive menu. This command can:

    1. Detect and sync branches with merged parents (rebase onto main)
    2. Detect and sync branches behind origin/main
    3. Rebase current branch onto its parent
    4. Rebase child branches onto current branch

%sEXAMPLES%s
    ezs sync              Interactive menu
    ezs sync -a           Auto-sync entire stack
    ezs sync -p           Rebase current onto parent
    ezs sync -c           Rebase children onto current
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	// Flags
	helpFlag := fs.Bool("h", false, "Show help")
	allFlag := fs.Bool("all", false, "Sync entire stack")
	allShort := fs.Bool("a", false, "Sync entire stack (short)")
	parentFlag := fs.Bool("parent", false, "Rebase onto parent")
	parentShort := fs.Bool("p", false, "Rebase onto parent (short)")
	childrenFlag := fs.Bool("children", false, "Rebase children")
	childrenShort := fs.Bool("c", false, "Rebase children (short)")

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
		*allFlag = true
	}
	if *parentShort {
		*parentFlag = true
	}
	if *childrenShort {
		*childrenFlag = true
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

	// Show current stack
	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		return fmt.Errorf("not in a stack. Create a branch first with: ezs new <branch-name>")
	}

	ui.PrintStack(currentStack, branch.Name)

	// Try to get GitHub client for checking squash/rebase merged PRs
	var gh *github.Client
	remoteURL, err := g.GetRemote("origin")
	if err == nil {
		gh, _ = github.NewClient(remoteURL)
	}

	// Handle flags
	if *allFlag {
		return syncEntireStack(mgr, gh, cwd)
	}
	if *parentFlag {
		return syncOntoParent(mgr, branch, cwd)
	}
	if *childrenFlag {
		return syncChildren(mgr, branch, cwd)
	}

	// Interactive mode - show menu
	return syncInteractive(mgr, gh, currentStack, branch, cwd)
}

// syncInteractive shows an interactive menu for sync operations
func syncInteractive(mgr *stack.Manager, gh *github.Client, currentStack *config.Stack, branch *config.Branch, cwd string) error {
	options := []string{}
	optionActions := []string{}

	// Option 1: Auto-sync (detect merged parents / behind main)
	options = append(options, fmt.Sprintf("%s  Auto-sync stack (detect merged parents / behind main)", ui.IconSync))
	optionActions = append(optionActions, "auto")

	// Option 2: Rebase current branch onto parent (if parent is not main)
	if branch.Parent != "" && !mgr.IsMainBranch(branch.Parent) {
		options = append(options, fmt.Sprintf("%s  Rebase current branch onto parent (%s)", ui.IconUp, branch.Parent))
		optionActions = append(optionActions, "parent")
	}

	// Option 3: Rebase child branches onto current
	allChildren := mgr.GetChildren(branch.Name)
	localChildren := []*config.Branch{}
	for _, c := range allChildren {
		if !c.IsRemote {
			localChildren = append(localChildren, c)
		}
	}
	if len(localChildren) > 0 {
		childNames := ""
		for i, c := range localChildren {
			if i > 0 {
				childNames += ", "
			}
			childNames += c.Name
		}
		options = append(options, fmt.Sprintf("%s  Rebase %d child branch(es) onto current (%s)", ui.IconDown, len(localChildren), childNames))
		optionActions = append(optionActions, "children")
	}

	if len(options) == 0 {
		ui.Info("No sync operations available for current branch")
		return nil
	}

	selected, err := ui.SelectOptionWithBack(options, "What would you like to do?")
	if err != nil {
		if err == ui.ErrBack {
			return ui.ErrBack
		}
		return err
	}

	action := optionActions[selected]
	switch action {
	case "auto":
		return syncEntireStack(mgr, gh, cwd)
	case "parent":
		return syncOntoParent(mgr, branch, cwd)
	case "children":
		return syncChildren(mgr, branch, cwd)
	}

	return nil
}

// syncEntireStack syncs the entire stack (auto-detect what needs syncing)
func syncEntireStack(mgr *stack.Manager, gh *github.Client, cwd string) error {
	// Check for branches that need syncing
	ui.Info("Fetching latest changes...")
	syncNeeded, err := mgr.DetectSyncNeeded(gh)
	if err != nil {
		ui.Warn(fmt.Sprintf("Could not check for sync needed: %v", err))
	}

	if len(syncNeeded) == 0 {
		ui.Success("All branches are up to date. No sync needed.")
		return nil
	}

	// Show what will be synced
	ui.Info(fmt.Sprintf("Found %d branch(es) that need syncing:", len(syncNeeded)))
	for _, info := range syncNeeded {
		if info.MergedParent != "" {
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: parent %s%s%s was merged to main\n",
				ui.IconBullet, ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.MergedParent, ui.Reset)
		} else if info.BehindParent != "" {
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: %s%d commits%s behind parent %s%s%s\n",
				ui.IconBullet, ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.BehindBy, ui.Reset, ui.Yellow, info.BehindParent, ui.Reset)
		} else if info.BehindBy > 0 {
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: %s%d commits%s behind origin/main\n",
				ui.IconBullet, ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.BehindBy, ui.Reset)
		}
	}
	fmt.Fprintln(os.Stderr)

	if !ui.ConfirmTUI("Sync stack (rebase branches onto latest main)") {
		ui.Warn("Cancelled")
		return nil
	}

	ui.Info("Syncing stack...")
	results, err := mgr.SyncStack(gh)
	if err != nil {
		return err
	}

	// Print results summary
	printSyncResults(results)

	// Check if any branches had conflicts
	hasConflicts := false
	successCount := 0
	for _, r := range results {
		if r.HasConflict {
			hasConflicts = true
		}
		if r.Success {
			successCount++
		}
	}

	fmt.Fprintln(os.Stderr)
	if hasConflicts {
		ui.Warn("Some branches have conflicts. Resolve them and run 'git rebase --continue' in each worktree.")
	}
	if successCount > 0 {
		ui.Success(fmt.Sprintf("Synced %d branch(es) with main!", successCount))
		// Offer to force push if there were successful rebases
		if !hasConflicts {
			offerPush(cwd)
		}
	}

	return nil
}

// syncOntoParent rebases the current branch onto its parent
func syncOntoParent(mgr *stack.Manager, branch *config.Branch, cwd string) error {
	if mgr.IsMainBranch(branch.Parent) {
		ui.Info("Parent is main - use 'Auto-sync' to rebase onto latest origin/main")
		return nil
	}

	if !ui.ConfirmTUI(fmt.Sprintf("Rebase %s onto %s", branch.Name, branch.Parent)) {
		ui.Warn("Cancelled")
		return nil
	}

	ui.Info("Rebasing onto parent...")
	if err := mgr.RebaseOnParent(); err != nil {
		return err
	}
	ui.Success("Rebase complete")
	offerPush(cwd)
	return nil
}

// syncChildren rebases child branches onto the current branch
func syncChildren(mgr *stack.Manager, branch *config.Branch, cwd string) error {
	children := mgr.GetChildren(branch.Name)
	localChildren := []*config.Branch{}
	for _, c := range children {
		if !c.IsRemote {
			localChildren = append(localChildren, c)
		}
	}

	if len(localChildren) == 0 {
		ui.Info("No local child branches to rebase")
		return nil
	}

	if !ui.ConfirmTUI(fmt.Sprintf("Rebase %d child branch(es) onto %s", len(localChildren), branch.Name)) {
		ui.Warn("Cancelled")
		return nil
	}

	ui.Info("Rebasing child branches...")
	results, err := mgr.RebaseChildren()
	if err != nil {
		return err
	}

	// Print results
	hasConflicts := false
	successCount := 0
	for _, r := range results {
		if r.Success {
			ui.Success(fmt.Sprintf("Rebased %s", r.Branch))
			successCount++
		} else if r.HasConflict {
			ui.Warn(fmt.Sprintf("Conflict in %s", r.Branch))
			hasConflicts = true
		} else if r.Error != nil {
			ui.Error(fmt.Sprintf("Failed to rebase %s: %v", r.Branch, r.Error))
		}
	}

	fmt.Fprintln(os.Stderr)
	if hasConflicts {
		ui.Warn("Some branches have conflicts. Resolve them and run 'git rebase --continue' in each worktree.")
	}
	if successCount > 0 {
		ui.Success(fmt.Sprintf("Rebased %d child branch(es)!", successCount))
	}

	return nil
}

// offerPush offers to force push after a successful rebase
func offerPush(cwd string) {
	fmt.Fprintln(os.Stderr)
	ui.Warn("Force push required to update remote branch")
	if ui.ConfirmTUI("Force push (--force-with-lease)") {
		g := git.New(cwd)
		ui.Info("Pushing...")
		if err := g.PushForce(); err != nil {
			ui.Error(fmt.Sprintf("Push failed: %v", err))
		} else {
			ui.Success("Pushed successfully")
		}
	}
}

// printSyncResults prints the results of a sync operation
func printSyncResults(results []stack.RebaseResult) {
	var conflicts []stack.RebaseResult

	for _, r := range results {
		if r.Success {
			if r.BehindBy > 0 {
				ui.Success(fmt.Sprintf("Synced %s (was %d commits behind)", r.Branch, r.BehindBy))
			} else if r.SyncedParent != "" {
				ui.Success(fmt.Sprintf("Synced %s (parent merged, now based on %s)", r.Branch, r.SyncedParent))
			} else {
				ui.Success(fmt.Sprintf("Synced %s", r.Branch))
			}
		} else if r.HasConflict {
			conflicts = append(conflicts, r)
			ui.Warn(fmt.Sprintf("Conflict in %s", r.Branch))
		} else if r.Error != nil {
			ui.Error(fmt.Sprintf("Failed to sync %s: %v", r.Branch, r.Error))
		}
	}

	// Print conflict summary at the end if there are any
	if len(conflicts) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "%s%s Branches with conflicts (%d):%s\n", ui.Yellow, ui.IconConflict, len(conflicts), ui.Reset)
		for _, c := range conflicts {
			fmt.Fprintf(os.Stderr, "  %s %s%s%s\n", ui.IconBullet, ui.Bold, c.Branch, ui.Reset)
			if c.WorktreePath != "" {
				fmt.Fprintf(os.Stderr, "    %sResolve in:%s %s\n", ui.Gray, ui.Reset, c.WorktreePath)
			}
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "%sTo resolve: cd to each worktree, fix conflicts, then run 'git rebase --continue'%s\n", ui.Gray, ui.Reset)
	}
}
