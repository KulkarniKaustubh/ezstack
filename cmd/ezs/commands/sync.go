package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/github"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
	"github.com/spf13/pflag"
)

// Sync syncs the stack with remote - handles merged parents and branches behind origin/main
func Sync(args []string) error {
	fs := pflag.NewFlagSet("sync", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sSync stack with remote%s

%sUSAGE%s
    ezs sync [options]
    ezs sync <hash-prefix>    Sync a specific stack by hash (min 3 characters)

%sOPTIONS%s
    -a, --all              Sync current stack (auto-detect what needs syncing)
    --all-stacks           Sync ALL stacks (not just current stack)
    -c, --current          Sync current branch only (auto-detect what it needs)
    -p, --parent           Rebase current branch onto its parent
    -C, --children         Rebase child branches onto current branch
    --no-delete-local      Don't delete local branches after their PRs are merged
    --dry-run              Preview what would be synced without making changes
    --no-autostash         Don't stash uncommitted changes before rebase
    --json                 Output dry-run results as JSON (requires --dry-run)
    -h, --help             Show this help message

%sDESCRIPTION%s
    Syncs your stack branches with the remote. Without flags, shows an
    interactive menu. This command can:

    1. Detect and sync branches with merged parents (rebase onto base)
    2. Detect and sync branches behind their stack's base branch
    3. Sync only the current branch (wherever it is in the chain)
    4. Rebase current branch onto its parent
    5. Rebase child branches onto current branch

    When run from main (not in a stack worktree), shows a menu to choose
    which stack to sync. You can also pass a stack hash prefix (minimum
    3 characters) to sync a specific stack from anywhere.

%sEXAMPLES%s
    ezs sync              Interactive menu
    ezs sync a1b2c        Sync stack matching hash prefix
    ezs sync -a           Auto-sync current stack
    ezs sync --all-stacks Auto-sync all stacks
    ezs sync -c           Sync current branch only
    ezs sync -p           Rebase current onto parent
    ezs sync -C           Rebase children onto current
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	helpFlag := fs.BoolP("help", "h", false, "Show help")
	allFlag := fs.BoolP("all", "a", false, "Sync current stack")
	allStacksFlag := fs.Bool("all-stacks", false, "Sync all stacks")
	currentFlag := fs.BoolP("current", "c", false, "Sync current branch only")
	parentFlag := fs.BoolP("parent", "p", false, "Rebase onto parent")
	childrenFlag := fs.BoolP("children", "C", false, "Rebase children")
	noDeleteLocal := fs.Bool("no-delete-local", false, "Don't delete local branches after their PRs are merged")
	dryRunFlag := fs.Bool("dry-run", false, "Preview what would be synced")
	noAutostashFlag := fs.Bool("no-autostash", false, "Don't stash uncommitted changes before rebase")
	jsonFlag := fs.Bool("json", false, "Output dry-run results as JSON")

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

	gh, _ := newGitHubClient(g)

	deleteLocal := !*noDeleteLocal

	dryRun := *dryRunFlag
	autostash := !*noAutostashFlag
	jsonOutput := *jsonFlag

	if jsonOutput && !dryRun {
		return fmt.Errorf("--json requires --dry-run")
	}

	// Check for positional arg (hash prefix)
	positionalArgs := fs.Args()
	if len(positionalArgs) > 0 {
		hashPrefix := positionalArgs[0]
		targetStack, err := mgr.GetStackByHash(hashPrefix)
		if err != nil {
			return err
		}
		if dryRun {
			return syncDryRun(mgr, gh, []*config.Stack{targetStack}, jsonOutput)
		}
		return syncSpecificStacks(mgr, gh, cwd, deleteLocal, []*config.Stack{targetStack}, autostash)
	}

	// Try to get current stack (may fail if on main)
	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		// On main or not in a stack - show main menu
		if *allStacksFlag || *allFlag {
			if dryRun {
				return syncDryRunAll(mgr, gh, jsonOutput)
			}
			return syncStacks(mgr, gh, cwd, deleteLocal, true, autostash)
		}
		if dryRun {
			return syncDryRunAll(mgr, gh, jsonOutput)
		}
		return syncFromMain(mgr, gh, cwd, deleteLocal, autostash)
	}

	// In a stack worktree - existing behavior
	spinner := ui.NewDelayedSpinner("Fetching branch status...")
	spinner.Start()
	statusMap := fetchBranchStatuses(g, currentStack)
	spinner.Stop()
	ui.PrintStack(currentStack, branch.Name, true, statusMap)

	if dryRun {
		if *allStacksFlag {
			return syncDryRunAll(mgr, gh, jsonOutput)
		}
		return syncDryRun(mgr, gh, []*config.Stack{currentStack}, jsonOutput)
	}

	if *allStacksFlag {
		return syncStacks(mgr, gh, cwd, deleteLocal, true, autostash)
	}
	if *allFlag {
		return syncStacks(mgr, gh, cwd, deleteLocal, false, autostash)
	}
	if *currentFlag {
		return syncCurrentBranch(mgr, gh, branch, cwd, autostash)
	}
	if *parentFlag {
		return syncOntoParent(mgr, branch)
	}
	if *childrenFlag {
		return syncChildren(mgr, branch)
	}

	return syncInteractive(mgr, gh, currentStack, branch, cwd, deleteLocal, autostash)
}

