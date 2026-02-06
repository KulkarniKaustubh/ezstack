package commands

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/github"
	"github.com/KulkarniKaustubh/ezstack/internal/helpers"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
)

// New creates a new branch in the stack
func New(args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
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
	parent := fs.String("parent", "", "Parent branch")
	worktree := fs.String("worktree", "", "Worktree path")
	cdFlag := fs.Bool("cd", false, "Change to worktree")
	noCdFlag := fs.Bool("no-cd", false, "Don't change to worktree")
	fromWorktree := fs.Bool("from-worktree", false, "Select from worktree")
	fromRemote := fs.Bool("from-remote", false, "Create stack from remote branch")
	parentShort := fs.String("p", "", "Parent branch (short)")
	worktreeShort := fs.String("w", "", "Worktree path (short)")
	cdFlagShort := fs.Bool("c", false, "Change to worktree (short)")
	noCdFlagShort := fs.Bool("C", false, "Don't change to worktree (short)")
	fromWorktreeShort := fs.Bool("f", false, "Select from worktree (short)")
	fromRemoteShort := fs.Bool("r", false, "Create stack from remote branch (short)")
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

	if *parentShort != "" {
		*parent = *parentShort
	}
	if *worktreeShort != "" {
		*worktree = *worktreeShort
	}
	helpers.MergeFlags(cdFlagShort, cdFlag, noCdFlagShort, noCdFlag, fromWorktreeShort, fromWorktree, fromRemoteShort, fromRemote)

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

		remoteURL, err := g.GetRemote("origin")
		if err == nil {
			gh, err := github.NewClient(remoteURL)
			if err == nil {
				pr, err := gh.GetPRByBranch(selected.Branch)
				if err == nil && pr != nil && pr.Number > 0 {
					branch.PRNumber = pr.Number
					branch.PRUrl = pr.URL

					stackCfg, err := config.LoadStackConfig(mgr.GetRepoDir())
					if err == nil {
						for _, s := range stackCfg.Stacks {
							for _, b := range s.Branches {
								if b.Name == branch.Name {
									b.PRNumber = pr.Number
									b.PRUrl = pr.URL
								}
							}
						}
						stackCfg.Save(mgr.GetRepoDir())
					}

					ui.Success(fmt.Sprintf("Registered '%s' as a stack root (found existing PR #%d)", branch.Name, pr.Number))
					ui.Info("You can now add child branches with: ezs new <branch-name>")
					return nil
				}
			}
		}

		ui.Success(fmt.Sprintf("Registered '%s' as a stack root", branch.Name))
		ui.Info("You can now add child branches with: ezs new <branch-name>")
		return nil
	}

	if useFromRemote {
		remoteURL, err := g.GetRemote("origin")
		if err != nil {
			return fmt.Errorf("failed to get remote: %w", err)
		}

		gh, err := github.NewClient(remoteURL)
		if err != nil {
			return fmt.Errorf("failed to create GitHub client: %w", err)
		}

		ui.Info("Fetching open PRs...")
		openPRs, err := gh.ListOpenPRs()
		if err != nil {
			return fmt.Errorf("failed to list open PRs: %w", err)
		}

		if len(openPRs) == 0 {
			return fmt.Errorf("no open PRs found in this repository")
		}

		prOptions := make([]string, len(openPRs))
		for i, pr := range openPRs {
			prOptions[i] = fmt.Sprintf("#%d %s - %s (%s)", pr.Number, pr.Branch, pr.Title, pr.Author)
		}

		selectedIdx, err := ui.SelectOption(prOptions, "Select PR to create stack from")
		if err != nil {
			return err
		}
		selectedPR := openPRs[selectedIdx]

		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "%s────────────────────────────────────────────────────────────────%s\n", ui.Yellow, ui.Reset)
		ui.Warn("Note: This remote branch will never be rebased since it is assumed")
		ui.Warn(fmt.Sprintf("that it does not belong to you. Only %sYOUR%s branches that are stacked", ui.Bold, ui.Reset+ui.Yellow))
		ui.Warn("on this branch will be handled by ezstack.")
		fmt.Fprintf(os.Stderr, "%s────────────────────────────────────────────────────────────────%s\n", ui.Yellow, ui.Reset)
		fmt.Fprintln(os.Stderr)

		newBranchName := ui.PromptRequired("Enter name for your new branch (stacked on " + selectedPR.Branch + ")")

		ui.Info("Fetching remote branch...")
		if err := g.Fetch(); err != nil {
			return fmt.Errorf("failed to fetch: %w", err)
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

		_, err = mgr.RegisterRemoteBranch(selectedPR.Branch, baseBranch, selectedPR.Number, selectedPR.URL)
		if err != nil {
			return fmt.Errorf("failed to register remote branch: %w", err)
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

		ui.Success(fmt.Sprintf("Created stack from PR #%d (%s)", selectedPR.Number, selectedPR.Branch))
		ui.Success(fmt.Sprintf("Created your branch '%s' at %s", userBranch.Name, worktreePath))
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

	worktreePath := *worktree
	if worktreePath == "" {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		repoDir := mgr.GetRepoDir()
		worktreeBaseDir := cfg.GetWorktreeBaseDir(repoDir)
		if worktreeBaseDir == "" {
			// Prompt user to set worktree base dir
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

	// Check if parent is the base branch (main/master) and ask about stack
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	baseBranch := cfg.GetBaseBranch(mgr.GetRepoDir())

	// Check if this would create a new stack (parent is base branch and not already in a stack)
	createAsStackRoot := true
	if parentBranch == baseBranch && mgr.GetBranch(parentBranch) == nil {
		// Parent is main/master and not in any stack - ask if user wants to create a stack
		createAsStackRoot = ui.ConfirmTUIWithDefault("Make this a stack root? (allows stacking more branches on top)", true)
	}

	if !ui.ConfirmTUI(fmt.Sprintf("Create new worktree for branch '%s'", branchName)) {
		ui.Warn("Cancelled")
		return nil
	}

	if !createAsStackRoot {
		// Just create the worktree without adding to a stack
		if err := mgr.CreateWorktreeOnly(branchName, parentBranch, worktreePath); err != nil {
			return err
		}
		ui.Success(fmt.Sprintf("Created worktree '%s' at '%s' (not part of a stack)", branchName, worktreePath))
		if shouldCd := getCdAfterNew(cfg, mgr.GetRepoDir(), *cdFlag, *noCdFlag); shouldCd {
			fmt.Printf("cd %s\n", worktreePath)
			ui.Info("Note: If `ezs goto` doesn't work, add this to your ~/.bashrc or ~/.zshrc:")
			ui.Info("  eval \"$(ezs --shell-init)\"")
		} else {
			ui.Info(fmt.Sprintf("To start working: cd %s", worktreePath))
		}
		return nil
	}

	branch, err := mgr.CreateBranch(branchName, parentBranch, worktreePath)
	if err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Created branch '%s' with worktree at '%s'", branch.Name, branch.WorktreePath))

	if getCdAfterNew(cfg, mgr.GetRepoDir(), *cdFlag, *noCdFlag) {
		fmt.Printf("cd %s\n", branch.WorktreePath)
		ui.Info("Note: If `ezs goto` doesn't work, add this to your ~/.bashrc or ~/.zshrc:")
		ui.Info("  eval \"$(ezs --shell-init)\"")
	} else {
		ui.Info(fmt.Sprintf("To start working: cd %s", branch.WorktreePath))
	}

	return nil
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
		worktreeBaseDir := ui.Prompt("Worktree base directory", defaultDir)
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
