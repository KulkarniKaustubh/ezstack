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

func (stubBackend) Confirm(string) bool                                 { return true }
func (stubBackend) ConfirmWithDefault(_ string, defaultYes bool) bool   { return defaultYes }
func (stubBackend) Select(_ []string, _ string, defaultIdx int) int    { return defaultIdx }
func (stubBackend) SelectOption(_ []string, _ string) (int, error)     { return 0, nil }
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
