package commands

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// Up navigates to the parent branch in the stack
func Up(args []string) error {
	if isNavigateHelpOnly(args) {
		fmt.Fprintf(os.Stderr, `%sNavigate up the stack (toward parent/base)%s

%sUSAGE%s
    ezs up [n]

%sDESCRIPTION%s
    Moves to the parent branch. Specify a number to move multiple
    levels up (e.g., 'ezs up 2' moves to grandparent).
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
		return nil
	}

	steps, err := parseNavigateArgs("up", args)
	if err != nil {
		return err
	}
	return navigate("up", steps)
}

// Down navigates to a child branch in the stack
func Down(args []string) error {
	if isNavigateHelpOnly(args) {
		fmt.Fprintf(os.Stderr, `%sNavigate down the stack (toward children/leaves)%s

%sUSAGE%s
    ezs down [n]

%sDESCRIPTION%s
    Moves to a child branch. If there are multiple children, shows a
    selector. Specify a number to move multiple levels down.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
		return nil
	}

	steps, err := parseNavigateArgs("down", args)
	if err != nil {
		return err
	}
	return navigate("down", steps)
}

// isNavigateHelpOnly returns true iff args is exactly one of `-h` or `--help`.
// We require an exact match so `ezs up --help garbage` falls through to
// parseNavigateArgs and gets rejected — matching the strict-flag philosophy
// of the rest of this PR. The previous check looked only at args[0], which
// let `--help <junk>` silently print help and exit 0.
func isNavigateHelpOnly(args []string) bool {
	return len(args) == 1 && (args[0] == "-h" || args[0] == "--help")
}

// parseNavigateArgs validates that args contains at most a single positive
// integer step count and returns it (defaulting to 1). Any flag-shaped arg or
// extra positional arg is rejected so users learn about typos immediately
// instead of silently no-oping.
func parseNavigateArgs(direction string, args []string) (int, error) {
	if len(args) == 0 {
		return 1, nil
	}
	if len(args) > 1 {
		return 0, fmt.Errorf("ezs %s takes at most one argument (step count); got %d", direction, len(args))
	}
	a := args[0]
	// Try the integer parse first so "-1" gets the more specific
	// "invalid step count" message rather than the misleading
	// "unknown flag: -1". Any non-integer that starts with "-" is
	// almost certainly a typoed flag, so we surface it as such.
	if n, err := strconv.Atoi(a); err == nil {
		if n < 1 {
			return 0, fmt.Errorf("invalid step count: %s. Must be a positive integer", a)
		}
		return n, nil
	}
	if strings.HasPrefix(a, "-") {
		return 0, fmt.Errorf("unknown flag: %s", a)
	}
	return 0, fmt.Errorf("invalid step count: %s. Must be a positive integer", a)
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
