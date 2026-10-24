package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ghstack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/github"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/hooks"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

// Sync restacks branches with `gh stack rebase`, pushes them, cleans up merged
// branches, and refreshes PR bases and the GitHub stack links.
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
    -s, --stack            Sync current stack
    -a, --all              Sync ALL stacks
    -c, --current          Sync current branch only (rebase it and the branches below it)
    -b, --branch <name>    Sync a specific branch by name (rebase onto parent + cascade to children)
    -p, --parent           Rebase current branch onto its parent (no trunk update)
    -C, --children         Rebase child branches onto current branch
    --continue             Continue a sync that stopped on a conflict, then finish syncing. Honors -s, -a, -c, -b, and positional <hash-prefix> to limit the scope.
    --abort                Abort a sync that stopped on a conflict and restore every branch
    --no-push              Rebase only; don't push rebased branches
    --stats                Print commits-per-branch summary after syncing
    --squash               Squash each child's commits before rebasing onto parent
    --no-delete-local      Don't delete local branches after their PRs are merged
    --include-remote-worktrees   Also rebase and push pickup branches
                                 (ezs new origin/<branch> / -r). Left as-is
                                 by default so another contributor's
                                 history is never rewritten.
    --dry-run              Preview what would be synced without making changes
    --json                 Output dry-run results as JSON (requires --dry-run)
    -h, --help             Show this help message

%sDESCRIPTION%s
    Restacks your branches with gh stack (github/gh-stack, required): fetches
    the base branch, rebases every branch onto its updated parent in the
    worktree that holds it, and drops the commits of parents whose PRs were
    merged. Rebased branches are then pushed with --force-with-lease (asks
    once; -y or --no-push to skip the prompt). Without flags, shows an
    interactive menu.

    Tree-shaped stacks map to one gh stack stack per root-to-leaf path; a
    branch with several children appears in each path below it, so every
    subtree is restacked when it changes.

    On a conflict, sync stops in the conflicted worktree. Resolve it, stage
    the files, then run "ezs sync --continue" (or "ezs sync --abort").

    Worktrees with uncommitted changes block the rebase; commit or stash
    them first.

