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
    -s, --stack    Push all branches in the current stack
    -f, --force    Force push
    -h, --help     Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	stackFlag := fs.BoolP("stack", "s", false, "Push all branches in the current stack")
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

	if !*stackFlag {
		return pushBranch(g, *force)
	}

	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	currentStack, _, err := mgr.GetCurrentStack()
	if err != nil {
		return err
	}

	return pushStack(g, currentStack, *force)
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
