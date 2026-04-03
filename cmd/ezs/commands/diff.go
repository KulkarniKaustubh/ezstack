package commands

import (
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
	"github.com/spf13/pflag"
)

func Diff(args []string) error {
	fs := pflag.NewFlagSet("diff", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sShow diff against parent branch%s

%sUSAGE%s
    ezs diff [options] [-- git-diff-options]

%sDESCRIPTION%s
    Shows the diff between the current branch and its parent in the stack.
    Any arguments after -- are passed directly to git diff.

%sOPTIONS%s
    --stat         Show diffstat only
    -h, --help     Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	stat := fs.Bool("stat", false, "Show diffstat only")
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
	mgr, err := stack.NewReadOnlyManager(cwd)
	if err != nil {
		return err
	}

	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		return err
	}

	// Use origin/ only for the stack root (e.g. main), not for stacked parents.
	// Stacked parents may have been rebased locally but not yet pushed, so the
	// local ref is the correct base for the diff.
	parentRef := branch.Parent
	if branch.Parent == currentStack.Root && g.RemoteBranchExists(branch.Parent) {
		parentRef = "origin/" + branch.Parent
	}

	diffArgs := []string{"diff", parentRef + "..." + branch.Name}
	if *stat {
		diffArgs = append(diffArgs, "--stat")
	}
	diffArgs = append(diffArgs, fs.Args()...)

	return g.RunInteractive(diffArgs...)
}