%sEXAMPLES%s
    ezs sync              Interactive menu
    ezs sync a1b2c        Sync stack matching hash prefix
    ezs sync -s           Sync current stack
    ezs sync -a           Sync all stacks
    ezs sync -c           Sync current branch only
    ezs sync -b feat-x    Sync a specific branch by name
    ezs sync -p           Rebase current onto parent
    ezs sync -C           Rebase children onto current
    ezs sync --continue   Finish a sync after resolving conflicts
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
	noDeleteLocal := fs.Bool("no-delete-local", false, "Don't delete local branches after their PRs are merged")
	dryRunFlag := fs.Bool("dry-run", false, "Preview what would be synced")
	continueFlag := fs.Bool("continue", false, "Continue after resolving conflicts")
	abortFlag := fs.Bool("abort", false, "Abort a sync stopped on a conflict")
	noPushFlag := fs.Bool("no-push", false, "Don't push rebased branches")
	jsonFlag := fs.Bool("json", false, "Output dry-run results as JSON")
	includeRemoteFlag := fs.Bool("include-remote-worktrees", false, "Also rebase and push pickup branches")

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
	if *jsonFlag && !*dryRunFlag {
		return fmt.Errorf("--json requires --dry-run")
	}
	if *continueFlag && *abortFlag {
		return fmt.Errorf("cannot use both --continue and --abort")
	}
	if *dryRunFlag && (*continueFlag || *abortFlag) {
		return fmt.Errorf("--dry-run cannot be combined with --continue or --abort")
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

	sel := syncSelection{
		all: *allFlag, stack: *stackFlag, current: *currentFlag,
		branch: *branchFlag, parent: *parentFlag, children: *childrenFlag,
		args: fs.Args(),
	}

	if *dryRunFlag {
		// Read-only, so no menu: default to the current stack (all from main).
		target, err := resolveDefaultTarget(mgr, sel)
		if err != nil {
			return err
		}
		return syncDryRun(g, mgr, target, *jsonFlag)
	}

	if err := requireGHStack(); err != nil {
		return err
	}

	// One sync at a time: stacks.json and the gh-stack catalog are shared state.
	if cfgDir, cfgErr := config.ConfigDir(); cfgErr == nil {
		lock, lockErr := config.AcquireSyncLock(filepath.Join(cfgDir, "stacks.json"))
		if lockErr != nil {
			return lockErr
		}
		defer lock.Release()
	}

	warnLegacySyncStrategy(mgr)

	opts := syncOptions{
		deleteLocal:   !*noDeleteLocal,
		push:          pushAsk,
		includeRemote: *includeRemoteFlag,
	}
	if *noPushFlag {
		opts.push = pushNever
	}

	if *abortFlag {
		if !ghstackPaused(mgr) {
			return fmt.Errorf("no stopped sync to abort")
		}
		if err := ghstack.New(mgr.GetRepoDir()).Abort(); err != nil {
			return err
		}
		clearPausedScope(mgr)
		ui.Success("Sync aborted; branches restored")
		return nil
	}

	hookCtx := BuildHookContext()

	// --continue resumes a cascade whose pre-sync hook already fired, and
	// whose --squash already ran; only post-sync fires here.
	if *continueFlag {
		if !ghstackPaused(mgr) {
			return fmt.Errorf("no stopped sync to continue (resolve plain git rebases with `git rebase --continue`)")
		}
		if err := ghstack.New(mgr.GetRepoDir()).Continue(); err != nil {
			return restackError(err)
		}
		defer func() {
			if hookErr := hooks.Run("post-sync", hookCtx); hookErr != nil {
				ui.Warn(hookErr.Error())
			}
		}()
		// The paused cascade finished its own chain; re-running the sync for
		// the same scope restacks anything after it (nested chains, other
		// stacks) and does the push / cleanup / PR steps. Restacking an
		// up-to-date chain is a no-op.
		target := loadPausedScope(mgr)
		if target == nil || sel.any() {
			if target, err = resolveDefaultTarget(mgr, sel); err != nil {
				return err
			}
		}
		return runSync(g, mgr, target, opts)
	}

	target, err := resolveSyncTarget(mgr, sel)
	if err != nil {
		return err
	}
	if target == nil { // interactive menu cancelled
		return nil
	}

	if err := hooks.Run("pre-sync", hookCtx); err != nil {
		return err
	}
	defer func() {
		if hookErr := hooks.Run("post-sync", hookCtx); hookErr != nil {
			ui.Warn(hookErr.Error())
		}
	}()
	// Registered after the post-sync defer so stats print first.
	if *statsFlag {
		defer printSyncStats(cwd)
	}
	if *squashFlag {
		ui.Info("--squash: squashing children before sync")
		if err := squashStackChildren(mgr); err != nil {
			return err
		}
	}
	return runSync(g, mgr, target, opts)
}

// warnLegacySyncStrategy tells users who had set sync_strategy=merge that
// syncs now always rebase (and force-push), once: the key is then dropped.
func warnLegacySyncStrategy(mgr *stack.Manager) {
	cfg := mgr.GetConfig()
	rc := cfg.GetRepoConfig(mgr.GetRepoDir())
	if rc == nil || rc.LegacySyncStrategy == "" {
		return
	}
	if rc.LegacySyncStrategy == "merge" {
		ui.Warn("sync_strategy=merge is no longer supported: ezs sync now rebases with gh stack and pushes with --force-with-lease. The setting has been removed from your config.")
	}
	rc.LegacySyncStrategy = ""
	cfg.SetRepoConfig(mgr.GetRepoDir(), rc)
	if err := cfg.Save(); err != nil {
		ui.Warn(fmt.Sprintf("Could not update config: %v", err))
	}
}

// syncSelection is the raw scope requested on the command line.
type syncSelection struct {
	all, stack, current, parent, children bool
	branch                                string
	args                                  []string
}

func (s syncSelection) any() bool {
	return s.all || s.stack || s.current || s.parent || s.children || s.branch != "" || len(s.args) > 0
}

// syncMode is what part of the stack(s) a sync restacks.
type syncMode int

const (
	modeStacks   syncMode = iota // every chain of the selected stacks
	modeCurrent                  // bottom of the branch's chain up to the branch
	modeBranch                   // the branch's whole chain + chains nested above it
	modeParent                   // only the branch onto its parent
	modeChildren                 // only the branch's descendants onto it
)

