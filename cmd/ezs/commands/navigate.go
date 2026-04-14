package commands

import (
	"fmt"
	"os"
	"strconv"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// Up navigates to the parent branch in the stack
func Up(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintf(os.Stderr, `%sNavigate up the stack (toward parent/base)%s

%sUSAGE%s
    ezs up [n]

%sDESCRIPTION%s
    Moves to the parent branch. Specify a number to move multiple
    levels up (e.g., 'ezs up 2' moves to grandparent).
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
		return nil
	}

	steps := 1
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 {
			return fmt.Errorf("invalid step count: %s. Must be a positive integer", args[0])
		}
		steps = n
	}

	return navigate("up", steps)
}

// Down navigates to a child branch in the stack
func Down(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintf(os.Stderr, `%sNavigate down the stack (toward children/leaves)%s

%sUSAGE%s
    ezs down [n]

%sDESCRIPTION%s
    Moves to a child branch. If there are multiple children, shows a
    selector. Specify a number to move multiple levels down.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
		return nil
	}

	steps := 1
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 {
			return fmt.Errorf("invalid step count: %s. Must be a positive integer", args[0])
		}
		steps = n
	}

	return navigate("down", steps)
}

// navigate handles the shared logic for up/down navigation.
// Navigation follows the original tree structure (BaseBranch) so that
// merged branches are still traversable. Up stops at the top of the
// stack (does NOT go to main/root).
func navigate(direction string, steps int) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)
	mgr, err := stack.NewReadOnlyManager(cwd)
	if err != nil {
		return err
	}

	_, branch, err := mgr.GetCurrentStack()
	if err != nil {
		return ui.NewExitError(ui.ExitNotInStack, "not in a stack. Navigation requires being on a stacked branch")
	}

	targetBranch := branch
	for i := 0; i < steps; i++ {
		if direction == "up" {
			// Navigate toward the tree parent (BaseBranch), not the effective parent.
			// BaseBranch is the original parent in the tree hierarchy.
			treePar := mgr.GetBranch(targetBranch.BaseBranch)
			if treePar == nil {
				// BaseBranch is not in the stack (it's the root) — stop here
				if i == 0 {
					ui.Info("Already at the top of the stack")
				} else {
					ui.Info(fmt.Sprintf("Reached top of the stack after %d step(s)", i))
				}
				break
			}
			targetBranch = treePar
		} else {
			// Navigate toward tree children (branches whose BaseBranch == current).
			// Skip merged branches whose worktrees are deleted.
			allChildren := mgr.GetTreeChildren(targetBranch.Name)
			var children []*config.Branch
			for _, c := range allChildren {
				if !c.IsMerged {
					children = append(children, c)
				}
			}
			if len(children) == 0 {
				if i == 0 {
					ui.Info("No child branches. Already at stack leaf")
				} else {
					ui.Info(fmt.Sprintf("Reached stack leaf after %d step(s)", i))
				}
				break
			}
			if len(children) == 1 {
				targetBranch = children[0]
			} else {
				// Multiple children — ask user to choose
				var options []string
				for _, c := range children {
					options = append(options, c.Name)
				}
				selected, err := ui.SelectOption(options, fmt.Sprintf("Multiple children of '%s'. Choose:", targetBranch.Name))
				if err != nil {
					return err
				}
				targetBranch = children[selected]
			}
		}
	}

	if targetBranch.Name == branch.Name {
		return nil // No movement
	}

	// Navigate to the target
	return NavigateToBranch(g, targetBranch.Name, targetBranch.WorktreePath)
}
