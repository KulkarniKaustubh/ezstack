package itests

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
)

// runEzsStubbed runs the built ezs binary inside env.RepoDir with PATH
// stubbed to env.StubBinDir, so tests can shadow `claude` / `aider` /
// `ezs-mcp` with their own scripts. Differs from runEzs (defined in
// cli_flag_validation_test.go) in two ways: it returns the raw bytes
// (callers parse them) and it overrides PATH to put the stubs first.
func runEzsStubbed(t *testing.T, env *TestEnv, args ...string) ([]byte, error) {
	t.Helper()
	ezsBin := buildEzsBinary(t)
	cmd := exec.Command(ezsBin, args...)
	cmd.Dir = env.RepoDir
	cmd.Env = append(os.Environ(),
		"EZSTACK_HOME="+env.ConfigDir,
		"PATH="+env.StubBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	return cmd.CombinedOutput()
}

// agentStubScript returns a shell script body that, on each invocation,
// records the full argv to a per-invocation file inside logDir. Files are
// named "0", "1", "2", … in invocation order.
//
// We separate args with NUL bytes (printf '%s\0') because the rendered
// agent prompt arg contains newlines — a per-line scheme would shred one
// arg across many "lines" and break any test that wants to inspect the
// prompt body. NUL is safe: argv strings cannot contain NUL on POSIX.
func agentStubScript(logDir string) string {
	return `#!/bin/sh
if [ "$1" = "mcp" ]; then exit 0; fi
mkdir -p "` + logDir + `"
n=$(ls "` + logDir + `" 2>/dev/null | wc -l | tr -d ' ')
file="` + logDir + `/$n"
for arg in "$@"; do
  printf '%s\0' "$arg" >> "$file"
done
exit 0
`
}

// readArgsLog returns the full agent argv (one entry per arg) for a given
// invocation index (0-based). Splits on NUL because the stub uses NUL as
// the inter-arg separator.
func readArgsLog(t *testing.T, logDir string, want int) []string {
	t.Helper()
	path := filepath.Join(logDir, fmt.Sprintf("%d", want))
	data, err := os.ReadFile(path)
	if err != nil {
		entries, _ := os.ReadDir(logDir)
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("invocation %d not found at %s (existing: %v): %v", want, path, names, err)
	}
	// Strip the trailing NUL added by the final printf, then split.
	out := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	if len(out) == 1 && out[0] == "" {
		return nil
	}
	return out
}

// TestAgentSession_FreshThenResume covers the round-trip: first `ezs agent`
// run mints a session ID and passes it via --session-id. Second run for the
// same stack reads the persisted ID and passes --resume.
//
// This is THE regression for issue #16 — every other test in this suite
// depends on it staying green.
func TestAgentSession_FreshThenResume(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feature-x", "main")

	// Per-invocation argv files so embedded newlines in the prompt arg can't
	// shred records across lines.
	logDir := filepath.Join(env.TmpDir, "claude_args")
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), agentStubScript(logDir))
	writeExecutable(t, filepath.Join(env.StubBinDir, "ezs-mcp"), "#!/bin/sh\nexit 0\n")

	// First run: should mint a fresh UUID and pass --session-id <uuid>.
	out, err := runEzsStubbed(t, env, "agent", "--branch", "feature-x")
	if err != nil {
		t.Fatalf("first run failed: %v\n%s", err, out)
	}

	first := readArgsLog(t, logDir, 0)
	sessionID := flagValue(first, "--session-id")
	if sessionID == "" {
		t.Fatalf("first run did not pass --session-id; argv: %v", first)
	}
	if !flagValueContains(first, "--name", "_ezstack-feature-x") {
		t.Errorf("first run --name should be _ezstack-feature-x; argv: %v", first)
	}
	if flagValue(first, "--resume") != "" {
		t.Errorf("first run unexpectedly used --resume; argv: %v", first)
	}

	// Second run: should pick up the persisted UUID and pass --resume.
	out, err = runEzsStubbed(t, env, "agent", "--branch", "feature-x")
	if err != nil {
		t.Fatalf("second run failed: %v\n%s", err, out)
	}
	second := readArgsLog(t, logDir, 1)
	if got := flagValue(second, "--resume"); got != sessionID {
		t.Errorf("second run --resume = %q, want %q (argv: %v)", got, sessionID, second)
	}
	if flagValue(second, "--session-id") != "" {
		t.Errorf("second run should not use --session-id when resuming; argv: %v", second)
	}
}

