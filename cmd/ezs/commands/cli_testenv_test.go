package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// setupCLITestEnv creates a temporary git repo on `main` with a single
// initial commit, points EZSTACK_HOME at an isolated config directory, and
// chdirs into the repo. Returns the resolved repo path and a *stack.Manager
// already wired to it. ui.YesMode is enabled for the test's duration so
// destructive commands don't block on interactive prompts.
//
// Cleanup is registered via t.Cleanup — caller does not need to defer.
//
// The Delete/Reparent/Stack commands operate on os.Getwd() and a config
// rooted at EZSTACK_HOME, so this helper is the minimum environment
// required to exercise their full code paths (including persistence through
// the stack manager and any git operations they perform).
func setupCLITestEnv(t *testing.T) (repoDir string, mgr *stack.Manager) {
	t.Helper()

	tmp := t.TempDir()
	// macOS resolves /tmp/* through a symlink; the manager normalizes paths
	// via EvalSymlinks, so do the same here to keep config keys aligned.
	tmp, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("EvalSymlinks(tmp): %v", err)
	}

	repoDir = filepath.Join(tmp, "repo")
	worktreeBaseDir := filepath.Join(tmp, "worktrees")
	configDir := filepath.Join(tmp, "config")
	for _, d := range []string{repoDir, worktreeBaseDir, configDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	t.Setenv("EZSTACK_HOME", configDir)

	// Init the repo on `main` so all stack operations have a stable root.
	mustGit(t, repoDir, "init", "-b", "main")
	mustGit(t, repoDir, "config", "user.email", "test@ezstack.invalid")
	mustGit(t, repoDir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatalf("seed README: %v", err)
	}
	mustGit(t, repoDir, "add", ".")
	mustGit(t, repoDir, "commit", "-m", "initial")

	// Persist config pointing at this repo so manager.NewManager picks up
	// the worktree base dir and default base branch.
	cfg := &config.Config{
		DefaultBaseBranch: "main",
		Repos: map[string]*config.RepoConfig{
			repoDir: {WorktreeBaseDir: worktreeBaseDir},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}

	prevYesMode := ui.YesMode
	ui.YesMode = true

	prevCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir to repo: %v", err)
	}

	t.Cleanup(func() {
		ui.YesMode = prevYesMode
		// Restore cwd before TempDir cleanup runs, otherwise some shells
		// will refuse to remove a directory the test process is sitting in.
		_ = os.Chdir(prevCwd)
	})

	mgr, err = stack.NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return repoDir, mgr
}

// mustGit runs a git command in dir and fails the test on non-zero exit.
// Used to seed test repos. Output is captured and surfaced on failure so
// debugging doesn't require running the test under -v.
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
}

// reloadManager forces a fresh read from disk. The manager caches config in
// memory; tests that mutate state through one manager and then check via a
// command (which constructs its own manager from cwd) need the in-memory
// view re-loaded to assert post-conditions accurately.
func reloadManager(t *testing.T, repoDir string) *stack.Manager {
	t.Helper()
	mgr, err := stack.NewManager(repoDir)
	if err != nil {
		t.Fatalf("reload NewManager: %v", err)
	}
	return mgr
}
