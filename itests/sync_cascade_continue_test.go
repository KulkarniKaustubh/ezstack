package itests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// withYesMode flips ui.YesMode on for the duration of t and restores it after.
// Sync commands use TUI prompts for confirms / pushes; itests need them
// auto-accepted so the test can drive the flow without interactive input.
func withYesMode(t *testing.T) {
	t.Helper()
	prev := ui.YesMode
	ui.YesMode = true
	t.Cleanup(func() { ui.YesMode = prev })
}

// initBareRemote initialises a bare origin under env.TmpDir and pushes main.
// Returns the bare repo path so callers can simulate collaborator pushes.
func initBareRemote(t *testing.T, env *TestEnv) string {
	t.Helper()
	bareDir := filepath.Join(env.TmpDir, "origin.git")
	if err := exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run(); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	if err := exec.Command("git", "-C", env.RepoDir, "remote", "add", "origin", bareDir).Run(); err != nil {
		t.Fatalf("add remote: %v", err)
	}
	if err := exec.Command("git", "-C", env.RepoDir, "push", "-u", "origin", "main").Run(); err != nil {
		t.Fatalf("push main: %v", err)
	}
	return bareDir
}

// createBranchInOwnStack creates a branch with parent=main but pins it to a
// fresh stack so two main-rooted branches don't auto-consolidate via
// findUniqueStackByRoot. Mirrors the common.go helper but passes "new" as the
// targetStackHash. Adds a single commit on a per-branch unique file.
func createBranchInOwnStack(t *testing.T, env *TestEnv, name, file, content string) string {
	t.Helper()
	mgr, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	worktreePath := filepath.Join(env.WorktreeDir, name)
	if _, err := mgr.CreateBranch(name, "main", worktreePath, "new"); err != nil {
		t.Fatalf("CreateBranch %s: %v", name, err)
	}
	worktreePath, _ = filepath.EvalSymlinks(worktreePath)
	if err := os.WriteFile(filepath.Join(worktreePath, file), []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	if err := exec.Command("git", "-C", worktreePath, "add", ".").Run(); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := exec.Command("git", "-C", worktreePath, "commit", "-m", name).Run(); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	return worktreePath
}

// chdirOrFail changes working directory or fails the test, registering a
// restore on cleanup. Many sync flows use os.Getwd() implicitly so itests
// must drive the CLI from the right directory.
func chdirOrFail(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

// abortRebaseQuiet best-effort aborts an in-progress rebase. Used in test
// cleanups so leftover .git/rebase-merge directories don't block worktree
// removal on filesystems that refuse to delete non-empty hidden dirs.
func abortRebaseQuiet(dir string) {
	exec.Command("git", "-C", dir, "rebase", "--abort").Run()
}

// TestSyncItest_CascadeFixThroughCLI exercises the full Sync CLI path for
// the cascading-conflict scenario: a parent's rebase conflicts with main, and
// child branches that touch separate regions must NOT re-encounter that
// conflict. This is the user's reported bug, end-to-end.
//
// Without the --onto-with-snapshot fix, `commands.Sync(["-s", "--continue"])`
// would re-run the parent's conflict on each downstream branch.
func TestSyncItest_CascadeFixThroughCLI(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	withYesMode(t)

	// 12-line shared file so per-branch edits sit in different diff hunks
	// (≥3 lines apart). Otherwise git's default 3-line context glues the
	// edits together and we'd hit hunk-overlap conflicts unrelated to the
	// cascade we're testing.
	sharedLines := "L1\nL2\nL3\nL4\nL5\nL6\nL7\nL8\nL9\nL10\nL11\nL12\n"
	sharedPath := filepath.Join(env.RepoDir, "shared.txt")
	if err := os.WriteFile(sharedPath, []byte(sharedLines), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "seed shared.txt").Run()

	initBareRemote(t, env)

	// Stack: main → a → b → c. Each edits a different far-apart line.
	mgr, _ := stack.NewManager(env.RepoDir)
	mustCreate := func(name, parent string, lineIdx int, val string) string {
		path := filepath.Join(env.WorktreeDir, name)
		if _, err := mgr.CreateBranch(name, parent, path, ""); err != nil {
			t.Fatalf("CreateBranch %s: %v", name, err)
		}
		mgr, _ = stack.NewManager(env.RepoDir)
		path, _ = filepath.EvalSymlinks(path)
		// Read current content from the worktree (already inherits parent's
		// edits) and edit one line.
		raw, err := os.ReadFile(filepath.Join(path, "shared.txt"))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		lines[lineIdx] = val
		if err := os.WriteFile(filepath.Join(path, "shared.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		exec.Command("git", "-C", path, "add", ".").Run()
		exec.Command("git", "-C", path, "commit", "-m", name).Run()
		return path
	}
	aPath := mustCreate("a", "main", 0, "L1-A")
	bPath := mustCreate("b", "a", 5, "L6-B")
	cPath := mustCreate("c", "b", 10, "L11-C")

	// Update main to conflict with a's edit on line 0.
	rawMain, _ := os.ReadFile(sharedPath)
	mainLines := strings.Split(strings.TrimRight(string(rawMain), "\n"), "\n")
	mainLines[0] = "L1-MAIN"
	os.WriteFile(sharedPath, []byte(strings.Join(mainLines, "\n")+"\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "main edit").Run()
	exec.Command("git", "-C", env.RepoDir, "push", "origin", "main").Run()

	// First sync: from a's worktree, with -s (current stack only). Expect
	// a's rebase to conflict, b/c untouched.
	chdirOrFail(t, aPath)
	t.Cleanup(func() {
		// Make sure no rebase-merge dir blocks Cleanup's RemoveAll.
		for _, p := range []string{aPath, bPath, cPath} {
			abortRebaseQuiet(p)
		}
	})
	if err := commands.Sync([]string{"-s"}); err != nil {
		t.Logf("first sync returned err (expected on conflict): %v", err)
	}
	aGit := git.New(aPath)
	if ip, _ := aGit.IsRebaseInProgress(); !ip {
		t.Fatal("expected a's rebase to be in progress after initial sync")
	}

	// Resolve a's conflict by writing the resolved content.
	mainLines[0] = "L1-RESOLVED"
	os.WriteFile(filepath.Join(aPath, "shared.txt"), []byte(strings.Join(mainLines, "\n")+"\n"), 0644)
	if err := exec.Command("git", "-C", aPath, "add", ".").Run(); err != nil {
		t.Fatalf("git add resolution: %v", err)
	}

	// --continue should: complete a's rebase, then re-sync b and c without
	// re-encountering a's conflict.
	if err := commands.Sync([]string{"-s", "--continue"}); err != nil {
		t.Fatalf("sync --continue should succeed end-to-end (no cascade); got: %v", err)
	}

	// All three branches must be fully rebased — no rebase in progress
	// anywhere.
	for _, name := range []string{"a", "b", "c"} {
		p := filepath.Join(env.WorktreeDir, name)
		g := git.New(p)
		if ip, _ := g.IsRebaseInProgress(); ip {
			t.Errorf("%s: rebase still in progress after --continue (cascade not prevented)", name)
		}
	}

	// b and c should each have a's resolved line + their own edit.
	bRaw, _ := os.ReadFile(filepath.Join(bPath, "shared.txt"))
	if !strings.Contains(string(bRaw), "L1-RESOLVED") || !strings.Contains(string(bRaw), "L6-B") {
		t.Errorf("b's shared.txt missing resolved-or-own-edit: %q", string(bRaw))
	}
	cRaw, _ := os.ReadFile(filepath.Join(cPath, "shared.txt"))
	if !strings.Contains(string(cRaw), "L1-RESOLVED") || !strings.Contains(string(cRaw), "L6-B") || !strings.Contains(string(cRaw), "L11-C") {
		t.Errorf("c's shared.txt missing one of resolved/b/c edits: %q", string(cRaw))
	}
}

// TestSyncItest_ContinueScopeRespectsStackFlag is the user's primary bug,
// driven through the full CLI. Two stacks both in conflict;
// `commands.Sync(["-s", "--continue"])` must only touch the current stack.
func TestSyncItest_ContinueScopeRespectsStackFlag(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	withYesMode(t)

	// Two unrelated files so each branch has an independent conflict point.
	for _, f := range []string{"x.txt", "y.txt"} {
		os.WriteFile(filepath.Join(env.RepoDir, f), []byte("base\n"), 0644)
	}
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "seed").Run()
	initBareRemote(t, env)

	xPath := createBranchInOwnStack(t, env, "x-feat", "x.txt", "x-FEAT\n")
	yPath := createBranchInOwnStack(t, env, "y-feat", "y.txt", "y-FEAT\n")

	// main edits both files so both branches conflict.
	for _, f := range []string{"x.txt", "y.txt"} {
		os.WriteFile(filepath.Join(env.RepoDir, f), []byte(strings.Replace(f, ".txt", "-MAIN\n", 1)), 0644)
	}
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "main").Run()
	exec.Command("git", "-C", env.RepoDir, "push", "origin", "main").Run()

	// Sync everything to trigger conflicts on both stacks.
	chdirOrFail(t, env.RepoDir)
	t.Cleanup(func() {
		abortRebaseQuiet(xPath)
		abortRebaseQuiet(yPath)
	})
	_ = commands.Sync([]string{"--all"})

	xGit := git.New(xPath)
	yGit := git.New(yPath)
	xIP, _ := xGit.IsRebaseInProgress()
	yIP, _ := yGit.IsRebaseInProgress()
	if !xIP || !yIP {
		t.Fatalf("expected both stacks mid-rebase; got xIP=%v yIP=%v", xIP, yIP)
	}

	// Resolve x's conflict but not y's.
	os.WriteFile(filepath.Join(xPath, "x.txt"), []byte("x-RESOLVED\n"), 0644)
	exec.Command("git", "-C", xPath, "add", ".").Run()

	// Run --continue scoped to current stack from x's worktree. y's stack
	// must remain mid-rebase.
	chdirOrFail(t, xPath)
	if err := commands.Sync([]string{"-s", "--continue"}); err != nil {
		t.Fatalf("scoped --continue from x: %v", err)
	}

	if ip, _ := xGit.IsRebaseInProgress(); ip {
		t.Errorf("x's rebase should be complete after --continue")
	}
	if ip, _ := yGit.IsRebaseInProgress(); !ip {
		t.Errorf("y's rebase MUST NOT be touched by `-s --continue` from x — this was the user-reported bug")
	}
}

// TestSyncItest_PullsCollaboratorCommits drives the collaborator-push scenario
// through the full CLI: you push a branch, a teammate adds a commit on
// origin/<branch>, you run `ezs sync` — sync must fast-forward your local
// branch to include the teammate's commit.
func TestSyncItest_PullsCollaboratorCommits(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	withYesMode(t)

	bareDir := initBareRemote(t, env)

	// Push a feature branch.
	CreateBranchWithCommit(t, env, "feat", "main")
	featPath := filepath.Join(env.WorktreeDir, "feat")
	featPath, _ = filepath.EvalSymlinks(featPath)
	if err := exec.Command("git", "-C", featPath, "push", "-u", "origin", "feat").Run(); err != nil {
		t.Fatalf("push feat: %v", err)
	}

	preLocal, _ := exec.Command("git", "-C", featPath, "rev-parse", "HEAD").Output()
	preLocalSHA := strings.TrimSpace(string(preLocal))

	// Simulate the collaborator: clone bare, add a commit on a different
	// file (so it doesn't conflict with anything), push back.
	collab, _ := os.MkdirTemp("", "collab-*")
	defer os.RemoveAll(collab)
	if err := exec.Command("git", "clone", "-q", "-b", "feat", bareDir, collab).Run(); err != nil {
		t.Fatalf("collaborator clone: %v", err)
	}
	exec.Command("git", "-C", collab, "config", "user.email", "collab@test.com").Run()
	exec.Command("git", "-C", collab, "config", "user.name", "Collab").Run()
	if err := os.WriteFile(filepath.Join(collab, "from-collab.txt"), []byte("teammate\n"), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", collab, "add", ".").Run()
	if err := exec.Command("git", "-C", collab, "commit", "-q", "-m", "collab adds a file").Run(); err != nil {
		t.Fatalf("collaborator commit: %v", err)
	}
	if err := exec.Command("git", "-C", collab, "push", "-q", "origin", "feat").Run(); err != nil {
		t.Fatalf("collaborator push: %v", err)
	}
	collabHEAD, _ := exec.Command("git", "-C", collab, "rev-parse", "HEAD").Output()
	collabSHA := strings.TrimSpace(string(collabHEAD))

	// Run sync from feat's worktree. With -c (current branch) we hit
	// SyncBranch, which is the path for single-branch sync.
	chdirOrFail(t, featPath)
	if err := commands.Sync([]string{"-c"}); err != nil {
		t.Fatalf("commands.Sync -c: %v", err)
	}

	postLocal, _ := exec.Command("git", "-C", featPath, "rev-parse", "HEAD").Output()
	postLocalSHA := strings.TrimSpace(string(postLocal))
	if postLocalSHA == preLocalSHA {
		t.Errorf("local HEAD did not advance — collaborator's commit was not integrated. pre=%s post=%s", preLocalSHA, postLocalSHA)
	}
	if postLocalSHA != collabSHA {
		t.Errorf("local HEAD = %s, want collab SHA %s — fast-forward did not match origin/feat", postLocalSHA, collabSHA)
	}
	if _, err := os.Stat(filepath.Join(featPath, "from-collab.txt")); err != nil {
		t.Errorf("collaborator's file is missing in the worktree: %v", err)
	}
}

// TestSyncItest_ContinueExitsNonZeroOnPartial verifies that a `--continue`
// run that completes one branch but cannot finish the descendant subtree
// returns a non-zero error (ErrSyncIncomplete). Scripts and CI rely on this
// to detect partial completion.
func TestSyncItest_ContinueExitsNonZeroOnPartial(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	withYesMode(t)

	// Single shared file so every branch and main edit the same line — the
	// b vs main conflict must persist after a's resolution. Otherwise the
	// re-sync of b would pass and there'd be no "partial" state.
	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte("seed\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "seed").Run()
	initBareRemote(t, env)

	mgr, _ := stack.NewManager(env.RepoDir)
	aPath := filepath.Join(env.WorktreeDir, "a")
	mgr.CreateBranch("a", "main", aPath, "")
	aPath, _ = filepath.EvalSymlinks(aPath)
	os.WriteFile(filepath.Join(aPath, "shared.txt"), []byte("a-FEAT\n"), 0644)
	exec.Command("git", "-C", aPath, "add", ".").Run()
	exec.Command("git", "-C", aPath, "commit", "-m", "a1").Run()

	mgr, _ = stack.NewManager(env.RepoDir)
	bPath := filepath.Join(env.WorktreeDir, "b")
	mgr.CreateBranch("b", "a", bPath, "")
	bPath, _ = filepath.EvalSymlinks(bPath)
	os.WriteFile(filepath.Join(bPath, "shared.txt"), []byte("b-FEAT\n"), 0644)
	exec.Command("git", "-C", bPath, "add", ".").Run()
	exec.Command("git", "-C", bPath, "commit", "-m", "b1").Run()

	// main edits the same line — both a and b's rebases will conflict.
	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte("MAIN\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "main").Run()
	exec.Command("git", "-C", env.RepoDir, "push", "origin", "main").Run()

	chdirOrFail(t, aPath)
	t.Cleanup(func() {
		abortRebaseQuiet(aPath)
		abortRebaseQuiet(bPath)
	})
	_ = commands.Sync([]string{"-s"})

	// Resolve a's conflict only.
	os.WriteFile(filepath.Join(aPath, "shared.txt"), []byte("a-RESOLVED\n"), 0644)
	exec.Command("git", "-C", aPath, "add", ".").Run()

	// --continue completes a but b's re-sync should hit a fresh conflict.
	err := commands.Sync([]string{"-s", "--continue"})
	if err == nil {
		t.Fatal("expected --continue to return an error when descendant re-sync hits a fresh conflict; got nil")
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("error should be ErrSyncIncomplete (mentions 'incomplete'); got: %v", err)
	}

	// Production-safety property: a partial `--continue` must NOT push the
	// in-conflict descendant. b's rebase paused on a fresh conflict; if its
	// half-rebased state were force-pushed, the bad state would land on
	// origin and a teammate's clone would inherit it.
	out, _ := exec.Command("git", "-C", env.RepoDir, "ls-remote", "origin", "refs/heads/b").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("origin/b must not exist after a partial --continue (b never finished its rebase). got: %q", string(out))
	}
}

// TestSyncItest_ContinueExitsWithExitConflictCode verifies the exit code
// surfaced for a partial `--continue` is ExitConflict (3), not the generic
// ExitGeneral (1). Scripts wrapping ezstack rely on this distinction to
// decide whether to retry vs. surface a hard failure.
func TestSyncItest_ContinueExitsWithExitConflictCode(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	withYesMode(t)

	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte("seed\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "seed").Run()
	initBareRemote(t, env)

	// One branch with a conflicting edit on shared.txt.
	mgr, _ := stack.NewManager(env.RepoDir)
	aPath := filepath.Join(env.WorktreeDir, "a")
	mgr.CreateBranch("a", "main", aPath, "")
	aPath, _ = filepath.EvalSymlinks(aPath)
	os.WriteFile(filepath.Join(aPath, "shared.txt"), []byte("a-FEAT\n"), 0644)
	exec.Command("git", "-C", aPath, "add", ".").Run()
	exec.Command("git", "-C", aPath, "commit", "-m", "a1").Run()

	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte("MAIN\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "main").Run()
	exec.Command("git", "-C", env.RepoDir, "push", "origin", "main").Run()

	chdirOrFail(t, aPath)
	t.Cleanup(func() { abortRebaseQuiet(aPath) })
	_ = commands.Sync([]string{"-s"})

	// Don't resolve. --continue should report still-in-conflict and surface
	// ErrSyncIncomplete (which must be a *ui.ExitError with ExitConflict).
	err := commands.Sync([]string{"-s", "--continue"})
	if err == nil {
		t.Fatal("expected --continue with unresolved conflict to error")
	}
	if got := ui.GetExitCode(err); got != ui.ExitConflict {
		t.Errorf("partial --continue must exit %d (ExitConflict); got %d", ui.ExitConflict, got)
	}
}

// TestSyncItest_ContinueSurvivesFetchFailure verifies that --continue
// gracefully degrades when the pre-descendant fetch fails (e.g. the user's
// network dropped, or origin is temporarily unreachable). The local
// `git rebase --continue` for the in-progress branch must still complete;
// only the descendant re-sync's freshness suffers.
func TestSyncItest_ContinueSurvivesFetchFailure(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	withYesMode(t)

	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte("seed\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "seed").Run()
	bareDir := initBareRemote(t, env)

	mgr, _ := stack.NewManager(env.RepoDir)
	aPath := filepath.Join(env.WorktreeDir, "a")
	mgr.CreateBranch("a", "main", aPath, "")
	aPath, _ = filepath.EvalSymlinks(aPath)
	os.WriteFile(filepath.Join(aPath, "shared.txt"), []byte("a-FEAT\n"), 0644)
	exec.Command("git", "-C", aPath, "add", ".").Run()
	exec.Command("git", "-C", aPath, "commit", "-m", "a1").Run()

	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte("MAIN\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "main").Run()
	exec.Command("git", "-C", env.RepoDir, "push", "origin", "main").Run()

	chdirOrFail(t, aPath)
	t.Cleanup(func() { abortRebaseQuiet(aPath) })
	_ = commands.Sync([]string{"-s"})

	// Resolve a's conflict so --continue's git rebase --continue can complete.
	os.WriteFile(filepath.Join(aPath, "shared.txt"), []byte("a-RESOLVED\n"), 0644)
	exec.Command("git", "-C", aPath, "add", ".").Run()

	// Make origin unreachable: rename the bare repo. The Fetch() at the top
	// of syncContinue will fail; the code must warn and proceed, not crash
	// or short-circuit before completing the rebase.
	moved := bareDir + ".moved"
	if err := os.Rename(bareDir, moved); err != nil {
		t.Fatalf("rename bare to simulate fetch failure: %v", err)
	}
	t.Cleanup(func() { _ = os.Rename(moved, bareDir) })

	if err := commands.Sync([]string{"-s", "--continue"}); err != nil {
		t.Fatalf("--continue should succeed despite fetch failure (rebase --continue is local); got: %v", err)
	}
	aGit := git.New(aPath)
	if ip, _ := aGit.IsRebaseInProgress(); ip {
		t.Errorf("a's rebase should be complete after --continue even with fetch failure")
	}
}
