package commands

import (
	"flag"
	"fmt"
	"os"

	"github.com/ezstack/ezstack/internal/config"
	"github.com/ezstack/ezstack/internal/stack"
	"github.com/ezstack/ezstack/internal/ui"
)

// Goto navigates to a branch worktree
func Goto(args []string) error {
	fs := flag.NewFlagSet("goto", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sNavigate to a branch worktree%s

%sUSAGE%s
    ezs goto [branch-name] [options]
    ezs go [branch-name] [options]

%sOPTIONS%s
    -h, --help    Show this help message

%sNOTES%s
    If branch-name is omitted, shows interactive selection.

    For cd to work, add this to your ~/.bashrc or ~/.zshrc:
        eval "$(ezs --shell-init)"
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	helpFlag := fs.Bool("h", false, "Show help")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
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

	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	var targetBranch *config.Branch

	if fs.NArg() > 0 {
		// Branch name provided
		targetBranch = mgr.GetBranch(fs.Arg(0))
		if targetBranch == nil {
			return fmt.Errorf("branch '%s' not found in any stack", fs.Arg(0))
		}
	} else {
		// Interactive selection with fzf
		stacks := mgr.ListStacks()
		var allBranches []*config.Branch
		for _, s := range stacks {
			allBranches = append(allBranches, s.Branches...)
		}

		if len(allBranches) == 0 {
			return fmt.Errorf("no branches found. Create one with: ezs new <branch-name>")
		}

		var err error
		targetBranch, err = ui.SelectBranchWithStacks(allBranches, stacks, "Select branch")
		if err != nil {
			return err
		}
	}

	if targetBranch.WorktreePath == "" {
		if targetBranch.IsRemote {
			return fmt.Errorf("cannot go to remote branch '%s' - it has no local worktree", targetBranch.Name)
		}
		return fmt.Errorf("no worktree path for branch '%s'", targetBranch.Name)
	}

	// Output cd command for eval
	// Usage: eval "$(ezs goto branch)"
	fmt.Printf("cd %s\n", targetBranch.WorktreePath)

	return nil
}
