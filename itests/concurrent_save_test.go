package itests

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

// TestStackConfig_MultiProcessConcurrentSave is the cross-process flavor
// of the existing in-process concurrency tests. Spawning real subprocesses
// catches regressions that goroutines can't see — most importantly, an
// flock implementation that's actually no-op on the host OS, or a Save
// path that reads stacks.json *before* taking the lock.
//
// The test re-execs the test binary itself with EZS_CONCURRENT_SAVE_HELPER=1
// (see TestMain). Each child writes 30 distinct stacks under its own
// prefix; with workers=4 we expect 120 total stacks in stacks.json
// afterwards. Anything less means an update was lost across processes.
//
// Tuned timeouts: 30s per child is generous on cold-cache CI, but the
// test as a whole must finish well inside Go's default 10-minute test
// budget so there's no risk of slipping into a flaky timeout pattern.
func TestStackConfig_MultiProcessConcurrentSave(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	tmpHome := t.TempDir()
	const repo = "/test/cross-process"
	const workers = 4
	const iters = 30

	prefixes := []string{"alpha", "beta", "gamma", "delta"}
	if len(prefixes) != workers {
		t.Fatalf("test setup bug: workers=%d but prefixes=%d", workers, len(prefixes))
	}

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	start := time.Now()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(prefix string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, exe, "-test.run=^$") // run no tests in child
			cmd.Env = append(os.Environ(),
				"EZS_CONCURRENT_SAVE_HELPER=1",
				"EZSTACK_HOME="+tmpHome,
				"EZS_HELPER_REPO="+repo,
				"EZS_HELPER_PREFIX="+prefix,
				fmt.Sprintf("EZS_HELPER_ITERS=%d", iters),
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				errs <- fmt.Errorf("worker %s: %w\noutput:\n%s", prefix, err, out)
			}
		}(prefixes[w])
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("%v", e)
	}
	if t.Failed() {
		return
	}

	t.Logf("multi-process save took %v", time.Since(start))

	// Reload final state and assert.
	t.Setenv("EZSTACK_HOME", tmpHome)
	final, err := config.LoadStackConfig(repo)
	if err != nil {
		t.Fatalf("final load: %v", err)
	}
	want := workers * iters
	if got := len(final.Stacks); got != want {
		t.Errorf("after multi-process save: %d stacks in repo, want %d (lost %d)", got, want, want-got)
	}

	// Every prefix must contribute exactly `iters` stacks. A regression
	// where one writer's prefix went missing entirely would only show up
	// in the per-prefix breakdown — total-count parity is necessary but
	// not sufficient.
	perPrefix := map[string]int{}
	for _, s := range final.Stacks {
		for name := range s.Tree {
			for _, p := range prefixes {
				if len(name) >= len(p) && name[:len(p)] == p {
					perPrefix[p]++
				}
			}
		}
	}
	for _, p := range prefixes {
		if perPrefix[p] != iters {
			t.Errorf("prefix %q contributed %d stacks, want %d", p, perPrefix[p], iters)
		}
	}
}

// TestAcquireFileLock_StaleLockFileRecoversAfterHolderExit is a
// process-level companion to the in-process TestAcquireFileLock_*
// suite: when a child process holding the lock exits, the OS releases
// flock automatically and a follow-up acquire from the parent (or a
// fresh child) must succeed without manual intervention. This catches
// regressions where the lock file's lifecycle is tied to the lock
// itself (e.g., we deleted the lock file on release and a stuck child
// blocks every future invocation).
func TestAcquireFileLock_StaleLockFileRecoversAfterHolderExit(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	tmpHome := t.TempDir()
	const repo = "/test/stale-lock"

	// Run the helper once with iters=1 and let it exit. That establishes
	// the lock file on disk. After it exits, the OS has released flock,
	// so an in-process acquire here must succeed promptly.
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = append(os.Environ(),
		"EZS_CONCURRENT_SAVE_HELPER=1",
		"EZSTACK_HOME="+tmpHome,
		"EZS_HELPER_REPO="+repo,
		"EZS_HELPER_PREFIX=stale",
		"EZS_HELPER_ITERS=1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed run failed: %v\n%s", err, out)
	}

	// The stacks.json.lock file should exist now.
	lockPath := filepath.Join(tmpHome, "stacks.json.lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file should exist after child exit: %v", err)
	}

	// Now exercise the parent path: load + save in this process. If lock
	// recovery is broken, this would either hang or time out.
	t.Setenv("EZSTACK_HOME", tmpHome)

	done := make(chan error, 1)
	go func() {
		sc, err := config.LoadStackConfig(repo)
		if err != nil {
			done <- err
			return
		}
		hash := config.GenerateStackHash("post-helper")
		sc.Stacks[hash] = &config.Stack{
			Hash: hash,
			Root: "main",
			Tree: config.BranchTree{"post-helper": config.BranchTree{}},
		}
		done <- sc.Save(repo)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("post-helper save: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("post-helper save hung — lock file was not released after child exit")
	}
}
