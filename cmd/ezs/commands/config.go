package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/helpers"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

func printConfigUsage() {
	fmt.Fprintf(os.Stderr, `%sConfigure ezstack%s (must be run inside a git repo)

%sUSAGE%s
    ezs config [subcommand] [options]

%sSUBCOMMANDS%s
    set <key> <value>    Set a configuration value
    show                 Show current configuration

%sKEYS FOR 'set'%s
    worktree_base_dir     Base directory for worktrees (per-repo)
    default_base_branch   Default base branch (e.g., main)
    github_token          GitHub token for API access
    cd_after_new          Auto-cd to new worktree (true/false, per-repo)
    use_worktrees         Use git worktrees for new branches (true/false, per-repo)
    sync_strategy         Sync method: "rebase" or "merge" (per-repo)
    agent_command         AI agent CLI command (default: "claude", per-repo)

%sOPTIONS%s
    -h, --help    Show this help message

%sNOTES%s
    If no subcommand is provided, runs interactive configuration.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
}

// Config handles configuration commands
func Config(args []string) error {
	_, err := getCurrentRepoPath()
	if err != nil {
		return fmt.Errorf("ezs config must be run inside a git repository")
	}

	if len(args) < 1 {
		return configInteractive()
	}

	switch args[0] {
	case "-h", "--help":
		printConfigUsage()
		return nil
	case "set":
		if len(args) < 3 {
			return fmt.Errorf("usage: ezs config set <key> <value>")
		}
		return configSet(args[1], strings.Join(args[2:], " "))
	case "show":
		return configShow()
	default:
		return fmt.Errorf("unknown config command: %s", args[0])
	}
}

// getCurrentRepoPath returns the main repo path for the current directory
func getCurrentRepoPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	g := git.New(cwd)
	mainWorktree, err := g.GetMainWorktree()
	if err != nil {
		repoRoot, err := g.GetRepoRoot()
		if err != nil {
			return "", fmt.Errorf("not in a git repository")
		}
		resolved, err := filepath.EvalSymlinks(repoRoot)
		if err == nil {
			repoRoot = resolved
		}
		return repoRoot, nil
	}
	return mainWorktree, nil
}

func configSet(key, value string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	switch key {
	case "worktree_base_dir":
		value = helpers.ExpandPath(value)
		repoPath, err := getCurrentRepoPath()
		if err != nil {
			return fmt.Errorf("worktree_base_dir is a per-repo setting: %w", err)
		}

		if !filepath.IsAbs(value) {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}
			value = filepath.Join(cwd, value)
		}
		value = filepath.Clean(value)

		// Validate: worktree base dir must NOT be inside the repo
		if err := ValidateWorktreeBaseDir(value, repoPath); err != nil {
			return err
		}

		repoCfg := cfg.GetRepoConfig(repoPath)
		if repoCfg == nil {
			repoCfg = &config.RepoConfig{}
		}
		repoCfg.WorktreeBaseDir = value
		cfg.SetRepoConfig(repoPath, repoCfg)
		ui.Info(fmt.Sprintf("Setting worktree_base_dir for repo: %s", repoPath))
	case "default_base_branch":
		cfg.DefaultBaseBranch = value
	case "github_token":
		cfg.GitHubToken = value
	case "cd_after_new":
		repoPath, err := getCurrentRepoPath()
		if err != nil {
			return fmt.Errorf("cd_after_new is a per-repo setting: %w", err)
		}
		repoCfg := cfg.GetRepoConfig(repoPath)
		if repoCfg == nil {
			repoCfg = &config.RepoConfig{}
		}
		boolVal := value == "true" || value == "1" || value == "yes"
		repoCfg.CdAfterNew = &boolVal
		cfg.SetRepoConfig(repoPath, repoCfg)
		ui.Info(fmt.Sprintf("Setting cd_after_new for repo: %s", repoPath))
	case "use_worktrees":
		repoPath, err := getCurrentRepoPath()
		if err != nil {
			return fmt.Errorf("use_worktrees is a per-repo setting: %w", err)
		}
		repoCfg := cfg.GetRepoConfig(repoPath)
		if repoCfg == nil {
			repoCfg = &config.RepoConfig{}
		}
		boolVal := value == "true" || value == "1" || value == "yes"
		repoCfg.UseWorktrees = &boolVal
		cfg.SetRepoConfig(repoPath, repoCfg)
		ui.Info(fmt.Sprintf("Setting use_worktrees for repo: %s", repoPath))
	case "sync_strategy":
		repoPath, err := getCurrentRepoPath()
		if err != nil {
			return fmt.Errorf("sync_strategy is a per-repo setting: %w", err)
		}
		if value != "rebase" && value != "merge" {
			return fmt.Errorf("sync_strategy must be 'rebase' or 'merge'")
		}
		repoCfg := cfg.GetRepoConfig(repoPath)
		if repoCfg == nil {
			repoCfg = &config.RepoConfig{}
		}
		repoCfg.SyncStrategy = value
		cfg.SetRepoConfig(repoPath, repoCfg)
		ui.Info(fmt.Sprintf("Setting sync_strategy for repo: %s", repoPath))
	case "agent_command":
		repoPath, err := getCurrentRepoPath()
		if err != nil {
			return fmt.Errorf("agent_command is a per-repo setting: %w", err)
		}
		if value == "" {
			return fmt.Errorf("agent_command must not be empty")
		}
		repoCfg := cfg.GetRepoConfig(repoPath)
		if repoCfg == nil {
			repoCfg = &config.RepoConfig{}
		}
		repoCfg.AgentCommand = value
		cfg.SetRepoConfig(repoPath, repoCfg)
		ui.Info(fmt.Sprintf("Setting agent_command for repo: %s", repoPath))
	default:
		return fmt.Errorf("unknown config key: %s\nValid keys: worktree_base_dir, default_base_branch, github_token, cd_after_new, use_worktrees, sync_strategy, agent_command", key)
	}

	if err := cfg.Save(); err != nil {
		return err
	}

	displayValue := value
	if key == "github_token" {
		displayValue = "****** (set)"
	}
	ui.Success(fmt.Sprintf("Set %s = %s", key, displayValue))
	return nil
}

func configShow() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	configDir, _ := config.ConfigDir()
	ezstackHome := os.Getenv("EZSTACK_HOME")

	fmt.Fprintf(os.Stderr, "%sezstack configuration%s\n", ui.Bold, ui.Reset)
	fmt.Fprintf(os.Stderr, "Config directory: %s\n", configDir)
	if ezstackHome != "" {
		fmt.Fprintf(os.Stderr, "  (set via EZSTACK_HOME environment variable)\n")
	} else {
		fmt.Fprintf(os.Stderr, "  (default: $HOME/.ezstack, override with EZSTACK_HOME env var)\n")
	}
	fmt.Fprintf(os.Stderr, "Config file: %s/config.json\n\n", configDir)

	fmt.Fprintf(os.Stderr, "%sGlobal Settings:%s\n", ui.Bold, ui.Reset)
	fmt.Fprintf(os.Stderr, "  default_base_branch: %s\n", valueOrDefault(cfg.DefaultBaseBranch, "main"))
	if cfg.GitHubToken != "" {
		fmt.Fprintf(os.Stderr, "  github_token:        %s\n", "****** (set)")
	} else {
		fmt.Fprintf(os.Stderr, "  github_token:        %s\n", "(not set - using gh cli)")
	}

	repoPath, err := getCurrentRepoPath()
	if err == nil {
		fmt.Fprintf(os.Stderr, "\n%sCurrent Repository:%s\n", ui.Bold, ui.Reset)
		fmt.Fprintf(os.Stderr, "  repo_path: %s\n", repoPath)
		repoCfg := cfg.GetRepoConfig(repoPath)
		if repoCfg != nil {
			fmt.Fprintf(os.Stderr, "  worktree_base_dir: %s\n", valueOrDefault(repoCfg.WorktreeBaseDir, "(not set)"))
			if repoCfg.DefaultBaseBranch != "" {
				fmt.Fprintf(os.Stderr, "  default_base_branch: %s (repo override)\n", repoCfg.DefaultBaseBranch)
			}
			if repoCfg.CdAfterNew != nil {
				fmt.Fprintf(os.Stderr, "  cd_after_new: %v\n", *repoCfg.CdAfterNew)
			} else {
				fmt.Fprintf(os.Stderr, "  cd_after_new: true (default)\n")
			}
			if repoCfg.UseWorktrees != nil {
				fmt.Fprintf(os.Stderr, "  use_worktrees: %v\n", *repoCfg.UseWorktrees)
			} else {
				fmt.Fprintf(os.Stderr, "  use_worktrees: true (default)\n")
			}
			if repoCfg.SyncStrategy != "" {
				fmt.Fprintf(os.Stderr, "  sync_strategy: %s\n", repoCfg.SyncStrategy)
			} else {
				fmt.Fprintf(os.Stderr, "  sync_strategy: rebase (default)\n")
			}
			if repoCfg.AgentCommand != "" {
				fmt.Fprintf(os.Stderr, "  agent_command: %s\n", repoCfg.AgentCommand)
			} else {
				fmt.Fprintf(os.Stderr, "  agent_command: claude (default)\n")
			}
		} else {
			fmt.Fprintf(os.Stderr, "  worktree_base_dir: %s(not configured for this repo)%s\n", ui.Yellow, ui.Reset)
			fmt.Fprintf(os.Stderr, "  Run: ezs config set worktree_base_dir <path>\n")
		}
	}

	if len(cfg.Repos) > 0 {
		fmt.Fprintf(os.Stderr, "\n%sConfigured Repositories:%s\n", ui.Bold, ui.Reset)
		for path, repoCfg := range cfg.Repos {
			marker := ""
			if path == repoPath {
				marker = " (current)"
			}
			fmt.Fprintf(os.Stderr, "  %s%s%s%s\n", ui.Cyan, path, marker, ui.Reset)
			fmt.Fprintf(os.Stderr, "    worktree_base_dir: %s\n", repoCfg.WorktreeBaseDir)
		}
	}

	return nil
}

func valueOrDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}

// configInteractive walks through config options interactively
func configInteractive() error {
	repoPath, err := getCurrentRepoPath()
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\n%sConfiguring ezstack for repository:%s\n", ui.Bold, ui.Reset)
	fmt.Fprintf(os.Stderr, "  %s%s%s\n\n", ui.Cyan, repoPath, ui.Reset)

	repoCfg := cfg.GetRepoConfig(repoPath)
	currentWorktreeBaseDir := ""
	currentCdAfterNew := true
	currentUseWorktrees := true
	if repoCfg != nil {
		currentWorktreeBaseDir = repoCfg.WorktreeBaseDir
		if repoCfg.CdAfterNew != nil {
			currentCdAfterNew = *repoCfg.CdAfterNew
		}
		if repoCfg.UseWorktrees != nil {
			currentUseWorktrees = *repoCfg.UseWorktrees
		}
	}

	if repoCfg == nil {
		repoCfg = &config.RepoConfig{}
	}

	useWorktrees := ui.ConfirmTUIWithDefault("Use git worktrees for new branches (recommended)", currentUseWorktrees)
	repoCfg.UseWorktrees = &useWorktrees
	ui.Success(fmt.Sprintf("Set use_worktrees = %v", useWorktrees))

	if useWorktrees {
		// Generate default worktree dir: ../<repo_name>_worktrees
		if currentWorktreeBaseDir == "" {
			repoName := filepath.Base(repoPath)
			currentWorktreeBaseDir = filepath.Join(filepath.Dir(repoPath), repoName+"_worktrees")
		}

		// Loop until valid path is provided
		for {
			worktreeBaseDir := ui.PromptPath("Worktree base directory (where new worktrees will be created)", currentWorktreeBaseDir)

			if worktreeBaseDir != "" {
				worktreeBaseDir = helpers.ExpandPath(worktreeBaseDir)
				if !filepath.IsAbs(worktreeBaseDir) {
					cwd, err := os.Getwd()
					if err != nil {
						return fmt.Errorf("failed to get current directory: %w", err)
					}
					worktreeBaseDir = filepath.Join(cwd, worktreeBaseDir)
				}
				worktreeBaseDir = filepath.Clean(worktreeBaseDir)

				// Check if path is inside repo root
				if err := ValidateWorktreeBaseDir(worktreeBaseDir, repoPath); err != nil {
					ui.Error(err.Error())
					continue
				}

				repoCfg.WorktreeBaseDir = worktreeBaseDir
				ui.Success(fmt.Sprintf("Set worktree_base_dir = %s", worktreeBaseDir))
			}
			break
		}
	}

	cdAfterNew := ui.ConfirmTUIWithDefault("Auto-cd into new worktrees after creation", currentCdAfterNew)
	repoCfg.CdAfterNew = &cdAfterNew
	ui.Success(fmt.Sprintf("Set cd_after_new = %v", cdAfterNew))

	// Sync strategy: rebase or merge
	options := []string{"merge", "rebase"}
	defaultIdx := 0
	syncStrategyIdx := ui.SelectTUI(options, "Select your sync strategy (merge is recommended since rebase will force push)", defaultIdx)
	if syncStrategyIdx >= 0 {
		if syncStrategyIdx == 0 {
			repoCfg.SyncStrategy = "merge"
		} else {
			repoCfg.SyncStrategy = "rebase"
		}
		ui.Success(fmt.Sprintf("Set sync_strategy = %s", repoCfg.SyncStrategy))
	}

	cfg.SetRepoConfig(repoPath, repoCfg)
	if err := cfg.Save(); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\n%sNote:%s For 'ezs goto' and 'ezs new --cd' to change directories, add this to your shell config (if not already done):\n", ui.Bold, ui.Reset)
	fmt.Fprintf(os.Stderr, "  eval \"$(ezs --shell-init)\"\n\n")

	return nil
}