// syncTarget is a resolved sync scope.
type syncTarget struct {
	mode   syncMode
	stacks []*config.Stack // stacks touched (for cleanup / PR refresh)
	branch *config.Branch  // anchor for the branch modes
}

type syncOptions struct {
	deleteLocal   bool
	push          pushPolicy
	includeRemote bool // rebase and push pickup branches too
}

// resolveSyncTarget turns flags (or the interactive menu, when no flag is
// given) into a syncTarget. A nil target with nil error means the user
// cancelled the menu.
func resolveSyncTarget(mgr *stack.Manager, sel syncSelection) (*syncTarget, error) {
	if sel.branch != "" {
		b := mgr.GetBranch(sel.branch)
		s := mgr.FindStackForBranch(sel.branch)
		if b == nil || s == nil {
			return nil, fmt.Errorf("branch %q not found in any stack", sel.branch)
		}
		return &syncTarget{mode: modeBranch, stacks: []*config.Stack{s}, branch: b}, nil
	}
	if len(sel.args) > 0 {
		s, err := mgr.GetStackByHash(sel.args[0])
		if err != nil {
			return nil, err
		}
		return &syncTarget{mode: modeStacks, stacks: []*config.Stack{s}}, nil
	}
	if sel.all {
		return allStacksTarget(mgr)
	}

	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		// Not in a stack worktree.
		if sel.stack {
			return allStacksTarget(mgr)
		}
		if sel.current || sel.parent || sel.children {
			return nil, err
		}
		return syncMenuFromMain(mgr)
	}
	one := []*config.Stack{currentStack}
	switch {
	case sel.stack:
		return &syncTarget{mode: modeStacks, stacks: one}, nil
	case sel.current:
		return &syncTarget{mode: modeCurrent, stacks: one, branch: branch}, nil
	case sel.parent:
		return &syncTarget{mode: modeParent, stacks: one, branch: branch}, nil
	case sel.children:
		return &syncTarget{mode: modeChildren, stacks: one, branch: branch}, nil
	}
	return syncMenuInStack(mgr, currentStack, branch)
}

// resolveDefaultTarget is resolveSyncTarget without menus (for --continue
// and --dry-run): with no scope flag it targets the current stack, or every
// stack when run outside one.
func resolveDefaultTarget(mgr *stack.Manager, sel syncSelection) (*syncTarget, error) {
	if sel.any() {
		return resolveSyncTarget(mgr, sel)
	}
	if s, _, err := mgr.GetCurrentStack(); err == nil {
		return &syncTarget{mode: modeStacks, stacks: []*config.Stack{s}}, nil
	}
	return allStacksTarget(mgr)
}

func allStacksTarget(mgr *stack.Manager) (*syncTarget, error) {
	stacks := mgr.ListStacks()
	if len(stacks) == 0 {
		return nil, fmt.Errorf("no stacks found. Create a branch first with: ezs new <branch-name>")
	}
	return &syncTarget{mode: modeStacks, stacks: stacks}, nil
}

func syncMenuFromMain(mgr *stack.Manager) (*syncTarget, error) {
	stacks := mgr.ListStacks()
	if len(stacks) == 0 {
		ui.Info("No stacks found. Create a branch first with: ezs new <branch-name>")
		return nil, nil
	}
	options := []string{
		fmt.Sprintf("%s  Sync ALL stacks", ui.IconSync),
		fmt.Sprintf("%s  Choose a stack to sync", ui.IconStack),
	}
	selected, err := ui.SelectOptionWithBack(options, "Sync from main - what would you like to do?")
	if err != nil {
		return nil, err
	}
	if selected == 0 {
		return &syncTarget{mode: modeStacks, stacks: stacks}, nil
	}
	s, err := ui.SelectStack(stacks, "Select a stack to sync")
	if err != nil {
		return nil, err
	}
	return &syncTarget{mode: modeStacks, stacks: []*config.Stack{s}}, nil
}

