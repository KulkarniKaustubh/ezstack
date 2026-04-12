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

	if *branchFlag != "" {
		return pushSpecificBranch(g, *branchFlag, *force)
	}

	if !*stackFlag {
		return pushBranch(g, *force)
	}

	mgr, err := stack.NewReadOnlyManager(cwd)
	if err != nil {
		return err
	}

	currentStack, _, err := mgr.GetCurrentStack()
	if err != nil {
		return err
	}

	return pushStack(g, currentStack, *force)
}

func pushSpecificBranch(g *git.Git, branch string, force bool) error {
	args := []string{"push", "-u", "origin", branch}
	if force {
		args = []string{"push", "-u", "--force-with-lease", "origin", branch}
	}
	if err := g.RunInteractive(args...); err != nil {
		return fmt.Errorf("push failed for '%s': %w", branch, err)
	}
	ui.Success(fmt.Sprintf("Pushed '%s' to remote", branch))
	return nil
}

func pushBranch(g *git.Git, force bool) error {
	if force {
		if err := g.PushForce(); err != nil {
			return fmt.Errorf("force push failed: %w", err)
		}
	} else if err := g.Push(false); err != nil {
		return fmt.Errorf("push failed: %w", err)
	}
	ui.Success("Pushed to remote")
	return nil
}

func pushStack(g *git.Git, s *config.Stack, force bool) error {
	failed := 0
	for _, b := range s.Branches {
		if b.IsMerged {
			continue
		}
		args := []string{"push", "-u", "origin", b.Name}
		if force {
			args = []string{"push", "-u", "--force-with-lease", "origin", b.Name}
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
