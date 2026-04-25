package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/github"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/helpers"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

// New creates a new branch in the stack
func New(args []string) error {
	if HasExamplesFlag("new", args) {
		return nil
	}
	fs := pflag.NewFlagSet("new", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sCreate a new branch in the stack%s

%sUSAGE%s
    ezs new [branch-name] [options]

%sOPTIONS%s
    --template <name>         Seed worktree from ~/.ezstack/templates/<name>
    -p, --parent <branch>     Parent branch (defaults to current branch)
    -w, --worktree <path>     Worktree path (defaults to configured base dir + branch name)
    -c, --cd                  Change to the new worktree after creation
    -C, --no-cd               Don't change to the new worktree (overrides config)
    -s, --init-submodules     Mirror main worktree's initialized submodules (overrides config)
    -S, --no-init-submodules  Skip submodule initialization (overrides config)
    -f, --from-worktree       Register an existing worktree as a stack root
    -r, --from-remote         Create a stack from a remote branch/PR
    -h, --help                Show this help message

%sNOTES%s
    If no arguments are provided, interactive mode will prompt for options.

    With origin/<branch>, creates a local worktree tracking the remote branch:
      ezs new origin/feature-branch       Checkout remote branch into a worktree (for PR reviews, etc.)

    With --from-remote, positional args are: [pr-number-or-branch] [new-branch-name]
      ezs new -r                          Interactive PR selection + branch name prompt
      ezs new -r 42                       Use PR #42, prompt for branch name
      ezs new -r feature-branch           Use PR for that branch, prompt for branch name
      ezs new -r 42 my-feature            Use PR #42, create branch "my-feature" (no prompts)

    For cd to work, add this to your ~/.bashrc or ~/.zshrc:
        eval "$(ezs --shell-init)"
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	parent := fs.StringP("parent", "p", "", "Parent branch")
	worktree := fs.StringP("worktree", "w", "", "Worktree path")
	templateFlag := fs.String("template", "", "Seed the new worktree from ~/.ezstack/templates/<name>")
	cdFlag := fs.BoolP("cd", "c", false, "Change to worktree")
	noCdFlag := fs.BoolP("no-cd", "C", false, "Don't change to worktree")
	initSubmodulesFlag := fs.BoolP("init-submodules", "s", false, "Mirror main worktree's initialized submodules")
	noInitSubmodulesFlag := fs.BoolP("no-init-submodules", "S", false, "Skip submodule initialization")
	fromWorktree := fs.BoolP("from-worktree", "f", false, "Register an existing worktree as a stack root")
	fromRemote := fs.BoolP("from-remote", "r", false, "Create stack from remote branch")
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

	var parentBranch string
	useFromWorktree := *fromWorktree
	useFromRemote := *fromRemote
	chooseParent := false

	// Check if the first arg is a remote branch reference (origin/...)
	// This creates a local worktree tracking the remote branch directly,
	// without creating a new branch on top or registering a stack.
	if fs.NArg() >= 1 && strings.HasPrefix(fs.Arg(0), "origin/") {
		return newFromRemoteRef(g, cwd, fs.Arg(0), *worktree, *cdFlag, *noCdFlag, *initSubmodulesFlag, *noInitSubmodulesFlag)
	}

	if fs.NArg() == 0 && !useFromWorktree && !useFromRemote && *parent == "" {
		choice, err := ui.SelectOptionWithBack([]string{
			"Create a new branch (use current branch as parent)",
			"Create a new branch (choose parent branch)",
			"Register an existing worktree as a stack root",
			"Create a stack from a remote branch",
		}, "What would you like to do?")
		if err != nil {
			if err == ui.ErrBack {
				return ui.ErrBack
			}
			return err
		}
		if choice == 1 {
			chooseParent = true
		} else if choice == 2 {
			useFromWorktree = true
		} else if choice == 3 {
			useFromRemote = true
		}
	}

	if useFromWorktree {
		worktrees, err := g.ListWorktrees()
		if err != nil {
			return fmt.Errorf("failed to list worktrees: %w", err)
		}

		if len(worktrees) == 0 {
			return fmt.Errorf("no worktrees found")
		}

		wtInfos := make([]ui.WorktreeInfo, len(worktrees))
		for i, wt := range worktrees {
			wtInfos[i] = ui.WorktreeInfo{
				Path:   wt.Path,
				Branch: wt.Branch,
			}
		}

		selected, err := ui.SelectWorktree(wtInfos, "Select worktree to register as stack root")
		if err != nil {
			return err
		}

		mgr, err := stack.NewManager(cwd)
		if err != nil {
			return err
		}

		cfg := mgr.GetConfig()
		baseBranch := cfg.GetBaseBranch(mgr.GetRepoDir())

		ui.Info(fmt.Sprintf("Registering '%s' as a stack root (base: %s)", selected.Branch, baseBranch))
		ui.Info(fmt.Sprintf("Worktree path: %s", selected.Path))

		if !ui.ConfirmTUI(fmt.Sprintf("Register '%s' as a new stack?", selected.Branch)) {
			ui.Warn("Cancelled")
			return nil
		}

		branch, err := mgr.RegisterExistingBranch(selected.Branch, selected.Path, baseBranch)
		if err != nil {
			return err
		}

		// Prompt for stack name
		promptStackName(mgr, branch.Name)

		gh, ghErr := newGitHubClient(g)
		if ghErr == nil {
			pr, err := gh.GetPRByBranch(selected.Branch)
			if err == nil && pr != nil && pr.Number > 0 {
				branch.PRNumber = pr.Number
				branch.PRUrl = pr.URL
				savePRToCache(mgr.GetRepoDir(), branch.Name, pr.Number, pr.URL)

				ui.Success(fmt.Sprintf("Registered '%s' as a stack root (found existing PR #%d)", branch.Name, pr.Number))
				ui.Info("You can now add child branches with: ezs new <branch-name>")
				if getCdAfterNew(cfg, mgr.GetRepoDir(), *cdFlag, *noCdFlag) {
					EmitCd(selected.Path)
				}
				return nil
			}
		}

		ui.Success(fmt.Sprintf("Registered '%s' as a stack root", branch.Name))
		ui.Info("You can now add child branches with: ezs new <branch-name>")
		if getCdAfterNew(cfg, mgr.GetRepoDir(), *cdFlag, *noCdFlag) {
			EmitCd(selected.Path)
		}
		return nil
	}

	if useFromRemote {
		mgr, err := stack.NewManager(cwd)
		if err != nil {
			return err
		}

		// First positional arg is the PR identifier (number or branch name)
		prIdentifier := ""
		if fs.NArg() >= 1 {
			prIdentifier = fs.Arg(0)
		}

		remote, err := selectAndRegisterRemoteBranch(g, mgr, prIdentifier)
		if err != nil {
			return err
		}

		// Second positional arg is the new branch name
		var newBranchName string
		if fs.NArg() >= 2 {
			newBranchName = fs.Arg(1)
		} else {
			newBranchName = ui.PromptRequired("Enter name for your new branch (stacked on " + remote.Branch + ")")
		}

		cfg := mgr.GetConfig()

		worktreePath := *worktree
		if worktreePath == "" {
			repoDir := mgr.GetRepoDir()
			worktreeBaseDir := cfg.GetWorktreeBaseDir(repoDir)
			if worktreeBaseDir == "" {
				// Prompt user to set worktree base dir
				var err error
				worktreeBaseDir, err = promptWorktreeBaseDir(repoDir, cfg)
				if err != nil {
					return err
				}
			}
			worktreePath = filepath.Join(worktreeBaseDir, newBranchName)
		}

		// Create the user's branch based on the remote branch
		ui.Info(fmt.Sprintf("Creating branch '%s' based on remote '%s'", newBranchName, remote.Branch))
		ui.Info(fmt.Sprintf("Worktree path: %s", worktreePath))

		if err := g.CreateWorktree(newBranchName, worktreePath, "origin/"+remote.Branch); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}

		mirrorSubmodulesIfEnabled(g, cfg, mgr.GetRepoDir(), worktreePath, *initSubmodulesFlag, *noInitSubmodulesFlag)

		userBranch, err := mgr.AddBranchToStack(newBranchName, remote.Branch, worktreePath, remote.StackHash)
		if err != nil {
			return fmt.Errorf("failed to add branch to stack: %w", err)
		}

		// Prompt for stack name (new stack was just created)
		promptStackName(mgr, userBranch.Name)

		if remote.PRNumber > 0 {
			ui.Success(fmt.Sprintf("Created stack from PR #%d (%s)", remote.PRNumber, remote.Branch))
		} else {
			ui.Success(fmt.Sprintf("Created stack from remote branch '%s'", remote.Branch))
		}
		ui.Success(fmt.Sprintf("Created your branch '%s' at %s", newBranchName, worktreePath))
		if getCdAfterNew(cfg, mgr.GetRepoDir(), *cdFlag, *noCdFlag) {
			EmitCd(worktreePath)
		}
		return nil
	}

	parentBranch = *parent
	if parentBranch == "" {
		if chooseParent {
			mgr, err := stack.NewManager(cwd)
			if err != nil {
				return err
			}

			cfg := mgr.GetConfig()
			baseBranch := cfg.GetBaseBranch(mgr.GetRepoDir())

			choices := buildParentChoices(baseBranch, mgr.ListStacks())
			labels := make([]string, len(choices))
			for i, c := range choices {
				labels[i] = c.label
			}

			selectedIdx, err := ui.SelectOption(labels, "Select parent branch")
			if err != nil {
				return err
			}
			parentBranch = choices[selectedIdx].branch
		} else {
			parentBranch, err = g.CurrentBranch()
			if err != nil {
				return fmt.Errorf("failed to get current branch: %w", err)
			}
		}
	}

	var branchName string
	if fs.NArg() >= 1 {
		branchName = fs.Arg(0)
	} else {
		branchName = ui.PromptRequired("Enter new branch name")
	}

	if err := git.ValidateBranchName(branchName); err != nil {
		return err
	}

	// Create the manager first to get repo-specific config
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	cfg := mgr.GetConfig()

	repoDir := mgr.GetRepoDir()
	useWorktrees := cfg.GetUseWorktrees(repoDir)

	// If worktree path was explicitly specified, use worktrees regardless of config
	if *worktree != "" {
		useWorktrees = true
	}

	if useWorktrees {
		worktreePath := *worktree
		if worktreePath == "" {
			worktreeBaseDir := cfg.GetWorktreeBaseDir(repoDir)
			if worktreeBaseDir == "" {
				worktreeBaseDir, err = promptWorktreeBaseDir(repoDir, cfg)
				if err != nil {
					return err
				}
			}
			worktreePath = filepath.Join(worktreeBaseDir, branchName)
		}

		worktreePath = helpers.ExpandPath(worktreePath)

		ui.Info(fmt.Sprintf("Creating branch '%s' from '%s'", branchName, parentBranch))
		ui.Info(fmt.Sprintf("Worktree path: %s", worktreePath))

		targetStack, isNewStack, skip, err := resolveStackIntent(mgr, parentBranch, branchName, worktreePath)
		if err != nil {
			return err
		}
		if skip {
			if err := mgr.CreateWorktreeOnly(branchName, parentBranch, worktreePath); err != nil {
				return err
			}
			mirrorSubmodulesIfEnabled(g, cfg, repoDir, worktreePath, *initSubmodulesFlag, *noInitSubmodulesFlag)
			ui.Success(fmt.Sprintf("Created worktree '%s' at '%s' (not part of a stack)", branchName, worktreePath))
			if shouldCd := getCdAfterNew(cfg, repoDir, *cdFlag, *noCdFlag); shouldCd {
				EmitCd(worktreePath)
			} else {
				ui.Info(fmt.Sprintf("To start working: cd %s", worktreePath))
			}
			return nil
		}

		branch, err := mgr.CreateBranch(branchName, parentBranch, worktreePath, targetStack)
		if err != nil {
			return err
		}

		mirrorSubmodulesIfEnabled(g, cfg, repoDir, branch.WorktreePath, *initSubmodulesFlag, *noInitSubmodulesFlag)

		if *templateFlag != "" {
			if err := applyTemplate(*templateFlag, branch.WorktreePath); err != nil {
				ui.Warn(fmt.Sprintf("Template seeding failed: %v", err))
			} else {
				ui.Success(fmt.Sprintf("Seeded worktree from template '%s'", *templateFlag))
			}
		}

		ui.Success(fmt.Sprintf("Created branch '%s' with worktree at '%s'", branch.Name, branch.WorktreePath))

		if isNewStack {
			promptStackName(mgr, branch.Name)
		}

		if getCdAfterNew(cfg, repoDir, *cdFlag, *noCdFlag) {
			EmitCd(branch.WorktreePath)
		} else {
			ui.Info(fmt.Sprintf("To start working: cd %s", branch.WorktreePath))
		}
	} else {
		// No worktrees mode: create a git branch and track it
		ui.Info(fmt.Sprintf("Creating branch '%s' from '%s' (no worktree)", branchName, parentBranch))

		targetStack, isNewStack, _, err := resolveStackIntent(mgr, parentBranch, branchName, "")
		if err != nil {
			return err
		}

		branch, err := mgr.CreateBranchNoWorktree(branchName, parentBranch, targetStack)
		if err != nil {
			return err
		}

		ui.Success(fmt.Sprintf("Created branch '%s'", branch.Name))

		if isNewStack {
			promptStackName(mgr, branch.Name)
		}

		// Switch to the new branch
		if getCdAfterNew(cfg, repoDir, *cdFlag, *noCdFlag) {
			if err := g.CheckoutBranch(branchName); err != nil {
				ui.Warn(fmt.Sprintf("Failed to switch to branch: %v", err))
			} else {
				ui.Success(fmt.Sprintf("Switched to branch '%s'", branchName))
			}
		} else {
			ui.Info(fmt.Sprintf("To start working: git checkout %s", branchName))
		}
	}

	return nil
}

