package itests

import (
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
)

// TestReparent_CrossStack_Issue19 is the integration version of the issue #19
// repro: it exercises the full worktree-backed CreateBranch / ReparentBranch
// pipeline (the unit-test version in internal/stack uses no-worktree branches).
// After reparenting branchB1 onto branchA2 the stacks must collapse into one.
func TestReparent_CrossStack_Issue19(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Stack A = main → branchA1 → branchA2
	CreateBranch(t, env, "branchA1", "main")
	CreateBranch(t, env, "branchA2", "branchA1")

	// Stack B = main → branchB1 (separate stack via mgr.CreateBranch with
	// targetStackHash="new" so it doesn't auto-attach to stack A under main).
	mgr, _ := stack.NewManager(env.RepoDir)
	if _, err := mgr.CreateBranch("branchB1", "main", "", "new"); err != nil {
		t.Fatalf("CreateBranch(branchB1, new) failed: %v", err)
	}

	mgr, _ = stack.NewManager(env.RepoDir)
	if got := len(mgr.ListStacks()); got != 2 {
		t.Fatalf("expected 2 stacks before reparent, got %d", got)
	}

	mgr, _ = stack.NewManager(env.RepoDir)
	if _, err := mgr.ReparentBranch("branchB1", "branchA2", false); err != nil {
		t.Fatalf("ReparentBranch failed: %v", err)
	}

	mgr, _ = stack.NewManager(env.RepoDir)
	stacks := mgr.ListStacks()
	if len(stacks) != 1 {
		t.Fatalf("expected 1 stack after cross-stack reparent, got %d", len(stacks))
	}

	// Branches must each appear in exactly one stack — the bug's fingerprint
	// was the same branch listed in multiple fragment stacks.
	seen := make(map[string]int)
	for _, s := range stacks {
		for _, b := range s.Branches {
			seen[b.Name]++
		}
	}
	for name, count := range seen {
		if count != 1 {
			t.Errorf("branch %q appears in %d stacks; want 1", name, count)
		}
	}

	AssertBranchParent(t, env, "branchB1", "branchA2")
	AssertBranchParent(t, env, "branchA2", "branchA1")
	AssertBranchParent(t, env, "branchA1", "main")
}

// TestReparent_CrossStack_SevenChain_Issue19 is the integration analogue of the
// seven-branch chain from the issue body. Six reparent calls must produce one
// contiguous stack, not six fragment stacks.
func TestReparent_CrossStack_SevenChain_Issue19(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	branches := []string{"new1", "new2", "new3", "new4", "new5", "new6", "new7"}
	for _, b := range branches {
		mgr, _ := stack.NewManager(env.RepoDir)
		// "new" forces each branch into its own single-branch stack rooted on main.
		if _, err := mgr.CreateBranch(b, "main", "", "new"); err != nil {
			t.Fatalf("CreateBranch(%s) failed: %v", b, err)
		}
	}

	mgr, _ := stack.NewManager(env.RepoDir)
	if got := len(mgr.ListStacks()); got != 7 {
		t.Fatalf("expected 7 stacks after setup, got %d", got)
	}

	pairs := [][2]string{
		{"new2", "new1"}, {"new3", "new2"}, {"new4", "new3"},
		{"new5", "new4"}, {"new6", "new5"}, {"new7", "new6"},
	}
	for _, p := range pairs {
		mgr, _ := stack.NewManager(env.RepoDir)
		if _, err := mgr.ReparentBranch(p[0], p[1], false); err != nil {
			t.Fatalf("ReparentBranch(%s, %s) failed: %v", p[0], p[1], err)
		}
	}

	mgr, _ = stack.NewManager(env.RepoDir)
	stacks := mgr.ListStacks()
	if len(stacks) != 1 {
		t.Fatalf("expected 1 stack after chaining; got %d", len(stacks))
	}
	if len(stacks[0].Branches) != 7 {
		t.Errorf("expected 7 branches in collapsed stack, got %d", len(stacks[0].Branches))
	}
	for i, b := range branches {
		got := mgr.GetBranch(b)
		if got == nil {
			t.Errorf("missing branch %s", b)
			continue
		}
		want := "main"
		if i > 0 {
			want = branches[i-1]
		}
		if got.Parent != want {
			t.Errorf("%s parent = %q, want %q", b, got.Parent, want)
		}
	}
}
