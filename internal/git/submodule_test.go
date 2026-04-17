package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

func TestMirrorSubmodules_RejectsEmptyPaths(t *testing.T) {
	if err := MirrorSubmodules("", "/tmp"); err == nil {
		t.Errorf("expected error on empty source path")
	}
	if err := MirrorSubmodules("/tmp", ""); err == nil {
		t.Errorf("expected error on empty dest path")
	}
}
