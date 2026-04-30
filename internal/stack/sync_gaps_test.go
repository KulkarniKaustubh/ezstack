package stack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
)

// TestEnableRerere_SetsRepoConfig verifies the auto-enable wires `rerere`
// keys into the repo's local config. Calls the helper directly so the test
// doesn't depend on a sync flow with a remote (which would need a bare
// origin and a tracked branch just to invoke EnableRerere indirectly).
func TestEnableRerere_SetsRepoConfig(t *testing.T) {
	repoDir, _, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	out, _ := exec.Command("git", "-C", repoDir, "config", "--local", "--get", "rerere.enabled").Output()
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Fatalf("rerere.enabled was already %q before the test; setup is dirty", got)
	}

	if err := git.EnableRerere(repoDir); err != nil {
		t.Fatalf("EnableRerere: %v", err)
	}

	for _, key := range []string{"rerere.enabled", "rerere.autoupdate"} {
		out, _ := exec.Command("git", "-C", repoDir, "config", "--local", "--get", key).Output()
		if strings.TrimSpace(string(out)) != "true" {
			t.Errorf("expected %s=true at repo level; got %q", key, strings.TrimSpace(string(out)))
		}
	}

	// Idempotency: second invocation must not error and must keep the values.
	if err := git.EnableRerere(repoDir); err != nil {
		t.Fatalf("second EnableRerere: %v", err)
	}
	out, _ = exec.Command("git", "-C", repoDir, "config", "--local", "--get", "rerere.enabled").Output()
	if strings.TrimSpace(string(out)) != "true" {
		t.Errorf("rerere.enabled flipped after second call: got %q", strings.TrimSpace(string(out)))
	}
}

// TestEnableRerere_PreservesUserFalse verifies idempotency respects an
// explicit user choice: if the user has already set `rerere.enabled = false`,
// EnableRerere must NOT overwrite that.
func TestEnableRerere_PreservesUserFalse(t *testing.T) {
	repoDir, _, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	if err := exec.Command("git", "-C", repoDir, "config", "--local", "rerere.enabled", "false").Run(); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := git.EnableRerere(repoDir); err != nil {
		t.Fatalf("EnableRerere: %v", err)
	}

	out, _ := exec.Command("git", "-C", repoDir, "config", "--local", "--get", "rerere.enabled").Output()
	if got := strings.TrimSpace(string(out)); got != "false" {
		t.Errorf("user's explicit `false` was overwritten: got %q (auto-enable must respect user choice)", got)
	}
}

// TestIntegrateRemoteForBranch_NoPushSkipped verifies _nopush branches
// short-circuit even when origin/<branch> exists. Otherwise we'd be doing
// remote work for branches the user explicitly opted out of remote
// operations on.
func TestIntegrateRemoteForBranch_NoPushSkipped(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()

	mgr, _ := NewManager(repoDir)
	featPath := filepath.Join(worktreeBaseDir, "feat")
	mgr.CreateBranch("feat", "main", featPath, "")
	mgr, _ = NewManager(repoDir)
	os.WriteFile(filepath.Join(featPath, "feat.txt"), []byte("local\n"), 0644)
	exec.Command("git", "-C", featPath, "add", ".").Run()
	exec.Command("git", "-C", featPath, "commit", "-m", "feat").Run()
	exec.Command("git", "-C", featPath, "push", "-u", "origin", "feat").Run()

	// Mark the branch as _nopush in the cache.
	branch := mgr.GetBranch("feat")
	bc := mgr.stackConfig.Cache.GetBranchCache("feat")
	bc.Remote = config.RemoteNoPush
	mgr.stackConfig.Cache.SetBranchCache("feat", bc)
	branch.Remote = config.RemoteNoPush
	mgr.stackConfig.Save(repoDir)

	preLocal, _ := exec.Command("git", "-C", featPath, "rev-parse", "HEAD").Output()
	preLocalSHA := strings.TrimSpace(string(preLocal))

	// Simulate a teammate commit that we'd normally pull.
	collab, _ := os.MkdirTemp("", "collab-*")
	defer os.RemoveAll(collab)
	exec.Command("git", "clone", "-q", "-b", "feat", bareDir, collab).Run()
	exec.Command("git", "-C", collab, "config", "user.email", "c@c.com").Run()
	exec.Command("git", "-C", collab, "config", "user.name", "c").Run()
	os.WriteFile(filepath.Join(collab, "from-them.txt"), []byte("teammate\n"), 0644)
	exec.Command("git", "-C", collab, "add", ".").Run()
	exec.Command("git", "-C", collab, "commit", "-q", "-m", "collab").Run()
	exec.Command("git", "-C", collab, "push", "-q", "origin", "feat").Run()

	mgr.Fetch()
	res := mgr.integrateRemoteForBranch(branch)
	if res.Error != nil {
		t.Fatalf("integrate failed: %v", res.Error)
	}

	postLocal, _ := exec.Command("git", "-C", featPath, "rev-parse", "HEAD").Output()
	if got := strings.TrimSpace(string(postLocal)); got != preLocalSHA {
		t.Errorf("local HEAD moved on a _nopush branch: %s → %s (must be skipped)", preLocalSHA, got)
	}
}

