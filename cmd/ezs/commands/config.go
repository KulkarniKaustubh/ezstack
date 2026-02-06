package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/helpers"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
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
		return configSet(args[1], args[2])
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

	value = helpers.ExpandPath(value)

	switch key {
	case "worktree_base_dir":
		repoPath, err := getCurrentRepoPath()
		if err != nil {
			return fmt.Errorf("worktree_base_dir is a per-repo setting: %w", err)
		}

		// Expand ~ in path
		value = helpers.ExpandPath(value)

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
	default:
		return fmt.Errorf("unknown config key: %s\nValid keys: worktree_base_dir, default_base_branch, github_token, cd_after_new", key)
	}

	if err := cfg.Save(); err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Set %s = %s", key, value))
	return nil
}

func configShow() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	configDir, _ := config.ConfigDir()
	ezstackHome := os.Getenv("EZSTACK_HOME")

	fmt.Printf("%sezstack configuration%s\n", ui.Bold, ui.Reset)
	fmt.Printf("Config directory: %s\n", configDir)
	if ezstackHome != "" {
		fmt.Printf("  (set via EZSTACK_HOME environment variable)\n")
	} else {
		fmt.Printf("  (default: $HOME/.ezstack, override with EZSTACK_HOME env var)\n")
	}
	fmt.Printf("Config file: %s/config.json\n\n", configDir)

	fmt.Printf("%sGlobal Settings:%s\n", ui.Bold, ui.Reset)
	fmt.Printf("  default_base_branch: %s\n", valueOrDefault(cfg.DefaultBaseBranch, "main"))
	if cfg.GitHubToken != "" {
		fmt.Printf("  github_token:        %s\n", "****** (set)")
	} else {
		fmt.Printf("  github_token:        %s\n", "(not set - using gh cli)")
	}

	repoPath, err := getCurrentRepoPath()
	if err == nil {
		fmt.Printf("\n%sCurrent Repository:%s\n", ui.Bold, ui.Reset)
		fmt.Printf("  repo_path: %s\n", repoPath)
		repoCfg := cfg.GetRepoConfig(repoPath)
		if repoCfg != nil {
			fmt.Printf("  worktree_base_dir: %s\n", valueOrDefault(repoCfg.WorktreeBaseDir, "(not set)"))
			if repoCfg.DefaultBaseBranch != "" {
				fmt.Printf("  default_base_branch: %s (repo override)\n", repoCfg.DefaultBaseBranch)
			}
			if repoCfg.CdAfterNew != nil {
				fmt.Printf("  cd_after_new: %v\n", *repoCfg.CdAfterNew)
			} else {
				fmt.Printf("  cd_after_new: false (default)\n")
			}
		} else {
			fmt.Printf("  worktree_base_dir: %s(not configured for this repo)%s\n", ui.Yellow, ui.Reset)
			fmt.Printf("  Run: ezs config set worktree_base_dir <path>\n")
		}
	}

	if len(cfg.Repos) > 0 {
		fmt.Printf("\n%sConfigured Repositories:%s\n", ui.Bold, ui.Reset)
		for path, repoCfg := range cfg.Repos {
			marker := ""
			if path == repoPath {
				marker = " (current)"
			}
			fmt.Printf("  %s%s%s%s\n", ui.Cyan, path, marker, ui.Reset)
			fmt.Printf("    worktree_base_dir: %s\n", repoCfg.WorktreeBaseDir)
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

// isInsidePath checks if child path is inside or equal to parent path
func isInsidePath(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	// If relative path starts with "..", it's outside parent
	return !strings.HasPrefix(rel, "..") && rel != ".."
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

	fmt.Printf("\n%sConfiguring ezstack for repository:%s\n", ui.Bold, ui.Reset)
	fmt.Printf("  %s%s%s\n\n", ui.Cyan, repoPath, ui.Reset)

	repoCfg := cfg.GetRepoConfig(repoPath)
	currentWorktreeBaseDir := ""
	currentCdAfterNew := false
	if repoCfg != nil {
		currentWorktreeBaseDir = repoCfg.WorktreeBaseDir
		if repoCfg.CdAfterNew != nil {
			currentCdAfterNew = *repoCfg.CdAfterNew
		}
	}

	// Generate default worktree dir: ../<repo_name>_worktrees
	if currentWorktreeBaseDir == "" {
		repoName := filepath.Base(repoPath)
		currentWorktreeBaseDir = filepath.Join(filepath.Dir(repoPath), repoName+"_worktrees")
	}

	configChanged := false

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
			if isInsidePath(worktreeBaseDir, repoPath) {
				ui.Error("Worktree directory cannot be inside the repository root")
				continue
			}

			if repoCfg == nil {
				repoCfg = &config.RepoConfig{}
			}
			repoCfg.WorktreeBaseDir = worktreeBaseDir
			configChanged = true
			ui.Success(fmt.Sprintf("Set worktree_base_dir = %s", worktreeBaseDir))
		}
		break
	}

	cdAfterNew := ui.ConfirmTUIWithDefault("Auto-cd into new worktrees after creation", currentCdAfterNew)
	if repoCfg == nil {
		repoCfg = &config.RepoConfig{}
	}
	repoCfg.CdAfterNew = &cdAfterNew
	configChanged = true
	ui.Success(fmt.Sprintf("Set cd_after_new = %v", cdAfterNew))

	if configChanged {
		cfg.SetRepoConfig(repoPath, repoCfg)
		if err := cfg.Save(); err != nil {
			return err
		}
	} else {
		ui.Info("No changes made to configuration")
	}

	fmt.Fprintf(os.Stderr, "\n%sNote:%s For 'ezs goto' and 'ezs new --cd' to change directories, add this to your shell config (if not already done):\n", ui.Bold, ui.Reset)
	fmt.Fprintf(os.Stderr, "  eval \"$(ezs --shell-init)\"\n\n")

	return nil
}
