package itests

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// stubBackend returns deterministic defaults for every prompt so that
// commands.New runs end-to-end without needing a TTY. Tests that inspect
// specific prompt behavior should craft their own backend instead.
type stubBackend struct{}

func (stubBackend) Confirm(string) bool                               { return true }
func (stubBackend) ConfirmWithDefault(_ string, defaultYes bool) bool { return defaultYes }
func (stubBackend) Select(_ []string, _ string, defaultIdx int) int   { return defaultIdx }
func (stubBackend) SelectOption(_ []string, _ string) (int, error)    { return 0, nil }
func (stubBackend) SelectOptionWithBack(_ []string, _ string) (int, error) {
	return 0, nil
}
func (stubBackend) SelectBranch(_ []*config.Branch, _ string) (*config.Branch, error) {
	return nil, nil
}
func (stubBackend) SelectStack(_ []*config.Stack, _ string) (*config.Stack, error) {
	return nil, nil
}
func (stubBackend) Prompt(_, defaultVal string) string { return defaultVal }
func (stubBackend) PromptRequired(_ string) string     { return "stub-required" }

func useStubBackend(t *testing.T) {
	t.Helper()
	ui.SetBackend(stubBackend{})
	t.Cleanup(func() { ui.SetBackend(&ui.TerminalBackend{}) })
}

// enableFileProtocolEnv allows git subprocesses (including submodule clone)
// to use file:// URLs. Git ≥ 2.38 rejects them by default.
func enableFileProtocolEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
}

// setupSubmoduleSource creates a minimal git repo suitable for use as a
// submodule source. Returns the absolute path; cleanup is registered via t.
func setupSubmoduleSource(t *testing.T, name string) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "ezstack-itest-sub-"+name+"-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	mustRun := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustRun("init", "-b", "main")
	mustRun("config", "user.email", TestUserEmail)
	mustRun("config", "user.name", TestUserName)
	if err := os.WriteFile(filepath.Join(dir, "sub.txt"), []byte("sub "+name+"\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustRun("add", ".")
	mustRun("commit", "-m", "initial")
	return dir
}

// addSubmoduleToRepo adds srcRepo at relPath inside parentDir and commits.
func addSubmoduleToRepo(t *testing.T, parentDir, srcRepo, relPath string) {
	t.Helper()
	mustRun := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = parentDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, parentDir, err, out)
		}
	}
	mustRun("submodule", "add", srcRepo, relPath)
	mustRun("commit", "-m", "add submodule "+relPath)
}

// setRepoInitSubmodules sets the per-repo `init_submodules` config for env.RepoDir.
func setRepoInitSubmodules(t *testing.T, env *TestEnv, value bool) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	repoCfg := cfg.GetRepoConfig(env.RepoDir)
	if repoCfg == nil {
		repoCfg = &config.RepoConfig{}
	}
	repoCfg.InitSubmodules = &value
	cfg.SetRepoConfig(env.RepoDir, repoCfg)
	if err := cfg.Save(); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}
}

// submoduleInitialized reports whether the submodule at relPath inside
// worktreePath has its content checked out. `git submodule add` creates
// the directory even on a fresh worktree, but the working files are only
// populated after `submodule update --init`.
func submoduleInitialized(worktreePath, relPath string) bool {
	_, err := os.Stat(filepath.Join(worktreePath, relPath, "sub.txt"))
	return err == nil
}

// TestNewBranch_MirrorsSubmodules_Default: default behavior mirrors submodules
// initialized in the main worktree into the new worktree.
func TestNewBranch_MirrorsSubmodules_Default(t *testing.T) {
	enableFileProtocolEnv(t)
	env := SetupTestEnv(t)
	defer env.Cleanup()
	useStubBackend(t)

	src := setupSubmoduleSource(t, "a")
	addSubmoduleToRepo(t, env.RepoDir, src, "vendor/a")

	chdirForTest(t, env.RepoDir)
	if err := commands.New([]string{"feature", "-p", "main"}); err != nil {
		t.Fatalf("commands.New: %v", err)
	}

	wt := filepath.Join(env.WorktreeDir, "feature")
	if !submoduleInitialized(wt, "vendor/a") {
		t.Errorf("expected submodule vendor/a to be initialized in new worktree %s", wt)
	}
}

// TestNewBranch_NoInitSubmodulesFlag: --no-init-submodules skips the mirror.
func TestNewBranch_NoInitSubmodulesFlag(t *testing.T) {
	enableFileProtocolEnv(t)
	env := SetupTestEnv(t)
	defer env.Cleanup()
	useStubBackend(t)

	src := setupSubmoduleSource(t, "a")
	addSubmoduleToRepo(t, env.RepoDir, src, "vendor/a")

	chdirForTest(t, env.RepoDir)
	if err := commands.New([]string{"feature", "-p", "main", "--no-init-submodules"}); err != nil {
		t.Fatalf("commands.New: %v", err)
	}

	wt := filepath.Join(env.WorktreeDir, "feature")
	if submoduleInitialized(wt, "vendor/a") {
		t.Errorf("expected submodule vendor/a NOT to be initialized with --no-init-submodules")
	}
}