// parentChoice pairs the picker label with the underlying branch name so that
// users can disambiguate "create a new stack from main" vs "add a child to an
// existing stack on top of main". The label is descriptive but the branch name
// is what gets passed downstream.
type parentChoice struct {
	label  string
	branch string
}

// buildParentChoices returns labeled options for the interactive "select parent
// branch" flow. The base branch comes first, then any other stack roots, then
// each stack's member branches. Without these labels, picking a stack tip from
// a flat list silently appends the new branch to that stack instead of starting
// a fresh one — see issue context for v4.5.1.
func buildParentChoices(baseBranch string, stacks []*config.Stack) []parentChoice {
	var choices []parentChoice
	seen := map[string]bool{}

	add := func(branch, label string) {
		if branch == "" || seen[branch] {
			return
		}
		seen[branch] = true
		choices = append(choices, parentChoice{label: label, branch: branch})
	}

	if baseBranch != "" {
		add(baseBranch, fmt.Sprintf("%s  (base branch — creates a NEW stack)", baseBranch))
	}

	// Other stack roots (skip baseBranch which is already added).
	for _, s := range stacks {
		add(s.Root, fmt.Sprintf("%s  (stack root — creates a NEW stack)", s.Root))
	}

	// Stack member branches — picking these adds a child to that stack.
	for _, s := range stacks {
		for _, b := range s.Branches {
			add(b.Name, fmt.Sprintf("%s  (in stack '%s' — adds child to this stack)", b.Name, s.DisplayName()))
		}
	}

	return choices
}

