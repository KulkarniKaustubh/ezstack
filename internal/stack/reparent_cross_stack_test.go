package stack

import (
	"encoding/json"
	"testing"
)

// branchAppearances counts how many distinct stacks each branch shows up in.
// Each branch must appear in exactly one stack — duplicates would be the
// fragmentation signature reported in issue #19.
func branchAppearances(mgr *Manager) map[string]int {
	counts := make(map[string]int)
	for _, s := range mgr.ListStacks() {
		seen := make(map[string]bool)
		for _, b := range s.Branches {
			if seen[b.Name] {
				continue
			}
			seen[b.Name] = true
			counts[b.Name]++
		}
	}
	return counts
}

func assertNoBranchInMultipleStacks(t *testing.T, mgr *Manager) {
	t.Helper()
	for name, n := range branchAppearances(mgr) {
		if n > 1 {
			t.Errorf("branch %q appears in %d stacks; should appear in exactly one", name, n)
		}
	}
}

// TestReparent_Issue19_SevenBranchChain replays the larger repro from issue #19:
// seven single-branch stacks each rooted on main, then six `reparent` calls to
// chain new1→…→new7. The fix must collapse them into a single stack with no
// branch appearing in more than one stack. Before the fix, every adjacent pair
// became its own fragment stack and branches duplicated across them.
func TestReparent_Issue19_SevenBranchChain(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	branches := []string{"new1", "new2", "new3", "new4", "new5", "new6", "new7"}
	for _, b := range branches {
		mgr, _ := NewManager(repoDir)
		// targetStackHash "new" forces a fresh single-branch stack — mirrors the
		// user answering "yes" to `ezs new`'s "make this a stack root?" prompt.
		if _, err := mgr.CreateBranchNoWorktree(b, "main", "new"); err != nil {
			t.Fatalf("CreateBranchNoWorktree(%s) error = %v", b, err)
		}
	}

	mgr, _ := NewManager(repoDir)
	if got := len(mgr.ListStacks()); got != 7 {
		t.Fatalf("expected 7 single-branch stacks after setup, got %d", got)
	}

	pairs := [][2]string{
		{"new2", "new1"},
		{"new3", "new2"},
		{"new4", "new3"},
		{"new5", "new4"},
		{"new6", "new5"},
		{"new7", "new6"},
	}
	for _, p := range pairs {
		mgr, _ := NewManager(repoDir)
		if _, err := mgr.ReparentBranch(p[0], p[1], false); err != nil {
			t.Fatalf("ReparentBranch(%s, %s) error = %v", p[0], p[1], err)
		}
		mgr, _ = NewManager(repoDir)
		assertNoBranchInMultipleStacks(t, mgr)
	}

	mgr, _ = NewManager(repoDir)
	stacks := mgr.ListStacks()
	if len(stacks) != 1 {
		dumpStacks(t, "final", mgr)
		t.Fatalf("expected 1 stack after chaining; got %d (see log for details)", len(stacks))
	}

	// The single surviving stack must contain the full ordered chain new1→…→new7.
	s := stacks[0]
	if s.Root != "main" {
		t.Errorf("stack root = %q, want main", s.Root)
	}
	if len(s.Branches) != 7 {
		t.Errorf("expected 7 branches in collapsed stack, got %d", len(s.Branches))
	}
	for i, b := range branches {
		if got := mgr.GetBranch(b); got == nil {
			t.Errorf("missing branch %q", b)
		} else if i == 0 && got.Parent != "main" {
			t.Errorf("%s parent = %q, want main", b, got.Parent)
		} else if i > 0 && got.Parent != branches[i-1] {
			t.Errorf("%s parent = %q, want %s", b, got.Parent, branches[i-1])
		}
	}
}

