package commands

import (
	"flag"
	"fmt"
	"os"

	"github.com/ezstack/ezstack/internal/git"
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
    If branch-name is omitted, shows interactive selection of all worktrees.
    Works for both stacked and unstacked worktrees.

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

	g := git.New(cwd)
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	if fs.NArg() > 0 {
		branchName := fs.Arg(0)

		// First try to find in stacks
		if targetBranch := mgr.GetBranch(branchName); targetBranch != nil {
			if targetBranch.IsMerged {
				return fmt.Errorf("branch '%s' has been merged and its worktree was deleted", branchName)
			}
			if targetBranch.WorktreePath == "" {
				if targetBranch.IsRemote {
					return fmt.Errorf("cannot go to remote branch '%s' - it has no local worktree", branchName)
				}
				return fmt.Errorf("no worktree path for branch '%s'", branchName)
			}
			fmt.Printf("cd %s\n", targetBranch.WorktreePath)
			return nil
		}

		// Not in a stack - search all worktrees
		worktrees, err := g.ListWorktrees()
		if err != nil {
			return fmt.Errorf("failed to list worktrees: %w", err)
		}

		for _, wt := range worktrees {
			if wt.Branch == branchName {
				fmt.Printf("cd %s\n", wt.Path)
				return nil
			}
		}

		return fmt.Errorf("branch '%s' not found in any worktree", branchName)
	}

	// Interactive selection - get all worktrees
	worktrees, err := g.ListWorktrees()
	if err != nil {
		return fmt.Errorf("failed to list worktrees: %w", err)
	}

	if len(worktrees) == 0 {
		return fmt.Errorf("no worktrees found. Create one with: ezs new <branch-name>")
	}

	// Convert to UI worktree info
	wtInfos := make([]ui.WorktreeInfo, len(worktrees))
	for i, wt := range worktrees {
		wtInfos[i] = ui.WorktreeInfo{
			Path:   wt.Path,
			Branch: wt.Branch,
		}
	}

	stacks := mgr.ListStacks()
	selected, err := ui.SelectWorktreeWithStackPreview(wtInfos, stacks, "Select worktree")
	if err != nil {
		return err
	}

	fmt.Printf("cd %s\n", selected.Path)
	return nil
}
