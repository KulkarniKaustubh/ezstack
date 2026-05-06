package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
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
		}
	}

	checkSubmoduleHealth(&problems)

	fmt.Fprintln(os.Stderr)
	if problems == 0 {
		ui.Success("No problems detected")
		return nil
	}
	return fmt.Errorf("%d problem(s) detected", problems)
}

// checkSubmoduleHealth surfaces submodule states that the user is likely
// to want to know about: dirty working trees, detached HEAD edits in
// progress, and unpushed commits whose SHA the parent already records.
// Runs in the current working directory's repo only — the active worktree
// is what the user is editing.
//
// Increments *problems for hard issues (dirty / unpushed). Detached HEAD
// is reported as a warning only because it's the default state right
// after `git submodule update`; only flag it when also dirty or with
// unpushed commits.
func checkSubmoduleHealth(problems *int) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	g := git.New(cwd)
	if !g.HasSubmodules() {
		return
	}
	statuses, err := g.SubmoduleStatuses()
	if err != nil {
		ui.Warn(fmt.Sprintf("Could not inspect submodules: %v", err))
		return
	}
	if len(statuses) == 0 {
		return
	}
	clean := true
	for _, s := range statuses {
		if s.MergeConflict {
			ui.Error(fmt.Sprintf("Submodule '%s' has unresolved merge conflicts", s.Path))
			*problems++
			clean = false
			continue
		}
		if s.Dirty {
			ui.Warn(fmt.Sprintf("Submodule '%s' has uncommitted changes", s.Path))
			clean = false
		}
		if s.HasUnpushed {
			ui.Warn(fmt.Sprintf("Submodule '%s' has local commits not on origin — push the submodule before pushing the parent", s.Path))
			clean = false
		}
		if s.DetachedHead && (s.Dirty || s.HasUnpushed) {
			ui.Warn(fmt.Sprintf("Submodule '%s' is on a detached HEAD with edits — commits made here can be orphaned. Run: git -C %s switch -c <branch>", s.Path, s.Path))
			clean = false
		}
		if s.PointerChanged {
			ui.Warn(fmt.Sprintf("Submodule '%s' pointer differs from parent — commit the submodule pointer change in the parent or run `git submodule update`", s.Path))
			clean = false
		}
	}
	if clean {
		ui.Success(fmt.Sprintf("Submodules healthy (%d initialized)", len(statuses)))
	}
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
