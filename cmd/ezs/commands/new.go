package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/helpers"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
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
    -r, --from-remote         Create a stack from a remote branch
    -h, --help                Show this help message

%sNOTES%s
    If no arguments are provided, interactive mode will prompt for options.

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

		cfg, err := config.Load()
		if err != nil {
			return err
		}
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

		selectedPR, err := selectAndRegisterRemotePR(g, mgr)
		if err != nil {
			return err
		}

		newBranchName := ui.PromptRequired("Enter name for your new branch (stacked on " + selectedPR.Branch + ")")

		cfg, err := config.Load()
		if err != nil {
			return err
		}

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
		ui.Info(fmt.Sprintf("Creating branch '%s' based on remote '%s'", newBranchName, selectedPR.Branch))
		ui.Info(fmt.Sprintf("Worktree path: %s", worktreePath))

		if err := g.CreateWorktree(newBranchName, worktreePath, "origin/"+selectedPR.Branch); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}

		userBranch, err := mgr.AddBranchToStack(newBranchName, selectedPR.Branch, worktreePath)
		if err != nil {
			return fmt.Errorf("failed to add branch to stack: %w", err)
		}

		// Prompt for stack name (new stack was just created)
		promptStackName(mgr, userBranch.Name)

		ui.Success(fmt.Sprintf("Created stack from PR #%d (%s)", selectedPR.Number, selectedPR.Branch))
		ui.Success(fmt.Sprintf("Created your branch '%s' at %s", userBranch.Name, worktreePath))
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

			cfg, err := config.Load()
			if err != nil {
				return err
			}
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

	// Create the manager first to get repo-specific config
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

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

		targetStack, isNewStack, skip := resolveStackIntent(mgr, parentBranch, branchName, worktreePath)
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

		targetStack, isNewStack, _ := resolveStackIntent(mgr, parentBranch, branchName, "")

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
func resolveStackIntent(mgr *stack.Manager, parentBranch, branchName, worktreePath string) (string, bool, bool) {
	// Parent is already a tracked branch — always add to its stack
	if mgr.GetBranch(parentBranch) != nil {
		return "", false, false
	}

	existingStacks := mgr.GetStacksWithRoot(parentBranch)

	// No existing stacks with this root — new stack
	if len(existingStacks) == 0 {
		if worktreePath != "" {
			if !ui.ConfirmTUIWithDefault("Make this a stack root? (allows stacking more branches on top)", true) {
				return "", false, true
			}
		}
		return "", true, false
	}

	// Existing stacks share this root — ask the user
	options := []string{
		fmt.Sprintf("Start a new stack (off %s)", parentBranch),
	}
	for _, s := range existingStacks {
		label := s.DisplayName()
		if len(s.Branches) > 0 {
			label += fmt.Sprintf(" (%d branch(es))", len(s.Branches))
		}
		options = append(options, fmt.Sprintf("Add to existing stack: %s", label))
	}
	if worktreePath != "" {
		options = append(options, "Don't add to any stack (standalone worktree)")
	}

	selected, err := ui.SelectOption(options, fmt.Sprintf("Stacks already exist off '%s'. What would you like to do?", parentBranch))
	if err != nil {
		return "new", true, false
	}

	if selected == 0 {
		return "new", true, false
	}
	if worktreePath != "" && selected == len(options)-1 {
		return "", false, true
	}

	// User chose an existing stack — pass its hash directly
	chosenStack := existingStacks[selected-1]
	return chosenStack.Hash, false, false
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

	// Suggest a default: parent directory of the repo
	defaultDir := ""
	if repoDir != "" {
		defaultDir = filepath.Dir(repoDir)
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