// resolveStackIntent determines whether a new branch should be added to an existing stack,
// create a new stack, or skip stack tracking entirely.
// Returns (targetStackHash, isNewStack, skipStack).
// targetStackHash is "" for auto-detect, "new" for a new stack, or a specific hash.
func resolveStackIntent(mgr *stack.Manager, parentBranch, branchName, worktreePath string) (string, bool, bool, error) {
	// Parent is already a tracked branch — always add to its stack
	if mgr.GetBranch(parentBranch) != nil {
		return "", false, false, nil
	}

	// Parent is not in a stack — ask if this should be a stack root
	if worktreePath != "" {
		if !ui.ConfirmTUIWithDefault("Make this a stack root? (allows stacking more branches on top)", true) {
			return "", false, true, nil
		}
	}
	return "new", true, false, nil
}

// getCdAfterNew determines if we should cd after creating a new worktree
func getCdAfterNew(cfg *config.Config, repoDir string, cdFlag, noCdFlag bool) bool {
	if noCdFlag {
		return false
	}
	if cdFlag {
		return true
	}
	if cfg != nil {
		return cfg.GetCdAfterNew(repoDir)
	}
	return false
}

// shouldInitSubmodules resolves whether to mirror the main worktree's
// initialized submodules into a newly-created worktree. --no-init-submodules
// wins over --init-submodules; otherwise the config decides, with a default
// of true.
func shouldInitSubmodules(cfg *config.Config, repoDir string, flag, noFlag bool) bool {
	if noFlag {
		return false
	}
	if flag {
		return true
	}
	if cfg != nil {
		return cfg.GetInitSubmodules(repoDir)
	}
	return true
}

