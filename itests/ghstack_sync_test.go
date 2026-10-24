package itests

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/ghstack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
)

// These tests drive the real ezs binary against a repo with a bare local
// origin. The stub gh (test/fixtures/stub_gh.sh) forwards `gh stack` to the
// real github/gh-stack extension, so rebases and pushes run through the same
// engine users get; PR/API calls stay stubbed. They skip when gh-stack >= 0.2
// isn't installed (CI installs it).

// ghFixture is an isolated repo + bare origin driven through the ezs binary.
type ghFixture struct {
	t    *testing.T
	env  *TestEnv
	bare string
	bin  string
}

func newGHFixture(t *testing.T) *ghFixture {
	t.Helper()
	env := SetupTestEnv(t)
	t.Cleanup(env.Cleanup)
	if err := ghstack.Check(); err != nil {
		t.Skipf("gh stack unavailable: %v", err)
	}
	f := &ghFixture{t: t, env: env, bare: filepath.Join(env.TmpDir, "origin.git"), bin: buildEzsBinary(t)}
	mustRunGit(t, "", "init", "-q", "--bare", "-b", "main", f.bare)
	mustRunGit(t, env.RepoDir, "remote", "add", "origin", f.bare)
	mustRunGit(t, env.RepoDir, "push", "-q", "-u", "origin", "main")
	return f
}

// ezs runs the binary in dir (with -y) and returns combined output and the
// exit code.
func (f *ghFixture) ezs(dir string, extraEnv []string, args ...string) (string, int) {
	f.t.Helper()
	cmd := exec.Command(f.bin, append([]string{"-y"}, args...)...)
	cmd.Dir = dir
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(), "EZSTACK_HOME="+f.env.ConfigDir)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		f.t.Fatalf("run ezs %v: %v", args, err)
	}
	return string(out), code
}

// mustEzs is ezs that fails the test on a non-zero exit.
func (f *ghFixture) mustEzs(dir string, args ...string) string {
	f.t.Helper()
	out, code := f.ezs(dir, nil, args...)
	if code != 0 {
		f.t.Fatalf("ezs %v exited %d:\n%s", args, code, out)
	}
	return out
}

// branch creates name off parent (in its own worktree) with one commit on
// <name>.txt and returns the worktree path.
func (f *ghFixture) branch(name, parent string) string {
	f.t.Helper()
	mgr, err := stack.NewManager(f.env.RepoDir)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := mgr.CreateBranch(name, parent, "", ""); err != nil {
		f.t.Fatalf("CreateBranch %s: %v", name, err)
	}
	wt := f.wt(name)
	GitCommit(f.t, wt, name+".txt", name+"\n", name+" work")
	return wt
}

func (f *ghFixture) wt(name string) string {
	p, _ := filepath.EvalSymlinks(filepath.Join(f.env.WorktreeDir, name))
	return p
}

// advanceMain lands a commit on origin/main (and local main).
func (f *ghFixture) advanceMain(file, content string) {
	f.t.Helper()
	GitCommit(f.t, f.env.RepoDir, file, content, "main: "+file)
	mustRunGit(f.t, f.env.RepoDir, "push", "-q", "origin", "main")
}

func (f *ghFixture) rev(dir, ref string) string {
	f.t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", ref).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (f *ghFixture) isAncestor(a, b string) bool {
	return exec.Command("git", "-C", f.env.RepoDir, "merge-base", "--is-ancestor", a, b).Run() == nil
}

func (f *ghFixture) count(rng string) string {
	f.t.Helper()
	out, err := exec.Command("git", "-C", f.env.RepoDir, "rev-list", "--count", rng).Output()
	if err != nil {
		f.t.Fatalf("rev-list %s: %v", rng, err)
	}
	return strings.TrimSpace(string(out))
}

func (f *ghFixture) remote(branch string) string { return f.rev(f.bare, "refs/heads/"+branch) }

