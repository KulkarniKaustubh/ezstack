package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/helpers"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

// Attach brings an existing local branch under ezs management — converging it
// to whatever "fully managed" looks like for the current repo (worktree mode
// vs. bare-branch mode). Idempotent: running it on an already-fully-attached
// branch is an explicit no-op.
//
// Replaces the older `ezs new -f` path and gives users one verb that doesn't
// require them to know whether their branch is bare, worktree-but-untracked,
// or tracked-without-worktree.
func Attach(args []string) error {
	if HasExamplesFlag("attach", args) {
		return nil
	}
	fs := pflag.NewFlagSet("attach", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sBring an existing branch under ezs management%s

%sUSAGE%s
    ezs attach [branch] [options]

%sOPTIONS%s
    -p, --parent <branch>     Override detected parent (default: nearest tracked ancestor)
    -w, --worktree <path>     Force a worktree at this path (overrides config)
    -W, --no-worktree         Register without a worktree (overrides config)
    -c, --cd                  Change to the worktree after attaching
    -C, --no-cd               Don't change to the worktree (overrides config)
    -s, --init-submodules     Mirror main worktree's initialized submodules
    -S, --no-init-submodules  Skip submodule initialization
    -y, --yes                 Skip the "Proceed?" confirm
    -h, --help                Show this help message

%sNOTES%s
    With no branch argument, prompts to pick from local branches that aren't
    in any stack yet. Run again on the same branch — it's a no-op.

    To pick up a remote branch, use:  ezs new origin/<branch>
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	parent := fs.StringP("parent", "p", "", "Override detected parent")
	worktree := fs.StringP("worktree", "w", "", "Force worktree at this path")
	noWorktree := fs.BoolP("no-worktree", "W", false, "Register without a worktree")
	cdFlag := fs.BoolP("cd", "c", false, "Change to worktree")
	noCdFlag := fs.BoolP("no-cd", "C", false, "Don't change to worktree")
	initSubmodulesFlag := fs.BoolP("init-submodules", "s", false, "Mirror submodules")
	noInitSubmodulesFlag := fs.BoolP("no-init-submodules", "S", false, "Skip submodules")
	yesFlag := fs.BoolP("yes", "y", false, "Skip confirmation prompt")
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
	if *worktree != "" && *noWorktree {
		return fmt.Errorf("--worktree and --no-worktree are mutually exclusive")
	}

	if *yesFlag {
		ui.YesMode = true
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

	// No branch arg → interactive picker. The picker shows the auto-detected
	// parent for each row so the user is never selecting blind.
	if fs.NArg() == 0 {
		return attachInteractive(g, mgr, *parent, *worktree, *noWorktree, *cdFlag, *noCdFlag, *initSubmodulesFlag, *noInitSubmodulesFlag)
	}

	branchName := fs.Arg(0)
	if err := git.ValidateBranchName(branchName); err != nil {
		return err
	}

	return attachOne(g, mgr, branchName, *parent, *worktree, *noWorktree, *cdFlag, *noCdFlag, *initSubmodulesFlag, *noInitSubmodulesFlag)
}

// attachOne performs a single-branch attach. Centralizes the state-machine
// dispatch so both single-arg and batch interactive paths share one
// implementation.
func attachOne(g *git.Git, mgr *stack.Manager, branchName, parentOverride, worktreeOverride string, noWorktree, cdFlag, noCdFlag, initSubmodulesFlag, noInitSubmodulesFlag bool) error {
	if !g.BranchExists(branchName) {
		return fmt.Errorf("branch %q does not exist locally\n\n  Create it: ezs new %s", branchName, branchName)
	}

	cfg := mgr.GetConfig()
	repoDir := mgr.GetRepoDir()
	baseBranch := cfg.GetBaseBranch(repoDir)

	if mgr.IsMainBranch(branchName) {
		return fmt.Errorf("branch %q is the base branch and cannot be attached", branchName)
	}

	tracked := mgr.GetBranch(branchName)
	actualWorktreePath := findWorktreePathForBranch(g, branchName)

	useWorktrees := cfg.GetUseWorktrees(repoDir)
	if worktreeOverride != "" {
		useWorktrees = true
	}
	if noWorktree {
		useWorktrees = false
	}

	// Already-tracked path: detect mismatch, sync metadata, no-op, or upgrade.
	if tracked != nil {
		metaPath := tracked.WorktreePath
		if metaPath != "" && actualWorktreePath != "" && metaPath != actualWorktreePath {
			return fmt.Errorf("worktree mismatch for branch %q\n  metadata says: %s\n  git reports:   %s\n  Move with: git worktree move %s %s",
				branchName, metaPath, actualWorktreePath, actualWorktreePath, metaPath)
		}
		if metaPath == "" && actualWorktreePath != "" {
			if err := mgr.SetBranchWorktreePath(branchName, actualWorktreePath); err != nil {
				return err
			}
			ui.Info(fmt.Sprintf("Synced metadata for %q to existing worktree at %s", branchName, actualWorktreePath))
			return nil
		}
		if (metaPath != "" && actualWorktreePath != "") || (metaPath == "" && actualWorktreePath == "" && !useWorktrees) {
			s := mgr.GetStackForBranch(branchName)
			ui.Info(fmt.Sprintf("Branch %q is already attached%s", branchName, formatStackSuffix(s)))
			return nil
		}
		return materializeWorktreeForTracked(g, mgr, tracked, worktreeOverride, cdFlag, noCdFlag, initSubmodulesFlag, noInitSubmodulesFlag)
	}

	// Untracked path: pick a parent, then register (and optionally materialize).
	parentBranch := parentOverride
	parentDetected := false
	if parentBranch == "" {
		detected, ok := detectParentBranch(g, mgr, branchName, baseBranch)
		if !ok {
			picked, err := pickParentForAttach(mgr, baseBranch)
			if err != nil {
				return err
			}
			parentBranch = picked
		} else {
			parentBranch = detected
			parentDetected = true
		}
	}
	if parentBranch == branchName {
		return fmt.Errorf("parent cannot be the branch itself (%q)", branchName)
	}

	var plannedWorktreePath string
	worktreeAction := "none"
	if useWorktrees {
		if actualWorktreePath != "" {
			if worktreeOverride != "" && helpers.ExpandPath(worktreeOverride) != actualWorktreePath {
				return fmt.Errorf("branch %q already has a worktree at %s; refusing to create a second one at %s",
					branchName, actualWorktreePath, worktreeOverride)
			}
			plannedWorktreePath = actualWorktreePath
			worktreeAction = "use existing"
		} else if worktreeOverride != "" {
			plannedWorktreePath = helpers.ExpandPath(worktreeOverride)
			worktreeAction = "create"
		} else {
			worktreeBaseDir := cfg.GetWorktreeBaseDir(repoDir)
			if worktreeBaseDir == "" {
				wbd, err := promptWorktreeBaseDir(repoDir, cfg)
				if err != nil {
					return err
				}
				worktreeBaseDir = wbd
			}
			plannedWorktreePath = filepath.Join(worktreeBaseDir, branchName)
			worktreeAction = "create"
		}
	} else if actualWorktreePath != "" {
		// User explicitly passed -W but a worktree already exists — register it
		// rather than ignore reality.
		plannedWorktreePath = actualWorktreePath
		worktreeAction = "use existing"
	}

	showAttachPlan(g, mgr, branchName, parentBranch, parentDetected, plannedWorktreePath, worktreeAction, baseBranch)
	if !ui.YesMode {
		if !ui.ConfirmTUIWithDefault(fmt.Sprintf("Attach %q?", branchName), true) {
			ui.Warn("Cancelled")
			return nil
		}
	}

	if worktreeAction == "create" {
		if err := g.CreateWorktree(branchName, plannedWorktreePath, parentBranch); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}
		mirrorSubmodulesIfEnabled(g, cfg, repoDir, plannedWorktreePath, initSubmodulesFlag, noInitSubmodulesFlag)
	}

	branch, err := mgr.AddWorktreeToStack(branchName, plannedWorktreePath, parentBranch)
	if err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Attached %q under %q", branchName, parentBranch))
	if plannedWorktreePath != "" {
		ui.Info(fmt.Sprintf("Worktree: %s", plannedWorktreePath))
	}

	if s := mgr.GetStackForBranch(branchName); s != nil && len(s.Branches) == 1 {
		promptStackName(mgr, branch.Name)
	}

	if plannedWorktreePath != "" && getCdAfterNew(cfg, repoDir, cdFlag, noCdFlag) {
		EmitCd(plannedWorktreePath)
	}
	return nil
}

// materializeWorktreeForTracked handles the "branch is already tracked but
// has no worktree, and use_worktrees=true" case — the path that lets users
// upgrade a no-worktree-mode branch to a worktree-mode branch without
// re-creating any stack metadata.
func materializeWorktreeForTracked(g *git.Git, mgr *stack.Manager, tracked *config.Branch, worktreeOverride string, cdFlag, noCdFlag, initSubmodulesFlag, noInitSubmodulesFlag bool) error {
	cfg := mgr.GetConfig()
	repoDir := mgr.GetRepoDir()

	worktreePath := worktreeOverride
	if worktreePath == "" {
		worktreeBaseDir := cfg.GetWorktreeBaseDir(repoDir)
		if worktreeBaseDir == "" {
			wbd, err := promptWorktreeBaseDir(repoDir, cfg)
			if err != nil {
				return err
			}
			worktreeBaseDir = wbd
		}
		worktreePath = filepath.Join(worktreeBaseDir, tracked.Name)
	}
	worktreePath = helpers.ExpandPath(worktreePath)

	ui.Info(fmt.Sprintf("Branch %q is tracked but has no worktree.", tracked.Name))
	ui.Info(fmt.Sprintf("Will create worktree at: %s", worktreePath))
	if !ui.YesMode && !ui.ConfirmTUIWithDefault("Proceed?", true) {
		ui.Warn("Cancelled")
		return nil
	}

	if err := g.CreateWorktree(tracked.Name, worktreePath, tracked.Parent); err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}
	mirrorSubmodulesIfEnabled(g, cfg, repoDir, worktreePath, initSubmodulesFlag, noInitSubmodulesFlag)

	if err := mgr.SetBranchWorktreePath(tracked.Name, worktreePath); err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Materialized worktree for %q at %s", tracked.Name, worktreePath))
	if getCdAfterNew(cfg, repoDir, cdFlag, noCdFlag) {
		EmitCd(worktreePath)
	}
	return nil
}