// TestNewBranch_ConfigDisablesMirror: init_submodules=false config prevents
// the mirror when no flag overrides it.
func TestNewBranch_ConfigDisablesMirror(t *testing.T) {
	enableFileProtocolEnv(t)
	env := SetupTestEnv(t)
	defer env.Cleanup()
	useStubBackend(t)

	src := setupSubmoduleSource(t, "a")
	addSubmoduleToRepo(t, env.RepoDir, src, "vendor/a")

	setRepoInitSubmodules(t, env, false)

	chdirForTest(t, env.RepoDir)
	if err := commands.New([]string{"feature", "-p", "main"}); err != nil {
		t.Fatalf("commands.New: %v", err)
	}

	wt := filepath.Join(env.WorktreeDir, "feature")
	if submoduleInitialized(wt, "vendor/a") {
		t.Errorf("expected submodule NOT to be mirrored when init_submodules=false in config")
	}
}

// TestNewBranch_FlagOverridesConfig: --init-submodules overrides a config
// setting of init_submodules=false.
func TestNewBranch_FlagOverridesConfig(t *testing.T) {
	enableFileProtocolEnv(t)
	env := SetupTestEnv(t)
	defer env.Cleanup()
	useStubBackend(t)

	src := setupSubmoduleSource(t, "a")
	addSubmoduleToRepo(t, env.RepoDir, src, "vendor/a")

	setRepoInitSubmodules(t, env, false)

	chdirForTest(t, env.RepoDir)
	if err := commands.New([]string{"feature", "-p", "main", "--init-submodules"}); err != nil {
		t.Fatalf("commands.New: %v", err)
	}

	wt := filepath.Join(env.WorktreeDir, "feature")
	if !submoduleInitialized(wt, "vendor/a") {
		t.Errorf("expected --init-submodules to override config and mirror submodule")
	}
}

// TestNewBranch_NoSubmodulesInSource_NoOp: a repo with no submodules at
// all must not produce a warning or fail. This is the default state for
// 99% of repos and the mirror code must stay silent in that case.
func TestNewBranch_NoSubmodulesInSource_NoOp(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	useStubBackend(t)

	// Note: deliberately no submodule setup — env.RepoDir has no .gitmodules.

	chdirForTest(t, env.RepoDir)
	if err := commands.New([]string{"feature", "-p", "main"}); err != nil {
		t.Fatalf("commands.New on submodule-free repo: %v", err)
	}

	wt := filepath.Join(env.WorktreeDir, "feature")
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("worktree not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".gitmodules")); !os.IsNotExist(err) {
		t.Errorf("expected no .gitmodules in worktree, got stat err = %v", err)
	}
}

// TestNewBranch_NoWorktreeMode_SkipsMirror: when use_worktrees=false, the
// branch is created without a worktree and the mirror code path must not
// run (there is no destination to mirror into). This guards against a
// regression where submodule init would either error out or attempt to
// run against a non-existent path.
func TestNewBranch_NoWorktreeMode_SkipsMirror(t *testing.T) {
	enableFileProtocolEnv(t)
	env := SetupTestEnv(t)
	defer env.Cleanup()
	useStubBackend(t)

	src := setupSubmoduleSource(t, "a")
	addSubmoduleToRepo(t, env.RepoDir, src, "vendor/a")

	// Disable worktrees for this repo.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	repoCfg := cfg.GetRepoConfig(env.RepoDir)
	if repoCfg == nil {
		repoCfg = &config.RepoConfig{}
	}
	falseVal := false
	repoCfg.UseWorktrees = &falseVal
	cfg.SetRepoConfig(env.RepoDir, repoCfg)
	if err := cfg.Save(); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}

	chdirForTest(t, env.RepoDir)
	if err := commands.New([]string{"feature", "-p", "main"}); err != nil {
		t.Fatalf("commands.New (no-worktree mode): %v", err)
	}

	// No worktree directory should have been created — mirror runs against
	// worktrees only.
	if _, err := os.Stat(filepath.Join(env.WorktreeDir, "feature")); !os.IsNotExist(err) {
		t.Errorf("worktree dir created in no-worktree mode (stat err = %v)", err)
	}
}

