package config

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMergeRepoData_AllFieldsCovered is a "tripwire" guard. The
// mergeRepoData function knows how to three-way-merge each field of
// repoData. If a future contributor adds a new field (Settings,
// Metadata, anything) without extending mergeRepoData, the new field's
// peer-process updates would be silently overwritten on every Save.
//
// We use reflection to count the actual struct fields and compare
// against mergedRepoDataFieldCount, which the function explicitly
// declares it merges. The constant must be incremented in lockstep with
// any new field — and the merge logic itself must be extended in
// mergeRepoData. This test ensures the latter happens by failing the
// build until both are done.
func TestMergeRepoData_AllFieldsCovered(t *testing.T) {
	got := reflect.TypeOf(repoData{}).NumField()
	if got != mergedRepoDataFieldCount {
		t.Fatalf(
			"repoData has %d fields but mergeRepoData claims to merge %d. "+
				"A new field was added to repoData without extending "+
				"mergeRepoData — the new field's concurrent peer-process "+
				"updates would be silently lost. "+
				"Update mergeRepoData to merge the new field, then bump "+
				"mergedRepoDataFieldCount in config.go.",
			got, mergedRepoDataFieldCount,
		)
	}
}

// TestMergeGlobalConfig_AllFieldsCovered is the same tripwire for the
// top-level Config struct. The original PR added a tripwire for repoData
// only; if a future contributor adds (say) a Telemetry or Notifications
// field to Config and forgets to extend mergeGlobalConfig, two parallel
// `ezs config set` runs would silently drop one writer's update. Failing
// the build is cheaper than discovering this in production.
func TestMergeGlobalConfig_AllFieldsCovered(t *testing.T) {
	got := reflect.TypeOf(Config{}).NumField()
	if got != mergedGlobalConfigFieldCount {
		t.Fatalf(
			"Config has %d fields but mergeGlobalConfig claims to merge %d. "+
				"A new field was added to Config without extending "+
				"mergeGlobalConfig — concurrent peer-process updates of "+
				"the new field would be silently dropped. "+
				"Update mergeGlobalConfig and bump "+
				"mergedGlobalConfigFieldCount in config.go.",
			got, mergedGlobalConfigFieldCount,
		)
	}
}

// TestConfig_ConcurrentSave_DifferentRepos_NoLostUpdates pins the
// global config equivalent of TestStackConfig_ConcurrentSave_SameRepo:
// two `ezs config set` runs from different terminals should both
// survive when they target *different* repos. Pre-fix, Config.Save did
// a naked Load → mutate → write with no lock or merge — A's read of
// disk happened before B's write, A's write clobbered B's repo entry.
func TestConfig_ConcurrentSave_DifferentRepos_NoLostUpdates(t *testing.T) {
	useEzstackHome(t)

	// Seed an empty config so both goroutines load a real file.
	seed := &Config{Repos: map[string]*RepoConfig{}}
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	const N = 8
	var wg sync.WaitGroup
	wg.Add(N)
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			cfg, err := Load()
			if err != nil {
				errs <- err
				return
			}
			repo := filepath.Join("/test/repo/", string(rune('A'+i)))
			cfg.SetRepoConfig(repo, &RepoConfig{
				WorktreeBaseDir:   "/wt/" + string(rune('A'+i)),
				DefaultBaseBranch: "main",
			})
			if err := cfg.Save(); err != nil {
				errs <- err
				return
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("save error: %v", e)
	}

	final, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < N; i++ {
		repo := filepath.Join("/test/repo/", string(rune('A'+i)))
		rc := final.GetRepoConfig(repo)
		if rc == nil {
			t.Errorf("repo %s lost — Config.Save races dropped a peer's update", repo)
			continue
		}
		want := "/wt/" + string(rune('A'+i))
		if rc.WorktreeBaseDir != want {
			t.Errorf("repo %s: WorktreeBaseDir = %q, want %q", repo, rc.WorktreeBaseDir, want)
		}
	}
}

// TestConfig_Save_PreservesPeerAddedRepos verifies the merge: process A
// loads, process B loads, both add to the Repos map for *different*
// keys, both Save. Final disk state must contain BOTH entries. Pre-fix,
// whichever wrote second would carry only its own Repos map (its
// in-memory copy, captured before B's write landed) and silently delete
// the peer's entry.
func TestConfig_Save_PreservesPeerAddedRepos(t *testing.T) {
	useEzstackHome(t)

	// A and B each load before either has saved.
	a, _ := Load()
	b, _ := Load()

	a.SetRepoConfig("/repo/a", &RepoConfig{WorktreeBaseDir: "/wt/a"})
	b.SetRepoConfig("/repo/b", &RepoConfig{WorktreeBaseDir: "/wt/b"})

	// A saves first, then B. With the merge, B's save reloads A's data
	// under the lock and preserves it. Without the merge, B writes only
	// /repo/b and silently deletes /repo/a.
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	final, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if final.GetRepoConfig("/repo/a") == nil {
		t.Error("/repo/a lost after B's save — peer addition was clobbered")
	}
	if final.GetRepoConfig("/repo/b") == nil {
		t.Error("/repo/b lost after B's save — own write didn't land")
	}
}

