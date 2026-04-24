package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
)

// setupResolveRemoteRepo creates an isolated ezstack environment with a git repo
// whose origin URL is intentionally NOT a GitHub URL. That way newGitHubClient()
// fails deterministically in ResolveBranchRemote and the tests can exercise the
// "GitHub unreachable" fallback without depending on network or auth state.
func setupResolveRemoteRepo(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoDir := filepath.Join(tmpDir, "repo")
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}

	t.Setenv("EZSTACK_HOME", configDir)

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	run("add", ".")
	run("commit", "-qm", "init")

	// Origin URL without "github.com" guarantees github.NewClient() returns an
	// error — the exact condition we want to verify the fallback for.
	fakeOrigin := filepath.Join(tmpDir, "not-github-upstream")
	run("remote", "add", "origin", fakeOrigin)

	repoDir, _ = filepath.EvalSymlinks(repoDir)

	cfg := &config.Config{
		DefaultBaseBranch: "main",
		Repos: map[string]*config.RepoConfig{
			repoDir: {WorktreeBaseDir: tmpDir},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("cfg save: %v", err)
	}
	return repoDir
}

func TestResolveBranchRemote_NilManagerReturnsOrigin(t *testing.T) {
	if got := ResolveBranchRemote(nil, nil, "anything"); got != "origin" {
		t.Errorf("ResolveBranchRemote(nil, nil, ...) = %q, want origin", got)
	}
}

func TestResolveBranchRemote_UnknownBranchReturnsOrigin(t *testing.T) {
	repoDir := setupResolveRemoteRepo(t)
	mgr, err := stack.NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if got := ResolveBranchRemote(git.New(repoDir), mgr, "does-not-exist"); got != "origin" {
		t.Errorf("ResolveBranchRemote(unknown) = %q, want origin", got)
	}
}

func TestResolveBranchRemote_NotFlaggedRemoteReturnsOrigin(t *testing.T) {
	repoDir := setupResolveRemoteRepo(t)
	mgr, err := stack.NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	runIn := func(args ...string) {
		_ = exec.Command("git", append([]string{"-C", repoDir}, args...)...).Run()
	}
	runIn("checkout", "-qb", "plain-branch")
	runIn("checkout", "-q", "main")
	if _, err := mgr.RegisterExistingBranch("plain-branch", "", "main"); err != nil {
		t.Fatalf("RegisterExistingBranch: %v", err)
	}

	if got := ResolveBranchRemote(git.New(repoDir), mgr, "plain-branch"); got != "origin" {
		t.Errorf("ResolveBranchRemote(plain) = %q, want origin", got)
	}
}

func TestResolveBranchRemote_StoredRemoteShortCircuits(t *testing.T) {
	repoDir := setupResolveRemoteRepo(t)
	mgr, err := stack.NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	runIn := func(args ...string) {
		_ = exec.Command("git", append([]string{"-C", repoDir}, args...)...).Run()
	}
	runIn("checkout", "-qb", "fork-branch")
	runIn("checkout", "-q", "main")
	if _, err := mgr.RegisterExistingBranch("fork-branch", "", "main"); err != nil {
		t.Fatalf("RegisterExistingBranch: %v", err)
	}
	// Pre-persist a specific fork remote. Future calls must return it without
	// running fork detection (so they don't overwrite or fall through).
	if err := mgr.MarkBranchRemote("fork-branch", "", "fork-owner"); err != nil {
		t.Fatalf("MarkBranchRemote: %v", err)
	}

	if got := ResolveBranchRemote(git.New(repoDir), mgr, "fork-branch"); got != "fork-owner" {
		t.Errorf("ResolveBranchRemote(fork) = %q, want fork-owner", got)
	}
}

