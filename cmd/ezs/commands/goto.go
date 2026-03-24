package commands

import (
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
	"github.com/spf13/pflag"
)

// Goto navigates to a branch worktree
func Goto(args []string) error {
	fs := pflag.NewFlagSet("goto", pflag.ContinueOnError)
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
			if targetBranch.WorktreePath != "" {
				EmitCd(targetBranch.WorktreePath)
				return nil
			}
			// No worktree — fall back to git checkout
			if err := g.CheckoutBranch(branchName); err != nil {
				return fmt.Errorf("failed to switch to branch '%s': %w", branchName, err)
			}
			ui.Success(fmt.Sprintf("Switched to branch '%s'", branchName))
			return nil
		}

		// Not in a stack - search all worktrees
		worktrees, err := g.ListWorktrees()
		if err != nil {
			return fmt.Errorf("failed to list worktrees: %w", err)
		}

		for _, wt := range worktrees {
			if wt.Branch == branchName {
				EmitCd(wt.Path)
				return nil
			}
		}

		// Last resort: try git checkout if the branch exists
		if g.BranchExists(branchName) {
			if err := g.CheckoutBranch(branchName); err != nil {
				return fmt.Errorf("failed to switch to branch '%s': %w", branchName, err)
			}
			ui.Success(fmt.Sprintf("Switched to branch '%s'", branchName))
			return nil
		}

		return ui.NewExitError(ui.ExitBranchNotFound, "branch '%s' not found in any stack or worktree", branchName)
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

	EmitCd(selected.Path)
	return nil
}
