package commands

import (
	"flag"
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/internal/helpers"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
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
	force := fs.Bool("force", false, "Force delete even if branch has children")
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

	var branchName string
	if fs.NArg() < 1 {
		stacks := mgr.ListStacks()
		var allBranches []ui.BranchWithChildInfo
		for _, s := range stacks {
			for _, b := range s.Branches {
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

		if hasChildren && !*force {
			ui.Error(fmt.Sprintf("Branch '%s' has child branches", branchName))
			return fmt.Errorf("cannot delete branch with children. Use --force to delete anyway")
		}

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

		if mgr.IsMainBranch(branchName) {
			return fmt.Errorf("cannot delete main/master branch")
		}

		children := mgr.GetChildren(branchName)
		if len(children) > 0 && !*force {
			ui.Error(fmt.Sprintf("Branch '%s' has child branches:", branchName))
			for _, c := range children {
				fmt.Printf("  - %s\n", c.Name)
			}
			return fmt.Errorf("cannot delete branch with children. Use --force to delete anyway")
		}

		ui.Warn(fmt.Sprintf("This will delete branch '%s' and its worktree", branchName))
		if len(children) > 0 {
			ui.Warn(fmt.Sprintf("This will also orphan %d child branch(es)", len(children)))
		}

		if !ui.ConfirmTUI(fmt.Sprintf("Delete branch '%s' and its worktree", branchName)) {
			ui.Warn("Cancelled")
			return nil
		}
	}

	repoRoot := mgr.GetRepoDir()
	if err := os.Chdir(repoRoot); err != nil {
		return fmt.Errorf("failed to change to repo root: %w", err)
	}

	mgr, err = stack.NewManager(repoRoot)
	if err != nil {
		return err
	}

	if err := mgr.DeleteBranch(branchName, *force); err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Deleted branch '%s' and its worktree", branchName))
	fmt.Printf("cd %s\n", repoRoot)

	return nil
}
