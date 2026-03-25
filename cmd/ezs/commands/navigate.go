package commands

import (
	"fmt"
	"os"
	"strconv"

	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
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

// navigate handles the shared logic for up/down navigation
func navigate(direction string, steps int) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	currentStack, branch, err := mgr.GetCurrentStack()
	if err != nil {
		return ui.NewExitError(ui.ExitNotInStack, "not in a stack. Navigation requires being on a stacked branch")
	}

	targetBranch := branch
	for i := 0; i < steps; i++ {
		if direction == "up" {
			// Navigate toward parent
			if targetBranch.Parent == currentStack.Root {
				if i > 0 {
					ui.Info(fmt.Sprintf("Reached stack root after %d step(s)", i))
				}
				mainPath := getMainWorktreePath(g)
				if mainPath != "" {
					ui.Info(fmt.Sprintf("Navigating to %s (%s)", currentStack.Root, mainPath))
					EmitCd(mainPath)
				} else {
					if err := g.CheckoutBranch(currentStack.Root); err != nil {
						ui.Warn(fmt.Sprintf("Failed to switch to %s: %v", currentStack.Root, err))
					} else {
						ui.Success(fmt.Sprintf("Switched to branch '%s'", currentStack.Root))
					}
				}
				return nil
			}
			parentBranch := mgr.GetBranch(targetBranch.Parent)
			if parentBranch == nil {
				return fmt.Errorf("parent branch '%s' not found in stack", targetBranch.Parent)
			}
			targetBranch = parentBranch
		} else {
			// Navigate toward child
			children := mgr.GetChildren(targetBranch.Name)
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
	if targetBranch.WorktreePath != "" {
		EmitCd(targetBranch.WorktreePath)
	} else {
		// No worktree — fall back to git checkout
		if err := g.CheckoutBranch(targetBranch.Name); err != nil {
			return fmt.Errorf("failed to switch to branch '%s': %w", targetBranch.Name, err)
		}
		ui.Success(fmt.Sprintf("Switched to branch '%s'", targetBranch.Name))
	}

	return nil
}
