package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// enableFileProtocol sets env vars so all git subprocesses in the current
// test allow the `file://` protocol. `git submodule add` and
// `git submodule update --init` shell out to `git clone`, which otherwise
// rejects file:// sources on git >= 2.38. `t.Setenv` resets on test exit.
func enableFileProtocol(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
}

// setupSubmoduleRepo creates a minimal git repo suitable for use as a
// submodule (has at least one commit on a branch that matches the consumer's
// default). Returns the absolute path to the repo and a cleanup func.
func setupSubmoduleRepo(t *testing.T, name string) (string, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "submodule-src-"+name+"-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	cleanup := func() { os.RemoveAll(dir) }

	mustRun := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			cleanup()
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	mustRun("init", "-b", "main")
	mustRun("config", "user.email", "sub@test.com")
	mustRun("config", "user.name", "Sub")

	readme := filepath.Join(dir, "SUB_README.md")
	if err := os.WriteFile(readme, []byte("# "+name+"\n"), 0644); err != nil {
		cleanup()
		t.Fatalf("write: %v", err)
	}
	mustRun("add", ".")
	mustRun("commit", "-m", "initial submodule commit")

	return dir, cleanup
}

// addSubmodule adds a submodule from srcPath at relPath inside the parent
// repo at parentDir. Commits the change (`git submodule add` already stages
// it).
func addSubmodule(t *testing.T, parentDir, srcPath, relPath string) {
	t.Helper()

	mustRun := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = parentDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, parentDir, err, out)
		}
	}

	mustRun("submodule", "add", srcPath, relPath)
	mustRun("commit", "-m", "add submodule "+relPath)
}

// deinitSubmodule deinitializes a submodule in parentDir so `git submodule
// status` reports it with a leading '-'.
func deinitSubmodule(t *testing.T, parentDir, relPath string) {
	t.Helper()
	cmd := exec.Command("git", "submodule", "deinit", "-f", "--", relPath)
	cmd.Dir = parentDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("submodule deinit: %v\n%s", err, out)
	}
}

func TestListInitializedSubmodules_NoSubmodules(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)
	paths, err := g.ListInitializedSubmodules()
	if err != nil {
		t.Fatalf("ListInitializedSubmodules: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("expected no submodules, got %v", paths)
	}
}

