package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/github"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

// Upstream is the entrypoint for `ezs upstream …` — manages public-fork
// stacking configuration: which upstream parent repo to target, what local
// git remote points at it, and the per-repo ForkMode toggle.
func Upstream(args []string) error {
	if HasExamplesFlag("upstream", args) {
		return nil
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printUpstreamUsage()
		return nil
	}
	switch args[0] {
	case "show":
		return upstreamShow(args[1:])
	case "set":
		return upstreamSet(args[1:])
	case "unset":
		return upstreamUnset(args[1:])
	case "init":
		return upstreamInit(args[1:])
	case "auto":
		return upstreamSetMode(config.ForkModeAuto)
	case "disable":
		return upstreamSetMode(config.ForkModeDisabled)
	default:
		return fmt.Errorf("unknown upstream subcommand: %s\nRun `ezs upstream` for usage", args[0])
	}
}

func printUpstreamUsage() {
	fmt.Fprintf(os.Stderr, `%sManage public-fork stacking configuration%s

%sUSAGE%s
    ezs upstream <subcommand> [args]

%sSUBCOMMANDS%s
    show              Print fork-mode and upstream config for the current repo
    set <owner/repo>  Manually set upstream (auto-adds git remote if missing)
                        --remote <name>          local remote name (default: upstream)
                        --default-branch <name>  upstream's default branch
    unset             Clear upstream and disable fork mode
    init              Run interactive auto-detection
    auto              Reset ForkMode to "auto" (will detect on next pr create)
    disable           Disable fork mode without clearing cached upstream

%sFORK MODE%s
    When origin is detected as a public fork on github.com, ezstack routes
    the bottom PR of a stack cross-repo against the upstream parent and
    keeps intermediate PRs inside your fork. When the bottom PR merges,
    `+"`ezs pr promote`"+` re-creates the next branch's PR cross-repo.

    Lost on promotion: PR #/URL, inline review comments, approvals, CI history.
    Preserved (copied):  title, body, labels, assignees, reviewers, milestone.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
}

func upstreamShow(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("upstream show takes no arguments")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}
	cfg := mgr.GetConfig()
	repoPath := mgr.GetRepoDir()
	fmt.Fprintf(os.Stdout, "Repo:       %s\n", repoPath)
	fmt.Fprintf(os.Stdout, "ForkMode:   %s\n", cfg.GetForkMode(repoPath))
	o, r, rem, db := cfg.GetUpstream(repoPath)
	if o == "" {
		fmt.Fprintln(os.Stdout, "Upstream:   (none)")
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "Run `ezs upstream init` to detect the upstream parent on GitHub,")
		fmt.Fprintln(os.Stdout, "or `ezs upstream set <owner>/<repo>` to configure it manually.")
		return nil
	}
	fmt.Fprintf(os.Stdout, "Upstream:   %s/%s\n", o, r)
	fmt.Fprintf(os.Stdout, "Remote:     %s\n", rem)
	fmt.Fprintf(os.Stdout, "DefaultBr:  %s\n", db)
	if rc := cfg.GetRepoConfig(repoPath); rc != nil && rc.UpstreamDetectedAt > 0 {
		fmt.Fprintf(os.Stdout, "DetectedAt: %d (unix)\n", rc.UpstreamDetectedAt)
	}
	return nil
}

func upstreamSet(args []string) error {
	fs := pflag.NewFlagSet("upstream set", pflag.ContinueOnError)
	remote := fs.String("remote", "upstream", "Local git remote name to use for upstream")
	defBranch := fs.String("default-branch", "", "Upstream's default branch (auto-detected when omitted)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: ezs upstream set <owner>/<repo> [--remote name] [--default-branch main]")
	}
	parts := strings.Split(fs.Arg(0), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("invalid owner/repo: %q", fs.Arg(0))
	}
	owner, repo := parts[0], parts[1]

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	g := git.New(cwd)
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}
	cfg := mgr.GetConfig()
	repoPath := mgr.GetRepoDir()

	// Resolve default branch — explicit > detected > "main".
	db := *defBranch
	if db == "" {
		if up, dErr := github.DetectUpstream(owner, repo); dErr == nil && up.IsFork {
			db = up.DefaultBranch
		} else if up, dErr := github.DetectUpstream(owner, repo); dErr == nil {
			// Manual `set` may target a non-fork (e.g. canonical repo where
			// the user has direct push); we still record its default branch.
			_ = up
		}
		// If neither path produced one, fall back to "main".
		if db == "" {
			db = "main"
		}
	}

	// Add or reuse the upstream remote.
	rem := *remote
	if existing, _, fErr := g.FindRemoteByOwner(owner); fErr == nil && existing != "" {
		rem = existing
	} else {
		// Prefer SSH iff origin is SSH, to keep auth scheme consistent.
		originURL, _ := g.GetRemote("origin")
		var url string
		if strings.HasPrefix(originURL, "git@") || strings.HasPrefix(originURL, "ssh://") {
			url = fmt.Sprintf("git@github.com:%s/%s.git", owner, repo)
		} else {
			url = fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
		}
		if err := g.AddRemote(rem, url); err != nil {
			return fmt.Errorf("add git remote %q: %w", rem, err)
		}
		ui.Success(fmt.Sprintf("Added git remote '%s' → %s", rem, url))
	}

	cfg.SetUpstream(repoPath, owner, repo, rem, db)
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	ui.Success(fmt.Sprintf("Upstream set to %s/%s (remote=%s, default-branch=%s)", owner, repo, rem, db))
	return nil
}

func upstreamUnset(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("upstream unset takes no arguments")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}
	cfg := mgr.GetConfig()
	cfg.ClearUpstream(mgr.GetRepoDir())
	if err := cfg.Save(); err != nil {
		return err
	}
	ui.Success("Upstream cleared; fork mode disabled")
	return nil
}

func upstreamInit(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("upstream init takes no arguments")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	g := git.New(cwd)
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}
	// Force detection: reset to "auto" so EnsureUpstreamDetected re-prompts.
	cfg := mgr.GetConfig()
	repoPath := mgr.GetRepoDir()
	rc := cfg.GetRepoConfig(repoPath)
	if rc == nil {
		rc = &config.RepoConfig{RepoPath: repoPath}
	}
	rc.ForkMode = config.ForkModeAuto
	rc.UpstreamOwner = ""
	rc.UpstreamRepo = ""
	rc.UpstreamRemote = ""
	rc.UpstreamDefaultBranch = ""
	rc.UpstreamDetectedAt = 0
	cfg.SetRepoConfig(repoPath, rc)
	if err := cfg.Save(); err != nil {
		return err
	}

	up, err := EnsureUpstreamDetected(g, mgr)
	if err != nil {
		return err
	}
	if up == nil || !up.Enabled {
		ui.Info("Fork mode not enabled (origin is not a fork, or you declined).")
	}
	return nil
}

func upstreamSetMode(mode string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}
	cfg := mgr.GetConfig()
	cfg.SetForkMode(mgr.GetRepoDir(), mode)
	if err := cfg.Save(); err != nil {
		return err
	}
	ui.Success(fmt.Sprintf("Fork mode set to %q", mode))
	return nil
}
