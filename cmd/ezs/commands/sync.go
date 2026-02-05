package commands

import (
	"flag"
	"fmt"
	"os"

	"github.com/ezstack/ezstack/internal/config"
	"github.com/ezstack/ezstack/internal/git"
	"github.com/ezstack/ezstack/internal/github"
	"github.com/ezstack/ezstack/internal/helpers"
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
    -a, --all              Sync current stack (auto-detect what needs syncing)
    --all-stacks           Sync ALL stacks (not just current stack)
    -cur, --current        Sync current branch only (auto-detect what it needs)
    -p, --parent           Rebase current branch onto its parent
    -c, --children         Rebase child branches onto current branch
    --no-delete-local      Don't delete local branches after their PRs are merged
    -h, --help             Show this help message

%sDESCRIPTION%s
    Syncs your stack branches with the remote. Without flags, shows an
    interactive menu. This command can:

    1. Detect and sync branches with merged parents (rebase onto main)
    2. Detect and sync branches behind origin/main
    3. Sync only the current branch (wherever it is in the chain)
    4. Rebase current branch onto its parent
    5. Rebase child branches onto current branch

%sEXAMPLES%s
    ezs sync              Interactive menu
    ezs sync -a           Auto-sync current stack
    ezs sync --all-stacks Auto-sync all stacks
    ezs sync -cur         Sync current branch only
    ezs sync -p           Rebase current onto parent
    ezs sync -c           Rebase children onto current
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	helpFlag := fs.Bool("h", false, "Show help")
	allFlag := fs.Bool("all", false, "Sync current stack")
	allShort := fs.Bool("a", false, "Sync current stack (short)")
	allStacksFlag := fs.Bool("all-stacks", false, "Sync all stacks")
	currentFlag := fs.Bool("current", false, "Sync current branch only")
	currentShort := fs.Bool("cur", false, "Sync current branch only (short)")
	parentFlag := fs.Bool("parent", false, "Rebase onto parent")
	parentShort := fs.Bool("p", false, "Rebase onto parent (short)")
	childrenFlag := fs.Bool("children", false, "Rebase children")
	childrenShort := fs.Bool("c", false, "Rebase children (short)")
	noDeleteLocal := fs.Bool("no-delete-local", false, "Don't delete local branches after their PRs are merged")

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

	helpers.MergeFlags(allShort, allFlag, currentShort, currentFlag, parentShort, parentFlag, childrenShort, childrenFlag)

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		return fmt.Errorf("not in a stack. Create a branch first with: ezs new <branch-name>")
	}

	var gh *github.Client
	remoteURL, err := g.GetRemote("origin")
	if err == nil {
		gh, _ = github.NewClient(remoteURL)
	}

	spinner := ui.NewDelayedSpinner("Fetching branch status...")
	spinner.Start()
	statusMap := fetchBranchStatuses(g, currentStack)
	spinner.Stop()
	ui.PrintStack(currentStack, branch.Name, true, statusMap)

	deleteLocal := !*noDeleteLocal

	if *allStacksFlag {
		return syncStacks(mgr, gh, cwd, deleteLocal, true)
	}
	if *allFlag {
		return syncStacks(mgr, gh, cwd, deleteLocal, false)
	}
	if *currentFlag {
		return syncCurrentBranch(mgr, gh, branch, cwd)
	}
	if *parentFlag {
		return syncOntoParent(mgr, branch, cwd)
	}
	if *childrenFlag {
		return syncChildren(mgr, branch, cwd)
	}

	return syncInteractive(mgr, gh, currentStack, branch, cwd, deleteLocal)
}

// syncInteractive shows an interactive menu for sync operations
func syncInteractive(mgr *stack.Manager, gh *github.Client, currentStack *config.Stack, branch *config.Branch, cwd string, deleteLocal bool) error {
	options := []string{}
	optionActions := []string{}

	options = append(options, fmt.Sprintf("%s  Auto-sync current stack (detect merged parents / behind main)", ui.IconSync))
	optionActions = append(optionActions, "auto")

	options = append(options, fmt.Sprintf("%s  Auto-sync ALL stacks", ui.IconSync))
	optionActions = append(optionActions, "auto-all")

	syncInfo := mgr.DetectSyncNeededForBranch(branch.Name, gh)
	if syncInfo != nil && syncInfo.NeedsSync {
		var reason string
		if syncInfo.MergedParent != "" {
			reason = fmt.Sprintf("parent %s merged", syncInfo.MergedParent)
		} else if syncInfo.BehindParent != "" {
			reason = fmt.Sprintf("%d commits behind %s", syncInfo.BehindBy, syncInfo.BehindParent)
		} else if syncInfo.BehindBy > 0 {
			reason = fmt.Sprintf("%d commits behind main", syncInfo.BehindBy)
		}
		options = append(options, fmt.Sprintf("%s  Sync current branch only (%s)", ui.IconSync, reason))
		optionActions = append(optionActions, "current")
	}

	if branch.Parent != "" && !mgr.IsMainBranch(branch.Parent) {
		options = append(options, fmt.Sprintf("%s  Rebase current branch onto parent (%s)", ui.IconUp, branch.Parent))
		optionActions = append(optionActions, "parent")
	}

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
		return syncStacks(mgr, gh, cwd, deleteLocal, false)
	case "auto-all":
		return syncStacks(mgr, gh, cwd, deleteLocal, true)
	case "current":
		return syncCurrentBranch(mgr, gh, branch, cwd)
	case "parent":
		return syncOntoParent(mgr, branch, cwd)
	case "children":
		return syncChildren(mgr, branch, cwd)
	}

	return nil
}