// TestAgentSession_NoResumeFlag covers --no-resume: even with a persisted
// session ID, the user can force a fresh one. The stored ID gets replaced.
func TestAgentSession_NoResumeFlag(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feat-y", "main")

	logDir := filepath.Join(env.TmpDir, "claude_args")
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), agentStubScript(logDir))

	// First run mints a session.
	if out, err := runEzsStubbed(t, env, "agent", "--branch", "feat-y"); err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	first := readArgsLog(t, logDir, 0)
	originalID := flagValue(first, "--session-id")
	if originalID == "" {
		t.Fatalf("first run missing --session-id; argv: %v", first)
	}

	// Second run with --no-resume: should mint a NEW UUID and pass --session-id again.
	if out, err := runEzsStubbed(t, env, "agent", "--branch", "feat-y", "--no-resume"); err != nil {
		t.Fatalf("second run with --no-resume: %v\n%s", err, out)
	}
	second := readArgsLog(t, logDir, 1)
	if flagValue(second, "--resume") != "" {
		t.Errorf("--no-resume must not use --resume; argv: %v", second)
	}
	newID := flagValue(second, "--session-id")
	if newID == "" {
		t.Fatalf("--no-resume should mint a fresh --session-id; argv: %v", second)
	}
	if newID == originalID {
		t.Errorf("--no-resume should replace session ID; got the same %q both runs", newID)
	}

	// Third run (no flag) should now resume the *new* ID, proving the
	// post-spawn persist actually wrote.
	if out, err := runEzsStubbed(t, env, "agent", "--branch", "feat-y"); err != nil {
		t.Fatalf("third run: %v\n%s", err, out)
	}
	third := readArgsLog(t, logDir, 2)
	if got := flagValue(third, "--resume"); got != newID {
		t.Errorf("third run --resume = %q, want fresh ID %q", got, newID)
	}
}

// TestAgentSession_PassthroughExtras covers `ezs agent -- <agent-args>`: any
// tokens after `--` should appear verbatim in the agent CLI invocation.
func TestAgentSession_PassthroughExtras(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feat-z", "main")

	logDir := filepath.Join(env.TmpDir, "claude_args")
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), agentStubScript(logDir))

	if out, err := runEzsStubbed(t, env, "agent", "--branch", "feat-z", "--", "--debug", "--model", "opus"); err != nil {
		t.Fatalf("run with extras: %v\n%s", err, out)
	}
	argv := readArgsLog(t, logDir, 0)
	// All three pass-through tokens must appear, in order, somewhere in the argv.
	wantSeq := []string{"--debug", "--model", "opus"}
	if !containsSeq(argv, wantSeq) {
		t.Errorf("expected pass-through %v in argv; got %v", wantSeq, argv)
	}
}

// TestAgentSession_NonClaudeAgent_NoSessionInjection ensures we don't
// silently inject --session-id/--resume into agents we don't understand.
// A user pointing agent_command at, say, `aider` should see plain argv
// (only the prompt) — anything else risks misparsing.
func TestAgentSession_NonClaudeAgent_NoSessionInjection(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feat-q", "main")

	logDir := filepath.Join(env.TmpDir, "aider_args")
	// Reuse the same stub generator: aider's "mcp" arg never matches because
	// ezs only does MCP setup for claude-family agents.
	writeExecutable(t, filepath.Join(env.StubBinDir, "aider"), agentStubScript(logDir))

	if out, err := runEzsStubbed(t, env, "agent", "--branch", "feat-q", "--cmd", "aider"); err != nil {
		t.Fatalf("run with aider: %v\n%s", err, out)
	}
	argv := readArgsLog(t, logDir, 0)
	if flagValue(argv, "--session-id") != "" {
		t.Errorf("aider must not receive --session-id; argv: %v", argv)
	}
	if flagValue(argv, "--resume") != "" {
		t.Errorf("aider must not receive --resume; argv: %v", argv)
	}
	if flagValue(argv, "--name") != "" {
		t.Errorf("aider must not receive --name; argv: %v", argv)
	}
}