// syncDryRun previews what sync would do for specific stacks
func syncDryRun(mgr *stack.Manager, gh *github.Client, stacks []*config.Stack, jsonOutput bool) error {
	syncNeeded, err := mgr.DetectSyncNeededForStacks(gh, stacks)
	if err != nil {
		return err
	}
	if jsonOutput {
		return printSyncInfoJSON(syncNeeded)
	}
	if len(syncNeeded) == 0 {
		ui.Success("All branches are up to date. Nothing to sync.")
		return nil
	}
	ui.Info("[dry-run] The following branches would be synced:")
	printSyncInfoList(syncNeeded)
	return nil
}

// syncDryRunAll previews what sync would do across all stacks
func syncDryRunAll(mgr *stack.Manager, gh *github.Client, jsonOutput bool) error {
	syncNeeded, err := mgr.DetectSyncNeededAllStacks(gh)
	if err != nil {
		return err
	}
	if jsonOutput {
		return printSyncInfoJSON(syncNeeded)
	}
	if len(syncNeeded) == 0 {
		ui.Success("All branches are up to date. Nothing to sync.")
		return nil
	}
	ui.Info("[dry-run] The following branches would be synced:")
	printSyncInfoList(syncNeeded)
	return nil
}

// syncFromMain shows an interactive menu when running sync from main (not in a stack worktree)
func syncFromMain(mgr *stack.Manager, gh *github.Client, cwd string, deleteLocal bool, autostash bool) error {
	stacks := mgr.ListStacks()
	if len(stacks) == 0 {
		ui.Info("No stacks found. Create a branch first with: ezs new <branch-name>")
		return nil
	}

	options := []string{
		fmt.Sprintf("%s  Auto-sync ALL stacks (detect merged parents / behind base branch)", ui.IconSync),
		fmt.Sprintf("%s  Choose a stack to sync", ui.IconStack),
	}

	selected, err := ui.SelectOptionWithBack(options, "Sync from main - what would you like to do?")
	if err != nil {
		if err == ui.ErrBack {
			return ui.ErrBack
		}
		return err
	}

	switch selected {
	case 0:
		return syncStacks(mgr, gh, cwd, deleteLocal, true, autostash)
	case 1:
		targetStack, err := ui.SelectStack(stacks, "Select a stack to sync")
		if err != nil {
			return err
		}
		return syncSpecificStacks(mgr, gh, cwd, deleteLocal, []*config.Stack{targetStack}, autostash)
	}

	return nil
}

// printSyncInfoList prints the list of branches that need syncing.
func printSyncInfoList(syncNeeded []stack.SyncInfo) {
	ui.Info(fmt.Sprintf("Found %d branch(es) that need syncing:", len(syncNeeded)))
	for _, info := range syncNeeded {
		if info.MergedParent != "" {
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: parent %s%s%s was merged to %s\n",
				ui.IconBullet, ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.MergedParent, ui.Reset, info.StackRoot)
		} else if info.BehindParent != "" {
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: %s%d commits%s behind parent %s%s%s\n",
				ui.IconBullet, ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.BehindBy, ui.Reset, ui.Yellow, info.BehindParent, ui.Reset)
		} else if info.BehindBy > 0 {
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: %s%d commits%s behind origin/%s\n",
				ui.IconBullet, ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.BehindBy, ui.Reset, info.StackRoot)
		}
	}
	fmt.Fprintln(os.Stderr)
}

