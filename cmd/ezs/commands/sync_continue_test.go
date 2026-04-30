package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// setupTwoStackConflictEnv builds a repo with two stacks each containing one
// branch, where both branches have a commit that conflicts with a subsequent
// main update. Running SyncStackAll triggers in-progress rebases on both
// stacks, providing the substrate to test --continue scope handling.
func setupTwoStackConflictEnv(t *testing.T) (mgr *stack.Manager, repoDir, worktreeBaseDir string, cleanup func()) {
	t.Helper()
	tmp, err := os.MkdirTemp("", "continue-test-*")
	if err != nil {
		t.Fatal(err)
	}
	tmp, _ = filepath.EvalSymlinks(tmp)
	repoDir = filepath.Join(tmp, "repo")
	worktreeBaseDir = filepath.Join(tmp, "worktrees")
	cfgDir := filepath.Join(tmp, "config")
	for _, d := range []string{repoDir, worktreeBaseDir, cfgDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("EZSTACK_HOME", cfgDir)

	exec.Command("git", "-C", repoDir, "init", "-b", "main").Run()
	exec.Command("git", "-C", repoDir, "config", "user.email", "t@t.com").Run()
	exec.Command("git", "-C", repoDir, "config", "user.name", "t").Run()
	// Two seed files, one per stack, so each branch's commit can independently
	// conflict against a corresponding main update.
	for _, name := range []string{"x.txt", "y.txt"} {
		os.WriteFile(filepath.Join(repoDir, name), []byte("base\n"), 0644)
	}
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "seed").Run()
	repoDir, _ = filepath.EvalSymlinks(repoDir)

	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()

	cfg := &config.Config{
		DefaultBaseBranch: "main",
		Repos: map[string]*config.RepoConfig{
			repoDir: {WorktreeBaseDir: worktreeBaseDir},
		},
	}
	cfg.Save()

	mgr, err = stack.NewManager(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	mustCreate := func(name, file, content string) {
		path := filepath.Join(worktreeBaseDir, name)
		// Force separate stacks even when both branches share root=main.
		// findOrCreateStack would otherwise consolidate them via
		// findUniqueStackByRoot, putting both branches in one stack.
		if _, err := mgr.CreateBranch(name, "main", path, "new"); err != nil {
			t.Fatalf("CreateBranch %s: %v", name, err)
		}
		mgr, _ = stack.NewManager(repoDir)
		os.WriteFile(filepath.Join(path, file), []byte(content), 0644)
		exec.Command("git", "-C", path, "add", ".").Run()
		exec.Command("git", "-C", path, "commit", "-m", "feat-"+name).Run()
	}
	mustCreate("x-feat", "x.txt", "x-FEAT\n")
	mustCreate("y-feat", "y.txt", "y-FEAT\n")

	// Update main to conflict with both branches.
	os.WriteFile(filepath.Join(repoDir, "x.txt"), []byte("x-MAIN\n"), 0644)
	os.WriteFile(filepath.Join(repoDir, "y.txt"), []byte("y-MAIN\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "main edits").Run()
	exec.Command("git", "-C", repoDir, "push", "origin", "main").Run()

	cleanup = func() {
		// Best-effort: abort any in-progress rebases so worktrees can be
		// removed cleanly. A leftover .git/rebase-merge directory inside a
		// worktree blocks os.RemoveAll on some filesystems.
		for _, name := range []string{"x-feat", "y-feat"} {
			p := filepath.Join(worktreeBaseDir, name)
			exec.Command("git", "-C", p, "rebase", "--abort").Run()
		}
		os.RemoveAll(tmp)
	}
	return mgr, repoDir, worktreeBaseDir, cleanup
}

// TestSyncContinue_ScopedToCurrentStackDoesNotTouchOtherStacks reproduces the
// user's primary complaint: `ezs sync -s --continue` was continuing rebases in
// every stack rather than only the current stack's. After the fix, only the
// in-scope stack's rebase is resumed; out-of-scope stacks remain in their
// in-progress state for the user to handle separately.
func TestSyncContinue_ScopedToCurrentStackDoesNotTouchOtherStacks(t *testing.T) {
	prevYes := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = prevYes }()

	mgr, _, worktreeBaseDir, cleanup := setupTwoStackConflictEnv(t)
	defer cleanup()

	// Trigger conflicts on both stacks via SyncStackAll.
	if _, err := mgr.SyncStackAll(nil, nil); err != nil {
		t.Fatalf("SyncStackAll: %v", err)
	}
	xPath := filepath.Join(worktreeBaseDir, "x-feat")
	yPath := filepath.Join(worktreeBaseDir, "y-feat")

	xGit := git.New(xPath)
	yGit := git.New(yPath)
	xRebase, _ := xGit.IsRebaseInProgress()
	yRebase, _ := yGit.IsRebaseInProgress()
	if !xRebase || !yRebase {
		t.Fatalf("expected both stacks to be mid-rebase; got xRebase=%v yRebase=%v", xRebase, yRebase)
	}

	// Resolve x's conflict only.
	os.WriteFile(filepath.Join(xPath, "x.txt"), []byte("x-RESOLVED\n"), 0644)
	exec.Command("git", "-C", xPath, "add", ".").Run()

	// Build a scope pinned to x-feat's stack.
	xMgr, _ := stack.NewManager(xPath)
	xStack := xMgr.GetStackForBranch("x-feat")
	if xStack == nil {
		t.Fatal("x-feat stack not found")
	}
	scope := continueScope{mode: continueModeCurrentStack, stack: xStack}

	// Re-open from x-feat's worktree so syncContinue's GetCurrentStack-style
	// lookups resolve consistently.
	if err := syncContinue(xMgr, nil, false, scope); err != nil {
		t.Fatalf("syncContinue (scope=x): %v", err)
	}

	// Assert: x's rebase is done, y's is still in progress.
	xRebaseAfter, _ := xGit.IsRebaseInProgress()
	yRebaseAfter, _ := yGit.IsRebaseInProgress()
	if xRebaseAfter {
		t.Errorf("x-feat should be fully rebased; rebase still in progress")
	}
	if !yRebaseAfter {
		t.Errorf("y-feat must NOT have been touched; expected its rebase still in progress (the original bug)")
	}
}

// TestSyncContinue_DescendantSubtreeReSyncsAfterRoot exercises B6: after the
// root of a deep stack has its conflict resolved and is `--continue`d, every
// descendant gets re-synced (not just immediate children).
func TestSyncContinue_DescendantSubtreeReSyncsAfterRoot(t *testing.T) {
	prevYes := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = prevYes }()

	tmp, err := os.MkdirTemp("", "continue-deep-*")
	if err != nil {
		t.Fatal(err)
	}
	tmp, _ = filepath.EvalSymlinks(tmp)
	defer os.RemoveAll(tmp)
	repoDir := filepath.Join(tmp, "repo")
	worktreeBaseDir := filepath.Join(tmp, "worktrees")
	cfgDir := filepath.Join(tmp, "config")
	for _, d := range []string{repoDir, worktreeBaseDir, cfgDir} {
		os.MkdirAll(d, 0755)
	}
	t.Setenv("EZSTACK_HOME", cfgDir)

	exec.Command("git", "-C", repoDir, "init", "-b", "main").Run()
	exec.Command("git", "-C", repoDir, "config", "user.email", "t@t.com").Run()
	exec.Command("git", "-C", repoDir, "config", "user.name", "t").Run()
	// 12-line file so per-branch edits stay outside each other's diff context.
	baseLines := strings.Repeat("base\n", 12)
	os.WriteFile(filepath.Join(repoDir, "shared.txt"), []byte(baseLines), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "seed").Run()
	repoDir, _ = filepath.EvalSymlinks(repoDir)
	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()

	cfg := &config.Config{
		DefaultBaseBranch: "main",
		Repos:             map[string]*config.RepoConfig{repoDir: {WorktreeBaseDir: worktreeBaseDir}},
	}
	cfg.Save()

	writeShared := func(dir string, edits map[int]string) {
		lines := strings.Split(strings.TrimRight(baseLines, "\n"), "\n")
		for idx, val := range edits {
			lines[idx] = val
		}
		os.WriteFile(filepath.Join(dir, "shared.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0644)
	}

	mgr, _ := stack.NewManager(repoDir)
	mustCreate := func(name, parent string, edits map[int]string) string {
		path := filepath.Join(worktreeBaseDir, name)
		if _, err := mgr.CreateBranch(name, parent, path, ""); err != nil {
			t.Fatalf("CreateBranch %s: %v", name, err)
		}
		mgr, _ = stack.NewManager(repoDir)
		writeShared(path, edits)
		exec.Command("git", "-C", path, "add", ".").Run()
		exec.Command("git", "-C", path, "commit", "-m", "feat-"+name).Run()
		return path
	}

	aPath := mustCreate("a", "main", map[int]string{0: "a-feat"})
	mustCreate("b", "a", map[int]string{0: "a-feat", 5: "b-feat"})
	mustCreate("c", "b", map[int]string{0: "a-feat", 5: "b-feat", 10: "c-feat"})

	// main update conflicts only with a's edit on line 0.
	writeShared(repoDir, map[int]string{0: "a-MAIN"})
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "main").Run()
	exec.Command("git", "-C", repoDir, "push", "origin", "main").Run()

	mgr, _ = stack.NewManager(aPath)
	if _, err := mgr.SyncStack(nil, nil); err != nil {
		t.Fatalf("SyncStack: %v", err)
	}
	// Resolve a's conflict.
	writeShared(aPath, map[int]string{0: "a-RESOLVED"})
	exec.Command("git", "-C", aPath, "add", ".").Run()

	// Re-open from a's worktree and scope to a's stack. Avoid GetCurrentStack
	// because mid-rebase HEAD is detached, so the worktree's "current branch"
	// doesn't resolve via git's CurrentBranch — look up the stack directly.
	mgr, _ = stack.NewManager(aPath)
	stk := mgr.GetStackForBranch("a")
	if stk == nil {
		t.Fatal("a's stack not found")
	}
	scope := continueScope{mode: continueModeCurrentStack, stack: stk}
	if err := syncContinue(mgr, nil, false, scope); err != nil {
		t.Fatalf("syncContinue: %v", err)
	}

	// Assert: a, b, c are all rebased. None has an in-progress state. b and c
	// each contain a's resolved line 0 plus their own edits.
	for _, name := range []string{"a", "b", "c"} {
		p := filepath.Join(worktreeBaseDir, name)
		g := git.New(p)
		if ip, _ := g.IsRebaseInProgress(); ip {
			t.Errorf("%s: rebase still in progress after subtree --continue", name)
		}
	}
	bShared, _ := os.ReadFile(filepath.Join(worktreeBaseDir, "b", "shared.txt"))
	if !strings.Contains(string(bShared), "a-RESOLVED") || !strings.Contains(string(bShared), "b-feat") {
		t.Errorf("b's shared.txt should hold a-RESOLVED + b-feat; got:\n%s", string(bShared))
	}
	cShared, _ := os.ReadFile(filepath.Join(worktreeBaseDir, "c", "shared.txt"))
	if !strings.Contains(string(cShared), "a-RESOLVED") || !strings.Contains(string(cShared), "b-feat") || !strings.Contains(string(cShared), "c-feat") {
		t.Errorf("c's shared.txt should hold a-RESOLVED + b-feat + c-feat; got:\n%s", string(cShared))
	}
}

// TestSyncContinue_PartialReturnsErrSyncIncomplete verifies B8: when a
// continued branch finishes cleanly but a descendant re-sync hits a fresh
// conflict, syncContinue returns ErrSyncIncomplete so callers and scripts can
// detect partial completion.
func TestSyncContinue_PartialReturnsErrSyncIncomplete(t *testing.T) {
	prevYes := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = prevYes }()

	tmp, err := os.MkdirTemp("", "continue-partial-*")
	if err != nil {
		t.Fatal(err)
	}
	tmp, _ = filepath.EvalSymlinks(tmp)
	defer os.RemoveAll(tmp)
	repoDir := filepath.Join(tmp, "repo")
	worktreeBaseDir := filepath.Join(tmp, "worktrees")
	cfgDir := filepath.Join(tmp, "config")
	for _, d := range []string{repoDir, worktreeBaseDir, cfgDir} {
		os.MkdirAll(d, 0755)
	}
	t.Setenv("EZSTACK_HOME", cfgDir)

	exec.Command("git", "-C", repoDir, "init", "-b", "main").Run()
	exec.Command("git", "-C", repoDir, "config", "user.email", "t@t.com").Run()
	exec.Command("git", "-C", repoDir, "config", "user.name", "t").Run()
	os.WriteFile(filepath.Join(repoDir, "shared.txt"), []byte("seed\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "seed").Run()
	repoDir, _ = filepath.EvalSymlinks(repoDir)
	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()
	cfg := &config.Config{
		DefaultBaseBranch: "main",
		Repos:             map[string]*config.RepoConfig{repoDir: {WorktreeBaseDir: worktreeBaseDir}},
	}
	cfg.Save()

	mgr, _ := stack.NewManager(repoDir)
	aPath := filepath.Join(worktreeBaseDir, "a")
	mgr.CreateBranch("a", "main", aPath, "")
	mgr, _ = stack.NewManager(repoDir)
	os.WriteFile(filepath.Join(aPath, "shared.txt"), []byte("a-FEAT\n"), 0644)
	exec.Command("git", "-C", aPath, "add", ".").Run()
	exec.Command("git", "-C", aPath, "commit", "-m", "a1").Run()

	bPath := filepath.Join(worktreeBaseDir, "b")
	mgr.CreateBranch("b", "a", bPath, "")
	mgr, _ = stack.NewManager(repoDir)
	// b also edits the same single line — so any change to that line in main
	// or a will conflict with b too.
	os.WriteFile(filepath.Join(bPath, "shared.txt"), []byte("b-FEAT\n"), 0644)
	exec.Command("git", "-C", bPath, "add", ".").Run()
	exec.Command("git", "-C", bPath, "commit", "-m", "b1").Run()

	// main update conflicts with both a and b.
	os.WriteFile(filepath.Join(repoDir, "shared.txt"), []byte("MAIN\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "main").Run()
	exec.Command("git", "-C", repoDir, "push", "origin", "main").Run()

	mgr, _ = stack.NewManager(aPath)
	mgr.SyncStack(nil, nil)
	// Resolve a's conflict only.
	os.WriteFile(filepath.Join(aPath, "shared.txt"), []byte("a-RESOLVED\n"), 0644)
	exec.Command("git", "-C", aPath, "add", ".").Run()

	mgr, _ = stack.NewManager(aPath)
	stk := mgr.GetStackForBranch("a")
	if stk == nil {
		t.Fatal("a's stack not found")
	}
	scope := continueScope{mode: continueModeCurrentStack, stack: stk}

	err = syncContinue(mgr, nil, false, scope)
	if err != ErrSyncIncomplete {
		t.Errorf("expected ErrSyncIncomplete (b's re-sync should hit a fresh conflict); got %v", err)
	}
}
