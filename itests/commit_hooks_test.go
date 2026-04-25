package itests

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
)

// mustRunGit is defined in remote_pickup_test.go in the same package.

// installPassingHook writes a hook that records its invocation (by touching
// `sentinel`) and exits 0. Useful for asserting that hooks ran.
func installPassingHook(t *testing.T, env *TestEnv, name, sentinel string) {
	t.Helper()
	hooksDir := filepath.Join(env.ConfigDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	body := "#!/bin/sh\ntouch " + sentinel + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(hooksDir, name), []byte(body), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
}

// TestCommitHooks_PreAndPostFire asserts both pre-commit and post-commit
// run during `ezs commit`. These hooks were added as part of the CLI feature
// bundle but had no coverage — a regression that silenced either would have
// gone undetected.
func TestCommitHooks_PreAndPostFire(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX hook semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "feat-hook", "main")
	wt := filepath.Join(env.WorktreeDir, "feat-hook")

	preSentinel := filepath.Join(env.TmpDir, "pre_commit_fired")
	postSentinel := filepath.Join(env.TmpDir, "post_commit_fired")
	installPassingHook(t, env, "pre-commit", preSentinel)
	installPassingHook(t, env, "post-commit", postSentinel)

	// Stage a change so there's actually something to commit.
	if err := os.WriteFile(filepath.Join(wt, "work.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustRunGit(t, wt, "add", "work.txt")

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(wt); err != nil {
		t.Fatal(err)
	}

	if err := commands.Commit([]string{"-m", "hook regression test", "--no-push"}); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	if _, err := os.Stat(preSentinel); err != nil {
		t.Error("pre-commit hook did not run — sentinel missing")
	}
	if _, err := os.Stat(postSentinel); err != nil {
		t.Error("post-commit hook did not run — sentinel missing")
	}
}

// TestCommitHooks_PreCommitFailureAbortsCommit asserts that if pre-commit
// exits non-zero, the commit is NOT created. Without this guarantee, users
// can't rely on pre-commit for anything safety-critical (lint, secrets, etc).
func TestCommitHooks_PreCommitFailureAbortsCommit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX hook semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "feat-abort", "main")
	wt := filepath.Join(env.WorktreeDir, "feat-abort")

	sentinel := filepath.Join(env.TmpDir, "pre_commit_fired_then_rejected")
	installFailingHook(t, env, "pre-commit", sentinel)

	if err := os.WriteFile(filepath.Join(wt, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(wt); err != nil {
		t.Fatal(err)
	}

	headBefore := headCommit(t, wt)
	err := commands.Commit([]string{"-m", "should be blocked", "--no-push"})
	if err == nil {
		t.Fatal("Commit succeeded despite pre-commit failure")
	}
	if !strings.Contains(err.Error(), "pre-commit") {
		t.Errorf("error %q should mention the failing hook", err.Error())
	}
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Error("pre-commit sentinel missing — hook never ran")
	}
	headAfter := headCommit(t, wt)
	if headBefore != headAfter {
		t.Errorf("HEAD moved despite pre-commit failure: %s -> %s", headBefore, headAfter)
	}
}

// TestCommitHooks_PostCommitFailureIsWarning asserts that post-commit
// failures do NOT roll back the commit — they're surfaced as a warning. This
// matches the commit.go wiring and prevents a broken post-commit hook (e.g.
// a notifier that went offline) from blocking users from committing.
func TestCommitHooks_PostCommitFailureIsWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX hook semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "feat-post-warn", "main")
	wt := filepath.Join(env.WorktreeDir, "feat-post-warn")

	sentinel := filepath.Join(env.TmpDir, "post_commit_failed")
	installFailingHook(t, env, "post-commit", sentinel)

	if err := os.WriteFile(filepath.Join(wt, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustRunGit(t, wt, "add", "x.txt")

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(wt); err != nil {
		t.Fatal(err)
	}

	headBefore := headCommit(t, wt)
	if err := commands.Commit([]string{"-m", "post-hook warn test", "--no-push"}); err != nil {
		t.Errorf("Commit should succeed despite post-commit failure; got: %v", err)
	}
	headAfter := headCommit(t, wt)
	if headBefore == headAfter {
		t.Error("HEAD did not advance — commit appears to have been rolled back")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Error("post-commit hook sentinel missing — hook never ran")
	}
}