// TestAgentSession_NonClaudeAgent_ExposesEnvVar pins the documented contract
// for non-claude agents: ezs does NOT inject CLI flags, but DOES expose the
// session UUID through EZS_AGENT_SESSION_ID. User-supplied wrappers can read
// that env var and decide whether to wire their own resume semantics on top.
//
// This was the doc/code mismatch the previous review caught — the docs said
// non-claude wrappers could opt in via the env var, but resolveWorkSession
// returned nil for them so the var was never set. Pin it from both sides
// (set on first run, persisted UUID on second run) so it can't regress.
func TestAgentSession_NonClaudeAgent_ExposesEnvVar(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feat-env", "main")

	envLog := filepath.Join(env.TmpDir, "aider_env_log.txt")
	envCaptureStub := `#!/bin/sh
printf '%s\n' "EZS_AGENT_SESSION_ID=${EZS_AGENT_SESSION_ID:-}" >> "` + envLog + `"
exit 0
`
	writeExecutable(t, filepath.Join(env.StubBinDir, "aider"), envCaptureStub)

	// First run: aider should receive a freshly-minted UUID via the env var.
	if out, err := runEzsStubbed(t, env, "agent", "--branch", "feat-env", "--cmd", "aider"); err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	first := readEnvLogLine(t, envLog, 0)
	firstID := strings.TrimPrefix(first, "EZS_AGENT_SESSION_ID=")
	if firstID == "" {
		t.Fatalf("non-claude agent did not receive EZS_AGENT_SESSION_ID on first run; got %q", first)
	}

	// Second run: the persisted UUID should be re-exposed (no flag injection,
	// just the env var). Same value as the first run — that's the contract.
	if out, err := runEzsStubbed(t, env, "agent", "--branch", "feat-env", "--cmd", "aider"); err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	second := readEnvLogLine(t, envLog, 1)
	secondID := strings.TrimPrefix(second, "EZS_AGENT_SESSION_ID=")
	if secondID != firstID {
		t.Errorf("second run env var should reuse persisted UUID; got %q, want %q", secondID, firstID)
	}

	// --no-resume: should mint a new UUID and re-expose that one instead.
	if out, err := runEzsStubbed(t, env, "agent", "--branch", "feat-env", "--cmd", "aider", "--no-resume"); err != nil {
		t.Fatalf("--no-resume run: %v\n%s", err, out)
	}
	third := readEnvLogLine(t, envLog, 2)
	thirdID := strings.TrimPrefix(third, "EZS_AGENT_SESSION_ID=")
	if thirdID == "" {
		t.Fatal("--no-resume did not expose a fresh UUID via env var")
	}
	if thirdID == firstID {
		t.Errorf("--no-resume should mint a new UUID; got the same value %q both times", thirdID)
	}
}

// TestAgentSession_ClaudeAgent_ExposesEnvVar mirrors the non-claude env-var
// test for claude — to make sure we didn't accidentally make CLI injection
// and env var injection mutually exclusive. Claude consumers should see both.
func TestAgentSession_ClaudeAgent_ExposesEnvVar(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feat-claude-env", "main")

	envLog := filepath.Join(env.TmpDir, "claude_env_log.txt")
	stub := `#!/bin/sh
if [ "$1" = "mcp" ]; then exit 0; fi
printf '%s\n' "EZS_AGENT_SESSION_ID=${EZS_AGENT_SESSION_ID:-}" >> "` + envLog + `"
exit 0
`
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), stub)

	if out, err := runEzsStubbed(t, env, "agent", "--branch", "feat-claude-env"); err != nil {
		t.Fatalf("claude run: %v\n%s", err, out)
	}
	got := readEnvLogLine(t, envLog, 0)
	id := strings.TrimPrefix(got, "EZS_AGENT_SESSION_ID=")
	if id == "" {
		t.Fatalf("claude must also receive EZS_AGENT_SESSION_ID; got %q", got)
	}
}

