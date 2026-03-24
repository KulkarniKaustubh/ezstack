package commands

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/github"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
	"github.com/spf13/pflag"
)

func printPRUsage() {
	fmt.Fprintf(os.Stderr, `%sManage pull requests%s

%sUSAGE%s
    ezs pr <subcommand> [options]

%sSUBCOMMANDS%s
    create    Create a new pull request
    update    Push changes to existing PR
    merge     Merge a pull request
    draft     Toggle PR between draft and ready
    stack     Update all PR descriptions with stack info

%sOPTIONS%s
    -h, --help    Show this help message

Run 'ezs pr <subcommand> --help' for subcommand options.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
}

// PR handles pull request operations
func PR(args []string) error {
	// Allow --help without requiring auth
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		printPRUsage()
		return nil
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

// prCreateAll creates PRs for all branches in the stack that don't have PRs
func prCreateAll(currentStack *config.Stack) error {
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
		if b.PRNumber == 0 {
			// Check GitHub for an existing open PR (handles stale cache)
			existingPR, err := gh.GetPRByBranch(b.Name)
			if err == nil && existingPR != nil && !existingPR.Merged && existingPR.State != "CLOSED" {
				b.PRNumber = existingPR.Number
				b.PRUrl = existingPR.URL
				savePRToCache(mainWorktree, b.Name, existingPR.Number, existingPR.URL)
				ui.Info(fmt.Sprintf("Branch '%s' already has PR #%d (updated local cache)", b.Name, existingPR.Number))
				continue
			}

			// Check if branch has commits ahead of its base
			commitsAhead, err := g.GetCommitsAhead(b.Name, b.Parent)
			if err != nil {
				ui.Warn(fmt.Sprintf("Could not check commits for %s: %v (skipping)", b.Name, err))
				continue
			}
			if commitsAhead == 0 {
				ui.Warn(fmt.Sprintf("Skipping %s: no commits ahead of '%s'", b.Name, b.Parent))
				continue
			}
			branchesToCreate = append(branchesToCreate, b)
		}
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

		// Push the branch first
		if err := runGitCommand(cwd, "push", "-u", "origin", b.Name); err != nil {
			ui.Warn(fmt.Sprintf("Failed to push %s: %v", b.Name, err))
			failed++
			continue
		}

		// Create the PR (not as draft for bulk creation)
		pr, err := gh.CreatePR(b.Name, "", b.Name, b.Parent, false)
		if err != nil {
			ui.Warn(fmt.Sprintf("Failed to create PR for %s: %v", b.Name, err))
			failed++
			continue
		}

		b.PRNumber = pr.Number
		b.PRUrl = pr.URL
		savePRToCache(mainWorktree, b.Name, pr.Number, pr.URL)
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

// runGitCommand runs a git command
func runGitCommand(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func prCreate(args []string) error {
	fs := pflag.NewFlagSet("pr create", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sCreate a new pull request%s

%sUSAGE%s
    ezs pr create [options]

%sOPTIONS%s
    -t, --title <title>    PR title (defaults to branch name)
    -b, --body <body>      PR body/description
    -d, --draft            Create as draft PR
    -h, --help             Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	title := fs.StringP("title", "t", "", "PR title")
	body := fs.StringP("body", "b", "", "PR body")
	draft := fs.BoolP("draft", "d", false, "Create as draft PR")
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

	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		return err
	}

	if branch.PRNumber > 0 {
		return fmt.Errorf("branch '%s' already has PR #%d: %s\nTo push updates, use: ezs pr update", branch.Name, branch.PRNumber, branch.PRUrl)
	}

	commitsAhead, err := g.GetCommitsAhead(branch.Name, branch.Parent)
	if err != nil {
		// If we can't determine, continue anyway (might be a new branch)
		ui.Warn(fmt.Sprintf("Could not check commits: %v", err))
	} else if commitsAhead == 0 {
		return fmt.Errorf("no commits to create PR from. This branch has no commits ahead of '%s'.\nPlease make at least one commit first", branch.Parent)
	}

	gh, err := newGitHubClient(g)
	if err != nil {
		return err
	}

	// Check if a PR already exists on GitHub for this branch (handles stale cache)
	existingPR, err := gh.GetPRByBranch(branch.Name)
	if err == nil && existingPR != nil && !existingPR.Merged && existingPR.State != "CLOSED" {
		// Update local cache with the existing PR
		branch.PRNumber = existingPR.Number
		branch.PRUrl = existingPR.URL
		savePRToCache(getMainWorktreePath(g), branch.Name, existingPR.Number, existingPR.URL)
		return fmt.Errorf("branch '%s' already has an open PR #%d: %s\nLocal cache has been updated. To push updates, use: ezs pr update", branch.Name, existingPR.Number, existingPR.URL)
	}

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
		commitMsg, err := g.GetLastCommitMessage()
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

	if err := g.Fetch(); err != nil {
		ui.Warn(fmt.Sprintf("Could not fetch from remote: %v", err))
	}

	hasDiverged, localAhead, remoteBehind, err := g.HasDivergedFromOrigin(branch.Name)
	if err != nil {
		ui.Warn(fmt.Sprintf("Could not check remote branch status: %v", err))
	}

	ui.Info("Pushing branch to remote...")
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
		if err := g.PushForce(); err != nil {
			return fmt.Errorf("failed to force push: %w", err)
		}
	} else if g.RemoteBranchExists(branch.Name) {
		// Remote exists and local is ahead or in sync - regular push should work
		if err := g.Push(false); err != nil {
			return fmt.Errorf("failed to push: %w", err)
		}
	} else {
		// Remote doesn't exist - set upstream
		if err := g.PushSetUpstream(); err != nil {
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

	savePRToCache(getMainWorktreePath(g), branch.Name, pr.Number, pr.URL)

	ui.Success(fmt.Sprintf("Created %s #%d: %s", prType, pr.Number, pr.URL))

	if err := updateStackDescriptions(gh, currentStack, branch.Name); err != nil {
		ui.Warn(fmt.Sprintf("Failed to update stack descriptions: %v", err))
	}

	return nil
}

// startsWithWIP checks if a string starts with "wip" (case-insensitive)
func startsWithWIP(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return strings.HasPrefix(s, "wip")
}

func prUpdate(args []string) error {
	fs := pflag.NewFlagSet("pr update", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sPush changes to existing pull request%s

%sUSAGE%s
    ezs pr update [options]

%sOPTIONS%s
    -h, --help    Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
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

	_, branch, err := mgr.GetCurrentStack()
	if err != nil {
		return err
	}

	if branch.PRNumber == 0 {
		return fmt.Errorf("no PR exists for this branch. Create one with: ezs pr create")
	}

	// Check if remote branch exists and detect divergence
	hasDiverged, localAhead, remoteAhead, err := g.HasDivergedFromOrigin(branch.Name)
	if err != nil {
		ui.Warn(fmt.Sprintf("Could not check remote status: %v", err))
	}

	remoteBranchExists := g.RemoteBranchExists(branch.Name)

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
		commits, _ = g.GetCommitsBetween("origin/"+branch.Name, branch.Name)
		if len(commits) == 0 {
			// If no new commits, show all local commits (amended case)
			commits, _ = g.GetCommitsBetween(branch.Parent, branch.Name)
		}
	} else if localAhead > 0 {
		// Simple case - local is ahead, regular push works
		commits, _ = g.GetCommitsBetween("origin/"+branch.Name, branch.Name)
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

	if !ui.ConfirmTUI(confirmMsg) {
		ui.Warn("Cancelled")
		return nil
	}

	ui.Info("Pushing changes...")
	if err := g.Push(needsForcePush); err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}

	ui.Success(fmt.Sprintf("Updated PR #%d", branch.PRNumber))
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
    --no-delete-branch     Don't delete the remote branch after merge
    -h, --help             Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	method := fs.StringP("method", "m", "", "Merge method (merge, squash, rebase)")
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

	_, branch, err := mgr.GetCurrentStack()
	if err != nil {
		return err
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
	ui.Info("Run 'ezs sync' to update the stack and clean up merged branches")

	return nil
}

func prDraft(args []string) error {
	fs := pflag.NewFlagSet("pr draft", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sToggle PR between draft and ready%s

%sUSAGE%s
    ezs pr draft [options]

%sOPTIONS%s
    -h, --help    Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
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

	_, branch, err := mgr.GetCurrentStack()
	if err != nil {
		return err
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
    -h, --help    Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
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

	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		return err
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
