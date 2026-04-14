package commands

import (
	"fmt"
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
	fs := pflag.NewFlagSet("new", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sCreate a new branch in the stack%s

%sUSAGE%s
    ezs new [branch-name] [options]

%sOPTIONS%s
    -p, --parent <branch>     Parent branch (defaults to current branch)
    -w, --worktree <path>     Worktree path (defaults to configured base dir + branch name)
    -c, --cd                  Change to the new worktree after creation
    -C, --no-cd               Don't change to the new worktree (overrides config)
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
	cdFlag := fs.BoolP("cd", "c", false, "Change to worktree")
	noCdFlag := fs.BoolP("no-cd", "C", false, "Don't change to worktree")
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
		return newFromRemoteRef(g, cwd, fs.Arg(0), *worktree, *cdFlag, *noCdFlag)
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

			branchOptions := []string{baseBranch}
			for _, s := range mgr.ListStacks() {
				for _, b := range s.Branches {
					branchOptions = append(branchOptions, b.Name)
				}
			}

			selectedIdx, err := ui.SelectOption(branchOptions, "Select parent branch")
			if err != nil {
				return err
			}
			parentBranch = branchOptions[selectedIdx]
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
func newFromRemoteRef(g *git.Git, cwd, ref, worktreeOverride string, cdFlag, noCdFlag bool) error {
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
		forkRemote := detectForkRemote(g, gh, pr)
		mgr.MarkBranchRemote(remoteBranch, prURL, forkRemote)
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
