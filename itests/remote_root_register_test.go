package itests

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// TestNewFromRemote_RegistersRootAsRemote is the boundary test that wires the
// `ezs new -r` flow end-to-end through to PrintStack, locking the contract
// that drives the (remote) tag in the human view AND the root_is_remote=true
// field in the JSON envelope.
//
// What it guards against:
//
//   - A future refactor that changes RegisterRemoteBranch to skip
//     RootIsRemote (e.g., gating on PR detection success): the field would
//     silently disappear from stacks.json.
//   - A future PrintStack refactor that drops the inline "(remote)" render
//     even though the field is set: the human-readable output would
//     disagree with what `ezs ls --json` reports.
//
// The unit-level coverage exists (TestManager_RegisterRemoteBranch_SetsRootIsRemote
// and TestPrintStack_RemoteTagOnRoot), but only this end-to-end path proves
// the CLI actually wires them together against real on-disk state.
func TestNewFromRemote_RegistersRootAsRemote(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	useStubBackend(t)

	// Bare upstream that the test repo will treat as `origin`.
	upstream := filepath.Join(env.TmpDir, "upstream.git")
	mustRunGit(t, "", "init", "--bare", "-b", "main", upstream)

	// Scratch clone seeds main + a contributor branch we'll pick up via -r.
	scratch := filepath.Join(env.TmpDir, "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatalf("mkdir scratch: %v", err)
	}
	mustRunGit(t, scratch, "init", "-q", "-b", "main")
	mustRunGit(t, scratch, "config", "user.email", TestUserEmail)
	mustRunGit(t, scratch, "config", "user.name", TestUserName)
	mustRunGit(t, scratch, "remote", "add", "origin", upstream)
	if err := os.WriteFile(filepath.Join(scratch, "SEED"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mustRunGit(t, scratch, "add", ".")
	mustRunGit(t, scratch, "commit", "-qm", "seed")
	mustRunGit(t, scratch, "push", "-q", "origin", "main")

	const remoteBranch = "alice/feature"
	mustRunGit(t, scratch, "checkout", "-qb", remoteBranch)
	if err := os.WriteFile(filepath.Join(scratch, "feat.txt"), []byte("alice work\n"), 0o644); err != nil {
		t.Fatalf("feat: %v", err)
	}
	mustRunGit(t, scratch, "add", ".")
	mustRunGit(t, scratch, "commit", "-qm", "alice's feature")
	mustRunGit(t, scratch, "push", "-q", "origin", remoteBranch)

	mustRunGit(t, env.RepoDir, "remote", "add", "origin", upstream)
	mustRunGit(t, env.RepoDir, "fetch", "-q", "origin")
	mustRunGit(t, env.RepoDir, "reset", "--hard", "-q", "origin/main")

	prevCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(env.RepoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(prevCwd)

	// `ezs new -r alice/feature my-feature` — register the contributor branch
	// as the new stack's root and create a user-owned child on top of it.
	if err := commands.New([]string{"-r", remoteBranch, "my-feature"}); err != nil {
		t.Fatalf("commands.New(-r %s my-feature): %v", remoteBranch, err)
	}

	// In-memory: the stack's RootIsRemote field must be set.
	mgr, err := stack.NewReadOnlyManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewReadOnlyManager: %v", err)
	}
	var s *config.Stack
	for _, st := range mgr.ListStacks() {
		if st.Root == remoteBranch {
			s = st
			break
		}
	}
	if s == nil {
		t.Fatalf("no stack found with root=%q after `ezs new -r`; stacks: %+v",
			remoteBranch, mgr.ListStacks())
	}
	if !s.RootIsRemote {
		t.Errorf("stack root=%q: RootIsRemote=false, want true after `ezs new -r`", s.Root)
	}

	// On-disk: the JSON-encoded stacks file must persist the field, since the
	// next invocation reads it from disk and the human/JSON renders both depend
	// on it round-tripping cleanly. omitempty would erase a true→false flip,
	// so the literal-string check is load-bearing.
	stacksPath := filepath.Join(env.ConfigDir, "stacks.json")
	data, readErr := os.ReadFile(stacksPath)
	if readErr != nil {
		t.Fatalf("read stacks.json: %v", readErr)
	}
	if !strings.Contains(string(data), `"root_is_remote": true`) {
		t.Errorf("stacks.json missing %q after `ezs new -r`:\n%s",
			`"root_is_remote": true`, string(data))
	}

	// Render parity: PrintStack must emit (remote) on the root line. This is
	// the actual user-visible signal — the field could exist on disk but be
	// dropped by a future render refactor and we'd never know without this
	// step.
	out := captureStderrItest(t, func() {
		ui.PrintStack(s, "my-feature", false, nil)
	})
	rootIdx := strings.Index(out, remoteBranch)
	if rootIdx == -1 {
		t.Fatalf("PrintStack didn't render root branch %q:\n%s", remoteBranch, out)
	}
	rootLineEnd := rootIdx + strings.Index(out[rootIdx:], "\n")
	rootLine := out[rootIdx:rootLineEnd]
	if !strings.Contains(rootLine, "(remote)") {
		t.Errorf("PrintStack root line %q missing (remote) tag — render contract regressed",
			rootLine)
	}
}

// captureStderrItest is a local copy of the stderr-capture pattern used by
// the unit tests in internal/ui — repeated here because itests can't import
// the test-only helpers from another package.
func captureStderrItest(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	w.Close()
	os.Stderr = orig
	return <-done
}