// TestGHStackSync_LinearAfterMainAdvances: a → b, main advances; one sync
// restacks both in their worktrees and pushes them.
func TestGHStackSync_LinearAfterMainAdvances(t *testing.T) {
	f := newGHFixture(t)
	f.branch("a", "main")
	bWT := f.branch("b", "a")
	f.advanceMain("m2.txt", "m2\n")

	f.mustEzs(bWT, "sync", "-s")

	if !f.isAncestor("origin/main", "a") || !f.isAncestor("a", "b") {
		t.Fatal("stack not restacked onto the new main")
	}
	for _, b := range []string{"a", "b"} {
		if got, want := f.remote(b), f.rev(f.env.RepoDir, b); got != want {
			t.Errorf("origin/%s = %q, want local %q", b, got, want)
		}
		if got := strings.TrimSpace(gitOut(t, f.wt(b), "branch", "--show-current")); got != b {
			t.Errorf("worktree %s is on %q after sync", b, got)
		}
	}
	commonDir := strings.TrimSpace(gitOut(t, f.env.RepoDir, "rev-parse", "--path-format=absolute", "--git-common-dir"))
	if _, err := os.Stat(filepath.Join(commonDir, "gh-stack")); err != nil {
		t.Errorf("gh-stack catalog not written: %v", err)
	}
}