func syncMenuInStack(mgr *stack.Manager, s *config.Stack, branch *config.Branch) (*syncTarget, error) {
	type option struct {
		label  string
		target *syncTarget
	}
	one := []*config.Stack{s}
	opts := []option{
		{fmt.Sprintf("%s  Sync current stack", ui.IconSync), &syncTarget{mode: modeStacks, stacks: one}},
		{fmt.Sprintf("%s  Sync ALL stacks", ui.IconSync), &syncTarget{mode: modeStacks, stacks: mgr.ListStacks()}},
		{fmt.Sprintf("%s  Sync current branch only", ui.IconSync), &syncTarget{mode: modeCurrent, stacks: one, branch: branch}},
	}
	if branch.Parent != "" && branch.Parent != s.Root {
		opts = append(opts, option{fmt.Sprintf("%s  Rebase current branch onto parent (%s)", ui.IconUp, branch.Parent),
			&syncTarget{mode: modeParent, stacks: one, branch: branch}})
	}
	if n := len(mgr.GetChildren(branch.Name)); n > 0 {
		opts = append(opts, option{fmt.Sprintf("%s  Rebase %d child branch(es) onto current", ui.IconDown, n),
			&syncTarget{mode: modeChildren, stacks: one, branch: branch}})
	}
	labels := make([]string, len(opts))
	for i, o := range opts {
		labels[i] = o.label
	}
	selected, err := ui.SelectOptionWithBack(labels, "What would you like to do?")
	if err != nil {
		return nil, err
	}
	return opts[selected].target, nil
}

// ghstackPaused reports whether a gh-stack cascade is stopped on a conflict.
func ghstackPaused(mgr *stack.Manager) bool {
	dir, err := ghstack.CommonDir(mgr.GetRepoDir())
	return err == nil && ghstack.RebaseInProgress(dir)
}

// restackSteps plans the gh-stack rebases for a target (modeParent is a
// single-branch move, see moveOntoParent).
//
// gh-stack picks the stack from the branch it is given and refuses one that
// sits in several stacks — a tree fork point is a layer of every chain below
// it — so steps only anchor on a branch that is Unique, falling back to the
// chain's topmost unique layer (restacking more of the chain; layers already
// stacked are no-ops).
func restackSteps(g *git.Git, mgr *stack.Manager, chains []ghstack.Chain, t *syncTarget) ([]restackStep, error) {
	if t.mode == modeStacks {
		return wholeChainSteps(g, mgr, chainsForStacks(chains, t.stacks), chains), nil
	}
	name := t.branch.Name
	c, ok := ghstack.ChainFor(chains, name)
	if !ok {
		return nil, fmt.Errorf("branch %q has nothing to sync (merged, or not tracked by gh stack)", name)
	}
	others := ghstack.ChainsSharing(chains, name) // chains of name's other subtrees

	switch t.mode {
	case modeCurrent:
		if !ghstack.Unique(chains, name) {
			// gh-stack can't anchor on a fork point; restack every chain
			// through it so no subtree is left on its old tip.
			ui.Info(fmt.Sprintf("'%s' has several child branches; restacking all of its subtrees", name))
			return wholeChainSteps(g, mgr, append([]ghstack.Chain{c}, others...), chains), nil
		}
		return []restackStep{{c, ghstack.RebaseOptions{Branch: name, Remote: trunkRemote(g, mgr, c), Downstack: true}}}, nil

	case modeChildren:
		// Restack only what is above name: in its own chain from the layer
		// right above it, in each other subtree's chain from its first own
		// layer. Inter-branch rebases only; name itself is not moved.
		var steps []restackStep
		active := c.Active()
		if i := indexOf(active, name); i+1 < len(active) {
			steps = append(steps, upstackStep(g, mgr, chains, c, active[i+1]))
		}
		for _, o := range others {
			if own := o.Own(); len(own) > 0 {
				steps = append(steps, upstackStep(g, mgr, chains, o, own[0]))
			}
		}
		return steps, nil

	case modeBranch:
		return wholeChainSteps(g, mgr, append([]ghstack.Chain{c}, others...), chains), nil
	}
	return nil, fmt.Errorf("unknown sync mode %d", t.mode)
}

// upstackStep rebases c from `from` to its top onto the layers below
// (--upstack --no-trunk). When gh-stack can't address `from` (it's a fork
// point), the whole chain is restacked without the trunk instead.
func upstackStep(g *git.Git, mgr *stack.Manager, chains []ghstack.Chain, c ghstack.Chain, from string) restackStep {
	o := ghstack.RebaseOptions{Branch: from, Remote: trunkRemote(g, mgr, c), Upstack: true, NoTrunk: true}
	if !ghstack.Unique(chains, from) {
		o.Branch, o.Upstack = chainAnchor(chains, c), false
	}
	return restackStep{c, o}
}