// mirrorSubmodulesIfEnabled mirrors the main worktree's initialized submodules
// into destPath when enabled by flag or config. Failures are logged but not
// returned — a worktree is usable without submodules, and failing `ezs new`
// over this would be a regression in behavior.
func mirrorSubmodulesIfEnabled(g *git.Git, cfg *config.Config, repoDir, destPath string, flag, noFlag bool) {
	if !shouldInitSubmodules(cfg, repoDir, flag, noFlag) {
		return
	}
	source, err := g.GetMainWorktree()
	if err != nil || source == "" {
		ui.Warn(fmt.Sprintf("Could not determine main worktree for submodule init: %v", err))
		return
	}
	if err := git.MirrorSubmodules(source, destPath); err != nil {
		ui.Warn(fmt.Sprintf("Submodule init skipped: %v", err))
	}
}

// ValidateWorktreeBaseDir validates that the worktree base directory is not inside the repo.
// Returns an error if the path is invalid, nil if valid.
func ValidateWorktreeBaseDir(worktreeBaseDir, repoDir string) error {
	if repoDir == "" {
		return nil
	}

	repoDir = filepath.Clean(repoDir)
	worktreeBaseDir = filepath.Clean(worktreeBaseDir)

	// Check if they're the same
	if worktreeBaseDir == repoDir {
		return fmt.Errorf("worktree base directory cannot be the repository itself")
	}

	// Check if worktreeBaseDir is inside repoDir
	rel, err := filepath.Rel(repoDir, worktreeBaseDir)
	if err == nil && !filepath.IsAbs(rel) && len(rel) > 0 && rel[0] != '.' {
		return fmt.Errorf("worktree base directory cannot be inside the repository")
	}

	return nil
}

