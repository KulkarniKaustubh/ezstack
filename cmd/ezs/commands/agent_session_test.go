package commands

import (
	"strings"
	"testing"
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
		"claude":                       true,
		"claude --model opus":          true,
		"/usr/local/bin/claude":        true,
		"claude-code":                  true,
		"claude_dev":                   true,
		"my-claude":                    false, // doesn't start with claude
		"aider":                        false,
		"cursor":                       false,
		"/usr/bin/something-claude-y":  false,
		"":                             false,
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
		"":                  "session",
		"feature-auth":      "feature-auth",
		"my stack":          "my-stack",
		"a/b/c":             "a-b-c",
		"foo:bar":           "foo-bar",
		"   ":               "session", // trim leading/trailing hyphens, fallback
		"hello!!!world":     "hello-world",
		"v1.2.3":            "v1.2.3",
		"a.b_c-d":           "a.b_c-d",
		"!!!":               "session",
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

// ── buildClaudeSessionArgs ─────────────────────────────────────────────────────

func TestBuildClaudeSessionArgs_FreshWhenNoStored(t *testing.T) {
	withStubSessionID(t, "stub-uuid-1")

	inj := buildClaudeSessionArgs("", "_ezstack-foo", false)

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

func TestBuildClaudeSessionArgs_ResumeWhenStored(t *testing.T) {
	withStubSessionID(t, "should-not-be-used")

	inj := buildClaudeSessionArgs("existing-id", "_ezstack-bar", false)

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

func TestBuildClaudeSessionArgs_ForceFreshIgnoresStored(t *testing.T) {
	withStubSessionID(t, "fresh-uuid")

	inj := buildClaudeSessionArgs("existing-id", "_ezstack-baz", true)

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
	fresh := &agentSessionPlan{injection: &claudeSessionInjection{SessionID: "abcdef0123", Fresh: true}}
	if got := sessionLogSuffix(fresh); !strings.Contains(got, "new session") || !strings.Contains(got, "abcdef01") {
		t.Errorf("fresh suffix = %q", got)
	}

	resume := &agentSessionPlan{injection: &claudeSessionInjection{SessionID: "abcdef0123", Fresh: false}}
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
