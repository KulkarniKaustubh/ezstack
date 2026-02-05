package commands

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ezstack/ezstack/internal/config"
	"github.com/ezstack/ezstack/internal/git"
	"github.com/ezstack/ezstack/internal/github"
	"github.com/ezstack/ezstack/internal/helpers"
	"github.com/ezstack/ezstack/internal/stack"
	"github.com/ezstack/ezstack/internal/ui"
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
			worktreeBaseDir := cfg.GetWorktreeBaseDir(mgr.GetRepoDir())
			if worktreeBaseDir != "" {
				worktreePath = filepath.Join(worktreeBaseDir, newBranchName)
			} else {
				repoDir := mgr.GetRepoDir()
				if repoDir != "" {
					worktreePath = filepath.Join(filepath.Dir(repoDir), newBranchName)
				} else {
					return fmt.Errorf("no worktree path specified and no default configured")
				}
			}
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
		if worktreeBaseDir != "" {
			worktreePath = filepath.Join(worktreeBaseDir, branchName)
		} else {
			if repoDir != "" {
				worktreePath = filepath.Join(filepath.Dir(repoDir), branchName)
			} else {
				return fmt.Errorf("no worktree path specified and no default configured for this repo. Run: ezs config set worktree_base_dir <path>")
			}
		}
	}

	worktreePath = helpers.ExpandPath(worktreePath)

	ui.Info(fmt.Sprintf("Creating branch '%s' from '%s'", branchName, parentBranch))
	ui.Info(fmt.Sprintf("Worktree path: %s", worktreePath))

	if !ui.ConfirmTUI(fmt.Sprintf("Create new worktree for branch '%s'", branchName)) {
		ui.Warn("Cancelled")
		return nil
	}

	branch, err := mgr.CreateBranch(branchName, parentBranch, worktreePath)
	if err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Created branch '%s' with worktree at '%s'", branch.Name, branch.WorktreePath))

	shouldCd := false
	if *noCdFlag {
		shouldCd = false
	} else if *cdFlag {
		shouldCd = true
	} else {
		cfg, err := config.Load()
		if err == nil {
			shouldCd = cfg.GetCdAfterNew(mgr.GetRepoDir())
		}
	}

	if shouldCd {
		fmt.Printf("cd %s\n", branch.WorktreePath)
		ui.Info("Note: If `ezs goto` doesn't work, add this to your ~/.bashrc or ~/.zshrc:")
		ui.Info("  eval \"$(ezs --shell-init)\"")
	} else {
		ui.Info(fmt.Sprintf("To start working: cd %s", branch.WorktreePath))
	}

	return nil
}
