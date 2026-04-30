package itests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// TestSyncItest_MultiStepRebaseStillInConflict verifies the ContinueResult
// classification: a branch with TWO conflicting commits, where resolving
// the first leaves the second still in conflict, must surface as
// `paused on next commit's conflict` and exit non-zero. Ensures we don't
// silently push a half-rebased branch.
func TestSyncItest_MultiStepRebaseStillInConflict(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	withYesMode(t)

	// shared.txt with two regions — a1 edits region 1, a2 edits region 2,
	// both will conflict when main moves both regions.
	lines := strings.Repeat("X\n", 12)
	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte(lines), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "seed").Run()
	initBareRemote(t, env)

	mgr, _ := stack.NewManager(env.RepoDir)
	aPath := filepath.Join(env.WorktreeDir, "a")
	mgr.CreateBranch("a", "main", aPath, "")
	aPath, _ = filepath.EvalSymlinks(aPath)

	editLine := func(p string, idx int, val string) {
		raw, _ := os.ReadFile(p)
		ls := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		ls[idx] = val
		os.WriteFile(p, []byte(strings.Join(ls, "\n")+"\n"), 0644)
	}

	// a1 edits line 0.
	editLine(filepath.Join(aPath, "shared.txt"), 0, "a1")
	exec.Command("git", "-C", aPath, "add", ".").Run()
	exec.Command("git", "-C", aPath, "commit", "-m", "a1").Run()
	// a2 edits line 6.
	editLine(filepath.Join(aPath, "shared.txt"), 6, "a2")
	exec.Command("git", "-C", aPath, "add", ".").Run()
	exec.Command("git", "-C", aPath, "commit", "-m", "a2").Run()

	// main edits BOTH lines so both a1 and a2 will conflict on rebase.
	editLine(filepath.Join(env.RepoDir, "shared.txt"), 0, "main-0")
	editLine(filepath.Join(env.RepoDir, "shared.txt"), 6, "main-6")
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "main").Run()
	exec.Command("git", "-C", env.RepoDir, "push", "origin", "main").Run()

	chdirOrFail(t, aPath)
	t.Cleanup(func() { abortRebaseQuiet(aPath) })
	_ = commands.Sync([]string{"-s"})

	// After the first sync, a's first conflict (a1 vs main-0) is mid-rebase.
	if ip, _ := git.New(aPath).IsRebaseInProgress(); !ip {
		t.Fatal("expected a's rebase mid-conflict on a1")
	}

	// Resolve only a1's conflict (line 0). a2 is still ahead and will
	// produce a fresh conflict when --continue advances to it.
	editLine(filepath.Join(aPath, "shared.txt"), 0, "RESOLVED-0")
	exec.Command("git", "-C", aPath, "add", ".").Run()

	err := commands.Sync([]string{"-s", "--continue"})
	if err == nil {
		t.Fatal("expected ErrSyncIncomplete because the next commit (a2) also conflicts; got nil")
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("expected ErrSyncIncomplete; got: %v", err)
	}
	// Rebase must still be in progress — paused on a2's conflict.
	if ip, _ := git.New(aPath).IsRebaseInProgress(); !ip {
		t.Errorf("rebase should still be in progress after multi-step continue; was reported as Done")
	}
}