// syncStacks syncs branches that need syncing (current stack or all stacks based on allStacks flag)
func syncStacks(mgr *stack.Manager, gh *github.Client, cwd string, deleteLocal bool, allStacks bool) error {
	ui.Info("Fetching latest changes...")

	var syncNeeded []stack.SyncInfo
	var err error
	if allStacks {
		syncNeeded, err = mgr.DetectSyncNeededAllStacks(gh)
	} else {
		syncNeeded, err = mgr.DetectSyncNeeded(gh)
	}
	if err != nil {
		ui.Warn(fmt.Sprintf("Could not check for sync needed: %v", err))
	}

	var mergedBranches []stack.MergedBranchInfo
	if deleteLocal && gh != nil {
		if allStacks {
			mergedBranches, err = mgr.DetectMergedBranchesAllStacks(gh)
		} else {
			mergedBranches, err = mgr.DetectMergedBranches(gh)
		}
		if err != nil {
			ui.Warn(fmt.Sprintf("Could not check for merged branches: %v", err))
		}
	}

	if len(syncNeeded) == 0 && len(mergedBranches) == 0 {
		ui.Success("All branches are up to date. No sync needed.")
		return nil
	}

	if len(syncNeeded) > 0 {
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
	}

	if len(mergedBranches) > 0 {
		ui.Info(fmt.Sprintf("Found %d branch(es) with merged PRs (will be deleted):", len(mergedBranches)))
		for _, info := range mergedBranches {
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: PR #%d merged\n",
				ui.IconSuccess, ui.Bold, info.Branch, ui.Reset, info.PRNumber)
		}
		fmt.Fprintln(os.Stderr)
	}

	if len(syncNeeded) > 0 {
		fmt.Fprintln(os.Stderr)

		// beforeRebase callback asks for confirmation before each branch sync
		beforeRebase := func(info stack.SyncInfo) bool {
			var msg string
			if info.MergedParent != "" {
				msg = fmt.Sprintf("Sync %s%s%s? (parent %s%s%s was merged)",
					ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.MergedParent, ui.Reset)
			} else if info.BehindParent != "" {
				msg = fmt.Sprintf("Sync %s%s%s? (%s%d commits%s behind parent %s%s%s)",
					ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.BehindBy, ui.Reset, ui.Yellow, info.BehindParent, ui.Reset)
			} else {
				msg = fmt.Sprintf("Sync %s%s%s? (%s%d commits%s behind origin/main)",
					ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.BehindBy, ui.Reset)
			}
			if ui.ConfirmTUI(msg) {
				ui.Info("Rebasing...")
				return true
			}
			return false
		}

		// afterRebase callback handles pushing after each successful rebase
		afterRebase := func(result stack.RebaseResult, g *git.Git) bool {
			fmt.Fprintln(os.Stderr)
			ui.Success(fmt.Sprintf("Rebased %s", result.Branch))

			needsPush, err := g.IsLocalAheadOfOrigin(result.Branch)
			if err != nil {
				ui.Warn(fmt.Sprintf("Could not check if push is needed: %v", err))
				needsPush = true
			}

			if !needsPush {
				return true
			}

			if ui.ConfirmTUI(fmt.Sprintf("Force push %s (--force-with-lease)", result.Branch)) {
				ui.Info("Pushing...")
				if err := g.PushForce(); err != nil {
					ui.Error(fmt.Sprintf("Push failed: %v", err))
					return false
				}
				ui.Success("Pushed successfully")
				return true
			}

			fmt.Fprintln(os.Stderr)
			ui.Error("Cannot continue syncing child branches without pushing parent first.")
			ui.Info("The rebased parent branch must be pushed before child branches can be synced.")
			ui.Info("Run 'ezs sync' again after pushing to continue.")
			return false
		}

		callbacks := &stack.SyncCallbacks{
			BeforeRebase: beforeRebase,
			AfterRebase:  afterRebase,
		}

		var results []stack.RebaseResult
		if allStacks {
			results, err = mgr.SyncStackAll(gh, callbacks)
		} else {
			results, err = mgr.SyncStack(gh, callbacks)
		}
		if err != nil {
			return err
		}

		printSyncResults(results)

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
			ui.Success(fmt.Sprintf("Synced %d branch(es)!", successCount))
		}
	}

	if len(mergedBranches) > 0 {
		fmt.Fprintln(os.Stderr)
		ui.Info(fmt.Sprintf("Found %d merged branch(es) to clean up:", len(mergedBranches)))
		for _, info := range mergedBranches {
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: PR #%d merged\n",
				ui.IconSuccess, ui.Bold, info.Branch, ui.Reset, info.PRNumber)
		}
		fmt.Fprintln(os.Stderr)
		if ui.ConfirmTUI(fmt.Sprintf("Delete %d merged branch(es) and their worktrees", len(mergedBranches))) {
			ui.Info("Cleaning up merged branches...")
			results := mgr.CleanupMergedBranches(mergedBranches, cwd)
			deletedCount := 0
			for _, r := range results {
				if r.Success {
					deletedCount++
					if r.WorktreeWasDeleted {
						ui.Success(fmt.Sprintf("Deleted %s (worktree was already removed)", r.Branch))
					} else {
						ui.Success(fmt.Sprintf("Deleted %s", r.Branch))
					}
				} else if r.Error != "" {
					ui.Warn(fmt.Sprintf("Failed to delete %s: %s", r.Branch, r.Error))
				}
			}
			if deletedCount > 0 {
				fmt.Fprintln(os.Stderr)
				ui.Success(fmt.Sprintf("Deleted %d merged branch(es)", deletedCount))
			}
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

// syncCurrentBranch syncs only the current branch (wherever it is in the chain)
func syncCurrentBranch(mgr *stack.Manager, gh *github.Client, branch *config.Branch, cwd string) error {
	ui.Info("Fetching latest changes...")
	g := git.New(cwd)
	if err := g.Fetch(); err != nil {
		return fmt.Errorf("failed to fetch: %w", err)
	}

	syncInfo := mgr.DetectSyncNeededForBranch(branch.Name, gh)
	if syncInfo == nil || !syncInfo.NeedsSync {
		ui.Success("Current branch is up to date. No sync needed.")
		return nil
	}

	if syncInfo.MergedParent != "" {
		ui.Info(fmt.Sprintf("Parent %s was merged to main. Will rebase onto main.", syncInfo.MergedParent))
	} else if syncInfo.BehindParent != "" {
		ui.Info(fmt.Sprintf("Current branch is %d commits behind %s.", syncInfo.BehindBy, syncInfo.BehindParent))
	} else if syncInfo.BehindBy > 0 {
		ui.Info(fmt.Sprintf("Current branch is %d commits behind origin/main.", syncInfo.BehindBy))
	}

	if !ui.ConfirmTUI("Sync current branch") {
		ui.Warn("Cancelled")
		return nil
	}

	ui.Info("Syncing current branch...")
	result, err := mgr.SyncBranch(branch.Name, gh)
	if err != nil {
		return err
	}

	if result.Success {
		if result.BehindBy > 0 && result.SyncedParent != "" {
			ui.Success(fmt.Sprintf("Synced %s (was %d commits behind %s)", result.Branch, result.BehindBy, result.SyncedParent))
		} else if result.SyncedParent != "" {
			ui.Success(fmt.Sprintf("Synced %s (parent merged, now based on %s)", result.Branch, result.SyncedParent))
		} else {
			ui.Success(fmt.Sprintf("Synced %s", result.Branch))
		}
		offerPush(cwd)
	} else if result.HasConflict {
		ui.Warn(fmt.Sprintf("Conflict in %s", result.Branch))
		if result.WorktreePath != "" {
			fmt.Fprintf(os.Stderr, "%sResolve in:%s %s\n", ui.Gray, ui.Reset, result.WorktreePath)
		}
		fmt.Fprintf(os.Stderr, "%sTo resolve: fix conflicts, then run 'git rebase --continue'%s\n", ui.Gray, ui.Reset)
	} else if result.Error != nil {
		ui.Error(fmt.Sprintf("Failed to sync %s: %v", result.Branch, result.Error))
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