// detectParentBranch finds the closest tracked ancestor of branchName. Walks
// every tracked branch (across all stacks) plus the configured base branch
// and picks the one with the smallest commit-distance from branchName,
// breaking ties in favor of non-base candidates. If nothing is an ancestor,
// returns ("", false) so the caller can fall back to a picker.
func detectParentBranch(g *git.Git, mgr *stack.Manager, branchName, baseBranch string) (string, bool) {
	var candidates []string
	seen := make(map[string]bool)
	add := func(name string) {
		if name == "" || name == branchName || seen[name] {
			return
		}
		seen[name] = true
		candidates = append(candidates, name)
	}
	for _, b := range mgr.GetAllBranchesInAllStacks() {
		add(b.Name)
	}
	if baseBranch != "" {
		add(baseBranch)
	}

	type scored struct {
		name     string
		distance int
		isBase   bool
	}
	var ancestors []scored
	for _, c := range candidates {
		isAncestor, err := g.IsBranchMerged(c, branchName)
		if err != nil || !isAncestor {
			continue
		}
		dist, err := g.GetCommitsAhead(branchName, c)
		if err != nil {
			continue
		}
		ancestors = append(ancestors, scored{c, dist, c == baseBranch})
	}
	if len(ancestors) == 0 {
		if baseBranch != "" {
			return baseBranch, false
		}
		return "", false
	}
	sort.SliceStable(ancestors, func(i, j int) bool {
		if ancestors[i].distance != ancestors[j].distance {
			return ancestors[i].distance < ancestors[j].distance
		}
		if ancestors[i].isBase != ancestors[j].isBase {
			return !ancestors[i].isBase
		}
		return ancestors[i].name < ancestors[j].name
	})
	return ancestors[0].name, true
}

