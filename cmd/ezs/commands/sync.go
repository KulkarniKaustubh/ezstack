package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/github"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/hooks"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

// Sync syncs the stack with remote - handles merged parents and branches behind origin/main
func Sync(args []string) error {
	if HasExamplesFlag("sync", args) {
		return nil
	}
	fs := pflag.NewFlagSet("sync", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sSync stack with remote%s

%sUSAGE%s
    ezs sync [options]
    ezs sync <hash-prefix>    Sync a specific stack by hash (min 3 characters)

%sOPTIONS%s
    -s, --stack            Sync current stack (auto-detect what needs syncing)
    -a, --all              Sync ALL stacks
    -c, --current          Sync current branch only (auto-detect what it needs)
    -b, --branch <name>    Sync a specific branch by name (rebase onto parent + cascade to children)
    -p, --parent           Rebase current branch onto its parent
    -C, --children         Rebase child branches onto current branch
    --continue             Continue after resolving conflicts (completes rebase/merge, pushes, then re-syncs the entire descendant subtree). Honors -s, -a, -c, -b, and positional <hash-prefix> to limit the scope.
    --merge                Use git merge instead of git rebase
    --rebase               Use git rebase (overrides sync_strategy config)
    --stats                Print commits-per-branch summary after syncing
    --squash               Squash each child's commits before rebasing onto parent
    --no-delete-local      Don't delete local branches after their PRs are merged
    --dry-run              Preview what would be synced without making changes
    --no-autostash         Don't stash uncommitted changes before rebase
    --json                 Output dry-run results as JSON (requires --dry-run)
    --include-remote-worktrees   Include pickup branches (ezs new origin/<branch> / -r)
                                 in bulk sync. Excluded by default to avoid rewriting
                                 another contributor's history.
    -h, --help             Show this help message

%sDESCRIPTION%s
    Syncs your stack branches with the remote. Without flags, shows an
    interactive menu. This command can:

    1. Detect and sync branches with merged parents (rebase onto base)
    2. Detect and sync branches behind their stack's base branch
    3. Sync only the current branch (wherever it is in the chain)
    4. Rebase current branch onto its parent
    5. Rebase child branches onto current branch

    By default, sync uses git rebase. Use --merge to use git merge instead,
    which preserves commit history and avoids force pushes. The default
    strategy can be set per-repo with: ezs config set sync_strategy merge
    Use --rebase or --merge to override the configured strategy.

    When run from main (not in a stack worktree), shows a menu to choose
    which stack to sync. You can also pass a stack hash prefix (minimum
    3 characters) to sync a specific stack from anywhere.

%sEXAMPLES%s
    ezs sync              Interactive menu
    ezs sync a1b2c        Sync stack matching hash prefix
    ezs sync -s           Auto-sync current stack
    ezs sync -a           Auto-sync all stacks
    ezs sync -c           Sync current branch only
    ezs sync -b feat-x    Sync a specific branch by name
    ezs sync -p           Rebase current onto parent
    ezs sync -C           Rebase children onto current
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	helpFlag := fs.BoolP("help", "h", false, "Show help")
	statsFlag := fs.Bool("stats", false, "Print commits-per-branch summary after syncing")
	squashFlag := fs.Bool("squash", false, "Squash each child's commits into one before rebase")
	stackFlag := fs.BoolP("stack", "s", false, "Sync current stack")
	allFlag := fs.BoolP("all", "a", false, "Sync all stacks")
	currentFlag := fs.BoolP("current", "c", false, "Sync current branch only")
	branchFlag := fs.StringP("branch", "b", "", "Sync a specific branch by name")
	parentFlag := fs.BoolP("parent", "p", false, "Rebase onto parent")
	childrenFlag := fs.BoolP("children", "C", false, "Rebase children")
	mergeFlag := fs.Bool("merge", false, "Use git merge instead of git rebase")
	rebaseFlag := fs.Bool("rebase", false, "Use git rebase (overrides config)")
	noDeleteLocal := fs.Bool("no-delete-local", false, "Don't delete local branches after their PRs are merged")
	dryRunFlag := fs.Bool("dry-run", false, "Preview what would be synced")
	continueFlag := fs.Bool("continue", false, "Continue after resolving conflicts")
	noAutostashFlag := fs.Bool("no-autostash", false, "Don't stash uncommitted changes before rebase")
	jsonFlag := fs.Bool("json", false, "Output dry-run results as JSON")
	includeRemoteFlag := fs.Bool("include-remote-worktrees", false, "Include pickup branches (IsRemote=true) in bulk sync")

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

	// Capture the starting cwd so the --stats defer below uses the directory
	// sync was invoked from, not whatever worktree we ended up in after
	// rebase/cd operations.
	statsCwd := cwd

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

	// Acquire a sync-level lock so two `ezs sync` invocations can't race on
	// snapshot reads/writes or fight over rebase state. The lock file lives
	// next to stacks.json (which is global per ezstack install), so the
	// lock is also global — concurrent syncs across different repos will
	// serialize. That's what we want: stacks.json is shared state.
	//
	// Skipped only for --dry-run, which is read-only. --continue acquires
	// the lock too: by the time the user runs --continue, the original sync
	// has already returned (its lock release fired), so there's no
	// contention with the conflicted run; the lock here exists to prevent
	// two simultaneous --continue invocations from racing on snapshot
	// cleanup and PR-metadata updates.
	if !dryRun {
		cfgDir, cfgErr := config.ConfigDir()
		if cfgErr == nil {
			lock, lockErr := config.AcquireSyncLock(filepath.Join(cfgDir, "stacks.json"))
			if lockErr != nil {
				return lockErr
			}
			defer lock.Release()
		}
	}

	// Resolve merge vs rebase: flags override config
	useMerge := false
	if *mergeFlag && *rebaseFlag {
		return fmt.Errorf("cannot use both --merge and --rebase")
	} else if *mergeFlag {
		useMerge = true
	} else if *rebaseFlag {
		useMerge = false
	} else {
		// No flag specified — use config
		useMerge = mgr.GetConfig().GetSyncStrategy(mgr.GetRepoDir()) == "merge"
	}

	hookCtx := BuildHookContext()

	// --continue resumes an in-progress rebase that already fired its pre-sync
	// hook on the original invocation. Re-running pre-sync here would execute
	// user-configured tests against an unresolved tree — wrong and slow. We
	// still fire post-sync on successful completion so "sync is done" hooks
	// see the terminal state, and --squash must not run again either (the
	// squash already happened on the original invocation).
	if *continueFlag {
		// Resolve the same scope flags as non-continue sync: -s, -a, -c, -b,
		// positional hash. Without this, syncContinue defaulted to "all
		// stacks", so `ezs sync -s --continue` (intended: current stack only)
		// silently continued conflicts in unrelated stacks.
		scope, err := resolveContinueScope(mgr, fs.Args(), *allFlag, *stackFlag, *currentFlag, *branchFlag)
		if err != nil {
			return err
		}
		defer func() {
			if hookErr := hooks.Run("post-sync", hookCtx); hookErr != nil {
				ui.Warn(hookErr.Error())
			}
		}()
		return syncContinue(mgr, gh, useMerge, scope)
	}

	if jsonOutput && !dryRun {
		return fmt.Errorf("--json requires --dry-run")
	}

	// Dry-run must be side-effect free: no hooks fire, no squash runs.
	// Everything below up through --squash is gated on !dryRun.
	if !dryRun {
		if err := hooks.Run("pre-sync", hookCtx); err != nil {
			return err
		}
		defer func() {
			if hookErr := hooks.Run("post-sync", hookCtx); hookErr != nil {
				ui.Warn(hookErr.Error())
			}
		}()

		// --stats prints a commits-ahead summary after sync completes. Registered
		// AFTER the post-sync defer so LIFO fires stats first, post-sync second.
		if *statsFlag {
			defer printSyncStats(statsCwd)
		}

		if *squashFlag {
			ui.Info("--squash: squashing children before sync")
			if err := squashStackChildren(mgr); err != nil {
				return err
			}
		}
	}

	// Handle --branch flag: sync a specific branch by name
	if *branchFlag != "" {
		branch := mgr.GetBranch(*branchFlag)
		if branch == nil {
			return fmt.Errorf("branch %q not found in any stack", *branchFlag)
		}
		targetStack := mgr.FindStackForBranch(*branchFlag)
		if targetStack == nil {
			return fmt.Errorf("branch %q is not part of any stack", *branchFlag)
		}
		if dryRun {
			// Per-branch dry-run mirrors the execute path (syncCurrentBranch),
			// which uses DetectSyncNeededForBranch and does NOT skip remote-only
			// branches. Routing through syncDryRun (bulk detection) would filter
			// pickups out and report "no sync needed" while the real run rebases
			// — a divergence between preview and apply.
			return syncDryRunBranch(mgr, gh, branch, jsonOutput)
		}
		// Use the branch's worktree path so stash operations target the correct worktree
		branchCwd := cwd
		if branch.WorktreePath != "" {
			branchCwd = branch.WorktreePath
		}
		return syncCurrentBranch(mgr, gh, branch, branchCwd, autostash, useMerge)
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
			return syncDryRun(mgr, gh, []*config.Stack{targetStack}, jsonOutput, *includeRemoteFlag)
		}
		return syncSpecificStacks(mgr, gh, cwd, deleteLocal, []*config.Stack{targetStack}, autostash, useMerge, *includeRemoteFlag)
	}

	// Try to get current stack (may fail if on main)
	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		// On main or not in a stack - show main menu
		if *allFlag || *stackFlag {
			if dryRun {
				return syncDryRunAll(mgr, gh, jsonOutput, *includeRemoteFlag)
			}
			return syncStacks(mgr, gh, cwd, deleteLocal, true, autostash, useMerge, *includeRemoteFlag)
		}
		if dryRun {
			return syncDryRunAll(mgr, gh, jsonOutput, *includeRemoteFlag)
		}
		return syncFromMain(mgr, gh, cwd, deleteLocal, autostash, useMerge, *includeRemoteFlag)
	}

	// In a stack worktree - existing behavior
	spinner := ui.NewDelayedSpinner("Fetching branch status...")
	spinner.Start()
	statusMap := fetchBranchStatuses(g, currentStack, false)
	spinner.Stop()
	ui.PrintStack(currentStack, branch.Name, true, statusMap)

	if dryRun {
		if *allFlag {
			return syncDryRunAll(mgr, gh, jsonOutput, *includeRemoteFlag)
		}
		return syncDryRun(mgr, gh, []*config.Stack{currentStack}, jsonOutput, *includeRemoteFlag)
	}

	if *allFlag {
		return syncStacks(mgr, gh, cwd, deleteLocal, true, autostash, useMerge, *includeRemoteFlag)
	}
	if *stackFlag {
		return syncStacks(mgr, gh, cwd, deleteLocal, false, autostash, useMerge, *includeRemoteFlag)
	}
	if *currentFlag {
		return syncCurrentBranch(mgr, gh, branch, cwd, autostash, useMerge)
	}
	if *parentFlag {
		return syncOntoParent(mgr, branch, useMerge)
	}
	if *childrenFlag {
		return syncChildren(mgr, branch, useMerge)
	}

	return syncInteractive(mgr, gh, currentStack, branch, cwd, deleteLocal, autostash, useMerge, *includeRemoteFlag)
}