// moveOntoParent rebases only branch's own commits onto its parent — the
// commits after its recorded base (gh-stack's record of the parent tip it was
// last stacked on), or after the fork point with the parent. gh-stack can't
// express this one-branch move: it always rebases a stack's bottom onto the
// trunk with a plain rebase, and skips a branch whose parent tip it already
// contains. Runs in the worktree that holds the branch.
func moveOntoParent(mgr *stack.Manager, branch, oldParent, newParent string) error {
	repo := git.New(mgr.GetRepoDir())
	newRef := parentTipRef(repo, mgr, newParent)
	// The branch's own commits start after: the old parent's tip when the
	// branch contains it; else the recorded base (the old parent tip it was
	// last stacked on — the parent has since been rewritten or merged); else
	// the fork point. Same order gh-stack itself uses.
	from := oldParent
	if from == "" { // a standalone branch joining a stack
		from = newParent
	}
	oldTip := parentTipRef(repo, mgr, from)
	var oldBase string
	if ok, _ := repo.IsAncestor(oldTip, "refs/heads/"+branch); ok {
		oldBase, _ = repo.RunCapture("rev-parse", oldTip)
	} else if rb := ghstack.RecordedBase(mgr.GetRepoDir(), branch); rb != "" {
		if ok, _ := repo.IsAncestor(rb, "refs/heads/"+branch); ok {
			oldBase = rb
		}
	}
	if oldBase == "" {
		oldBase, _ = repo.RunCapture("merge-base", "refs/heads/"+branch, oldTip)
	}
	if oldBase == "" {
		return fmt.Errorf("cannot find where '%s' forked from '%s'", branch, oldParent)
	}
	dir := checkedOutDir(repo, []string{branch})
	if dir == "" {
		dir = mgr.GetRepoDir()
	}
	wg := git.New(dir)
	if dirty, _ := wg.HasChanges(); dirty {
		return fmt.Errorf("'%s' has uncommitted changes in %s; commit or stash them first", branch, dir)
	}
	ui.Info(fmt.Sprintf("Rebasing %s onto %s", branch, newParent))
	if _, err := wg.RunCapture("rebase", "--onto", newRef, oldBase, branch); err != nil {
		if wg.BranchFromRebaseState() != "" {
			return ui.NewExitError(ui.ExitConflict,
				"rebase of '%s' stopped on a conflict in %s: resolve it, `git add` the files and run `git rebase --continue` there, then `ezs sync -C` from that worktree to restack its children", branch, dir)
		}
		return fmt.Errorf("rebase %s onto %s: %w", branch, newParent, err)
	}
	return nil
}