// pickParentForAttach falls back to the parent picker used by `ezs new` so
// the attach UX matches user expectations when auto-detect can't decide.
func pickParentForAttach(mgr *stack.Manager, baseBranch string) (string, error) {
	choices := buildParentChoices(baseBranch, mgr.ListStacks())
	if len(choices) == 0 {
		return baseBranch, nil
	}
	labels := make([]string, len(choices))
	for i, c := range choices {
		labels[i] = c.label
	}
	idx, err := ui.SelectOption(labels, "Could not detect parent — select one")
	if err != nil {
		return "", err
	}
	return choices[idx].branch, nil
}

// findWorktreePathForBranch returns the path of any existing worktree for the
// branch, or "" if none exists. The main worktree counts — if the user did a
// plain `git checkout -b` in the main repo, that's its worktree.
func findWorktreePathForBranch(g *git.Git, branchName string) string {
	wts, err := g.ListWorktrees()
	if err != nil {
		return ""
	}
	for _, wt := range wts {
		if wt.Branch == branchName {
			return wt.Path
		}
	}
	return ""
}

func formatStackSuffix(s *config.Stack) string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf(" in stack '%s'", s.DisplayName())
}

// showAttachPlan prints the planned attach action and returns false if the
// user had any reason to abort before the confirm. Always returns true today;
// reserved for future "preflight failed" branches.
func showAttachPlan(g *git.Git, mgr *stack.Manager, branchName, parentBranch string, detected bool, worktreePath, worktreeAction, baseBranch string) bool {
	commitsAhead, _ := g.GetCommitsAhead(branchName, parentBranch)

	stackLabel := "(new stack)"
	if s := mgr.FindStackForBranch(parentBranch); s != nil {
		stackLabel = fmt.Sprintf("'%s' [%s]", s.DisplayName(), s.Hash)
	}

	parentNote := ""
	if detected {
		parentNote = "  (auto-detected)"
	} else if parentBranch == baseBranch {
		parentNote = "  (default base)"
	}

	worktreeNote := "(no worktree)"
	switch worktreeAction {
	case "create":
		worktreeNote = fmt.Sprintf("%s  (will be created)", worktreePath)
	case "use existing":
		worktreeNote = fmt.Sprintf("%s  (existing — will be registered)", worktreePath)
	}

	fmt.Fprintf(os.Stderr, "%s%sPlan:%s\n", ui.Bold, ui.Cyan, ui.Reset)
	fmt.Fprintf(os.Stderr, "  Branch:   %s  (%d commit(s) ahead of %s)\n", branchName, commitsAhead, parentBranch)
	fmt.Fprintf(os.Stderr, "  Parent:   %s%s\n", parentBranch, parentNote)
	fmt.Fprintf(os.Stderr, "  Stack:    %s\n", stackLabel)
	fmt.Fprintf(os.Stderr, "  Worktree: %s\n", worktreeNote)
	fmt.Fprintln(os.Stderr)
	return true
}

