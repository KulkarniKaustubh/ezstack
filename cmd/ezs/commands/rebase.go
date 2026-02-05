package commands

import (
	"flag"
	"fmt"
	"os"

	"github.com/ezstack/ezstack/internal/stack"
	"github.com/ezstack/ezstack/internal/ui"
)

// Rebase handles rebase operations
func Rebase(args []string) error {
	fs := flag.NewFlagSet("rebase", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sRebase branches in the stack%s

%sUSAGE%s
    ezs rebase [options]

%sOPTIONS%s
    -a, --all         Rebase all branches in the stack
    -c, --children    Rebase child branches after updating current
    -h, --help        Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	// Long flags
	all := fs.Bool("all", false, "Rebase all branches in the stack")
	children := fs.Bool("children", false, "Rebase child branches after updating current")
	// Short flags
	allShort := fs.Bool("a", false, "Rebase all (short)")
	childrenShort := fs.Bool("c", false, "Rebase children (short)")
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
	if *allShort {
		*all = true
	}
	if *childrenShort {
		*children = true
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	if *all {
		ui.Info("This will rebase all branches in the stack")
		if !ui.ConfirmTUI("Rebase entire stack") {
			ui.Warn("Cancelled")
			return nil
		}
		ui.Info("Rebasing entire stack...")
		results, err := mgr.RebaseStack(false)
		if err != nil {
			return err
		}
		printRebaseResults(results)
		return nil
	}

	if *children {
		ui.Info("This will rebase all child branches of the current branch")
		if !ui.ConfirmTUI("Rebase child branches") {
			ui.Warn("Cancelled")
			return nil
		}
		ui.Info("Rebasing child branches...")
		if err := mgr.RebaseChildren(); err != nil {
			return err
		}
		ui.Success("Child branches rebased successfully")
		return nil
	}

	// Interactive mode - show menu
	return rebaseInteractive(mgr)
}

// rebaseInteractive shows an interactive menu for rebase operations
func rebaseInteractive(mgr *stack.Manager) error {
	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		return fmt.Errorf("not in a stack. Create a branch first with: ezs new <branch-name>")
	}

	// Show current stack status
	ui.PrintStack(currentStack, branch.Name)

	// Build options based on current state
	options := []string{}
	optionActions := []string{}

	// Option 1: Rebase current branch onto parent
	if branch.Parent != "" && !mgr.IsMainBranch(branch.Parent) {
		options = append(options, fmt.Sprintf("%s  Rebase current branch onto parent (%s)", ui.IconUp, branch.Parent))
		optionActions = append(optionActions, "parent")
	}

	// Option 2: Rebase child branches
	children := mgr.GetChildren(branch.Name)
	if len(children) > 0 {
		childNames := ""
		for i, c := range children {
			if i > 0 {
				childNames += ", "
			}
			childNames += c.Name
		}
		options = append(options, fmt.Sprintf("%s  Rebase %d child branch(es) onto current (%s)", ui.IconDown, len(children), childNames))
		optionActions = append(optionActions, "children")
	}

	// Option 3: Rebase entire stack
	if len(currentStack.Branches) > 1 {
		options = append(options, fmt.Sprintf("%s  Rebase entire stack (%d branches)", ui.IconSync, len(currentStack.Branches)))
		optionActions = append(optionActions, "all")
	}

	if len(options) == 0 {
		ui.Info("No rebase operations available for current branch")
		return nil
	}

	selected, err := ui.SelectOptionWithBack(options, "What would you like to rebase?")
	if err != nil {
		if err == ui.ErrBack {
			return ui.ErrBack
		}
		return err
	}

	action := optionActions[selected]
	switch action {
	case "parent":
		if !ui.ConfirmTUI(fmt.Sprintf("Rebase %s onto %s", branch.Name, branch.Parent)) {
			ui.Warn("Cancelled")
			return nil
		}
		ui.Info("Rebasing current branch onto parent...")
		if err := mgr.RebaseOnParent(); err != nil {
			return err
		}
		ui.Success("Rebase complete")
	case "children":
		if !ui.ConfirmTUI("Rebase child branches onto current") {
			ui.Warn("Cancelled")
			return nil
		}
		ui.Info("Rebasing child branches...")
		if err := mgr.RebaseChildren(); err != nil {
			return err
		}
		ui.Success("Child branches rebased successfully")
	case "all":
		if !ui.ConfirmTUI("Rebase entire stack") {
			ui.Warn("Cancelled")
			return nil
		}
		ui.Info("Rebasing entire stack...")
		results, err := mgr.RebaseStack(false)
		if err != nil {
			return err
		}
		printRebaseResults(results)
	}

	return nil
}

// Sync syncs the stack when parent branches are merged
func Sync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sSync stack when parent branches are merged%s

%sUSAGE%s
    ezs sync [options]

%sOPTIONS%s
    -h, --help    Show this help message

%sNOTES%s
    This command detects when parent branches have been merged to main
    and automatically updates the base branches and rebases accordingly.
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

	// Show current stack
	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		return fmt.Errorf("not in a stack. Create a branch first with: ezs new <branch-name>")
	}

	ui.PrintStack(currentStack, branch.Name)

	// Check for merged branches
	ui.Info("Fetching latest changes...")
	mergedBranches, err := mgr.DetectMergedParents()
	if err != nil {
		ui.Warn(fmt.Sprintf("Could not check for merged branches: %v", err))
	}

	if len(mergedBranches) == 0 {
		ui.Success("All parent branches are up to date. No sync needed.")
		return nil
	}

	// Show what will be synced
	ui.Info(fmt.Sprintf("Found %d branch(es) with merged parents:", len(mergedBranches)))
	for _, mb := range mergedBranches {
		fmt.Fprintf(os.Stderr, "  %s %s%s%s: parent %s%s%s was merged to main\n",
			ui.IconBullet, ui.Bold, mb.Branch, ui.Reset, ui.Yellow, mb.MergedParent, ui.Reset)
	}
	fmt.Fprintln(os.Stderr)

	if !ui.ConfirmTUI("Sync stack (update base branches and rebase)") {
		ui.Warn("Cancelled")
		return nil
	}

	ui.Info("Syncing stack...")
	if err := mgr.SyncWithMain(); err != nil {
		return err
	}

	ui.Success("Stack synced with main!")
	return nil
}

func printRebaseResults(results []stack.RebaseResult) {
	for _, r := range results {
		if r.Success {
			ui.Success(fmt.Sprintf("Rebased %s", r.Branch))
		} else if r.HasConflict {
			ui.Warn(fmt.Sprintf("Conflict in %s - resolve and continue", r.Branch))
		} else if r.Error != nil {
			ui.Error(fmt.Sprintf("Failed to rebase %s: %v", r.Branch, r.Error))
		}
	}
}
