package commands

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
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

func TestHasExamplesFlag_EqualsForm(t *testing.T) {
	// `--examples=foo` should still trigger help. The previous detector
	// only matched the bare token and let `--examples=` fall through to
	// the per-command flag parser as an unknown flag.
	var got bool
	silenceStdout(t, func() {
		got = HasExamplesFlag("commit", []string{"--examples=help"})
	})
	if !got {
		t.Error("--examples=help should trigger the examples handler")
	}
}

func TestHasExamplesFlag_EqualsFormConsumedAsValue(t *testing.T) {
	// `-m "--examples=foo"` is still a commit-message value, not a help
	// trigger. The flag-consumption walker must protect this.
	if HasExamplesFlag("commit", []string{"-m", "--examples=hi"}) {
		t.Error("--examples=hi consumed as -m value must not trigger help")
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

// TestFetchDiffStats_BaseDriftedFromOrigin reproduces the reporter's bug where
// `ezs ls` line diff was "way off" after `ezs sync`. Sync rebases children onto
// origin/<base> but never fast-forwards local <base>. If fetchDiffStats then
// diffs against local <base>, the report includes every upstream commit landed
// since the last local update — not what the branch actually contributes.
//
// The fix is to diff against origin/<base> when the branch's parent is the
// stack's base (an upstream-tracked branch). This test sets up exactly that
// drifted state and checks the reported additions/deletions match the feature
// branch's own delta, not the upstream delta.
func TestFetchDiffStats_BaseDriftedFromOrigin(t *testing.T) {
	// Create a bare origin that local clones push/fetch against.
	originDir := t.TempDir()
	runIn := func(t *testing.T, dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git -C %s %v: %v\n%s", dir, args, err, out)
		}
	}
	runIn(t, originDir, "init", "-q", "--bare", "-b", "main")

	// Seed a working repo that will act as the publisher of upstream commits.
	upstreamDir := t.TempDir()
	runIn(t, upstreamDir, "init", "-q", "-b", "main")
	runIn(t, upstreamDir, "config", "user.email", "up@test.com")
	runIn(t, upstreamDir, "config", "user.name", "Up")
	runIn(t, upstreamDir, "remote", "add", "origin", originDir)
	if err := os.WriteFile(filepath.Join(upstreamDir, "README"), []byte("seed\n"), 0644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runIn(t, upstreamDir, "add", ".")
	runIn(t, upstreamDir, "commit", "-qm", "seed")
	runIn(t, upstreamDir, "push", "-q", "origin", "main")

	// Local clone: fetch the seed, nothing else yet.
	localDir := t.TempDir()
	runIn(t, localDir, "clone", "-q", "-b", "main", originDir, ".")
	runIn(t, localDir, "config", "user.email", "me@test.com")
	runIn(t, localDir, "config", "user.name", "Me")

	// Local: create a feature branch off main with its own delta (2 lines).
	runIn(t, localDir, "checkout", "-qb", "feature")
	featureContent := "feature-line-1\nfeature-line-2\n"
	if err := os.WriteFile(filepath.Join(localDir, "feature.txt"), []byte(featureContent), 0644); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	runIn(t, localDir, "add", ".")
	runIn(t, localDir, "commit", "-qm", "feature work")

	// Upstream: advance main with a much larger commit (10 lines).
	var bigBuf strings.Builder
	for i := 0; i < 10; i++ {
		bigBuf.WriteString("upstream-line\n")
	}
	if err := os.WriteFile(filepath.Join(upstreamDir, "upstream.txt"), []byte(bigBuf.String()), 0644); err != nil {
		t.Fatalf("write upstream: %v", err)
	}
	runIn(t, upstreamDir, "add", ".")
	runIn(t, upstreamDir, "commit", "-qm", "upstream advance")
	runIn(t, upstreamDir, "push", "-q", "origin", "main")

	// Local: fetch — now origin/main is ahead of local main. This is the
	// drifted state. Simulate `ezs sync` by rebasing feature onto origin/main
	// without fast-forwarding local main.
	runIn(t, localDir, "fetch", "-q", "origin")
	runIn(t, localDir, "rebase", "-q", "origin/main")

	// Sanity: local main must still point at the original seed, not origin/main.
	localMain, err := exec.Command("git", "-C", localDir, "rev-parse", "main").Output()
	if err != nil {
		t.Fatalf("rev-parse main: %v", err)
	}
	originMain, err := exec.Command("git", "-C", localDir, "rev-parse", "origin/main").Output()
	if err != nil {
		t.Fatalf("rev-parse origin/main: %v", err)
	}
	if strings.TrimSpace(string(localMain)) == strings.TrimSpace(string(originMain)) {
		t.Fatalf("test precondition failed: local main and origin/main should have diverged")
	}

	// Invoke the function under test.
	g := git.New(localDir)
	s := &config.Stack{
		Hash: "test",
		Root: "main",
		Branches: []*config.Branch{
			{Name: "feature", Parent: "main"},
		},
	}
	stats := fetchDiffStats(g, s)

	got, ok := stats["feature"]
	if !ok || got == nil {
		t.Fatalf("no diff stats for feature branch; got %+v", stats)
	}

	// Feature's own delta is exactly 2 added lines. If the bug returns (diff
	// resolves local main instead of origin/main) we'd see 12 (2 + 10).
	if got.Additions != 2 || got.Deletions != 0 {
		t.Errorf("feature diff = +%d/-%d; want +2/-0 (base drift leaked in)", got.Additions, got.Deletions)
	}
}

// TestShowDiffStatsAgainstBase_BaseDriftedFromOrigin is the same drifted-base
// scenario as TestFetchDiffStats_BaseDriftedFromOrigin (which covers `ezs ls`)
// but applied to the post-create info message printed by `ezs new origin/*`
// and `ezs new -r`. Both should agree on the diff size; previously they
// didn't, because showDiffStatsAgainstBase used resolveLocalRef for the base
// and inherited stale local main, while fetchDiffStats already used
// upstreamRef.
func TestShowDiffStatsAgainstBase_BaseDriftedFromOrigin(t *testing.T) {
	originDir := t.TempDir()
	runIn := func(t *testing.T, dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git -C %s %v: %v\n%s", dir, args, err, out)
		}
	}
	runIn(t, originDir, "init", "-q", "--bare", "-b", "main")

	upstreamDir := t.TempDir()
	runIn(t, upstreamDir, "init", "-q", "-b", "main")
	runIn(t, upstreamDir, "config", "user.email", "up@test.com")
	runIn(t, upstreamDir, "config", "user.name", "Up")
	runIn(t, upstreamDir, "remote", "add", "origin", originDir)
	if err := os.WriteFile(filepath.Join(upstreamDir, "README"), []byte("seed\n"), 0644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runIn(t, upstreamDir, "add", ".")
	runIn(t, upstreamDir, "commit", "-qm", "seed")
	runIn(t, upstreamDir, "push", "-q", "origin", "main")

	localDir := t.TempDir()
	runIn(t, localDir, "clone", "-q", "-b", "main", originDir, ".")
	runIn(t, localDir, "config", "user.email", "me@test.com")
	runIn(t, localDir, "config", "user.name", "Me")

	// Local feature branch with a small delta of its own (3 added lines).
	runIn(t, localDir, "checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(localDir, "feature.txt"), []byte("a\nb\nc\n"), 0644); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	runIn(t, localDir, "add", ".")
	runIn(t, localDir, "commit", "-qm", "feature work")

	// Upstream advances main with a much larger commit. After fetch, local main
	// stays put; origin/main moves ahead. This is the drifted state.
	var bigBuf strings.Builder
	for i := 0; i < 10; i++ {
		bigBuf.WriteString("upstream-line\n")
	}
	if err := os.WriteFile(filepath.Join(upstreamDir, "upstream.txt"), []byte(bigBuf.String()), 0644); err != nil {
		t.Fatalf("write upstream: %v", err)
	}
	runIn(t, upstreamDir, "add", ".")
	runIn(t, upstreamDir, "commit", "-qm", "upstream advance")
	runIn(t, upstreamDir, "push", "-q", "origin", "main")

	runIn(t, localDir, "fetch", "-q", "origin")
	// Rebase feature onto origin/main without fast-forwarding local main
	// (mirrors how `ezs sync` and a fresh `ezs new origin/<branch>` checkout
	// land relative to a stale local base).
	runIn(t, localDir, "rebase", "-q", "origin/main")

	// Capture the stderr line emitted by showDiffStatsAgainstBase. The
	// captureStdAndErr helper lives in doctor_test.go in this package.
	g := git.New(localDir)
	_, stderr := captureStdAndErr(t, func() {
		showDiffStatsAgainstBase(g, "feature", "main")
	})

	if !strings.Contains(stderr, "+3") || !strings.Contains(stderr, "-0") {
		t.Errorf("expected +3/-0 in info message (feature's own delta), got:\n%s", stderr)
	}
	// Sanity: the base-drift bug would show +13 (3 feature + 10 upstream).
	if strings.Contains(stderr, "+13") {
		t.Errorf("base drift leaked in: stderr contains +13, want +3:\n%s", stderr)
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