// attachInteractive shows the orphan-branch picker with each row's auto-
// detected parent. Selecting a row hands off to attachOne — so the
// confirmation flow there still applies, and the user gets the full plan
// preview before any changes.
func attachInteractive(g *git.Git, mgr *stack.Manager, parentOverride, worktreeOverride string, noWorktree, cdFlag, noCdFlag, initSubmodulesFlag, noInitSubmodulesFlag bool) error {
	candidates, err := CollectUntrackedBranches(g, mgr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%swarning: %v%s\n", ui.Yellow, err, ui.Reset)
	}
	if len(candidates) == 0 {
		ui.Info("No orphan branches found — every local branch is already in a stack.")
		return nil
	}

	cfg := mgr.GetConfig()
	baseBranch := cfg.GetBaseBranch(mgr.GetRepoDir())

	type candRow struct {
		name       string
		worktree   string
		parent     string
		detected   bool
		commitsAhd int
	}
	rows := make([]candRow, 0, len(candidates))
	for _, c := range candidates {
		row := candRow{name: c.Name, worktree: c.WorktreePath}
		if parentOverride != "" {
			row.parent = parentOverride
		} else {
			p, ok := detectParentBranch(g, mgr, c.Name, baseBranch)
			row.parent = p
			row.detected = ok
		}
		if row.parent != "" {
			row.commitsAhd, _ = g.GetCommitsAhead(c.Name, row.parent)
		}
		rows = append(rows, row)
	}

	options := make([]string, len(rows))
	for i, r := range rows {
		wt := "[no worktree]"
		if r.worktree != "" {
			wt = fmt.Sprintf("(worktree at %s)", r.worktree)
		}
		parentNote := ""
		if r.parent != "" {
			parentNote = fmt.Sprintf("  → would attach under %s (%d commit(s) ahead)", r.parent, r.commitsAhd)
			if !r.detected {
				parentNote += " [fallback]"
			}
		}
		options[i] = fmt.Sprintf("%s %s%s", r.name, wt, parentNote)
	}

	selected, err := ui.SelectOption(options, "Select branch to attach")
	if err != nil {
		return err
	}
	row := rows[selected]

	parent := row.parent
	if parent == "" {
		parent = baseBranch
	}
	return attachOne(g, mgr, row.name, parent, worktreeOverride, noWorktree, cdFlag, noCdFlag, initSubmodulesFlag, noInitSubmodulesFlag)
}