// TestConfig_Save_LockDoesNotBlockSingleProcess covers the basic shape:
// repeated saves from the same process succeed back-to-back. A
// regression that forgot to release the lock would deadlock on the
// second save.
func TestConfig_Save_LockDoesNotBlockSingleProcess(t *testing.T) {
	useEzstackHome(t)

	cfg, _ := Load()
	cfg.DefaultBaseBranch = "main"
	for i := 0; i < 5; i++ {
		if err := cfg.Save(); err != nil {
			t.Fatalf("save #%d: %v", i, err)
		}
	}
}

// TestCacheConfig_Save_MergesPeerAddedBranches: the audit's H8 — pre-
// fix, CacheConfig.Save did `rd.Branches = cc.Branches`, wholesale
// replacing the disk's branches map. A peer that added a different
// branch entry between our load and save would have its addition
// silently dropped. The fix uses a three-way merge keyed on the
// origBranches snapshot captured at load time.
func TestCacheConfig_Save_MergesPeerAddedBranches(t *testing.T) {
	useEzstackHome(t)

	const repo = "/test/repo"

	// Seed an entry so LoadCacheConfig has something to snapshot.
	if err := MutateBranchCache(repo, "seed", func(_ *BranchCache) (*BranchCache, error) {
		return &BranchCache{WorktreePath: "/wt/seed"}, nil
	}); err != nil {
		t.Fatal(err)
	}

	cc, err := LoadCacheConfig(repo)
	if err != nil {
		t.Fatal(err)
	}

	// While we hold cc in memory (without yet saving), a peer adds a
	// totally separate branch via the canonical narrow-update path.
	if err := MutateBranchCache(repo, "peer-only", func(_ *BranchCache) (*BranchCache, error) {
		return &BranchCache{WorktreePath: "/wt/peer", PRUrl: "https://example.com/pull/99"}, nil
	}); err != nil {
		t.Fatal(err)
	}

	// We modify our own branch and Save. The peer's "peer-only" entry
	// must survive — that's the production-grade promise.
	cc.SetBranchCache("ours", &BranchCache{WorktreePath: "/wt/ours"})
	if err := cc.Save(repo); err != nil {
		t.Fatal(err)
	}

	final, err := LoadCacheConfig(repo)
	if err != nil {
		t.Fatal(err)
	}
	if final.GetBranchCache("seed") == nil {
		t.Error("seed entry lost — original branches map was clobbered")
	}
	if final.GetBranchCache("ours") == nil {
		t.Error("ours not persisted — own write didn't land")
	}
	if final.GetBranchCache("peer-only") == nil {
		t.Error("peer-only lost — peer addition was clobbered (the H8 regression)")
	}
}

