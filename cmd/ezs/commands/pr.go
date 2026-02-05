package commands

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ezstack/ezstack/internal/config"
	"github.com/ezstack/ezstack/internal/git"
	"github.com/ezstack/ezstack/internal/github"
	"github.com/ezstack/ezstack/internal/stack"
	"github.com/ezstack/ezstack/internal/ui"
)

func printPRUsage() {
	fmt.Fprintf(os.Stderr, `%sManage pull requests%s

%sUSAGE%s
    ezs pr <subcommand> [options]

%sSUBCOMMANDS%s
    create    Create a new pull request
    update    Push changes to existing PR
    stack     Update all PR descriptions with stack info

%sOPTIONS%s
    -h, --help    Show this help message

Run 'ezs pr <subcommand> --help' for subcommand options.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
}

// PR handles pull request operations
func PR(args []string) error {
	if len(args) < 1 {
		// Interactive mode
		return prInteractive()
	}

	switch args[0] {
	case "-h", "--help":
		printPRUsage()
		return nil
	case "create":
		return prCreate(args[1:])
	case "update":
		return prUpdate(args[1:])
	case "stack":
		return prStack(args[1:])
	default:
		return fmt.Errorf("unknown pr command: %s", args[0])
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
		return fmt.Errorf("not in a stack. Create a branch first with: ezs new <branch-name>")
	}

	// Show current stack status
	ui.PrintStack(currentStack, branch.Name)

	// Build options based on current state
	options := []string{}
	optionActions := []string{}

	if branch.PRNumber == 0 {
		options = append(options, fmt.Sprintf("%s  Create PR for current branch", ui.IconNew))
		optionActions = append(optionActions, "create")
	} else {
		options = append(options, fmt.Sprintf("%s  Push updates to PR #%d", ui.IconPush, branch.PRNumber))
		optionActions = append(optionActions, "update")
	}

	// Count PRs in stack
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

	// Add option to create PRs for branches without PRs
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
	remoteURL, err := g.GetRemote("origin")
	if err != nil {
		return err
	}

	gh, err := github.NewClient(remoteURL)
	if err != nil {
		return err
	}

	// Get main worktree for saving config
	mainWorktree, _ := g.GetMainWorktree()
	if mainWorktree == "" {
		mainWorktree = cwd
	}

	branchesToCreate := []*config.Branch{}
	for _, b := range currentStack.Branches {
		if b.PRNumber == 0 {
			branchesToCreate = append(branchesToCreate, b)
		}
	}

	if len(branchesToCreate) == 0 {
		ui.Info("All branches already have PRs")
		return nil
	}

	ui.Info(fmt.Sprintf("Will create PRs for %d branches:", len(branchesToCreate)))
	for _, b := range branchesToCreate {
		fmt.Fprintf(os.Stderr, "  %s %s (base: %s)\n", ui.IconBullet, b.Name, b.BaseBranch)
	}

	if !ui.ConfirmTUI("Create all PRs") {
		ui.Warn("Cancelled")
		return nil
	}

	for _, b := range branchesToCreate {
		ui.Info(fmt.Sprintf("Creating PR for %s...", b.Name))

		// Push the branch first (need to be in that worktree or use git push from main)
		pushCmd := fmt.Sprintf("git push -u origin %s", b.Name)
		if err := runGitCommand(cwd, "push", "-u", "origin", b.Name); err != nil {
			ui.Warn(fmt.Sprintf("Failed to push %s: %v (trying %s)", b.Name, err, pushCmd))
			continue
		}

		// Create the PR (not as draft for bulk creation)
		pr, err := gh.CreatePR(b.Name, "", b.Name, b.BaseBranch, false)
		if err != nil {
			ui.Warn(fmt.Sprintf("Failed to create PR for %s: %v", b.Name, err))
			continue
		}

		b.PRNumber = pr.Number
		b.PRUrl = pr.URL
		ui.Success(fmt.Sprintf("Created PR #%d for %s: %s", pr.Number, b.Name, pr.URL))
	}

	// Save the updated config
	stackCfg, _ := config.LoadStackConfig(mainWorktree)
	for _, s := range stackCfg.Stacks {
		if s.Name == currentStack.Name {
			for _, b := range s.Branches {
				for _, updated := range branchesToCreate {
					if b.Name == updated.Name {
						b.PRNumber = updated.PRNumber
						b.PRUrl = updated.PRUrl
					}
				}
			}
		}
	}
	stackCfg.Save(mainWorktree)

	// Update stack descriptions
	ui.Info("Updating PR stack descriptions...")
	if err := gh.UpdateStackDescription(currentStack, ""); err != nil {
		ui.Warn(fmt.Sprintf("Failed to update stack descriptions: %v", err))
	}

	ui.Success("All PRs created!")
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
	fs := flag.NewFlagSet("pr create", flag.ContinueOnError)
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
	// Long flags
	title := fs.String("title", "", "PR title")
	body := fs.String("body", "", "PR body")
	draft := fs.Bool("draft", false, "Create as draft PR")
	// Short flags
	titleShort := fs.String("t", "", "PR title (short)")
	bodyShort := fs.String("b", "", "PR body (short)")
	draftShort := fs.Bool("d", false, "Create as draft PR (short)")
	helpFlag := fs.Bool("h", false, "Show help")

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
	if *titleShort != "" {
		*title = *titleShort
	}
	if *bodyShort != "" {
		*body = *bodyShort
	}
	if *draftShort {
		*draft = true
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

	// Get remote URL for GitHub client
	remoteURL, err := g.GetRemote("origin")
	if err != nil {
		return err
	}

	gh, err := github.NewClient(remoteURL)
	if err != nil {
		return err
	}

	// Prompt for title if not provided, with branch name as default
	prTitle := *title
	if prTitle == "" {
		prTitle = ui.Prompt("PR title", branch.Name)
	}

	// Check for WIP commit and auto-draft setting
	isDraft := *draft
	if !isDraft {
		commitMsg, err := g.GetLastCommitMessage()
		if err == nil && startsWithWIP(commitMsg) {
			// Check if auto-draft is enabled for this repo
			cfg, _ := config.Load()
			mainWorktree, _ := g.GetMainWorktree()
			if mainWorktree == "" {
				mainWorktree = cwd
			}
			repoCfg := cfg.GetRepoConfig(mainWorktree)

			if repoCfg != nil && repoCfg.AutoDraftWipCommits != nil && *repoCfg.AutoDraftWipCommits {
				// Auto-draft is enabled
				isDraft = true
				ui.Info("Auto-creating as draft (commit starts with 'wip')")
			} else {
				// Ask user if they want to create as draft
				if ui.ConfirmTUI("Commit starts with 'wip'. Create as draft PR?") {
					isDraft = true
					// Ask if they want to save this preference
					if ui.ConfirmTUI("Always auto-draft PRs when commit starts with 'wip'?") {
						if repoCfg == nil {
							repoCfg = &config.RepoConfig{RepoPath: mainWorktree}
						}
						autoDraft := true
						repoCfg.AutoDraftWipCommits = &autoDraft
						cfg.SetRepoConfig(mainWorktree, repoCfg)
						if err := cfg.Save(); err != nil {
							ui.Warn(fmt.Sprintf("Failed to save config: %v", err))
						} else {
							ui.Success("Saved preference: auto-draft WIP commits")
						}
					}
				}
			}
		}
	}

	// Show what we're about to do and ask for confirmation
	prType := "PR"
	if isDraft {
		prType = "draft PR"
	}
	ui.Info(fmt.Sprintf("Will create %s '%s' with base branch: %s", prType, prTitle, branch.BaseBranch))
	if !ui.ConfirmTUI(fmt.Sprintf("Create %s", prType)) {
		ui.Warn("Cancelled")
		return nil
	}

	// Push the branch first
	ui.Info("Pushing branch to remote...")
	if err := g.PushSetUpstream(); err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}

	// Create the PR
	ui.Info(fmt.Sprintf("Creating %s with base branch: %s", prType, branch.BaseBranch))
	pr, err := gh.CreatePR(prTitle, *body, branch.Name, branch.BaseBranch, isDraft)
	if err != nil {
		return fmt.Errorf("failed to create PR: %w", err)
	}

	// Update branch metadata
	branch.PRNumber = pr.Number
	branch.PRUrl = pr.URL

	// Save the updated config
	mainWorktree, _ := g.GetMainWorktree()
	if mainWorktree == "" {
		mainWorktree = cwd
	}
	stackCfg, _ := config.LoadStackConfig(mainWorktree)
	for _, s := range stackCfg.Stacks {
		for _, b := range s.Branches {
			if b.Name == branch.Name {
				b.PRNumber = pr.Number
				b.PRUrl = pr.URL
			}
		}
	}
	stackCfg.Save(mainWorktree)

	ui.Success(fmt.Sprintf("Created %s #%d: %s", prType, pr.Number, pr.URL))

	// Update stack description in all PRs
	ui.Info("Updating PR stack descriptions...")
	if err := gh.UpdateStackDescription(currentStack, branch.Name); err != nil {
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
	fs := flag.NewFlagSet("pr update", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sPush changes to existing pull request%s

%sUSAGE%s
    ezs pr update [options]

%sOPTIONS%s
    -h, --help    Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	helpFlag := fs.Bool("h", false, "Show help")
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

	// Ask for confirmation
	ui.Info(fmt.Sprintf("Will push changes to PR #%d", branch.PRNumber))
	if !ui.ConfirmTUI(fmt.Sprintf("Push changes to PR #%d", branch.PRNumber)) {
		ui.Warn("Cancelled")
		return nil
	}

	// Push changes
	ui.Info("Pushing changes...")
	if err := g.Push(true); err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}

	ui.Success(fmt.Sprintf("Updated PR #%d", branch.PRNumber))
	return nil
}

func prStack(args []string) error {
	fs := flag.NewFlagSet("pr stack", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sUpdate all PR descriptions with stack info%s

%sUSAGE%s
    ezs pr stack [options]

%sOPTIONS%s
    -h, --help    Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	helpFlag := fs.Bool("h", false, "Show help")
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

	remoteURL, err := g.GetRemote("origin")
	if err != nil {
		return err
	}

	gh, err := github.NewClient(remoteURL)
	if err != nil {
		return err
	}

	// Count PRs to be updated
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

	ui.Info("Updating PR stack descriptions...")
	if err := gh.UpdateStackDescription(currentStack, branch.Name); err != nil {
		return err
	}

	ui.Success("Stack descriptions updated in all PRs")
	return nil
}
