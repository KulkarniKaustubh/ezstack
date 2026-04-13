package itests

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/cmd/ezs/commands"
)

// installFailingHook writes a hook into ~/.ezstack/hooks/<name> that exits 1
// and also writes a sentinel file when invoked, so tests can detect both the
// error return and the fact that the hook ran at all.
func installFailingHook(t *testing.T, env *TestEnv, name, sentinel string) {
	t.Helper()
	hooksDir := filepath.Join(env.ConfigDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\ntouch " + sentinel + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(hooksDir, name), []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
}

// headCommit returns the HEAD commit SHA of the given directory.
func headCommit(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD in %s: %v", dir, err)
	}
	return strings.TrimSpace(string(out))
}

// TestSyncDryRun_NoHooksFire asserts that `ezs sync --dry-run` does NOT run
// pre-sync (or post-sync) hooks. Regression for the bug where hooks fired
// unconditionally before the dryRun guard.
func TestSyncDryRun_NoHooksFire(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX hook semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Build a two-branch stack so sync has something to operate on.
	CreateBranchWithCommit(t, env, "feat-a", "main")
	CreateBranchWithCommit(t, env, "feat-b", "feat-a")

	sentinel := filepath.Join(env.TmpDir, "pre_sync_ran")
	installFailingHook(t, env, "pre-sync", sentinel)

	// Chdir into feat-b's worktree so Sync picks up a real stack.
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(filepath.Join(env.WorktreeDir, "feat-b")); err != nil {
		t.Fatal(err)
	}

	// Sync --dry-run may return a non-nil error in this test environment
	// (no real origin to fetch from), but what we care about is that the
	// pre-sync hook NEVER ran. Without the fix, the error mentions the hook
	// and the sentinel file exists; with the fix, neither is true.
	err := commands.Sync([]string{"--dry-run"})
	if err != nil && strings.Contains(err.Error(), "pre-sync") {
		t.Errorf("Sync --dry-run fired pre-sync hook: %v", err)
	}
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Error("pre-sync hook sentinel exists — hook fired on --dry-run")
	}
}

// TestSyncDryRun_NoSquash asserts that `ezs sync --dry-run --squash` does NOT
// rewrite child-branch history. Regression for the bug where squash ran
// before the dry-run guard.
func TestSyncDryRun_NoSquash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feat-a", "main")
	// Two commits on feat-b so squash would actually collapse something.
	CreateBranchWithCommit(t, env, "feat-b", "feat-a")
	GitCommit(t, filepath.Join(env.WorktreeDir, "feat-b"), "feat-b-2.txt", "x", "second")

	bWt := filepath.Join(env.WorktreeDir, "feat-b")
	before := headCommit(t, bWt)

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(bWt); err != nil {
		t.Fatal(err)
	}

	// The fetch step may fail in this test env (no real origin) and that
	// is fine — we only care that --squash did not rewrite history before
	// the dry-run guard short-circuited.
	_ = commands.Sync([]string{"--dry-run", "--squash"})

	after := headCommit(t, bWt)
	if before != after {
		t.Errorf("feat-b HEAD changed on --dry-run --squash: %s -> %s", before, after)
	}
}
