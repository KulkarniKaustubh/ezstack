package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadStackConfig_RefusesNewerSchema verifies that an older ezstack
// binary refuses to load a stacks.json written by a newer one. The previous
// behavior fell through to JSON unmarshal, which silently dropped fields the
// older schema didn't recognize — and the next Save persisted the truncated
// form, losing data the newer binary intentionally stored.
func TestLoadStackConfig_RefusesNewerSchema(t *testing.T) {
	home := useEzstackHome(t)

	// Hand-write a stacks.json that claims a schema version higher than this
	// binary supports. The repos block is intentionally empty — the guard
	// must trigger on the version field alone, before any structural parse.
	future := map[string]any{
		"version": currentStackConfigVersion + 1,
		"repos":   map[string]any{},
	}
	data, err := json.MarshalIndent(future, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "stacks.json"), data, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err = LoadStackConfig("/test/repo")
	if err == nil {
		t.Fatal("LoadStackConfig() returned nil error for newer-schema stacks.json; want refusal")
	}
	// The error must mention the upgrade path so the user has an actionable
	// remediation. We don't pin the exact wording, just the operative tokens.
	msg := err.Error()
	for _, want := range []string{"newer", "upgrade"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q missing %q hint", msg, want)
		}
	}
}

// TestLoadStackConfig_AcceptsCurrentSchema confirms the guard doesn't
// regress the happy path: a stacks.json at exactly currentStackConfigVersion
// must still load cleanly.
func TestLoadStackConfig_AcceptsCurrentSchema(t *testing.T) {
	home := useEzstackHome(t)

	current := stackConfigFile{
		Version: currentStackConfigVersion,
		Repos:   map[string]*repoData{},
	}
	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "stacks.json"), data, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := LoadStackConfig("/test/repo"); err != nil {
		t.Fatalf("LoadStackConfig() rejected a current-schema file: %v", err)
	}
}

// writeNewerSchemaStacksJSON drops a stacks.json at the test's EZSTACK_HOME
// claiming a schema version one beyond what this binary supports. The repos
// block is intentionally empty — every guard under test must trigger on the
// version field alone, before any structural parse.
func writeNewerSchemaStacksJSON(t *testing.T) {
	t.Helper()
	home := useEzstackHome(t)
	future := map[string]any{
		"version": currentStackConfigVersion + 1,
		"repos":   map[string]any{},
	}
	data, err := json.MarshalIndent(future, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "stacks.json"), data, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// assertNewerSchemaError fails the test unless err is a non-nil refusal
// that mentions the upgrade path. Callers that exercise the four guarded
// write paths share this assertion so the user-visible remediation
// message stays consistent across them.
func assertNewerSchemaError(t *testing.T, err error, label string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s returned nil error for newer-schema stacks.json; want refusal", label)
	}
	msg := err.Error()
	for _, want := range []string{"newer", "upgrade"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%s error %q missing %q hint", label, msg, want)
		}
	}
}

// TestStackConfigSave_RefusesNewerSchema covers the race where a long-lived
// older binary holds an in-memory StackConfig and a newer binary writes a
// newer-schema stacks.json behind its back. Without the guard, Save's
// internal read+unmarshal+rewrite would persist a truncated form. The
// LoadStackConfig guard alone doesn't close this hole — Save can be
// reached from a StackConfig that was loaded before the upgrade.
func TestStackConfigSave_RefusesNewerSchema(t *testing.T) {
	writeNewerSchemaStacksJSON(t)

	// Construct a StackConfig directly so we exercise Save's read path
	// without going through LoadStackConfig (which would have refused
	// first). This simulates the in-memory-after-upgrade race.
	sc := &StackConfig{
		Stacks:  make(map[string]*Stack),
		Cache:   &CacheConfig{Branches: make(map[string]*BranchCache), repoDir: "/test/repo"},
		repoDir: "/test/repo",
	}
	assertNewerSchemaError(t, sc.Save("/test/repo"), "StackConfig.Save")
}

// TestMutateBranchCache_RefusesNewerSchema covers the narrow-update path
// (e.g. `ezs pr update` writing a single branch's PR state). It does its
// own read+modify+write under the file lock independently of
// LoadStackConfig, so the guard must live in MutateBranchCache too.
func TestMutateBranchCache_RefusesNewerSchema(t *testing.T) {
	writeNewerSchemaStacksJSON(t)

	err := MutateBranchCache("/test/repo", "feature", func(_ *BranchCache) (*BranchCache, error) {
		return &BranchCache{PRUrl: "https://example.invalid/1"}, nil
	})
	assertNewerSchemaError(t, err, "MutateBranchCache")
}

// TestCacheConfigSave_RefusesNewerSchema covers CacheConfig.Save (the
// `LoadCacheConfig + SetBranchCache + Save` path). Same race shape as
// StackConfig.Save: an older binary holding an in-memory CacheConfig
// must refuse to overwrite a newer-schema file rather than dropping
// fields it doesn't understand.
func TestCacheConfigSave_RefusesNewerSchema(t *testing.T) {
	writeNewerSchemaStacksJSON(t)

	cc := &CacheConfig{
		Branches: map[string]*BranchCache{
			"feature": {PRUrl: "https://example.invalid/1"},
		},
		repoDir: "/test/repo",
	}
	assertNewerSchemaError(t, cc.Save("/test/repo"), "CacheConfig.Save")
}

// TestLoadCacheConfig_RefusesNewerSchema covers the cache-only loader
// that older code paths use independently of LoadStackConfig. Without
// the guard, a subsequent CacheConfig.Save would persist the truncated
// form even though Save itself is now guarded — refusing at load is the
// safer default and keeps the error surface uniform.
func TestLoadCacheConfig_RefusesNewerSchema(t *testing.T) {
	writeNewerSchemaStacksJSON(t)

	_, err := LoadCacheConfig("/test/repo")
	assertNewerSchemaError(t, err, "LoadCacheConfig")
}