// promptWorktreeBaseDir prompts the user to set the worktree base directory
// and saves it to the config. It validates that the path is not inside the repo.
func promptWorktreeBaseDir(repoDir string, cfg *config.Config) (string, error) {
	ui.Info("No worktree base directory configured for this repository.")
	ui.Info("Worktrees should be stored OUTSIDE the repository (e.g., as sibling directories).")
	fmt.Fprintln(os.Stderr)

	// Suggest a default: <parent>/<repo>_worktrees
	defaultDir := ""
	if repoDir != "" {
		defaultDir = filepath.Join(filepath.Dir(repoDir), filepath.Base(repoDir)+"_worktrees")
	}

	for {
		worktreeBaseDir := ui.PromptPath("Worktree base directory", defaultDir)
		if worktreeBaseDir == "" {
			return "", fmt.Errorf("worktree base directory is required")
		}

		// Expand ~ in path
		if len(worktreeBaseDir) > 0 && worktreeBaseDir[0] == '~' {
			home, _ := os.UserHomeDir()
			worktreeBaseDir = filepath.Join(home, worktreeBaseDir[1:])
		}

		// Convert relative path to absolute path
		if !filepath.IsAbs(worktreeBaseDir) {
			cwd, err := os.Getwd()
			if err != nil {
				return "", fmt.Errorf("failed to get current directory: %w", err)
			}
			worktreeBaseDir = filepath.Join(cwd, worktreeBaseDir)
		}
		// Clean the path
		worktreeBaseDir = filepath.Clean(worktreeBaseDir)

		// Validate: worktree base dir must NOT be inside the repo
		if err := ValidateWorktreeBaseDir(worktreeBaseDir, repoDir); err != nil {
			ui.Error(err.Error())
			ui.Info(fmt.Sprintf("Repository: %s", repoDir))
			ui.Info("Please choose a directory outside the repository.")
			fmt.Fprintln(os.Stderr)
			continue
		}

		// Save to config
		repoCfg := cfg.GetRepoConfig(repoDir)
		if repoCfg == nil {
			repoCfg = &config.RepoConfig{}
		}
		repoCfg.WorktreeBaseDir = worktreeBaseDir
		cfg.SetRepoConfig(repoDir, repoCfg)

		if err := cfg.Save(); err != nil {
			return "", fmt.Errorf("failed to save config: %w", err)
		}

		ui.Success(fmt.Sprintf("Saved worktree_base_dir = %s", worktreeBaseDir))
		fmt.Fprintln(os.Stderr)

		return worktreeBaseDir, nil
	}
}