// syncInfoJSON represents a sync info entry in JSON output
type syncInfoJSON struct {
	Branch       string `json:"branch"`
	NeedsSync    bool   `json:"needs_sync"`
	MergedParent string `json:"merged_parent,omitempty"`
	BehindParent string `json:"behind_parent,omitempty"`
	BehindBy     int    `json:"behind_by,omitempty"`
	StackRoot    string `json:"stack_root"`
}

// printSyncInfoJSON outputs sync info as JSON to stdout
func printSyncInfoJSON(syncNeeded []stack.SyncInfo) error {
	result := make([]syncInfoJSON, 0, len(syncNeeded))
	for _, info := range syncNeeded {
		result = append(result, syncInfoJSON{
			Branch:       info.Branch,
			NeedsSync:    info.NeedsSync,
			MergedParent: info.MergedParent,
			BehindParent: info.BehindParent,
			BehindBy:     info.BehindBy,
			StackRoot:    info.StackRoot,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// printMergedBranchesList prints the list of merged branches.
func printMergedBranchesList(mergedBranches []stack.MergedBranchInfo) {
	ui.Info(fmt.Sprintf("Found %d branch(es) with merged PRs (will be deleted):", len(mergedBranches)))
	for _, info := range mergedBranches {
		fmt.Fprintf(os.Stderr, "  %s %s%s%s: PR #%d merged\n",
			ui.IconSuccess, ui.Bold, info.Branch, ui.Reset, info.PRNumber)
	}
	fmt.Fprintln(os.Stderr)
}

// formatSyncConfirmMsg builds the confirmation prompt for a sync operation.
func formatSyncConfirmMsg(info stack.SyncInfo) string {
	if info.MergedParent != "" {
		return fmt.Sprintf("Sync %s%s%s? (parent %s%s%s was merged)",
			ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.MergedParent, ui.Reset)
	}
	if info.BehindParent != "" {
		return fmt.Sprintf("Sync %s%s%s? (%s%d commits%s behind parent %s%s%s)",
			ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.BehindBy, ui.Reset, ui.Yellow, info.BehindParent, ui.Reset)
	}
	return fmt.Sprintf("Sync %s%s%s? (%s%d commits%s behind origin/%s)",
		ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.BehindBy, ui.Reset, info.StackRoot)
}

// makeSyncCallbacks creates standard sync callbacks for interactive syncing.
// When singleStackMode is true, declining a push shows a more detailed error
// explaining that child branches can't be synced without pushing the parent.
func makeSyncCallbacks(singleStackMode bool, autostash bool) *stack.SyncCallbacks {
	beforeRebase := func(info stack.SyncInfo) bool {
		if ui.ConfirmTUI(formatSyncConfirmMsg(info)) {
			ui.Info("Rebasing...")
			return true
		}
		return false
	}

	afterRebase := func(result stack.RebaseResult, g *git.Git) bool {
		fmt.Fprintln(os.Stderr)
		ui.Success(fmt.Sprintf("Rebased %s", result.Branch))

		if !OfferForcePush(result.Branch, result.WorktreePath) {
			if singleStackMode {
				fmt.Fprintln(os.Stderr)
				ui.Error("Cannot continue syncing child branches without pushing parent first.")
				ui.Info("The rebased parent branch must be pushed before child branches can be synced.")
				ui.Info("Run 'ezs sync' again after pushing to continue.")
			} else {
				ui.Warn("Skipping remaining branches in this stack (push required for children)")
			}
			return false
		}

		return true
	}

	return &stack.SyncCallbacks{
		BeforeRebase: beforeRebase,
		AfterRebase:  afterRebase,
		Autostash:    autostash,
	}
}

// printSyncSummary prints the summary after sync operations complete.
func printSyncSummary(results []stack.RebaseResult) {
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

// handleMergedBranchCleanup handles cleanup of merged branches, including
// detection of fully merged stacks for stack-level cleanup.
func handleMergedBranchCleanup(mgr *stack.Manager, mergedBranches []stack.MergedBranchInfo, stacks []*config.Stack, cwd string) {
	// Group merged branches by stack to detect fully merged stacks
	mergedByStack := make(map[string][]stack.MergedBranchInfo)
	for _, mb := range mergedBranches {
		mergedByStack[mb.StackHash] = append(mergedByStack[mb.StackHash], mb)
	}
	stackByHash := make(map[string]*config.Stack)
	for _, s := range stacks {
		stackByHash[s.Hash] = s
	}

	// Detect fully merged stacks
	fullyMergedHashes := make(map[string]bool)
	for _, s := range stacks {
		mbs := mergedByStack[s.Hash]
		mergedNames := make(map[string]bool)
		for _, mb := range mbs {
			mergedNames[mb.Branch] = true
		}
		allAccountedFor := true
		hasPRBranches := false
		for _, b := range s.Branches {
			if b.PRNumber == 0 {
				allAccountedFor = false
				break
			}
			hasPRBranches = true
			if !mergedNames[b.Name] {
				allAccountedFor = false
				break
			}
		}
		if allAccountedFor && hasPRBranches {
			fullyMergedHashes[s.Hash] = true
		}
	}

	// For fully merged stacks: stack-level cleanup prompt
	for hash := range fullyMergedHashes {
		s := stackByHash[hash]
		if s.DeleteDeclined {
			continue
		}
		fmt.Fprintln(os.Stderr)
		ui.Info(fmt.Sprintf("Stack '%s' is fully merged (%d branch(es)):", s.DisplayName(), len(s.Branches)))
		for _, b := range s.Branches {
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: PR #%d merged\n",
				ui.IconSuccess, ui.Bold, b.Name, ui.Reset, b.PRNumber)
		}
		fmt.Fprintln(os.Stderr)
		if ui.ConfirmTUI(fmt.Sprintf("Delete all worktrees, branches, and tracking for stack '%s'", s.DisplayName())) {
			if err := mgr.DeleteStack(hash); err != nil {
				ui.Warn(fmt.Sprintf("Failed to clean up stack '%s': %v", s.DisplayName(), err))
			} else {
				ui.Success(fmt.Sprintf("Removed fully merged stack '%s'", s.DisplayName()))
			}
		} else {
			mgr.DeclineStackDelete(hash)
		}
	}

	// For partially merged stacks: per-branch cleanup
	var partialMerged []stack.MergedBranchInfo
	for _, mb := range mergedBranches {
		if !fullyMergedHashes[mb.StackHash] {
			partialMerged = append(partialMerged, mb)
		}
	}
	if len(partialMerged) > 0 {
		fmt.Fprintln(os.Stderr)
		ui.Info(fmt.Sprintf("Found %d merged branch(es) to clean up:", len(partialMerged)))
		for _, info := range partialMerged {
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: PR #%d merged\n",
				ui.IconSuccess, ui.Bold, info.Branch, ui.Reset, info.PRNumber)
		}
		fmt.Fprintln(os.Stderr)
		if ui.ConfirmTUI(fmt.Sprintf("Delete %d merged branch(es) and their worktrees", len(partialMerged))) {
			ui.Info("Cleaning up merged branches...")
			results := mgr.CleanupMergedBranches(partialMerged, cwd)
			deletedCount := 0
			needsCd := false
			for _, r := range results {
				if r.Success {
					deletedCount++
					if r.WasCurrentWorktree {
						needsCd = true
					}
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
			if needsCd {
				EmitCd(mgr.GetRepoDir())
			}
		}
	}
}

// syncSpecificStacks syncs a specific set of stacks
func syncSpecificStacks(mgr *stack.Manager, gh *github.Client, cwd string, deleteLocal bool, stacks []*config.Stack, autostash bool) error {
	ui.Info("Fetching latest changes...")

	syncNeeded, err := mgr.DetectSyncNeededForStacks(gh, stacks)
	if err != nil {
		ui.Warn(fmt.Sprintf("Could not check for sync needed: %v", err))
	}

	var mergedBranches []stack.MergedBranchInfo
	if deleteLocal && gh != nil {
		mergedBranches, err = mgr.DetectMergedBranchesForStacks(gh, stacks)
		if err != nil {
			ui.Warn(fmt.Sprintf("Could not check for merged branches: %v", err))
		}
	}

	if len(syncNeeded) == 0 && len(mergedBranches) == 0 {
		// Even if no sync is needed, check for fully merged stacks that need cleanup
		cleanupFullyMergedStacks(mgr, stacks)
		ui.Success("All branches are up to date. No sync needed.")
		return nil
	}

	if len(syncNeeded) > 0 {
		printSyncInfoList(syncNeeded)
	}

	if len(mergedBranches) > 0 {
		printMergedBranchesList(mergedBranches)
	}

	if len(syncNeeded) > 0 {
		fmt.Fprintln(os.Stderr)

		callbacks := makeSyncCallbacks(len(stacks) == 1, autostash)
		results, err := mgr.SyncSpecificStacks(stacks, gh, callbacks)
		if err != nil {
			return err
		}

		printSyncResults(results)
		printSyncSummary(results)
	}

	if len(mergedBranches) > 0 {
		handleMergedBranchCleanup(mgr, mergedBranches, stacks, cwd)
	}

	// Check for stacks that were already fully merged in cache before this sync run
	cleanupFullyMergedStacks(mgr, stacks)

	// Ensure all PR base branches are correct (fixes manually-created PRs pointing to wrong base)
	if gh != nil {
		for _, s := range stacks {
			if err := gh.EnsureCorrectBaseBranches(s); err != nil {
				ui.Warn(fmt.Sprintf("Failed to update PR base branches: %v", err))
			}
		}
	}

	return nil
}

// syncInteractive shows an interactive menu for sync operations
func syncInteractive(mgr *stack.Manager, gh *github.Client, currentStack *config.Stack, branch *config.Branch, cwd string, deleteLocal bool, autostash bool) error {
	options := []string{}
	optionActions := []string{}

	options = append(options, fmt.Sprintf("%s  Auto-sync current stack (detect merged parents / behind %s)", ui.IconSync, currentStack.Root))
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
			reason = fmt.Sprintf("%d commits behind %s", syncInfo.BehindBy, currentStack.Root)
		}
		options = append(options, fmt.Sprintf("%s  Sync current branch only (%s)", ui.IconSync, reason))
		optionActions = append(optionActions, "current")
	}

	if branch.Parent != "" && branch.Parent != currentStack.Root {
		options = append(options, fmt.Sprintf("%s  Rebase current branch onto parent (%s)", ui.IconUp, branch.Parent))
		optionActions = append(optionActions, "parent")
	}

	localChildren := mgr.GetChildren(branch.Name)
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
		return syncStacks(mgr, gh, cwd, deleteLocal, false, autostash)
	case "auto-all":
		return syncStacks(mgr, gh, cwd, deleteLocal, true, autostash)
	case "current":
		return syncCurrentBranch(mgr, gh, branch, cwd, autostash)
	case "parent":
		return syncOntoParent(mgr, branch)
	case "children":
		return syncChildren(mgr, branch)
	}

	return nil
}

// syncStacks resolves the target stacks and delegates to syncSpecificStacks.
func syncStacks(mgr *stack.Manager, gh *github.Client, cwd string, deleteLocal bool, allStacks bool, autostash bool) error {
	var stacks []*config.Stack
	if allStacks {
		stacks = mgr.ListStacks()
		if len(stacks) == 0 {
			ui.Info("No stacks found.")
			return nil
		}
	} else {
		currentStack, _, err := mgr.GetCurrentStack()
		if err != nil {
			return err
		}
		stacks = []*config.Stack{currentStack}
	}
	return syncSpecificStacks(mgr, gh, cwd, deleteLocal, stacks, autostash)
}

// syncOntoParent rebases the current branch onto its parent
func syncOntoParent(mgr *stack.Manager, branch *config.Branch) error {
	stack := mgr.GetStackForBranch(branch.Name)
	if stack != nil && branch.Parent == stack.Root {
		ui.Info(fmt.Sprintf("Parent is %s - use 'Auto-sync' to rebase onto latest origin/%s", stack.Root, stack.Root))
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
	OfferForcePush(branch.Name, branch.WorktreePath)
	return nil
}

// syncChildren rebases child branches onto the current branch
func syncChildren(mgr *stack.Manager, branch *config.Branch) error {
	localChildren := mgr.GetChildren(branch.Name)

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
	var successfulBranches []string
	for _, r := range results {
		if r.Success {
			ui.Success(fmt.Sprintf("Rebased %s", r.Branch))
			successCount++
			successfulBranches = append(successfulBranches, r.Branch)
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

		// Offer to push successfully rebased branches
		if len(successfulBranches) > 0 {
			OfferForcePushMultiple(successfulBranches, func(branchName string) string {
				childBranch := mgr.GetBranch(branchName)
				if childBranch == nil {
					return ""
				}
				return childBranch.WorktreePath
			})
		}
	}

	return nil
}

// syncCurrentBranch syncs only the current branch (wherever it is in the chain)
func syncCurrentBranch(mgr *stack.Manager, gh *github.Client, branch *config.Branch, cwd string, autostash bool) error {
	ui.Info("Fetching latest changes...")
	g := git.New(cwd)
	if err := g.Fetch(); err != nil {
		return fmt.Errorf("failed to fetch from remote: %w. Check your network connection and that the remote is accessible", err)
	}

	syncInfo := mgr.DetectSyncNeededForBranch(branch.Name, gh)
	if syncInfo == nil || !syncInfo.NeedsSync {
		ui.Success("Current branch is up to date. No sync needed.")
		return nil
	}

	if syncInfo.MergedParent != "" {
		ui.Info(fmt.Sprintf("Parent %s was merged to %s. Will rebase onto %s.", syncInfo.MergedParent, syncInfo.StackRoot, syncInfo.StackRoot))
	} else if syncInfo.BehindParent != "" {
		ui.Info(fmt.Sprintf("Current branch is %d commits behind %s.", syncInfo.BehindBy, syncInfo.BehindParent))
	} else if syncInfo.BehindBy > 0 {
		ui.Info(fmt.Sprintf("Current branch is %d commits behind origin/%s.", syncInfo.BehindBy, syncInfo.StackRoot))
	}

	if !ui.ConfirmTUI("Sync current branch") {
		ui.Warn("Cancelled")
		return nil
	}

	// Autostash: stash uncommitted changes before rebase
	didStash := false
	if autostash {
		if hasChanges, _ := g.HasChanges(); hasChanges {
			if err := g.StashPush(); err == nil {
				didStash = true
				ui.Info("Stashed uncommitted changes")
			}
		}
	}

	ui.Info("Syncing current branch...")
	result, err := mgr.SyncBranch(branch.Name, gh)
	if err != nil {
		if didStash {
			g.StashPop()
		}
		return err
	}

	// Pop stash after successful sync (not on conflict — user resolves first)
	if didStash && !result.HasConflict {
		if err := g.StashPop(); err != nil {
			ui.Warn(fmt.Sprintf("Failed to pop stash: %v", err))
		} else {
			ui.Info("Restored stashed changes")
		}
	}

	if result.Success {
		if result.BehindBy > 0 && result.SyncedParent != "" {
			ui.Success(fmt.Sprintf("Synced %s (was %d commits behind %s)", result.Branch, result.BehindBy, result.SyncedParent))
		} else if result.SyncedParent != "" {
			ui.Success(fmt.Sprintf("Synced %s (parent merged, now based on %s)", result.Branch, result.SyncedParent))
		} else {
			ui.Success(fmt.Sprintf("Synced %s", result.Branch))
		}
		OfferForcePush(result.Branch, result.WorktreePath)
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

// cleanupFullyMergedStacks checks if any stacks are fully merged and offers to remove them.
// If all worktrees/branches are already cleaned up, removes the stack automatically.
// If some local artifacts remain, prompts the user for deletion.
func cleanupFullyMergedStacks(mgr *stack.Manager, stacks []*config.Stack) {
	fullyMerged := mgr.DetectFullyMergedStacks(stacks)
	if len(fullyMerged) == 0 {
		return
	}

	for _, info := range fullyMerged {
		s := mgr.GetStackByHashExact(info.StackHash)
		displayName := info.StackHash
		if s != nil {
			if s.DeleteDeclined {
				continue
			}
			displayName = s.DisplayName()
		}
		fmt.Fprintln(os.Stderr)
		if info.HasLocalArtifacts {
			// Some worktrees or git branches still exist locally
			ui.Info(fmt.Sprintf("Stack '%s' is fully merged but has remaining local branches/worktrees", displayName))
			if ui.ConfirmTUI(fmt.Sprintf("Delete all remaining worktrees and branches for stack '%s'", displayName)) {
				if err := mgr.DeleteStack(info.StackHash); err != nil {
					ui.Warn(fmt.Sprintf("Failed to delete stack '%s': %v", displayName, err))
				} else {
					ui.Success(fmt.Sprintf("Removed fully merged stack '%s'", displayName))
				}
			} else {
				mgr.DeclineStackDelete(info.StackHash)
			}
		} else {
			// Everything already cleaned up - remove stack from config automatically
			if err := mgr.DeleteStack(info.StackHash); err != nil {
				ui.Warn(fmt.Sprintf("Failed to remove stack '%s' from config: %v", displayName, err))
			} else {
				ui.Success(fmt.Sprintf("Removed fully merged stack '%s' (all branches already cleaned up)", displayName))
			}
		}
	}
}
