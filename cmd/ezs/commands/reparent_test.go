package commands

import (
	"strings"
	"testing"
)

// TestIsDescendantOf_Direct verifies the trivial case: child is a descendant
// of its immediate parent. Used by reparent's cycle-detection guard.
func TestIsDescendantOf_Direct(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	mgr := reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("p", "main", "new"); err != nil {
		t.Fatalf("create p: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("c", "p", ""); err != nil {
		t.Fatalf("create c: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if !IsDescendantOf(mgr, "c", "p") {
		t.Error("c should be descendant of p")
	}
	if IsDescendantOf(mgr, "p", "c") {
		t.Error("p must not be descendant of its own child c")
	}
}

// TestIsDescendantOf_Transitive walks two hops. The current implementation
// chases parent pointers until it hits the requested ancestor or runs out
// of ancestors; the cycle guard via the visited map should never trigger
// on a well-formed tree.
func TestIsDescendantOf_Transitive(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	mgr := reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("a", "main", "new"); err != nil {
		t.Fatalf("a: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("b", "a", ""); err != nil {
		t.Fatalf("b: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("c", "b", ""); err != nil {
		t.Fatalf("c: %v", err)
	}

	mgr = reloadManager(t, repoDir)
	if !IsDescendantOf(mgr, "c", "a") {
		t.Error("c should be descendant of a (via b)")
	}
	if !IsDescendantOf(mgr, "b", "a") {
		t.Error("b should be descendant of a")
	}
	if !IsDescendantOf(mgr, "c", "main") {
		t.Error("c should be descendant of main")
	}
}

// TestIsDescendantOf_Sibling and unrelated branches must NOT report as
// descendants. Symmetric to direct/transitive cases — exercises the
// "ran out of parents" exit path.
func TestIsDescendantOf_Sibling(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	mgr := reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("left", "main", "new"); err != nil {
		t.Fatalf("left: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("right", "main", "new"); err != nil {
		t.Fatalf("right: %v", err)
	}

	mgr = reloadManager(t, repoDir)
	if IsDescendantOf(mgr, "left", "right") {
		t.Error("siblings must not be descendants of each other")
	}
	if IsDescendantOf(mgr, "right", "left") {
		t.Error("siblings must not be descendants of each other")
	}
}

// TestIsDescendantOf_NonexistentBranch must return false rather than panic
// when the queried branch isn't tracked. The implementation reads the
// branch and bails on nil — locking that behavior here so a future refactor
// that drops the nil check gets caught.
func TestIsDescendantOf_NonexistentBranch(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	mgr := reloadManager(t, repoDir)
	if IsDescendantOf(mgr, "ghost", "main") {
		t.Error("non-existent branch should not report as a descendant")
	}
}

// TestReparent_NoRebase_SameStack confirms tracking metadata flips when
// reparenting within a single stack. --no-rebase skips the actual git
// rebase so the test doesn't need real commits on each branch — we're
// validating the metadata update, which is the source of past bugs.
//
// Tree before: main → p1 → c (and a sibling p2 also rooted on main)
// Reparent c onto p2.
// Expected: c.Parent == "p2" and the old p1 stack still contains p1 alone.
func TestReparent_NoRebase_SameStack(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	mgr := reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("p1", "main", "new"); err != nil {
		t.Fatalf("p1: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("p2", "main", "new"); err != nil {
		t.Fatalf("p2: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("c", "p1", ""); err != nil {
		t.Fatalf("c: %v", err)
	}

	if err := Reparent([]string{"--no-rebase", "c", "p2"}); err != nil {
		t.Fatalf("Reparent: %v", err)
	}

	mgr = reloadManager(t, repoDir)
	got := mgr.GetBranch("c")
	if got == nil {
		t.Fatal("c missing after reparent")
	}
	if got.Parent != "p2" {
		t.Errorf("c.Parent = %q, want p2", got.Parent)
	}
}

// TestReparent_NoRebase_CrossStack is the historical panic surface from
// the cross-stack reparent fix recorded in project memory: moving a branch
// from one stack to another collapsed both stacks incorrectly. The fix
// added newParentName plumbing to moveBranchToStack so the destination
// stack got the right anchor.
//
// Setup: Stack A = main → A1, Stack B = main → B1.
// Reparent B1 onto A1.
// Expected: B1 lives in stack A (parent = A1), stack B is gone, no branch
// appears in two stacks (the duplicate-fragment failure mode).
func TestReparent_NoRebase_CrossStack(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	mgr := reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("A1", "main", "new"); err != nil {
		t.Fatalf("A1: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("B1", "main", "new"); err != nil {
		t.Fatalf("B1: %v", err)
	}

	if err := Reparent([]string{"--no-rebase", "B1", "A1"}); err != nil {
		t.Fatalf("Reparent: %v", err)
	}

	mgr = reloadManager(t, repoDir)
	if got := mgr.GetBranch("B1"); got == nil || got.Parent != "A1" {
		t.Fatalf("B1 parent = %v, want A1", got)
	}

	// Single surviving stack containing both branches.
	stacks := mgr.ListStacks()
	if len(stacks) != 1 {
		t.Fatalf("want 1 stack after cross-stack reparent, got %d", len(stacks))
	}

	// No branch may appear in more than one stack — that was the
	// fragmentation signature in issue #19 / cross-stack panic.
	seen := map[string]int{}
	for _, s := range stacks {
		for _, b := range s.Branches {
			seen[b.Name]++
		}
	}
	for name, n := range seen {
		if n > 1 {
			t.Errorf("branch %q appears in %d stacks; should appear in exactly one", name, n)
		}
	}
}

// TestReparent_RejectsCycle guards against a user (or future caller)
// reparenting a branch onto one of its own descendants, which would form a
// cycle in the tracking tree. The manager's underlying ReparentBranch is
// expected to detect this; this test pins the user-visible CLI error path.
func TestReparent_RejectsCycle(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	mgr := reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("a", "main", "new"); err != nil {
		t.Fatalf("a: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("b", "a", ""); err != nil {
		t.Fatalf("b: %v", err)
	}

	// Reparenting `a` onto `b` would close a loop a→b→a.
	err := Reparent([]string{"--no-rebase", "a", "b"})
	if err == nil {
		t.Fatal("Reparent allowed a cycle (a → b → a); want refusal")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "cycle") &&
		!strings.Contains(strings.ToLower(err.Error()), "circular") &&
		!strings.Contains(strings.ToLower(err.Error()), "descendant") {
		t.Errorf("error %q should mention cycle/circular/descendant for clarity", err.Error())
	}
}