// TestCacheConfig_Save_DeleteSurvivesPeerAdd: when we explicitly drop a
// branch from cc.Branches but a peer added a *different* branch in the
// same window, our deletion still applies (we know we deleted because
// origBranches has it and Branches doesn't) and the peer's add still
// survives.
func TestCacheConfig_Save_DeleteSurvivesPeerAdd(t *testing.T) {
	useEzstackHome(t)

	const repo = "/test/repo"
	if err := MutateBranchCache(repo, "to-delete", func(_ *BranchCache) (*BranchCache, error) {
		return &BranchCache{WorktreePath: "/wt/to-delete"}, nil
	}); err != nil {
		t.Fatal(err)
	}

	cc, _ := LoadCacheConfig(repo)
	delete(cc.Branches, "to-delete")

	if err := MutateBranchCache(repo, "peer-only", func(_ *BranchCache) (*BranchCache, error) {
		return &BranchCache{WorktreePath: "/wt/peer"}, nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := cc.Save(repo); err != nil {
		t.Fatal(err)
	}

	final, _ := LoadCacheConfig(repo)
	if final.GetBranchCache("to-delete") != nil {
		t.Error("to-delete still present — our explicit deletion was lost")
	}
	if final.GetBranchCache("peer-only") == nil {
		t.Error("peer-only lost — peer addition clobbered alongside our delete")
	}
}

// TestLoadStackConfig_MigrationDoesNotRaceWithConcurrentLoads: two
// processes opening an old-version stacks.json simultaneously must not
// produce a torn write. Both will compute the same migrated bytes, but
// without the lock the writes could interleave at the OS layer (atomic
// rename only buys us "atomic at the rename boundary", not "no
// duplicated effort"). Verify that after N concurrent loads, the file
// version is current and the content is parseable.
func TestLoadStackConfig_MigrationDoesNotRaceWithConcurrentLoads(t *testing.T) {
	tmpDir := useEzstackHome(t)

	// Seed a v1 stacks.json (oldest format the migration chain handles).
	v1 := stackConfigFile{Version: 1, Repos: map[string]*repoData{
		"/repo": {Stacks: map[string]*Stack{}, Branches: map[string]*BranchCache{}},
	}}
	data, _ := json.MarshalIndent(v1, "", "  ")
	if err := os.WriteFile(filepath.Join(tmpDir, "stacks.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	const N = 16
	var wg sync.WaitGroup
	wg.Add(N)
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if _, err := LoadStackConfig("/repo"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("concurrent migration load: %v", e)
	}

	// Verify final disk state is valid and at the current version.
	finalData, err := os.ReadFile(filepath.Join(tmpDir, "stacks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f stackConfigFile
	if err := json.Unmarshal(finalData, &f); err != nil {
		t.Fatalf("post-migration file does not parse: %v\n%s", err, finalData)
	}
	if f.Version != currentStackConfigVersion {
		t.Errorf("file version = %d, want %d (migration didn't complete)", f.Version, currentStackConfigVersion)
	}
}

// TestLoadStackConfig_MigrationLockTimeout_DoesNotRaceUnlockedWrite pins the
// fix for a subtle bug: the original migration code fell back to an UNLOCKED
// atomicWriteFile on any acquireFileLock error, including the
// "timed out waiting for peer" case. That defeated the migration lock — two
// processes could time each other out and both write concurrently.
//
// The fix distinguishes ErrLockTimeout (peer holds it; skip our persist;
// migration is idempotent so peer's write will land) from "lock subsystem
// broken" (open errors, etc.; fall back to unlocked write because the lock
// is unusable anyway). This test simulates the timeout case by holding the
// lock from a parallel goroutine for longer than LockTimeout, then verifies:
//
//  1. LoadStackConfig still returns success (no error propagated to caller)
//  2. The disk file is NOT touched by us during the window where the peer
//     holds it (no torn write). The peer's eventual release+write produces
//     the only on-disk persistence.
//
// We use a short LockTimeout (50ms) and LockPollInterval (5ms) so the test
// completes in well under a second.
func TestLoadStackConfig_MigrationLockTimeout_DoesNotRaceUnlockedWrite(t *testing.T) {
	tmpDir := useEzstackHome(t)

	// Seed a v1 stacks.json that needs migration.
	v1 := stackConfigFile{Version: 1, Repos: map[string]*repoData{
		"/repo": {Stacks: map[string]*Stack{}, Branches: map[string]*BranchCache{}},
	}}
	data, _ := json.MarshalIndent(v1, "", "  ")
	stackPath := filepath.Join(tmpDir, "stacks.json")
	if err := os.WriteFile(stackPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	// Capture original lock-tunable values and restore on exit.
	origTimeout, origPoll := LockTimeout, LockPollInterval
	defer func() { LockTimeout, LockPollInterval = origTimeout, origPoll }()
	LockTimeout = 100 * time.Millisecond
	LockPollInterval = 5 * time.Millisecond

	// Hold the lock from a peer goroutine. Use the actual acquire helper so
	// tryAcquireFileLockOnce sees the lock as held by another fd.
	peerLock, err := acquireFileLock(stackPath + ".lock")
	if err != nil {
		t.Fatalf("peer acquire: %v", err)
	}

	// Read disk content NOW so we can compare after the migration attempt.
	preData, _ := os.ReadFile(stackPath)

	// Capture stderr to verify the user-facing notice fires.
	r, w, _ := os.Pipe()
	origStderr := os.Stderr
	os.Stderr = w

	done := make(chan error, 1)
	go func() {
		_, err := LoadStackConfig("/repo")
		done <- err
	}()

	loadErr := <-done
	os.Stderr = origStderr
	w.Close()
	stderrBytes, _ := io.ReadAll(r)
	stderrStr := string(stderrBytes)

	// Release the peer lock so test cleanup is clean.
	peerLock.release()

	if loadErr != nil {
		t.Fatalf("LoadStackConfig returned error during peer-held migration: %v", loadErr)
	}

	// Disk file MUST NOT have been written to during our timeout window —
	// peer still holds the lock and migration hasn't completed yet.
	postData, _ := os.ReadFile(stackPath)
	if !bytes.Equal(preData, postData) {
		t.Errorf("disk file was modified during peer-held migration window — unlocked-fallback regression. before=%d bytes, after=%d bytes", len(preData), len(postData))
	}

	if !strings.Contains(stderrStr, "peer process is migrating") {
		t.Errorf("expected user-facing 'peer process is migrating' notice on stderr; got: %q", stderrStr)
	}
}