func TestListInitializedSubmodules_MissingGitmodules(t *testing.T) {
	// A bare directory shouldn't explode. Not a git repo → returns nil, nil.
	dir, err := os.MkdirTemp("", "not-a-repo-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer os.RemoveAll(dir)

	g := New(dir)
	paths, err := g.ListInitializedSubmodules()
	if err != nil {
		t.Fatalf("expected no error on non-repo, got %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("expected no paths, got %v", paths)
	}
}

func TestListInitializedSubmodules_AllInitialized(t *testing.T) {
	enableFileProtocol(t)
	parent, parentCleanup := setupTestRepo(t)
	defer parentCleanup()

	subA, cleanupA := setupSubmoduleRepo(t, "a")
	defer cleanupA()
	subB, cleanupB := setupSubmoduleRepo(t, "b")
	defer cleanupB()

	addSubmodule(t, parent, subA, "vendor/a")
	addSubmodule(t, parent, subB, "vendor/b")

	g := New(parent)
	paths, err := g.ListInitializedSubmodules()
	if err != nil {
		t.Fatalf("ListInitializedSubmodules: %v", err)
	}
	sort.Strings(paths)
	want := []string{"vendor/a", "vendor/b"}
	if len(paths) != len(want) || paths[0] != want[0] || paths[1] != want[1] {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestListInitializedSubmodules_SomeDeinitialized(t *testing.T) {
	enableFileProtocol(t)
	parent, parentCleanup := setupTestRepo(t)
	defer parentCleanup()

	subA, cleanupA := setupSubmoduleRepo(t, "a")
	defer cleanupA()
	subB, cleanupB := setupSubmoduleRepo(t, "b")
	defer cleanupB()

	addSubmodule(t, parent, subA, "vendor/a")
	addSubmodule(t, parent, subB, "vendor/b")

	// Deinit B so it shows with leading '-' in status.
	deinitSubmodule(t, parent, "vendor/b")

	g := New(parent)
	paths, err := g.ListInitializedSubmodules()
	if err != nil {
		t.Fatalf("ListInitializedSubmodules: %v", err)
	}
	if len(paths) != 1 || paths[0] != "vendor/a" {
		t.Errorf("paths = %v, want [vendor/a]", paths)
	}
}

func TestInitSubmodules_Empty(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)
	if err := g.InitSubmodules(nil); err != nil {
		t.Errorf("InitSubmodules(nil) = %v, want nil", err)
	}
	if err := g.InitSubmodules([]string{}); err != nil {
		t.Errorf("InitSubmodules([]) = %v, want nil", err)
	}
}

func TestMirrorSubmodules_NoOpWhenSourceEmpty(t *testing.T) {
	src, srcCleanup := setupTestRepo(t)
	defer srcCleanup()
	dst, dstCleanup := setupTestRepo(t)
	defer dstCleanup()

	// Neither repo has submodules → mirror is a no-op.
	if err := MirrorSubmodules(src, dst); err != nil {
		t.Errorf("MirrorSubmodules without submodules: %v", err)
	}
}

func TestMirrorSubmodules_EndToEnd(t *testing.T) {
	// Build a parent repo with two submodules, then a worktree of that parent
	// where submodules are not yet initialized. MirrorSubmodules should
	// initialize only the ones that are init'd in the source.

	enableFileProtocol(t)
	parent, parentCleanup := setupTestRepo(t)
	defer parentCleanup()

	subA, cleanupA := setupSubmoduleRepo(t, "a")
	defer cleanupA()
	subB, cleanupB := setupSubmoduleRepo(t, "b")
	defer cleanupB()

	addSubmodule(t, parent, subA, "vendor/a")
	addSubmodule(t, parent, subB, "vendor/b")

	// Deinit B in the source so only A is "initialized".
	deinitSubmodule(t, parent, "vendor/b")

	// Create a worktree of the parent on a new branch.
	worktreePath := filepath.Join(filepath.Dir(parent), "wt-"+filepath.Base(parent))
	defer os.RemoveAll(worktreePath)

	g := New(parent)
	mainBranch, _ := g.CurrentBranch()
	if err := g.CreateWorktree("mirror-test", worktreePath, mainBranch); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// The worktree's submodules start uninitialized (git worktree add does
	// not populate submodules even if the source has them checked out).
	wt := New(worktreePath)
	preInit, err := wt.ListInitializedSubmodules()
	if err != nil {
		t.Fatalf("pre-mirror list: %v", err)
	}
	if len(preInit) != 0 {
		t.Fatalf("expected worktree to have no init'd submodules initially, got %v", preInit)
	}

	if err := MirrorSubmodules(parent, worktreePath); err != nil {
		t.Fatalf("MirrorSubmodules: %v", err)
	}

	postInit, err := wt.ListInitializedSubmodules()
	if err != nil {
		t.Fatalf("post-mirror list: %v", err)
	}
	if len(postInit) != 1 || postInit[0] != "vendor/a" {
		t.Errorf("after mirror, worktree init'd submodules = %v, want [vendor/a]", postInit)
	}

	// Submodule A's working tree should have its content populated.
	subAReadme := filepath.Join(worktreePath, "vendor/a", "SUB_README.md")
	if _, err := os.Stat(subAReadme); err != nil {
		t.Errorf("expected submodule content at %s, got %v", subAReadme, err)
	}

	// Submodule B must stay uninitialized in the mirror (directory may exist
	// as an empty gitlink, but its content should not be populated).
	subBReadme := filepath.Join(worktreePath, "vendor/b", "SUB_README.md")
	if _, err := os.Stat(subBReadme); err == nil {
		t.Errorf("expected submodule B to remain uninitialized, but %s exists", subBReadme)
	}
}

// TestListInitializedSubmodules_PathWithSpaces verifies that submodule paths
// containing spaces survive the parser. This is rare in practice but legal,
// and a naive whitespace-splitting parser would silently truncate them.
func TestListInitializedSubmodules_PathWithSpaces(t *testing.T) {
	enableFileProtocol(t)
	parent, parentCleanup := setupTestRepo(t)
	defer parentCleanup()

	src, cleanupSrc := setupSubmoduleRepo(t, "spaced")
	defer cleanupSrc()

	addSubmodule(t, parent, src, "vendor/dir with spaces")

	g := New(parent)
	paths, err := g.ListInitializedSubmodules()
	if err != nil {
		t.Fatalf("ListInitializedSubmodules: %v", err)
	}
	if len(paths) != 1 || paths[0] != "vendor/dir with spaces" {
		t.Errorf("paths = %v, want [vendor/dir with spaces]", paths)
	}
}

func TestMirrorSubmodules_RejectsEmptyPaths(t *testing.T) {
	if err := MirrorSubmodules("", "/tmp"); err == nil {
		t.Errorf("expected error on empty source path")
	}
	if err := MirrorSubmodules("/tmp", ""); err == nil {
		t.Errorf("expected error on empty dest path")
	}
}

func TestHasSubmodules(t *testing.T) {
	enableFileProtocol(t)

	dir, cleanup := setupTestRepo(t)
	defer cleanup()
	g := New(dir)
	if g.HasSubmodules() {
		t.Errorf("HasSubmodules() = true, want false on bare repo")
	}

	src, srcCleanup := setupSubmoduleRepo(t, "x")
	defer srcCleanup()
	addSubmodule(t, dir, src, "vendor/x")
	if !g.HasSubmodules() {
		t.Errorf("HasSubmodules() = false after adding submodule, want true")
	}
}

func TestUpdateSubmodulesRecursive_NoOpWhenEmpty(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()
	g := New(dir)

	// No .gitmodules — short-circuit, no error.
	if err := g.UpdateSubmodulesRecursive(); err != nil {
		t.Errorf("UpdateSubmodulesRecursive on bare repo: %v", err)
	}
}

func TestUpdateSubmodulesRecursive_AdvancesPointer(t *testing.T) {
	enableFileProtocol(t)

	parent, parentCleanup := setupTestRepo(t)
	defer parentCleanup()

	src, srcCleanup := setupSubmoduleRepo(t, "advance")
	defer srcCleanup()

	addSubmodule(t, parent, src, "vendor/x")

	subWorkingPath := filepath.Join(parent, "vendor/x")

	// Capture the pointer SHA the parent currently records.
	beforeSHA := strings.TrimSpace(mustOutput(t, subWorkingPath, "rev-parse", "HEAD"))

	// Add a new commit in the *source* repo (i.e. the submodule's upstream).
	mustRun := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "extra.txt"), []byte("hi\n"), 0644); err != nil {
		t.Fatalf("write extra: %v", err)
	}
	mustRun(src, "add", "extra.txt")
	mustRun(src, "commit", "-m", "extra")
	newSrcSHA := strings.TrimSpace(mustOutput(t, src, "rev-parse", "HEAD"))

	// Bump the parent's recorded SHA: fetch + checkout the new SHA inside
	// the submodule, then commit the gitlink change in the parent.
	mustRun(filepath.Join(parent, "vendor/x"), "fetch", "origin")
	mustRun(filepath.Join(parent, "vendor/x"), "checkout", newSrcSHA)
	mustRun(parent, "add", "vendor/x")
	mustRun(parent, "commit", "-m", "bump submodule")

	// Move the submodule working tree back to the old SHA to simulate the
	// "stale after rebase/checkout" state.
	mustRun(filepath.Join(parent, "vendor/x"), "checkout", beforeSHA)

	g := New(parent)
	if err := g.UpdateSubmodulesRecursive(); err != nil {
		t.Fatalf("UpdateSubmodulesRecursive: %v", err)
	}

	afterSHA := strings.TrimSpace(mustOutput(t, subWorkingPath, "rev-parse", "HEAD"))
	if afterSHA != newSrcSHA {
		t.Errorf("submodule SHA after update = %s, want %s (parent's recorded SHA)", afterSHA, newSrcSHA)
	}
}

func TestUpdateSubmodulesRecursive_DoesNotInitNewSubmodules(t *testing.T) {
	// If the user opted out of cloning a submodule (deinit), we must not
	// silently re-clone it during a post-rebase refresh.
	enableFileProtocol(t)

	parent, cleanup := setupTestRepo(t)
	defer cleanup()
	src, srcCleanup := setupSubmoduleRepo(t, "optout")
	defer srcCleanup()

	addSubmodule(t, parent, src, "vendor/x")
	deinitSubmodule(t, parent, "vendor/x")

	g := New(parent)
	if err := g.UpdateSubmodulesRecursive(); err != nil {
		t.Fatalf("UpdateSubmodulesRecursive: %v", err)
	}

	// vendor/x should still be empty (no SUB_README.md).
	subContent := filepath.Join(parent, "vendor/x", "SUB_README.md")
	if _, err := os.Stat(subContent); err == nil {
		t.Errorf("expected deinitialized submodule to remain empty, but %s exists", subContent)
	}
}

func TestSubmoduleStatuses_CleanInitialized(t *testing.T) {
	enableFileProtocol(t)

	parent, cleanup := setupTestRepo(t)
	defer cleanup()
	src, srcCleanup := setupSubmoduleRepo(t, "clean")
	defer srcCleanup()

	addSubmodule(t, parent, src, "vendor/x")

	g := New(parent)
	statuses, err := g.SubmoduleStatuses()
	if err != nil {
		t.Fatalf("SubmoduleStatuses: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1: %+v", len(statuses), statuses)
	}
	s := statuses[0]
	if s.Path != "vendor/x" {
		t.Errorf("Path = %q, want vendor/x", s.Path)
	}
	if s.MergeConflict || s.Dirty || s.HasUnpushed {
		t.Errorf("expected clean status, got %+v", s)
	}
	// A freshly-added submodule defaults to detached HEAD (git submodule
	// add followed by `git submodule update` leaves it on a SHA, not a
	// branch). That's normal — HasIssues should still report false.
	if s.HasIssues() {
		t.Errorf("HasIssues() = true on a freshly added submodule, want false")
	}
}

func TestSubmoduleStatuses_DirtyWorkingTree(t *testing.T) {
	enableFileProtocol(t)

	parent, cleanup := setupTestRepo(t)
	defer cleanup()
	src, srcCleanup := setupSubmoduleRepo(t, "dirty")
	defer srcCleanup()

	addSubmodule(t, parent, src, "vendor/x")

	// Dirty up the submodule.
	if err := os.WriteFile(filepath.Join(parent, "vendor/x", "SUB_README.md"), []byte("changed\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	g := New(parent)
	statuses, err := g.SubmoduleStatuses()
	if err != nil {
		t.Fatalf("SubmoduleStatuses: %v", err)
	}
	if len(statuses) != 1 || !statuses[0].Dirty {
		t.Errorf("expected dirty, got %+v", statuses)
	}
	if !statuses[0].HasIssues() {
		t.Errorf("HasIssues() = false on dirty submodule")
	}
}

func TestSubmoduleStatuses_NoSubmodules(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)
	statuses, err := g.SubmoduleStatuses()
	if err != nil {
		t.Fatalf("SubmoduleStatuses: %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("got %d statuses on bare repo, want 0", len(statuses))
	}
}

func TestSubmoduleStatuses_DetectsUnpushedInDetachedHead(t *testing.T) {
	// Regression: an earlier branch-tip-vs-origin/branch check missed the
	// case where edits were committed in detached HEAD (no branch points
	// at the new SHA), letting `ezs push` silently publish a parent that
	// records a SHA teammates can't fetch. The SHA-reachability check used
	// here flags it correctly.
	enableFileProtocol(t)

	parent, parentCleanup := setupTestRepo(t)
	defer parentCleanup()
	src, srcCleanup := setupSubmoduleRepo(t, "detached-edit")
	defer srcCleanup()

	addSubmodule(t, parent, src, "vendor/x")

	subDir := filepath.Join(parent, "vendor/x")
	mustRun := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	// Configure identity in the submodule's working tree. After `git
	// submodule add`, the submodule's config lives in .git/modules/<path>/
	// and does NOT inherit the source repo's identity. CI runners have no
	// global gitconfig, so without this `git commit` would error with
	// "Author identity unknown".
	mustRun(subDir, "config", "user.email", "test@test.com")
	mustRun(subDir, "config", "user.name", "Test User")

	// Detach HEAD, then commit a new file. The new SHA exists on no
	// branch and is not on origin.
	currentSHA := strings.TrimSpace(mustOutput(t, subDir, "rev-parse", "HEAD"))
	mustRun(subDir, "checkout", "--detach", currentSHA)
	if err := os.WriteFile(filepath.Join(subDir, "local-only.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatalf("write local-only: %v", err)
	}
	mustRun(subDir, "add", "local-only.txt")
	mustRun(subDir, "commit", "-m", "detached edit")

	g := New(parent)
	statuses, err := g.SubmoduleStatuses()
	if err != nil {
		t.Fatalf("SubmoduleStatuses: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	s := statuses[0]
	if !s.HasUnpushed {
		t.Errorf("HasUnpushed = false on detached-HEAD edit, want true (regression)")
	}
	if s.UnpushedCount != 1 {
		t.Errorf("UnpushedCount = %d, want 1", s.UnpushedCount)
	}
	if !s.DetachedHead {
		t.Errorf("DetachedHead = false, want true")
	}
}

func TestSubmoduleStatuses_NoUnpushedWhenAtOrigin(t *testing.T) {
	// A freshly-added submodule's checkout is exactly the SHA on origin.
	// HasUnpushed must stay false and UnpushedCount must be zero.
	enableFileProtocol(t)

	parent, parentCleanup := setupTestRepo(t)
	defer parentCleanup()
	src, srcCleanup := setupSubmoduleRepo(t, "at-origin")
	defer srcCleanup()

	addSubmodule(t, parent, src, "vendor/x")

	g := New(parent)
	statuses, err := g.SubmoduleStatuses()
	if err != nil {
		t.Fatalf("SubmoduleStatuses: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	if statuses[0].HasUnpushed || statuses[0].UnpushedCount != 0 {
		t.Errorf("expected no unpushed, got HasUnpushed=%v UnpushedCount=%d",
			statuses[0].HasUnpushed, statuses[0].UnpushedCount)
	}
}

func TestParseSubmoduleStatusLine(t *testing.T) {
	cases := []struct {
		name   string
		line   string
		marker byte
		sha    string
		path   string
		ok     bool
	}{
		{"clean", " abc123 vendor/x", ' ', "abc123", "vendor/x", true},
		{"changed", "+abc123 vendor/x (heads/main)", '+', "abc123", "vendor/x", true},
		{"deinit", "-abc123 vendor/x", '-', "abc123", "vendor/x", true},
		{"conflict", "Uabc123 vendor/x", 'U', "abc123", "vendor/x", true},
		{"path with spaces", " abc123 vendor/dir with spaces", ' ', "abc123", "vendor/dir with spaces", true},
		{"path with parens, no desc", " abc123 vendor/x(y)", ' ', "abc123", "vendor/x(y)", true},
		// `g.run` calls `strings.TrimSpace` on the full multi-line output,
		// stripping the leading-space marker from the first line. Verify
		// the parser still recovers the full SHA in that case.
		{"clean, leading space stripped", "f5055296abc vendor/x (heads/main)", ' ', "f5055296abc", "vendor/x", true},
		{"empty", "", 0, "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			marker, sha, path, ok := parseSubmoduleStatusLine(c.line)
			if marker != c.marker || sha != c.sha || path != c.path || ok != c.ok {
				t.Errorf("got (marker=%q sha=%q path=%q ok=%v), want (marker=%q sha=%q path=%q ok=%v)",
					marker, sha, path, ok, c.marker, c.sha, c.path, c.ok)
			}
		})
	}
}

func TestIsDetachedHead(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)
	detached, err := g.IsDetachedHead()
	if err != nil {
		t.Fatalf("IsDetachedHead: %v", err)
	}
	if detached {
		t.Errorf("IsDetachedHead = true on branch, want false")
	}

	// Detach HEAD and re-check.
	mustOutput(t, dir, "checkout", "--detach", "HEAD")
	detached, err = g.IsDetachedHead()
	if err != nil {
		t.Fatalf("IsDetachedHead after detach: %v", err)
	}
	if !detached {
		t.Errorf("IsDetachedHead = false on detached HEAD, want true")
	}
}

// mustOutput runs git in dir and returns combined output, failing the
// test on error. Used by tests that need the SHA / branch the command
// produced rather than just success/failure.
func mustOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// TestRebase_RefreshesSubmodules verifies that a successful rebase
// auto-advances submodule working trees. This catches regressions in the
// wiring between RebaseNonInteractive and refreshSubmodulesBestEffort.
func TestRebase_RefreshesSubmodules(t *testing.T) {
	enableFileProtocol(t)

	parent, parentCleanup := setupTestRepo(t)
	defer parentCleanup()

	src, srcCleanup := setupSubmoduleRepo(t, "rebase")
	defer srcCleanup()

	addSubmodule(t, parent, src, "vendor/x")

	mustRun := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	// Capture the SHA the parent records right after `submodule add`.
	startSubSHA := strings.TrimSpace(mustOutput(t, filepath.Join(parent, "vendor/x"), "rev-parse", "HEAD"))

	// Branch off main, advance the submodule on the source, and bump the
	// parent's pointer on the branch.
	mustRun(parent, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(src, "extra.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatalf("write extra: %v", err)
	}
	mustRun(src, "add", "extra.txt")
	mustRun(src, "commit", "-m", "extra")
	newSubSHA := strings.TrimSpace(mustOutput(t, src, "rev-parse", "HEAD"))
	mustRun(filepath.Join(parent, "vendor/x"), "fetch", "origin")
	mustRun(filepath.Join(parent, "vendor/x"), "checkout", newSubSHA)
	mustRun(parent, "add", "vendor/x")
	mustRun(parent, "commit", "-m", "bump submodule on feature")

	// Switch back to main, add an unrelated commit, then rebase feature
	// onto the new main tip.
	mustRun(parent, "checkout", "main")
	if err := os.WriteFile(filepath.Join(parent, "main_extra.txt"), []byte("m\n"), 0644); err != nil {
		t.Fatalf("write main_extra: %v", err)
	}
	mustRun(parent, "add", "main_extra.txt")
	mustRun(parent, "commit", "-m", "main extra")
	mustRun(parent, "checkout", "feature")

	// Force the submodule working tree back to the old SHA so we can
	// observe whether RebaseNonInteractive's tail refreshes it.
	mustRun(filepath.Join(parent, "vendor/x"), "checkout", startSubSHA)

	g := New(parent)
	result := g.RebaseNonInteractive("main")
	if !result.Success {
		t.Fatalf("rebase did not succeed: %+v", result)
	}

	// After the rebase, the submodule working tree should match the SHA
	// the parent's HEAD now records (newSubSHA).
	finalSubSHA := strings.TrimSpace(mustOutput(t, filepath.Join(parent, "vendor/x"), "rev-parse", "HEAD"))
	if finalSubSHA != newSubSHA {
		t.Errorf("after rebase, submodule SHA = %s, want %s — auto-refresh did not run", finalSubSHA, newSubSHA)
	}
}

// TestSubmoduleStatuses_PushGateUsesGitlinkSHA exercises the divergence
// between checkout and gitlink: the submodule's working tree has a new
// local commit, but the parent's HEAD still pins the old SHA. Pushing the
// parent now would record the OLD SHA, which is on origin — so the push
// gate must NOT warn even though the user has unpushed local work in the
// submodule. The doctor's informational HasUnpushed should still fire.
func TestSubmoduleStatuses_PushGateUsesGitlinkSHA(t *testing.T) {
	enableFileProtocol(t)

	parent, parentCleanup := setupTestRepo(t)
	defer parentCleanup()
	src, srcCleanup := setupSubmoduleRepo(t, "pushgate")
	defer srcCleanup()

	addSubmodule(t, parent, src, "vendor/x")

	subDir := filepath.Join(parent, "vendor/x")
	mustRun := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	// Author identity inside the submodule (post-`submodule add` it has its
	// own .git/modules/<path> config).
	mustRun(subDir, "config", "user.email", "test@test.com")
	mustRun(subDir, "config", "user.name", "Test")

	// Detach + commit a new file in the submodule. CheckoutSHA advances;
	// parent's gitlink still records the original SHA.
	currentSHA := strings.TrimSpace(mustOutput(t, subDir, "rev-parse", "HEAD"))
	mustRun(subDir, "checkout", "--detach", currentSHA)
	if err := os.WriteFile(filepath.Join(subDir, "local.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustRun(subDir, "add", "local.txt")
	mustRun(subDir, "commit", "-m", "local edit")

	g := New(parent)
	statuses, err := g.SubmoduleStatuses()
	if err != nil {
		t.Fatalf("SubmoduleStatuses: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	s := statuses[0]
	// Doctor's check: the submodule's working tree has unpushed work.
	if !s.HasUnpushed || s.UnpushedCount != 1 {
		t.Errorf("HasUnpushed=%v UnpushedCount=%d, want true/1 (informational)", s.HasUnpushed, s.UnpushedCount)
	}
	// Push gate's check: the gitlink SHA (old) IS on origin, so pushing
	// the parent is safe — must not flag.
	if s.GitlinkUnpushed {
		t.Errorf("GitlinkUnpushed=true, want false — gitlink still points at the on-origin SHA")
	}
	if s.GitlinkUnpushedCount != 0 {
		t.Errorf("GitlinkUnpushedCount=%d, want 0", s.GitlinkUnpushedCount)
	}
	if !s.PointerChanged {
		t.Errorf("PointerChanged=false, want true — gitlink and checkout disagree")
	}
}

// TestSubmoduleStatuses_PushGateFiresAfterGitlinkBump verifies the push
// gate DOES fire once the gitlink itself is committed to point at the
// unpushed local SHA. This is the real "push will break teammates" state.
func TestSubmoduleStatuses_PushGateFiresAfterGitlinkBump(t *testing.T) {
	enableFileProtocol(t)

	parent, parentCleanup := setupTestRepo(t)
	defer parentCleanup()
	src, srcCleanup := setupSubmoduleRepo(t, "gitlinkbump")
	defer srcCleanup()

	addSubmodule(t, parent, src, "vendor/x")

	subDir := filepath.Join(parent, "vendor/x")
	mustRun := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	mustRun(subDir, "config", "user.email", "test@test.com")
	mustRun(subDir, "config", "user.name", "Test")

	currentSHA := strings.TrimSpace(mustOutput(t, subDir, "rev-parse", "HEAD"))
	mustRun(subDir, "checkout", "--detach", currentSHA)
	if err := os.WriteFile(filepath.Join(subDir, "local.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustRun(subDir, "add", "local.txt")
	mustRun(subDir, "commit", "-m", "local edit")

	// Bump the gitlink in the parent.
	mustRun(parent, "add", "vendor/x")
	mustRun(parent, "commit", "-m", "bump submodule pin")

	g := New(parent)
	statuses, err := g.SubmoduleStatuses()
	if err != nil {
		t.Fatalf("SubmoduleStatuses: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	s := statuses[0]
	if !s.GitlinkUnpushed || s.GitlinkUnpushedCount != 1 {
		t.Errorf("GitlinkUnpushed=%v GitlinkUnpushedCount=%d, want true/1", s.GitlinkUnpushed, s.GitlinkUnpushedCount)
	}
	if s.PointerChanged {
		t.Errorf("PointerChanged=true, want false — gitlink == checkout after bump")
	}
}

// TestSubmoduleStatuses_NestedSubmodules verifies that the recursive scan
// surfaces issues in submodules-of-submodules with their full path from
// the top-level worktree.
func TestSubmoduleStatuses_NestedSubmodules(t *testing.T) {
	enableFileProtocol(t)

	parent, parentCleanup := setupTestRepo(t)
	defer parentCleanup()

	// We'll use a "middle" submodule which itself contains an "inner"
	// submodule. setupSubmoduleRepo gives us a repo we can adopt as the
	// inner; we then init+add it inside the middle source repo before
	// adding middle to parent.
	middle, middleCleanup := setupSubmoduleRepo(t, "middle")
	defer middleCleanup()
	inner, innerCleanup := setupSubmoduleRepo(t, "inner")
	defer innerCleanup()

	mustRun := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	// Add `inner` as a submodule inside `middle`.
	mustRun(middle, "submodule", "add", inner, "nested/inner")
	mustRun(middle, "commit", "-m", "add inner submodule")

	// Add `middle` (which now contains inner) as a submodule of `parent`.
	addSubmodule(t, parent, middle, "vendor/middle")
	// `submodule add` does not recurse — the inner submodule isn't init'd
	// in parent's checkout yet. Init it explicitly so it's on disk and
	// SubmoduleStatuses can recurse into it.
	mustRun(parent, "submodule", "update", "--init", "--recursive")

	// Dirty up the inner submodule so the recursion has something to flag.
	innerPath := filepath.Join(parent, "vendor/middle", "nested/inner")
	if err := os.WriteFile(filepath.Join(innerPath, "SUB_README.md"), []byte("dirty\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	g := New(parent)
	statuses, err := g.SubmoduleStatuses()
	if err != nil {
		t.Fatalf("SubmoduleStatuses: %v", err)
	}
	// Expect both vendor/middle and vendor/middle/nested/inner.
	gotPaths := make(map[string]SubmoduleStatus, len(statuses))
	for _, s := range statuses {
		gotPaths[s.Path] = s
	}
	if _, ok := gotPaths["vendor/middle"]; !ok {
		t.Errorf("missing vendor/middle in statuses: %+v", statuses)
	}
	innerStatus, ok := gotPaths["vendor/middle/nested/inner"]
	if !ok {
		t.Fatalf("missing nested submodule vendor/middle/nested/inner: %+v", statuses)
	}
	if !innerStatus.Dirty {
		t.Errorf("nested inner submodule Dirty=false, want true")
	}
}

// TestParseSubmoduleStatusLine_PathWithParenAndSpace documents the
// known limitation: a submodule path that contains a literal " (...)" at
// the end is indistinguishable from a `git describe` desc suffix, and the
// parser will strip it. Real submodule paths essentially never look like
// this (filesystem paths don't end in describe-style annotations), so we
// accept the false-positive in exchange for a simple parser.
func TestParseSubmoduleStatusLine_PathWithParenAndSpace(t *testing.T) {
	// This is the documented mis-parse: the trailing " (annotation)" looks
	// just like a describe suffix, so it gets stripped. The test pins the
	// behavior so any future rewrite to truly disambiguate is a deliberate
	// change rather than a silent fix.
	marker, sha, path, ok := parseSubmoduleStatusLine(" abc123 vendor/dir (annotation)")
	if !ok {
		t.Fatalf("parser returned ok=false unexpectedly")
	}
	if marker != ' ' || sha != "abc123" {
		t.Errorf("marker=%q sha=%q, want ' '/abc123", marker, sha)
	}
	if path != "vendor/dir" {
		t.Errorf("path=%q, want 'vendor/dir' (the trailing ' (annotation)' is stripped — see comment)", path)
	}
}

// TestMerge_RefreshesSubmodules locks in that the interactive Merge wrapper
// runs the post-success submodule refresh. Uses MergeNonInteractive's
// equivalent setup: no conflicts, fast-forward style.
func TestMerge_RefreshesSubmodules(t *testing.T) {
	enableFileProtocol(t)

	parent, parentCleanup := setupTestRepo(t)
	defer parentCleanup()
	src, srcCleanup := setupSubmoduleRepo(t, "merge")
	defer srcCleanup()

	addSubmodule(t, parent, src, "vendor/x")

	mustRun := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	startSubSHA := strings.TrimSpace(mustOutput(t, filepath.Join(parent, "vendor/x"), "rev-parse", "HEAD"))

	// Branch off main, advance the submodule on the source, bump the
	// parent's pointer on the branch.
	mustRun(parent, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(src, "extra.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatalf("write extra: %v", err)
	}
	mustRun(src, "add", "extra.txt")
	mustRun(src, "commit", "-m", "extra")
	newSubSHA := strings.TrimSpace(mustOutput(t, src, "rev-parse", "HEAD"))
	mustRun(filepath.Join(parent, "vendor/x"), "fetch", "origin")
	mustRun(filepath.Join(parent, "vendor/x"), "checkout", newSubSHA)
	mustRun(parent, "add", "vendor/x")
	mustRun(parent, "commit", "-m", "feature: bump submodule")

	// Switch to main, force submodule back to old SHA, then merge feature.
	mustRun(parent, "checkout", "main")
	mustRun(filepath.Join(parent, "vendor/x"), "checkout", startSubSHA)

	g := New(parent)
	// Use MergeNonInteractive; the interactive Merge wrapper would
	// require a TTY to behave normally. Both call refreshSubmodulesBest-
	// Effort at the tail, so this exercises the same wiring.
	result := g.MergeNonInteractive("feature")
	if !result.Success {
		t.Fatalf("merge failed: %+v", result)
	}
	finalSubSHA := strings.TrimSpace(mustOutput(t, filepath.Join(parent, "vendor/x"), "rev-parse", "HEAD"))
	if finalSubSHA != newSubSHA {
		t.Errorf("after merge, submodule SHA = %s, want %s — auto-refresh did not run", finalSubSHA, newSubSHA)
	}
}