// TestResolveBranchRemote_GitHubUnreachableDoesNotLatchNoPush is the direct
// regression for the bug where ResolveBranchRemote latched Remote=_nopush
// into stacks.json whenever IsRemote=true but the GitHub client could not be
// reached. That state poisoned future pushes — every subsequent `ezs push`
// short-circuited with "fork does not allow maintainer push", even for
// ordinary user-owned branches that had simply been picked up via
// `ezs new origin/<branch>` before a PR existed.
func TestResolveBranchRemote_GitHubUnreachableDoesNotLatchNoPush(t *testing.T) {
	repoDir := setupResolveRemoteRepo(t)
	mgr, err := stack.NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	runIn := func(args ...string) {
		_ = exec.Command("git", append([]string{"-C", repoDir}, args...)...).Run()
	}
	runIn("checkout", "-qb", "orphan-branch")
	runIn("checkout", "-q", "main")
	if _, err := mgr.RegisterExistingBranch("orphan-branch", "", "main"); err != nil {
		t.Fatalf("RegisterExistingBranch: %v", err)
	}
	// Replicate the pre-fix stale state: IsRemote=true, Remote="". In v4.6.1 this
	// was produced automatically by `ezs new origin/<branch>` for same-repo branches.
	if err := mgr.MarkBranchRemote("orphan-branch", "", ""); err != nil {
		t.Fatalf("MarkBranchRemote: %v", err)
	}

	got := ResolveBranchRemote(git.New(repoDir), mgr, "orphan-branch")
	if got != "origin" {
		t.Errorf("return value: got %q, want origin (fallback must not latch _nopush)", got)
	}

	// Reload from disk to verify no persistence side-effects.
	mgr2, err := stack.NewReadOnlyManager(repoDir)
	if err != nil {
		t.Fatalf("reload NewReadOnlyManager: %v", err)
	}
	b := mgr2.GetBranch("orphan-branch")
	if b == nil {
		t.Fatal("orphan-branch missing after reload")
	}
	if b.Remote == config.RemoteNoPush {
		t.Errorf("branch was latched to %q — the bug regressed", config.RemoteNoPush)
	}

	// Belt-and-suspenders: the sentinel must not appear anywhere in the persisted stacks.json.
	stacksPath := filepath.Join(os.Getenv("EZSTACK_HOME"), "stacks.json")
	data, err := os.ReadFile(stacksPath)
	if err != nil {
		t.Fatalf("read stacks.json: %v", err)
	}
	if strings.Contains(string(data), config.RemoteNoPush) {
		t.Errorf("stacks.json contains %q after ResolveBranchRemote fallback:\n%s", config.RemoteNoPush, data)
	}
}

// TestResolveBranchRemote_NoPRYetDoesNotLatchNoPush covers the same regression
// via a different trigger: IsRemote=true, GitHub client initializes fine, but
// GetPRByBranch returns no PR. Previously ResolveBranchRemote latched _nopush
// here too. We can't reach the real GetPRByBranch in a hermetic test, but since
// the fallback goes through the same early-return path as the unreachable-GitHub
// case when newGitHubClient fails, the unreachable test above already covers the
// critical assertion for the persisted state. This test locks down the explicit
// invariant that IsRemote=true + Remote="" is not, by itself, a push-blocker:
// something downstream must have first observed a fork.
func TestResolveBranchRemote_IsRemoteOnlyIsNotPushBlocker(t *testing.T) {
	repoDir := setupResolveRemoteRepo(t)
	mgr, err := stack.NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	runIn := func(args ...string) {
		_ = exec.Command("git", append([]string{"-C", repoDir}, args...)...).Run()
	}
	runIn("checkout", "-qb", "speculative-remote")
	runIn("checkout", "-q", "main")
	if _, err := mgr.RegisterExistingBranch("speculative-remote", "", "main"); err != nil {
		t.Fatalf("RegisterExistingBranch: %v", err)
	}
	if err := mgr.MarkBranchRemote("speculative-remote", "", ""); err != nil {
		t.Fatalf("MarkBranchRemote: %v", err)
	}

	b := mgr.GetBranch("speculative-remote")
	if b == nil {
		t.Fatal("speculative-remote not registered")
	}
	if !b.IsRemote {
		t.Fatal("precondition: IsRemote should be true")
	}
	if b.CanPush() != true {
		t.Errorf("CanPush() = false for IsRemote-only branch; this branch should still be pushable until a fork is proven")
	}
}