// printSyncStats prints a "commits ahead of parent" summary for each branch
// in the stack rooted at cwd. It's called from a deferred function in Sync()
// so the caller can request post-action reporting without forcing a return
// path through a helper.
func printSyncStats(cwd string) {
	writeSyncStats(os.Stderr, cwd)
}

// writeSyncStats is the testable core of printSyncStats: it writes the stats
// block to w so callers don't have to swap os.Stderr. printSyncStats exists
// only to bind the default destination.
func writeSyncStats(w io.Writer, cwd string) {
	mgrLocal, mErr := stack.NewReadOnlyManager(cwd)
	if mErr != nil {
		return
	}
	currentStack, _, csErr := mgrLocal.GetCurrentStack()
	if csErr != nil || currentStack == nil {
		return
	}
	gLocal := git.New(cwd)
	fmt.Fprintf(w, "\n%sSync stats (commits ahead of parent):%s\n", ui.Bold, ui.Reset)
	for _, b := range currentStack.Branches {
		if b.Parent == "" {
			continue
		}
		ahead, err := gLocal.GetCommitsAhead(b.Name, b.Parent)
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "  %s %-30s %s%d commits%s\n", ui.IconBullet, b.Name, ui.Yellow, ahead, ui.Reset)
	}
}

// squashStackChildren rewrites every branch in the current stack (except the
// root) so it has a single commit relative to its parent.
//
// Algorithm:
//  1. Walk branches in any order; we only reset the branch itself, never the
//     parent, so iteration order does not matter for correctness.
//  2. For each branch with ≥2 commits ahead of its parent: soft-reset onto
//     the parent and re-commit as a single commit, preserving the most recent
//     commit message.
//  3. Dirty worktrees, missing worktrees, or branches <2 commits ahead are
//     skipped with a warning (for the first two cases) or silently (for the
//     third — already-squashed).
//
// IMPORTANT: rewritten branches will diverge from their remotes and require
// a force-push. The subsequent sync operation will push them with
// --force-with-lease. Users must not have unsynced collaborators on these
// branches when running --squash.
// topoOrderStackBranches returns the stack's branches in parent-before-child
// order. Any branch whose parent isn't present in the stack (typically the
// root, whose parent is the base branch) is emitted first.
//
// Cycles shouldn't occur in a well-formed stack but the implementation is
// cycle-tolerant: `visited` is set on entry to each `visit`, so a back-edge
// short-circuits without recursing forever. The first cycle member visited
// is emitted first (after any of its non-cycle ancestors), then each later
// member follows in DFS order; no input is dropped, but the resulting order
// within a cycle is not a true topological one (it can't be — a cycle has
// no topological order). Callers that depend on a strict parent-before-child
// invariant should never see a cycle in practice.
func topoOrderStackBranches(s *config.Stack) []*config.Branch {
	if s == nil || len(s.Branches) == 0 {
		return nil
	}
	byName := make(map[string]*config.Branch, len(s.Branches))
	for _, b := range s.Branches {
		byName[b.Name] = b
	}
	visited := make(map[string]bool, len(s.Branches))
	var out []*config.Branch
	var visit func(b *config.Branch)
	visit = func(b *config.Branch) {
		if b == nil || visited[b.Name] {
			return
		}
		visited[b.Name] = true
		if p, ok := byName[b.Parent]; ok {
			visit(p)
		}
		out = append(out, b)
	}
	for _, b := range s.Branches {
		visit(b)
	}
	return out
}

