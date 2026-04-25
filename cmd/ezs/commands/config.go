package commands

import (
	"encoding/json"
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
    export <file>        Write the global config to <file>
    import <file>        Replace the global config from <file>

%sKEYS FOR 'set'%s
    worktree_base_dir     Base directory for worktrees (per-repo)
    default_base_branch   Default base branch (e.g., main)
    github_token          GitHub token for API access
    cd_after_new          Auto-cd to new worktree (true/false, per-repo)
    use_worktrees         Use git worktrees for new branches (true/false, per-repo)
    init_submodules       Mirror initialized submodules into new worktrees (true/false, per-repo)
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
	if HasExamplesFlag("config", args) {
		return nil
	}
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
		// Always exactly 3 args. The previous code joined trailing tokens
		// into the value with strings.Join, which silently absorbed typos
		// like `set worktree_base_dir /tmp/foo --bogus` into the stored
		// value. Multi-word values (notably agent_command shell command
		// lines) must now be quoted: `set agent_command "claude --flag"`.
		if len(args) != 3 {
			return fmt.Errorf("usage: ezs config set <key> <value>\n(if the value contains spaces or starts with '-', wrap it in quotes — e.g. ezs config set agent_command \"claude --dangerously-skip-permissions\")")
		}
		return configSet(args[1], args[2])
	case "show":
		if len(args) != 1 {
			return fmt.Errorf("usage: ezs config show")
		}
		return configShow()
	case "export":
		if len(args) != 2 {
			return fmt.Errorf("usage: ezs config export <file>")
		}
		return configExport(args[1])
	case "import":
		if len(args) != 2 {
			return fmt.Errorf("usage: ezs config import <file>")
		}
		return configImport(args[1])
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
	case "init_submodules":
		repoPath, err := getCurrentRepoPath()
		if err != nil {
			return fmt.Errorf("init_submodules is a per-repo setting: %w", err)
		}
		repoCfg := cfg.GetRepoConfig(repoPath)
		if repoCfg == nil {
			repoCfg = &config.RepoConfig{}
		}
		boolVal := value == "true" || value == "1" || value == "yes"
		repoCfg.InitSubmodules = &boolVal
		cfg.SetRepoConfig(repoPath, repoCfg)
		ui.Info(fmt.Sprintf("Setting init_submodules for repo: %s", repoPath))
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
		return fmt.Errorf("unknown config key: %s\nValid keys: worktree_base_dir, default_base_branch, github_token, cd_after_new, use_worktrees, init_submodules, sync_strategy, agent_command", key)
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
			if repoCfg.InitSubmodules != nil {
				fmt.Fprintf(os.Stderr, "  init_submodules: %v\n", *repoCfg.InitSubmodules)
			} else {
				fmt.Fprintf(os.Stderr, "  init_submodules: true (default)\n")
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

// redactedExportMarker is written in place of the real github_token on
// export. Imports that see this value preserve the existing token instead of
// clobbering it, so exporting + importing is a safe round-trip even when the
// token is redacted.
const redactedExportMarker = "<redacted-by-ezs-export>"

// configExport writes the global ezstack config file to the given path. The
// github_token field is always redacted — backups that live outside
// ~/.ezstack should never leak a PAT. The file is written 0600 because even
// a redacted config reveals repo layouts worth protecting on shared hosts.
func configExport(dest string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	exportCopy := *cfg
	if exportCopy.GitHubToken != "" {
		exportCopy.GitHubToken = redactedExportMarker
	}
	data, err := json.MarshalIndent(&exportCopy, "", "  ")
	if err != nil {
		return err
	}
	dest = helpers.ExpandPath(dest)
	if err := os.WriteFile(dest, data, 0600); err != nil {
		return fmt.Errorf("failed to write %s: %w", dest, err)
	}
	ui.Success(fmt.Sprintf("Exported config to %s (github_token redacted, mode 0600)", dest))
	return nil
}

// configImport replaces the global ezstack config file with the contents of
// the given path. The import is validated (valid JSON into Config shape) and
// the user must confirm a summary of what will change before the replacement
// takes effect. A redacted github_token in the import is ignored — the
// existing local token is preserved — so configExport → configImport is a
// lossless round-trip that never leaks secrets through a backup file.
func configImport(src string) error {
	src = helpers.ExpandPath(src)
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", src, err)
	}
	// Validate into a throwaway value first so we don't mutate anything on a
	// malformed file.
	var imported config.Config
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&imported); err != nil {
		return fmt.Errorf("invalid config file: %w", err)
	}

	current, err := config.Load()
	if err != nil {
		return err
	}

	// Preserve the existing token when the import has the redacted marker or
	// is empty (so a backup never blanks out a real token).
	if imported.GitHubToken == redactedExportMarker || imported.GitHubToken == "" {
		imported.GitHubToken = current.GitHubToken
	}

	summarizeConfigDiff(current, &imported)
	if !ui.ConfirmTUIWithDefault(fmt.Sprintf("Replace global ezstack config with %s?", src), false) {
		ui.Warn("Cancelled")
		return nil
	}
	if err := imported.Save(); err != nil {
		return err
	}
	ui.Success(fmt.Sprintf("Imported config from %s", src))
	return nil
}

