package commands

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdAndErr replaces os.Stdout and os.Stderr with pipes, runs fn, and
// returns whatever was written to each. Used to verify Doctor/Info produce the
// expected shape without touching the test process's terminal.
func captureStdAndErr(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	os.Stdout, os.Stderr = outW, errW

	outDone := make(chan string)
	errDone := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, outR)
		outDone <- buf.String()
	}()
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, errR)
		errDone <- buf.String()
	}()

	fn()

	outW.Close()
	errW.Close()
	os.Stdout, os.Stderr = origOut, origErr
	return <-outDone, <-errDone
}

// TestDoctor_RunsOnCleanEnvironment asserts Doctor executes end-to-end and
// produces its header + the dependency-check lines for git/gh/fzf. It's a
// smoke test — the branch that actually ships Doctor had zero test coverage
// for the command itself.
func TestDoctor_RunsOnCleanEnvironment(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("EZSTACK_HOME", tmp)

	var err error
	_, stderr := captureStdAndErr(t, func() {
		err = Doctor(nil)
	})

	if stderr == "" {
		t.Fatal("Doctor produced no stderr output — expected at least a header")
	}
	if !strings.Contains(stderr, "ezstack doctor") {
		t.Errorf("Doctor stderr missing header:\n%s", stderr)
	}
	// The three dependency checks should each produce a line mentioning the
	// tool. At least `git` is almost always present on dev/CI boxes; gh/fzf
	// may or may not be, but Doctor must at least attempt them.
	for _, tool := range []string{"git", "gh", "fzf"} {
		if !strings.Contains(stderr, tool) {
			t.Errorf("Doctor stderr missing check for %q:\n%s", tool, stderr)
		}
	}
	// Regardless of pass/fail, Doctor must have reached the final verdict line.
	if !strings.Contains(stderr, "problem") && !strings.Contains(stderr, "No problems detected") {
		t.Errorf("Doctor did not emit a final verdict line:\n%s", stderr)
	}
	// err is non-nil only when problems were detected; either is acceptable
	// for a smoke test, but a panic or early exit would show as neither output
	// nor a verdict line.
	_ = err
}

// TestDoctor_ExamplesShortCircuits covers the `--examples` shared-helper path
// wired into Doctor — it must return nil and print registered examples
// without running the health checks.
func TestDoctor_ExamplesShortCircuits(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("EZSTACK_HOME", tmp)

	var err error
	stdout, stderr := captureStdAndErr(t, func() {
		err = Doctor([]string{"--examples"})
	})
	if err != nil {
		t.Errorf("Doctor --examples returned error: %v", err)
	}
	// --examples prints to stdout (PrintExamples), not stderr. Doctor's
	// health-check output goes to stderr, which must be empty for the
	// short-circuit path.
	if stderr != "" {
		t.Errorf("Doctor --examples produced stderr output (health check should not have run):\n%s", stderr)
	}
	if stdout == "" {
		t.Error("Doctor --examples produced no stdout — expected registered examples")
	}
}

// TestInfo_PrintsDiagnosticReport asserts Info emits all the sections a bug
// report needs: version, toolchain versions, config state.
func TestInfo_PrintsDiagnosticReport(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("EZSTACK_HOME", tmp)

	stdout, _ := captureStdAndErr(t, func() {
		Info("9.9.9-test")
	})

	expected := []string{
		"ezstack diagnostic info",
		"ezstack version: 9.9.9-test",
		"config dir:",
	}
	for _, want := range expected {
		if !strings.Contains(stdout, want) {
			t.Errorf("Info output missing %q:\n%s", want, stdout)
		}
	}
	// Doctor/Info operate over the process env's config dir. With a fresh
	// EZSTACK_HOME, config.json is missing — Info must report that rather
	// than crashing.
	if !strings.Contains(stdout, "config file: missing") {
		t.Errorf("Info should report missing config when EZSTACK_HOME is empty:\n%s", stdout)
	}
}

// TestInfo_FallsBackToNotInstalledOnToolError asserts every toolchain entry
// (go, git, gh, fzf) prints either its `--version` line or "not installed".
// The previous code silently dropped go/git failures, leaving a surprising
// gap in bug reports. This test enforces the symmetric fallback.
func TestInfo_FallsBackToNotInstalledOnToolError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("EZSTACK_HOME", tmp)
	// Empty PATH means none of the tools resolve. Every tool entry must
	// then read "<name>: not installed".
	t.Setenv("PATH", "")

	stdout, _ := captureStdAndErr(t, func() {
		Info("t")
	})

	for _, tool := range []string{"go", "git", "gh", "fzf"} {
		want := tool + ": not installed"
		if !strings.Contains(stdout, want) {
			t.Errorf("Info missing fallback line %q with empty PATH:\n%s", want, stdout)
		}
	}
}

// TestInfo_ReportsConfigPresent writes a minimal config and asserts Info
// reads it back instead of reporting missing.
func TestInfo_ReportsConfigPresent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("EZSTACK_HOME", tmp)
	cfgPath := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"default_base_branch": "main", "repos": {}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	stdout, _ := captureStdAndErr(t, func() {
		Info("t")
	})

	if !strings.Contains(stdout, "config file: present") {
		t.Errorf("Info should report present config:\n%s", stdout)
	}
	if !strings.Contains(stdout, "repos configured: 0") {
		t.Errorf("Info should report 0 repos for empty-repos config:\n%s", stdout)
	}
	if !strings.Contains(stdout, "default base branch: main") {
		t.Errorf("Info should surface default base branch:\n%s", stdout)
	}
}
