package ghstack

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// gitT runs git in dir and fails the test on error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitAt(dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return out
}

// newStackRepo builds an isolated repo with a bare "remote" and a two-layer
// stack (main <- l1 <- l2) initialized through gh-stack. It returns the main
// worktree path. Tests that need gh-stack skip when it isn't installed.
func newStackRepo(t *testing.T) string {
	t.Helper()
	if !Installed() {
		t.Skip("gh stack extension not installed; skipping integration test")
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
	// Create the two layers, then adopt them into a stack.
	gitT(t, repo, "checkout", "-q", "-b", "l1", "main")
	writeCommit(t, repo, "a.txt", "a", "l1 work")
	gitT(t, repo, "checkout", "-q", "-b", "l2", "l1")
	writeCommit(t, repo, "b.txt", "b", "l2 work")
	gitT(t, repo, "checkout", "-q", "main")
	gitT(t, repo, "push", "-q", "origin", "main", "l1", "l2")

	c := New(repo)
	if err := c.Init("main", "l1", "l2"); err != nil {
		t.Fatalf("gh stack init: %v", err)
	}
	return repo
}

func writeCommit(t *testing.T, repo, file, content, msg string) {
	t.Helper()
	if err := writeFile(filepath.Join(repo, file), content); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", msg)
}

func TestReadState_ParsesLinearStack(t *testing.T) {
	repo := newStackRepo(t)
	st, err := New(repo).ReadState()
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st == nil || len(st.Stacks) != 1 {
		t.Fatalf("expected 1 stack, got %+v", st)
	}
	if got := st.Stacks[0].BranchNames(); len(got) != 2 || got[0] != "l1" || got[1] != "l2" {
		t.Fatalf("branch names = %v, want [l1 l2]", got)
	}
}

func TestView_ParsesCurrentStack(t *testing.T) {
	repo := newStackRepo(t)
	gitT(t, repo, "checkout", "-q", "l2")
	v, err := New(repo).View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if v.Trunk != "main" {
		t.Errorf("trunk = %q, want main", v.Trunk)
	}
	if len(v.Branches) != 2 {
		t.Fatalf("branches = %d, want 2", len(v.Branches))
	}
	if v.CurrentBranch != "l2" {
		t.Errorf("currentBranch = %q, want l2", v.CurrentBranch)
	}
}

// TestWorktreeCollision_And_Rescue is the load-bearing test: it proves both the
// failure mode (gh-stack rebase collides with a per-branch worktree) and that
// WithDetachedWorktrees rescues it. If gh-stack ever changes so the raw rebase
// stops colliding, the first assertion flags it so we can drop the choreography.
func TestWorktreeCollision_And_Rescue(t *testing.T) {
	repo := newStackRepo(t)
	// Advance main so a real rebase is warranted.
	gitT(t, repo, "checkout", "-q", "main")
	writeCommit(t, repo, "m2.txt", "m2", "main advance")
	gitT(t, repo, "push", "-q", "origin", "main")

	// Put l2 in its own worktree, ezstack-style.
	wt := filepath.Join(filepath.Dir(repo), "wt-l2")
	gitT(t, repo, "worktree", "add", "-q", wt, "l2")

	c := New(repo)

	// 1) Raw rebase must collide because l2 is checked out in wt.
	rawErr := c.Rebase(RebaseOptions{NoTrunk: true})
	if rawErr == nil {
		t.Fatal("expected raw rebase to collide with worktree, but it succeeded")
	}

	// 2) Wrapped in the choreography it must succeed and re-attach l2.
	err := WithDetachedWorktrees(repo, []string{"l1", "l2"}, func() error {
		return c.Rebase(RebaseOptions{NoTrunk: true})
	})
	if err != nil {
		t.Fatalf("rebase within WithDetachedWorktrees: %v", err)
	}

	// l2 worktree must be re-attached to branch l2 (not left detached).
	head := gitT(t, wt, "rev-parse", "--abbrev-ref", "HEAD")
	if head != "l2" {
		t.Fatalf("worktree HEAD = %q after rescue, want branch l2 (re-attached)", head)
	}
}

func TestExitCodeErr(t *testing.T) {
	cases := map[int]error{
		2: ErrNotInStack,
		3: ErrRebaseConflict,
		4: ErrGitHubAPI,
		5: ErrInvalidArgs,
		6: ErrDisambiguation,
		7: ErrRebaseActive,
		8: ErrLocked,
	}
	for code, want := range cases {
		if got := exitCodeErr(code); !errors.Is(got, want) {
			t.Errorf("exitCodeErr(%d) = %v, want %v", code, got, want)
		}
	}
	for _, code := range []int{0, 1, 99} {
		if got := exitCodeErr(code); got != nil {
			t.Errorf("exitCodeErr(%d) = %v, want nil (generic)", code, got)
		}
	}
}
