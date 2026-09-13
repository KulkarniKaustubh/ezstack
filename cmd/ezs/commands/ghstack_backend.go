package commands

import (
	"fmt"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ghstack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// ghStackEnabled reports whether stack operations for the repo containing g
// should be delegated to the `gh stack` backend. It is true only when the
// per-repo use_gh_stack config is set AND the extension is actually installed —
// so a stale config can never wedge a repo whose gh-stack was uninstalled.
func ghStackEnabled(g *git.Git) bool {
	cfg, err := config.Load()
	if err != nil {
		return false
	}
	repoPath := getMainWorktreePath(g)
	if !cfg.GetUseGHStack(repoPath) {
		return false
	}
	if !ghstack.Installed() {
		ui.Warn("use_gh_stack is set but the `gh stack` extension is not installed; falling back to the built-in engine")
		return false
	}
	return true
}

// syncViaGHStackDispatch resolves which stack(s) a whole-stack `ezs sync`
// targets (default/-s = current, -a = all, positional hash = that stack) and
// cascades each through gh-stack, pushing after rebase. Specialized sync modes
// (-c, -b, -p, -C, --squash, --merge, --dry-run, --continue) are NOT routed
// here — the caller gates on that and lets the built-in engine handle them, so
// no existing behavior is lost.
func syncViaGHStackDispatch(g *git.Git, mgr *stack.Manager, all bool, hashArgs []string) error {
	repoPath := getMainWorktreePath(g)
	var stacks []*config.Stack
	switch {
	case all:
		stacks = mgr.ListStacks()
	case len(hashArgs) > 0:
		s, err := mgr.GetStackByHash(hashArgs[0])
		if err != nil {
			return err
		}
		stacks = []*config.Stack{s}
	default:
		cur, _, err := mgr.GetCurrentStack()
		if err != nil {
			return err
		}
		stacks = []*config.Stack{cur}
	}
	if len(stacks) == 0 {
		ui.Info("No stacks to sync")
		return nil
	}
	for _, s := range stacks {
		ui.Info(fmt.Sprintf("Syncing stack %s via gh stack", s.DisplayName()))
		if err := syncStackViaGHStack(g, repoPath, s, true); err != nil {
			return err
		}
	}
	ui.Success("Sync complete (gh stack backend)")
	return nil
}

// reconcileGHStackForBranch projects the stack that branchName belongs to into
// gh-stack, so a freshly created branch (via `ezs new`) is immediately tracked
// by the backend. Best-effort: a reconcile failure is surfaced as a warning but
// never blocks branch creation — ezstack's own model is still authoritative and
// a later sync will reconcile again.
func reconcileGHStackForBranch(g *git.Git, mgr *stack.Manager, branchName string) {
	if !ghStackEnabled(g) {
		return
	}
	s := mgr.GetStackForBranch(branchName)
	if s == nil {
		return
	}
	repoPath := getMainWorktreePath(g)
	if _, err := ghstack.Reconcile(repoPath, s); err != nil {
		ui.Warn(fmt.Sprintf("gh stack: could not register '%s': %v", branchName, err))
	}
}

// stackBranchNames returns every branch name in a stack (all tree paths),
// used as the set to protect from the worktree-checkout collision.
func stackBranchNames(s *config.Stack) []string {
	names := make([]string, 0, len(s.Branches))
	for _, b := range s.Branches {
		names = append(names, b.Name)
	}
	return names
}

// syncStackViaGHStack cascades a rebase (and optional push) for one ezstack
// stack using gh-stack as the engine, while preserving ezstack's
// worktree-per-branch layout.
//
// Flow: project the tree into gh-stack (Reconcile) -> detach the per-branch
// worktrees that would collide with gh-stack's checkouts -> for each linear
// leaf path, check the leaf out in the main worktree so gh-stack targets that
// stack, rebase, optionally push -> re-attach every worktree (handled by
// WithDetachedWorktrees) and restore the main worktree's original branch.
func syncStackViaGHStack(g *git.Git, repoPath string, s *config.Stack, doPush bool) error {
	ghc := ghstack.New(repoPath)
	if _, err := ghstack.Reconcile(repoPath, s); err != nil {
		return err
	}
	paths := ghstack.LeafPaths(s.Tree)
	if len(paths) == 0 {
		ui.Info("Stack has no branches to sync")
		return nil
	}

	repoGit := git.New(repoPath)
	originalBranch, _ := repoGit.CurrentBranch() // best-effort restore target

	return ghstack.WithDetachedWorktrees(repoPath, stackBranchNames(s), func() error {
		// gh-stack leaves the driver worktree on the last branch it touched.
		// Restore it BEFORE WithDetachedWorktrees re-attaches the per-branch
		// worktrees (which happens on this fn's return) so the two can't collide.
		if originalBranch != "" {
			defer func() { _ = repoGit.CheckoutBranch(originalBranch) }()
		}
		for _, p := range paths {
			top := p[len(p)-1]
			if err := repoGit.CheckoutBranch(top); err != nil {
				return fmt.Errorf("checkout %s in main worktree: %w", top, err)
			}
			ui.Info(fmt.Sprintf("gh stack: rebasing %s", top))
			if err := ghc.Rebase(ghstack.RebaseOptions{}); err != nil {
				return fmt.Errorf("gh stack rebase (%s): %w", top, err)
			}
			if doPush {
				ui.Info(fmt.Sprintf("gh stack: pushing %s", top))
				if err := ghc.Push(); err != nil {
					return fmt.Errorf("gh stack push (%s): %w", top, err)
				}
			}
		}
		return nil
	})
}

// pushStackViaGHStack pushes one ezstack stack's branches through gh-stack's
// atomic force-with-lease push, using the same worktree choreography as sync.
func pushStackViaGHStack(g *git.Git, repoPath string, s *config.Stack) error {
	ghc := ghstack.New(repoPath)
	if _, err := ghstack.Reconcile(repoPath, s); err != nil {
		return err
	}
	paths := ghstack.LeafPaths(s.Tree)
	if len(paths) == 0 {
		return nil
	}
	repoGit := git.New(repoPath)
	originalBranch, _ := repoGit.CurrentBranch()

	return ghstack.WithDetachedWorktrees(repoPath, stackBranchNames(s), func() error {
		if originalBranch != "" {
			defer func() { _ = repoGit.CheckoutBranch(originalBranch) }()
		}
		for _, p := range paths {
			top := p[len(p)-1]
			if err := repoGit.CheckoutBranch(top); err != nil {
				return fmt.Errorf("checkout %s in main worktree: %w", top, err)
			}
			if err := ghc.Push(); err != nil {
				return fmt.Errorf("gh stack push (%s): %w", top, err)
			}
		}
		return nil
	})
}