// TestAgentFeature_ExistingStackResumes covers feature-mode resume: running
// `ezs agent feature -s <hash> "<desc>"` twice against the same stack must
// resume the same session ID on the second run. This is the feature-mode
// counterpart to TestAgentSession_FreshThenResume; without it,
// resolveFeatureSession's resume branch (existing stack with stored ID) had
// no integration coverage.
func TestAgentFeature_ExistingStackResumes(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Seed a stack so feature mode binds its session to it. The first branch
	// implicitly creates the stack and gives us a hash to pass via -s.
	CreateBranchWithCommit(t, env, "feat-base", "main")

	// Look up the stack hash that owns feat-base.
	mgr, err := stack.NewReadOnlyManager(env.RepoDir)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	s := mgr.FindStackForBranch("feat-base")
	if s == nil {
		t.Fatal("seeded stack not found")
	}
	stackHash := s.Hash

	logDir := filepath.Join(env.TmpDir, "claude_args")
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), agentStubScript(logDir))
	writeExecutable(t, filepath.Join(env.StubBinDir, "ezs-mcp"), "#!/bin/sh\nexit 0\n")

	// First feature run binds a session to the existing stack.
	if out, err := runEzsStubbed(t, env, "agent", "feature", "-s", stackHash, "Add JWT auth"); err != nil {
		t.Fatalf("first feature run: %v\n%s", err, out)
	}
	first := readArgsLog(t, logDir, 0)
	id := flagValue(first, "--session-id")
	if id == "" {
		t.Fatalf("first feature run missing --session-id; argv: %v", first)
	}

	// Second feature run with the same stack hash should resume the same ID.
	if out, err := runEzsStubbed(t, env, "agent", "feature", "-s", stackHash, "Continue auth work"); err != nil {
		t.Fatalf("second feature run: %v\n%s", err, out)
	}
	second := readArgsLog(t, logDir, 1)
	if got := flagValue(second, "--resume"); got != id {
		t.Errorf("second feature run --resume = %q, want %q (existing-stack session must be resumed)", got, id)
	}
	if flagValue(second, "--session-id") != "" {
		t.Errorf("second feature run should not start a new --session-id; argv: %v", second)
	}
}

// TestAgentSession_StackScopedPersistsToStack verifies that when the agent
// runs without --branch, the session is bound to the stack hash (not to
// any individual branch). Switching to a sibling branch and running again
// resumes the same session.
func TestAgentSession_StackScopedPersistsToStack(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Two branches in the same stack.
	CreateBranchWithCommit(t, env, "feat-a", "main")
	CreateBranchWithCommit(t, env, "feat-b", "feat-a")

	logDir := filepath.Join(env.TmpDir, "claude_args")
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), agentStubScript(logDir))

	// First run from feat-a's worktree (no --branch flag) → stack-scoped.
	cmd := exec.Command(buildEzsBinary(t), "agent")
	cmd.Dir = filepath.Join(env.WorktreeDir, "feat-a")
	cmd.Env = append(os.Environ(),
		"EZSTACK_HOME="+env.ConfigDir,
		"PATH="+env.StubBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	first := readArgsLog(t, logDir, 0)
	id := flagValue(first, "--session-id")
	if id == "" {
		t.Fatalf("first run missing --session-id; argv: %v", first)
	}

	// Second run from feat-b's worktree (different branch, same stack) →
	// must resume the SAME session.
	cmd = exec.Command(buildEzsBinary(t), "agent")
	cmd.Dir = filepath.Join(env.WorktreeDir, "feat-b")
	cmd.Env = append(os.Environ(),
		"EZSTACK_HOME="+env.ConfigDir,
		"PATH="+env.StubBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	second := readArgsLog(t, logDir, 1)
	if got := flagValue(second, "--resume"); got != id {
		t.Errorf("second run --resume = %q, want %q (stack-scoped session should persist across sibling branches)", got, id)
	}
}

// TestAgentFeature_PromptIncludesStackRenameInstruction is the
// integration-level pin for the "name the stack with ≤5 words" rule baked
// into the feature-mode prompt. Without this, a regression that drops the
// rename instructions during template rendering would slip through unit
// tests that only assert on the raw template body.
func TestAgentFeature_PromptIncludesStackRenameInstruction(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	logDir := filepath.Join(env.TmpDir, "claude_args")
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), agentStubScript(logDir))
	writeExecutable(t, filepath.Join(env.StubBinDir, "ezs-mcp"), "#!/bin/sh\nexit 0\n")

	if out, err := runEzsStubbed(t, env, "agent", "feature", "Add JWT auth"); err != nil {
		t.Fatalf("agent feature run: %v\n%s", err, out)
	}

	// The prompt arg is the very last token in argv. agentStubScript writes
	// each arg on its own line, so the last line of invocation 0's file is
	// the prompt body.
	argv := readArgsLog(t, logDir, 0)
	if len(argv) == 0 {
		t.Fatal("stub claude was not invoked with any args")
	}
	prompt := argv[len(argv)-1]

	for _, want := range []string{
		"Add JWT auth",                       // feature description survives template rendering
		"ezs stack rename",                   // the rename instruction is in the rendered prompt
		"≤5 words",                           // length budget is preserved
		"FIRST BRANCH ONLY",                  // timing constraint is preserved
		"derived from the FEATURE_DESCRIPTION", // name is anchored to the description
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("feature prompt missing %q (first 400 chars):\n%s", want, truncate(prompt, 400))
		}
	}
}

