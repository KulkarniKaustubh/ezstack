package ghstack

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

// requireGHStack skips integration tests when a usable gh-stack is absent
// (CI installs it; see .github/workflows/ci.yml).
func requireGHStack(t *testing.T) {
	t.Helper()
	if err := Check(); err != nil {
		t.Skipf("gh stack unavailable: %v", err)
	}
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitFile(t *testing.T, dir, file, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", file)
	gitT(t, dir, "commit", "-q", "-m", msg)
}

// fixture is a repo with a bare origin; every stack branch lives in its own
// linked worktree, like ezstack lays them out.
type fixture struct {
	t    *testing.T
	root string
	repo string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	requireGHStack(t)
	root := t.TempDir()
	f := &fixture{t: t, root: root, repo: filepath.Join(root, "repo")}
	gitT(t, root, "init", "-q", "--bare", "-b", "main", filepath.Join(root, "origin.git"))
	gitT(t, root, "init", "-q", "-b", "main", f.repo)
	gitT(t, f.repo, "config", "user.email", "t@example.com")
	gitT(t, f.repo, "config", "user.name", "t")
	commitFile(t, f.repo, "base.txt", "base\n", "base")
	gitT(t, f.repo, "remote", "add", "origin", filepath.Join(root, "origin.git"))
	gitT(t, f.repo, "push", "-q", "-u", "origin", "main")
	return f
}

// branch creates name off parent in its own worktree with one commit.
func (f *fixture) branch(name, parent string) string {
	wt := filepath.Join(f.root, "wt-"+name)
	gitT(f.t, f.repo, "worktree", "add", "-q", "-b", name, wt, parent)
	commitFile(f.t, wt, name+".txt", name+"\n", name+" work")
	return wt
}

func (f *fixture) wt(name string) string { return filepath.Join(f.root, "wt-"+name) }

func (f *fixture) reconcile(s *config.Stack, forget ...string) []Chain {
	f.t.Helper()
	chains, err := Reconcile(f.repo, []*config.Stack{s}, forget...)
	if err != nil {
		f.t.Fatalf("Reconcile: %v", err)
	}
	return chains
}

func (f *fixture) catalog() *Catalog {
	f.t.Helper()
	dir, err := CommonDir(f.repo)
	if err != nil {
		f.t.Fatal(err)
	}
	cat, err := LoadCatalog(dir)
	if err != nil {
		f.t.Fatal(err)
	}
	return cat
}

func (f *fixture) isAncestor(a, b string) bool {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", a, b)
	cmd.Dir = f.repo
	return cmd.Run() == nil
}

func stackOf(root string, tree config.BranchTree, merged ...string) *config.Stack {
	s := &config.Stack{Hash: "abc1234", Root: root, Tree: tree}
	isMerged := map[string]bool{}
	for _, m := range merged {
		isMerged[m] = true
	}
	var walk func(parent string, t config.BranchTree)
	walk = func(parent string, t config.BranchTree) {
		for _, k := range sortedKeys(t) {
			b := &config.Branch{Name: k, Parent: parent, IsMerged: isMerged[k]}
			if isMerged[k] {
				b.PRNumber = 100 + len(s.Branches)
			}
			s.Branches = append(s.Branches, b)
			walk(k, t[k])
		}
	}
	walk(root, tree)
	return s
}

func (f *fixture) client() *Client { return New(f.repo).WithOutput(io.Discard) }

// TestRebase_LinearAcrossWorktrees: main advances; one gh-stack rebase
// restacks every branch in its own worktree, leaving each worktree attached.
func TestRebase_LinearAcrossWorktrees(t *testing.T) {
	f := newFixture(t)
	f.branch("a", "main")
	f.branch("b", "a")
	s := stackOf("main", config.BranchTree{"a": {"b": {}}})
	chains := f.reconcile(s)
	if len(chains) != 1 || strings.Join(chains[0].Branches, ",") != "a,b" {
		t.Fatalf("chains = %+v", chains)
	}

	commitFile(t, f.repo, "main2.txt", "m2\n", "main advances")
	gitT(t, f.repo, "push", "-q", "origin", "main")

	if err := f.client().Rebase(RebaseOptions{Branch: "b", Remote: "origin"}); err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if !f.isAncestor("main", "a") || !f.isAncestor("a", "b") {
		t.Fatal("stack was not restacked onto main")
	}
	if got := gitT(t, f.wt("b"), "branch", "--show-current"); got != "b" {
		t.Fatalf("worktree b is on %q, want b", got)
	}
	// Second reconcile is a no-op.
	before := f.catalog()
	f.reconcile(s)
	if len(f.catalog().Stacks) != len(before.Stacks) {
		t.Fatal("reconcile not idempotent")
	}
}

// TestRebase_TreeFork: a branch with two children becomes two root→leaf
// chains sharing the fork point. After the fork point is REWRITTEN (amended)
// and main advances, restacking both chains moves each child's own commit
// onto the new fork point — the case a trunk-based nested stack gets wrong.
func TestRebase_TreeFork(t *testing.T) {
	f := newFixture(t)
	f.branch("a", "main")
	f.branch("b1", "a")
	f.branch("b2", "a")
	s := stackOf("main", config.BranchTree{"a": {"b1": {}, "b2": {}}})
	chains := f.reconcile(s)
	if len(chains) != 2 || strings.Join(chains[1].Branches, ",") != "a,b2" || chains[1].Shared != 1 ||
		chains[1].Base() != "a" || strings.Join(chains[1].Own(), ",") != "b2" {
		t.Fatalf("chains = %+v", chains)
	}
	if Unique(chains, "a") || !Unique(chains, "b2") {
		t.Fatal("Unique: fork point must be shared, leaves unique")
	}

	if err := os.WriteFile(filepath.Join(f.wt("a"), "a.txt"), []byte("a amended\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldA := gitT(t, f.repo, "rev-parse", "a")
	gitT(t, f.wt("a"), "commit", "-q", "-a", "--amend", "-m", "a amended")
	if f.isAncestor(oldA, "a") {
		t.Fatal("fixture: amend did not rewrite a")
	}
	commitFile(t, f.repo, "main2.txt", "m2\n", "main advances")
	gitT(t, f.repo, "push", "-q", "origin", "main")

	c := f.client()
	for _, ch := range chains {
		if err := c.Rebase(RebaseOptions{Branch: ch.Top(), Remote: "origin"}); err != nil {
			t.Fatalf("Rebase %v: %v", ch.Branches, err)
		}
	}
	for _, b := range []string{"b1", "b2"} {
		if !f.isAncestor("a", b) {
			t.Fatalf("%s not restacked onto amended a", b)
		}
		if n := gitT(t, f.repo, "rev-list", "--count", "a.."+b); n != "1" {
			t.Fatalf("%s has %s commits over a, want 1 (old a replayed?)", b, n)
		}
	}
}

// TestRebase_SquashMergedParentDropped: the bottom branch is squash-merged
// into main and its branch deleted; the projection keeps it as a merged layer
// and gh-stack moves the child onto main without replaying the parent.
func TestRebase_SquashMergedParentDropped(t *testing.T) {
	f := newFixture(t)
	f.branch("a", "main")
	commitFile(t, f.wt("a"), "base.txt", "base\nfrom a\n", "a edits base")
	f.branch("b", "a")
	s := stackOf("main", config.BranchTree{"a": {"b": {}}})
	f.reconcile(s)

	// Squash-merge a into main on the "server", then prune a locally.
	gitT(t, f.repo, "merge", "-q", "--squash", "a")
	gitT(t, f.repo, "commit", "-q", "-m", "a (#100)")
	gitT(t, f.repo, "push", "-q", "origin", "main")
	gitT(t, f.repo, "worktree", "remove", "--force", f.wt("a"))
	gitT(t, f.repo, "branch", "-D", "a")

	s = stackOf("main", config.BranchTree{"a": {"b": {}}}, "a")
	chains := f.reconcile(s)
	if len(chains) != 1 || !chains[0].Merged["a"] {
		t.Fatalf("chains = %+v", chains)
	}
	if err := f.client().Rebase(RebaseOptions{Branch: "b", Remote: "origin"}); err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if !f.isAncestor("main", "b") {
		t.Fatal("b not rebased onto main")
	}
	if n := gitT(t, f.repo, "rev-list", "--count", "main..b"); n != "1" {
		t.Fatalf("b has %s commits over main, want 1 (parent's commits replayed?)", n)
	}
}

// TestReparent_RecomputesBase: moving b from a onto c (after the caller
// rebased it there) must not keep a's base, or the next cascade would replay
// a's commits onto c.
func TestReparent_RecomputesBase(t *testing.T) {
	f := newFixture(t)
	f.branch("a", "main")
	f.branch("b", "a")
	f.branch("c", "main")
	f.reconcile(stackOf("main", config.BranchTree{"a": {"b": {}}, "c": {}}))

	gitT(t, f.wt("b"), "rebase", "-q", "--onto", "c", "a")
	f.reconcile(stackOf("main", config.BranchTree{"a": {}, "c": {"b": {}}}))
	for _, st := range f.catalog().Stacks {
		for i, b := range st.Branches {
			if b.Branch == "b" {
				if i == 0 || st.Branches[i-1].Branch != "c" {
					t.Fatalf("b not stacked on c: %+v", st)
				}
				if want := gitT(t, f.repo, "rev-parse", "c"); b.Base != want {
					t.Fatalf("b base = %s, want c tip %s", b.Base, want)
				}
			}
		}
	}
}

// TestReparent_WithoutRebase: moving b from a onto c in the model only, then
// letting gh-stack restack, carries just b's own commits onto c.
func TestReparent_WithoutRebase(t *testing.T) {
	f := newFixture(t)
	f.branch("a", "main")
	f.branch("b", "a")
	f.branch("c", "main")
	f.reconcile(stackOf("main", config.BranchTree{"a": {"b": {}}, "c": {}}))

	chains := f.reconcile(stackOf("main", config.BranchTree{"a": {}, "c": {"b": {}}}))
	ch, ok := ChainFor(chains, "b")
	if !ok {
		t.Fatalf("b not in any chain: %+v", chains)
	}
	if err := f.client().Rebase(RebaseOptions{Branch: ch.Top(), Remote: "origin"}); err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if !f.isAncestor("c", "b") {
		t.Fatal("b not moved onto c")
	}
	if f.isAncestor("a", "b") {
		t.Fatal("a's commits were carried along onto c")
	}
	if n := gitT(t, f.repo, "rev-list", "--count", "c..b"); n != "1" {
		t.Fatalf("b has %s commits over c, want 1", n)
	}
}

// TestConflictContinue: a conflict pauses the cascade in the owning
// worktree; Reconcile refuses meanwhile; Continue finishes the cascade.
func TestConflictContinue(t *testing.T) {
	f := newFixture(t)
	f.branch("a", "main")
	commitFile(t, f.wt("a"), "base.txt", "a\n", "a edits base")
	f.branch("b", "a")
	s := stackOf("main", config.BranchTree{"a": {"b": {}}})
	f.reconcile(s)

	commitFile(t, f.repo, "base.txt", "main\n", "main edits base")
	gitT(t, f.repo, "push", "-q", "origin", "main")

	c := f.client()
	err := c.Rebase(RebaseOptions{Branch: "b", Remote: "origin"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Rebase err = %v, want ErrConflict", err)
	}
	if _, err := Reconcile(f.repo, []*config.Stack{s}); !errors.Is(err, ErrRebasePaused) {
		t.Fatalf("Reconcile during paused rebase: %v, want ErrRebasePaused", err)
	}

	// Resolve in a's worktree, where gh-stack left the conflict.
	if err := os.WriteFile(filepath.Join(f.wt("a"), "base.txt"), []byte("resolved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, f.wt("a"), "add", "base.txt")
	if err := c.Continue(); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if !f.isAncestor("main", "a") || !f.isAncestor("a", "b") {
		t.Fatal("cascade did not complete after continue")
	}
	if got := gitT(t, f.wt("a"), "branch", "--show-current"); got != "a" {
		t.Fatalf("worktree a is on %q after continue", got)
	}
}

// TestReconcile_LeavesUnmanagedStacks: stacks built with gh-stack directly
// survive reconciliation; forgotten branches are dropped.
func TestReconcile_LeavesUnmanagedStacks(t *testing.T) {
	f := newFixture(t)
	f.branch("mine", "main")
	f.branch("x", "main")
	f.branch("y", "main")
	cmd := command(f.repo, "init", "--base", "main", "mine")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gh stack init: %v\n%s", err, out)
	}
	s := stackOf("main", config.BranchTree{"x": {}, "y": {}})
	f.reconcile(s)
	if n := len(f.catalog().Stacks); n != 3 {
		t.Fatalf("stacks = %d, want 3 (unmanaged + x + y)", n)
	}
	f.reconcile(stackOf("main", config.BranchTree{"x": {}}), "y")
	cat := f.catalog()
	var names []string
	for _, st := range cat.Stacks {
		names = append(names, strings.Join(st.BranchNames(), ","))
	}
	if strings.Join(names, "|") != "mine|x" {
		t.Fatalf("stacks = %v, want [mine x]", names)
	}
}

// TestRebase_StackRootedOnStackBranch: a stack rooted on another stack's
// branch x is projected with x as a shared layer above the real trunk, so
// after x is amended its child moves with --onto (own commit only) instead
// of a plain rebase that would replay x's old commit.
func TestRebase_StackRootedOnStackBranch(t *testing.T) {
	f := newFixture(t)
	f.branch("x", "main")
	f.branch("y", "x")
	s1 := stackOf("main", config.BranchTree{"x": {}})
	s1.Hash = "s1aaaaa"
	s2 := &config.Stack{Hash: "s2bbbbb", Root: "x", Tree: config.BranchTree{"y": {}},
		Branches: []*config.Branch{{Name: "y", Parent: "x", BaseBranch: "x"}}}
	s1.Branches[0].BaseBranch = "main"
	chains, err := Reconcile(f.repo, []*config.Stack{s2, s1})
	if err != nil {
		t.Fatal(err)
	}
	if len(chains) != 2 || chains[1].Trunk != "main" || strings.Join(chains[1].Branches, ",") != "x,y" || chains[1].Shared != 1 {
		t.Fatalf("chains = %+v", chains)
	}

	if err := os.WriteFile(filepath.Join(f.wt("x"), "x.txt"), []byte("x amended\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, f.wt("x"), "commit", "-q", "-a", "--amend", "-m", "x amended")
	// s1's chain [x] has no layer of its own that gh-stack can address (x
	// is also in s2's chain); restacking s2's chain covers it.
	if Unique(chains, chains[0].Top()) {
		t.Fatal("x should be shared")
	}
	if err := f.client().Rebase(RebaseOptions{Branch: chains[1].Top(), Remote: "origin"}); err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if !f.isAncestor("x", "y") {
		t.Fatal("y not restacked onto amended x")
	}
	if n := gitT(t, f.repo, "rev-list", "--count", "x..y"); n != "1" {
		t.Fatalf("y has %s commits over x, want 1 (old x replayed?)", n)
	}
}
