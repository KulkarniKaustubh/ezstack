package commands

import (
	"flag"
	"fmt"
	"os"

	"github.com/ezstack/ezstack/internal/config"
	"github.com/ezstack/ezstack/internal/git"
	"github.com/ezstack/ezstack/internal/helpers"
	"github.com/ezstack/ezstack/internal/stack"
	"github.com/ezstack/ezstack/internal/ui"
)

// Update reconciles ezstack config with git reality
func Update(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sReconcile ezstack config with git reality%s

%sUSAGE%s
    ezs update [options]

%sOPTIONS%s
    -a, --auto        Auto-accept all changes without prompting
    -d, --dry-run     Show what would be changed without making changes
    -h, --help        Show this help message

%sDESCRIPTION%s
    Scans git worktrees and branches to:
    
    1. Remove branches from config that no longer exist in git
    2. Add untracked worktrees to stacks (auto-detects parent via merge-base)
    3. Detect if parent relationships have changed (e.g., after manual rebase)

    After running update, all ezs commands (ls, status, sync, etc.) will
    work correctly with the current git state.

%sEXAMPLES%s
    ezs update              Interactive mode - confirm each change
    ezs update --auto       Auto-accept all detected changes
    ezs update --dry-run    Preview changes without applying
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	autoFlag := fs.Bool("auto", false, "Auto-accept all changes")
	autoShort := fs.Bool("a", false, "Auto-accept all changes (short)")
	dryRunFlag := fs.Bool("dry-run", false, "Show what would be changed")
	dryRunShort := fs.Bool("d", false, "Dry run (short)")
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

	helpers.MergeFlags(autoShort, autoFlag, dryRunShort, dryRunFlag)
	autoMode := *autoFlag
	dryRun := *dryRunFlag

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	baseBranch := cfg.GetBaseBranch(mgr.GetRepoDir())

	var result stack.UpdateResult
	hasChanges := false

	// Step 1: Detect orphaned branches (in config but not in git)
	orphaned := mgr.DetectOrphanedBranches()
	if len(orphaned) > 0 {
		hasChanges = true
		ui.Warn(fmt.Sprintf("Found %d orphaned branch(es) in config:", len(orphaned)))
		for _, name := range orphaned {
			fmt.Fprintf(os.Stderr, "  • %s (no longer exists in git)\n", name)
		}

		if !dryRun {
			if autoMode || ui.ConfirmTUI("Remove these branches from config?") {
				if err := mgr.RemoveOrphanedBranches(orphaned); err != nil {
					return fmt.Errorf("failed to remove orphaned branches: %w", err)
				}
				result.RemovedBranches = orphaned
				ui.Success(fmt.Sprintf("Removed %d orphaned branch(es)", len(orphaned)))
			}
		}
	}

	// Step 2: Detect untracked worktrees
	untracked, err := mgr.GetUnregisteredWorktrees()
	if err != nil {
		return fmt.Errorf("failed to get unregistered worktrees: %w", err)
	}

	if len(untracked) > 0 {
		hasChanges = true
		ui.Info(fmt.Sprintf("Found %d untracked worktree(s):", len(untracked)))
		for _, wt := range untracked {
			fmt.Fprintf(os.Stderr, "  • %s (%s)\n", wt.Branch, wt.Path)
		}

		if !dryRun {
			for _, wt := range untracked {
				added, err := addUntrackedWorktree(mgr, g, wt, baseBranch, autoMode)
				if err != nil {
					ui.Warn(fmt.Sprintf("Failed to add %s: %v", wt.Branch, err))
					continue
				}
				if added != nil {
					result.AddedBranches = append(result.AddedBranches, added)
				}
			}
		}
	}

	// Summary
	if dryRun {
		if hasChanges {
			ui.Info("Dry run complete. Use 'ezs update' to apply changes.")
		} else {
			ui.Success("Config is in sync with git. No changes needed.")
		}
		return nil
	}

	if !hasChanges {
		ui.Success("Config is already in sync with git. No changes needed.")
		return nil
	}

	// Print summary
	fmt.Fprintln(os.Stderr)
	ui.Success("Update complete!")
	if len(result.RemovedBranches) > 0 {
		fmt.Fprintf(os.Stderr, "  • Removed %d orphaned branch(es)\n", len(result.RemovedBranches))
	}
	if len(result.AddedBranches) > 0 {
		fmt.Fprintf(os.Stderr, "  • Added %d worktree(s) to stacks\n", len(result.AddedBranches))
	}
	if len(result.ReparentedBranches) > 0 {
		fmt.Fprintf(os.Stderr, "  • Updated %d parent relationship(s)\n", len(result.ReparentedBranches))
	}

	return nil
}

// addUntrackedWorktree adds an untracked worktree to a stack
func addUntrackedWorktree(mgr *stack.Manager, g *git.Git, wt git.Worktree, baseBranch string, autoMode bool) (*config.Branch, error) {
	var parentName string
	var err error

	if autoMode {
		// In auto mode, default to base branch
		parentName = baseBranch
		ui.Info(fmt.Sprintf("Adding '%s' with parent '%s' (auto mode)", wt.Branch, parentName))
	} else {
		// Always ask user to select parent
		parentName, err = selectParentForWorktree(mgr, wt.Branch, baseBranch)
		if err != nil {
			return nil, err
		}

		if !ui.ConfirmTUI(fmt.Sprintf("Add '%s' to stack with parent '%s'?", wt.Branch, parentName)) {
			return nil, nil
		}
	}

	branch, err := mgr.AddWorktreeToStack(wt.Branch, wt.Path, parentName)
	if err != nil {
		return nil, err
	}

	ui.Success(fmt.Sprintf("Added '%s' to stack with parent '%s'", wt.Branch, parentName))
	return branch, nil
}

// selectParentForWorktree shows a selection UI for choosing the parent of an untracked worktree
func selectParentForWorktree(mgr *stack.Manager, branchName, baseBranch string) (string, error) {
	allBranches := mgr.GetAllBranchesInAllStacks()
	stacks := mgr.ListStacks()

	var options []string
	var parentNames []string

	// Add base branch first (default)
	options = append(options, fmt.Sprintf("%s (base branch)", baseBranch))
	parentNames = append(parentNames, baseBranch)

	// Add other branches from stacks, excluding the current branch
	for _, b := range allBranches {
		if b.Name == branchName || b.IsMerged {
			continue
		}

		stackName := ""
		for _, s := range stacks {
			for _, sb := range s.Branches {
				if sb.Name == b.Name {
					stackName = s.Name
					break
				}
			}
		}

		options = append(options, fmt.Sprintf("%s (%s %s) [stack: %s]", b.Name, ui.IconArrow, b.Parent, stackName))
		parentNames = append(parentNames, b.Name)
	}

	if len(options) == 0 {
		return baseBranch, nil
	}

	ui.Info(fmt.Sprintf("Select parent for '%s':", branchName))
	selected, err := ui.SelectOption(options, "Select parent branch")
	if err != nil {
		return "", err
	}

	return parentNames[selected], nil
}
