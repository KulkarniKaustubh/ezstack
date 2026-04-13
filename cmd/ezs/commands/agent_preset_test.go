package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyAgentPreset_Empty(t *testing.T) {
	out, err := applyAgentPreset("hello", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello" {
		t.Errorf("empty preset should be no-op, got %q", out)
	}
}

func TestApplyAgentPreset_Known(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("EZSTACK_HOME", tmp)

	presetsDir := filepath.Join(tmp, "agent-presets")
	if err := os.MkdirAll(presetsDir, 0755); err != nil {
		t.Fatal(err)
	}
	presetBody := "Be thorough.\n"
	if err := os.WriteFile(filepath.Join(presetsDir, "thorough.md"), []byte(presetBody), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := applyAgentPreset("base prompt", "thorough")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "base prompt") {
		t.Error("result should contain the original prompt")
	}
	if !strings.Contains(out, "## Preset: thorough") {
		t.Error("result should contain preset header")
	}
	if !strings.Contains(out, "Be thorough.") {
		t.Error("result should contain preset body")
	}
}

func TestApplyAgentPreset_Unknown(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("EZSTACK_HOME", tmp)

	_, err := applyAgentPreset("base", "does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown preset")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error should mention preset name, got %v", err)
	}
}

func TestApplyAgentPreset_AppendsNewlineBeforeHeader(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("EZSTACK_HOME", tmp)
	presetsDir := filepath.Join(tmp, "agent-presets")
	os.MkdirAll(presetsDir, 0755)
	os.WriteFile(filepath.Join(presetsDir, "p.md"), []byte("body"), 0644)

	// Prompt does NOT end with newline — applyAgentPreset should add one
	// before appending the preset header so the header isn't glued on.
	out, err := applyAgentPreset("no-newline", "p")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no-newline\n") {
		t.Errorf("expected newline inserted after original prompt, got %q", out)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~/foo", filepath.Join(home, "foo")},
		{"~/", home},
	}
	for _, c := range cases {
		got := expandHome(c.in)
		if got != c.want {
			t.Errorf("expandHome(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
