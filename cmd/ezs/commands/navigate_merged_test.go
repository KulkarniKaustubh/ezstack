package commands

import (
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
)

// Tests pinning the merged-branch handling in navigate's selection helpers.
// These exercise the effective-tree walk that fixes the up/down regression
// where a merged ancestor (or a merged intermediate child) caused navigation
// to fail or stop short. See navigate.go for the fix; the bug is documented
// in the PR description.

// makeStackChain builds a single linear stack
//
//	main → branches[0] → branches[1] → ...
//
// without worktrees, then optionally marks any of them as merged. Returns a
// fresh manager loaded from disk so the in-memory branch structs reflect the
// merge state (Parent pointers re-routed through walkTree).
func makeStackChain(t *testing.T, mgr *stack.Manager, repoDir string, branches []string, merged map[string]bool) *stack.Manager {
	t.Helper()

	parent := "main"
	stackHash := "new"
	for _, b := range branches {
		if _, err := mgr.CreateBranchNoWorktree(b, parent, stackHash); err != nil {
			t.Fatalf("CreateBranchNoWorktree(%s, %s): %v", b, parent, err)
		}
		// Reload between additions: every CreateBranchNoWorktree saves and
		// the cache is rebuilt from disk, so subsequent calls need a fresh
		// view.
		mgr = reloadManager(t, repoDir)
		parent = b
		stackHash = "" // only the first branch starts a new stack
	}

	for name := range merged {
		if err := mgr.MarkBranchMerged(name); err != nil {
			t.Fatalf("MarkBranchMerged(%s): %v", name, err)
		}
		mgr = reloadManager(t, repoDir)
	}

	return mgr
}

// TestEffectiveParentForNavigation_NormalStack walks up an unmerged chain.
// The bug only manifests with merged branches, but the unmerged baseline is a
// regression gate: it would fail if we accidentally broke ordinary navigation.
func TestEffectiveParentForNavigation_NormalStack(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	mgr = makeStackChain(t, mgr, repoDir, []string{"a", "b", "c"}, nil)

	c := mgr.GetBranch("c")
	if c == nil {
		t.Fatal("branch c missing")
	}
	if got := effectiveParentForNavigation(mgr, c); got == nil || got.Name != "b" {
		t.Errorf("up from c = %v, want b", got)
	}

	b := mgr.GetBranch("b")
	if got := effectiveParentForNavigation(mgr, b); got == nil || got.Name != "a" {
		t.Errorf("up from b = %v, want a", got)
	}

	a := mgr.GetBranch("a")
	if got := effectiveParentForNavigation(mgr, a); got != nil {
		t.Errorf("up from a (top) = %v, want nil", got)
	}
}

// TestEffectiveParentForNavigation_SkipsMergedAncestor is the load-bearing
// fix for `ezs up`: previously this walked via BaseBranch, landed on the
// merged ancestor, and then `git checkout` failed because MarkBranchMerged
// deletes the local git ref. The effective Parent skips merged ancestors so
// `up` lands on a usable branch.
func TestEffectiveParentForNavigation_SkipsMergedAncestor(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	mgr = makeStackChain(t, mgr, repoDir, []string{"a", "b", "c"}, map[string]bool{"b": true})

	c := mgr.GetBranch("c")
	got := effectiveParentForNavigation(mgr, c)
	if got == nil {
		t.Fatal("up from c with merged b returned nil; expected to skip to a")
	}
	if got.Name != "a" {
		t.Errorf("up from c with merged b = %s, want a", got.Name)
	}
	if got.IsMerged {
		t.Error("navigation landed on a merged branch — would fail at checkout")
	}
}

// TestEffectiveParentForNavigation_SkipsChainOfMerged ensures the skip is
// transitive: stacked merges in a row still let `up` find the first usable
// ancestor in one step.
func TestEffectiveParentForNavigation_SkipsChainOfMerged(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	mgr = makeStackChain(t, mgr, repoDir, []string{"a", "b", "c", "d"}, map[string]bool{
		"b": true,
		"c": true,
	})

	d := mgr.GetBranch("d")
	got := effectiveParentForNavigation(mgr, d)
	if got == nil || got.Name != "a" {
		t.Errorf("up from d with merged b,c = %v, want a", got)
	}
}

// TestEffectiveParentForNavigation_AllAncestorsMerged_ReturnsNil exercises the
// edge case where every ancestor between a non-merged tip and the trunk root
// is merged. Parent walks to the stack root ("main") which is not stored as a
// Branch, so we get nil — the "Already at top of stack" message will fire,
// matching `goto`'s "merged is unreachable" stance.
func TestEffectiveParentForNavigation_AllAncestorsMerged_ReturnsNil(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	mgr = makeStackChain(t, mgr, repoDir, []string{"a", "b"}, map[string]bool{"a": true})

	b := mgr.GetBranch("b")
	if got := effectiveParentForNavigation(mgr, b); got != nil {
		t.Errorf("up from b with all ancestors merged = %v, want nil (top of stack)", got)
	}
}