// TestReparent_Issue19_TwoStackCanonical reproduces the minimal repro from the
// issue body: Stack A = main→branchA1→branchA2, Stack B = main→branchB1. Reparent
// branchB1 onto branchA2. The whole branchB1 subtree must move into stack A;
// stack B must be deleted. No third "fragment" stack may be created.
func TestReparent_Issue19_TwoStackCanonical(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	if _, err := mgr.CreateBranchNoWorktree("branchA1", "main", "new"); err != nil {
		t.Fatalf("CreateBranchNoWorktree(branchA1): %v", err)
	}
	mgr, _ = NewManager(repoDir)
	if _, err := mgr.CreateBranchNoWorktree("branchA2", "branchA1", ""); err != nil {
		t.Fatalf("CreateBranchNoWorktree(branchA2): %v", err)
	}
	mgr, _ = NewManager(repoDir)
	if _, err := mgr.CreateBranchNoWorktree("branchB1", "main", "new"); err != nil {
		t.Fatalf("CreateBranchNoWorktree(branchB1): %v", err)
	}

	mgr, _ = NewManager(repoDir)
	if _, err := mgr.ReparentBranch("branchB1", "branchA2", false); err != nil {
		t.Fatalf("ReparentBranch: %v", err)
	}

	mgr, _ = NewManager(repoDir)
	stacks := mgr.ListStacks()
	if len(stacks) != 1 {
		dumpStacks(t, "after reparent", mgr)
		t.Fatalf("expected 1 stack after cross-stack reparent, got %d", len(stacks))
	}

	assertNoBranchInMultipleStacks(t, mgr)

	if got := mgr.GetBranch("branchB1"); got == nil || got.Parent != "branchA2" {
		t.Errorf("branchB1 parent = %v, want branchA2", got)
	}
	// Verify the surviving stack has all three branches.
	if len(stacks[0].Branches) != 3 {
		t.Errorf("expected 3 branches in collapsed stack, got %d", len(stacks[0].Branches))
	}
}

// TestReparent_Issue19_SubtreeMove verifies that reparenting a branch with
// descendants moves the entire subtree across stacks, not just the branch.
// Stack A = main→branchA1, Stack B = main→branchB1→branchB2. Reparent branchB1
// onto branchA1. Expected: Stack A becomes main→branchA1→branchB1→branchB2 and
// stack B disappears.
func TestReparent_Issue19_SubtreeMove(t *testing.T) {
	repoDir, _, cleanup := setupTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	if _, err := mgr.CreateBranchNoWorktree("branchA1", "main", "new"); err != nil {
		t.Fatalf("CreateBranchNoWorktree(branchA1): %v", err)
	}
	mgr, _ = NewManager(repoDir)
	if _, err := mgr.CreateBranchNoWorktree("branchB1", "main", "new"); err != nil {
		t.Fatalf("CreateBranchNoWorktree(branchB1): %v", err)
	}
	mgr, _ = NewManager(repoDir)
	if _, err := mgr.CreateBranchNoWorktree("branchB2", "branchB1", ""); err != nil {
		t.Fatalf("CreateBranchNoWorktree(branchB2): %v", err)
	}

	mgr, _ = NewManager(repoDir)
	if _, err := mgr.ReparentBranch("branchB1", "branchA1", false); err != nil {
		t.Fatalf("ReparentBranch: %v", err)
	}

	mgr, _ = NewManager(repoDir)
	stacks := mgr.ListStacks()
	if len(stacks) != 1 {
		dumpStacks(t, "after subtree move", mgr)
		t.Fatalf("expected 1 stack, got %d", len(stacks))
	}
	assertNoBranchInMultipleStacks(t, mgr)

	if got := mgr.GetBranch("branchB1"); got == nil || got.Parent != "branchA1" {
		t.Errorf("branchB1 parent = %v, want branchA1", got)
	}
	if got := mgr.GetBranch("branchB2"); got == nil || got.Parent != "branchB1" {
		t.Errorf("branchB2 parent = %v, want branchB1 (subtree must follow)", got)
	}
}

func dumpStacks(t *testing.T, label string, mgr *Manager) {
	t.Helper()
	for _, s := range mgr.ListStacks() {
		j, _ := json.Marshal(s.Tree)
		t.Logf("  [%s] stack %s root=%s tree=%s", label, s.Hash, s.Root, string(j))
	}
}
