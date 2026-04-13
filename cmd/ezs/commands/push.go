package commands

import (
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
	"github.com/spf13/pflag"
)

func Push(args []string) error {
	fs := pflag.NewFlagSet("push", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sPush current branch or entire stack%s

%sUSAGE%s
    ezs push [options]

%sOPTIONS%s
    -s, --stack          Push all branches in the current stack
    -b, --branch <name>  Push a specific branch by name
    -f, --force          Force push
    -h, --help           Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	stackFlag := fs.BoolP("stack", "s", false, "Push all branches in the current stack")
	branchFlag := fs.StringP("branch", "b", "", "Push a specific branch by name")
	force := fs.BoolP("force", "f", false, "Force push")
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

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)

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
		return pushSpecificBranch(g, *branchFlag, *force, getBranchRemote(*branchFlag))
	}

	if !*stackFlag {
		currentBranch, err := g.CurrentBranch()
		if err != nil {
			return pushBranch(g, *force, "origin")
		}
		return pushBranch(g, *force, getBranchRemote(currentBranch))
	}

	mgr, err := stack.NewReadOnlyManager(cwd)
	if err != nil {
		return err
	}

	currentStack, _, err := mgr.GetCurrentStack()
	if err != nil {
		return err
	}

	return pushStack(g, mgr, currentStack, *force)
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

func pushStack(g *git.Git, mgr *stack.Manager, s *config.Stack, force bool) error {
	failed := 0
	for _, b := range s.Branches {
		if b.IsMerged {
			continue
		}
		remote := ResolveBranchRemote(g, mgr, b.Name)
		if remote == config.RemoteNoPush {
			ui.Warn(fmt.Sprintf("Skipping '%s' (fork does not allow maintainer push)", b.Name))
			continue
		}
		args := []string{"push", "-u", remote, b.Name}
		if force {
			args = []string{"push", "-u", "--force-with-lease", remote, b.Name}
		}
		if err := g.RunInteractive(args...); err != nil {
			ui.Warn(fmt.Sprintf("Failed to push '%s': %v", b.Name, err))
			failed++
			continue
		}
		ui.Success(fmt.Sprintf("Pushed '%s'", b.Name))
	}
	if failed > 0 {
		return fmt.Errorf("%d branch(es) failed to push", failed)
	}
	return nil
}
