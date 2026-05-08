package commands

import (
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

// ── agentCLIBase / isClaudeFamily ──────────────────────────────────────────────

func TestAgentCLIBase(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"   ":                    "",
		"claude":                 "claude",
		"  claude  ":             "claude",
		"claude --debug":         "claude",
		"/usr/local/bin/claude":  "claude",
		"./claude --print":       "claude",
		"/opt/Claude.exe":        "claude", // .exe stripped, lowercased
		"claude-code":            "claude-code",
		"claude-code-1.0":        "claude-code-1",
		"~/bin/CLAUDE":           "claude",
		"my-other-agent":         "my-other-agent",
		"/usr/local/bin/aider":   "aider",
		"C:/bin/Cursor.cmd argv": "cursor",
	}
	for in, want := range cases {
		if got := agentCLIBase(in); got != want {
			t.Errorf("agentCLIBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsClaudeFamily(t *testing.T) {
	cases := map[string]bool{
		"claude":                      true,
		"claude --model opus":         true,
		"/usr/local/bin/claude":       true,
		"claude-code":                 true,
		"claude_dev":                  true,
		"my-claude":                   false, // doesn't start with claude
		"aider":                       false,
		"cursor":                      false,
		"/usr/bin/something-claude-y": false,
		"":                            false,
	}
	for in, want := range cases {
		if got := isClaudeFamily(in); got != want {
			t.Errorf("isClaudeFamily(%q) = %v, want %v", in, got, want)
		}
	}
}

// ── sanitizeSessionLabel / sessionDisplayName ──────────────────────────────────

func TestSanitizeSessionLabel(t *testing.T) {
	cases := map[string]string{
		"":                         "session",
		"feature-auth":             "feature-auth",
		"my stack":                 "my-stack",
		"a/b/c":                    "a-b-c",
		"foo:bar":                  "foo-bar",
		"   ":                      "session", // trim leading/trailing hyphens, fallback
		"hello!!!world":            "hello-world",
		"v1.2.3":                   "v1.2.3",
		"a.b_c-d":                  "a.b_c-d",
		"!!!":                      "session",
		"branch with spaces and 🎉": "branch-with-spaces-and",
	}
	for in, want := range cases {
		if got := sanitizeSessionLabel(in); got != want {
			t.Errorf("sanitizeSessionLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSessionDisplayName(t *testing.T) {
	if got := sessionDisplayName("feature-auth", scopeBranch); got != "_ezstack-feature-auth" {
		t.Errorf("sessionDisplayName(feature-auth) = %q", got)
	}
	if got := sessionDisplayName("my stack", scopeStack); got != "_ezstack-my-stack" {
		t.Errorf("sessionDisplayName(my stack) = %q", got)
	}
}

func TestOneShotFeatureLabel(t *testing.T) {
	cases := []struct {
		desc string
		uuid string
		want string
	}{
		// Typical: short description plus 6-char UUID suffix.
		{"Add user auth with JWT", "7a3b9f12-4567-89ab-cdef-1234567890ab", "add-user-auth-with-jwt-7a3b9f"},
		// Empty description falls back to suffix only.
		{"", "abcdef0123456789", "abcdef"},
		// Pure-symbol description sanitizes to nothing → suffix only.
		{"!!!", "abcdef0123456789", "abcdef"},
		// Long description gets truncated at 32 sanitized chars before the suffix joins.
		{"This is a very long description that goes on and on", "abcdef0123456789",
			"this-is-a-very-long-description-abcdef"},
		// Empty UUID degrades gracefully to description only.
		{"feature x", "", "feature-x"},
	}
	for _, c := range cases {
		if got := oneShotFeatureLabel(c.desc, c.uuid); got != c.want {
			t.Errorf("oneShotFeatureLabel(%q, %q) = %q, want %q", c.desc, c.uuid, got, c.want)
		}
	}
}

// TestResolveFeatureSession_OneShotLabelEmbedsDescription pins the bug fix for
// parallel feature sessions: two `ezs agent feature` runs with different
// descriptions must produce distinct display labels even when no stack is bound.
func TestResolveFeatureSession_OneShotLabelEmbedsDescription(t *testing.T) {
	withStubSessionID(t, "abcdef1234567890")

	plan := resolveFeatureSession("/repo", "claude", nil, false, "Add user authentication")
	if plan == nil || plan.injection == nil {
		t.Fatal("expected non-nil plan")
	}
	wantLabel := "_ezstack-feature-add-user-authentication-abcdef"
	if !containsString(plan.injection.Args, wantLabel) {
		t.Errorf("expected display label %q in args; got %v", wantLabel, plan.injection.Args)
	}
}

// ── buildAgentSessionArgs (claude family) ──────────────────────────────────────

func TestBuildAgentSessionArgs_ClaudeFreshWhenNoStored(t *testing.T) {
	withStubSessionID(t, "stub-uuid-1")

	inj := buildAgentSessionArgs("claude", "", "_ezstack-foo", false)

	if !inj.Fresh {
		t.Error("expected Fresh=true when no stored session ID")
	}
	if inj.SessionID != "stub-uuid-1" {
		t.Errorf("SessionID = %q, want stub-uuid-1", inj.SessionID)
	}
	if !inj.IncludePrompt {
		t.Error("fresh session should include the prompt in argv")
	}
	wantArgs := []string{"--session-id", "stub-uuid-1", "--name", "_ezstack-foo"}
	if !equalStrings(inj.Args, wantArgs) {
		t.Errorf("Args = %v, want %v", inj.Args, wantArgs)
	}
}

func TestBuildAgentSessionArgs_ClaudeResumeWhenStored(t *testing.T) {
	withStubSessionID(t, "should-not-be-used")

	inj := buildAgentSessionArgs("claude", "existing-id", "_ezstack-bar", false)

	if inj.Fresh {
		t.Error("expected Fresh=false when reusing stored session")
	}
	if inj.SessionID != "existing-id" {
		t.Errorf("SessionID = %q, want existing-id", inj.SessionID)
	}
	if inj.IncludePrompt {
		t.Error("resume must NOT append the prompt — claude reopens the conversation interactively")
	}
	wantArgs := []string{"--resume", "existing-id", "--name", "_ezstack-bar"}
	if !equalStrings(inj.Args, wantArgs) {
		t.Errorf("Args = %v, want %v", inj.Args, wantArgs)
	}
}

func TestBuildAgentSessionArgs_ClaudeForceFreshIgnoresStored(t *testing.T) {
	withStubSessionID(t, "fresh-uuid")

	inj := buildAgentSessionArgs("claude", "existing-id", "_ezstack-baz", true)

	if !inj.Fresh {
		t.Error("--no-resume must override a stored session ID")
	}
	if inj.SessionID != "fresh-uuid" {
		t.Errorf("SessionID = %q, want fresh-uuid", inj.SessionID)
	}
	if !inj.IncludePrompt {
		t.Error("forced-fresh session should include the prompt in argv")
	}
}

// ── buildAgentSessionArgs (non-claude) ─────────────────────────────────────────
//
// For agents whose flag schema we don't know, ezs still mints/persists a
// UUID and exposes it via EZS_AGENT_SESSION_ID, but never injects CLI flags.
// IncludePrompt stays true in every mode (we have no resume semantics to
// suppress the prompt for) so wrappers always see the rendered prompt.

func TestBuildAgentSessionArgs_NonClaudeFreshOmitsArgs(t *testing.T) {
	withStubSessionID(t, "uuid-non-claude-fresh")

	inj := buildAgentSessionArgs("aider --model gpt-4", "", "_ezstack-foo", false)

	if !inj.Fresh {
		t.Error("expected Fresh=true with no stored ID")
	}
	if inj.SessionID != "uuid-non-claude-fresh" {
		t.Errorf("SessionID = %q, want uuid-non-claude-fresh", inj.SessionID)
	}
	if len(inj.Args) != 0 {
		t.Errorf("non-claude must not get CLI flags injected; got Args=%v", inj.Args)
	}
	if !inj.IncludePrompt {
		t.Error("non-claude fresh session must include the prompt — no resume semantics to skip it")
	}
}

func TestBuildAgentSessionArgs_NonClaudeResumeOmitsArgs(t *testing.T) {
	withStubSessionID(t, "should-not-be-used")

	inj := buildAgentSessionArgs("aider", "stored-id", "_ezstack-bar", false)

	if inj.Fresh {
		t.Error("expected Fresh=false when reusing stored ID")
	}
	if inj.SessionID != "stored-id" {
		t.Errorf("SessionID = %q, want stored-id", inj.SessionID)
	}
	if len(inj.Args) != 0 {
		t.Errorf("non-claude must not get CLI flags injected on resume; got Args=%v", inj.Args)
	}
	if !inj.IncludePrompt {
		t.Error("non-claude must always include the prompt — wrappers may not auto-reload state")
	}
}

func TestBuildAgentSessionArgs_NonClaudeForceFreshMintsNew(t *testing.T) {
	withStubSessionID(t, "minted-fresh")

	inj := buildAgentSessionArgs("cursor", "stored-id", "_ezstack-baz", true)

	if !inj.Fresh {
		t.Error("--no-resume must mint a new ID for non-claude too")
	}
	if inj.SessionID != "minted-fresh" {
		t.Errorf("SessionID = %q, want minted-fresh", inj.SessionID)
	}
	if len(inj.Args) != 0 {
		t.Errorf("non-claude must not get CLI flags injected; got Args=%v", inj.Args)
	}
}

// ── resolveFeatureSession ──────────────────────────────────────────────────────
//
// Feature mode has two shapes: one-shot (no existing stack — mints a UUID
// but has nowhere to persist it) and existing-stack (binds the session to
// the supplied stack hash). Both shapes must work for any agent.

func TestResolveFeatureSession_OneShotMintsUUID(t *testing.T) {
	withStubSessionID(t, "feature-oneshot-uuid")

	plan := resolveFeatureSession("/repo", "claude", nil, false, "build a thing")
	if plan == nil || plan.injection == nil {
		t.Fatal("expected non-nil plan for feature one-shot mode")
	}
	if plan.injection.SessionID != "feature-oneshot-uuid" {
		t.Errorf("SessionID = %q, want feature-oneshot-uuid", plan.injection.SessionID)
	}
	if !plan.injection.Fresh {
		t.Error("one-shot must always be Fresh — no stored ID to resume from")
	}
	// Persist for one-shot is a no-op (no scope to attach to). It must not
	// error when invoked, even though there's nothing for it to do.
	if err := plan.persist("any-id"); err != nil {
		t.Errorf("one-shot persist should be a no-op, got err=%v", err)
	}
}

func TestResolveFeatureSession_OneShotNonClaudeOmitsArgs(t *testing.T) {
	withStubSessionID(t, "non-claude-oneshot")

	plan := resolveFeatureSession("/repo", "aider", nil, false, "build a thing")
	if plan == nil || plan.injection == nil {
		t.Fatal("expected non-nil plan for feature one-shot mode (non-claude)")
	}
	if len(plan.injection.Args) != 0 {
		t.Errorf("non-claude must not get CLI flags injected; got %v", plan.injection.Args)
	}
	if !plan.injection.IncludePrompt {
		t.Error("non-claude feature one-shot must include the prompt")
	}
}

func TestResolveFeatureSession_ExistingStackResumes(t *testing.T) {
	withStubSessionID(t, "should-not-be-used")
	stack := &config.Stack{Hash: "abc1234", Name: "my-feature", AgentSessionID: "stored-feature-id"}

	plan := resolveFeatureSession("/repo", "claude", stack, false, "")
	if plan == nil || plan.injection == nil {
		t.Fatal("expected non-nil plan for existing-stack feature mode")
	}
	if plan.injection.Fresh {
		t.Error("existing stack with stored ID must resume, not mint fresh")
	}
	if plan.injection.SessionID != "stored-feature-id" {
		t.Errorf("SessionID = %q, want stored-feature-id", plan.injection.SessionID)
	}
	// The display label must include the stack identifier so it's
	// distinguishable in claude's /resume picker.
	if !containsString(plan.injection.Args, "_ezstack-feature-my-feature") {
		t.Errorf("expected display name '_ezstack-feature-my-feature' in args; got %v", plan.injection.Args)
	}
}

func TestResolveFeatureSession_ExistingStackForceFreshMints(t *testing.T) {
	withStubSessionID(t, "minted-fresh-feature")
	stack := &config.Stack{Hash: "abc1234", Name: "my-feature", AgentSessionID: "stored-feature-id"}

	plan := resolveFeatureSession("/repo", "claude", stack, true, "")
	if !plan.injection.Fresh {
		t.Error("--no-resume must mint a new ID even for existing-stack feature")
	}
	if plan.injection.SessionID != "minted-fresh-feature" {
		t.Errorf("SessionID = %q, want minted-fresh-feature", plan.injection.SessionID)
	}
}

// ── sticky-rename: resume path honors /rename done inside Claude ──────────
//
// When a user runs `/rename foo` inside Claude, the latest agent-name event
// in the session journal is "foo". On the next `ezs agent`, the resume args
// must pass `--name foo` (not the deterministic `_ezstack-<id>` label) so
// the rename survives the relaunch instead of being clobbered. Fresh
// launches and --no-resume keep the deterministic label.

func TestResolveWorkSession_ResumePrefersLiveRename(t *testing.T) {
	root := withClaudeProjectsDir(t)
	storedID := "stack-uuid"
	writeJSONL(t, root, "-Users-test-repo", storedID, []string{
		nameEvent(storedID, "_ezstack-launch-label"),
		nameEvent(storedID, "renamed-by-user"),
	})
	stack := &config.Stack{Hash: "abc1234", Name: "my-stack", AgentSessionID: storedID}

	plan := resolveWorkSession("/repo", "claude", stack, "", false, false)
	if plan == nil || plan.injection == nil {
		t.Fatal("expected non-nil plan")
	}
	if plan.injection.Fresh {
		t.Error("resuming with stored ID must not be Fresh")
	}
	if !containsString(plan.injection.Args, "renamed-by-user") {
		t.Errorf("expected resume to carry user's rename; got args=%v", plan.injection.Args)
	}
	if containsString(plan.injection.Args, "_ezstack-my-stack") {
		t.Errorf("rename should override deterministic label; got args=%v", plan.injection.Args)
	}
}

func TestResolveWorkSession_ResumeNoLiveNameKeepsFallback(t *testing.T) {
	// Resume against a stored ID whose journal has no agent-name event
	// (e.g. corrupted/truncated file): we must fall back to the
	// deterministic label rather than emit `--name ""`.
	root := withClaudeProjectsDir(t)
	storedID := "stack-uuid-no-name"
	writeJSONL(t, root, "-Users-test-repo", storedID, []string{
		`{"type":"user","message":"hi"}`,
	})
	stack := &config.Stack{Hash: "abc1234", Name: "my-stack", AgentSessionID: storedID}

	plan := resolveWorkSession("/repo", "claude", stack, "", false, false)
	if !containsString(plan.injection.Args, "_ezstack-my-stack") {
		t.Errorf("expected fallback label '_ezstack-my-stack' in args; got %v", plan.injection.Args)
	}
}

func TestResolveWorkSession_ForceFreshIgnoresLiveRename(t *testing.T) {
	// --no-resume mints a new UUID — any rename on the *previous* session
	// belongs to that prior conversation and must not leak into the fresh
	// one.
	root := withClaudeProjectsDir(t)
	storedID := "stack-uuid"
	writeJSONL(t, root, "-Users-test-repo", storedID, []string{
		nameEvent(storedID, "old-rename"),
	})
	withStubSessionID(t, "minted-fresh-uuid")
	stack := &config.Stack{Hash: "abc1234", Name: "my-stack", AgentSessionID: storedID}

	plan := resolveWorkSession("/repo", "claude", stack, "", false, true)
	if !plan.injection.Fresh {
		t.Error("forceFresh must mint fresh")
	}
	if !containsString(plan.injection.Args, "_ezstack-my-stack") {
		t.Errorf("forceFresh must use deterministic label; got args=%v", plan.injection.Args)
	}
	if containsString(plan.injection.Args, "old-rename") {
		t.Errorf("forceFresh must not surface prior rename; got args=%v", plan.injection.Args)
	}
}

func TestResolveFeatureSession_ResumePrefersLiveRename(t *testing.T) {
	root := withClaudeProjectsDir(t)
	storedID := "stored-feature-id"
	writeJSONL(t, root, "-Users-test-repo", storedID, []string{
		nameEvent(storedID, "feature-renamed"),
	})
	stack := &config.Stack{Hash: "abc1234", Name: "my-feature", AgentSessionID: storedID}

	plan := resolveFeatureSession("/repo", "claude", stack, false)
	if !containsString(plan.injection.Args, "feature-renamed") {
		t.Errorf("expected feature resume to carry user's rename; got %v", plan.injection.Args)
	}
}

// ── splitAgentExtras ───────────────────────────────────────────────────────────

func TestSplitAgentExtras(t *testing.T) {
	cases := []struct {
		name      string
		in        []string
		wantArgs  []string
		wantExtra []string
	}{
		{"no separator", []string{"-s", "foo", "--dry-run"}, []string{"-s", "foo", "--dry-run"}, nil},
		{"only separator", []string{"--"}, []string{}, []string{}},
		{"separator with extras", []string{"-s", "foo", "--", "--resume", "abc"},
			[]string{"-s", "foo"}, []string{"--resume", "abc"}},
		{"separator at start", []string{"--", "--debug"}, []string{}, []string{"--debug"}},
		{"empty", nil, nil, nil},
	}
	for _, c := range cases {
		gotArgs, gotExtra := splitAgentExtras(c.in)
		if !equalStrings(gotArgs, c.wantArgs) {
			t.Errorf("%s: agent args = %v, want %v", c.name, gotArgs, c.wantArgs)
		}
		if !equalStrings(gotExtra, c.wantExtra) {
			t.Errorf("%s: extras = %v, want %v", c.name, gotExtra, c.wantExtra)
		}
	}
}

// ── agentProcessEnv with session ID ────────────────────────────────────────────

func TestAgentProcessEnv_InjectsSessionID(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "HOME=/home/test"}

	got := agentProcessEnv(parent, false, "abc-123")

	wantPair := agentSessionIDEnv + "=abc-123"
	if !containsString(got, wantPair) {
		t.Errorf("agent env missing %s; got %v", wantPair, got)
	}
}

func TestAgentProcessEnv_StripsInheritedSessionWhenEmpty(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		agentSessionIDEnv + "=stale-id-from-outer-agent",
	}

	got := agentProcessEnv(parent, false, "")

	for _, kv := range got {
		if strings.HasPrefix(kv, agentSessionIDEnv+"=") {
			t.Errorf("expected stale %s to be stripped when sessionID is empty; got %v", agentSessionIDEnv, got)
		}
	}
}

func TestAgentProcessEnv_DeduplicatesSessionID(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		agentSessionIDEnv + "=stale",
	}

	got := agentProcessEnv(parent, false, "fresh")

	count := 0
	for _, kv := range got {
		if strings.HasPrefix(kv, agentSessionIDEnv+"=") {
			count++
			if kv != agentSessionIDEnv+"=fresh" {
				t.Errorf("expected session ID = fresh, got %q", kv)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one %s entry, got %d", agentSessionIDEnv, count)
	}
}

// ── sessionLogSuffix / printSessionDryRun (smoke) ─────────────────────────────

func TestSessionLogSuffix_Empty(t *testing.T) {
	if got := sessionLogSuffix(nil); got != "" {
		t.Errorf("nil plan should produce empty suffix, got %q", got)
	}
	plan := &agentSessionPlan{} // injection nil
	if got := sessionLogSuffix(plan); got != "" {
		t.Errorf("plan with nil injection should produce empty suffix, got %q", got)
	}
}

func TestSessionLogSuffix_FreshAndResume(t *testing.T) {
	fresh := &agentSessionPlan{injection: &agentSessionInjection{SessionID: "abcdef0123", Fresh: true}}
	if got := sessionLogSuffix(fresh); !strings.Contains(got, "new session") || !strings.Contains(got, "abcdef01") {
		t.Errorf("fresh suffix = %q", got)
	}

	resume := &agentSessionPlan{injection: &agentSessionInjection{SessionID: "abcdef0123", Fresh: false}}
	if got := sessionLogSuffix(resume); !strings.Contains(got, "resuming") || !strings.Contains(got, "abcdef01") {
		t.Errorf("resume suffix = %q", got)
	}
}

// ── helpers ────────────────────────────────────────────────────────────────────

func withStubSessionID(t *testing.T, id string) {
	t.Helper()
	prev := newSessionID
	newSessionID = func() string { return id }
	t.Cleanup(func() { newSessionID = prev })
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(slice []string, want string) bool {
	for _, s := range slice {
		if s == want {
			return true
		}
	}
	return false
}
