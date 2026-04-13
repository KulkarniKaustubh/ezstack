package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
)

// Doctor verifies that ezstack's runtime dependencies and config are healthy.
func Doctor(args []string) error {
	if HasExamplesFlag("doctor", args) {
		return nil
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintf(os.Stderr, `%sCheck ezstack health%s

%sUSAGE%s
    ezs doctor

Verifies required tools (git, gh, fzf) are installed and that the
ezstack configuration is valid.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset)
			return nil
		}
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
	checkBin("gh", "required for PR/CI features", false)
	checkBin("fzf", "required for fuzzy selection", false)

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

	fmt.Fprintln(os.Stderr)
	if problems == 0 {
		ui.Success("No problems detected")
		return nil
	}
	return fmt.Errorf("%d problem(s) detected", problems)
}

// Info prints a diagnostic report (versions, config state) for bug reports.
func Info(version string) {
	fmt.Printf("%sezstack diagnostic info%s\n\n", ui.Bold, ui.Reset)
	fmt.Printf("ezstack version: %s\n", version)

	if out, err := exec.Command("go", "version").Output(); err == nil {
		fmt.Printf("go: %s", string(out))
	}
	if out, err := exec.Command("git", "--version").Output(); err == nil {
		fmt.Printf("git: %s", string(out))
	}
	if out, err := exec.Command("gh", "--version").Output(); err == nil {
		fmt.Printf("gh: %s", string(out))
	} else {
		fmt.Println("gh: not installed")
	}
	if out, err := exec.Command("fzf", "--version").Output(); err == nil {
		fmt.Printf("fzf: %s", string(out))
	} else {
		fmt.Println("fzf: not installed")
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