// newFromRemoteRef handles `ezs new origin/<branch>` — creates a local worktree
// that tracks the remote branch directly and registers it in a stack.
func newFromRemoteRef(g *git.Git, cwd, ref, worktreeOverride string, cdFlag, noCdFlag, initSubmodulesFlag, noInitSubmodulesFlag bool) error {
	remoteBranch := strings.TrimPrefix(ref, "origin/")
	if remoteBranch == "" {
		return fmt.Errorf("branch name cannot be empty (got %q)", ref)
	}

	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}
	cfg := mgr.GetConfig()
	repoDir := mgr.GetRepoDir()

	// Fetch latest remote refs
	if err := g.Fetch(); err != nil {
		return fmt.Errorf("failed to fetch: %w", err)
	}

	// Verify the remote branch exists
	if !g.RemoteBranchExists(remoteBranch) {
		return fmt.Errorf("remote branch '%s' not found on origin", remoteBranch)
	}

	// Check if the branch already has a worktree — if so, just navigate there
	if g.BranchExists(remoteBranch) {
		worktrees, _ := g.ListWorktrees()
		for _, wt := range worktrees {
			if wt.Branch == remoteBranch {
				ui.Info(fmt.Sprintf("Branch '%s' already has a worktree at %s", remoteBranch, wt.Path))
				if getCdAfterNew(cfg, repoDir, cdFlag, noCdFlag) {
					EmitCd(wt.Path)
				}
				return nil
			}
		}
	}

	// Determine worktree path
	worktreePath := worktreeOverride
	if worktreePath == "" {
		worktreeBaseDir := cfg.GetWorktreeBaseDir(repoDir)
		if worktreeBaseDir == "" {
			var err error
			worktreeBaseDir, err = promptWorktreeBaseDir(repoDir, cfg)
			if err != nil {
				return err
			}
		}
		// Replace slashes in branch name for the directory name (e.g., "user/feature" → "user-feature")
		dirName := strings.ReplaceAll(remoteBranch, "/", "-")
		worktreePath = filepath.Join(worktreeBaseDir, dirName)
	}

	ui.Info(fmt.Sprintf("Creating worktree for remote branch '%s'...", remoteBranch))

	if err := g.CreateWorktreeFromRemoteBranch(remoteBranch, worktreePath); err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}

	mirrorSubmodulesIfEnabled(g, cfg, repoDir, worktreePath, initSubmodulesFlag, noInitSubmodulesFlag)

	ui.Success(fmt.Sprintf("Created worktree for '%s' at %s", remoteBranch, worktreePath))

	// Look up PR info for registration and display
	var pr *github.PR
	gh, ghErr := newGitHubClient(g)
	if ghErr == nil {
		pr, _ = gh.GetPRByBranch(remoteBranch)
	}

	// Register the remote branch in a stack so it shows up in ezs ls
	baseBranch := ""
	var prURL string
	if pr != nil {
		baseBranch = pr.Base
		prURL = pr.URL
	}
	if baseBranch == "" {
		for _, candidate := range []string{"main", "master"} {
			if g.BranchExists(candidate) || g.RemoteBranchExists(candidate) {
				baseBranch = candidate
				break
			}
		}
	}
	if _, regErr := mgr.RegisterExistingBranch(remoteBranch, worktreePath, baseBranch); regErr == nil {
		// Only mark the branch as remote-contributor when we actually detected a fork
		// (or a fork with push forbidden). detectForkRemote returns "" for same-repo PRs
		// and for "no PR yet" — in those cases the branch is your own and should be
		// pushed to origin like any other. Previously MarkBranchRemote was called
		// unconditionally, which poisoned same-repo branches into the fork-detection
		// path on every push.
		if forkRemote := detectForkRemote(g, gh, pr); forkRemote != "" {
			mgr.MarkBranchRemote(remoteBranch, prURL, forkRemote)
		} else if pr != nil {
			savePRToCache(mgr.GetRepoDir(), remoteBranch, pr.Number, prURL)
		}
	}

	// Show PR info and diff stats
	showRemoteBranchInfoWithPR(g, remoteBranch, pr)

	if getCdAfterNew(cfg, repoDir, cdFlag, noCdFlag) {
		EmitCd(worktreePath)
	} else {
		ui.Info(fmt.Sprintf("To start working: cd %s", worktreePath))
	}
	return nil
}

