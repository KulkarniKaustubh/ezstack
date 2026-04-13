package hooks

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setupHooksDir redirects EZSTACK_HOME to a tempdir and returns the hooks
// subdirectory path (pre-created).
func setupHooksDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("EZSTACK_HOME", home)
	hooksDir := filepath.Join(home, "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		t.Fatal(err)
	}
	return hooksDir
}

func writeHook(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func TestRun_MissingIsNoOp(t *testing.T) {
	setupHooksDir(t)
	if err := Run("pre-push", nil); err != nil {
		t.Errorf("missing hook should be no-op, got %v", err)
	}
}

func TestRun_NonExecutableIsNoOp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX exec-bit semantics")
	}
	dir := setupHooksDir(t)
	writeHook(t, filepath.Join(dir, "pre-push"), "#!/bin/sh\nexit 1\n", 0644)

	if err := Run("pre-push", nil); err != nil {
		t.Errorf("non-executable hook should be treated as not installed (no-op), got %v", err)
	}
}

func TestRun_ExitZeroSucceeds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX")
	}
	dir := setupHooksDir(t)
	writeHook(t, filepath.Join(dir, "pre-push"), "#!/bin/sh\nexit 0\n", 0755)

	if err := Run("pre-push", nil); err != nil {
		t.Errorf("exit 0 hook failed: %v", err)
	}
}

func TestRun_NonZeroReturnsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX")
	}
	dir := setupHooksDir(t)
	writeHook(t, filepath.Join(dir, "pre-push"), "#!/bin/sh\nexit 3\n", 0755)

	err := Run("pre-push", nil)
	if err == nil {
		t.Fatal("expected error from exit 3 hook")
	}
	if !strings.Contains(err.Error(), "pre-push") {
		t.Errorf("error should name the hook, got %v", err)
	}
}

// Run returns an error on any non-zero exit; the pre-/post- distinction is
// applied by callers. We document current behavior here.
func TestRun_PostHookNonZeroAlsoReturnsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX")
	}
	dir := setupHooksDir(t)
	writeHook(t, filepath.Join(dir, "post-push"), "#!/bin/sh\nexit 1\n", 0755)

	err := Run("post-push", nil)
	if err == nil {
		t.Error("post-push non-zero: Run returns the error to the caller (caller decides severity)")
	}
}

func TestRun_EnvVarsPassedThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX")
	}
	dir := setupHooksDir(t)
	outFile := filepath.Join(t.TempDir(), "env.out")
	body := "#!/bin/sh\n" +
		"{ echo EZS_HOOK=$EZS_HOOK; " +
		"echo EZS_REPO_ROOT=$EZS_REPO_ROOT; " +
		"echo EZS_BRANCH=$EZS_BRANCH; " +
		"echo EZS_STACK_HASH=$EZS_STACK_HASH; " +
		"echo EZS_STACK_NAME=$EZS_STACK_NAME; " +
		"echo EZS_CUSTOM=$EZS_CUSTOM; } > " + outFile + "\n"
	writeHook(t, filepath.Join(dir, "pre-commit"), body, 0755)

	ctx := &Context{
		RepoRoot:  "/my/repo",
		Branch:    "feature-x",
		StackHash: "abc123",
		StackName: "my-stack",
		Extra:     map[string]string{"EZS_CUSTOM": "yes"},
	}
	if err := Run("pre-commit", ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	f, err := os.Open(outFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "="); i >= 0 {
			got[line[:i]] = line[i+1:]
		}
	}

	want := map[string]string{
		"EZS_HOOK":       "pre-commit",
		"EZS_REPO_ROOT":  "/my/repo",
		"EZS_BRANCH":     "feature-x",
		"EZS_STACK_HASH": "abc123",
		"EZS_STACK_NAME": "my-stack",
		"EZS_CUSTOM":     "yes",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("env %s = %q, want %q", k, got[k], v)
		}
	}
}

func TestExists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX")
	}
	dir := setupHooksDir(t)
	if Exists("pre-push") {
		t.Error("Exists should be false when no hook file")
	}
	writeHook(t, filepath.Join(dir, "pre-push"), "#!/bin/sh\n", 0755)
	if !Exists("pre-push") {
		t.Error("Exists should be true for executable hook")
	}
	// Non-executable: not "installed"
	writeHook(t, filepath.Join(dir, "post-push"), "#!/bin/sh\n", 0644)
	if Exists("post-push") {
		t.Error("Exists should be false for non-executable hook")
	}
}
