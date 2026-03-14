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
    Syncs ezstack config with the actual state of git worktrees:

    • Detects renamed branches (git branch -m) and updates config
    • Removes branches from config if their worktree folder was deleted
    • Removes branches from config if the git branch no longer exists

    After running update, all ezs commands will work correctly.

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

	var result stack.UpdateResult
	hasChanges := false

	// Step 1: Prune stale git worktrees first (cleans up after manual rm)
	g.PruneWorktrees()

	// Step 2: Remove branches from config whose worktrees no longer exist
	missingWorktrees := mgr.DetectMissingWorktrees()
	if len(missingWorktrees) > 0 {
		hasChanges = true
		for _, info := range missingWorktrees {
			ui.Info(fmt.Sprintf("Removing '%s' from config (worktree deleted: %s)", info.BranchName, info.WorktreePath))
			result.RemovedBranches = append(result.RemovedBranches, info.BranchName)
		}
		if !dryRun {
			if err := mgr.HandleMissingWorktrees(missingWorktrees); err != nil {
				return fmt.Errorf("failed to clean up missing worktrees: %w", err)
			}
		}
	}

	// Step 3: Detect orphaned branches and untracked worktrees
	orphaned := mgr.DetectOrphanedBranches()
	untracked, err := mgr.GetUnregisteredWorktrees()
	if err != nil {
		return fmt.Errorf("failed to get unregistered worktrees: %w", err)
	}

	// Step 4: Detect renames (orphaned branch + untracked worktree at same path)
	renames := mgr.DetectRenamedBranches(orphaned, untracked)
	if len(renames) > 0 {
		hasChanges = true

		// Build set of renamed old names to exclude from orphan processing
		renamedOld := make(map[string]bool)
		for _, r := range renames {
			renamedOld[r.OldName] = true
		}

		for _, r := range renames {
			ui.Info(fmt.Sprintf("Detected rename: '%s' → '%s' (worktree: %s)", r.OldName, r.NewName, r.WorktreePath))
		}

		if !dryRun {
			if autoMode || ui.ConfirmTUI("Apply detected renames?") {
				if err := mgr.ApplyBranchRenames(renames); err != nil {
					return fmt.Errorf("failed to apply renames: %w", err)
				}
				for _, r := range renames {
					ui.Success(fmt.Sprintf("Renamed '%s' → '%s'", r.OldName, r.NewName))
				}
				result.RenamedBranches = append(result.RenamedBranches, renames...)
			}
		}

		// Filter out renamed branches from orphaned list
		var remainingOrphaned []string
		for _, name := range orphaned {
			if !renamedOld[name] {
				remainingOrphaned = append(remainingOrphaned, name)
			}
		}
		orphaned = remainingOrphaned
	}

	// Step 5: Remove remaining orphaned branches from config
	if len(orphaned) > 0 {
		hasChanges = true
		for _, name := range orphaned {
			ui.Info(fmt.Sprintf("Removing '%s' from config (branch deleted from git)", name))
			result.RemovedBranches = append(result.RemovedBranches, name)
		}
		if !dryRun {
			if err := mgr.RemoveOrphanedBranches(orphaned); err != nil {
				return fmt.Errorf("failed to remove orphaned branches: %w", err)
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
	if len(result.RenamedBranches) > 0 {
		fmt.Fprintf(os.Stderr, "  • Renamed %d branch(es)\n", len(result.RenamedBranches))
	}
	if len(result.RemovedBranches) > 0 {
		fmt.Fprintf(os.Stderr, "  • Removed %d orphaned branch(es)\n", len(result.RemovedBranches))
	}
	if len(result.ReparentedBranches) > 0 {
		fmt.Fprintf(os.Stderr, "  • Updated %d parent relationship(s)\n", len(result.ReparentedBranches))
	}

	return nil
}