// TestSyncItest_YesModeAutoConfirmsContinuePush verifies the user's literal
// invocation: `ezs sync -s -y --continue`. With YesMode set, push offers
// after a successful continue must auto-accept without TUI prompts.
func TestSyncItest_YesModeAutoConfirmsContinuePush(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	prev := ui.YesMode
	defer func() { ui.YesMode = prev }()

	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte("v0\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "seed").Run()
	initBareRemote(t, env)

	mgr, _ := stack.NewManager(env.RepoDir)
	aPath := filepath.Join(env.WorktreeDir, "a")
	mgr.CreateBranch("a", "main", aPath, "")
	aPath, _ = filepath.EvalSymlinks(aPath)
	os.WriteFile(filepath.Join(aPath, "shared.txt"), []byte("a-FEAT\n"), 0644)
	exec.Command("git", "-C", aPath, "add", ".").Run()
	exec.Command("git", "-C", aPath, "commit", "-m", "a").Run()
	exec.Command("git", "-C", aPath, "push", "-u", "origin", "a").Run()

	// Conflict: main edits the same line.
	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte("main\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "main").Run()
	exec.Command("git", "-C", env.RepoDir, "push", "origin", "main").Run()

	chdirOrFail(t, aPath)
	t.Cleanup(func() { abortRebaseQuiet(aPath) })

	// Initial sync hits the conflict.
	ui.YesMode = true
	_ = commands.Sync([]string{"-s"})
	os.WriteFile(filepath.Join(aPath, "shared.txt"), []byte("RESOLVED\n"), 0644)
	exec.Command("git", "-C", aPath, "add", ".").Run()

	// Now run --continue. With YesMode set globally (the user's `-y` is
	// stripped by main.go before reaching commands.Sync — it's a global
	// flag, not a sync flag), the post-continue force-push offer must
	// auto-accept without hanging on a TUI prompt. We assert completion by
	// checking that origin/a has advanced to the new tip.
	if err := commands.Sync([]string{"-s", "--continue"}); err != nil {
		t.Fatalf("commands.Sync -s --continue (with YesMode): %v", err)
	}

	// Local a's HEAD post-continue.
	localTip, _ := exec.Command("git", "-C", aPath, "rev-parse", "HEAD").Output()
	originTip, _ := exec.Command("git", "-C", env.RepoDir, "rev-parse", "origin/a").Output()
	if strings.TrimSpace(string(localTip)) != strings.TrimSpace(string(originTip)) {
		t.Errorf("origin/a not advanced after -y --continue (push likely blocked on prompt). local=%s origin=%s",
			strings.TrimSpace(string(localTip)), strings.TrimSpace(string(originTip)))
	}
}

// TestSyncItest_BranchModeContinueResyncsDescendants documents the
// deliberate behaviour departure: `ezs sync -b X --continue` resyncs X's
// in-stack descendants (so the post-resolution stack is coherent), even
// though the non-continue `ezs sync -b X` only operates on X.
func TestSyncItest_BranchModeContinueResyncsDescendants(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	withYesMode(t)

	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte("v0\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "seed").Run()
	initBareRemote(t, env)

	mgr, _ := stack.NewManager(env.RepoDir)
	mustCreate := func(name, parent, content string) string {
		p := filepath.Join(env.WorktreeDir, name)
		mgr.CreateBranch(name, parent, p, "")
		mgr, _ = stack.NewManager(env.RepoDir)
		p, _ = filepath.EvalSymlinks(p)
		os.WriteFile(filepath.Join(p, name+".txt"), []byte(content), 0644)
		exec.Command("git", "-C", p, "add", ".").Run()
		exec.Command("git", "-C", p, "commit", "-m", name).Run()
		return p
	}
	aPath := mustCreate("a", "main", "a-FEAT\n")
	bPath := mustCreate("b", "a", "b-FEAT\n")

	// Conflict on a: main edits the file a touches.
	os.WriteFile(filepath.Join(env.RepoDir, "a.txt"), []byte("main-a\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "main").Run()
	exec.Command("git", "-C", env.RepoDir, "push", "origin", "main").Run()

	chdirOrFail(t, aPath)
	t.Cleanup(func() {
		abortRebaseQuiet(aPath)
		abortRebaseQuiet(bPath)
	})

	// Trigger a's conflict.
	_ = commands.Sync([]string{"-s"})

	// Resolve and continue with branch-scoped --continue.
	os.WriteFile(filepath.Join(aPath, "a.txt"), []byte("RESOLVED\n"), 0644)
	exec.Command("git", "-C", aPath, "add", ".").Run()

	if err := commands.Sync([]string{"-b", "a", "--continue"}); err != nil {
		t.Fatalf("commands.Sync -b a --continue: %v", err)
	}
	// b should ALSO be re-synced after a's continue (deliberate departure).
	preBHead, err := exec.Command("git", "-C", bPath, "merge-base", "b", "a").Output()
	if err != nil {
		t.Fatalf("merge-base check: %v", err)
	}
	aHead, _ := exec.Command("git", "-C", bPath, "rev-parse", "a").Output()
	if strings.TrimSpace(string(preBHead)) != strings.TrimSpace(string(aHead)) {
		t.Errorf("b is not based on the new a after `-b a --continue` — descendant re-sync did not fire. merge-base=%s a=%s",
			strings.TrimSpace(string(preBHead)), strings.TrimSpace(string(aHead)))
	}
}

// TestSyncItest_OrphanStashSurfaced verifies the orphan-stash warning fires
// on `--continue` when an in-scope branch has an ezstack autostash but is
// NOT in conflict (i.e. the stash was orphaned by an earlier aborted sync).
func TestSyncItest_OrphanStashSurfaced(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	withYesMode(t)

	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte("v0\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "seed").Run()
	initBareRemote(t, env)

	mgr, _ := stack.NewManager(env.RepoDir)
	aPath := filepath.Join(env.WorktreeDir, "a")
	mgr.CreateBranch("a", "main", aPath, "")
	aPath, _ = filepath.EvalSymlinks(aPath)
	os.WriteFile(filepath.Join(aPath, "a.txt"), []byte("v0\n"), 0644)
	exec.Command("git", "-C", aPath, "add", ".").Run()
	exec.Command("git", "-C", aPath, "commit", "-m", "a1").Run()

	// Make a dirty change and stash it under the ezstack name. This is what
	// an aborted prior sync would leave behind.
	os.WriteFile(filepath.Join(aPath, "dirty.txt"), []byte("uncommitted\n"), 0644)
	if err := exec.Command("git", "-C", aPath, "stash", "push", "-u", "-m", "ezstack-autostash").Run(); err != nil {
		t.Fatalf("seed stash: %v", err)
	}

	// To exercise --continue's orphan-stash scan, we need at least one
	// in-progress conflict in scope (otherwise --continue exits early with
	// "Nothing to continue"). Cause an unrelated conflict on a different
	// branch in the same scope by setting up b → main and having b conflict.
	bPath := filepath.Join(env.WorktreeDir, "b")
	mgr, _ = stack.NewManager(env.RepoDir)
	mgr.CreateBranch("b", "main", bPath, "")
	mgr, _ = stack.NewManager(env.RepoDir)
	bPath, _ = filepath.EvalSymlinks(bPath)
	os.WriteFile(filepath.Join(bPath, "b.txt"), []byte("b-feat\n"), 0644)
	exec.Command("git", "-C", bPath, "add", ".").Run()
	exec.Command("git", "-C", bPath, "commit", "-m", "b1").Run()
	os.WriteFile(filepath.Join(env.RepoDir, "b.txt"), []byte("main-b\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "main b").Run()
	exec.Command("git", "-C", env.RepoDir, "push", "origin", "main").Run()

	chdirOrFail(t, env.RepoDir)
	t.Cleanup(func() {
		abortRebaseQuiet(aPath)
		abortRebaseQuiet(bPath)
	})

	_ = commands.Sync([]string{"--all"})
	// b should now be mid-rebase. a is not in conflict but has the orphan stash.

	// Capture stderr for the duration of --continue so we can assert on the
	// orphan warning.
	orig := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	_ = commands.Sync([]string{"--all", "--continue"})
	w.Close()
	os.Stderr = orig
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	captured := string(buf[:n])

	if !strings.Contains(captured, "Orphan ezstack autostash found for a") {
		t.Errorf("expected orphan-stash warning for a in --continue output. got:\n%s", captured)
	}
}

// TestSyncItest_ForkRemoteIntegration verifies that a branch with a
// non-origin remote (e.g., a fork) gets pulled correctly when the fork has
// new commits.
func TestSyncItest_ForkRemoteIntegration(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	withYesMode(t)

	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte("v0\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "seed").Run()
	initBareRemote(t, env) // origin

	// Set up a separate "fork" bare and add it as a remote.
	forkBare := filepath.Join(env.TmpDir, "fork.git")
	if err := exec.Command("git", "init", "--bare", "-b", "main", forkBare).Run(); err != nil {
		t.Fatalf("init fork bare: %v", err)
	}
	if err := exec.Command("git", "-C", env.RepoDir, "remote", "add", "fork", forkBare).Run(); err != nil {
		t.Fatalf("add fork remote: %v", err)
	}

	// Create branch and push to fork (not origin).
	CreateBranchWithCommit(t, env, "feat-fork", "main")
	featPath := filepath.Join(env.WorktreeDir, "feat-fork")
	featPath, _ = filepath.EvalSymlinks(featPath)
	exec.Command("git", "-C", featPath, "push", "-u", "fork", "feat-fork").Run()

	// Mark the branch as using "fork" remote in ezstack's cache.
	mgr, _ := stack.NewManager(env.RepoDir)
	if err := mgr.SetBranchRemote("feat-fork", "fork"); err != nil {
		t.Fatalf("SetBranchRemote: %v", err)
	}

	preLocal, _ := exec.Command("git", "-C", featPath, "rev-parse", "HEAD").Output()
	preLocalSHA := strings.TrimSpace(string(preLocal))

	// Simulate teammate pushing to fork.
	collab, _ := os.MkdirTemp("", "collab-fork-*")
	defer os.RemoveAll(collab)
	exec.Command("git", "clone", "-q", "-b", "feat-fork", forkBare, collab).Run()
	exec.Command("git", "-C", collab, "config", "user.email", "c@c.com").Run()
	exec.Command("git", "-C", collab, "config", "user.name", "c").Run()
	os.WriteFile(filepath.Join(collab, "from-fork-collab.txt"), []byte("teammate-on-fork\n"), 0644)
	exec.Command("git", "-C", collab, "add", ".").Run()
	exec.Command("git", "-C", collab, "commit", "-q", "-m", "fork collab").Run()
	exec.Command("git", "-C", collab, "push", "-q", "origin", "feat-fork").Run() // pushes to forkBare via the clone's origin

	chdirOrFail(t, featPath)
	if err := commands.Sync([]string{"-c"}); err != nil {
		t.Fatalf("Sync -c: %v", err)
	}

	postLocal, _ := exec.Command("git", "-C", featPath, "rev-parse", "HEAD").Output()
	if strings.TrimSpace(string(postLocal)) == preLocalSHA {
		t.Errorf("local HEAD did not advance — fork-remote pull did not fire. pre=%s post=%s",
			preLocalSHA, strings.TrimSpace(string(postLocal)))
	}
	if _, err := os.Stat(filepath.Join(featPath, "from-fork-collab.txt")); err != nil {
		t.Errorf("teammate's file is missing in worktree after sync: %v", err)
	}
}

// TestSyncItest_DryRunJSONIncludesBehindRemote verifies that the new
// BehindRemote field surfaces correctly in `--dry-run --json` output, so
// scripts can detect collaborator-pull work without reading prose.
func TestSyncItest_DryRunJSONIncludesBehindRemote(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	withYesMode(t)

	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte("v0\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "seed").Run()
	bareDir := initBareRemote(t, env)

	CreateBranchWithCommit(t, env, "feat", "main")
	featPath := filepath.Join(env.WorktreeDir, "feat")
	featPath, _ = filepath.EvalSymlinks(featPath)
	exec.Command("git", "-C", featPath, "push", "-u", "origin", "feat").Run()

	// Teammate adds a commit on origin/feat.
	collab, _ := os.MkdirTemp("", "collab-dry-*")
	defer os.RemoveAll(collab)
	exec.Command("git", "clone", "-q", "-b", "feat", bareDir, collab).Run()
	exec.Command("git", "-C", collab, "config", "user.email", "c@c.com").Run()
	exec.Command("git", "-C", collab, "config", "user.name", "c").Run()
	os.WriteFile(filepath.Join(collab, "x.txt"), []byte("teammate\n"), 0644)
	exec.Command("git", "-C", collab, "add", ".").Run()
	exec.Command("git", "-C", collab, "commit", "-q", "-m", "x").Run()
	exec.Command("git", "-C", collab, "push", "-q", "origin", "feat").Run()

	chdirOrFail(t, featPath)

	// Capture stdout (JSON goes to stdout) for parsing.
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	if err := commands.Sync([]string{"-s", "--dry-run", "--json"}); err != nil {
		t.Fatalf("dry-run --json: %v", err)
	}
	w.Close()
	os.Stdout = orig
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)

	var entries []map[string]any
	if err := json.Unmarshal(buf[:n], &entries); err != nil {
		t.Fatalf("parse JSON: %v\nraw: %q", err, string(buf[:n]))
	}
	var featEntry map[string]any
	for _, e := range entries {
		if e["branch"] == "feat" {
			featEntry = e
			break
		}
	}
	if featEntry == nil {
		t.Fatalf("feat entry missing from dry-run JSON: %v", entries)
	}
	br, ok := featEntry["behind_remote"]
	if !ok {
		t.Errorf("behind_remote field missing from JSON: %v", featEntry)
	}
	// JSON unmarshals numbers to float64 by default.
	if n, _ := br.(float64); int(n) <= 0 {
		t.Errorf("behind_remote should be > 0 after teammate pushed; got %v", br)
	}
}

// TestSyncItest_RealBinaryExitCode builds and invokes the actual `ezs`
// binary as a subprocess to confirm the partial-completion path returns
// exit code != 0 (not just the in-process error). Scripts and CI rely on
// this rather than the Go return value.
func TestSyncItest_RealBinaryExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell semantics for the subprocess invocation")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Build the ezs binary.
	bin := filepath.Join(env.TmpDir, "ezs-built")
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/ezs")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ezs: %v\n%s", err, string(out))
	}

	// Set up a partial-completion scenario: a → b stack, both will conflict
	// against main on the same single line.
	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte("v0\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "seed").Run()
	initBareRemote(t, env)

	mgr, _ := stack.NewManager(env.RepoDir)
	aPath := filepath.Join(env.WorktreeDir, "a")
	mgr.CreateBranch("a", "main", aPath, "")
	mgr, _ = stack.NewManager(env.RepoDir)
	aPath, _ = filepath.EvalSymlinks(aPath)
	os.WriteFile(filepath.Join(aPath, "shared.txt"), []byte("a\n"), 0644)
	exec.Command("git", "-C", aPath, "add", ".").Run()
	exec.Command("git", "-C", aPath, "commit", "-m", "a").Run()

	bPath := filepath.Join(env.WorktreeDir, "b")
	mgr.CreateBranch("b", "a", bPath, "")
	mgr, _ = stack.NewManager(env.RepoDir)
	bPath, _ = filepath.EvalSymlinks(bPath)
	os.WriteFile(filepath.Join(bPath, "shared.txt"), []byte("b\n"), 0644)
	exec.Command("git", "-C", bPath, "add", ".").Run()
	exec.Command("git", "-C", bPath, "commit", "-m", "b").Run()

	os.WriteFile(filepath.Join(env.RepoDir, "shared.txt"), []byte("main\n"), 0644)
	exec.Command("git", "-C", env.RepoDir, "add", ".").Run()
	exec.Command("git", "-C", env.RepoDir, "commit", "-m", "main").Run()
	exec.Command("git", "-C", env.RepoDir, "push", "origin", "main").Run()

	t.Cleanup(func() {
		abortRebaseQuiet(aPath)
		abortRebaseQuiet(bPath)
	})

	// Run the real binary: first sync to trigger the conflict.
	cmd := exec.Command(bin, "sync", "-s", "-y")
	cmd.Dir = aPath
	cmd.Env = append(os.Environ(), "EZSTACK_HOME="+env.ConfigDir)
	cmd.Run()
	// Resolve a's conflict.
	os.WriteFile(filepath.Join(aPath, "shared.txt"), []byte("RESOLVED\n"), 0644)
	exec.Command("git", "-C", aPath, "add", ".").Run()

	// --continue: a continues, b's re-sync hits a fresh conflict → exit non-zero.
	cmd2 := exec.Command(bin, "sync", "-s", "-y", "--continue")
	cmd2.Dir = aPath
	cmd2.Env = append(os.Environ(), "EZSTACK_HOME="+env.ConfigDir)
	err = cmd2.Run()
	if err == nil {
		t.Fatal("real binary exited 0 on partial completion; should be non-zero so scripts can detect")
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() == 0 {
			t.Errorf("real binary exit code 0 on partial completion; expected non-zero")
		}
	} else {
		t.Errorf("expected *exec.ExitError; got %T: %v", err, err)
	}
}

// TestSyncItest_ConcurrentSyncRejected verifies the sync-level lock blocks
// a second `ezs sync` invocation from racing with the first. Acquires the
// lock manually (simulating an in-flight first sync) and asserts the second
// call surfaces the "already running" error rather than racing.
func TestSyncItest_ConcurrentSyncRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("flock-based lock is no-op on Windows")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	withYesMode(t)

	CreateBranchWithCommit(t, env, "feat-lock", "main")
	initBareRemote(t, env)

	stacksPath := filepath.Join(env.ConfigDir, "stacks.json")
	lock, err := config.AcquireSyncLock(stacksPath)
	if err != nil {
		t.Fatalf("acquire lock for test: %v", err)
	}
	defer lock.Release()

	chdirOrFail(t, filepath.Join(env.WorktreeDir, "feat-lock"))
	err = commands.Sync([]string{"-s"})
	if err == nil {
		t.Fatal("commands.Sync should reject when another sync holds the lock; returned nil")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("expected `already running` error; got: %v", err)
	}
}

