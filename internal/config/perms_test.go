package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// TestSecureConfigPerms pins the security-critical contract that ezstack
// never leaves its config directory or sensitive files (config.json,
// stacks.json, cache.json) world- or group-readable.
//
// Pre-fix the directory was created at 0755 and files at 0644, which on
// shared hosts let any local user read the persisted github_token. This
// test verifies the post-fix modes (0700 / 0600) and the upgrade path that
// chmods pre-existing wide-mode files when first encountered.
//
// Skipped on Windows: chmod / Unix mode bits don't apply there.
func TestSecureConfigPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are not meaningful on Windows")
	}

	t.Run("Save creates dir at 0700 and config.json at 0600", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("EZSTACK_HOME", tmp)
		// sync.Once persists across subtests within the same process; reset
		// it so each subtest exercises the migration path on its own dir.
		configPermsMigrated = sync.Once{}

		cfg := &Config{
			DefaultBaseBranch: "main",
			GitHubToken:       "ghp_secret_test",
			Repos:             map[string]*RepoConfig{},
		}
		if err := cfg.Save(); err != nil {
			t.Fatalf("Save: %v", err)
		}

		assertMode(t, tmp, 0700)
		assertMode(t, filepath.Join(tmp, "config.json"), 0600)
	})

	t.Run("StackConfig.Save creates stacks.json at 0600", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("EZSTACK_HOME", tmp)
		configPermsMigrated = sync.Once{}

		repo := tmp + "/fake-repo"
		sc, err := LoadStackConfig(repo)
		if err != nil {
			t.Fatalf("LoadStackConfig: %v", err)
		}
		if err := sc.Save(repo); err != nil {
			t.Fatalf("Save: %v", err)
		}

		assertMode(t, tmp, 0700)
		assertMode(t, filepath.Join(tmp, "stacks.json"), 0600)
	})

	t.Run("upgrade path tightens wide-mode pre-existing files", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("EZSTACK_HOME", tmp)
		configPermsMigrated = sync.Once{}

		// Simulate a config dir left behind by an older ezstack: dir 0755,
		// files 0644 with a token in config.json.
		if err := os.MkdirAll(tmp, 0755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		_ = os.Chmod(tmp, 0755)
		legacyConfig := []byte(`{"default_base_branch":"main","github_token":"ghp_old"}`)
		if err := os.WriteFile(filepath.Join(tmp, "config.json"), legacyConfig, 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "stacks.json"), []byte(`{"version":2}`), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		// Trigger the migration via any code path that calls
		// ensureSecureConfigDir.
		if err := ensureSecureConfigDir(tmp); err != nil {
			t.Fatalf("ensureSecureConfigDir: %v", err)
		}

		assertMode(t, tmp, 0700)
		assertMode(t, filepath.Join(tmp, "config.json"), 0600)
		assertMode(t, filepath.Join(tmp, "stacks.json"), 0600)
	})
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	got := fi.Mode().Perm()
	if got != want {
		t.Errorf("mode of %s = %#o, want %#o", path, got, want)
	}
}