// parentTipRef is the ref a branch should sit on for parent: the local
// branch for a stack branch, the remote-tracking ref for a stack root (the
// base branch) when there is one.
func parentTipRef(g *git.Git, mgr *stack.Manager, parent string) string {
	if mgr.GetBranch(parent) == nil {
		if r := defaultRemote(g); r != "" && g.RefExists("refs/remotes/"+r+"/"+parent) {
			return "refs/remotes/" + r + "/" + parent
		}
	}
	return "refs/heads/" + parent
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// runSync is the whole sync pipeline for a resolved target.
func runSync(g *git.Git, mgr *stack.Manager, t *syncTarget, opts syncOptions) error {
	cwd, _ := os.Getwd()
	gh, _ := newGitHubClient(g)
	if gh != nil {
		gh = gh.WithPRCache() // detection, cleanup and PR refresh share lookups
	}

	// Find merged PRs before restacking. The cleanup list is computed first
	// and the merged set is only a hint to the gh-stack projection (so it
	// drops their commits even when gh-stack can't see PR state itself);
	// nothing is marked merged in ezstack's model until the user confirms
	// the cleanup.
	var merged []stack.MergedBranchInfo
	var mergedPRs map[string]bool
	if gh != nil {
		if opts.deleteLocal {
			var err error
			if merged, err = mgr.DetectMergedBranchesForStacks(gh, t.stacks); err != nil {
				ui.Warn(fmt.Sprintf("Could not check for merged branches: %v", err))
			}
		}
		mergedPRs = mergedPRBranches(gh, t.stacks)
	}

	chains, err := reconcileGHStackMerged(mgr, mergedPRs)
	if err != nil {
		return err
	}
	p := newPusher(g, mgr, opts.push, chains)
	p.includeRemote = opts.includeRemote

	if t.mode == modeParent {
		if c, ok := ghstack.ChainFor(chains, t.branch.Name); ok {
			integrateRemoteCommits(g, mgr, []ghstack.Chain{c}, p)
		}
		if err := syncOntoParent(g, mgr, t.branch, p, chains); err != nil {
			return err
		}
	} else {
		steps, err := restackSteps(g, mgr, chains, t)
		if err != nil {
			return err
		}
		if len(steps) > 0 {
			limitPushes(mgr, p, t)
			integrateRemoteCommits(g, mgr, stepChains(steps), p)
			if err := runRestack(g, mgr, steps, p); err != nil {
				if errors.Is(err, errSyncConflict) {
					savePausedScope(mgr, t)
				}
				return err
			}
			ui.Success("Restack complete")
		}
	}
	clearPausedScope(mgr)

	if len(merged) > 0 {
		printMergedBranchesList(merged)
		handleMergedBranchCleanup(mgr, merged, t.stacks, cwd)
	}
	cleanupFullyMergedStacks(mgr, t.stacks)

	// Cleanup may have removed branches or whole stacks; re-read the model.
	refreshGHStack(mgr)
	if gh != nil {
		for _, s := range t.stacks {
			if live := mgr.GetStackByHashExact(s.Hash); live != nil {
				updatePRMetadata(gh, mgr, live)
			}
		}
	}
	return nil
}

// limitPushes restricts a branch-scoped sync to pushing what it restacked:
// -C the branch's descendants, -c the branch and the branches below it.
func limitPushes(mgr *stack.Manager, p *pusher, t *syncTarget) {
	switch t.mode {
	case modeChildren:
		p.only = map[string]bool{}
		for _, d := range mgr.GetDescendants(t.branch.Name) {
			p.only[d.Name] = true
		}
	case modeCurrent:
		p.only = map[string]bool{}
		for b := t.branch; b != nil; b = mgr.GetBranch(b.Parent) {
			if p.only[b.Name] {
				break
			}
			p.only[b.Name] = true
		}
	}
}

// stepChains returns the distinct chains of steps.
func stepChains(steps []restackStep) []ghstack.Chain {
	var out []ghstack.Chain
	for _, s := range steps {
		out = append(out, s.chain)
	}
	return out
}

// mergedPRBranches returns the branches of stacks, not yet marked merged,
// whose PRs are merged on GitHub. PR lookups run in parallel.
func mergedPRBranches(gh *github.Client, stacks []*config.Stack) map[string]bool {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = map[string]bool{}
	)
	sem := make(chan struct{}, 10)
	for _, s := range stacks {
		for _, b := range s.Branches {
			if b.PRNumber == 0 || b.IsMerged {
				continue
			}
			wg.Add(1)
			go func(name string, number int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if pr, err := gh.GetPR(number); err == nil && pr.Merged {
					mu.Lock()
					out[name] = true
					mu.Unlock()
				}
			}(b.Name, b.PRNumber)
		}
	}
	wg.Wait()
	return out
}

// syncOntoParent is `ezs sync -p`: move only the branch onto its parent
// (no trunk update, children untouched), then offer to push it.
func syncOntoParent(g *git.Git, mgr *stack.Manager, b *config.Branch, p *pusher, chains []ghstack.Chain) error {
	if s := mgr.GetStackForBranch(b.Name); s != nil && b.Parent == s.Root {
		ui.Info(fmt.Sprintf("Parent is %s - use 'ezs sync -s' to sync onto the latest %s", s.Root, s.Root))
		return nil
	}
	if err := moveOntoParent(mgr, b.Name, b.Parent, b.Parent); err != nil {
		return err
	}
	ui.Success(fmt.Sprintf("Rebased %s onto %s", b.Name, b.Parent))
	p.only = map[string]bool{b.Name: true}
	if c, ok := ghstack.ChainFor(chains, b.Name); ok {
		if err := p.pushChain(c); err != nil {
			ui.Warn(err.Error())
		}
	}
	return nil
}

