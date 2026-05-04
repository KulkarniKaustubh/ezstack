package commands

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/github"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

func printPRUsage() {
	fmt.Fprintf(os.Stderr, `%sManage pull requests%s

%sUSAGE%s
    ezs pr <subcommand> [options]

%sSUBCOMMANDS%s
    create    Create a new pull request
    draft     Toggle PR between draft and ready
    merge     Merge a pull request
    refresh   Reconcile cached PR state from GitHub
    stack     Update all PR descriptions with stack info
    unlink    Clear cached PR association for a branch
    update    Push changes to existing PR

%sOPTIONS%s
    --draft-all   Create draft PRs for every branch in the current stack
    -h, --help    Show this help message

Run 'ezs pr <subcommand> --help' for subcommand options.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
}

// PR handles pull request operations
func PR(args []string) error {
	if HasExamplesFlag("pr", args) {
		return nil
	}
	// --draft-all is a top-level shortcut for "create draft PRs across the
	// whole current stack". Only honored when no subcommand is present —
	// `pr create --draft-all` is parsed inside prCreate. We also recognize
	// --force/-f/--recreate alongside it so `ezs pr --draft-all --force`
	// behaves the same as `ezs pr create --draft-all --force`.
	if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		hasDraftAll := false
		force := false
		for _, a := range args {
			switch a {
			case "--draft-all":
				hasDraftAll = true
			case "-f", "--force", "--recreate":
				force = true
			}
		}
		if hasDraftAll {
			return prDraftAllForce(force)
		}
	}
	// Allow --help without requiring auth
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		printPRUsage()
		return nil
	}

	// `pr unlink` is the only subcommand that's purely local — it edits
	// stacks.json and doesn't touch GitHub. Dispatch it before the auth
	// gate so users whose gh login has expired can still recover from a
	// stale cache without re-authenticating first.
	if len(args) > 0 && args[0] == "unlink" {
		return prUnlink(args[1:])
	}

	if err := github.CheckAuth(); err != nil {
		return ui.NewExitError(ui.ExitAuthRequired, "%v", err)
	}

	if len(args) < 1 {
		// Interactive mode
		return prInteractive()
	}

	switch args[0] {
	case "create":
		return prCreate(args[1:])
	case "update":
		return prUpdate(args[1:])
	case "merge":
		return prMerge(args[1:])
	case "draft":
		return prDraft(args[1:])
	case "stack":
		return prStack(args[1:])
	case "refresh":
		return prRefresh(args[1:])
	default:
		return fmt.Errorf("unknown pr command: %s. Run 'ezs pr --help' for available subcommands", args[0])
	}
}

// prInteractive shows an interactive menu for PR operations
func prInteractive() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		return ui.NewExitError(ui.ExitNotInStack, "not in a stack. Create a branch first with: ezs new <branch-name>")
	}

	ui.PrintStack(currentStack, branch.Name, false, nil)

	options := []string{}
	optionActions := []string{}

	if branch.PRNumber == 0 {
		options = append(options, fmt.Sprintf("%s  Create PR for current branch", ui.IconNew))
		optionActions = append(optionActions, "create")
	} else {
		options = append(options, fmt.Sprintf("%s  Push updates to PR #%d", ui.IconPush, branch.PRNumber))
		optionActions = append(optionActions, "update")

		options = append(options, fmt.Sprintf("%s  Merge PR #%d", ui.IconSuccess, branch.PRNumber))
		optionActions = append(optionActions, "merge")

		options = append(options, fmt.Sprintf("%s  Toggle draft/ready for PR #%d", ui.IconSync, branch.PRNumber))
		optionActions = append(optionActions, "draft")
	}

	prCount := 0
	for _, b := range currentStack.Branches {
		if b.PRNumber > 0 {
			prCount++
		}
	}
	if prCount > 0 {
		options = append(options, fmt.Sprintf("%s  Update stack info in %d PR(s)", ui.IconStack, prCount))
		optionActions = append(optionActions, "stack")
	}

	branchesWithoutPR := 0
	for _, b := range currentStack.Branches {
		if b.PRNumber == 0 {
			branchesWithoutPR++
		}
	}
	if branchesWithoutPR > 0 {
		options = append(options, fmt.Sprintf("%s  Create PRs for all %d branches without PRs", ui.IconRocket, branchesWithoutPR))
		optionActions = append(optionActions, "create-all")
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
	case "create":
		return prCreate(nil)
	case "update":
		return prUpdate(nil)
	case "merge":
		return prMerge(nil)
	case "draft":
		return prDraft(nil)
	case "stack":
		return prStack(nil)
	case "create-all":
		return prCreateAll(currentStack)
	}

	return nil
}

// prDraftAllForce is the --draft-all entry point. force=true reuses the same
// reconciliation logic as `pr create --stack --force`, which is what makes
// `ezs pr --draft-all --force` (and `ezs pr create --draft-all --force`) work
// against stacks where some cached PRs have drifted into a terminal state.
func prDraftAllForce(force bool) error {
	if err := github.CheckAuth(); err != nil {
		return ui.NewExitError(ui.ExitAuthRequired, "%v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}
	currentStack, _, err := mgr.GetCurrentStack()
	if err != nil {
		return err
	}
	return prCreateAllDraft(currentStack, true, force)
}

// prCreateAll creates PRs for all branches in the stack that don't have PRs
func prCreateAll(currentStack *config.Stack) error {
	return prCreateAllDraft(currentStack, false, false)
}

// prCreateAllForce mirrors prCreateAll but threads the --force flag through.
func prCreateAllForce(currentStack *config.Stack, force bool) error {
	return prCreateAllDraft(currentStack, false, force)
}

// prCreateAllDraft handles `pr create --stack` and `pr --draft-all`.
//
// Branch eligibility (per branch in the stack):
//   - is_merged or PRState == "MERGED" / "CLOSED" → terminal cache; treat as
//     having no live PR. Reconcile against GitHub. If GitHub agrees, queue
//     for new-PR creation. If GitHub still reports a live PR, adopt it.
//   - PRNumber > 0 with non-terminal cache state → query GitHub. If still
//     live, skip (or queue with warning when --force is set). If terminal,
//     queue for create.
//   - PRNumber == 0 → existing behavior: query GitHub, adopt if a live PR
//     exists, else queue.
//
// commitsAhead == 0 always disqualifies a branch — there's nothing to PR.
func prCreateAllDraft(currentStack *config.Stack, draft, force bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)
	gh, err := newGitHubClient(g)
	if err != nil {
		return err
	}

	mainWorktree := getMainWorktreePath(g)

	branchesToCreate := []*config.Branch{}
	for _, b := range currentStack.Branches {
		// commitsAhead is a local git op, so checking it before either the
		// fast-path skip or the GitHub refresh is free and avoids printing
		// "will force create" warnings for branches we're about to skip.
		commitsAhead, err := g.GetCommitsAhead(b.Name, b.Parent)
		if err != nil {
			ui.Warn(fmt.Sprintf("Could not check commits for %s: %v (skipping)", b.Name, err))
			continue
		}
		if commitsAhead == 0 {
			ui.Warn(fmt.Sprintf("Skipping %s: no commits ahead of '%s'", b.Name, b.Parent))
			continue
		}

		cacheClaimsLive := b.PRNumber > 0 && !b.IsMerged && (b.PRState == "OPEN" || b.PRState == "DRAFT")

		// Fast path: cache says PR is live and we're not forcing. Skip
		// without burning a GitHub roundtrip — preserves the original
		// performance on the common case. Users wanting to refresh stale
		// state should run `ezs pr refresh` (or pass --force here).
		if cacheClaimsLive && !force {
			ui.Info(fmt.Sprintf("Branch '%s' already has PR #%d (skipping)", b.Name, b.PRNumber))
			continue
		}

		// Slow path: ambiguous cache or --force. Reconcile against GitHub.
		// refreshPRStateFromGitHub returns (nil, nil) when no PR exists,
		// (pr, nil) when a PR is found, and (nil, err) on transient errors.
		// Routing both the cached-PR and the no-PR-number paths through this
		// helper keeps cache writes uniform and gives ErrPRNotFound the same
		// "(nil, nil) → cache cleared" treatment whether the branch had a
		// cached number or not. Only warn when there was something cached to
		// reconcile — fresh branches shouldn't get a "could not refresh" nag
		// just because gh is briefly flaky.
		var livePR *github.PR
		pr, refreshErr := refreshPRStateFromGitHub(gh, mainWorktree, b)
		if refreshErr != nil {
			if b.PRNumber > 0 || b.IsMerged {
				ui.Warn(fmt.Sprintf("Could not refresh PR state for %s: %v (using cache)", b.Name, refreshErr))
			}
		} else {
			livePR = pr
		}

		liveOpen := livePR != nil && !livePR.Merged && livePR.State != "CLOSED"
		if liveOpen {
			if !force {
				ui.Info(fmt.Sprintf("Branch '%s' already has open PR #%d (skipping)", b.Name, livePR.Number))
				continue
			}
			// --force: warn but still queue. Final confirmation happens in
			// the shared "create all PRs?" prompt below.
			ui.Warn(fmt.Sprintf("--force: will create a new PR for '%s' even though PR #%d is open", b.Name, livePR.Number))
		}
		branchesToCreate = append(branchesToCreate, b)
	}

	if len(branchesToCreate) == 0 {
		ui.Info("No branches to create PRs for (all have PRs or no commits)")
		return nil
	}

	ui.Info(fmt.Sprintf("Will create PRs for %d branches:", len(branchesToCreate)))
	for _, b := range branchesToCreate {
		fmt.Fprintf(os.Stderr, "  %s %s (base: %s)\n", ui.IconBullet, b.Name, b.Parent)
	}

	if !ui.ConfirmTUI("Create all PRs") {
		ui.Warn("Cancelled")
		return nil
	}

	created := 0
	failed := 0
	for _, b := range branchesToCreate {
		ui.Info(fmt.Sprintf("Creating PR for %s...", b.Name))

		// Single-branch prCreate threads the fork remote through every push
		// site (commit 642eb5a). Bulk creation must do the same — without
		// it, a stack containing a fork branch tries to push to origin and
		// fails confusingly. Branches whose remote is `_nopush` (fork that
		// disallows maintainer push) are skipped with a warning rather than
		// counted as a hard failure.
		if !b.CanPush() {
			ui.Warn(fmt.Sprintf("Skipping %s: push not allowed (fork does not allow maintainer push)", b.Name))
			continue
		}
		// Push the branch first to its effective remote (fork-aware).
		if err := g.PushBranchSetUpstream(b.Name, b.EffectiveRemote()); err != nil {
			ui.Warn(fmt.Sprintf("Failed to push %s: %v", b.Name, err))
			failed++
			continue
		}

		pr, err := gh.CreatePR(formatBranchTitle(b.Name), "", b.Name, b.Parent, draft)
		if err != nil {
			ui.Warn(fmt.Sprintf("Failed to create PR for %s: %v", b.Name, err))
			failed++
			continue
		}

		b.PRNumber = pr.Number
		b.PRUrl = pr.URL
		b.PRState = prStateFromGitHub(pr)
		b.IsMerged = pr.Merged
		savePRToCache(mainWorktree, b.Name, pr)
		created++
		ui.Success(fmt.Sprintf("Created PR #%d for %s: %s", pr.Number, b.Name, pr.URL))
	}

	if created > 0 {
		if err := updateStackDescriptions(gh, currentStack, ""); err != nil {
			ui.Warn(fmt.Sprintf("Failed to update stack descriptions: %v", err))
		}
	}

	if failed > 0 {
		ui.Warn(fmt.Sprintf("Created %d PR(s), %d failed", created, failed))
	} else {
		ui.Success(fmt.Sprintf("Created %d PR(s)", created))
	}
	return nil
}

func prCreate(args []string) error {
	fs := pflag.NewFlagSet("pr create", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sCreate a new pull request%s

%sUSAGE%s
    ezs pr create [options]

%sOPTIONS%s
    -s, --stack            Create PRs for all branches in the current stack
    --draft-all            Create draft PRs across the whole stack (implies --stack --draft)
    -t, --title <title>    PR title (defaults to branch name)
    -b, --body <body>      PR body/description
    -d, --draft            Create as draft PR
    --branch <name>        Create PR for a specific branch (instead of current)
    -f, --force            Create a new PR even if one already exists for the branch
                           (alias: --recreate). Skips the confirmation prompt so
                           scripts don't deadlock on stdin — the warning is still
                           printed to stderr.
    -h, --help             Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	stackFlag := fs.BoolP("stack", "s", false, "Create PRs for all branches in the current stack")
	draftAll := fs.Bool("draft-all", false, "Create draft PRs across the whole stack")
	title := fs.StringP("title", "t", "", "PR title")
	body := fs.StringP("body", "b", "", "PR body")
	draft := fs.BoolP("draft", "d", false, "Create as draft PR")
	branchFlag := fs.String("branch", "", "Create PR for a specific branch")
	force := fs.BoolP("force", "f", false, "Create a new PR even if one already exists")
	recreate := fs.Bool("recreate", false, "Alias for --force")
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

	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	forceCreate := *force || *recreate

	if *draftAll {
		if *title != "" || *body != "" || *branchFlag != "" {
			ui.Warn("--draft-all creates one PR per branch with auto-generated titles; -t/-b/--branch are ignored")
		}
		return prDraftAllForce(forceCreate)
	}
	if *stackFlag {
		if *title != "" || *body != "" || *branchFlag != "" {
			ui.Warn("--stack creates one PR per branch with auto-generated titles; -t/-b/--branch are ignored")
		}
		currentStack, _, err := mgr.GetCurrentStack()
		if err != nil {
			return err
		}
		return prCreateAllForce(currentStack, forceCreate)
	}

	g := git.New(cwd)

	var currentStack *config.Stack
	var branch *config.Branch

	if *branchFlag != "" {
		// Look up the specified branch
		branch = mgr.GetBranch(*branchFlag)
		if branch == nil {
			return fmt.Errorf("branch '%s' is not tracked by ezstack", *branchFlag)
		}
		currentStack = mgr.GetStackForBranch(*branchFlag)
		if currentStack == nil {
			return fmt.Errorf("branch '%s' is not in any stack", *branchFlag)
		}
	} else {
		var err error
		currentStack, branch, err = mgr.GetCurrentStack()
		if err != nil {
			return err
		}
	}

	commitsAhead, err := g.GetCommitsAhead(branch.Name, branch.Parent)
	if err != nil {
		// If we can't determine, continue anyway (might be a new branch)
		ui.Warn(fmt.Sprintf("Could not check commits: %v", err))
	} else if commitsAhead == 0 {
		return fmt.Errorf("no commits to create PR from. This branch has no commits ahead of '%s'.\nPlease make at least one commit first", branch.Parent)
	}

	// The PR base branch must exist on whichever remote hosts the parent.
	// For tracked parents that's the parent's own EffectiveRemote (fork or
	// origin); for an untracked parent (rare), fall back to origin.
	parentRemote := "origin"
	if parentBranch := mgr.GetBranch(branch.Parent); parentBranch != nil {
		parentRemote = parentBranch.EffectiveRemote()
	}
	if !mgr.IsMainBranch(branch.Parent) && !g.RemoteHasBranch(parentRemote, branch.Parent) {
		ui.Warn(fmt.Sprintf("Parent branch '%s' has not been pushed to %s.", branch.Parent, parentRemote))
		ui.Warn("The PR base branch won't exist on GitHub until it is pushed.")
		if !ui.ConfirmTUI("Continue anyway?") {
			ui.Warn("Cancelled")
			return nil
		}
	}

	gh, err := newGitHubClient(g)
	if err != nil {
		return err
	}

	// Reconcile local cache against GitHub before deciding whether to refuse.
	// Issue #22: previously a cached PR (in any state) blocked recreation
	// outright. We now trust GitHub as source of truth — a terminal-state PR
	// (MERGED/CLOSED) lets the new PR proceed silently, a still-live PR
	// requires --force.
	mainWorktree := getMainWorktreePath(g)
	livePR, refreshErr := refreshPRStateFromGitHub(gh, mainWorktree, branch)
	switch {
	case refreshErr != nil:
		// Couldn't reach GitHub (or the branch genuinely has no PR — the
		// real github.Client surfaces both as errors). Only nag when there
		// was something cached to lose: a fresh branch with no PR shouldn't
		// see "couldn't check PR state" warnings on its first `pr create`.
		if branch.PRNumber > 0 {
			ui.Warn(fmt.Sprintf("Could not check PR state on GitHub: %v", refreshErr))
			cacheTerminal := branch.IsMerged || branch.PRState == "CLOSED" || branch.PRState == "MERGED"
			switch {
			case !forceCreate && !cacheTerminal:
				return fmt.Errorf("branch '%s' has cached PR #%d (%s): %s\nGitHub is unreachable so the cache cannot be verified. Use 'ezs pr update' to push to the existing PR, or 'ezs pr create --force' to create a new one anyway", branch.Name, branch.PRNumber, displayPRState(branch.PRState), branch.PRUrl)
			case forceCreate && !cacheTerminal:
				// User explicitly opted into duplication. Be loud about it
				// so a scripted `pr create --force` against a flaky network
				// doesn't quietly create a duplicate of a still-live PR.
				ui.Warn(fmt.Sprintf("--force: creating a new PR for '%s' even though cached PR #%d (%s) may still be live on GitHub", branch.Name, branch.PRNumber, displayPRState(branch.PRState)))
			}
		}
	case livePR != nil && !livePR.Merged && livePR.State != "CLOSED":
		// PR is live on GitHub.
		if !forceCreate {
			return fmt.Errorf("branch '%s' already has an open PR #%d: %s\nUse 'ezs pr update' to push changes to it, or 'ezs pr create --force' to create a new PR (the existing one will stay open)", branch.Name, livePR.Number, livePR.URL)
		}
		ui.Warn(fmt.Sprintf("Branch '%s' already has an open PR #%d: %s", branch.Name, livePR.Number, livePR.URL))
		ui.Warn("--force will leave the existing PR open and create a new one. GitHub may refuse this as a duplicate.")
		// --force is the explicit "I know what I'm doing" gate — don't add
		// a second prompt. ConfirmTUI in non-tty contexts (CI, scripts)
		// always returns false, which silently turned every scripted
		// `pr create --force` into a no-op.
	}
	// Reaching here means: no live PR (terminal state or never existed), or
	// --force was confirmed. The branch cache has been reconciled by
	// refreshPRStateFromGitHub. Proceed to create.

	prTitle := *title
	if prTitle == "" {
		prTitle = ui.Prompt("PR title", branch.Name)
	}

	prBody := *body
	if prBody == "" {
		// Get PR template if available
		template := g.GetPRTemplate()
		if template == "" {
			template = "<!-- Enter your PR description here -->\n\n"
		}
		prBody = template

		if ui.ConfirmTUI("Edit PR description?") {
			editedBody, err := ui.EditWithEditor(template, ".md")
			if err != nil {
				ui.Warn(fmt.Sprintf("Editor failed: %v (keeping template)", err))
			} else {
				prBody = editedBody
			}
		}
	}

	isDraft := *draft
	if !isDraft {
		// Inspect the target branch's tip rather than HEAD — when --branch
		// <other> is passed, HEAD may be on a different (possibly unrelated)
		// branch and its WIP-ness shouldn't influence the PR being created.
		commitMsg, err := g.GetLastCommitMessageOf(branch.Name)
		isWipCommit := err == nil && startsWithWIP(commitMsg)

		// Ask user to choose PR type
		// Default to Draft if commit starts with wip, otherwise Ready for review
		defaultIdx := 1 // Ready for review
		if isWipCommit {
			defaultIdx = 0 // Draft
		}
		prTypeOptions := []string{"Draft", "Ready for review", "Cancel"}
		choice := ui.SelectTUI(prTypeOptions, "Choose PR Type", defaultIdx)
		if choice == -1 || choice == 2 {
			ui.Warn("Cancelled")
			return nil
		}
		isDraft = choice == 0
	}

	prType := "PR"
	if isDraft {
		prType = "draft PR"
	}
	ui.Info(fmt.Sprintf("Will create %s '%s' with base branch: %s", prType, prTitle, branch.Parent))

	if !branch.CanPush() {
		return fmt.Errorf("cannot create PR: push not allowed for '%s' (fork does not allow maintainer push)", branch.Name)
	}

	if err := g.Fetch(); err != nil {
		ui.Warn(fmt.Sprintf("Could not fetch from remote: %v", err))
	}

	branchRemote := branch.EffectiveRemote()
	hasDiverged, localAhead, remoteBehind, err := g.HasDivergedFromRemote(branch.Name, branchRemote)
	if err != nil {
		ui.Warn(fmt.Sprintf("Could not check remote branch status: %v", err))
	}

	ui.Info("Pushing branch to remote...")
	// Always use the branch-explicit push helpers: when --branch <other> is
	// passed, branch.Name is not the current checkout, so Push / PushForce
	// (which use g.CurrentBranch internally) would silently push the wrong
	// branch and then create a PR pointing at code that doesn't match.
	if hasDiverged || remoteBehind > 0 {
		// Remote branch exists with different commits - need force push
		if hasDiverged {
			ui.Warn(fmt.Sprintf("Remote branch '%s' has diverged (local: %d ahead, remote: %d ahead)", branch.Name, localAhead, remoteBehind))
		} else {
			ui.Warn(fmt.Sprintf("Remote branch '%s' is ahead by %d commit(s)", branch.Name, remoteBehind))
		}
		if !ui.ConfirmTUI("Force push to overwrite remote branch?") {
			ui.Warn("Cancelled - cannot create PR without pushing")
			return nil
		}
		if err := g.PushForceBranch(branch.Name, branchRemote); err != nil {
			return fmt.Errorf("failed to force push: %w", err)
		}
	} else if g.RemoteHasBranch(branchRemote, branch.Name) {
		// Remote exists and local is ahead or in sync - regular push should work
		if err := g.PushBranch(branch.Name, false, branchRemote); err != nil {
			return fmt.Errorf("failed to push: %w", err)
		}
	} else {
		// Remote doesn't exist - set upstream
		if err := g.PushBranchSetUpstream(branch.Name, branchRemote); err != nil {
			return fmt.Errorf("failed to push: %w", err)
		}
	}

	ui.Info(fmt.Sprintf("Creating %s with base branch: %s", prType, branch.Parent))
	pr, err := gh.CreatePR(prTitle, prBody, branch.Name, branch.Parent, isDraft)
	if err != nil {
		return fmt.Errorf("failed to create PR: %w. Check that the branch is pushed and you have repo access", err)
	}

	branch.PRNumber = pr.Number
	branch.PRUrl = pr.URL
	branch.PRState = prStateFromGitHub(pr)
	branch.IsMerged = pr.Merged

	savePRToCache(getMainWorktreePath(g), branch.Name, pr)

	ui.Success(fmt.Sprintf("Created %s #%d: %s", prType, pr.Number, pr.URL))

	if err := updateStackDescriptions(gh, currentStack, branch.Name); err != nil {
		ui.Warn(fmt.Sprintf("Failed to update stack descriptions: %v", err))
	}

	return nil
}

// formatBranchTitle converts a branch name like "fix/my-cool-feature" into "Fix my cool feature".
func formatBranchTitle(branch string) string {
	// Strip common prefixes like "feature/", "fix/", etc.
	if idx := strings.LastIndex(branch, "/"); idx >= 0 {
		branch = branch[idx+1:]
	}
	title := strings.ReplaceAll(branch, "-", " ")
	title = strings.ReplaceAll(title, "_", " ")
	if len(title) > 0 {
		title = strings.ToUpper(title[:1]) + title[1:]
	}
	return title
}

// startsWithWIP checks if a string starts with "wip" (case-insensitive)
func startsWithWIP(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return strings.HasPrefix(s, "wip")
}

func prUpdate(args []string) error {
	fs := pflag.NewFlagSet("pr update", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sPush changes and update PR metadata%s

%sUSAGE%s
    ezs pr update [options]

%sDESCRIPTION%s
    Reconciles cached PR state from GitHub, then pushes code changes and
    updates the PR base branch and stack descriptions to match the current
    stack structure. If the PR has been merged or closed externally, this
    refuses to push and reports the new state.

%sOPTIONS%s
    --branch <name>    Update PR for a specific branch (instead of current)
    -h, --help         Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	branchFlag := fs.String("branch", "", "Update PR for a specific branch")
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

	var currentStack *config.Stack
	var branch *config.Branch

	if *branchFlag != "" {
		branch = mgr.GetBranch(*branchFlag)
		if branch == nil {
			return fmt.Errorf("branch '%s' is not tracked by ezstack", *branchFlag)
		}
		currentStack = mgr.GetStackForBranch(*branchFlag)
		if currentStack == nil {
			return fmt.Errorf("branch '%s' is not in any stack", *branchFlag)
		}
	} else {
		var err error
		currentStack, branch, err = mgr.GetCurrentStack()
		if err != nil {
			return err
		}
	}

	if branch.PRNumber == 0 {
		return fmt.Errorf("no PR exists for this branch. Create one with: ezs pr create")
	}

	// Reconcile cache from GitHub before deciding whether to push. Without
	// this, an externally-merged or externally-closed PR stays cached as
	// OPEN forever and `ezs pr update` silently no-ops or pushes pointlessly.
	// Issue #22: pr_update never re-queries GitHub on the no-push code path.
	originalPRNumber := branch.PRNumber
	gh, ghErr := newGitHubClient(g)
	if ghErr != nil {
		// Origin URL doesn't parse as a GitHub remote (or some other gh
		// init failure). We can't verify cache state — warn explicitly so
		// the user knows the merged/closed-PR refusal won't fire and the
		// push proceeds against potentially-stale cache. Mirrors what
		// `pr refresh` reports on the same failure mode.
		ui.Warn(fmt.Sprintf("Could not verify PR state on GitHub: %v (using cache)", ghErr))
	} else {
		livePR, refreshErr := refreshPRStateFromGitHub(gh, getMainWorktreePath(g), branch)
		switch {
		case refreshErr != nil:
			// Transient error — warn and proceed with the cached state.
			// Better to attempt a useful push than to bail on a flaky network.
			ui.Warn(fmt.Sprintf("Could not refresh PR state from GitHub: %v (using cache)", refreshErr))
		case livePR == nil:
			// applyPRRefresh has already cleared the cache for us, so suggesting
			// `ezs pr unlink` here would only print "no cached PR association".
			// Point the user at the recovery action that still has work to do.
			return fmt.Errorf("PR #%d is no longer on GitHub. Cleared the cached association — run 'ezs pr create' to make a new one", originalPRNumber)
		case livePR.Merged:
			return fmt.Errorf("PR #%d is already merged: %s\nUse 'ezs pr create --force' to recreate, or 'ezs pr unlink' to forget the association", livePR.Number, livePR.URL)
		case livePR.State == "CLOSED":
			return fmt.Errorf("PR #%d is closed: %s\nReopen it on GitHub, or use 'ezs pr create --force' to make a new PR", livePR.Number, livePR.URL)
		}
	}

	// Check if remote branch exists and detect divergence against the
	// branch's effective remote (not always origin — fork PRs target the
	// contributor's fork).
	branchRemote := branch.EffectiveRemote()
	hasDiverged, localAhead, remoteAhead, divergeErr := g.HasDivergedFromRemote(branch.Name, branchRemote)
	if divergeErr != nil {
		ui.Warn(fmt.Sprintf("Could not check remote status: %v", divergeErr))
	}

	remoteBranchExists := g.RemoteHasBranch(branchRemote, branch.Name)
	remoteRef := branchRemote + "/" + branch.Name

	// Determine push type and get commits to show
	var needsForcePush bool
	var commits []git.Commit

	if !remoteBranchExists {
		// First push - no remote branch yet
		commits, _ = g.GetCommitsBetween(branch.Parent, branch.Name)
		ui.Info(fmt.Sprintf("Pushing new branch to PR #%d", branch.PRNumber))
	} else if hasDiverged || remoteAhead > 0 {
		// History has diverged (amended commits, rebase, etc.) - needs force push
		needsForcePush = true
		// Show commits that will be pushed
		commits, _ = g.GetCommitsBetween(remoteRef, branch.Name)
		if len(commits) == 0 {
			// If no new commits, show all local commits (amended case)
			commits, _ = g.GetCommitsBetween(branch.Parent, branch.Name)
		}
	} else if localAhead > 0 {
		// Simple case - local is ahead, regular push works
		commits, _ = g.GetCommitsBetween(remoteRef, branch.Name)
	} else if divergeErr != nil {
		// Status check failed — attempt force push rather than silently skipping
		needsForcePush = true
		commits, _ = g.GetCommitsBetween(branch.Parent, branch.Name)
	} else {
		ui.Success("Already up to date. Nothing to push.")
		return nil
	}

	// Show commits that will be pushed
	if len(commits) > 0 {
		fmt.Fprintln(os.Stderr)
		if needsForcePush {
			ui.Warn("History has changed (amended/rebased). Force push required.")
			fmt.Fprintf(os.Stderr, "\n%sCommits to push:%s\n", ui.Cyan, ui.Reset)
		} else {
			fmt.Fprintf(os.Stderr, "%sNew commits to push:%s\n", ui.Cyan, ui.Reset)
		}
		for _, c := range commits {
			fmt.Fprintf(os.Stderr, "  %s%.7s%s %s\n", ui.Yellow, c.Hash, ui.Reset, c.Subject)
		}
		fmt.Fprintln(os.Stderr)
	}

	// Confirm with appropriate message
	var confirmMsg string
	if needsForcePush {
		confirmMsg = fmt.Sprintf("Force push to PR #%d? (overwrites remote history)", branch.PRNumber)
	} else {
		confirmMsg = fmt.Sprintf("Push %d commit(s) to PR #%d?", len(commits), branch.PRNumber)
	}

	if !branch.CanPush() {
		return fmt.Errorf("push not allowed for '%s' (fork does not allow maintainer push)", branch.Name)
	}

	if !ui.ConfirmTUI(confirmMsg) {
		ui.Warn("Cancelled")
		return nil
	}

	ui.Info("Pushing changes...")
	if err := g.PushBranch(branch.Name, needsForcePush, branchRemote); err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}

	ui.Success(fmt.Sprintf("Updated PR #%d", branch.PRNumber))

	// Reuse the gh client already constructed for the reconcile pass — we
	// only need to update PR metadata when GitHub is reachable, and that
	// determination was already made above.
	if ghErr == nil {
		updatePRMetadata(gh, currentStack, branch)
	}

	return nil
}

func prMerge(args []string) error {
	fs := pflag.NewFlagSet("pr merge", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sMerge a pull request%s

%sUSAGE%s
    ezs pr merge [options]

%sOPTIONS%s
    -m, --method <method>  Merge method: merge, squash, rebase (default: squash)
    --branch <name>        Merge PR for a specific branch (instead of current)
    --no-delete-branch     Don't delete the remote branch after merge
    -h, --help             Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	method := fs.StringP("method", "m", "", "Merge method (merge, squash, rebase)")
	branchFlag := fs.String("branch", "", "Merge PR for a specific branch")
	noDeleteBranch := fs.Bool("no-delete-branch", false, "Don't delete remote branch after merge")
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

	var branch *config.Branch

	if *branchFlag != "" {
		branch = mgr.GetBranch(*branchFlag)
		if branch == nil {
			return fmt.Errorf("branch '%s' is not tracked by ezstack", *branchFlag)
		}
		if mgr.GetStackForBranch(*branchFlag) == nil {
			return fmt.Errorf("branch '%s' is not in any stack", *branchFlag)
		}
	} else {
		var err error
		_, branch, err = mgr.GetCurrentStack()
		if err != nil {
			return err
		}
	}

	if branch.PRNumber == 0 {
		return fmt.Errorf("no PR exists for branch '%s'. Create one with: ezs pr create", branch.Name)
	}

	gh, err := newGitHubClient(g)
	if err != nil {
		return err
	}

	// Fetch PR details to show status
	pr, err := gh.GetPR(branch.PRNumber)
	if err != nil {
		return fmt.Errorf("failed to get PR: %w", err)
	}

	if pr.Merged {
		return fmt.Errorf("PR #%d is already merged", branch.PRNumber)
	}
	if pr.State == "CLOSED" {
		return fmt.Errorf("PR #%d is closed. Reopen it on GitHub first", branch.PRNumber)
	}

	// Choose merge method
	mergeMethod := *method
	if mergeMethod == "" {
		methodOptions := []string{"Squash and merge", "Create a merge commit", "Rebase and merge"}
		choice := ui.SelectTUI(methodOptions, "Merge method", 0)
		if choice == -1 {
			ui.Warn("Cancelled")
			return nil
		}
		switch choice {
		case 0:
			mergeMethod = "squash"
		case 1:
			mergeMethod = "merge"
		case 2:
			mergeMethod = "rebase"
		}
	}

	// Validate method
	switch mergeMethod {
	case "merge", "squash", "rebase":
		// valid
	default:
		return fmt.Errorf("invalid merge method: %s. Must be one of: merge, squash, rebase", mergeMethod)
	}

	deleteRemoteBranch := !*noDeleteBranch

	ui.Info(fmt.Sprintf("Merging PR #%d (%s) via %s", branch.PRNumber, branch.Name, mergeMethod))
	if !ui.ConfirmTUI(fmt.Sprintf("Merge PR #%d via %s?", branch.PRNumber, mergeMethod)) {
		ui.Warn("Cancelled")
		return nil
	}

	if err := gh.MergePR(branch.PRNumber, mergeMethod, deleteRemoteBranch); err != nil {
		return fmt.Errorf("failed to merge PR: %w. Check for required reviews, CI status, or branch protection rules", err)
	}

	ui.Success(fmt.Sprintf("Merged PR #%d via %s", branch.PRNumber, mergeMethod))

	if ui.ConfirmTUIWithDefault("Run sync to update the stack and clean up merged branches?", true) {
		return Sync([]string{"-s"})
	}

	ui.Info("Run 'ezs sync' later to update the stack")
	return nil
}

func prDraft(args []string) error {
	fs := pflag.NewFlagSet("pr draft", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sToggle PR between draft and ready%s

%sUSAGE%s
    ezs pr draft [options]

%sOPTIONS%s
    --branch <name>    Toggle draft for a specific branch (instead of current)
    -h, --help         Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	branchFlag := fs.String("branch", "", "Toggle draft for a specific branch")
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

	var branch *config.Branch

	if *branchFlag != "" {
		branch = mgr.GetBranch(*branchFlag)
		if branch == nil {
			return fmt.Errorf("branch '%s' is not tracked by ezstack", *branchFlag)
		}
	} else {
		var err error
		_, branch, err = mgr.GetCurrentStack()
		if err != nil {
			return err
		}
	}

	if branch.PRNumber == 0 {
		return fmt.Errorf("no PR exists for branch '%s'. Create one with: ezs pr create", branch.Name)
	}

	gh, err := newGitHubClient(g)
	if err != nil {
		return err
	}

	pr, err := gh.GetPR(branch.PRNumber)
	if err != nil {
		return fmt.Errorf("failed to get PR: %w", err)
	}

	if pr.Merged {
		return fmt.Errorf("PR #%d is already merged", branch.PRNumber)
	}

	if pr.IsDraft {
		ui.Info(fmt.Sprintf("PR #%d is currently a draft. Marking as ready for review...", branch.PRNumber))
		if err := gh.SetPRReady(branch.PRNumber); err != nil {
			return fmt.Errorf("failed to mark PR as ready: %w", err)
		}
		ui.Success(fmt.Sprintf("PR #%d is now ready for review", branch.PRNumber))
	} else {
		ui.Info(fmt.Sprintf("PR #%d is currently ready for review. Converting to draft...", branch.PRNumber))
		if err := gh.SetPRDraft(branch.PRNumber); err != nil {
			return fmt.Errorf("failed to convert PR to draft: %w", err)
		}
		ui.Success(fmt.Sprintf("PR #%d is now a draft", branch.PRNumber))
	}

	return nil
}

func prStack(args []string) error {
	fs := pflag.NewFlagSet("pr stack", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sUpdate all PR descriptions with stack info%s

%sUSAGE%s
    ezs pr stack [options]

%sOPTIONS%s
    --branch <name>  Target a specific branch's stack (instead of current)
    -h, --help       Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	branchFlag := fs.String("branch", "", "Target a specific branch's stack")
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

	var currentStack *config.Stack
	var branch *config.Branch

	if *branchFlag != "" {
		branch = mgr.GetBranch(*branchFlag)
		if branch == nil {
			return fmt.Errorf("branch '%s' is not tracked by ezstack", *branchFlag)
		}
		currentStack = mgr.GetStackForBranch(*branchFlag)
		if currentStack == nil {
			return fmt.Errorf("branch '%s' is not in any stack", *branchFlag)
		}
	} else {
		var err error
		currentStack, branch, err = mgr.GetCurrentStack()
		if err != nil {
			return err
		}
	}

	gh, err := newGitHubClient(g)
	if err != nil {
		return err
	}

	prCount := 0
	for _, b := range currentStack.Branches {
		if b.PRNumber > 0 {
			prCount++
		}
	}

	ui.Info(fmt.Sprintf("Will update stack descriptions in %d PR(s)", prCount))
	if !ui.ConfirmTUI("Update all PR stack descriptions") {
		ui.Warn("Cancelled")
		return nil
	}

	if err := updateStackDescriptions(gh, currentStack, branch.Name); err != nil {
		return err
	}

	ui.Success("Stack descriptions updated in all PRs")
	return nil
}

// prRefresh reconciles the local PR cache for one or more branches by
// querying GitHub. Companion to issue #22 — `pr update` already does an
// implicit refresh before pushing, but users sometimes want explicit
// reconciliation (e.g., after manually closing a PR via the GitHub UI).
func prRefresh(args []string) error {
	fs := pflag.NewFlagSet("pr refresh", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sReconcile PR cache from GitHub%s

%sUSAGE%s
    ezs pr refresh [options]

%sDESCRIPTION%s
    Queries GitHub for the current state of one or more PRs and updates
    the local cache to match. Use after PRs have been merged, closed, or
    re-targeted via the GitHub UI to bring 'ezs ls' back in sync.

%sOPTIONS%s
    --branch <name>    Refresh a specific branch (instead of current)
    -s, --stack        Refresh every PR in the current stack
    -h, --help         Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	branchFlag := fs.String("branch", "", "Refresh a specific branch")
	stackFlag := fs.BoolP("stack", "s", false, "Refresh every PR in the current stack")
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
	if *stackFlag && *branchFlag != "" {
		return fmt.Errorf("--stack and --branch are mutually exclusive")
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
	gh, err := newGitHubClient(g)
	if err != nil {
		return err
	}

	mainWorktree := getMainWorktreePath(g)

	// Collect targets.
	var branches []*config.Branch
	if *stackFlag {
		currentStack, _, err := mgr.GetCurrentStack()
		if err != nil {
			return err
		}
		for _, b := range currentStack.Branches {
			// Match the single-branch filter below: a branch with a cached PRUrl
			// but no parsed PRNumber (URL parse failure on legacy entries) is
			// still a valid refresh target — fetchLivePR will fall through to
			// the head-branch lookup and either rediscover or clear it.
			if b.PRNumber > 0 || b.PRUrl != "" {
				branches = append(branches, b)
			}
		}
		if len(branches) == 0 {
			ui.Info("No branches in the current stack have a cached PR.")
			return nil
		}
	} else {
		var b *config.Branch
		if *branchFlag != "" {
			b = mgr.GetBranch(*branchFlag)
			if b == nil {
				return fmt.Errorf("branch '%s' is not tracked by ezstack", *branchFlag)
			}
		} else {
			_, b, err = mgr.GetCurrentStack()
			if err != nil {
				return err
			}
		}
		if b.PRNumber == 0 && b.PRUrl == "" {
			ui.Info(fmt.Sprintf("Branch '%s' has no cached PR association.", b.Name))
			return nil
		}
		branches = append(branches, b)
	}

	// Fetch each branch's live PR in parallel. Apply the results serially
	// below — applyPRRefresh hits the on-disk cache, and concurrent
	// load-modify-save calls against stacks.json would race (each
	// goroutine reads the file before the others' saves land, then
	// overwrites the whole branches map on save). The network fetch is
	// the slow part anyway.
	type result struct {
		branch   *config.Branch
		oldNum   int
		oldURL   string
		oldState string
		newPR    *github.PR
		err      error
	}
	results := make([]result, len(branches))

	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)
	for i, b := range branches {
		wg.Add(1)
		go func(idx int, br *config.Branch) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			oldNum := br.PRNumber
			oldURL := br.PRUrl
			oldState := br.PRState
			pr, fetchErr := fetchLivePR(gh, br)
			results[idx] = result{branch: br, oldNum: oldNum, oldURL: oldURL, oldState: oldState, newPR: pr, err: fetchErr}
		}(i, b)
	}
	wg.Wait()

	changed := 0
	failed := 0
	for _, r := range results {
		newPR, applyErr := applyPRRefresh(mainWorktree, r.branch, r.newPR, r.err)
		switch {
		case applyErr != nil:
			ui.Warn(fmt.Sprintf("%s: refresh failed (%v)", r.branch.Name, applyErr))
			failed++
		case newPR == nil:
			// applyPRRefresh cleared the cache iff the branch had a cached
			// association before. Track that via either oldNum (parsed
			// successfully) or oldURL (raw cache value, covers the rare
			// malformed-URL case where PRNumber failed to parse). Without
			// the oldURL check, a successful clear in the malformed-URL
			// case shows up as "no PR on GitHub" with changed=0 — and the
			// summary then claims everything was already in sync.
			switch {
			case r.oldNum > 0:
				fmt.Fprintf(os.Stderr, "  %s %s: PR #%d no longer on GitHub (cache cleared)\n", ui.IconBullet, r.branch.Name, r.oldNum)
				changed++
			case r.oldURL != "":
				fmt.Fprintf(os.Stderr, "  %s %s: cached PR URL %q no longer on GitHub (cache cleared)\n", ui.IconBullet, r.branch.Name, r.oldURL)
				changed++
			default:
				fmt.Fprintf(os.Stderr, "  %s %s: no PR on GitHub\n", ui.IconBullet, r.branch.Name)
			}
		default:
			newState := prStateFromGitHub(newPR)
			if r.oldNum == newPR.Number && r.oldState == newState {
				fmt.Fprintf(os.Stderr, "  %s %s: PR #%d unchanged (%s)\n", ui.IconBullet, r.branch.Name, newPR.Number, newState)
				continue
			}
			if r.oldNum == 0 {
				fmt.Fprintf(os.Stderr, "  %s %s: discovered PR #%d (%s)\n", ui.IconBullet, r.branch.Name, newPR.Number, newState)
			} else if r.oldNum != newPR.Number {
				fmt.Fprintf(os.Stderr, "  %s %s: PR #%d -> #%d (%s)\n", ui.IconBullet, r.branch.Name, r.oldNum, newPR.Number, newState)
			} else {
				fmt.Fprintf(os.Stderr, "  %s %s: PR #%d %s -> %s\n", ui.IconBullet, r.branch.Name, newPR.Number, displayPRState(r.oldState), newState)
			}
			changed++
		}
	}
	switch {
	case changed > 0 && failed > 0:
		ui.Warn(fmt.Sprintf("Refreshed %d branch(es); %d failed (cache left untouched for those)", changed, failed))
	case changed > 0:
		ui.Success(fmt.Sprintf("Refreshed %d branch(es)", changed))
	case failed > 0:
		// Distinguish total failure from "everything was already in sync".
		// Returning an error here lets scripts (and `pr update`'s later
		// caller chain) know the cache wasn't reconciled.
		return fmt.Errorf("refresh failed for %d branch(es); cache left as-is", failed)
	default:
		ui.Info("All branches already in sync with GitHub.")
	}
	return nil
}

// prUnlink clears the cached PR association for one or more branches without
// touching GitHub. Recovery primitive for issue #22 — when local cache and
// GitHub disagree and the user wants to start fresh, this is the scripted
// way to forget a stale association without hand-editing stacks.json.
func prUnlink(args []string) error {
	fs := pflag.NewFlagSet("pr unlink", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sClear cached PR association for a branch%s

%sUSAGE%s
    ezs pr unlink [options]

%sDESCRIPTION%s
    Removes the local cache entry that links a branch to a GitHub PR
    (pr_url, pr_state, is_merged). Does not touch GitHub — the PR itself is
    unaffected. Use when the cached association has gone stale and you want
    'ezs pr create' to make a fresh PR for the branch.

%sOPTIONS%s
    --branch <name>    Unlink a specific branch (instead of current)
    --all              Unlink every branch in the current stack
    -y, --yes          Skip the confirmation prompt
    -h, --help         Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	branchFlag := fs.String("branch", "", "Unlink a specific branch")
	allFlag := fs.Bool("all", false, "Unlink every branch in the current stack")
	yes := fs.BoolP("yes", "y", false, "Skip confirmation")
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
	if *allFlag && *branchFlag != "" {
		return fmt.Errorf("--all and --branch are mutually exclusive")
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

	mainWorktree := getMainWorktreePath(g)

	// Collect targets.
	type target struct {
		name     string
		prNumber int
		prURL    string
		prState  string
	}
	var targets []target

	if *allFlag {
		currentStack, _, err := mgr.GetCurrentStack()
		if err != nil {
			return err
		}
		for _, b := range currentStack.Branches {
			if b.PRNumber == 0 && b.PRUrl == "" {
				continue
			}
			targets = append(targets, target{name: b.Name, prNumber: b.PRNumber, prURL: b.PRUrl, prState: b.PRState})
		}
		if len(targets) == 0 {
			ui.Info("No branches in the current stack have a cached PR.")
			return nil
		}
	} else {
		var b *config.Branch
		if *branchFlag != "" {
			b = mgr.GetBranch(*branchFlag)
			if b == nil {
				return fmt.Errorf("branch '%s' is not tracked by ezstack", *branchFlag)
			}
		} else {
			_, b, err = mgr.GetCurrentStack()
			if err != nil {
				return err
			}
		}
		if b.PRNumber == 0 && b.PRUrl == "" {
			ui.Info(fmt.Sprintf("Branch '%s' has no cached PR association.", b.Name))
			return nil
		}
		targets = append(targets, target{name: b.Name, prNumber: b.PRNumber, prURL: b.PRUrl, prState: b.PRState})
	}

	// Show what will be cleared.
	if len(targets) == 1 {
		t := targets[0]
		if t.prNumber > 0 {
			ui.Info(fmt.Sprintf("Will unlink PR #%d (%s) from branch '%s'", t.prNumber, displayPRState(t.prState), t.name))
		} else {
			ui.Info(fmt.Sprintf("Will clear cached PR URL %q from branch '%s'", t.prURL, t.name))
		}
	} else {
		ui.Info(fmt.Sprintf("Will unlink cached PR associations from %d branch(es):", len(targets)))
		for _, t := range targets {
			if t.prNumber > 0 {
				fmt.Fprintf(os.Stderr, "  %s %s -> PR #%d (%s)\n", ui.IconBullet, t.name, t.prNumber, displayPRState(t.prState))
			} else {
				fmt.Fprintf(os.Stderr, "  %s %s -> %s\n", ui.IconBullet, t.name, t.prURL)
			}
		}
	}

	if !*yes {
		if !ui.ConfirmTUI("Proceed?") {
			ui.Warn("Cancelled")
			return nil
		}
	}

	cleared := 0
	failed := 0
	for _, t := range targets {
		if err := clearPRFromCache(mainWorktree, t.name); err != nil {
			ui.Warn(fmt.Sprintf("Failed to unlink %s: %v", t.name, err))
			failed++
			continue
		}
		// Also clear in-memory mirror so subsequent commands in this process
		// see the unlinked state.
		if b := mgr.GetBranch(t.name); b != nil {
			b.PRNumber = 0
			b.PRUrl = ""
			b.PRState = ""
			b.IsMerged = false
		}
		cleared++
	}
	switch {
	case cleared > 0 && failed > 0:
		ui.Warn(fmt.Sprintf("Unlinked %d branch(es); %d failed", cleared, failed))
	case cleared > 0:
		ui.Success(fmt.Sprintf("Unlinked %d branch(es)", cleared))
	default:
		// Surface as a non-zero exit so scripts notice — `ui.Success("Unlinked
		// 0 branch(es)")` was actively misleading when every clear failed.
		return fmt.Errorf("unlink failed for all %d branch(es)", failed)
	}
	return nil
}
