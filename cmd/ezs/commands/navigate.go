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
// Navigation walks the literal stack tree (via Branch.BaseBranch) and lands on
// the first branch whose worktree still exists, bridging over any branch that
// can't be reached. A branch is unreachable only when it has been merged AND
// its worktree is gone: `ezs pr merge` (MarkBranchMerged) deletes both the
// worktree and the local git ref, so cd/checkout there always fails. A merged
// branch whose worktree is still on disk — e.g. a merge that `ezs ls` detected
// from GitHub but hasn't pruned yet — is still a valid stop, so up/down keep
// working both *through* and *from* it. (The effective Branch.Parent that
// walkTree computes re-points a merged branch's children past it, which is
// exactly why `down` from such a branch used to report no children.)
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

	// Defensive guard: both selection helpers only return landable branches,
	// but if we somehow target one whose worktree is gone the cd would have
	// nowhere to land — surface a clear error matching `goto`'s behavior.
	if !landable(targetBranch) {
		return fmt.Errorf("branch '%s' has been merged and its worktree was deleted", targetBranch.Name)
	}

	return NavigateToBranch(g, targetBranch.Name, targetBranch.WorktreePath)
}

// effectiveParentForNavigation returns the branch `up` should land on, or nil
// if currentBranch sits at the top of its stack. It walks the literal stack
// tree upward (via Branch.BaseBranch) and returns the first ancestor we can
// land on, bridging over any merged ancestor whose worktree is gone. Walking
// the literal tree (rather than the effective Branch.Parent) is what lets `up`
// work *from* a merged branch whose worktree is still on disk. Exported within
// the package so navigate_merged_test.go can pin its behavior with a real
// stack.Manager fixture.
func effectiveParentForNavigation(mgr *stack.Manager, currentBranch *config.Branch) *config.Branch {
	name := currentBranch.BaseBranch
	for name != "" {
		parent := mgr.GetBranch(name)
		if parent == nil {
			// Reached the stack root (e.g. "main"), which is not tracked as a
			// Branch — currentBranch is at the top of the stack.
			return nil
		}
		if landable(parent) {
			return parent
		}
		name = parent.BaseBranch
	}
	return nil
}

// effectiveChildrenForNavigation returns the candidates `down` should choose
// between, sorted alphabetically for determinism. It walks the literal stack
// tree (via Branch.BaseBranch) and bridges over any merged child whose worktree
// is gone, surfacing the nearest landable descendant on each path: A →
// B(merged, worktree deleted) → C surfaces C as a direct candidate of A. A
// merged child whose worktree is still present is itself landable and surfaces
// directly.
func effectiveChildrenForNavigation(mgr *stack.Manager, branchName string) []*config.Branch {
	var out []*config.Branch
	var walk func(parent string)
	walk = func(parent string) {
		for _, c := range literalChildren(mgr, parent) {
			if landable(c) {
				out = append(out, c)
				continue
			}
			// Worktree-less merged intermediate: descend through it so a
			// landable grandchild still surfaces as a direct candidate.
			walk(c.Name)
		}
	}
	walk(branchName)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// literalChildren returns the branches whose literal tree parent (BaseBranch)
// is parent, across every stack. Unlike Manager.GetChildren — which uses the
// effective Branch.Parent that walkTree re-points past merged branches — this
// follows the unchanged tree structure so navigation can decide for itself
// which branches to bridge over.
func literalChildren(mgr *stack.Manager, parent string) []*config.Branch {
	var out []*config.Branch
	for _, b := range mgr.GetAllBranchesInAllStacks() {
		if b.BaseBranch == parent {
			out = append(out, b)
		}
	}
	return out
}

// landable reports whether navigation can stop on b: a branch we can actually
// cd into. A branch is unreachable only when it has been merged AND its
// worktree is gone — MarkBranchMerged (via `ezs pr merge`) clears WorktreePath
// and deletes both the worktree and the local git ref. A merged branch whose
// worktree is still on disk — e.g. a merge that `ezs ls` detected from GitHub
// but has not pruned yet — remains landable, so up/down keep working from and
// through it.
func landable(b *config.Branch) bool {
	if b == nil {
		return false
	}
	if b.IsMerged && !worktreeExists(b) {
		return false
	}
	return true
}

// worktreeExists reports whether b's worktree directory is recorded and still
// present on disk.
func worktreeExists(b *config.Branch) bool {
	if b.WorktreePath == "" {
		return false
	}
	info, err := os.Stat(b.WorktreePath)
	return err == nil && info.IsDir()
}
