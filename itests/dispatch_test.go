package itests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// buildEzsOnce caches the compiled ezs binary across tests in one process.
// Compiling ezs takes ~1s on a warm cache; once per test would dominate the
// suite's wall time. The binary lives in its own os.MkdirTemp directory —
// NOT t.TempDir() — so its lifetime spans every test in the package.
var (
	buildEzsOnceGuard sync.Once
	builtEzsPath      string
	builtEzsErr       error
)

func buildEzs(t *testing.T) string {
	t.Helper()
	buildEzsOnceGuard.Do(func() {
		dir, err := os.MkdirTemp("", "ezs-itest-bin-*")
		if err != nil {
			builtEzsErr = err
			return
		}
		out := filepath.Join(dir, "ezs")
		cmd := exec.Command("go", "build", "-o", out, "../cmd/ezs")
		if b, err := cmd.CombinedOutput(); err != nil {
			builtEzsErr = err
			t.Logf("go build output:\n%s", b)
			return
		}
		builtEzsPath = out
	})
	if builtEzsErr != nil {
		t.Fatalf("build ezs: %v", builtEzsErr)
	}
	return builtEzsPath
}

// TestDispatch_DoctorIsWired verifies `ezs doctor` actually reaches Doctor
// instead of hitting the unknown-command fallback. The feature was added as
// part of the CLI bundle but the dispatch was never wired up in
// cmd/ezs/main.go — this test is the regression gate.
func TestDispatch_DoctorIsWired(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	bin := buildEzs(t)

	cmd := exec.Command(bin, "doctor")
	cmd.Dir = env.RepoDir
	out, _ := cmd.CombinedOutput() // exit code depends on system deps; ignore
	body := string(out)

	if strings.Contains(body, "Unknown command") {
		t.Fatalf("doctor not wired up — got 'Unknown command':\n%s", body)
	}
	if !strings.Contains(body, "ezstack doctor") {
		t.Errorf("expected 'ezstack doctor' header:\n%s", body)
	}
}

// TestDispatch_DoctorWorksOutsideGitRepo locks down the documented behavior
// that `ezs doctor` is meant to be runnable on a fresh machine, before any
// repo has been cloned. The main.go repo-root check must skip doctor.
func TestDispatch_DoctorWorksOutsideGitRepo(t *testing.T) {
	bin := buildEzs(t)

	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)
	cmd := exec.Command(bin, "doctor")
	cmd.Dir = tmp // not a git repo
	cmd.Env = append(cmd.Environ(), "EZSTACK_HOME="+tmp)
	out, _ := cmd.CombinedOutput()
	body := string(out)

	if strings.Contains(body, "must be run from a git repository") {
		t.Errorf("doctor refused to run outside a git repo — regression on the fresh-machine use case:\n%s", body)
	}
	if !strings.Contains(body, "ezstack doctor") {
		t.Errorf("doctor didn't produce expected header:\n%s", body)
	}
}

// TestDispatch_InfoIsWired verifies `ezs --info` reaches Info.
func TestDispatch_InfoIsWired(t *testing.T) {
	bin := buildEzs(t)

	tmp := t.TempDir()
	cmd := exec.Command(bin, "--info")
	cmd.Dir = tmp
	cmd.Env = append(cmd.Environ(), "EZSTACK_HOME="+tmp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ezs --info failed: %v\n%s", err, out)
	}
	body := string(out)

	if strings.Contains(body, "Unknown command") {
		t.Fatalf("--info not wired up:\n%s", body)
	}
	for _, want := range []string{"ezstack diagnostic info", "ezstack version:", "config dir:"} {
		if !strings.Contains(body, want) {
			t.Errorf("--info output missing %q:\n%s", want, body)
		}
	}
}

// TestDispatch_DidYouMeanForTypo verifies the unknown-command default path
// calls ui.SuggestCommand and prints a typo hint. Table-driven so we pin
// down exactly which typos the branch promises to catch.
func TestDispatch_DidYouMeanForTypo(t *testing.T) {
	bin := buildEzs(t)
	env := SetupTestEnv(t)
	defer env.Cleanup()

	tests := []struct {
		typo string
		want string
	}{
		{"snc", "sync"},     // swap
		{"statu", "status"}, // truncation
		{"pr", ""},          // exact match — must NOT suggest anything
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.typo, func(t *testing.T) {
			cmd := exec.Command(bin, tc.typo)
			cmd.Dir = env.RepoDir
			out, _ := cmd.CombinedOutput()
			body := string(out)

			if tc.typo == "pr" {
				// pr is a real command — this case confirms the test setup
				// actually reaches dispatch. It should NOT print "Unknown".
				if strings.Contains(body, "Unknown command") {
					t.Errorf("real command 'pr' was dispatched as unknown:\n%s", body)
				}
				return
			}
			if !strings.Contains(body, "Unknown command: "+tc.typo) {
				t.Errorf("expected 'Unknown command: %s':\n%s", tc.typo, body)
			}
			if tc.want != "" {
				hint := "Did you mean: " + tc.want + "?"
				if !strings.Contains(body, hint) {
					t.Errorf("expected hint %q for typo %q:\n%s", hint, tc.typo, body)
				}
			}
		})
	}
}

// TestDispatch_NoSuggestionForGarbage asserts we don't spam users with
// nonsense suggestions when the typo is nowhere near any command.
func TestDispatch_NoSuggestionForGarbage(t *testing.T) {
	bin := buildEzs(t)
	env := SetupTestEnv(t)
	defer env.Cleanup()

	cmd := exec.Command(bin, "xyzzy123")
	cmd.Dir = env.RepoDir
	out, _ := cmd.CombinedOutput()
	body := string(out)

	if !strings.Contains(body, "Unknown command") {
		t.Fatalf("expected 'Unknown command':\n%s", body)
	}
	if strings.Contains(body, "Did you mean:") {
		t.Errorf("spurious did-you-mean suggestion for gibberish input:\n%s", body)
	}
}