// summarizeConfigDiff prints a human-readable diff of the repo-scoped config
// entries between the current and imported configs. Global fields that
// differ are also highlighted. This is best-effort: its only job is giving
// the user enough context to decide whether to confirm the import.
func summarizeConfigDiff(current, imported *config.Config) {
	fmt.Fprintf(os.Stderr, "\n%sConfig changes preview:%s\n", ui.Bold, ui.Reset)
	if current.DefaultBaseBranch != imported.DefaultBaseBranch {
		fmt.Fprintf(os.Stderr, "  default_base_branch: %q → %q\n",
			current.DefaultBaseBranch, imported.DefaultBaseBranch)
	}
	curRepos := current.Repos
	impRepos := imported.Repos
	for path := range curRepos {
		if _, ok := impRepos[path]; !ok {
			fmt.Fprintf(os.Stderr, "  %s- repo removed: %s%s\n", ui.Red, path, ui.Reset)
		}
	}
	for path := range impRepos {
		if _, ok := curRepos[path]; !ok {
			fmt.Fprintf(os.Stderr, "  %s+ repo added:   %s%s\n", ui.Green, path, ui.Reset)
		}
	}
	fmt.Fprintln(os.Stderr)
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
	currentInitSubmodules := true
	currentAgentCommand := "claude"
	if repoCfg != nil {
		currentWorktreeBaseDir = repoCfg.WorktreeBaseDir
		if repoCfg.CdAfterNew != nil {
			currentCdAfterNew = *repoCfg.CdAfterNew
		}
		if repoCfg.UseWorktrees != nil {
			currentUseWorktrees = *repoCfg.UseWorktrees
		}
		if repoCfg.InitSubmodules != nil {
			currentInitSubmodules = *repoCfg.InitSubmodules
		}
		if repoCfg.AgentCommand != "" {
			currentAgentCommand = repoCfg.AgentCommand
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

	initSubmodules := ui.ConfirmTUIWithDefault("Mirror initialized submodules into new worktrees", currentInitSubmodules)
	repoCfg.InitSubmodules = &initSubmodules
	ui.Success(fmt.Sprintf("Set init_submodules = %v", initSubmodules))

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

	// Agent command: which CLI 'ezs agent' should invoke.
	agentCommand := strings.TrimSpace(ui.Prompt("AI agent CLI command (used by 'ezs agent')", currentAgentCommand))
	if agentCommand == "" {
		agentCommand = "claude"
	}
	repoCfg.AgentCommand = agentCommand
	ui.Success(fmt.Sprintf("Set agent_command = %s", agentCommand))

	cfg.SetRepoConfig(repoPath, repoCfg)
	if err := cfg.Save(); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\n%sNote:%s For 'ezs goto' and 'ezs new --cd' to change directories, add this to your shell config (if not already done):\n", ui.Bold, ui.Reset)
	fmt.Fprintf(os.Stderr, "  eval \"$(ezs --shell-init)\"\n\n")

	return nil
}