func squashStackChildren(mgr *stack.Manager) error {
	currentStack, _, err := mgr.GetCurrentStack()
	if err != nil {
		return err
	}
	ui.Warn("--squash rewrites branch history; pushed branches will need --force-with-lease")

	// Walk branches parents-before-children. Squashing a child before its
	// parent would leave the child pointing at the pre-squash parent commit,
	// so the parent's later squash orphans the child's ancestry and rebase
	// inherits spurious conflicts. Topological order guarantees each branch
	// sees its parent's squashed tip as the reset target.
	ordered := topoOrderStackBranches(currentStack)

	squashed := 0
	for _, b := range ordered {
		// Skip the stack root: it has no parent inside the stack, and its
		// "parent" in config is typically the base branch (main), which we
		// must never touch.
		if b.Parent == "" || b.Name == currentStack.Root {
			continue
		}
		if b.WorktreePath == "" {
			ui.Warn(fmt.Sprintf("Skipping squash of '%s': no worktree", b.Name))
			continue
		}
		bg := git.New(b.WorktreePath)
		dirty, _ := bg.HasChanges()
		if dirty {
			ui.Warn(fmt.Sprintf("Skipping squash of '%s': uncommitted changes", b.Name))
			continue
		}
		ahead, err := bg.GetCommitsAhead(b.Name, b.Parent)
		if err != nil || ahead < 2 {
			continue
		}
		msg := fmt.Sprintf("squash: %s onto %s", b.Name, b.Parent)
		if last, lerr := bg.GetLastCommitMessage(); lerr == nil && last != "" {
			msg = last
		}
		if _, runErr := bg.RunCapture("reset", "--soft", b.Parent); runErr != nil {
			ui.Warn(fmt.Sprintf("Squash failed for '%s' (reset): %v", b.Name, runErr))
			continue
		}
		if _, runErr := bg.RunCapture("commit", "-m", msg); runErr != nil {
			ui.Warn(fmt.Sprintf("Squash failed for '%s' (commit): %v", b.Name, runErr))
			continue
		}
		ui.Success(fmt.Sprintf("Squashed '%s' (%d → 1 commit)", b.Name, ahead))
		squashed++
	}
	if squashed == 0 {
		ui.Info("--squash: nothing to do (no branches had ≥2 commits)")
	}
	return nil
}

