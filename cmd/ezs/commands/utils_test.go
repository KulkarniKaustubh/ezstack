package commands

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
)

// silenceStdout swallows stdout for the duration of fn. HasExamplesFlag calls
// PrintExamples which writes to stdout; tests that exercise the hit path don't
// want that noise cluttering `go test` output.
func silenceStdout(t *testing.T, fn func()) {
	t.Helper()
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, r); close(done) }()
	defer func() {
		w.Close()
		<-done
		os.Stdout = orig
	}()
	fn()
}

func TestHasExamplesFlag_DirectMatch(t *testing.T) {
	var got bool
	silenceStdout(t, func() {
		got = HasExamplesFlag("commit", []string{"--examples"})
	})
	if !got {
		t.Error("expected --examples to be detected")
	}
}

func TestHasExamplesFlag_ConsumedAsMessageValue(t *testing.T) {
	// `ezs commit -m "--examples"` must commit, not print help.
	// Regression guard for the naive "contains --examples" check.
	if HasExamplesFlag("commit", []string{"-m", "--examples"}) {
		t.Error("--examples consumed as -m value must not trigger help")
	}
	if HasExamplesFlag("commit", []string{"--message", "--examples"}) {
		t.Error("--examples consumed as --message value must not trigger help")
	}
}

func TestHasExamplesFlag_AfterEqualsForm(t *testing.T) {
	// --message=foo does NOT consume the next arg, so a following --examples is real.
	var got bool
	silenceStdout(t, func() {
		got = HasExamplesFlag("commit", []string{"--message=foo", "--examples"})
	})
	if !got {
		t.Error("--examples after --message=foo should be detected")
	}
}

func TestHasExamplesFlag_MixedFlags(t *testing.T) {
	// --preset consumes its value; --examples afterward is real.
	var got bool
	silenceStdout(t, func() {
		got = HasExamplesFlag("agent", []string{"--preset", "thorough", "--examples"})
	})
	if !got {
		t.Error("--examples after --preset value should be detected")
	}
}

// setupBranchModeRepo creates a git repo with several branches sharing the main
// worktree (pure branch mode — no secondary worktrees).
func setupBranchModeRepo(t *testing.T, branches ...string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0644)
	run("add", ".")
	run("commit", "-qm", "init")
	for _, b := range branches {
		run("branch", b)
	}
	return dir
}

func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "branch", "--show-current").CombinedOutput()
	if err != nil {
		t.Fatalf("branch --show-current: %v\n%s", err, out)
	}
	return string(out[:len(out)-1])
}

// TestNavigateToBranch_MainWorktreePath reproduces the reporter's bug in issue #9:
// in pure branch mode, the stack root ends up with a WorktreePath pointing at the
// main worktree. Before the fix, NavigateToBranch took the "cd only" fast path
// and silently left the user on the previous branch because cd'ing into the main
// worktree does not switch branches (only secondary worktrees are branch-pinned).
func TestNavigateToBranch_MainWorktreePath(t *testing.T) {
	dir := setupBranchModeRepo(t, "root_branch", "child_branch")

	// Check out the child, then ask NavigateToBranch to go to root_branch using
	// a WorktreePath that points at the main worktree — same shape as the reporter.
	if out, err := exec.Command("git", "-C", dir, "checkout", "-q", "child_branch").CombinedOutput(); err != nil {
		t.Fatalf("checkout child: %v\n%s", err, out)
	}
	if got := currentBranch(t, dir); got != "child_branch" {
		t.Fatalf("precondition failed: on %q, want child_branch", got)
	}

	g := git.New(dir)
	if err := NavigateToBranch(g, "root_branch", dir); err != nil {
		t.Fatalf("NavigateToBranch: %v", err)
	}

	if got := currentBranch(t, dir); got != "root_branch" {
		t.Errorf("after NavigateToBranch: on %q, want root_branch (branch never actually switched)", got)
	}
}

// TestNavigateToBranch_PureBranchMode verifies that the checkout fallback (empty
// WorktreePath) still works for stacks that have no worktree paths at all.
func TestNavigateToBranch_PureBranchMode(t *testing.T) {
	dir := setupBranchModeRepo(t, "feature_a", "feature_b")

	if out, err := exec.Command("git", "-C", dir, "checkout", "-q", "feature_b").CombinedOutput(); err != nil {
		t.Fatalf("checkout: %v\n%s", err, out)
	}

	g := git.New(dir)
	if err := NavigateToBranch(g, "feature_a", ""); err != nil {
		t.Fatalf("NavigateToBranch: %v", err)
	}
	if got := currentBranch(t, dir); got != "feature_a" {
		t.Errorf("after NavigateToBranch: on %q, want feature_a", got)
	}
}

// TestNavigateToBranch_AlreadyOnTarget ensures the no-op path is cheap and
// doesn't error when the worktree is already on the requested branch
// (the common case for secondary worktrees, which are pinned).
func TestNavigateToBranch_AlreadyOnTarget(t *testing.T) {
	dir := setupBranchModeRepo(t, "already_here")

	if out, err := exec.Command("git", "-C", dir, "checkout", "-q", "already_here").CombinedOutput(); err != nil {
		t.Fatalf("checkout: %v\n%s", err, out)
	}

	g := git.New(dir)
	if err := NavigateToBranch(g, "already_here", dir); err != nil {
		t.Fatalf("NavigateToBranch: %v", err)
	}
	if got := currentBranch(t, dir); got != "already_here" {
		t.Errorf("after NavigateToBranch: on %q, want already_here", got)
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"/path/to/dir", "'/path/to/dir'"},
		{"/path/with spaces/dir", "'/path/with spaces/dir'"},
		{"/path/with'quote", "'/path/with'\\''quote'"},
		{"", "''"},
		{"$(rm -rf /)", "'$(rm -rf /)'"},
		{"`whoami`", "'`whoami`'"},
		{"a;b", "'a;b'"},
		{"a\nb", "'a\nb'"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ShellQuote(tt.input)
			if got != tt.want {
				t.Errorf("ShellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