// TestGHStackSync_TreeForkAmendedForkPoint: a has two children; a is amended
// (rewritten) and main advances. Both children must be restacked onto the
// new a with exactly their own commit — the old a must not be replayed.
func TestGHStackSync_TreeForkAmendedForkPoint(t *testing.T) {
	f := newGHFixture(t)
	aWT := f.branch("a", "main")
	f.branch("b1", "a")
	f.branch("b2", "a")
	f.mustEzs(aWT, "sync", "-s") // publish everything once

	oldA := f.rev(aWT, "HEAD")
	if err := os.WriteFile(filepath.Join(aWT, "a.txt"), []byte("a amended\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, aWT, "commit", "-q", "-a", "--amend", "-m", "a amended")
	f.advanceMain("m2.txt", "m2\n")

	f.mustEzs(aWT, "sync", "-s")

	if f.isAncestor(oldA, "a") {
		t.Fatal("fixture: a was not rewritten")
	}
	if !f.isAncestor("origin/main", "a") {
		t.Error("a not rebased onto main")
	}
	for _, b := range []string{"b1", "b2"} {
		if !f.isAncestor("a", b) {
			t.Errorf("%s not restacked onto amended a", b)
		}
		if n := f.count("a.." + b); n != "1" {
			t.Errorf("%s has %s commits over a, want 1 (old a replayed?)", b, n)
		}
		if f.remote(b) != f.rev(f.env.RepoDir, b) {
			t.Errorf("%s not pushed after restack", b)
		}
	}
}

// conflictStack builds a → b where a and main both edit shared.txt, so a
// sync stops on a conflict in a's worktree.
func conflictStack(t *testing.T, f *ghFixture) (aWT, bWT string) {
	t.Helper()
	GitCommit(t, f.env.RepoDir, "shared.txt", "v0\n", "seed shared")
	mustRunGit(t, f.env.RepoDir, "push", "-q", "origin", "main")
	aWT = f.branch("a", "main")
	GitCommit(t, aWT, "shared.txt", "from a\n", "a edits shared")
	bWT = f.branch("b", "a")
	f.advanceMain("shared.txt", "from main\n")
	return aWT, bWT
}

// TestGHStackSync_ConflictContinue: the conflict exits 3 and pauses in a's
// worktree; after resolving, `ezs sync --continue` finishes the cascade.
func TestGHStackSync_ConflictContinue(t *testing.T) {
	f := newGHFixture(t)
	aWT, bWT := conflictStack(t, f)
	t.Cleanup(func() { f.ezs(bWT, nil, "sync", "--abort") })

	out, code := f.ezs(bWT, nil, "sync", "-s")
	if code != 3 {
		t.Fatalf("conflicting sync exited %d, want 3 (ExitConflict):\n%s", code, out)
	}
	if !strings.Contains(out, "ezs sync --continue") {
		t.Errorf("conflict output should point at `ezs sync --continue`:\n%s", out)
	}
	if strings.Contains(out, "gh stack rebase --continue") {
		t.Errorf("gh-stack's own continue hint leaked into ezs output:\n%s", out)
	}

	// A second plain sync while paused is refused with the same exit code.
	if out, code := f.ezs(bWT, nil, "sync", "-s"); code != 3 {
		t.Errorf("sync during a paused rebase exited %d, want 3:\n%s", code, out)
	}

	if err := os.WriteFile(filepath.Join(aWT, "shared.txt"), []byte("resolved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, aWT, "add", "shared.txt")
	f.mustEzs(bWT, "sync", "--continue")

	if !f.isAncestor("origin/main", "a") || !f.isAncestor("a", "b") {
		t.Fatal("cascade did not complete after --continue")
	}
	if got := strings.TrimSpace(gitOut(t, aWT, "branch", "--show-current")); got != "a" {
		t.Errorf("worktree a is on %q after continue", got)
	}
	if out, code := f.ezs(bWT, nil, "sync", "--continue"); code == 0 {
		t.Errorf("--continue with nothing paused should fail:\n%s", out)
	}
}

// TestGHStackSync_ConflictAbort: --abort restores every branch.
func TestGHStackSync_ConflictAbort(t *testing.T) {
	f := newGHFixture(t)
	aWT, bWT := conflictStack(t, f)
	aBefore, bBefore := f.rev(aWT, "a"), f.rev(aWT, "b")

	if out, code := f.ezs(bWT, nil, "sync", "-s"); code != 3 {
		t.Fatalf("conflicting sync exited %d, want 3:\n%s", code, out)
	}
	f.mustEzs(bWT, "sync", "--abort")

	if f.rev(aWT, "a") != aBefore || f.rev(aWT, "b") != bBefore {
		t.Fatal("--abort did not restore branches")
	}
	if st := gitOut(t, aWT, "status", "--porcelain"); strings.TrimSpace(st) != "" {
		t.Errorf("worktree a dirty after abort:\n%s", st)
	}
}

// TestGHStackSync_SquashMergedParent: a (two commits) is squash-merged into
// main and pruned locally. ezs records it as merged (with its PR), the
// projection keeps it as a merged layer, and gh-stack moves b onto main with
// only b's own commit.
//
// The merged state is injected through ezstack's cache (what `ezs sync`'s
// merged-branch cleanup records) rather than through gh-stack's own GitHub
// PR refresh, which needs a real GitHub remote.
func TestGHStackSync_SquashMergedParent(t *testing.T) {
	f := newGHFixture(t)
	aWT := f.branch("a", "main")
	GitCommit(t, aWT, "a.txt", "a\nmore\n", "a second commit")
	bWT := f.branch("b", "a")
	f.mustEzs(bWT, "sync", "-s") // record bases in the catalog, publish

	// Squash-merge a on "GitHub".
	mustRunGit(t, f.env.RepoDir, "merge", "-q", "--squash", "a")
	mustRunGit(t, f.env.RepoDir, "commit", "-q", "-m", "a (#100)")
	mustRunGit(t, f.env.RepoDir, "push", "-q", "origin", "main")

	mgr, err := stack.NewManager(f.env.RepoDir)
	if err != nil {
		t.Fatal(err)
	}
	sc := mgr.GetStackConfig()
	sc.Cache.GetBranchCache("a").PRUrl = "https://github.com/testorg/testrepo/pull/100"
	if err := sc.Save(f.env.RepoDir); err != nil {
		t.Fatal(err)
	}
	mgr, _ = stack.NewManager(f.env.RepoDir)
	if err := mgr.MarkBranchMerged("a"); err != nil {
		t.Fatalf("MarkBranchMerged: %v", err)
	}

	f.mustEzs(bWT, "sync", "-s")

	if !f.isAncestor("origin/main", "b") {
		t.Fatal("b not moved onto main")
	}
	if n := f.count("origin/main..b"); n != "1" {
		t.Errorf("b has %s commits over main, want 1 (merged parent replayed?)", n)
	}
}

// TestGHStackReparent_MovesOnlyOwnCommits: b moves from a onto c; only b's
// commit travels.
func TestGHStackReparent_MovesOnlyOwnCommits(t *testing.T) {
	f := newGHFixture(t)
	f.branch("a", "main")
	bWT := f.branch("b", "a")
	f.branch("c", "main")

	f.mustEzs(bWT, "reparent", "b", "c")

	if !f.isAncestor("c", "b") {
		t.Fatal("b not moved onto c")
	}
	if f.isAncestor("a", "b") {
		t.Error("a's commits were carried onto c")
	}
	if n := f.count("c..b"); n != "1" {
		t.Errorf("b has %s commits over c, want 1", n)
	}
}

// TestGHStackPush_Stack: `ezs push -s` publishes every branch of the stack.
func TestGHStackPush_Stack(t *testing.T) {
	f := newGHFixture(t)
	f.branch("a", "main")
	bWT := f.branch("b", "a")

	f.mustEzs(bWT, "push", "-s")

	for _, b := range []string{"a", "b"} {
		if got, want := f.remote(b), f.rev(f.env.RepoDir, b); got == "" || got != want {
			t.Errorf("origin/%s = %q, want %q", b, got, want)
		}
	}
}

// TestGHStackSync_DryRunJSON: --dry-run --json reports what would move and
// changes nothing.
func TestGHStackSync_DryRunJSON(t *testing.T) {
	f := newGHFixture(t)
	f.branch("a", "main")
	bWT := f.branch("b", "a")
	f.advanceMain("m2.txt", "m2\n")
	aBefore, bBefore := f.rev(bWT, "a"), f.rev(bWT, "b")

	cmd := exec.Command(f.bin, "sync", "--dry-run", "--json")
	cmd.Dir = bWT
	cmd.Env = append(os.Environ(), "EZSTACK_HOME="+f.env.ConfigDir)
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	var infos []map[string]any
	if err := json.Unmarshal(stdout, &infos); err != nil {
		t.Fatalf("dry-run stdout is not a JSON array: %v\n%s", err, stdout)
	}
	if len(infos) != 1 {
		t.Fatalf("infos = %v, want exactly a", infos)
	}
	got := infos[0]
	if got["branch"] != "a" || got["needs_sync"] != true || got["behind_by"] != float64(1) || got["stack_root"] != "main" {
		t.Errorf("unexpected dry-run entry: %v", got)
	}
	if f.rev(bWT, "a") != aBefore || f.rev(bWT, "b") != bBefore {
		t.Error("dry-run moved branches")
	}
}

// TestGHStackSync_DirtyWorktreeBlocks: gh-stack refuses up front when an
// affected worktree has uncommitted changes; nothing is rewritten.
func TestGHStackSync_DirtyWorktreeBlocks(t *testing.T) {
	f := newGHFixture(t)
	f.branch("a", "main")
	bWT := f.branch("b", "a")
	f.advanceMain("m2.txt", "m2\n")
	if err := os.WriteFile(filepath.Join(bWT, "b.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	aBefore, bBefore := f.rev(bWT, "a"), f.rev(bWT, "b")

	out, code := f.ezs(bWT, nil, "sync", "-s")
	if code == 0 {
		t.Fatalf("sync with a dirty worktree succeeded:\n%s", out)
	}
	if !strings.Contains(out, "uncommitted changes") {
		t.Errorf("expected an uncommitted-changes message:\n%s", out)
	}
	if f.rev(bWT, "a") != aBefore || f.rev(bWT, "b") != bBefore {
		t.Error("branches were rewritten despite the dirty worktree")
	}
}

// TestGHStackSync_NoPush: --no-push and the agent no-push env both rebase
// locally without touching origin.
func TestGHStackSync_NoPush(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  []string
	}{
		{"flag", []string{"sync", "-s", "--no-push"}, nil},
		{"agent-env", []string{"sync", "-s"}, []string{"EZS_AGENT_NO_PUSH=1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGHFixture(t)
			f.branch("a", "main")
			bWT := f.branch("b", "a")
			f.mustEzs(bWT, "push", "-s")
			aRemote := f.remote("a")
			f.advanceMain("m2.txt", "m2\n")

			if out, code := f.ezs(bWT, tc.env, tc.args...); code != 0 {
				t.Fatalf("sync exited %d:\n%s", code, out)
			}
			if !f.isAncestor("origin/main", "a") {
				t.Fatal("a not rebased locally")
			}
			if f.remote("a") != aRemote {
				t.Error("origin/a changed despite no-push")
			}
		})
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// TestGHStackCatalog_DeletedBranchDropped: after `ezs delete`, the next
// gh-stack operation no longer tracks the deleted branch.
func TestGHStackCatalog_DeletedBranchDropped(t *testing.T) {
	f := newGHFixture(t)
	aWT := f.branch("a", "main")
	f.branch("b", "a")
	f.mustEzs(aWT, "sync", "-s", "--no-push")

	f.mustEzs(f.env.RepoDir, "delete", "b")
	f.mustEzs(aWT, "sync", "-s", "--no-push")

	commonDir := strings.TrimSpace(gitOut(t, f.env.RepoDir, "rev-parse", "--path-format=absolute", "--git-common-dir"))
	cat, err := ghstack.LoadCatalog(commonDir)
	if err != nil {
		t.Fatal(err)
	}
	var layers []string
	for _, s := range cat.Stacks {
		layers = append(layers, s.BranchNames()...)
	}
	if strings.Join(layers, ",") != "a" {
		t.Errorf("catalog layers = %v, want [a]", layers)
	}
}

// collaboratorPush clones origin, commits file on branch, and pushes it —
// a teammate pushing to the PR branch.
func (f *ghFixture) collaboratorPush(branch, file, content string) string {
	f.t.Helper()
	dir := filepath.Join(f.env.TmpDir, "collab-"+branch)
	mustRunGit(f.t, "", "clone", "-q", "-b", branch, f.bare, dir)
	mustRunGit(f.t, dir, "config", "user.email", TestUserEmail)
	mustRunGit(f.t, dir, "config", "user.name", TestUserName)
	GitCommit(f.t, dir, file, content, "collaborator: "+file)
	mustRunGit(f.t, dir, "push", "-q", "origin", branch)
	return f.rev(dir, "HEAD")
}

// markMerged records branch as merged with a PR, as `ezs sync`'s merged
// cleanup does after GitHub reports the PR merged (deletes branch+worktree).
func (f *ghFixture) markMerged(branch string, pr int) {
	f.t.Helper()
	mgr, err := stack.NewManager(f.env.RepoDir)
	if err != nil {
		f.t.Fatal(err)
	}
	sc := mgr.GetStackConfig()
	sc.Cache.GetBranchCache(branch).PRUrl = "https://github.com/testorg/testrepo/pull/" + strconv.Itoa(pr)
	if err := sc.Save(f.env.RepoDir); err != nil {
		f.t.Fatal(err)
	}
	mgr, _ = stack.NewManager(f.env.RepoDir)
	if err := mgr.MarkBranchMerged(branch); err != nil {
		f.t.Fatalf("MarkBranchMerged(%s): %v", branch, err)
	}
}

// TestGHStackReparent_OntoMainAndGrandparent: b moves from a onto main, and
// (separately) c moves from b onto its grandparent a — each carries only its
// own commit.
func TestGHStackReparent_OntoMainAndGrandparent(t *testing.T) {
	t.Run("onto main", func(t *testing.T) {
		f := newGHFixture(t)
		f.branch("a", "main")
		bWT := f.branch("b", "a")

		f.mustEzs(bWT, "reparent", "b", "main")

		if f.isAncestor("a", "b") {
			t.Error("a's commit was carried along onto main")
		}
		if n := f.count("main..b"); n != "1" {
			t.Errorf("b has %s commits over main, want 1", n)
		}
	})
	t.Run("onto grandparent", func(t *testing.T) {
		f := newGHFixture(t)
		f.branch("a", "main")
		f.branch("b", "a")
		cWT := f.branch("c", "b")

		f.mustEzs(cWT, "reparent", "c", "a")

		if f.isAncestor("b", "c") {
			t.Error("b's commit was kept under c")
		}
		if !f.isAncestor("a", "c") {
			t.Error("c not on a")
		}
		if n := f.count("a..c"); n != "1" {
			t.Errorf("c has %s commits over a, want 1", n)
		}
	})
}

// TestGHStackSync_CollaboratorPushFastForwarded: a teammate pushed to
// origin/b; sync fast-forwards b first, so their commit survives the restack
// and the push.
func TestGHStackSync_CollaboratorPushFastForwarded(t *testing.T) {
	f := newGHFixture(t)
	f.branch("a", "main")
	bWT := f.branch("b", "a")
	f.mustEzs(bWT, "push", "-s")
	collab := f.collaboratorPush("b", "collab.txt", "teammate\n")
	f.advanceMain("m2.txt", "m2\n")

	f.mustEzs(bWT, "sync", "-s")

	if !f.isAncestor("origin/main", "b") {
		t.Error("b not restacked onto main")
	}
	if _, err := os.Stat(filepath.Join(bWT, "collab.txt")); err != nil {
		t.Errorf("collaborator's change lost from b (commit %s): %v", collab, err)
	}
	if f.remote("b") != f.rev(f.env.RepoDir, "b") {
		t.Error("b not pushed after restack")
	}
}

// TestGHStackSync_DivergedRemoteNotForcePushed: b has a local rewrite AND a
// teammate's new commit on origin/b; sync must not force-push over it.
func TestGHStackSync_DivergedRemoteNotForcePushed(t *testing.T) {
	f := newGHFixture(t)
	f.branch("a", "main")
	bWT := f.branch("b", "a")
	f.mustEzs(bWT, "push", "-s")
	collab := f.collaboratorPush("b", "collab.txt", "teammate\n")
	GitCommit(t, bWT, "b.txt", "rewritten\n", "unused")
	mustRunGit(t, bWT, "reset", "-q", "--soft", "HEAD~2")
	mustRunGit(t, bWT, "commit", "-q", "-m", "b rewritten")
	f.advanceMain("m2.txt", "m2\n")

	out, _ := f.ezs(bWT, nil, "sync", "-s")

	if got := f.remote("b"); got != collab {
		t.Errorf("origin/b = %s, want collaborator commit %s preserved:\n%s", got, collab, out)
	}
}

// TestGHStackSync_ForkWithMergedFirstChild: a's first child (b1) was merged
// and pruned; the sibling b2 must still restack (gh-stack anchored on b2's
// chain, not on the shared fork point).
func TestGHStackSync_ForkWithMergedFirstChild(t *testing.T) {
	f := newGHFixture(t)
	aWT := f.branch("a", "main")
	f.branch("b1", "a")
	f.branch("b2", "a")
	f.mustEzs(aWT, "sync", "-s")
	f.markMerged("b1", 101)
	f.advanceMain("m2.txt", "m2\n")

	f.mustEzs(aWT, "sync", "-s")

	if !f.isAncestor("origin/main", "a") || !f.isAncestor("a", "b2") {
		t.Error("a/b2 not restacked")
	}
	if n := f.count("a..b2"); n != "1" {
		t.Errorf("b2 has %s commits over a, want 1", n)
	}
}

// TestGHStackCommit_ConflictContinueResumesChildrenOnly: `ezs commit` on a
// conflicts while restacking b; `ezs sync --continue` (no flags) finishes
// just that children restack — it must not pull in main, which advanced in
// the meantime.
func TestGHStackCommit_ConflictContinueResumesChildrenOnly(t *testing.T) {
	f := newGHFixture(t)
	aWT := f.branch("a", "main")
	bWT := f.branch("b", "a")
	GitCommit(t, bWT, "a.txt", "b's version\n", "b edits a.txt")
	t.Cleanup(func() { f.ezs(aWT, nil, "sync", "--abort") })

	if err := os.WriteFile(filepath.Join(aWT, "a.txt"), []byte("a's new version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, aWT, "add", "a.txt")
	out, _ := f.ezs(aWT, nil, "commit", "-m", "a edits a.txt", "--no-push")
	if !strings.Contains(out, "ezs sync --continue") {
		t.Fatalf("commit restack should stop on a conflict pointing at --continue:\n%s", out)
	}
	f.advanceMain("m2.txt", "m2\n")

	if err := os.WriteFile(filepath.Join(bWT, "a.txt"), []byte("resolved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, bWT, "add", "a.txt")
	f.mustEzs(aWT, "sync", "--continue")

	if !f.isAncestor("a", "b") {
		t.Error("b not restacked onto a after --continue")
	}
	if f.isAncestor("origin/main", "a") {
		t.Error("--continue widened scope: a was rebased onto the newly advanced main")
	}
}

// TestGHStackPush_SetsUpstream: `ezs push -s` leaves each branch tracking
// its remote branch.
func TestGHStackPush_SetsUpstream(t *testing.T) {
	f := newGHFixture(t)
	f.branch("a", "main")
	bWT := f.branch("b", "a")

	f.mustEzs(bWT, "push", "-s")

	for _, b := range []string{"a", "b"} {
		if got := strings.TrimSpace(gitOut(t, f.env.RepoDir, "rev-parse", "--abbrev-ref", b+"@{upstream}")); got != "origin/"+b {
			t.Errorf("%s upstream = %q, want origin/%s", b, got, b)
		}
	}
}