// showRemoteBranchInfoWithPR displays PR info and diff stats for a remote branch.
// If pr is nil, only diff stats against an inferred base are shown.
func showRemoteBranchInfoWithPR(g *git.Git, remoteBranch string, pr *github.PR) {
	if pr == nil {
		showDiffStatsAgainstBase(g, remoteBranch, "")
		return
	}

	fmt.Fprintln(os.Stderr)
	ui.Info(fmt.Sprintf("PR #%d: %s", pr.Number, pr.Title))
	ui.Info(fmt.Sprintf("URL: %s", pr.URL))

	state := pr.State
	if pr.IsDraft {
		state = "DRAFT"
	}
	if pr.Merged {
		state = "MERGED"
	}
	ui.Info(fmt.Sprintf("State: %s  Base: %s", state, pr.Base))

	if pr.ReviewState != "" {
		ui.Info(fmt.Sprintf("Review: %s", pr.ReviewState))
	}

	showDiffStatsAgainstBase(g, remoteBranch, pr.Base)
}

// showDiffStatsAgainstBase displays line additions/deletions for a branch relative to a base.
// If baseBranch is empty, it tries to infer one from common defaults (main, master).
func showDiffStatsAgainstBase(g *git.Git, branch, baseBranch string) {
	if baseBranch == "" {
		for _, candidate := range []string{"main", "master"} {
			if g.RemoteBranchExists(candidate) {
				baseBranch = candidate
				break
			}
		}
	}
	if baseBranch == "" {
		return
	}

	// Diff against the LOCAL base and LOCAL branch so stats reflect the
	// user's working state rather than possibly-stale origin refs.
	baseRef := resolveLocalRef(g, baseBranch)
	branchRef := resolveLocalRef(g, branch)

	added, removed, err := g.GetDiffStat(baseRef, branchRef)
	if err != nil {
		return
	}

	ui.Info(fmt.Sprintf("Diff vs %s: %s+%d%s / %s-%d%s lines",
		baseBranch, ui.Green, added, ui.Reset, ui.Red, removed, ui.Reset))
}

