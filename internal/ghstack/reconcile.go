package ghstack

import (
	"fmt"
	"sort"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

// LeafPaths enumerates every root->leaf path through an ezstack BranchTree,
// each returned bottom->top as an ordered list of branch names (the stack's
// Root/trunk is NOT included — it is passed separately as gh-stack's --base).
//
// This is the heart of the "tree overlay": ezstack supports tree-shaped stacks
// (a branch may have several children), but gh-stack only models linear chains.
// Each leaf path becomes one linear gh-stack stack. A branch shared by several
// paths (e.g. l1 in l1->l2a and l1->l2b) appears in each path, mirroring how a
// shared ancestor participates in multiple gh-stack stacks.
//
// Keys at each level are visited in sorted order so the projection is
// deterministic across runs (important for idempotent reconciliation and for
// stable tests). The returned slices never alias the recursion buffer.
func LeafPaths(tree config.BranchTree) [][]string {
	var paths [][]string
	var walk func(prefix []string, t config.BranchTree)
	walk = func(prefix []string, t config.BranchTree) {
		if len(t) == 0 {
			if len(prefix) > 0 {
				// Copy so later appends can't mutate an emitted path.
				p := make([]string, len(prefix))
				copy(p, prefix)
				paths = append(paths, p)
			}
			return
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			// Fresh backing array per child to avoid append aliasing across siblings.
			next := make([]string, len(prefix)+1)
			copy(next, prefix)
			next[len(prefix)] = k
			walk(next, t[k])
		}
	}
	walk(nil, tree)
	return paths
}

// Reconcile projects an ezstack stack into gh-stack's model so gh-stack can
// execute rebase/sync/push/submit against branches ezstack owns. It processes
// branches in topological (parent-before-child) order and, for each branch not
// yet tracked by gh-stack, either starts a new stack at the trunk
// (`gh stack init <trunk> <branch>`) or extends its parent's stack
// (`gh stack add <branch>`, which adopts the already-existing branch). Returns
// the number of branches newly registered.
//
// Both `init` and `add` check out branches, colliding with ezstack's per-branch
// worktrees, so the whole projection runs inside WithDetachedWorktrees and the
// driver worktree is restored to its original branch before the worktrees
// re-attach. Idempotent: already-tracked branches are skipped, so calling after
// every `ezs new` is cheap and safe.
//
// gh-stack enforces one-branch-one-stack: a branch cannot belong to two stacks.
// True tree branching (a parent with two tracked children) therefore cannot be
// fully represented — the second child's `add` extends the same linear stack.
// This is a gh-stack limitation, surfaced here rather than silently mis-modeled;
// linear stacks (the common case) reconcile exactly.
func Reconcile(repoDir string, stack *config.Stack) (int, error) {
	if stack == nil {
		return 0, nil
	}
	c := New(repoDir)
	st, err := c.ReadState()
	if err != nil {
		return 0, fmt.Errorf("ghstack reconcile: read state: %w", err)
	}
	tracked := map[string]bool{}
	if st != nil {
		for _, s := range st.Stacks {
			for _, n := range s.BranchNames() {
				tracked[n] = true
			}
		}
	}

	ordered := treeOrder(stack.Tree, stack.Root)
	var pending []treeNode
	protectSet := map[string]bool{}
	var protect []string
	addProtect := func(name string) {
		if name == "" || name == stack.Root || protectSet[name] {
			return
		}
		protectSet[name] = true
		protect = append(protect, name)
	}
	for _, b := range ordered {
		if tracked[b.name] {
			continue
		}
		pending = append(pending, b)
		addProtect(b.name)
		addProtect(b.parent) // we check the parent out in the driver to `add` onto it
	}
	if len(pending) == 0 {
		return 0, nil
	}

	// init/add leave the driver (main) worktree on the branch they touched; that
	// branch's own worktree is re-attached by WithDetachedWorktrees on fn return,
	// so restore the driver to its original branch first to avoid a re-collision.
	driverBranch, _ := gitAt(repoDir, "rev-parse", "--abbrev-ref", "HEAD")

	created := 0
	err = WithDetachedWorktrees(repoDir, protect, func() error {
		if driverBranch != "" && driverBranch != "HEAD" {
			defer func() { _, _ = gitAt(repoDir, "checkout", driverBranch) }()
		}
		for _, b := range pending {
			parent := b.parent
			if parent == "" || parent == stack.Root || !tracked[parent] {
				// Start a new stack at the trunk with just this branch.
				if ierr := c.Init(stack.Root, b.name); ierr != nil {
					return fmt.Errorf("ghstack reconcile: init %q on base %q: %w", b.name, stack.Root, ierr)
				}
			} else {
				// Extend the parent's existing stack: check the parent out so it
				// is the current stack, then adopt this branch on top of it.
				if _, cerr := gitAt(repoDir, "checkout", parent); cerr != nil {
					return fmt.Errorf("ghstack reconcile: checkout parent %q: %w", parent, cerr)
				}
				if aerr := c.Add(b.name); aerr != nil {
					return fmt.Errorf("ghstack reconcile: add %q onto %q: %w", b.name, parent, aerr)
				}
			}
			tracked[b.name] = true
			created++
		}
		return nil
	})
	return created, err
}

// treeNode is one branch in a stack tree paired with its tree parent (the
// stack Root for top-level branches).
type treeNode struct {
	name   string
	parent string
}

// treeOrder flattens a BranchTree into parent-before-child order, pairing each
// branch with its tree parent (root for top-level entries). Keys at each level
// are visited sorted so the projection is deterministic.
func treeOrder(tree config.BranchTree, root string) []treeNode {
	var out []treeNode
	var walk func(parent string, t config.BranchTree)
	walk = func(parent string, t config.BranchTree) {
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, treeNode{name: k, parent: parent})
			walk(k, t[k])
		}
	}
	walk(root, tree)
	return out
}
