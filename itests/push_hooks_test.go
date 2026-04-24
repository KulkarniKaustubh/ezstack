package itests

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
)

// setupPushEnv attaches a bare upstream remote to env.RepoDir so `ezs push`
// has somewhere to send commits, and mirrors main over so push has a shared
// history to extend. Returns the upstream path.
func setupPushEnv(t *testing.T, env *TestEnv) string {
	t.Helper()
	upstream := filepath.Join(env.TmpDir, "push-upstream.git")
	mustRunGit(t, "", "init", "--bare", "-b", "main", upstream)
	mustRunGit(t, env.RepoDir, "remote", "add", "origin", upstream)
	mustRunGit(t, env.RepoDir, "push", "-q", "origin", "main")
	return upstream
}

// TestPushHooks_VerifyFailsWhenPreHookMissing asserts that `ezs push --verify`
// hard-errors when ~/.ezstack/hooks/pre-push is not installed. --verify is the
// contract that promotes pre-push from "run if present" to "required" — if this
// check regresses, --verify silently becomes a no-op for CI / policy workflows.
func TestPushHooks_VerifyFailsWhenPreHookMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX hook semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	setupPushEnv(t, env)

	CreateBranchWithCommit(t, env, "feat-verify", "main")
	wt := filepath.Join(env.WorktreeDir, "feat-verify")

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(wt); err != nil {
		t.Fatal(err)
	}

	err := commands.Push([]string{"--verify"})
	if err == nil {
		t.Fatal("push --verify succeeded without pre-push hook — --verify is broken")
	}
	if !strings.Contains(err.Error(), "pre-push") {
		t.Errorf("error %q should mention pre-push", err.Error())
	}
}

// TestPushHooks_VerifyPassesWhenPreHookExists mirrors the opposite direction.
// Installing a passing pre-push hook must satisfy --verify.
func TestPushHooks_VerifyPassesWhenPreHookExists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX hook semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	setupPushEnv(t, env)

	CreateBranchWithCommit(t, env, "feat-verify-ok", "main")
	wt := filepath.Join(env.WorktreeDir, "feat-verify-ok")

	preSentinel := filepath.Join(env.TmpDir, "pre_push_fired_verify")
	installPassingHook(t, env, "pre-push", preSentinel)

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(wt); err != nil {
		t.Fatal(err)
	}

	if err := commands.Push([]string{"--verify"}); err != nil {
		t.Fatalf("push --verify failed with passing hook: %v", err)
	}
	if _, err := os.Stat(preSentinel); err != nil {
		t.Error("pre-push hook sentinel missing — hook never ran under --verify")
	}
}

// TestPushHooks_PreAndPostFire asserts both pre-push and post-push run during
// a vanilla `ezs push`. Covers the default "run if present" wiring.
func TestPushHooks_PreAndPostFire(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX hook semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	setupPushEnv(t, env)

	CreateBranchWithCommit(t, env, "feat-push-hooks", "main")
	wt := filepath.Join(env.WorktreeDir, "feat-push-hooks")

	preSentinel := filepath.Join(env.TmpDir, "pre_push_fired")
	postSentinel := filepath.Join(env.TmpDir, "post_push_fired")
	installPassingHook(t, env, "pre-push", preSentinel)
	installPassingHook(t, env, "post-push", postSentinel)

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(wt); err != nil {
		t.Fatal(err)
	}

	if err := commands.Push(nil); err != nil {
		t.Fatalf("push failed: %v", err)
	}
	if _, err := os.Stat(preSentinel); err != nil {
		t.Error("pre-push hook did not run — sentinel missing")
	}
	if _, err := os.Stat(postSentinel); err != nil {
		t.Error("post-push hook did not run — sentinel missing")
	}
}

// TestPushHooks_PreFailureAbortsPush asserts pre-push failure prevents the
// actual git push. Without this, pre-push can't be trusted for pre-release
// checks (e.g. test suite, lint, secret scan).
func TestPushHooks_PreFailureAbortsPush(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX hook semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()
	upstream := setupPushEnv(t, env)

	CreateBranchWithCommit(t, env, "feat-push-abort", "main")
	wt := filepath.Join(env.WorktreeDir, "feat-push-abort")

	sentinel := filepath.Join(env.TmpDir, "pre_push_rejected")
	installFailingHook(t, env, "pre-push", sentinel)

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(wt); err != nil {
		t.Fatal(err)
	}

	err := commands.Push(nil)
	if err == nil {
		t.Fatal("push succeeded despite pre-push failure")
	}
	if !strings.Contains(err.Error(), "pre-push") {
		t.Errorf("error %q should name the failing hook", err.Error())
	}
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Error("pre-push sentinel missing — hook never ran")
	}

	// Confirm the upstream didn't receive the branch — its refs/heads dir
	// must not contain feat-push-abort.
	showRef := exec.Command("git", "-C", upstream, "show-ref", "--verify", "refs/heads/feat-push-abort")
	if out, runErr := showRef.CombinedOutput(); runErr == nil {
		t.Errorf("upstream received the branch despite pre-push abort: %s", strings.TrimSpace(string(out)))
	}
}
