package commands

import (
	"reflect"
	"strings"
	"testing"
)

// TestCollectDescendants_DeepestFirst pins the ordering contract that
// `Delete --cascade` relies on: children must appear before their parents
// in the returned slice so `mgr.DeleteBranch` can be called in slice order
// without ever asking git to remove a branch that still has live children.
//
// Tree under construction:
//
//	main
//	└── a
//	    ├── b
//	    │   └── d
//	    └── c
//
// Valid orderings: any post-order walk of {b,c,d} where d precedes b.
// Invalid: anything where b precedes d, or where the root `a` appears at all.
func TestCollectDescendants_DeepestFirst(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)

	mgr := reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("a", "main", "new"); err != nil {
		t.Fatalf("create a: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("b", "a", ""); err != nil {
		t.Fatalf("create b: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("c", "a", ""); err != nil {
		t.Fatalf("create c: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("d", "b", ""); err != nil {
		t.Fatalf("create d: %v", err)
	}

	mgr = reloadManager(t, repoDir)
	got := collectDescendants(mgr, "a")

	// Must contain exactly {b, c, d} in some order — `a` itself excluded.
	if len(got) != 3 {
		t.Fatalf("collectDescendants returned %d names (%v); want 3", len(got), got)
	}
	seen := map[string]int{}
	for i, n := range got {
		seen[n] = i
	}
	for _, want := range []string{"b", "c", "d"} {
		if _, ok := seen[want]; !ok {
			t.Errorf("missing descendant %q in %v", want, got)
		}
	}
	if _, ok := seen["a"]; ok {
		t.Errorf("root branch 'a' should not appear in descendants: %v", got)
	}
	// Deepest-first: d must come before b (d is a child of b).
	if seen["d"] > seen["b"] {
		t.Errorf("ordering violated: d at %d should come before b at %d in %v", seen["d"], seen["b"], got)
	}
}

func TestCollectDescendants_NoChildren(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	mgr := reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("solo", "main", "new"); err != nil {
		t.Fatalf("create solo: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if got := collectDescendants(mgr, "solo"); !reflect.DeepEqual(got, []string(nil)) && len(got) != 0 {
		t.Errorf("expected empty descendants for leaf, got %v", got)
	}
}

// TestDelete_NonexistentBranch_ErrorsBeforePrompt locks down the fix from
// edge-case audit pass: previously, `ezs delete <typo>` showed a scary
// "this will delete X and orphan Y children" warning + confirmation BEFORE
// validating the branch existed. The fix moved validation upstream of the
// prompt so a typo errors out immediately.
//
// The assertion: with ui.YesMode enabled (which would auto-confirm any
// prompt), the call still returns an error — proving the validation runs
// before the prompt is reached. A regression that re-introduces the prompt
// would silently auto-confirm and proceed to a no-op success.
func TestDelete_NonexistentBranch_ErrorsBeforePrompt(t *testing.T) {
	setupCLITestEnv(t)

	err := Delete([]string{"branch-that-does-not-exist"})
	if err == nil {
		t.Fatal("Delete of non-existent branch returned nil error; want 'not found' error before any prompt")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q should mention 'not found'", err.Error())
	}
}

// TestDelete_LeafBranch_RemovesFromStack exercises the happy path for a
// branch with no children and no worktree (CreateBranchNoWorktree). The
// branch should disappear from stack tracking after Delete.
func TestDelete_LeafBranch_RemovesFromStack(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	mgr := reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("feature", "main", "new"); err != nil {
		t.Fatalf("create feature: %v", err)
	}

	if err := Delete([]string{"feature"}); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	mgr = reloadManager(t, repoDir)
	if b := mgr.GetBranch("feature"); b != nil {
		t.Errorf("branch 'feature' still tracked after delete: %+v", b)
	}
}

// TestDelete_BranchWithChildren_RefusesWithoutForce confirms the safety
// guard refuses to orphan children unless --force or --cascade is given.
// Without this, a Delete on a parent silently leaves dangling tree refs.
//
// This is the primary regression we want to catch — the pre-prompt error
// path was the same scaffolding affected by the cross-stack reparent
// panic fix; reverts in the area can re-open it.
func TestDelete_BranchWithChildren_RefusesWithoutForce(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	mgr := reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("parent", "main", "new"); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	mgr = reloadManager(t, repoDir)
	if _, err := mgr.CreateBranchNoWorktree("child", "parent", ""); err != nil {
		t.Fatalf("create child: %v", err)
	}

	err := Delete([]string{"parent"})
	if err == nil {
		t.Fatal("Delete of parent-with-children returned nil; want refusal")
	}
	for _, want := range []string{"child", "force", "cascade"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q to be actionable", err.Error(), want)
		}
	}

	// Both branches should still exist in the stack.
	mgr = reloadManager(t, repoDir)
	if b := mgr.GetBranch("parent"); b == nil {
		t.Error("parent was removed despite refusal")
	}
	if b := mgr.GetBranch("child"); b == nil {
		t.Error("child was removed despite refusal")
	}
}
