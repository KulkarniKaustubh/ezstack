package commands

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
)

// setupSyncStatsRepo builds a two-branch stack in an isolated EZSTACK_HOME
// and returns the worktree path of the tip branch, where Sync would be run
// from. Each child is one commit ahead of its parent so writeSyncStats has
// something to report.
func setupSyncStatsRepo(t *testing.T) (tipWT string) {
	t.Helper()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoDir := filepath.Join(tmpDir, "repo")
	worktrees := filepath.Join(tmpDir, "worktrees")
	cfgDir := filepath.Join(tmpDir, "config")
	for _, d := range []string{repoDir, worktrees, cfgDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	t.Setenv("EZSTACK_HOME", cfgDir)

	run := func(dir string, args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(repoDir, "init", "-q", "-b", "main")
	run(repoDir, "config", "user.email", "test@test.com")
	run(repoDir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "seed"), []byte("s"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	run(repoDir, "add", ".")
	run(repoDir, "commit", "-qm", "init")

	repoDir, _ = filepath.EvalSymlinks(repoDir)

	cfg := &config.Config{
		DefaultBaseBranch: "main",
		Repos: map[string]*config.RepoConfig{
			repoDir: {WorktreeBaseDir: worktrees},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("cfg save: %v", err)
	}

	mgr, err := stack.NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, err := mgr.CreateBranch("feat-a", "main", "", ""); err != nil {
		t.Fatalf("create feat-a: %v", err)
	}
	if _, err := mgr.CreateBranch("feat-b", "feat-a", "", ""); err != nil {
		t.Fatalf("create feat-b: %v", err)
	}

	// One commit on feat-a off main.
	aWT := filepath.Join(worktrees, "feat-a")
	if err := os.WriteFile(filepath.Join(aWT, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	run(aWT, "add", ".")
	run(aWT, "commit", "-qm", "a work")

	// One commit on feat-b off feat-a.
	bWT := filepath.Join(worktrees, "feat-b")
	if err := os.WriteFile(filepath.Join(bWT, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}
	run(bWT, "add", ".")
	run(bWT, "commit", "-qm", "b work")
	// Rebase feat-b onto feat-a's new tip so it picks up a's commit in its
	// ancestry and is "1 ahead of feat-a" rather than "2 ahead of main".
	run(bWT, "rebase", "-q", "feat-a")

	return bWT
}

// TestWriteSyncStats_EmitsHeaderAndBranches is the direct unit test for the
// content of `ezs sync --stats`. The feature had no coverage before; if the
// header ever drifts or a branch stops reporting, this test catches it.
func TestWriteSyncStats_EmitsHeaderAndBranches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX git semantics")
	}
	tipWT := setupSyncStatsRepo(t)

	var buf bytes.Buffer
	writeSyncStats(&buf, tipWT)
	got := buf.String()

	if !strings.Contains(got, "Sync stats (commits ahead of parent)") {
		t.Errorf("missing header:\n%s", got)
	}
	for _, branch := range []string{"feat-a", "feat-b"} {
		if !strings.Contains(got, branch) {
			t.Errorf("missing per-branch line for %q:\n%s", branch, got)
		}
	}
	// Each branch is exactly one commit ahead of its parent.
	if !strings.Contains(got, "1 commits") {
		t.Errorf("expected '1 commits' per branch:\n%s", got)
	}
}

// TestWriteSyncStats_SkipsRootBranches asserts branches whose Parent is empty
// (stack roots) are NOT listed — their "commits ahead of parent" is
// meaningless. Without this skip, the output would include noisy "<root>    0 commits"
// lines (or crash, depending on how GetCommitsAhead handles "").
func TestWriteSyncStats_SkipsRootBranches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX git semantics")
	}
	tipWT := setupSyncStatsRepo(t)

	var buf bytes.Buffer
	writeSyncStats(&buf, tipWT)
	got := buf.String()

	// "main" is the stack root — it has no parent and must not appear in
	// the per-branch block. The header mentions "parent" in passing, so
	// search specifically for a line pattern that would imply main was listed.
	for _, line := range strings.Split(got, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "•") && strings.Contains(trimmed, "main") {
			t.Errorf("stack root 'main' appeared in per-branch block:\n%s", got)
		}
	}
}

// TestWriteSyncStats_UnregisteredRepoIsNoOp covers the silent-return path:
// if cwd is not in any registered stack, writeSyncStats must write nothing
// rather than panicking. This matches the existing "return on manager error"
// guards in printSyncStats.
func TestWriteSyncStats_UnregisteredRepoIsNoOp(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EZSTACK_HOME", tmpDir)

	var buf bytes.Buffer
	writeSyncStats(&buf, tmpDir) // no git repo, no stack config
	if buf.Len() != 0 {
		t.Errorf("expected empty output on no-stack repo; got %q", buf.String())
	}
}
