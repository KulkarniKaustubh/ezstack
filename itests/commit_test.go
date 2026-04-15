package itests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
)

// setupCommitTestStack creates a parent/child stack wired to a bare origin
// remote. Both branches are pushed to origin so the `ezs commit` push-child
// path has something to act on. Returns the parent worktree path (where the
// test will call commands.Commit from), the child worktree path, and the
// bare origin path.
func setupCommitTestStack(t *testing.T, env *TestEnv) (parentWT, childWT, bare string) {
	t.Helper()

	// Create a bare repo to act as the remote.
	bare = filepath.Join(env.TmpDir, "bare.git")
	if err := exec.Command("git", "init", "--bare", "-b", "main", bare).Run(); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	// Wire up origin and push main so origin/main exists.
	if err := exec.Command("git", "-C", env.RepoDir, "remote", "add", "origin", bare).Run(); err != nil {
		t.Fatalf("remote add: %v", err)
	}
	if err := exec.Command("git", "-C", env.RepoDir, "push", "-u", "origin", "main").Run(); err != nil {
		t.Fatalf("push main: %v", err)
	}

	// Create parent (with worktree), add a commit, push it to origin.
	CreateBranchWithCommit(t, env, "parent", "main")
	parentWT = filepath.Join(env.WorktreeDir, "parent")
	if err := exec.Command("git", "-C", parentWT, "push", "-u", "origin", "parent").Run(); err != nil {
		t.Fatalf("push parent: %v", err)
	}

	// Create child off parent, push it too.
	CreateBranchWithCommit(t, env, "child", "parent")
	childWT = filepath.Join(env.WorktreeDir, "child")
	if err := exec.Command("git", "-C", childWT, "push", "-u", "origin", "child").Run(); err != nil {
		t.Fatalf("push child: %v", err)
	}

	// Sanity: fetch so origin/* refs are populated in main repo.
	if err := exec.Command("git", "-C", env.RepoDir, "fetch", "origin").Run(); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	return parentWT, childWT, bare
}

// revParse returns the commit SHA the given ref resolves to in the given
// git directory, or t.Fatal on failure.
func revParse(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("rev-parse %s in %s: %v", ref, dir, err)
	}
	return strings.TrimSpace(string(out))
}

// stageNewFile writes and stages a new file in `dir`. commands.Commit will
// then pick this up when it runs `git commit`.
func stageNewFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := exec.Command("git", "-C", dir, "add", name).Run(); err != nil {
		t.Fatalf("git add %s: %v", name, err)
	}
}

// chdirForTest switches cwd to `dir` and restores it on cleanup. commands.Commit
// reads os.Getwd() to determine which repo it's operating on.
func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// TestCommit_NoPushFlag: --no-push must not touch origin, and must still
// sync child branches locally.
func TestCommit_NoPushFlag(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	parentWT, childWT, bare := setupCommitTestStack(t, env)

	originParentBefore := revParse(t, bare, "parent")
	originChildBefore := revParse(t, bare, "child")
	childLocalBefore := revParse(t, childWT, "HEAD")

	stageNewFile(t, parentWT, "no-push.txt", "no push body\n")
	chdirForTest(t, parentWT)

	if err := commands.Commit([]string{"-m", "parent: no push change", "--no-push"}); err != nil {
		t.Fatalf("commands.Commit: %v", err)
	}

	// Origin must be untouched.
	if got := revParse(t, bare, "parent"); got != originParentBefore {
		t.Errorf("origin/parent moved despite --no-push: before=%s after=%s", originParentBefore, got)
	}
	if got := revParse(t, bare, "child"); got != originChildBefore {
		t.Errorf("origin/child moved despite --no-push: before=%s after=%s", originChildBefore, got)
	}

	// Parent local must have advanced (the commit happened).
	parentLocalAfter := revParse(t, parentWT, "HEAD")
	if parentLocalAfter == originParentBefore {
		t.Errorf("parent HEAD did not advance; commit never happened")
	}

	// Child local must have been synced onto new parent (rebase).
	childLocalAfter := revParse(t, childWT, "HEAD")
	if childLocalAfter == childLocalBefore {
		t.Errorf("child was not rebased onto updated parent")
	}
}

// TestCommit_PushFlagOnly: --push pushes current branch only. Child is
// rebased locally but origin/child must remain stale.
func TestCommit_PushFlagOnly(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	parentWT, childWT, bare := setupCommitTestStack(t, env)

	originChildBefore := revParse(t, bare, "child")

	stageNewFile(t, parentWT, "push-only.txt", "push only\n")
	chdirForTest(t, parentWT)

	if err := commands.Commit([]string{"-m", "parent: push only", "--push"}); err != nil {
		t.Fatalf("commands.Commit: %v", err)
	}

	// Parent should be pushed — origin/parent matches local parent HEAD.
	parentLocal := revParse(t, parentWT, "HEAD")
	if got := revParse(t, bare, "parent"); got != parentLocal {
		t.Errorf("origin/parent = %s, want %s (local parent HEAD)", got, parentLocal)
	}

	// Child origin stays stale (rebase happened locally but no --push-children).
	if got := revParse(t, bare, "child"); got != originChildBefore {
		t.Errorf("origin/child changed without --push-children: before=%s after=%s", originChildBefore, got)
	}

	// Local child still rebased though.
	childLocal := revParse(t, childWT, "HEAD")
	if childLocal == originChildBefore {
		t.Errorf("child was not rebased locally")
	}
}

