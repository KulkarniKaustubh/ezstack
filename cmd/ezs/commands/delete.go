package commands

import (
	"flag"
	"fmt"
	"os"

	"github.com/ezstack/ezstack/internal/git"
	"github.com/ezstack/ezstack/internal/stack"
	"github.com/ezstack/ezstack/internal/ui"
)

// Delete deletes a branch and its worktree
func Delete(args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sDelete a branch and its worktree%s

%sUSAGE%s
    ezs delete [branch-name] [options]
    ezs rm [branch-name] [options]

%sOPTIONS%s
    -f, --force    Force delete even if branch has children
    -h, --help     Show this help message

%sNOTES%s
    If branch-name is omitted, uses the current branch.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	// Long flags
	force := fs.Bool("force", false, "Force delete even if branch has children")
	// Short flags
	forceShort := fs.Bool("f", false, "Force delete (short)")
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

	// Merge short and long flags
	if *forceShort {
		*force = true
	}

	// Get the branch name to delete
	var branchName string
	if fs.NArg() < 1 {
		// If no branch specified, use current branch
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		g := git.New(cwd)
		branchName, err = g.CurrentBranch()
		if err != nil {
			return fmt.Errorf("failed to get current branch: %w", err)
		}
	} else {
		branchName = fs.Arg(0)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	// Check if it's a protected branch
	if mgr.IsMainBranch(branchName) {
		return fmt.Errorf("cannot delete main/master branch")
	}

	// Check for children first (before confirmation)
	children := mgr.GetChildren(branchName)
	if len(children) > 0 && !*force {
		ui.Error(fmt.Sprintf("Branch '%s' has child branches:", branchName))
		for _, c := range children {
			fmt.Printf("  - %s\n", c.Name)
		}
		return fmt.Errorf("cannot delete branch with children. Use --force to delete anyway")
	}

	// Show warning and ask for confirmation
	ui.Warn(fmt.Sprintf("This will delete branch '%s' and its worktree", branchName))
	if len(children) > 0 {
		ui.Warn(fmt.Sprintf("This will also orphan %d child branch(es)", len(children)))
	}

	if !ui.ConfirmTUI(fmt.Sprintf("Delete branch '%s' and its worktree", branchName)) {
		ui.Warn("Cancelled")
		return nil
	}

	// Change to repo root before deleting, in case we're deleting the current worktree
	repoRoot := mgr.GetRepoDir()
	if err := os.Chdir(repoRoot); err != nil {
		return fmt.Errorf("failed to change to repo root: %w", err)
	}

	// Reinitialize manager from repo root to ensure git commands work
	mgr, err = stack.NewManager(repoRoot)
	if err != nil {
		return err
	}

	// Perform the deletion
	if err := mgr.DeleteBranch(branchName, *force); err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Deleted branch '%s' and its worktree", branchName))
	return nil
}
