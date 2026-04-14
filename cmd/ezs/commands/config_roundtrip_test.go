package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

func seedConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("EZSTACK_HOME", home)
	useWT := true
	cfg := &config.Config{
		DefaultBaseBranch: "main",
		GitHubToken:       "secret-token-abc",
		Repos: map[string]*config.RepoConfig{
			"/fake/repo": {
				RepoPath:        "/fake/repo",
				WorktreeBaseDir: "/fake/worktrees",
				UseWorktrees:    &useWT,
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestConfigExport_RedactsTokenAnd0600(t *testing.T) {
	seedConfig(t)
	dest := filepath.Join(t.TempDir(), "export.json")

	if err := configExport(dest); err != nil {
		t.Fatalf("configExport: %v", err)
	}

	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-token-abc") {
		t.Error("exported file leaked real token")
	}
	// json.MarshalIndent HTML-escapes '<' and '>' so compare against the
	// decoded value rather than a raw substring.
	_ = redactedExportMarker

	// Ensure the exported JSON parses cleanly as Config.
	var parsed config.Config
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Errorf("exported JSON does not parse as Config: %v", err)
	}
	if parsed.GitHubToken != redactedExportMarker {
		t.Errorf("token in export = %q, want redaction marker", parsed.GitHubToken)
	}
}

func TestConfigImport_RoundtripPreservesToken(t *testing.T) {
	seedConfig(t)
	dest := filepath.Join(t.TempDir(), "export.json")
	if err := configExport(dest); err != nil {
		t.Fatal(err)
	}

	// configImport asks for confirmation via ConfirmTUIWithDefault — in
	// YesMode this auto-confirms without reading stdin.
	prev := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = prev }()

	if err := configImport(dest); err != nil {
		t.Fatalf("configImport: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitHubToken != "secret-token-abc" {
		t.Errorf("token after roundtrip = %q, want secret-token-abc (should have been preserved)", cfg.GitHubToken)
	}
	if _, ok := cfg.Repos["/fake/repo"]; !ok {
		t.Error("repo config lost in roundtrip")
	}
}

func TestConfigImport_RejectsUnknownFields(t *testing.T) {
	seedConfig(t)
	junk := filepath.Join(t.TempDir(), "junk.json")
	if err := os.WriteFile(junk, []byte(`{"default_base_branch":"main","bogus_field":true}`), 0600); err != nil {
		t.Fatal(err)
	}

	prev := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = prev }()

	err := configImport(junk)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), "invalid config") {
		t.Errorf("error = %v, want 'invalid config' wrapping", err)
	}
}

func TestConfigImport_InvalidJSON(t *testing.T) {
	seedConfig(t)
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte("not json"), 0600)

	prev := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = prev }()

	if err := configImport(bad); err == nil {
		t.Error("expected error for invalid JSON")
	}
}
