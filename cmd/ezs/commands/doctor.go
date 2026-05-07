package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/github"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

// Doctor verifies that ezstack's runtime dependencies and config are healthy.
func Doctor(args []string) error {
	if HasExamplesFlag("doctor", args) {
		return nil
	}
	fs := pflag.NewFlagSet("doctor", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sCheck ezstack health%s

%sUSAGE%s
    ezs doctor

%sOPTIONS%s
    -h, --help    Show this help message

Verifies required tools (git, gh, fzf) are installed and that the
ezstack configuration is valid.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	helpFlag := fs.BoolP("help", "h", false, "Show help")
	// pflag.ErrHelp is unreachable here because we registered our own --help
	// flag (which sets *helpFlag instead of returning ErrHelp). Surface the
	// help banner via the explicit *helpFlag branch below.
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *helpFlag {
		fs.Usage()
		return nil
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument: %s", fs.Arg(0))
	}

	fmt.Fprintf(os.Stderr, "%sezstack doctor%s\n\n", ui.Bold, ui.Reset)

	problems := 0
	checkBin := func(name, hint string, required bool) {
		path, err := exec.LookPath(name)
		if err != nil {
			if required {
				ui.Error(fmt.Sprintf("%s not found in PATH (%s)", name, hint))
				problems++
			} else {
				ui.Warn(fmt.Sprintf("%s not found in PATH (%s)", name, hint))
			}
			return
		}
		ui.Success(fmt.Sprintf("%s: %s", name, path))
	}

	checkBin("git", "required", true)
	checkBin("gh", "required for PR/CI features", true)
	checkBin("fzf", "required for interactive selection", true)

	cfgDir, err := config.ConfigDir()
	if err != nil {
		ui.Error(fmt.Sprintf("Could not resolve config directory: %v", err))
		problems++
	} else {
		ui.Success(fmt.Sprintf("Config directory: %s", cfgDir))
	}

	cfg, err := config.Load()
	if err != nil {
		ui.Error(fmt.Sprintf("Failed to load config: %v", err))
		problems++
	} else {
		ui.Success("Config file loads cleanly")
		for path, repoCfg := range cfg.Repos {
			if repoCfg.WorktreeBaseDir == "" {
				ui.Warn(fmt.Sprintf("Repo '%s': worktree_base_dir is not set", path))
				continue
			}
			if _, statErr := os.Stat(repoCfg.WorktreeBaseDir); statErr != nil && os.IsNotExist(statErr) {
				ui.Warn(fmt.Sprintf("Repo '%s': worktree_base_dir does not exist: %s", path, repoCfg.WorktreeBaseDir))
			}
			if filepath.IsAbs(repoCfg.WorktreeBaseDir) {
				if err := ValidateWorktreeBaseDir(repoCfg.WorktreeBaseDir, path); err != nil {
					ui.Error(fmt.Sprintf("Repo '%s': %v", path, err))
					problems++
				}
			}
			// Public-fork stacking sanity. Reports per-repo fork mode status,
			// flags drift between cached upstream and the actual git remote,
			// and surfaces the "you have push to upstream" warning when a
			// fork mode is enabled despite direct push access being available.
			problems += checkForkModeForRepo(cfg, path)
		}
	}

	fmt.Fprintln(os.Stderr)
	if problems == 0 {
		ui.Success("No problems detected")
		return nil
	}
	return fmt.Errorf("%d problem(s) detected", problems)
}

// checkForkModeForRepo runs per-repo fork-mode and upstream-remote
// validation. Returns the number of problems detected (> 0 contributes
// to doctor's overall failure exit code). Reads cached config only —
// never prompts or modifies state.
func checkForkModeForRepo(cfg *config.Config, repoPath string) int {
	problems := 0
	mode := cfg.GetForkMode(repoPath)
	switch mode {
	case config.ForkModeAuto:
		ui.Info(fmt.Sprintf("Repo '%s': fork mode = auto (detection on next pr create)", repoPath))
		return 0
	case config.ForkModeDisabled:
		ui.Info(fmt.Sprintf("Repo '%s': fork mode = disabled", repoPath))
		return 0
	case config.ForkModeEnabled:
		// fall through to the heavier checks below
	default:
		ui.Warn(fmt.Sprintf("Repo '%s': unknown fork mode %q", repoPath, mode))
		return 1
	}

	owner, repo, remote, defBr := cfg.GetUpstream(repoPath)
	if owner == "" {
		ui.Warn(fmt.Sprintf("Repo '%s': fork mode is enabled but no upstream is configured — run `ezs upstream init`", repoPath))
		return 1
	}
	ui.Success(fmt.Sprintf("Repo '%s': fork mode → %s/%s (remote=%s, default=%s)", repoPath, owner, repo, remote, defBr))

	// Verify the upstream git remote is wired up and points where we expect.
	g := git.New(repoPath)
	url, gErr := g.GetRemote(remote)
	if gErr != nil {
		ui.Warn(fmt.Sprintf("Repo '%s': git remote '%s' is missing — run `ezs upstream init` to add it", repoPath, remote))
		return problems + 1
	}
	expected := fmt.Sprintf("github.com/%s/%s", owner, repo)
	expectedSSH := fmt.Sprintf("github.com:%s/%s", owner, repo)
	urlLower := strings.ToLower(url)
	expLower := strings.ToLower(expected)
	expSSHLower := strings.ToLower(expectedSSH)
	if !strings.Contains(urlLower, expLower) && !strings.Contains(urlLower, expSSHLower) {
		ui.Warn(fmt.Sprintf("Repo '%s': git remote '%s' (%s) does not match cached upstream %s/%s — run `ezs upstream init` to refresh", repoPath, remote, url, owner, repo))
		problems++
	}

	// Optional: surface "you have push to upstream" so the user knows fork
	// mode is unnecessary. Only emit when fork mode is enabled (not auto/
	// disabled) to avoid spurious warnings on every doctor run for non-fork
	// repos. Network-bound — best effort only.
	if username, err := github.GetCurrentUser(); err == nil && username != "" {
		if github.CanPushToRepo(owner, repo, username) {
			ui.Warn(fmt.Sprintf("Repo '%s': you have push access to upstream %s/%s — consider `ezs upstream disable` and contributing directly", repoPath, owner, repo))
		}
	}
	return problems
}

// Info prints a diagnostic report (versions, config state) for bug reports.
func Info(version string) {
	fmt.Printf("%sezstack diagnostic info%s\n\n", ui.Bold, ui.Reset)
	fmt.Printf("ezstack version: %s\n", version)

	for _, tool := range []struct{ name, flag string }{
		{"go", "version"},
		{"git", "--version"},
		{"gh", "--version"},
		{"fzf", "--version"},
	} {
		out, err := exec.Command(tool.name, tool.flag).Output()
		if err != nil {
			fmt.Printf("%s: not installed\n", tool.name)
			continue
		}
		fmt.Printf("%s: %s", tool.name, string(out))
		if !strings.HasSuffix(string(out), "\n") {
			fmt.Println()
		}
	}

	cfgDir, _ := config.ConfigDir()
	fmt.Printf("\nconfig dir: %s\n", cfgDir)
	if _, err := os.Stat(filepath.Join(cfgDir, "config.json")); err == nil {
		fmt.Println("config file: present")
	} else {
		fmt.Println("config file: missing")
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("config load: error: %v\n", err)
		return
	}
	fmt.Printf("repos configured: %d\n", len(cfg.Repos))
	fmt.Printf("default base branch: %s\n", cfg.DefaultBaseBranch)
}
