package itests

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
)

// captureStdout replaces os.Stdout with a pipe for the duration of fn and
// returns everything written to it. Used to verify EmitCd emitted a cd line
// when EZS_SHELL_WRAPPER is set.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	w.Close()
	os.Stdout = orig
	return <-done
}

// TestGotoSearch_SingleMatchJumps covers the happy path: a unique substring
// match navigates straight to the branch without prompting.
func TestGotoSearch_SingleMatchJumps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "authentication", "main")
	CreateBranchWithCommit(t, env, "billing", "main")

	// Shell-wrapper mode triggers the `cd <path>` output on stdout that the
	// shell function evals. Without this, EmitCd prints an advisory to
	// stderr instead.
	t.Setenv("EZS_SHELL_WRAPPER", "1")

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(env.RepoDir); err != nil {
		t.Fatal(err)
	}

	var err error
	stdout := captureStdout(t, func() {
		err = commands.Goto([]string{"--search", "auth"})
	})
	if err != nil {
		t.Fatalf("goto --search auth: %v", err)
	}
	// The emitted cd should point at the authentication worktree.
	wantPath := filepath.Join(env.WorktreeDir, "authentication")
	if !strings.Contains(stdout, "cd ") {
		t.Errorf("expected cd line; got:\n%s", stdout)
	}
	if !strings.Contains(stdout, wantPath) {
		t.Errorf("expected cd target %q in output; got:\n%s", wantPath, stdout)
	}
}

// TestGotoSearch_NoMatchReturnsError locks down the empty-result behavior —
// no interactive picker, just a clean error with the ExitBranchNotFound code.
func TestGotoSearch_NoMatchReturnsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feat-one", "main")

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(env.RepoDir); err != nil {
		t.Fatal(err)
	}

	err := commands.Goto([]string{"--search", "nonexistent-xyz"})
	if err == nil {
		t.Fatal("goto --search for non-matching query returned nil")
	}
	if !strings.Contains(err.Error(), "no branch matched") {
		t.Errorf("error %q should mention 'no branch matched'", err.Error())
	}
}

// TestGotoSearch_CaseInsensitiveMatch locks down the case-folding guarantee:
// "AUTH" should still find "authentication". This is the documented behavior
// ("Fuzzy-match a branch name by substring") — users expect to type in any
// case and have it work.
func TestGotoSearch_CaseInsensitiveMatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "authentication", "main")

	t.Setenv("EZS_SHELL_WRAPPER", "1")

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(env.RepoDir); err != nil {
		t.Fatal(err)
	}

	var err error
	stdout := captureStdout(t, func() {
		err = commands.Goto([]string{"--search", "AUTH"})
	})
	if err != nil {
		t.Fatalf("goto --search AUTH: %v", err)
	}
	if !strings.Contains(stdout, "authentication") {
		t.Errorf("expected cd toward 'authentication'; got:\n%s", stdout)
	}
}