// pausedScope is the target of a restack that stopped on a conflict, so
// `ezs sync --continue` without flags finishes the same operation (e.g. a
// commit's child restack) instead of a full stack sync.
type pausedScope struct {
	Mode   syncMode `json:"mode"`
	Branch string   `json:"branch,omitempty"`
	Stacks []string `json:"stacks,omitempty"`
}

func pausedScopePath(mgr *stack.Manager) string {
	dir, err := ghstack.CommonDir(mgr.GetRepoDir())
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "ezstack-paused-sync")
}

func savePausedScope(mgr *stack.Manager, t *syncTarget) {
	path := pausedScopePath(mgr)
	if path == "" {
		return
	}
	ps := pausedScope{Mode: t.mode}
	if t.branch != nil {
		ps.Branch = t.branch.Name
	}
	for _, s := range t.stacks {
		ps.Stacks = append(ps.Stacks, s.Hash)
	}
	if data, err := json.Marshal(ps); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
}

func clearPausedScope(mgr *stack.Manager) {
	if path := pausedScopePath(mgr); path != "" {
		_ = os.Remove(path)
	}
}

// loadPausedScope returns the saved target, or nil when there is none or it
// no longer resolves (a branch or stack was removed meanwhile).
func loadPausedScope(mgr *stack.Manager) *syncTarget {
	path := pausedScopePath(mgr)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var ps pausedScope
	if json.Unmarshal(data, &ps) != nil {
		return nil
	}
	t := &syncTarget{mode: ps.Mode}
	for _, h := range ps.Stacks {
		if s := mgr.GetStackByHashExact(h); s != nil {
			t.stacks = append(t.stacks, s)
		}
	}
	if ps.Branch != "" {
		if t.branch = mgr.GetBranch(ps.Branch); t.branch == nil {
			return nil
		}
	}
	if t.mode == modeStacks && len(t.stacks) == 0 {
		return nil
	}
	return t
}

// ── dry run ──────────────────────────────────────────────────────────────────

// syncInfo describes a branch a sync would rebase.
type syncInfo struct {
	Branch       string `json:"branch"`
	NeedsSync    bool   `json:"needs_sync"`
	MergedParent string `json:"merged_parent,omitempty"`
	BehindParent string `json:"behind_parent,omitempty"`
	BehindBy     int    `json:"behind_by,omitempty"`
	StackRoot    string `json:"stack_root"`
}

// syncDryRun previews a sync without touching branches, the gh-stack catalog,
// or hooks. It fetches remotes so "behind" counts are current.
func syncDryRun(g *git.Git, mgr *stack.Manager, t *syncTarget, jsonOutput bool) error {
	if t == nil {
		return nil
	}
	repo := git.New(mgr.GetRepoDir())
	_ = repo.Fetch()
	chains, err := ghstack.PlanChains(mgr.GetRepoDir(), mgr.ListStacks())
	if err != nil {
		return err
	}
	scope := map[string]bool{}
	if t.mode == modeStacks {
		for _, c := range chainsForStacks(chains, t.stacks) {
			for _, b := range c.Own() {
				scope[b] = true
			}
		}
	} else {
		scope[t.branch.Name] = true
		if t.mode == modeBranch || t.mode == modeChildren {
			for _, d := range mgr.GetDescendants(t.branch.Name) {
				scope[d.Name] = true
			}
		}
	}

	var infos []syncInfo
	for _, c := range chains {
		root := c.Trunk
		if s := mgr.GetStackByHashExact(c.StackHash); s != nil {
			root = s.Root
		}
		var mergedBelow string
		parent := c.Trunk
		for i, b := range c.Branches {
			if c.Merged[b] {
				mergedBelow = b
				continue
			}
			if i >= c.Shared && scope[b] {
				info := syncInfo{Branch: b, StackRoot: root}
				if mergedBelow != "" {
					info.MergedParent = mergedBelow
				} else if behind := commitsBehind(repo, b, parentRef(repo, mgr, parent, c)); behind > 0 {
					info.BehindBy = behind
					if parent != root {
						info.BehindParent = parent
					}
				}
				info.NeedsSync = info.MergedParent != "" || info.BehindBy > 0
				if info.NeedsSync {
					infos = append(infos, info)
				}
			}
			parent, mergedBelow = b, ""
		}
	}

	if jsonOutput {
		if infos == nil {
			infos = []syncInfo{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(infos)
	}
	if len(infos) == 0 {
		ui.Success("All branches are up to date. No sync needed.")
		return nil
	}
	ui.Info(fmt.Sprintf("Found %d branch(es) that need syncing:", len(infos)))
	for _, info := range infos {
		switch {
		case info.MergedParent != "":
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: parent %s%s%s was merged to %s\n",
				ui.IconBullet, ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.MergedParent, ui.Reset, info.StackRoot)
		case info.BehindParent != "":
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: %s%d commits%s behind parent %s%s%s\n",
				ui.IconBullet, ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.BehindBy, ui.Reset, ui.Yellow, info.BehindParent, ui.Reset)
		default:
			fmt.Fprintf(os.Stderr, "  %s %s%s%s: %s%d commits%s behind %s\n",
				ui.IconBullet, ui.Bold, info.Branch, ui.Reset, ui.Yellow, info.BehindBy, ui.Reset, info.StackRoot)
		}
	}
	return nil
}

