package ghstack

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

func TestLeafPaths(t *testing.T) {
	tests := []struct {
		name string
		tree config.BranchTree
		want [][]string
	}{
		{
			name: "empty",
			tree: config.BranchTree{},
			want: nil,
		},
		{
			name: "linear",
			tree: config.BranchTree{"l1": {"l2": {"l3": {}}}},
			want: [][]string{{"l1", "l2", "l3"}},
		},
		{
			name: "branching at l1 (tree overlay -> two linear stacks)",
			tree: config.BranchTree{"l1": {"l2a": {}, "l2b": {}}},
			want: [][]string{{"l1", "l2a"}, {"l1", "l2b"}},
		},
		{
			name: "two roots",
			tree: config.BranchTree{"a": {}, "b": {"c": {}}},
			want: [][]string{{"a"}, {"b", "c"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LeafPaths(tc.tree)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("LeafPaths(%v) = %v, want %v", tc.tree, got, tc.want)
			}
		})
	}
}

// TestReconcile_Linear creates branches, projects a linear ezstack stack into
// gh-stack via Reconcile, and asserts gh-stack now tracks them. A second call
// must be a no-op (idempotent) — no duplicate stack.
func TestReconcile_Linear(t *testing.T) {
	if !Installed() {
		t.Skip("gh stack extension not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	bare := filepath.Join(root, "remote.git")
	gitT(t, root, "init", "-q", "--bare", bare)
	gitT(t, root, "init", "-q", "-b", "main", repo)
	gitT(t, repo, "config", "user.email", "t@t.com")
	gitT(t, repo, "config", "user.name", "t")
	writeCommit(t, repo, "f.txt", "base", "base")
	gitT(t, repo, "remote", "add", "origin", bare)
	gitT(t, repo, "checkout", "-q", "-b", "l1", "main")
	writeCommit(t, repo, "a.txt", "a", "l1")
	gitT(t, repo, "checkout", "-q", "-b", "l2", "l1")
	writeCommit(t, repo, "b.txt", "b", "l2")
	gitT(t, repo, "checkout", "-q", "main")
	gitT(t, repo, "push", "-q", "origin", "main", "l1", "l2")

	stack := &config.Stack{Root: "main", Tree: config.BranchTree{"l1": {"l2": {}}}}

	n, err := Reconcile(repo, stack)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if n != 2 {
		t.Errorf("first Reconcile registered %d branches, want 2 (l1 via init, l2 via add)", n)
	}

	// One linear gh-stack stack [l1 l2] — l2 adopted onto l1, not a duplicate.
	st, err := New(repo).ReadState()
	if err != nil || st == nil || len(st.Stacks) != 1 {
		t.Fatalf("state after reconcile = %+v, err=%v", st, err)
	}
	if got := st.Stacks[0].BranchNames(); !reflect.DeepEqual(got, []string{"l1", "l2"}) {
		t.Errorf("tracked branches = %v, want [l1 l2]", got)
	}

	// Idempotent: second call registers nothing.
	n2, err := Reconcile(repo, stack)
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second Reconcile registered %d branches, want 0 (idempotent)", n2)
	}
}

// TestReconcile_GrowExisting verifies the incremental path: a stack already
// tracking [l1] grows to [l1 l2] via `gh stack add` (not a duplicate init),
// mirroring what happens across successive `ezs new` calls.
func TestReconcile_GrowExisting(t *testing.T) {
	if !Installed() {
		t.Skip("gh stack extension not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	bare := filepath.Join(root, "remote.git")
	gitT(t, root, "init", "-q", "--bare", bare)
	gitT(t, root, "init", "-q", "-b", "main", repo)
	gitT(t, repo, "config", "user.email", "t@t.com")
	gitT(t, repo, "config", "user.name", "t")
	writeCommit(t, repo, "f.txt", "base", "base")
	gitT(t, repo, "remote", "add", "origin", bare)
	gitT(t, repo, "checkout", "-q", "-b", "l1", "main")
	writeCommit(t, repo, "a.txt", "a", "l1")
	gitT(t, repo, "checkout", "-q", "main")
	gitT(t, repo, "push", "-q", "origin", "main", "l1")

	// Register just l1.
	if _, err := Reconcile(repo, &config.Stack{Root: "main", Tree: config.BranchTree{"l1": {}}}); err != nil {
		t.Fatalf("reconcile l1: %v", err)
	}
	// Now create l2 and grow.
	gitT(t, repo, "checkout", "-q", "-b", "l2", "l1")
	writeCommit(t, repo, "b.txt", "b", "l2")
	gitT(t, repo, "checkout", "-q", "main")
	gitT(t, repo, "push", "-q", "origin", "l2")

	n, err := Reconcile(repo, &config.Stack{Root: "main", Tree: config.BranchTree{"l1": {"l2": {}}}})
	if err != nil {
		t.Fatalf("reconcile l1->l2: %v", err)
	}
	if n != 1 {
		t.Errorf("grow registered %d branches, want 1 (just l2)", n)
	}
	st, _ := New(repo).ReadState()
	if st == nil || len(st.Stacks) != 1 {
		t.Fatalf("want a single stack after grow, got %+v", st)
	}
	if got := st.Stacks[0].BranchNames(); !reflect.DeepEqual(got, []string{"l1", "l2"}) {
		t.Errorf("after grow = %v, want [l1 l2] (adopted, not duplicated)", got)
	}
}
