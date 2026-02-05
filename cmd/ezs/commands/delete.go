package commands

import (
	"flag"
	"fmt"
	"os"

	"github.com/ezstack/ezstack/internal/helpers"
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
    If branch-name is omitted, shows interactive branch selector.
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

	helpers.MergeFlags(forceShort, force)

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	// Get the branch name to delete
	var branchName string
	if fs.NArg() < 1 {
		// If no branch specified, show interactive fzf selection
		stacks := mgr.ListStacks()
		var allBranches []ui.BranchWithChildInfo
		for _, s := range stacks {
			for _, b := range s.Branches {
				// Skip merged branches and main branch
				if b.IsMerged || mgr.IsMainBranch(b.Name) {
					continue
				}
				children := mgr.GetChildren(b.Name)
				allBranches = append(allBranches, ui.BranchWithChildInfo{
					Branch:      b,
					HasChildren: len(children) > 0,
				})
			}
		}

		if len(allBranches) == 0 {
			return fmt.Errorf("no branches found. Create one with: ezs new <branch-name>")
		}

		selectedBranch, hasChildren, err := ui.SelectBranchForDeletion(allBranches, "Select branch to delete")
		if err != nil {
			return err
		}
		branchName = selectedBranch.Name

		// Check if branch has children and force flag is not set
		if hasChildren && !*force {
			ui.Error(fmt.Sprintf("Branch '%s' has child branches", branchName))
			return fmt.Errorf("cannot delete branch with children. Use --force to delete anyway")
		}

		// Show confirmation dialog immediately after selection
		confirmPrompt := fmt.Sprintf("Delete branch '%s'?", branchName)
		if hasChildren {
			confirmPrompt = fmt.Sprintf("Delete branch '%s'? %s(has child branches!)%s", branchName, ui.Red, ui.Reset)
		}
		if !ui.ConfirmTUI(confirmPrompt) {
			ui.Warn("Cancelled")
			return nil
		}
	} else {
		branchName = fs.Arg(0)

		// Non-interactive mode: check if it's a protected branch
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

	// Output cd command to move shell to repo root (for shell wrapper to eval)
	fmt.Printf("cd %s\n", repoRoot)

	return nil
}
