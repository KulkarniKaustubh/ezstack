package commands

import (
	"fmt"
	"os"
	"sort"
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
//
// Navigation follows the *effective* tree (skipping merged ancestors), not
// the literal BaseBranch tree. A merged branch has had both its worktree
// and its local git branch deleted by MarkBranchMerged, so trying to
// `cd` or `git checkout` it always fails — landing on one would leave the
// user with an error mid-traversal. Walking via Branch.Parent (the nearest
// non-merged ancestor, computed in walkTree) and Manager.GetChildren
// (effective-parent children, excluding merged) keeps navigation in sync
// with how `goto` and `sync` already treat merged branches: as gone.
//
// Up stops at the top of the stack (does NOT go to main/root).
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
			effPar := effectiveParentForNavigation(mgr, targetBranch)
			if effPar == nil {
				if i == 0 {
					ui.Info("Already at the top of the stack")
				} else {
					ui.Info(fmt.Sprintf("Reached top of the stack after %d step(s)", i))
				}
				break
			}
			targetBranch = effPar
		} else {
			children := effectiveChildrenForNavigation(mgr, targetBranch.Name)
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

	// Defensive guard: if for any reason we land on a merged branch
	// (shouldn't happen with the effective-tree walk above, but the cost
	// of being wrong is a confusing `git checkout` failure), surface a
	// clear error matching `goto`'s behavior.
	if targetBranch.IsMerged {
		return fmt.Errorf("branch '%s' has been merged and its worktree was deleted", targetBranch.Name)
	}

	return NavigateToBranch(g, targetBranch.Name, targetBranch.WorktreePath)
}

// effectiveParentForNavigation returns the branch `up` should land on, or nil
// if currentBranch sits at the top of its stack. It walks via Branch.Parent
// (the nearest non-merged ancestor populated by walkTree) so merged ancestors
// are seamlessly skipped — landing on one would attempt a `git checkout` of a
// branch MarkBranchMerged already deleted. Pure function (no I/O), exported
// within the package so navigate_test.go can pin its behavior with a real
// stack.Manager fixture.
func effectiveParentForNavigation(mgr *stack.Manager, currentBranch *config.Branch) *config.Branch {
	return mgr.GetBranch(currentBranch.Parent)
}

// effectiveChildrenForNavigation returns the non-merged candidates `down`
// should choose between, sorted alphabetically for determinism. Walks via
// Manager.GetChildren (effective parent) so a merged intermediate is bridged
// out: A → B(merged) → C surfaces C as a direct candidate of A. The IsMerged
// filter is still required because a merged direct child of A keeps
// Parent=A until walkTree re-points its descendants past it.
func effectiveChildrenForNavigation(mgr *stack.Manager, branchName string) []*config.Branch {
	var out []*config.Branch
	for _, c := range mgr.GetChildren(branchName) {
		if !c.IsMerged {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