// TestCommit_PushChildrenFlag: --push --push-children pushes both parent
// and each rebased child.
func TestCommit_PushChildrenFlag(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	parentWT, childWT, bare := setupCommitTestStack(t, env)

	stageNewFile(t, parentWT, "push-children.txt", "push children\n")
	chdirForTest(t, parentWT)

	if err := commands.Commit([]string{"-m", "parent: push children", "--push", "--push-children"}); err != nil {
		t.Fatalf("commands.Commit: %v", err)
	}

	// origin/parent must match local parent HEAD.
	parentLocal := revParse(t, parentWT, "HEAD")
	if got := revParse(t, bare, "parent"); got != parentLocal {
		t.Errorf("origin/parent = %s, want %s", got, parentLocal)
	}

	// origin/child must match the freshly rebased local child HEAD.
	childLocal := revParse(t, childWT, "HEAD")
	if got := revParse(t, bare, "child"); got != childLocal {
		t.Errorf("origin/child = %s, want %s (local rebased child)", got, childLocal)
	}
}

// TestCommit_PushChildrenFlag_Merge: same as above but with --merge. We want
// to prove the merge path also ends up with origin/child in sync, and that
// no force push is required (merge only adds commits).
func TestCommit_PushChildrenFlag_Merge(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	parentWT, childWT, bare := setupCommitTestStack(t, env)

	stageNewFile(t, parentWT, "push-children-merge.txt", "merge body\n")
	chdirForTest(t, parentWT)

	if err := commands.Commit([]string{"-m", "parent: merge sync", "--merge", "--push", "--push-children"}); err != nil {
		t.Fatalf("commands.Commit: %v", err)
	}

	parentLocal := revParse(t, parentWT, "HEAD")
	if got := revParse(t, bare, "parent"); got != parentLocal {
		t.Errorf("origin/parent = %s, want %s", got, parentLocal)
	}
	childLocal := revParse(t, childWT, "HEAD")
	if got := revParse(t, bare, "child"); got != childLocal {
		t.Errorf("origin/child = %s, want %s", got, childLocal)
	}
}

// TestCommit_PushChildrenSkipsUnpushedChild: a child that has never been
// pushed to origin must stay local even when --push-children is set.
// commands.Commit should not silently publish brand-new branches.
func TestCommit_PushChildrenSkipsUnpushedChild(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	parentWT, _, bare := setupCommitTestStack(t, env)

	// Create a second child that was never pushed.
	CreateBranchWithCommit(t, env, "child2", "parent")

	// Make sure origin does not have child2.
	if out, _ := exec.Command("git", "-C", bare, "rev-parse", "--verify", "child2").CombinedOutput(); len(out) > 0 && !strings.HasPrefix(string(out), "fatal") {
		// It's fine if rev-parse errors; we just want to confirm no ref.
	}

	stageNewFile(t, parentWT, "skip-unpushed.txt", "x\n")
	chdirForTest(t, parentWT)

	if err := commands.Commit([]string{"-m", "parent: skip unpushed", "--push", "--push-children"}); err != nil {
		t.Fatalf("commands.Commit: %v", err)
	}

	// child (which was pushed before) should be in sync.
	childWT := filepath.Join(env.WorktreeDir, "child")
	childLocal := revParse(t, childWT, "HEAD")
	if got := revParse(t, bare, "child"); got != childLocal {
		t.Errorf("origin/child = %s, want %s", got, childLocal)
	}

	// child2 must not exist on origin — we never published it, so the guard
	// in pushChildBranches must have skipped it.
	if err := exec.Command("git", "-C", bare, "rev-parse", "--verify", "child2").Run(); err == nil {
		t.Errorf("origin/child2 exists but should not (branch never pushed before)")
	}
}

// TestCommit_FlagConflict: the parser surfaces incompatible flag combinations
// as errors without running git commit.
func TestCommit_FlagConflict(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	parentWT, _, bare := setupCommitTestStack(t, env)
	parentHeadBefore := revParse(t, parentWT, "HEAD")
	originParentBefore := revParse(t, bare, "parent")

	stageNewFile(t, parentWT, "conflict-flags.txt", "c\n")
	chdirForTest(t, parentWT)

	err := commands.Commit([]string{"-m", "wont apply", "--push", "--no-push"})
	if err == nil {
		t.Fatal("expected flag-conflict error, got nil")
	}

	// git commit must not have run — parent HEAD unchanged.
	if got := revParse(t, parentWT, "HEAD"); got != parentHeadBefore {
		t.Errorf("parent HEAD advanced despite flag error: before=%s after=%s", parentHeadBefore, got)
	}
	if got := revParse(t, bare, "parent"); got != originParentBefore {
		t.Errorf("origin/parent advanced despite flag error")
	}
}