// parentRef is the ref a layer is rebased onto: the remote-tracking trunk for
// a chain's bottom (gh-stack fetches it), otherwise the parent branch.
func parentRef(g *git.Git, mgr *stack.Manager, parent string, c ghstack.Chain) string {
	if parent == c.Trunk {
		if r := trunkRemote(g, mgr, c); r != "" && g.RefExists("refs/remotes/"+r+"/"+parent) {
			return "refs/remotes/" + r + "/" + parent
		}
	}
	return parent
}

func commitsBehind(g *git.Git, branch, target string) int {
	out, err := g.RunCapture("rev-list", "--count", branch+".."+target)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(out)
	return n
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

	// Walk branches parents-before-children, and squash each branch onto its
	// fork point from the parent's PRE-squash tip. Resetting onto the parent's
	// current tip instead would turn the squash commit into a revert of any
	// parent commits the child hadn't been rebased onto yet, and once a parent
	// is squashed its new tip is no longer in the child's history at all. The
	// rebase that follows moves each squashed child onto its new parent.
	ordered := topoOrderStackBranches(currentStack)
	repo := git.New(mgr.GetRepoDir())
	origTip := map[string]string{}
	for _, b := range ordered {
		if sha, err := repo.GetBranchCommit(b.Name); err == nil {
			origTip[b.Name] = sha
		}
	}

	squashed := 0
	for _, b := range ordered {
		// The stack root has no parent inside the stack; its "parent" is the
		// base branch (main), which we must never touch.
		if b.Parent == "" || b.Name == currentStack.Root || b.IsMerged {
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
		parentTip := origTip[b.Parent]
		if parentTip == "" {
			parentTip = b.Parent
		}
		forkPoint, err := bg.RunCapture("merge-base", b.Name, parentTip)
		if err != nil || forkPoint == "" {
			ui.Warn(fmt.Sprintf("Skipping squash of '%s': no common ancestor with %s", b.Name, b.Parent))
			continue
		}
		ahead, err := bg.GetCommitsAhead(b.Name, forkPoint)
		if err != nil || ahead < 2 {
			continue
		}
		msg := fmt.Sprintf("squash: %s onto %s", b.Name, b.Parent)
		if last, lerr := bg.GetLastCommitMessage(); lerr == nil && last != "" {
			msg = last
		}
		if _, runErr := bg.RunCapture("reset", "--soft", forkPoint); runErr != nil {
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

// printMergedBranchesList prints the list of merged branches.
func printMergedBranchesList(mergedBranches []stack.MergedBranchInfo) {
	ui.Info(fmt.Sprintf("Found %d branch(es) with merged PRs (will be deleted):", len(mergedBranches)))
	for _, info := range mergedBranches {
		fmt.Fprintf(os.Stderr, "  %s %s%s%s: PR #%d merged\n",
			ui.IconSuccess, ui.Bold, info.Branch, ui.Reset, info.PRNumber)
	}
	fmt.Fprintln(os.Stderr)
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
