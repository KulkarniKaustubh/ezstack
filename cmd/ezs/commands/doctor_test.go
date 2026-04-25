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
//
// Doctor's output split: per-tool checks always go to stderr, but the final
// verdict only reaches stderr on the success path ("No problems detected").
// On the failure path the count goes only into the returned error — the
// caller in main.go is responsible for printing it. This test handles both.
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
	// Doctor must always reach a terminal state. Two valid shapes:
	//   - All checks passed: stderr contains "No problems detected", err is nil.
	//   - At least one failed: err is non-nil and matches "N problem(s) detected".
	// A panic or silent early-return shows up as neither.
	switch {
	case strings.Contains(stderr, "No problems detected") && err == nil:
		// healthy environment — fine
	case err != nil && strings.Contains(err.Error(), "problem(s) detected"):
		// some tool was missing — also fine, expected on minimal CI runners
	default:
		t.Errorf(
			"Doctor did not reach a verdict.\nstderr:\n%s\nerr: %v",
			stderr, err,
		)
	}
}

// TestDoctor_ProblemDetectedReturnsError pins down the failure path
// explicitly: when a required tool is missing, Doctor must return a
// non-nil error whose message contains "problem(s) detected" so callers
// (and CI gates, and shell scripts checking $?) can act on it. We force
// the failure by setting PATH to "" so no required tool resolves.
func TestDoctor_ProblemDetectedReturnsError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("EZSTACK_HOME", tmp)
	t.Setenv("PATH", "") // no git/gh/fzf reachable

	var err error
	captureStdAndErr(t, func() {
		err = Doctor(nil)
	})

	if err == nil {
		t.Fatal("Doctor returned nil with no tools on PATH — should report problems")
	}
	if !strings.Contains(err.Error(), "problem(s) detected") {
		t.Errorf("error %q should contain 'problem(s) detected'", err.Error())
	}
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
