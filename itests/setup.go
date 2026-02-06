package itests

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
)

// TestEnv holds the test environment configuration
type TestEnv struct {
	TmpDir       string
	RepoDir      string
	WorktreeDir  string
	ConfigDir    string
	StubBinDir   string
	OriginalHome string
	OriginalPath string
}

// SetupTestEnv creates a complete test environment with git repo, config, and stub gh
func SetupTestEnv(t *testing.T) *TestEnv {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "ezstack-itest-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Resolve symlinks (macOS /tmp -> /private/tmp)
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	env := &TestEnv{
		TmpDir:       tmpDir,
		RepoDir:      filepath.Join(tmpDir, "repo"),
		WorktreeDir:  filepath.Join(tmpDir, "worktrees"),
		ConfigDir:    filepath.Join(tmpDir, "config"),
		StubBinDir:   filepath.Join(tmpDir, "bin"),
		OriginalHome: os.Getenv("EZSTACK_HOME"),
		OriginalPath: os.Getenv("PATH"),
	}

	// Create directories
	os.MkdirAll(env.RepoDir, 0755)
	os.MkdirAll(env.WorktreeDir, 0755)
	os.MkdirAll(env.ConfigDir, 0755)
	os.MkdirAll(env.StubBinDir, 0755)

	// Set EZSTACK_HOME
	os.Setenv("EZSTACK_HOME", env.ConfigDir)

	// Initialize git repo
	initGitRepo(t, env.RepoDir)

	// Resolve repoDir again after git init
	env.RepoDir, _ = filepath.EvalSymlinks(env.RepoDir)

	// Setup ezstack config
	cfg := &config.Config{
		DefaultBaseBranch: "main",
		Repos: map[string]*config.RepoConfig{
			env.RepoDir: {
				WorktreeBaseDir: env.WorktreeDir,
			},
		},
	}
	cfg.Save()

	// Setup stub gh
	setupStubGh(t, env)

	return env
}

// Cleanup cleans up the test environment
func (env *TestEnv) Cleanup() {
	os.Setenv("EZSTACK_HOME", env.OriginalHome)
	os.Setenv("PATH", env.OriginalPath)
	os.RemoveAll(env.TmpDir)
}

// initGitRepo initializes a git repository with an initial commit
func initGitRepo(t *testing.T, repoDir string) {
	t.Helper()

	commands := [][]string{
		{"git", "-C", repoDir, "init"},
		{"git", "-C", repoDir, "config", "user.email", TestUserEmail},
		{"git", "-C", repoDir, "config", "user.name", TestUserName},
	}

	for _, cmd := range commands {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			t.Fatalf("Failed to run %v: %v", cmd, err)
		}
	}

	// Create initial commit
	readmePath := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Test Repository\n"), 0644); err != nil {
		t.Fatalf("Failed to create README: %v", err)
	}

	exec.Command("git", "-C", repoDir, "add", ".").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "Initial commit").Run()
}

// setupStubGh creates a symlink to the stub gh script
func setupStubGh(t *testing.T, env *TestEnv) {
	t.Helper()

	// Find stub_gh.sh
	stubScript := findStubGhScript(t)

	// Create 'gh' symlink
	ghPath := filepath.Join(env.StubBinDir, "gh")
	if err := os.Symlink(stubScript, ghPath); err != nil {
		t.Fatalf("Failed to create gh symlink: %v", err)
	}

	// Prepend stub bin dir to PATH
	os.Setenv("PATH", env.StubBinDir+":"+env.OriginalPath)

	// Set stub state dir
	os.Setenv("STUB_GH_STATE_DIR", filepath.Join(env.TmpDir, "gh_state"))
	os.MkdirAll(filepath.Join(env.TmpDir, "gh_state"), 0755)
}

// findStubGhScript locates the stub_gh.sh script
func findStubGhScript(t *testing.T) string {
	t.Helper()

	// Try various paths
	paths := []string{
		"test/fixtures/stub_gh.sh",
		"../test/fixtures/stub_gh.sh",
		"../../test/fixtures/stub_gh.sh",
	}

	for _, p := range paths {
		absPath, _ := filepath.Abs(p)
		if _, err := os.Stat(absPath); err == nil {
			return absPath
		}
	}

	t.Fatal("stub_gh.sh not found")
	return ""
}