// TestNewBranch_FromRemoteRef_MirrorsSubmodules: `ezs new origin/<branch>`
// goes through newFromRemoteRef which has its own mirror call site. Verify
// it actually mirrors so this path doesn't silently miss the feature.
func TestNewBranch_FromRemoteRef_MirrorsSubmodules(t *testing.T) {
	enableFileProtocolEnv(t)
	env := SetupTestEnv(t)
	defer env.Cleanup()
	useStubBackend(t)

	src := setupSubmoduleSource(t, "a")
	addSubmoduleToRepo(t, env.RepoDir, src, "vendor/a")

	// Wire up a bare origin and push main + a remote branch.
	bare := filepath.Join(env.TmpDir, "bare.git")
	if err := exec.Command("git", "init", "--bare", "-b", "main", bare).Run(); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	if err := exec.Command("git", "-C", env.RepoDir, "remote", "add", "origin", bare).Run(); err != nil {
		t.Fatalf("remote add: %v", err)
	}
	if err := exec.Command("git", "-C", env.RepoDir, "push", "-u", "origin", "main").Run(); err != nil {
		t.Fatalf("push main: %v", err)
	}
	// Create a branch on origin that the user will check out via origin/<branch>.
	if err := exec.Command("git", "-C", env.RepoDir, "branch", "remote-feature", "main").Run(); err != nil {
		t.Fatalf("branch remote-feature: %v", err)
	}
	if err := exec.Command("git", "-C", env.RepoDir, "push", "origin", "remote-feature").Run(); err != nil {
		t.Fatalf("push remote-feature: %v", err)
	}
	// Drop the local branch so newFromRemoteRef takes the "create" path
	// rather than the "branch already exists" early-return.
	if err := exec.Command("git", "-C", env.RepoDir, "branch", "-D", "remote-feature").Run(); err != nil {
		t.Fatalf("delete local remote-feature: %v", err)
	}

	chdirForTest(t, env.RepoDir)
	if err := commands.New([]string{"origin/remote-feature"}); err != nil {
		t.Fatalf("commands.New origin/remote-feature: %v", err)
	}

	wt := filepath.Join(env.WorktreeDir, "remote-feature")
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}
	if !submoduleInitialized(wt, "vendor/a") {
		t.Errorf("expected vendor/a to be mirrored into newFromRemoteRef worktree")
	}
}

// TestNewBranch_FromRemoteRef_NoInitFlag: --no-init-submodules must also
// take effect on the newFromRemoteRef path (there are two flag plumbings —
// one for the normal flow, one for origin/<branch>). Without a test, a
// future refactor could drop the flag from one path and not the other.
func TestNewBranch_FromRemoteRef_NoInitFlag(t *testing.T) {
	enableFileProtocolEnv(t)
	env := SetupTestEnv(t)
	defer env.Cleanup()
	useStubBackend(t)

	src := setupSubmoduleSource(t, "a")
	addSubmoduleToRepo(t, env.RepoDir, src, "vendor/a")

	bare := filepath.Join(env.TmpDir, "bare.git")
	if err := exec.Command("git", "init", "--bare", "-b", "main", bare).Run(); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	if err := exec.Command("git", "-C", env.RepoDir, "remote", "add", "origin", bare).Run(); err != nil {
		t.Fatalf("remote add: %v", err)
	}
	if err := exec.Command("git", "-C", env.RepoDir, "push", "-u", "origin", "main").Run(); err != nil {
		t.Fatalf("push main: %v", err)
	}
	if err := exec.Command("git", "-C", env.RepoDir, "branch", "remote-skip-sub", "main").Run(); err != nil {
		t.Fatalf("branch: %v", err)
	}
	if err := exec.Command("git", "-C", env.RepoDir, "push", "origin", "remote-skip-sub").Run(); err != nil {
		t.Fatalf("push branch: %v", err)
	}
	if err := exec.Command("git", "-C", env.RepoDir, "branch", "-D", "remote-skip-sub").Run(); err != nil {
		t.Fatalf("delete local: %v", err)
	}

	chdirForTest(t, env.RepoDir)
	if err := commands.New([]string{"origin/remote-skip-sub", "--no-init-submodules"}); err != nil {
		t.Fatalf("commands.New: %v", err)
	}

	wt := filepath.Join(env.WorktreeDir, "remote-skip-sub")
	if submoduleInitialized(wt, "vendor/a") {
		t.Errorf("expected --no-init-submodules to suppress mirror in origin/<branch> flow")
	}
}

// TestNewBranch_OnlyInitializedSubmodulesMirror: submodules that are
// deinit'd in the main worktree must NOT be initialized in the child,
// even when mirroring is enabled. This is the SONiC use case.
func TestNewBranch_OnlyInitializedSubmodulesMirror(t *testing.T) {
	enableFileProtocolEnv(t)
	env := SetupTestEnv(t)
	defer env.Cleanup()
	useStubBackend(t)

	srcA := setupSubmoduleSource(t, "a")
	srcB := setupSubmoduleSource(t, "b")
	addSubmoduleToRepo(t, env.RepoDir, srcA, "vendor/a")
	addSubmoduleToRepo(t, env.RepoDir, srcB, "vendor/b")

	// Deinit B in the main worktree so only A is "active".
	cmd := exec.Command("git", "submodule", "deinit", "-f", "--", "vendor/b")
	cmd.Dir = env.RepoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("deinit vendor/b: %v\n%s", err, out)
	}

	chdirForTest(t, env.RepoDir)
	if err := commands.New([]string{"feature", "-p", "main"}); err != nil {
		t.Fatalf("commands.New: %v", err)
	}

	wt := filepath.Join(env.WorktreeDir, "feature")
	if !submoduleInitialized(wt, "vendor/a") {
		t.Errorf("expected vendor/a (active in main) to be mirrored")
	}
	if submoduleInitialized(wt, "vendor/b") {
		t.Errorf("expected vendor/b (deinit'd in main) NOT to be mirrored")
	}
}