// applyTemplate copies the contents of ~/.ezstack/templates/<name>/ into
// dest. It is a layered overlay: existing files in dest are overwritten, new
// directories are created, and file modes (including the executable bit) are
// preserved.
//
// Safety rules enforced here (all failures abort the copy with an error —
// partial overlays are never left behind):
//
//   - The template root must be a real directory, not a symlink.
//   - Every source path must be inside the template root after EvalSymlinks,
//     so a symlink inside the template can't escape to read arbitrary files.
//   - The `.git` directory of the template (if any) is skipped — it's a
//     template authoring artifact, not content.
//   - Every destination path must stay inside dest after joining — guards
//     against "../" payloads in template file names.
//   - Symlinks inside the template are recreated as symlinks in dest
//     verbatim, BUT only if their target (resolved relative to the
//     symlink's new location in dest) stays inside dest. A symlink that
//     would resolve outside the worktree — absolute or relative — is
//     rejected, so a malicious template can't plant `secrets → /etc/passwd`
//     that later leaks via a grep, cat, or commit+push.
func applyTemplate(name, dest string) error {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return err
	}
	src := filepath.Join(cfgDir, "templates", name)
	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("template '%s' not found at %s", name, src)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("template '%s' is a symlink; refusing to follow", name)
	}
	if !info.IsDir() {
		return fmt.Errorf("template '%s' is not a directory", src)
	}
	// Resolve once so per-file containment checks can compare against a
	// canonical base path.
	srcReal, err := filepath.EvalSymlinks(src)
	if err != nil {
		return fmt.Errorf("failed to resolve template path: %w", err)
	}
	destReal, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("failed to resolve dest path: %w", err)
	}

	return filepath.Walk(srcReal, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(srcReal, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		// Skip any .git metadata the template might carry.
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(os.PathSeparator)) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Belt & suspenders: reject any rel that escapes.
		if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return fmt.Errorf("template entry %q escapes template root", rel)
		}
		target := filepath.Join(destReal, rel)
		targetAbs, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(targetAbs, destReal+string(os.PathSeparator)) && targetAbs != destReal {
			return fmt.Errorf("template entry %q resolves outside dest", rel)
		}

		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			linkTarget, rerr := os.Readlink(path)
			if rerr != nil {
				return rerr
			}
			// Resolve the symlink's effective target as it would appear
			// once the link lives at `target` inside dest. Relative links
			// are anchored at filepath.Dir(target); absolute links are
			// used as-is. Either way, the cleaned path must remain inside
			// destReal — otherwise a malicious template could plant a
			// pointer to an arbitrary file on the host.
			resolved := linkTarget
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(filepath.Dir(target), resolved)
			}
			resolved = filepath.Clean(resolved)
			if !strings.HasPrefix(resolved, destReal+string(os.PathSeparator)) && resolved != destReal {
				return fmt.Errorf("template symlink %q points outside worktree (%s)", rel, linkTarget)
			}
			_ = os.Remove(target)
			return os.Symlink(linkTarget, target)
		case fi.IsDir():
			return os.MkdirAll(target, fi.Mode().Perm())
		default:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			return copyRegularFile(path, target, fi.Mode().Perm())
		}
	})
}

// copyRegularFile copies src to dest, creating dest with the given mode. It
// never follows a dest symlink: an existing symlink at dest is removed first
// so writes can't leak through a pre-placed link.
func copyRegularFile(src, dest string, mode os.FileMode) error {
	if fi, err := os.Lstat(dest); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if rmErr := os.Remove(dest); rmErr != nil {
			return rmErr
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, copyErr := io.Copy(out, in); copyErr != nil {
		return copyErr
	}
	return out.Chmod(mode)
}