// syncDryRun previews what sync would do for specific stacks
func syncDryRun(mgr *stack.Manager, gh *github.Client, stacks []*config.Stack, jsonOutput bool, includeRemote bool) error {
	syncNeeded, err := mgr.DetectSyncNeededForStacks(gh, stacks, includeRemote)
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

// syncDryRunBranch previews what sync would do for a single explicitly-named
// branch. Matches the execute path (syncCurrentBranch → DetectSyncNeededForBranch),
// which never filters IsRemote because the user named the branch directly.
//
// We fetch before detecting because DetectSyncNeededForBranch only consults
// local tracking refs — without a fetch, a teammate's just-pushed commit on
// origin/<branch> would be invisible and we'd report "no sync needed",
// while the execute path (which fetches at syncCurrentBranch start) would
// see the commit and rebase. That's the same dry-run-vs-apply divergence
// this wrapper exists to close. Failures are non-fatal: a stale tracking
// ref just means the preview can't flag a remote-pull; surface a warning
// so the user knows to retry once their network is healthy.
func syncDryRunBranch(mgr *stack.Manager, gh *github.Client, branch *config.Branch, jsonOutput bool) error {
	if !jsonOutput {
		ui.Info("Fetching latest changes...")
	}
	if err := mgr.Fetch(); err != nil {
		return fmt.Errorf("failed to fetch from remote: %w. Check your network connection and that the remote is accessible", err)
	}
	if r := branch.EffectiveRemote(); r != "" && r != "origin" && branch.CanPush() {
		if ferr := mgr.FetchRemote(r); ferr != nil && !jsonOutput {
			ui.Warn(fmt.Sprintf("Could not fetch %s for %s: %v — preview may miss collaborator commits on that remote.", r, branch.Name, ferr))
		}
	}
	info := mgr.DetectSyncNeededForBranch(branch.Name, gh)
	var syncNeeded []stack.SyncInfo
	if info != nil && info.NeedsSync {
		syncNeeded = []stack.SyncInfo{*info}
	}
	if jsonOutput {
		return printSyncInfoJSON(syncNeeded)
	}
	if len(syncNeeded) == 0 {
		ui.Success("Current branch is up to date. No sync needed.")
		return nil
	}
	ui.Info("[dry-run] The following branches would be synced:")
	printSyncInfoList(syncNeeded)
	return nil
}

// syncDryRunAll previews what sync would do across all stacks
func syncDryRunAll(mgr *stack.Manager, gh *github.Client, jsonOutput bool, includeRemote bool) error {
	syncNeeded, err := mgr.DetectSyncNeededForStacks(gh, mgr.ListStacks(), includeRemote)
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
func syncFromMain(mgr *stack.Manager, gh *github.Client, cwd string, deleteLocal bool, autostash bool, useMerge bool, includeRemote bool) error {
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
		return syncStacks(mgr, gh, cwd, deleteLocal, true, autostash, useMerge, includeRemote)
	case 1:
		targetStack, err := ui.SelectStack(stacks, "Select a stack to sync")
		if err != nil {
			return err
		}
		return syncSpecificStacks(mgr, gh, cwd, deleteLocal, []*config.Stack{targetStack}, autostash, useMerge, includeRemote)
	}

	return nil
}

// printSyncInfoList prints the list of branches that need syncing.
func printSyncInfoList(syncNeeded []stack.SyncInfo) {
	ui.Info(fmt.Sprintf("Found %d branch(es) that need syncing:", len(syncNeeded)))
	for _, info := range syncNeeded {
		switch {
		case info.MergedParent != "":
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: parent %s%s%s was merged to %s\n",
				ui.IconBullet, ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.MergedParent, ui.Reset, info.StackRoot)
		case info.BehindParent != "":
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: %s%d commits%s behind parent %s%s%s\n",
				ui.IconBullet, ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.BehindBy, ui.Reset, ui.Yellow, info.BehindParent, ui.Reset)
		case info.BehindBy > 0:
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: %s%d commits%s behind origin/%s\n",
				ui.IconBullet, ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.BehindBy, ui.Reset, info.StackRoot)
		case info.BehindRemote > 0:
			// Remote-only behind: branch is in sync with parent but a teammate
			// pushed new commits to origin/<branch>.
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: %s%d commits%s on origin/%s (collaborator pushed)\n",
				ui.IconBullet, ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.BehindRemote, ui.Reset, info.Branch)
			continue
		}
		// If a branch is behind both its parent and its remote, append the
		// remote count after the primary reason printed above.
		if info.BehindRemote > 0 && (info.MergedParent != "" || info.BehindParent != "" || info.BehindBy > 0) {
			fmt.Fprintf(os.Stderr, "      (also %s%d commits%s behind origin/%s)\n",
				ui.Yellow, info.BehindRemote, ui.Reset, info.Branch)
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
	BehindRemote int    `json:"behind_remote,omitempty"`
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
			BehindRemote: info.BehindRemote,
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
func makeSyncCallbacks(singleStackMode bool, autostash bool, useMerge bool, includeRemote bool) *stack.SyncCallbacks {
	beforeRebase := func(info stack.SyncInfo) bool {
		if ui.ConfirmTUI(formatSyncConfirmMsg(info)) {
			if useMerge {
				ui.Info("Merging...")
			} else {
				ui.Info("Rebasing...")
			}
			return true
		}
		return false
	}

	afterRebase := func(result stack.RebaseResult, g *git.Git) bool {
		fmt.Fprintln(os.Stderr)
		if useMerge {
			ui.Success(fmt.Sprintf("Merged into %s", result.Branch))
		} else {
			ui.Success(fmt.Sprintf("Rebased %s", result.Branch))
		}

		remote := result.Remote
		if remote == "" {
			if mgr, mErr := stack.NewReadOnlyManager(g.RepoDir); mErr == nil {
				remote = ResolveBranchRemote(g, mgr, result.Branch)
			} else {
				remote = "origin"
			}
		}

		if useMerge {
			if !OfferPush(result.Branch, result.WorktreePath, remote) {
				if singleStackMode {
					fmt.Fprintln(os.Stderr)
					ui.Error("Cannot continue syncing child branches without pushing parent first.")
					ui.Info("The merged parent branch must be pushed before child branches can be synced.")
					ui.Info("Run 'ezs sync' again after pushing to continue.")
				} else {
					ui.Warn("Skipping remaining branches in this stack (push required for children)")
				}
				return false
			}
		} else {
			if !OfferForcePush(result.Branch, result.WorktreePath, remote) {
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
		}

		return true
	}

	return &stack.SyncCallbacks{
		BeforeRebase:           beforeRebase,
		AfterRebase:            afterRebase,
		Autostash:              autostash,
		UseMerge:               useMerge,
		IncludeRemoteWorktrees: includeRemote,
	}
}

// printSyncSummary prints the summary after sync operations complete.
func printSyncSummary(results []stack.RebaseResult, useMerge bool) {
	successCount := 0
	conflictStacks := make(map[string]bool)
	for _, r := range results {
		if r.HasConflict {
			name := r.StackName
			if name == "" {
				name = "(unknown stack)"
			}
			conflictStacks[name] = true
		}
		if r.Success {
			successCount++
		}
	}

	fmt.Fprintln(os.Stderr)
	if successCount > 0 {
		ui.Success(fmt.Sprintf("Synced %d branch(es)!", successCount))
	}
	if len(conflictStacks) > 0 {
		names := make([]string, 0, len(conflictStacks))
		for name := range conflictStacks {
			names = append(names, name)
		}
		if useMerge {
			ui.Warn(fmt.Sprintf("%d stack(s) unsynced due to merge conflicts: %s", len(names), strings.Join(names, ", ")))
		} else {
			ui.Warn(fmt.Sprintf("%d stack(s) unsynced due to rebase conflicts: %s", len(names), strings.Join(names, ", ")))
		}
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
			needsCd, err := mgr.DeleteStack(hash)
			if err != nil {
				ui.Warn(fmt.Sprintf("Failed to clean up stack '%s': %v", s.DisplayName(), err))
			} else {
				ui.Success(fmt.Sprintf("Removed fully merged stack '%s'", s.DisplayName()))
				if needsCd {
					EmitCd(mgr.GetRepoDir())
				}
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
func syncSpecificStacks(mgr *stack.Manager, gh *github.Client, cwd string, deleteLocal bool, stacks []*config.Stack, autostash bool, useMerge bool, includeRemote bool) error {
	ui.Info("Fetching latest changes...")

	syncNeeded, err := mgr.DetectSyncNeededForStacks(gh, stacks, includeRemote)
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

		callbacks := makeSyncCallbacks(len(stacks) == 1, autostash, useMerge, includeRemote)
		results, err := mgr.SyncSpecificStacks(stacks, gh, callbacks)
		if err != nil {
			return err
		}

		printSyncResults(results, useMerge)
		printSyncSummary(results, useMerge)
	}

	if len(mergedBranches) > 0 {
		handleMergedBranchCleanup(mgr, mergedBranches, stacks, cwd)
	}

	// Check for stacks that were already fully merged in cache before this sync run
	cleanupFullyMergedStacks(mgr, stacks)

	// Update PR base branches and stack descriptions (parallelized per stack)
	if gh != nil {
		for _, s := range stacks {
			updatePRMetadata(gh, s, nil)
		}
	}

	return nil
}

// syncInteractive shows an interactive menu for sync operations
func syncInteractive(mgr *stack.Manager, gh *github.Client, currentStack *config.Stack, branch *config.Branch, cwd string, deleteLocal bool, autostash bool, useMerge bool, includeRemote bool) error {
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
		return syncStacks(mgr, gh, cwd, deleteLocal, false, autostash, useMerge, includeRemote)
	case "auto-all":
		return syncStacks(mgr, gh, cwd, deleteLocal, true, autostash, useMerge, includeRemote)
	case "current":
		return syncCurrentBranch(mgr, gh, branch, cwd, autostash, useMerge)
	case "parent":
		return syncOntoParent(mgr, branch, useMerge)
	case "children":
		return syncChildren(mgr, branch, useMerge)
	}

	return nil
}

// syncStacks resolves the target stacks and delegates to syncSpecificStacks.
func syncStacks(mgr *stack.Manager, gh *github.Client, cwd string, deleteLocal bool, allStacks bool, autostash bool, useMerge bool, includeRemote bool) error {
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
	return syncSpecificStacks(mgr, gh, cwd, deleteLocal, stacks, autostash, useMerge, includeRemote)
}

// continueScope captures the user-requested scope for `ezs sync --continue`.
// Mirrors the dispatch shape of non-continue sync (-a / -s / -c / -b /
// positional hash) so `--continue` honors the same selectors.
//
// `defaulted` is true when no scope flag was provided and the resolver picked
// a default — used to print one explanatory line so the user isn't surprised
// when, e.g., running `ezs sync --continue` from main implicitly touches every
// stack with an in-progress rebase.
type continueScope struct {
	mode       continueMode
	stack      *config.Stack
	branchName string
	defaulted  bool
}

type continueMode int

const (
	continueModeAll continueMode = iota
	continueModeCurrentStack
	continueModeSpecificStack
	continueModeBranch
)

// ErrSyncIncomplete signals that `ezs sync --continue` finished but some
// branches are still mid-conflict, or a re-synced child hit a new conflict.
// Wrapped as *ui.ExitError so main() exits with ExitConflict (3) rather than
// the generic ExitGeneral (1), letting scripts distinguish "still in conflict —
// re-run after resolving" from any other failure mode.
var ErrSyncIncomplete = ui.NewExitError(ui.ExitConflict, "sync continue incomplete: resolve remaining conflicts and re-run `ezs sync --continue`")

// errSyncContinueFailed wraps a non-conflict failure during `--continue` (a
// real git or filesystem error from `git rebase --continue` / `git commit`,
// or a SyncBranch failure on a descendant re-sync). Exits with
// ExitGeneral (1) instead of ExitConflict (3) so scripts and the user can
// distinguish "broken state — investigate" from "still in conflict —
// resolve and re-run".
func errSyncContinueFailed(detail string) error {
	return ui.NewExitError(ui.ExitGeneral, "sync continue failed: %s", detail)
}

// resolveContinueScope mirrors the scope-flag dispatch used by non-continue
// sync (lines reading -a / -s / -c / -b / positional hash). Defaults: in a
// stack worktree → currentStack; on main with no flags → all.
func resolveContinueScope(mgr *stack.Manager, posArgs []string, allFlag, stackFlag, currentFlag bool, branchFlag string) (continueScope, error) {
	// At most one explicit selector.
	selectors := 0
	if allFlag {
		selectors++
	}
	if stackFlag {
		selectors++
	}
	if currentFlag {
		selectors++
	}
	if branchFlag != "" {
		selectors++
	}
	if len(posArgs) > 0 {
		selectors++
	}
	if selectors > 1 {
		return continueScope{}, fmt.Errorf("--continue accepts at most one of: -a, -s, -c, -b <name>, <stack-hash>")
	}

	if branchFlag != "" {
		if mgr.GetBranch(branchFlag) == nil {
			return continueScope{}, fmt.Errorf("branch %q not found in any stack", branchFlag)
		}
		return continueScope{mode: continueModeBranch, branchName: branchFlag}, nil
	}
	if currentFlag {
		_, branch, err := mgr.GetCurrentStack()
		if err != nil {
			return continueScope{}, fmt.Errorf("--current requires being in a stack worktree: %w", err)
		}
		return continueScope{mode: continueModeBranch, branchName: branch.Name}, nil
	}
	if len(posArgs) > 0 {
		stk, err := mgr.GetStackByHash(posArgs[0])
		if err != nil {
			return continueScope{}, err
		}
		return continueScope{mode: continueModeSpecificStack, stack: stk}, nil
	}
	if stackFlag {
		stk, _, err := mgr.GetCurrentStack()
		if err != nil {
			return continueScope{}, fmt.Errorf("-s requires being in a stack worktree: %w", err)
		}
		return continueScope{mode: continueModeCurrentStack, stack: stk}, nil
	}
	if allFlag {
		return continueScope{mode: continueModeAll}, nil
	}
	// Default: if in a stack, current stack only; else all.
	if stk, _, err := mgr.GetCurrentStack(); err == nil {
		return continueScope{mode: continueModeCurrentStack, stack: stk, defaulted: true}, nil
	}
	return continueScope{mode: continueModeAll, defaulted: true}, nil
}

// stacksInScope returns the stacks that --continue should consider, given a
// resolved scope. For branch / specificStack / currentStack it's a single
// stack; for all it's every stack.
func stacksInScope(mgr *stack.Manager, scope continueScope) []*config.Stack {
	switch scope.mode {
	case continueModeAll:
		return mgr.ListStacks()
	case continueModeCurrentStack, continueModeSpecificStack:
		return []*config.Stack{scope.stack}
	case continueModeBranch:
		if stk := mgr.GetStackForBranch(scope.branchName); stk != nil {
			return []*config.Stack{stk}
		}
	}
	return nil
}

// syncContinue resumes any in-progress rebase or merge that's within the
// given scope, then re-syncs the entire descendant subtree of each branch
// whose continue completed cleanly. Returns ErrSyncIncomplete when one or
// more branches are still in conflict — either because the original branch's
// rebase paused on its next commit, or because a re-synced descendant hit a
// new conflict.
//
// Topology:
//   - Branches are processed parents-before-children so a parent's `--continue`
//     reaches its terminal state before any child consults its PreSyncCommit.
//   - Children that themselves have an in-progress rebase/merge are skipped
//     during re-sync — they'll be picked up on the user's next `--continue`,
//     after they've been resolved.
//   - The full descendant subtree (not just immediate children) is walked, so
//     deep stacks fully re-sync after a single root resolution.
func syncContinue(mgr *stack.Manager, gh *github.Client, useMerge bool, scope continueScope) error {
	scopedStacks := stacksInScope(mgr, scope)
	if len(scopedStacks) == 0 {
		ui.Info("No stacks in scope.")
		return nil
	}

	// When the user gave no scope flag, surface the resolved default so the
	// implicit blast radius of `--continue` (especially "all stacks" when run
	// from main) isn't a surprise. Explicit selectors don't need this — the
	// user already knows what they asked for.
	if scope.defaulted {
		switch scope.mode {
		case continueModeAll:
			ui.Info(fmt.Sprintf("--continue: no scope flag given, defaulting to all stacks (%d). Use -s/-c/-b/<hash> to scope.", len(scopedStacks)))
		case continueModeCurrentStack:
			if scope.stack != nil {
				ui.Info(fmt.Sprintf("--continue: scoped to current stack %s. Use -a to include all stacks.", scope.stack.DisplayName()))
			}
		}
	}

	// Fetch so the descendant re-sync can pick up any commits that landed on
	// origin/<branch> while the user was resolving the original conflict.
	// Manager.Fetch is deduped per process, so this is cheap on subsequent
	// internal calls.
	if err := mgr.Fetch(); err != nil {
		ui.Warn(fmt.Sprintf("fetch failed before --continue: %v (proceeding with cached refs)", err))
	}
	scopedSet := make(map[string]bool, len(scopedStacks))
	for _, s := range scopedStacks {
		scopedSet[s.Hash] = true
	}

	type conflictBranch struct {
		branch   *config.Branch
		stack    *config.Stack
		isRebase bool
		isMerge  bool
	}

	branchInProgress := func(b *config.Branch) (rebaseIP, mergeIP bool) {
		workdir := b.WorktreePath
		isCheckout := workdir == ""
		if isCheckout {
			workdir = mgr.GetRepoDir()
		}
		g := git.New(workdir)
		rebaseIP, _ = g.IsRebaseInProgress()
		mergeIP, _ = g.IsMergeInProgress()
		// For checkout-based branches we share the main repo with every other
		// checkout-based branch in this manager. Any in-progress rebase/merge
		// there is "the main repo's" — not necessarily this branch's. Without
		// disambiguation, a checkout-based sibling's mid-rebase would falsely
		// flag B as in-progress, and `RebaseContinue` would advance the wrong
		// branch's rebase and offer to push it under B's name.
		//
		// rebase: head-name in rebase-state files is canonical.
		// merge: HEAD is still on the branch ref during a merge conflict, so
		//   CurrentBranch is sufficient.
		if isCheckout {
			if rebaseIP {
				if name := g.BranchFromRebaseState(); name != "" && name != b.Name {
					rebaseIP = false
				}
			}
			if mergeIP {
				cur, _ := g.CurrentBranch()
				if cur != "" && cur != "HEAD" && cur != b.Name {
					mergeIP = false
				}
			}
		}
		return
	}

	// Collect in-progress branches across in-scope stacks. For "branch" mode
	// only consider that branch (other in-progress branches are out of scope).
	var found []conflictBranch
	for _, s := range scopedStacks {
		ordered := topoOrderStackBranches(s)
		for _, b := range ordered {
			if scope.mode == continueModeBranch && b.Name != scope.branchName {
				continue
			}
			rebaseIP, mergeIP := branchInProgress(b)
			if rebaseIP || mergeIP {
				found = append(found, conflictBranch{branch: b, stack: s, isRebase: rebaseIP, isMerge: mergeIP})
			}
		}
	}

	// Surface orphan ezstack autostashes for branches in scope that are NOT in
	// conflict — they were left behind by a prior aborted sync. We don't
	// auto-pop because the user may have edited the worktree after the stash
	// was created, so popping could surprise them. Just inform.
	//
	// Why `git -C b.WorktreePath` is correct: ezstack only ever creates an
	// autostash from inside b's own worktree (StashPush is invoked there),
	// so the saved diff applies cleanly to that worktree. Stashes are stored
	// at repo level (`refs/stash`) so `FindEzstackStash` works from any
	// worktree, but popping is only safe in the worktree the diff was taken
	// from — `b.WorktreePath` per ezstack's one-worktree-per-branch model.
	inProgressSet := make(map[string]bool, len(found))
	for _, cb := range found {
		inProgressSet[cb.branch.Name] = true
	}
	for _, s := range scopedStacks {
		for _, b := range s.Branches {
			if inProgressSet[b.Name] || b.WorktreePath == "" {
				continue
			}
			g := git.New(b.WorktreePath)
			if _, ok := g.FindEzstackStash(b.Name); ok {
				ui.Warn(fmt.Sprintf("Orphan ezstack autostash found for %s. Run `git -C %s stash pop` to restore, or `git stash drop` to discard.", b.Name, b.WorktreePath))
			}
		}
	}

	if len(found) == 0 {
		ui.Success("No in-progress rebase or merge found. Nothing to continue.")
		return nil
	}

	ui.Info(fmt.Sprintf("Found %d branch(es) with in-progress rebase/merge:", len(found)))
	for _, cb := range found {
		kind := "rebase"
		if cb.isMerge {
			kind = "merge"
		}
		fmt.Fprintf(os.Stderr, "  %s %s%s%s (%s in %s)\n",
			ui.IconBullet, ui.Bold, cb.branch.Name, ui.Reset, kind, cb.stack.DisplayName())
	}
	fmt.Fprintln(os.Stderr)

	successCount := 0
	stillInConflict := false
	hardErrorCount := 0
	var firstHardError string
	var continuedBranches []conflictBranch
	for _, cb := range found {
		branchWorkDir := cb.branch.WorktreePath
		if branchWorkDir == "" {
			branchWorkDir = mgr.GetRepoDir()
		}
		g := git.New(branchWorkDir)

		// Check for unresolved conflicts before attempting to continue.
		hasConflicts, _ := g.HasUnresolvedConflicts()
		if hasConflicts {
			ui.Warn(fmt.Sprintf("Skipping %s: still has unresolved conflicts in %s", cb.branch.Name, branchWorkDir))
			stillInConflict = true
			continue
		}

		var res git.ContinueResult
		if cb.isRebase {
			ui.Info(fmt.Sprintf("Continuing rebase for %s...", cb.branch.Name))
			res = g.RebaseContinue()
		} else {
			ui.Info(fmt.Sprintf("Continuing merge for %s...", cb.branch.Name))
			res = g.MergeContinue()
		}

		switch {
		case res.Done:
			ui.Success(fmt.Sprintf("Completed %s", cb.branch.Name))
			successCount++
			continuedBranches = append(continuedBranches, cb)

			// Pop this branch's autostash if one was created.
			if _, stashFound := g.FindEzstackStash(cb.branch.Name); stashFound {
				if err := g.StashPop(); err != nil {
					ui.Warn(fmt.Sprintf("Failed to pop autostash for %s: %v", cb.branch.Name, err))
				} else {
					ui.Info(fmt.Sprintf("Restored stashed changes for %s", cb.branch.Name))
				}
			}

			// Offer to push the freshly-completed branch.
			if cb.isRebase {
				OfferForcePush(cb.branch.Name, branchWorkDir, cb.branch.EffectiveRemote())
			} else {
				OfferPush(cb.branch.Name, branchWorkDir, cb.branch.EffectiveRemote())
			}
		case res.StillInConflict:
			ui.Warn(fmt.Sprintf("%s: paused on next commit's conflict — resolve in %s, then re-run `ezs sync --continue`", cb.branch.Name, branchWorkDir))
			stillInConflict = true
		default:
			ui.Error(fmt.Sprintf("Failed to continue %s: %v", cb.branch.Name, res.Err))
			hardErrorCount++
			if firstHardError == "" {
				firstHardError = fmt.Sprintf("%s: %v", cb.branch.Name, res.Err)
			}
		}
	}

	if successCount == 0 {
		if hardErrorCount > 0 {
			return errSyncContinueFailed(firstHardError)
		}
		if stillInConflict {
			return ErrSyncIncomplete
		}
		return nil
	}

	// Re-sync the full descendant subtree of each continued branch in topo
	// order. Skip descendants that are themselves mid-rebase — they'll be
	// resumed by the user's next `--continue` invocation. Stop walking a
	// subtree once any node hits a fresh conflict, since further descendants
	// would just re-derive the same problem.
	descendantConflict := false
	descendantHardError := false
	for _, cb := range continuedBranches {
		descendants := mgr.GetDescendants(cb.branch.Name)
		if len(descendants) == 0 {
			continue
		}
		// Filter to in-scope descendants. (Cross-scope descendants exist when
		// stacks share branches via reparenting; conservatively skip them in
		// branch / single-stack modes.)
		var inScope []*config.Branch
		for _, d := range descendants {
			ds := mgr.GetStackForBranch(d.Name)
			if ds != nil && scopedSet[ds.Hash] {
				inScope = append(inScope, d)
			}
		}
		if len(inScope) == 0 {
			continue
		}

		fmt.Fprintln(os.Stderr)
		names := make([]string, len(inScope))
		for i, d := range inScope {
			names[i] = d.Name
		}
		ui.Info(fmt.Sprintf("Re-syncing %d descendant(s) of %s: %s",
			len(inScope), cb.branch.Name, strings.Join(names, ", ")))

		// Track branches we've stopped descending into so we don't re-sync
		// their further descendants.
		stoppedSubtrees := make(map[string]bool)
		for _, child := range inScope {
			// Skip if any ancestor in this subtree was already stopped.
			// `seen` guards against a malformed tree producing a parent cycle —
			// shouldn't happen in practice (validated on stack ops), but a
			// blind walk would otherwise loop forever on corruption.
			ancestorStopped := false
			seen := make(map[string]bool)
			for parent := child.Parent; parent != ""; {
				if seen[parent] {
					break
				}
				seen[parent] = true
				if stoppedSubtrees[parent] {
					ancestorStopped = true
					break
				}
				parentBranch := mgr.GetBranch(parent)
				if parentBranch == nil {
					break
				}
				parent = parentBranch.Parent
			}
			if ancestorStopped {
				continue
			}

			// If the child is mid-rebase already, leave it for the user's next
			// --continue. Don't try to start a new rebase on top.
			rebaseIP, mergeIP := branchInProgress(child)
			if rebaseIP || mergeIP {
				ui.Warn(fmt.Sprintf("Skipping %s: already has its own in-progress rebase/merge — run `ezs sync --continue` after resolving", child.Name))
				stillInConflict = true
				stoppedSubtrees[child.Name] = true
				continue
			}

			// Autostash any uncommitted changes in the child's worktree before
			// the re-sync. The bulk-sync path autostashes per-branch via
			// SyncCallbacks; SyncBranch (called below) doesn't, so descendants
			// re-synced under --continue would otherwise hit `git merge
			// --ff-only`'s "would be overwritten" refusal during integrate, or
			// git's rebase dirty-tree complaint.
			//
			// Skipped when no dedicated worktree exists (checkout-based sync
			// requires the main repo to be clean — an existing constraint).
			didChildStash := false
			var childStashGit *git.Git
			if child.WorktreePath != "" {
				childStashGit = git.New(child.WorktreePath)
				if _, found := childStashGit.FindEzstackStash(child.Name); found {
					// A prior aborted sync already left an autostash. Don't
					// stack another one on top — the existing one still has
					// the user's changes. The orphan-stash banner above
					// already surfaced this to the user.
				} else if hasChanges, _ := childStashGit.HasChanges(); hasChanges {
					if err := childStashGit.StashPush(); err != nil {
						ui.Warn(fmt.Sprintf("Failed to autostash %s before re-sync: %v (refusing to rebase over uncommitted changes)", child.Name, err))
						stoppedSubtrees[child.Name] = true
						descendantConflict = true
						continue
					}
					didChildStash = true
				}
			}

			childResult, err := mgr.SyncBranch(child.Name, gh, useMerge)
			if err != nil {
				if didChildStash {
					if popErr := childStashGit.StashPop(); popErr != nil {
						ui.Warn(fmt.Sprintf("Failed to pop autostash for %s after sync error: %v (your changes are still in `git stash list`)", child.Name, popErr))
					}
				}
				ui.Warn(fmt.Sprintf("Failed to sync %s: %v", child.Name, err))
				stoppedSubtrees[child.Name] = true
				descendantHardError = true
				continue
			}
			childWorkDir := child.WorktreePath
			if childWorkDir == "" {
				childWorkDir = mgr.GetRepoDir()
			}
			switch {
			case childResult.HasConflict:
				// Leave the autostash in place — the user resolves the conflict
				// and the next successful --continue (or manual `git stash pop`)
				// restores their changes.
				ui.Warn(fmt.Sprintf("Conflict syncing %s — resolve in: %s, then re-run `ezs sync --continue`", child.Name, childWorkDir))
				if didChildStash {
					ui.Warn(fmt.Sprintf("Uncommitted changes were autostashed for %s; they will restore on the next successful sync, or run `git stash pop` manually.", child.Name))
				}
				stoppedSubtrees[child.Name] = true
				descendantConflict = true
			case childResult.Error != nil:
				if didChildStash {
					if popErr := childStashGit.StashPop(); popErr != nil {
						ui.Warn(fmt.Sprintf("Failed to pop autostash for %s: %v", child.Name, popErr))
					}
				}
				ui.Warn(fmt.Sprintf("Failed to sync %s: %v", child.Name, childResult.Error))
				stoppedSubtrees[child.Name] = true
				descendantHardError = true
			case childResult.Success:
				if didChildStash {
					if popErr := childStashGit.StashPop(); popErr != nil {
						ui.Warn(fmt.Sprintf("Failed to pop autostash for %s: %v", child.Name, popErr))
					} else {
						ui.Info(fmt.Sprintf("Restored stashed changes for %s", child.Name))
					}
				}
				ui.Success(fmt.Sprintf("Synced %s", child.Name))
				if useMerge {
					OfferPush(child.Name, childWorkDir, child.EffectiveRemote())
				} else {
					OfferForcePush(child.Name, childWorkDir, child.EffectiveRemote())
				}
			}
		}
	}

	// Update PR metadata for in-scope stacks.
	if gh != nil {
		for _, s := range scopedStacks {
			updatePRMetadata(gh, s, nil)
		}
	}

	// If the entire scope is now fully resolved (no in-progress branches and
	// no descendant conflicts or hard errors), clear PreSyncCommits for every
	// branch in the scope — the snapshots have served their purpose. Otherwise
	// leave them for the next --continue.
	if !stillInConflict && !descendantConflict && hardErrorCount == 0 && !descendantHardError {
		var toClear []string
		for _, s := range scopedStacks {
			for _, b := range s.Branches {
				toClear = append(toClear, b.Name)
			}
		}
		mgr.ClearPreSyncCommits(toClear)
	}

	fmt.Fprintln(os.Stderr)
	ui.Success(fmt.Sprintf("Continued %d branch(es)!", successCount))

	// Exit-code priority: hard errors (broken state) outrank conflicts (just
	// re-run after resolving). Scripts can use the exit code to decide
	// whether to retry automatically (3) or escalate to a human (1).
	if hardErrorCount > 0 || descendantHardError {
		if firstHardError != "" {
			return errSyncContinueFailed(firstHardError)
		}
		return errSyncContinueFailed("descendant re-sync failed (see log)")
	}
	if stillInConflict || descendantConflict {
		return ErrSyncIncomplete
	}
	return nil
}

// syncOntoParent syncs the current branch onto its parent (rebase or merge)
func syncOntoParent(mgr *stack.Manager, branch *config.Branch, useMerge bool) error {
	stack := mgr.GetStackForBranch(branch.Name)
	if stack != nil && branch.Parent == stack.Root {
		ui.Info(fmt.Sprintf("Parent is %s - use 'Auto-sync' to sync onto latest origin/%s", stack.Root, stack.Root))
		return nil
	}

	var confirmMsg string
	if useMerge {
		confirmMsg = fmt.Sprintf("Merge %s into %s", branch.Parent, branch.Name)
	} else {
		confirmMsg = fmt.Sprintf("Rebase %s onto %s", branch.Name, branch.Parent)
	}
	if !ui.ConfirmTUI(confirmMsg) {
		ui.Warn("Cancelled")
		return nil
	}

	if useMerge {
		ui.Info("Merging parent...")
	} else {
		ui.Info("Rebasing onto parent...")
	}
	if err := mgr.RebaseOnParent(useMerge); err != nil {
		// Interactive merge/rebase returns an exit status error on conflict.
		// The user sees the conflict output directly on the terminal, so
		// give them resolution instructions instead of a raw "exit status 1".
		// But if the error is NOT an exit status (e.g., GetCurrentStack failed),
		// propagate it as-is.
		errStr := err.Error()
		if strings.Contains(errStr, "exit status") {
			if useMerge {
				ui.Warn("Merge has conflicts. Resolve them, then run: git add . && git merge --continue")
			} else {
				ui.Warn("Rebase has conflicts. Resolve them, then run: git add . && git rebase --continue")
			}
			return nil
		}
		return err
	}
	worktreePath := branch.WorktreePath
	if worktreePath == "" {
		cwd, _ := os.Getwd()
		worktreePath = cwd
	}
	if useMerge {
		ui.Success("Merge complete")
		OfferPush(branch.Name, worktreePath, branch.EffectiveRemote())
	} else {
		ui.Success("Rebase complete")
		OfferForcePush(branch.Name, worktreePath, branch.EffectiveRemote())
	}
	return nil
}

// syncChildren rebases child branches onto the current branch
func syncChildren(mgr *stack.Manager, branch *config.Branch, useMerge bool) error {
	localChildren := mgr.GetChildren(branch.Name)

	if len(localChildren) == 0 {
		ui.Info("No local child branches to sync")
		return nil
	}

	var confirmMsg string
	if useMerge {
		confirmMsg = fmt.Sprintf("Merge %s into %d child branch(es)", branch.Name, len(localChildren))
	} else {
		confirmMsg = fmt.Sprintf("Rebase %d child branch(es) onto %s", len(localChildren), branch.Name)
	}
	if !ui.ConfirmTUI(confirmMsg) {
		ui.Warn("Cancelled")
		return nil
	}

	if useMerge {
		ui.Info("Merging into child branches...")
	} else {
		ui.Info("Rebasing child branches...")
	}
	results, err := mgr.RebaseChildren(useMerge)
	if err != nil {
		return err
	}

	hasConflicts := false
	successCount := 0
	var successfulBranches []string
	for _, r := range results {
		if r.Success {
			if useMerge {
				ui.Success(fmt.Sprintf("Merged into %s", r.Branch))
			} else {
				ui.Success(fmt.Sprintf("Rebased %s", r.Branch))
			}
			successCount++
			successfulBranches = append(successfulBranches, r.Branch)
		} else if r.HasConflict {
			ui.Warn(fmt.Sprintf("Conflict in %s", r.Branch))
			hasConflicts = true
		} else if r.Error != nil {
			ui.Error(fmt.Sprintf("Failed to sync %s: %v", r.Branch, r.Error))
		}
	}

	fmt.Fprintln(os.Stderr)
	if hasConflicts {
		if useMerge {
			ui.Warn("Some branches have conflicts. Resolve them and run 'git merge --continue' in each worktree.")
		} else {
			ui.Warn("Some branches have conflicts. Resolve them and run 'git rebase --continue' in each worktree.")
		}
	}
	if successCount > 0 {
		ui.Success(fmt.Sprintf("Synced %d child branch(es)!", successCount))

		// Offer to push successfully synced branches
		if len(successfulBranches) > 0 {
			getWorktree := func(branchName string) string {
				childBranch := mgr.GetBranch(branchName)
				if childBranch == nil {
					return ""
				}
				if childBranch.WorktreePath == "" {
					return mgr.GetRepoDir()
				}
				return childBranch.WorktreePath
			}
			getRemote := func(branchName string) string {
				childBranch := mgr.GetBranch(branchName)
				if childBranch == nil {
					return "origin"
				}
				return childBranch.EffectiveRemote()
			}
			if useMerge {
				OfferPushMultiple(successfulBranches, getWorktree, getRemote)
			} else {
				OfferForcePushMultiple(successfulBranches, getWorktree, getRemote)
			}
		}
	}

	return nil
}

// syncCurrentBranch syncs only the current branch (wherever it is in the chain)
func syncCurrentBranch(mgr *stack.Manager, gh *github.Client, branch *config.Branch, cwd string, autostash bool, useMerge bool) error {
	ui.Info("Fetching latest changes...")
	// Use Manager.Fetch() so the fetch is deduped if already done earlier
	if err := mgr.Fetch(); err != nil {
		return fmt.Errorf("failed to fetch from remote: %w. Check your network connection and that the remote is accessible", err)
	}
	// If this branch's configured remote isn't `origin` (e.g. a fork), also
	// fetch that remote so the ahead-behind check below can see any commits
	// a teammate pushed there. Failure is non-fatal: detection just can't
	// flag a remote-pull, and the user gets an "up to date" message they
	// can ignore by running `git fetch <remote>` themselves.
	if r := branch.EffectiveRemote(); r != "" && r != "origin" && branch.CanPush() {
		_ = mgr.FetchRemote(r)
	}

	g := git.New(cwd)
	syncInfo := mgr.DetectSyncNeededForBranch(branch.Name, gh)
	if syncInfo == nil || !syncInfo.NeedsSync {
		ui.Success("Current branch is up to date. No sync needed.")
		return nil
	}

	if syncInfo.MergedParent != "" {
		if useMerge {
			ui.Info(fmt.Sprintf("Parent %s was merged to %s. Will merge %s.", syncInfo.MergedParent, syncInfo.StackRoot, syncInfo.StackRoot))
		} else {
			ui.Info(fmt.Sprintf("Parent %s was merged to %s. Will rebase onto %s.", syncInfo.MergedParent, syncInfo.StackRoot, syncInfo.StackRoot))
		}
	} else if syncInfo.BehindParent != "" {
		ui.Info(fmt.Sprintf("Current branch is %d commits behind %s.", syncInfo.BehindBy, syncInfo.BehindParent))
	} else if syncInfo.BehindBy > 0 {
		ui.Info(fmt.Sprintf("Current branch is %d commits behind origin/%s.", syncInfo.BehindBy, syncInfo.StackRoot))
	}
	if syncInfo.BehindRemote > 0 {
		ui.Info(fmt.Sprintf("Current branch is %d commits behind origin/%s (collaborator pushed). Will fast-forward.", syncInfo.BehindRemote, branch.Name))
	}

	if !ui.ConfirmTUI("Sync current branch") {
		ui.Warn("Cancelled")
		return nil
	}

	// Autostash: stash uncommitted changes before sync
	didStash := false
	if autostash {
		// Check for orphaned ezstack stash from a previous conflicted sync
		if _, found := g.FindEzstackStash(branch.Name); found {
			ui.Warn(fmt.Sprintf("Existing autostash found for %s (from a previous sync)", branch.Name))
			ui.Info("Skipping autostash. Run 'git stash pop' in the worktree to restore, or 'git stash drop' to discard.")
			// Don't create another stash on top — the old one already has the user's changes
		} else if hasChanges, _ := g.HasChanges(); hasChanges {
			// Mirror the engine's behavior in internal/stack/sync.go: refuse to
			// rebase over uncommitted changes if stash fails, otherwise the
			// rebase could clobber the user's work.
			if err := g.StashPush(); err != nil {
				return fmt.Errorf("autostash failed for %s: %w (refusing to rebase over uncommitted changes)", branch.Name, err)
			}
			didStash = true
			ui.Info("Stashed uncommitted changes")
		}
	}

	ui.Info("Syncing current branch...")
	result, err := mgr.SyncBranch(branch.Name, gh, useMerge)
	if err != nil {
		if didStash {
			if popErr := g.StashPop(); popErr != nil {
				ui.Warn(fmt.Sprintf("Failed to pop stash: %v (your changes are still in 'git stash list')", popErr))
			}
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
		resultRemote := result.Remote
		if resultRemote == "" {
			resultRemote = ResolveBranchRemote(g, mgr, result.Branch)
		}
		if useMerge {
			OfferPush(result.Branch, result.WorktreePath, resultRemote)
		} else {
			OfferForcePush(result.Branch, result.WorktreePath, resultRemote)
		}
	} else if result.HasConflict {
		ui.Warn(fmt.Sprintf("Conflict in %s", result.Branch))
		if result.WorktreePath != "" {
			fmt.Fprintf(os.Stderr, "%sResolve in:%s %s\n", ui.Gray, ui.Reset, result.WorktreePath)
		}
		if useMerge {
			fmt.Fprintf(os.Stderr, "%sTo resolve: fix conflicts, then run 'git merge --continue'%s\n", ui.Gray, ui.Reset)
		} else {
			fmt.Fprintf(os.Stderr, "%sTo resolve: fix conflicts, then run 'git rebase --continue'%s\n", ui.Gray, ui.Reset)
		}
		if didStash {
			ui.Warn("Uncommitted changes were stashed. They will be restored on next successful sync, or run 'git stash pop' manually.")
		}
	} else if result.Error != nil {
		ui.Error(fmt.Sprintf("Failed to sync %s: %v", result.Branch, result.Error))
	}

	return nil
}

// printSyncResults prints the results of a sync operation
func printSyncResults(results []stack.RebaseResult, useMerge bool) {
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
		} else if r.Error != nil {
			ui.Error(fmt.Sprintf("Failed to sync %s: %v", r.Branch, r.Error))
		}
	}

	if len(conflicts) > 0 {
		fmt.Fprintln(os.Stderr)
		// Group conflicts by stack
		type stackConflicts struct {
			name     string
			branches []stack.RebaseResult
		}
		var ordered []stackConflicts
		seen := make(map[string]int)
		for _, c := range conflicts {
			name := c.StackName
			if name == "" {
				name = "(unknown stack)"
			}
			if idx, ok := seen[name]; ok {
				ordered[idx].branches = append(ordered[idx].branches, c)
			} else {
				seen[name] = len(ordered)
				ordered = append(ordered, stackConflicts{name: name, branches: []stack.RebaseResult{c}})
			}
		}

		fmt.Fprintf(os.Stderr, "%s%s Stacks with conflicts (%d):%s\n", ui.Yellow, ui.IconConflict, len(ordered), ui.Reset)
		for _, sc := range ordered {
			fmt.Fprintf(os.Stderr, "\n  %s%sStack %s%s\n", ui.Red, ui.Bold, sc.name, ui.Reset)
			for _, c := range sc.branches {
				fmt.Fprintf(os.Stderr, "    %s %s%s%s\n", ui.IconBullet, ui.Bold, c.Branch, ui.Reset)
				if c.WorktreePath != "" {
					fmt.Fprintf(os.Stderr, "      %sResolve in:%s %s\n", ui.Gray, ui.Reset, c.WorktreePath)
				}
			}
		}
		fmt.Fprintln(os.Stderr)
		if useMerge {
			fmt.Fprintf(os.Stderr, "%sTo resolve: cd to each worktree, fix conflicts, then run 'git merge --continue'%s\n", ui.Gray, ui.Reset)
		} else {
			fmt.Fprintf(os.Stderr, "%sTo resolve: cd to each worktree, fix conflicts, then run 'git rebase --continue'%s\n", ui.Gray, ui.Reset)
		}
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
				needsCd, err := mgr.DeleteStack(info.StackHash)
				if err != nil {
					ui.Warn(fmt.Sprintf("Failed to delete stack '%s': %v", displayName, err))
				} else {
					ui.Success(fmt.Sprintf("Removed fully merged stack '%s'", displayName))
					if needsCd {
						EmitCd(mgr.GetRepoDir())
					}
				}
			} else {
				mgr.DeclineStackDelete(info.StackHash)
			}
		} else {
			// Everything already cleaned up - remove stack from config automatically
			needsCd, err := mgr.DeleteStack(info.StackHash)
			if err != nil {
				ui.Warn(fmt.Sprintf("Failed to remove stack '%s' from config: %v", displayName, err))
			} else {
				ui.Success(fmt.Sprintf("Removed fully merged stack '%s' (all branches already cleaned up)", displayName))
				if needsCd {
					EmitCd(mgr.GetRepoDir())
				}
			}
		}
	}
}
