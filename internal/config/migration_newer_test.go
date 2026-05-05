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