// TestAgentLs_EmptyRepo_JSONReturnsEmptyArray pins the JSON contract for
// empty results: `[]`, not null and not an error. Scripts piping through
// jq need a stable empty-list shape.
func TestAgentLs_EmptyRepo_JSONReturnsEmptyArray(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzsStubbed(t, env, "agent", "ls", "--json")
	if err != nil {
		t.Fatalf("agent ls --json on empty repo: %v\n%s", err, out)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed != "[]" {
		t.Errorf("expected empty JSON array, got %q", trimmed)
	}
}

// TestAgentLs_FilterEmptyMessages covers the message-wording edges for the
// new filter flags: each filter must surface an empty-list message that
// names the active filter so users on, say, `--feature` against a
// feature-less repo don't see "no sessions in <path>" and assume the tool
// is broken. The single-repo plain `agent ls` message stays as it was.
func TestAgentLs_FilterEmptyMessages(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Need a stack to be on so --branch / --stack don't error out on the
	// "current branch isn't tracked" gate before we test the empty path.
	CreateBranchWithCommit(t, env, "feat-empty", "main")

	cases := []struct {
		flag string
		want string
	}{
		{"--feature", "in feature mode"},
	}
	for _, c := range cases {
		out, err := runEzsStubbed(t, env, "agent", "ls", c.flag)
		if err != nil {
			t.Fatalf("agent ls %s on empty repo: %v\n%s", c.flag, err, out)
		}
		if !strings.Contains(string(out), c.want) {
			t.Errorf("agent ls %s empty message missing %q; got:\n%s", c.flag, c.want, out)
		}
	}
}

// TestAgentSession_PersistedToStacksJSON verifies the on-disk shape: after a
// run, $EZSTACK_HOME/stacks.json contains an `agent_session_id` field on the
// stack (or branch cache). We don't constrain the JSON path beyond that — if
// the field appears anywhere, the persistence layer is wired up correctly.
func TestAgentSession_PersistedToStacksJSON(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feat-persisted", "main")

	stub := `#!/bin/sh
if [ "$1" = "mcp" ]; then exit 0; fi
exit 0
`
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), stub)

	if out, err := runEzsStubbed(t, env, "agent", "--branch", "feat-persisted"); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(env.ConfigDir, "stacks.json"))
	if err != nil {
		t.Fatalf("read stacks.json: %v", err)
	}
	if !strings.Contains(string(data), "agent_session_id") {
		t.Errorf("stacks.json missing agent_session_id field; content:\n%s", string(data))
	}

	// Sanity: the JSON should still parse (we didn't break the schema).
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Errorf("stacks.json is no longer valid JSON: %v", err)
	}
}

// ── helpers ────────────────────────────────────────────────────────────────────

// readEnvLogLine returns the n-th line written to a stub's env log file.
// Used by tests that inspect what environment a stub child saw — each stub
// invocation appends one newline-terminated record. n is 0-based.
func readEnvLogLine(t *testing.T, path string, n int) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read env log %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if n >= len(lines) {
		t.Fatalf("env log only has %d entries, asked for index %d (path: %s, content: %q)", len(lines), n, path, string(data))
	}
	return lines[n]
}

// flagValue returns the value following `flag` in argv, or "" if absent.
// Treats "--flag value" form; doesn't handle "--flag=value" since claude
// doesn't use that style and our injection always uses two-token form.
func flagValue(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// flagValueContains returns true if argv contains the flag and its value
// equals or contains substr (substring match for forgiving comparisons).
func flagValueContains(argv []string, flag, substr string) bool {
	v := flagValue(argv, flag)
	return v != "" && strings.Contains(v, substr)
}

// containsSeq returns true if argv contains every element of seq, in order
// (not necessarily contiguous).
func containsSeq(argv, seq []string) bool {
	if len(seq) == 0 {
		return true
	}
	idx := 0
	for _, a := range argv {
		if a == seq[idx] {
			idx++
			if idx == len(seq) {
				return true
			}
		}
	}
	return false
}
