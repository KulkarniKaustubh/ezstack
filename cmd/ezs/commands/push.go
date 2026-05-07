package commands

import (
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/hooks"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

func Push(args []string) error {
	if HasExamplesFlag("push", args) {
		return nil
	}
	fs := pflag.NewFlagSet("push", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sPush current branch or entire stack%s

%sUSAGE%s
    ezs push [options]

%sOPTIONS%s
    -s, --stack          Push all branches in the current stack
    -b, --branch <name>  Push a specific branch by name
    -f, --force          Force push
    --verify             Require ~/.ezstack/hooks/pre-push to exist and pass
    --all-remotes        Push to origin and the configured fork remote
    -h, --help           Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	stackFlag := fs.BoolP("stack", "s", false, "Push all branches in the current stack")
	branchFlag := fs.StringP("branch", "b", "", "Push a specific branch by name")
	force := fs.BoolP("force", "f", false, "Force push")
	verifyFlag := fs.Bool("verify", false, "Require ~/.ezstack/hooks/pre-push to exist and pass")
	allRemotesFlag := fs.Bool("all-remotes", false, "Push to origin and the configured fork remote")
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

	if os.Getenv(agentNoPushEnv) == "1" {
		return fmt.Errorf("push blocked: ezs agent --no-push is in effect")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	// --verify promotes pre-push from "run if present" to "require present and
	// pass". Without --verify a missing hook is fine; with --verify, a missing
	// or non-executable hook is a hard error.
	hookCtx := BuildHookContext()
	if *verifyFlag {
		if !hooks.Exists("pre-push") {
			return fmt.Errorf("--verify requires an executable hook at ~/.ezstack/hooks/pre-push")
		}
	}
	if err := hooks.Run("pre-push", hookCtx); err != nil {
		return err
	}
	defer func() {
		if hookErr := hooks.Run("post-push", hookCtx); hookErr != nil {
			ui.Warn(hookErr.Error())
		}
	}()

	g := git.New(cwd)
	warnUnpushedSubmodules(g)

	// Look up the branch's configured remote (if any), running fork detection lazily
	// for branches tagged is_remote that don't yet have a fork remote recorded.
	getBranchRemote := func(branchName string) string {
		mgr, err := stack.NewReadOnlyManager(cwd)
		if err != nil {
			return "origin"
		}
		return ResolveBranchRemote(g, mgr, branchName)
	}

	if *branchFlag != "" {
		remotes := pushTargets(*allRemotesFlag, getBranchRemote(*branchFlag))
		return pushToRemotes(remotes, func(r string) error {
			return pushSpecificBranch(g, *branchFlag, *force, r)
		})
	}

	if !*stackFlag {
		currentBranch, err := g.CurrentBranch()
		if err != nil {
			return pushBranch(g, *force, "origin")
		}
		remotes := pushTargets(*allRemotesFlag, getBranchRemote(currentBranch))
		return pushToRemotes(remotes, func(r string) error {
			return pushBranch(g, *force, r)
		})
	}

	mgr, err := stack.NewReadOnlyManager(cwd)
	if err != nil {
		return err
	}

	currentStack, _, err := mgr.GetCurrentStack()
	if err != nil {
		return err
	}

	return pushStack(g, mgr, currentStack, *force, *allRemotesFlag)
}

// pushToRemotes runs fn against each remote in order. Failures on earlier
// remotes do not short-circuit — every remote is attempted so users get a
// clear picture of what succeeded and what didn't. The returned error is
// aggregate-style: nil if all succeeded, otherwise a summary count.
func pushToRemotes(remotes []string, fn func(remote string) error) error {
	failed := 0
	for _, r := range remotes {
		if err := fn(r); err != nil {
			ui.Warn(err.Error())
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d push(es) failed", failed, len(remotes))
	}
	return nil
}

// pushTargets returns the list of remotes a single push invocation should fan out to.
// When allRemotes is true and the branch has a non-origin primary remote, both
// "origin" and the primary remote are returned; the RemoteNoPush sentinel is
// dropped so callers don't try to push a forbidden fork.
func pushTargets(allRemotes bool, primary string) []string {
	if !allRemotes {
		return []string{primary}
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range []string{"origin", primary} {
		if r == "" || r == config.RemoteNoPush || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	if len(out) == 0 {
		return []string{primary}
	}
	return out
}

// warnUnpushedSubmodules prints a warning for each initialized submodule
// that has local commits not on its origin. The parent's push will publish
// commits whose recorded submodule SHA may not be fetchable by anyone
// else, breaking teammates. This is a warning, not a block — the user may
// be intentionally pushing the parent first and will push the submodule
// next, or they may have already pushed the submodule and the local
// remote-tracking ref is stale. We can't tell the difference without a
// network fetch, and we don't want to slow down every push.
func warnUnpushedSubmodules(g *git.Git) {
	if !g.HasSubmodules() {
		return
	}
	statuses, err := g.SubmoduleStatuses()
	if err != nil || len(statuses) == 0 {
		return
	}
	for _, s := range statuses {
		if !s.HasUnpushed {
			continue
		}
		ui.Warn(fmt.Sprintf(
			"Submodule '%s' has %s not on origin — pushing the parent now will record a SHA teammates can't fetch.\n    To push the submodule first: cd %s && git push",
			s.Path, pluralizeCommits(s.UnpushedCount), s.Path,
		))
	}
}

// pluralizeCommits formats a commit count for human display. Used in
// submodule warnings where "1 commit" reads better than "1 commits".
func pluralizeCommits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	if n <= 0 {
		return "commits"
	}
	return fmt.Sprintf("%d commits", n)
}

func pushSpecificBranch(g *git.Git, branch string, force bool, remote string) error {
	if remote == config.RemoteNoPush {
		return fmt.Errorf("push not allowed for '%s' (fork does not allow maintainer push)", branch)
	}
	if remote == "" {
		remote = "origin"
	}
	args := []string{"push", "-u", remote, branch}
	if force {
		args = []string{"push", "-u", "--force-with-lease", remote, branch}
	}
	if err := g.RunInteractive(args...); err != nil {
		return fmt.Errorf("push failed for '%s': %w", branch, err)
	}
	ui.Success(fmt.Sprintf("Pushed '%s' to %s", branch, remote))
	return nil
}

func pushBranch(g *git.Git, force bool, remote string) error {
	if remote == config.RemoteNoPush {
		return fmt.Errorf("push not allowed (fork does not allow maintainer push)")
	}
	if remote == "" {
		remote = "origin"
	}
	if force {
		if err := g.PushForce(remote); err != nil {
			return fmt.Errorf("force push failed: %w", err)
		}
	} else if err := g.Push(false, remote); err != nil {
		return fmt.Errorf("push failed: %w", err)
	}
	ui.Success("Pushed to remote")
	return nil
}

func pushStack(g *git.Git, mgr *stack.Manager, s *config.Stack, force bool, allRemotes bool) error {
	failed := 0
	for _, b := range s.Branches {
		if b.IsMerged {
			continue
		}
		primary := ResolveBranchRemote(g, mgr, b.Name)
		if primary == config.RemoteNoPush && !allRemotes {
			ui.Warn(fmt.Sprintf("Skipping '%s' (fork does not allow maintainer push)", b.Name))
			continue
		}
		for _, remote := range pushTargets(allRemotes, primary) {
			if remote == config.RemoteNoPush {
				ui.Warn(fmt.Sprintf("Skipping '%s' on disallowed fork remote", b.Name))
				continue
			}
			args := []string{"push", "-u", remote, b.Name}
			if force {
				args = []string{"push", "-u", "--force-with-lease", remote, b.Name}
			}
			if err := g.RunInteractive(args...); err != nil {
				ui.Warn(fmt.Sprintf("Failed to push '%s' to %s: %v", b.Name, remote, err))
				failed++
				continue
			}
			ui.Success(fmt.Sprintf("Pushed '%s' to %s", b.Name, remote))
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d push(es) failed", failed)
	}
	return nil
}