// TestEndOfRunCleanup_PreservesSnapshotsOnError verifies the audit-fix
// tightening: an error result (not just HasConflict) must keep PreSyncCommits
// set so a retry can use them. Triggers an autostash failure by writing a
// stale lock file under the linked-worktree's git dir, which is the same
// failure mode tested in the existing autostash itest.
func TestEndOfRunCleanup_PreservesSnapshotsOnError(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()

	mgr, _ := NewManager(repoDir)
	featPath := filepath.Join(worktreeBaseDir, "feat")
	mgr.CreateBranch("feat", "main", featPath, "")
	mgr, _ = NewManager(repoDir)
	os.WriteFile(filepath.Join(featPath, "feat.txt"), []byte("v1\n"), 0644)
	exec.Command("git", "-C", featPath, "add", ".").Run()
	exec.Command("git", "-C", featPath, "commit", "-m", "v1").Run()
	exec.Command("git", "-C", featPath, "push", "-u", "origin", "feat").Run()

	// Advance main so feat is behind.
	os.WriteFile(filepath.Join(repoDir, "feat.txt"), []byte("from-main\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "main edit").Run()
	exec.Command("git", "-C", repoDir, "push", "origin", "main").Run()

	// Make worktree dirty so autostash fires, then squat on the index lock so
	// stash push fails — this triggers the engine's "refusing to rebase over
	// uncommitted changes" error path.
	os.WriteFile(filepath.Join(featPath, "uncommitted.txt"), []byte("dirty\n"), 0644)
	lockPath := filepath.Join(repoDir, ".git", "worktrees", "feat", "index.lock")
	os.WriteFile(lockPath, []byte("squatter"), 0644)
	defer os.Remove(lockPath)

	mgr, _ = NewManager(repoDir)
	results, err := mgr.SyncStackAll(nil, &SyncCallbacks{Autostash: true})
	if err != nil {
		t.Logf("SyncStackAll err (expected): %v", err)
	}
	hadError := false
	for _, r := range results {
		if r.Error != nil {
			hadError = true
		}
	}
	if !hadError {
		t.Fatal("expected at least one result with Error (autostash failure)")
	}

	// The snapshot for feat must still be set — a retry needs it.
	mgr, _ = NewManager(repoDir)
	bc := mgr.stackConfig.Cache.GetBranchCache("feat")
	if bc == nil || bc.PreSyncCommit == "" {
		t.Errorf("PreSyncCommit was cleared despite Error result; retry would lose snapshot info. bc=%+v", bc)
	}
}

// TestLookupPreSyncSHA_FallsBackWhenSnapshotMissing verifies the case the
// cross-stack-parent fallback in syncStackInternal relies on: when the
// caller's snapshotted set doesn't include a branch, lookupPreSyncSHA must
// still return *something* useful — either the persisted PreSyncCommit or
// the branch's current HEAD as a degraded fallback.
func TestLookupPreSyncSHA_FallsBackWhenSnapshotMissing(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("solo", "main", filepath.Join(worktreeBaseDir, "solo"), "")
	mgr, _ = NewManager(repoDir)
	soloHead, _ := mgr.git.GetBranchCommit("solo")

	// No PreSyncCommit set — falls back to live HEAD.
	if got := mgr.lookupPreSyncSHA("solo"); got != soloHead {
		t.Errorf("fallback to live HEAD: got %q, want %q", got, soloHead)
	}

	// Set PreSyncCommit to the live HEAD; lookup returns it (RefExists ok).
	bc := &config.BranchCache{PreSyncCommit: soloHead}
	mgr.stackConfig.Cache.SetBranchCache("solo", bc)
	if got := mgr.lookupPreSyncSHA("solo"); got != soloHead {
		t.Errorf("snapshot lookup: got %q, want %q", got, soloHead)
	}

	// Set PreSyncCommit to a fabricated SHA — RefExists fails, lookup
	// should drop the bad snapshot and return live HEAD.
	bc.PreSyncCommit = "0000000000000000000000000000000000000000"
	mgr.stackConfig.Cache.SetBranchCache("solo", bc)
	if got := mgr.lookupPreSyncSHA("solo"); got != soloHead {
		t.Errorf("stale-snapshot fallback: got %q, want %q (live HEAD)", got, soloHead)
	}
	// The bad snapshot should now be cleared.
	if bc2 := mgr.stackConfig.Cache.GetBranchCache("solo"); bc2 != nil && bc2.PreSyncCommit != "" {
		t.Errorf("stale snapshot should be cleared after lookup; got %q", bc2.PreSyncCommit)
	}
}

// TestRebaseOnParent_SnapshotsBeforeRewrite verifies that calling
// RebaseOnParent on a branch sets PreSyncCommit on it before the rebase
// runs, so any subsequent SyncBranch on a child has the right oldBase to
// pass to --onto. (The actual --onto invocation is exercised indirectly via
// the cascade itests; doing it directly here ran into rerere-replay
// weirdness because the test inherits any earlier resolution.)
func TestRebaseOnParent_SnapshotsBeforeRewrite(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	bareDir := filepath.Join(filepath.Dir(repoDir), "bare.git")
	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "-C", repoDir, "remote", "add", "origin", bareDir).Run()
	exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main").Run()

	mgr, _ := NewManager(repoDir)
	bPath := filepath.Join(worktreeBaseDir, "b")
	mgr.CreateBranch("b", "main", bPath, "")
	mgr, _ = NewManager(repoDir)
	os.WriteFile(filepath.Join(bPath, "b.txt"), []byte("b\n"), 0644)
	exec.Command("git", "-C", bPath, "add", ".").Run()
	exec.Command("git", "-C", bPath, "commit", "-m", "b1").Run()

	// Advance main so RebaseOnParent has work.
	os.WriteFile(filepath.Join(repoDir, "main-only.txt"), []byte("from main\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "main update").Run()
	exec.Command("git", "-C", repoDir, "push", "origin", "main").Run()

	mgr, _ = NewManager(bPath)
	preBSHA, _ := mgr.git.GetBranchCommit("b")

	if err := mgr.RebaseOnParent(false); err != nil {
		t.Fatalf("RebaseOnParent: %v", err)
	}

	// Two assertions:
	// 1. PreSyncCommit[b] must equal the pre-rebase SHA (snapshot was taken).
	mgr, _ = NewManager(bPath)
	bc := mgr.stackConfig.Cache.GetBranchCache("b")
	if bc == nil || bc.PreSyncCommit != preBSHA {
		t.Errorf("RebaseOnParent did not snapshot b before rewrite. bc=%+v want PreSyncCommit=%s", bc, preBSHA)
	}
	// 2. b's HEAD has actually moved (rebase succeeded).
	postBSHA, _ := mgr.git.GetBranchCommit("b")
	if postBSHA == preBSHA {
		t.Errorf("RebaseOnParent did not advance b's HEAD; rebase didn't run")
	}
}

// TestRebaseChildren_GrandchildrenCascade is a regression for the audit-fix
// where eager-clearing PreSyncCommit[child] inside RebaseChildren broke the
// recursive grandchildren rebase (it lost the snapshot it needed for --onto).
//
// Setup: parent → child → grandchild, parent gets an update, RebaseChildren
// rebases child and recursively rebases grandchild. Grandchild must NOT
// re-encounter the cascade.
func TestRebaseChildren_GrandchildrenCascade(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	baseLines := "L1\nL2\nL3\nL4\nL5\nL6\nL7\nL8\nL9\nL10\nL11\nL12\n"
	os.WriteFile(filepath.Join(repoDir, "shared.txt"), []byte(baseLines), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "seed").Run()

	mgr, _ := NewManager(repoDir)
	editLine := func(path string, idx int, val string) {
		raw, _ := os.ReadFile(path)
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		lines[idx] = val
		os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
	}
	mustCreate := func(name, parent string, lineIdx int, val string) string {
		path := filepath.Join(worktreeBaseDir, name)
		mgr.CreateBranch(name, parent, path, "")
		mgr, _ = NewManager(repoDir)
		editLine(filepath.Join(path, "shared.txt"), lineIdx, val)
		exec.Command("git", "-C", path, "add", ".").Run()
		exec.Command("git", "-C", path, "commit", "-m", name).Run()
		return path
	}

	parentPath := mustCreate("parent", "main", 0, "L1-PARENT")
	_ = mustCreate("child", "parent", 5, "L6-CHILD")
	grandPath := mustCreate("grandchild", "child", 10, "L11-GRAND")

	// Parent gets a new commit on a far-apart line; this triggers cascade
	// risk for both child and grandchild.
	editLine(filepath.Join(parentPath, "shared.txt"), 3, "L4-PARENT-NEW")
	exec.Command("git", "-C", parentPath, "add", ".").Run()
	exec.Command("git", "-C", parentPath, "commit", "-m", "parent v2").Run()

	// Drive RebaseChildren from parent's worktree.
	mgr, _ = NewManager(parentPath)
	results, err := mgr.RebaseChildren()
	if err != nil {
		t.Fatalf("RebaseChildren: %v", err)
	}
	for _, r := range results {
		if r.HasConflict || r.Error != nil {
			t.Fatalf("unexpected failure for %s: conflict=%v err=%v", r.Branch, r.HasConflict, r.Error)
		}
	}

	// Grandchild must contain ALL three edits (parent-new, child, grand)
	// and L1-PARENT (the original parent commit) — no L1-A-like duplicates.
	grandRaw, _ := os.ReadFile(filepath.Join(grandPath, "shared.txt"))
	got := string(grandRaw)
	for _, want := range []string{"L1-PARENT", "L4-PARENT-NEW", "L6-CHILD", "L11-GRAND"} {
		if !strings.Contains(got, want) {
			t.Errorf("grandchild's tree missing %q after RebaseChildren cascade: %q", want, got)
		}
	}
}