// TestEffectiveChildrenForNavigation_DirectChildrenSorted verifies the
// alphabetical-sort guarantee. GetChildren walks a map and is not ordered;
// without the sort, the multi-child selector would shuffle between runs.
func TestEffectiveChildrenForNavigation_DirectChildrenSorted(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	// Build with intentionally non-alphabetical insertion order.
	if _, err := mgr.CreateBranchNoWorktree("zeta", "main", "new"); err != nil {
		t.Fatal(err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("alpha", "main", "new"); err != nil {
		t.Fatal(err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("middle", "main", "new"); err != nil {
		t.Fatal(err)
	}
	mgr = reloadManager(t, repoDir)

	// All three are stack roots, so use main as the reference. They each
	// live in their own stack (targetStackHash="new" each time), so they
	// appear as separate roots; effective children of "main" cross stacks
	// because GetChildren walks every stack — which is what we want for
	// navigation in repos with multiple parallel stacks.
	got := effectiveChildrenForNavigation(mgr, "main")
	if len(got) != 3 {
		t.Fatalf("got %d children, want 3", len(got))
	}
	want := []string{"alpha", "middle", "zeta"}
	for i, b := range got {
		if b.Name != want[i] {
			t.Errorf("children[%d] = %s, want %s", i, b.Name, want[i])
		}
	}
}

// TestEffectiveChildrenForNavigation_SkipsMergedDirectChild is the core fix
// for `ezs down`: when the only direct child is merged, walkTree re-points
// any non-merged grandchildren's Parent to the grandparent, so they surface
// as effective children.
func TestEffectiveChildrenForNavigation_SkipsMergedDirectChild(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	mgr = makeStackChain(t, mgr, repoDir, []string{"a", "b", "c"}, map[string]bool{"b": true})

	got := effectiveChildrenForNavigation(mgr, "a")
	if len(got) != 1 {
		t.Fatalf("children of a = %d, want 1 (c via merged b)", len(got))
	}
	if got[0].Name != "c" {
		t.Errorf("children of a = %s, want c", got[0].Name)
	}
	if got[0].IsMerged {
		t.Error("returned a merged branch — would fail at checkout")
	}
}

// TestEffectiveChildrenForNavigation_GrandchildrenViaMergedChain verifies the
// transitive case: A → B(merged) → C(merged) → D, `down` from A surfaces D.
func TestEffectiveChildrenForNavigation_GrandchildrenViaMergedChain(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	mgr = makeStackChain(t, mgr, repoDir, []string{"a", "b", "c", "d"}, map[string]bool{
		"b": true,
		"c": true,
	})

	got := effectiveChildrenForNavigation(mgr, "a")
	if len(got) != 1 {
		t.Fatalf("children of a = %d, want 1 (d via merged b,c)", len(got))
	}
	if got[0].Name != "d" {
		t.Errorf("children of a = %s, want d", got[0].Name)
	}
}

// TestEffectiveChildrenForNavigation_FiltersMergedSibling — when one direct
// child is merged and another is not, only the unmerged sibling shows up.
func TestEffectiveChildrenForNavigation_FiltersMergedSibling(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	if _, err := mgr.CreateBranchNoWorktree("a", "main", "new"); err != nil {
		t.Fatal(err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("b1", "a", ""); err != nil {
		t.Fatal(err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("b2", "a", ""); err != nil {
		t.Fatal(err)
	}
	mgr = reloadManager(t, repoDir)
	if err := mgr.MarkBranchMerged("b1"); err != nil {
		t.Fatal(err)
	}
	mgr = reloadManager(t, repoDir)

	got := effectiveChildrenForNavigation(mgr, "a")
	if len(got) != 1 {
		t.Fatalf("children of a = %d, want 1 (b2 only)", len(got))
	}
	if got[0].Name != "b2" {
		t.Errorf("children of a = %s, want b2", got[0].Name)
	}
}

// TestEffectiveChildrenForNavigation_NoChildren — leaves return an empty
// slice. The caller treats this as "already at stack leaf".
func TestEffectiveChildrenForNavigation_NoChildren(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	mgr = makeStackChain(t, mgr, repoDir, []string{"a"}, nil)

	got := effectiveChildrenForNavigation(mgr, "a")
	if len(got) != 0 {
		t.Errorf("leaf children = %v, want []", got)
	}
}

// TestEffectiveChildrenForNavigation_OnlyMergedChildren — a parent whose only
// child has been merged with no descendants reports zero children. The user
// gets the leaf message, not a confusing "navigated to a deleted branch"
// error. This is the regression case for the original `ezs down` bug where
// merged-direct-child caused a stop, not a step-through.
func TestEffectiveChildrenForNavigation_OnlyMergedChildren(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	mgr = makeStackChain(t, mgr, repoDir, []string{"a", "b"}, map[string]bool{"b": true})

	got := effectiveChildrenForNavigation(mgr, "a")
	if len(got) != 0 {
		t.Errorf("children of a (only merged b, no descendants) = %v, want []", got)
	}
}

// TestEffectiveChildrenForNavigation_MultipleNonMergedChildrenSorted —
// multi-child selector path. Build siblings in non-alpha order and verify
// the helper returns them sorted.
func TestEffectiveChildrenForNavigation_MultipleNonMergedChildrenSorted(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	if _, err := mgr.CreateBranchNoWorktree("a", "main", "new"); err != nil {
		t.Fatal(err)
	}
	mgr = reloadManager(t, repoDir)
	for _, name := range []string{"zoo", "ant", "moth"} {
		if _, err := mgr.CreateBranchNoWorktree(name, "a", ""); err != nil {
			t.Fatal(err)
		}
		mgr = reloadManager(t, repoDir)
	}

	got := effectiveChildrenForNavigation(mgr, "a")
	want := []string{"ant", "moth", "zoo"}
	if len(got) != len(want) {
		t.Fatalf("got %d children, want %d", len(got), len(want))
	}
	for i, b := range got {
		if b.Name != want[i] {
			t.Errorf("children[%d] = %s, want %s", i, b.Name, want[i])
		}
	}
}
