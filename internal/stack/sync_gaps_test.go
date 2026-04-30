package stack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

// TestEnsureRerereEnabled_CachesAfterFirstCall verifies that the
// Manager-level wrapper runs git.EnableRerere at most once per Manager
// lifetime. The lower-level EnableRerere is itself idempotent, but it
// forks two `git config` processes per call; caching matters because
// every sync entry point ensures rerere is on.
//
// Strategy: invoke once to seed, then manually flip the config to false
// and call again. If caching works, the second call is a no-op and the
// `false` value survives. If caching is broken, the second call overwrites
// the user's value back to `true`.
func TestEnsureRerereEnabled_CachesAfterFirstCall(t *testing.T) {
	repoDir, _, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, err := NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	mgr.ensureRerereEnabled()
	if got, _ := exec.Command("git", "-C", repoDir, "config", "--local", "--get", "rerere.enabled").Output(); strings.TrimSpace(string(got)) != "true" {
		t.Fatalf("first ensureRerereEnabled did not set rerere.enabled=true; got %q", strings.TrimSpace(string(got)))
	}

	// Flip the value behind the Manager's back. If caching is honored, the
	// next call must NOT touch the config and the `false` survives.
	if err := exec.Command("git", "-C", repoDir, "config", "--local", "rerere.enabled", "false").Run(); err != nil {
		t.Fatalf("flip rerere.enabled=false: %v", err)
	}

	mgr.ensureRerereEnabled()
	got, _ := exec.Command("git", "-C", repoDir, "config", "--local", "--get", "rerere.enabled").Output()
	if strings.TrimSpace(string(got)) != "false" {
		t.Errorf("second ensureRerereEnabled re-ran git.EnableRerere instead of caching; rerere.enabled=%q (expected user-set `false` to survive)", strings.TrimSpace(string(got)))
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
//
// The descendantName argument is empty in this test because we're inspecting
// a parent in isolation — the ancestor check is exercised by
// TestLookupPreSyncSHA_RejectsStaleSnapshotForDescendant below.
func TestLookupPreSyncSHA_FallsBackWhenSnapshotMissing(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	mgr.CreateBranch("solo", "main", filepath.Join(worktreeBaseDir, "solo"), "")
	mgr, _ = NewManager(repoDir)
	soloHead, _ := mgr.git.GetBranchCommit("solo")

	// No PreSyncCommit set — falls back to live HEAD.
	if got := mgr.lookupPreSyncSHA("solo", ""); got != soloHead {
		t.Errorf("fallback to live HEAD: got %q, want %q", got, soloHead)
	}

	// Set PreSyncCommit to the live HEAD; lookup returns it (RefExists ok).
	bc := &config.BranchCache{PreSyncCommit: soloHead}
	mgr.stackConfig.Cache.SetBranchCache("solo", bc)
	if got := mgr.lookupPreSyncSHA("solo", ""); got != soloHead {
		t.Errorf("snapshot lookup: got %q, want %q", got, soloHead)
	}

	// Set PreSyncCommit to a fabricated SHA — RefExists fails, lookup
	// should drop the bad snapshot and return live HEAD.
	bc.PreSyncCommit = "0000000000000000000000000000000000000000"
	mgr.stackConfig.Cache.SetBranchCache("solo", bc)
	if got := mgr.lookupPreSyncSHA("solo", ""); got != soloHead {
		t.Errorf("stale-snapshot fallback: got %q, want %q (live HEAD)", got, soloHead)
	}
	// The bad snapshot should now be cleared.
	if bc2 := mgr.stackConfig.Cache.GetBranchCache("solo"); bc2 != nil && bc2.PreSyncCommit != "" {
		t.Errorf("stale snapshot should be cleared after lookup; got %q", bc2.PreSyncCommit)
	}
}

// TestLookupPreSyncSHA_RejectsStaleSnapshotForDescendant exercises the
// audit-fix ancestor guard: when a parent's PreSyncCommit is set to a SHA
// that is NOT in the descendant's history (e.g. the parent was rebased a
// second time, overwriting the original snapshot), lookupPreSyncSHA must
// reject it and fall back to the parent's live HEAD. Otherwise
// `git rebase --onto newParent staleSnapshot` would compute a wrong commit
// range and silently produce garbage.
func TestLookupPreSyncSHA_RejectsStaleSnapshotForDescendant(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	parentPath := filepath.Join(worktreeBaseDir, "parent")
	mgr.CreateBranch("parent", "main", parentPath, "")
	mgr, _ = NewManager(repoDir)
	os.WriteFile(filepath.Join(parentPath, "p.txt"), []byte("p1\n"), 0644)
	exec.Command("git", "-C", parentPath, "add", ".").Run()
	exec.Command("git", "-C", parentPath, "commit", "-m", "p1").Run()

	childPath := filepath.Join(worktreeBaseDir, "child")
	mgr.CreateBranch("child", "parent", childPath, "")
	mgr, _ = NewManager(repoDir)
	os.WriteFile(filepath.Join(childPath, "c.txt"), []byte("c1\n"), 0644)
	exec.Command("git", "-C", childPath, "add", ".").Run()
	exec.Command("git", "-C", childPath, "commit", "-m", "c1").Run()

	// Compute a SHA that is reachable in the repo but NOT in child's
	// history. main's tip works: child diverged from main at the parent's
	// branchpoint, so main's tip (which has no other commits since seed)
	// IS actually a parent-ancestor… use a different strategy: create an
	// orphan branch with one commit, capture its SHA, then delete the
	// branch ref. The commit object survives reachable from reflog briefly.
	exec.Command("git", "-C", repoDir, "checkout", "-b", "orphan-tmp").Run()
	os.WriteFile(filepath.Join(repoDir, "orphan.txt"), []byte("orphan\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "orphan").Run()
	orphanSHA, _ := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	staleSHA := strings.TrimSpace(string(orphanSHA))
	exec.Command("git", "-C", repoDir, "checkout", "main").Run()

	mgr, _ = NewManager(repoDir)
	// Plant the stale snapshot on parent. It exists as an object but is
	// NOT on child's history.
	bc := &config.BranchCache{PreSyncCommit: staleSHA}
	mgr.stackConfig.Cache.SetBranchCache("parent", bc)

	parentLiveHead, _ := mgr.git.GetBranchCommit("parent")

	// Lookup with descendantName="child" — must reject the stale snapshot
	// (not on child's history) and return parent's live HEAD instead.
	got := mgr.lookupPreSyncSHA("parent", "child")
	if got == staleSHA {
		t.Errorf("lookupPreSyncSHA returned stale snapshot %q for descendant whose history doesn't include it; --onto with this SHA would walk a wrong commit range", staleSHA)
	}
	if got != parentLiveHead {
		t.Errorf("lookupPreSyncSHA fell back to %q, want parent's live HEAD %q", got, parentLiveHead)
	}

	// Without descendantName, the lookup keeps the legacy behavior:
	// returns the snapshot as long as it resolves to a real object.
	if got := mgr.lookupPreSyncSHA("parent", ""); got != staleSHA {
		t.Errorf("lookupPreSyncSHA(parent, \"\") = %q, want %q (no descendant context = no ancestor check)", got, staleSHA)
	}
}

// TestSnapshotPreSyncSHAs_PreservesSnapshotNeededByDescendant covers the
// "rebased twice without children being touched" hazard. Without the
// preserve-when-still-needed guard, the second snapshot call would
// overwrite the first, losing the SHA descendants need as their --onto
// oldBase. The follow-up assertion verifies the guard yields once the
// snapshot is no longer reachable from any descendant.
func TestSnapshotPreSyncSHAs_PreservesSnapshotNeededByDescendant(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	parentPath := filepath.Join(worktreeBaseDir, "parent")
	mgr.CreateBranch("parent", "main", parentPath, "")
	mgr, _ = NewManager(repoDir)
	os.WriteFile(filepath.Join(parentPath, "p.txt"), []byte("p1\n"), 0644)
	exec.Command("git", "-C", parentPath, "add", ".").Run()
	exec.Command("git", "-C", parentPath, "commit", "-m", "p1").Run()
	parentV0, _ := mgr.git.GetBranchCommit("parent")

	childPath := filepath.Join(worktreeBaseDir, "child")
	mgr.CreateBranch("child", "parent", childPath, "")
	mgr, _ = NewManager(repoDir)
	os.WriteFile(filepath.Join(childPath, "c.txt"), []byte("c1\n"), 0644)
	exec.Command("git", "-C", childPath, "add", ".").Run()
	exec.Command("git", "-C", childPath, "commit", "-m", "c1").Run()
	mgr, _ = NewManager(repoDir)

	// Take the first snapshot at parent's pre-rewrite HEAD.
	mgr.snapshotPreSyncSHAs([]string{"parent"})
	if bc := mgr.stackConfig.Cache.GetBranchCache("parent"); bc == nil || bc.PreSyncCommit != parentV0 {
		t.Fatalf("first snapshot did not record parent's HEAD; bc=%+v want PreSyncCommit=%s", bc, parentV0)
	}

	// Rewrite parent's history with `commit --amend` so parentV0 is no
	// longer reachable from parent's new tip. Child's tip is still
	// anchored at parentV0 (its merge-base) because child has NOT been
	// re-synced. This is exactly the "rebased twice without descendants
	// being touched" state the guard exists to handle.
	os.WriteFile(filepath.Join(parentPath, "p.txt"), []byte("p1-rewritten\n"), 0644)
	exec.Command("git", "-C", parentPath, "add", ".").Run()
	exec.Command("git", "-C", parentPath, "commit", "--amend", "-m", "p1 (rewritten)").Run()
	parentV1, _ := mgr.git.GetBranchCommit("parent")
	if parentV1 == parentV0 {
		t.Fatal("setup: parent's HEAD did not change after amend")
	}
	if isAnc, err := mgr.git.IsAncestor(parentV0, "parent"); err != nil || isAnc {
		t.Fatalf("setup: expected parentV0 to NOT be in parent's history after amend (isAnc=%v err=%v)", isAnc, err)
	}
	if isAnc, err := mgr.git.IsAncestor(parentV0, "child"); err != nil || !isAnc {
		t.Fatalf("setup: expected parentV0 to still be in child's history (isAnc=%v err=%v)", isAnc, err)
	}

	// Second snapshot: guard preserves parentV0 because child still anchored on it.
	mgr.snapshotPreSyncSHAs([]string{"parent"})
	bcAfter := mgr.stackConfig.Cache.GetBranchCache("parent")
	if bcAfter == nil || bcAfter.PreSyncCommit != parentV0 {
		t.Errorf("snapshot was overwritten despite child still anchored on the older SHA. PreSyncCommit=%q want %q (parentV0); parent's new HEAD was %q.",
			bcAfterField(bcAfter), parentV0, parentV1)
	}

	// Move child off parentV0: rebase --onto parent parentV0 replays only
	// child's own commits onto parent's new tip. After this, parentV0 is
	// no longer reachable from child, so a subsequent snapshot call MAY
	// overwrite — the guard yields.
	rebaseCmd := exec.Command("git", "-C", childPath, "rebase", "--onto", "parent", parentV0)
	rebaseCmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	if out, err := rebaseCmd.CombinedOutput(); err != nil {
		t.Fatalf("rebase child --onto parent: %v\n%s", err, string(out))
	}

	mgr, _ = NewManager(repoDir)
	if isAnc, err := mgr.git.IsAncestor(parentV0, "child"); err != nil || isAnc {
		t.Fatalf("setup: expected parentV0 to no longer be reachable from child after rebase (isAnc=%v err=%v)", isAnc, err)
	}

	mgr.snapshotPreSyncSHAs([]string{"parent"})
	bcFinal := mgr.stackConfig.Cache.GetBranchCache("parent")
	if bcFinal == nil || bcFinal.PreSyncCommit != parentV1 {
		t.Errorf("snapshot should have updated to parent's current HEAD %q once child no longer anchored on older SHA; got %q", parentV1, bcAfterField(bcFinal))
	}
}

// bcAfterField is a tiny helper so the assertion messages in the test above
// stay readable — printing the field directly off a possibly-nil bc would
// panic on the failure path.
func bcAfterField(bc *config.BranchCache) string {
	if bc == nil {
		return "<nil cache entry>"
	}
	return bc.PreSyncCommit
}

// TestCleanupStalePreSyncSHAs_AgesOutOldSnapshots verifies the age-based
// cleanup heuristic that handles the checkout-based branch case. Without
// it, a snapshot on a branch with no worktree (so the rebase-state
// inspection can't fire) would persist forever. With it, snapshots older
// than preSyncSnapshotMaxAge are cleared regardless of worktree presence.
func TestCleanupStalePreSyncSHAs_AgesOutOldSnapshots(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	bPath := filepath.Join(worktreeBaseDir, "feat-aged")
	mgr.CreateBranch("feat-aged", "main", bPath, "")
	mgr, _ = NewManager(repoDir)
	os.WriteFile(filepath.Join(bPath, "f.txt"), []byte("v1\n"), 0644)
	exec.Command("git", "-C", bPath, "add", ".").Run()
	exec.Command("git", "-C", bPath, "commit", "-m", "v1").Run()

	mgr, _ = NewManager(repoDir)
	headSHA, _ := mgr.git.GetBranchCommit("feat-aged")

	// Plant an aged snapshot AT a different SHA than current HEAD (the
	// branch was rewritten 30 days ago). Heuristic-1 wouldn't fire (HEAD
	// != snapshot); only the age fallback should clear it.
	bc := &config.BranchCache{
		PreSyncCommit:   "abcdef0123456789abcdef0123456789abcdef01",
		PreSyncCommitAt: time.Now().Add(-30 * 24 * time.Hour).Unix(),
		WorktreePath:    bPath,
	}
	// Make absolutely sure the planted SHA differs from the live HEAD,
	// so heuristic-1 (HEAD == snapshot) doesn't accidentally fire.
	if bc.PreSyncCommit == headSHA {
		t.Fatalf("test setup: planted SHA collides with live HEAD %q", headSHA)
	}
	mgr.stackConfig.Cache.SetBranchCache("feat-aged", bc)

	mgr.cleanupStalePreSyncSHAs()

	bcAfter := mgr.stackConfig.Cache.GetBranchCache("feat-aged")
	if bcAfter == nil {
		t.Fatal("cache entry vanished")
	}
	if bcAfter.PreSyncCommit != "" {
		t.Errorf("aged snapshot was not cleared; PreSyncCommit=%q (>14 days old should be aged out)", bcAfter.PreSyncCommit)
	}
	if bcAfter.PreSyncCommitAt != 0 {
		t.Errorf("aged timestamp was not cleared; PreSyncCommitAt=%d (should be 0)", bcAfter.PreSyncCommitAt)
	}
}

// TestCleanupStalePreSyncSHAs_RecentSnapshotPreserved is the happy-path
// counterpart: a fresh snapshot (taken seconds ago) on an unchanged HEAD
// is preserved by the cleanup pass — both heuristics correctly identify
// it as live, not stale.
func TestCleanupStalePreSyncSHAs_RecentSnapshotPreserved(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	bPath := filepath.Join(worktreeBaseDir, "feat-fresh")
	mgr.CreateBranch("feat-fresh", "main", bPath, "")
	mgr, _ = NewManager(repoDir)
	os.WriteFile(filepath.Join(bPath, "f.txt"), []byte("v1\n"), 0644)
	exec.Command("git", "-C", bPath, "add", ".").Run()
	exec.Command("git", "-C", bPath, "commit", "-m", "v1").Run()

	mgr, _ = NewManager(repoDir)
	headSHA, _ := mgr.git.GetBranchCommit("feat-fresh")

	// Plant a fresh snapshot at a SHA that differs from current HEAD —
	// heuristic-1 (HEAD == snapshot) would clear, so we deliberately set
	// a different SHA to isolate the age fallback.
	bc := &config.BranchCache{
		PreSyncCommit:   "fedcba9876543210fedcba9876543210fedcba98",
		PreSyncCommitAt: time.Now().Add(-1 * time.Hour).Unix(),
		WorktreePath:    bPath,
	}
	if bc.PreSyncCommit == headSHA {
		t.Fatalf("test setup: planted SHA collides with live HEAD %q", headSHA)
	}
	mgr.stackConfig.Cache.SetBranchCache("feat-fresh", bc)

	mgr.cleanupStalePreSyncSHAs()

	bcAfter := mgr.stackConfig.Cache.GetBranchCache("feat-fresh")
	if bcAfter == nil || bcAfter.PreSyncCommit == "" {
		t.Errorf("recent snapshot (<1h old) was incorrectly cleared; bc=%+v", bcAfter)
	}
}

// TestCleanupStalePreSyncSHAs_ZeroTimestampClearedAsAncient covers the
// migration case: snapshots written by an older ezstack that didn't have
// the PreSyncCommitAt field have a zero timestamp. They're treated as
// "ancient" so they don't accumulate forever. (Without this, an older
// install upgrading to this version could carry stale snapshots until
// the next time HEAD happens to equal the snapshot.)
func TestCleanupStalePreSyncSHAs_ZeroTimestampClearedAsAncient(t *testing.T) {
	repoDir, worktreeBaseDir, cleanup := setupSyncTestEnv(t)
	defer cleanup()

	mgr, _ := NewManager(repoDir)
	bPath := filepath.Join(worktreeBaseDir, "feat-legacy")
	mgr.CreateBranch("feat-legacy", "main", bPath, "")
	mgr, _ = NewManager(repoDir)
	os.WriteFile(filepath.Join(bPath, "f.txt"), []byte("v1\n"), 0644)
	exec.Command("git", "-C", bPath, "add", ".").Run()
	exec.Command("git", "-C", bPath, "commit", "-m", "v1").Run()

	mgr, _ = NewManager(repoDir)
	headSHA, _ := mgr.git.GetBranchCommit("feat-legacy")

	bc := &config.BranchCache{
		PreSyncCommit:   "0123456789abcdef0123456789abcdef01234567",
		PreSyncCommitAt: 0, // legacy entry: no timestamp
		WorktreePath:    bPath,
	}
	if bc.PreSyncCommit == headSHA {
		t.Fatalf("test setup: planted SHA collides with live HEAD %q", headSHA)
	}
	mgr.stackConfig.Cache.SetBranchCache("feat-legacy", bc)

	mgr.cleanupStalePreSyncSHAs()
	bcAfter := mgr.stackConfig.Cache.GetBranchCache("feat-legacy")
	if bcAfter != nil && bcAfter.PreSyncCommit != "" {
		t.Errorf("legacy snapshot (no timestamp) was not cleared; PreSyncCommit=%q", bcAfter.PreSyncCommit)
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

// TestDebugLog_FiresOnlyWhenEnvSet verifies the debugLog helper's gate:
// EZSTACK_DEBUG unset → no output; set → tag + key=value lands on stderr.
// Catches the typical regression where someone bypasses the gate.
func TestDebugLog_FiresOnlyWhenEnvSet(t *testing.T) {
	// Reset the cached gate so the env var is re-read at each call below.
	debugOnce = sync.Once{}
	defer func() { debugOnce = sync.Once{} }()

	orig := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	os.Unsetenv("EZSTACK_DEBUG")
	debugLog("test-tag", "k", "v") // should produce nothing

	debugOnce = sync.Once{}
	os.Setenv("EZSTACK_DEBUG", "1")
	defer os.Unsetenv("EZSTACK_DEBUG")
	debugLog("test-tag", "k", "v") // should produce one line

	w.Close()
	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	got := string(buf[:n])

	if !strings.Contains(got, "[ezstack:test-tag] k=v") {
		t.Errorf("debug log missing when env set; got %q", got)
	}
	if strings.Count(got, "[ezstack:test-tag]") != 1 {
		t.Errorf("expected exactly one debug line; got %d in %q", strings.Count(got, "[ezstack:test-tag]"), got)
	}
}
