package commands

import (
	"flag"
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/internal/git"
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

	g := git.New(cwd)

	var branchName string
	if fs.NArg() < 1 {
		worktrees, err := g.ListWorktrees()
		if err != nil {
			return fmt.Errorf("failed to list worktrees: %w", err)
		}

		// Filter out main branch
		var wtInfos []ui.WorktreeInfo
		for _, wt := range worktrees {
			if mgr.IsMainBranch(wt.Branch) {
				continue
			}
			wtInfos = append(wtInfos, ui.WorktreeInfo{
				Path:   wt.Path,
				Branch: wt.Branch,
			})
		}

		if len(wtInfos) == 0 {
			return fmt.Errorf("no branches found. Create one with: ezs new <branch-name>")
		}

		stacks := mgr.ListStacks()
		selected, err := ui.SelectWorktreeWithStackPreview(wtInfos, stacks, "Select branch to delete")
		if err != nil {
			return err
		}
		branchName = selected.Branch

		children := mgr.GetChildren(branchName)
		hasChildren := len(children) > 0

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

	// Try stack-aware delete first; if the branch isn't in any stack,
	// fall back to direct worktree + branch removal.
	if err := mgr.DeleteBranch(branchName, *force); err != nil {
		if mgr.GetBranch(branchName) != nil {
			return err
		}

		if err := deleteNonStackBranch(repoRoot, branchName); err != nil {
			return err
		}
	}

	ui.Success(fmt.Sprintf("Deleted branch '%s' and its worktree", branchName))
	fmt.Printf("cd %s\n", repoRoot)

	return nil
}

// deleteNonStackBranch removes a worktree and branch that aren't tracked in any stack.
func deleteNonStackBranch(repoRoot, branchName string) error {
	g := git.New(repoRoot)

	worktrees, err := g.ListWorktrees()
	if err != nil {
		return fmt.Errorf("failed to list worktrees: %w", err)
	}

	for _, wt := range worktrees {
		if wt.Branch == branchName {
			return g.RemoveWorktree(wt.Path, true, branchName)
		}
	}

	// No worktree found - try deleting just the branch
	if g.BranchExists(branchName) {
		return g.DeleteBranch(branchName, true)
	}

	return fmt.Errorf("branch '%s' not found", branchName)
}
