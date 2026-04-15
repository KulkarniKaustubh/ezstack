package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
)

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
