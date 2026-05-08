package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withClaudeProjectsDir swaps the package-level claudeProjectsDir for the
// duration of a test, restoring it on cleanup. Returns the temp dir.
func withClaudeProjectsDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	orig := claudeProjectsDir
	claudeProjectsDir = tmp
	t.Cleanup(func() { claudeProjectsDir = orig })
	return tmp
}

// writeJSONL writes lines to <dir>/<sid>.jsonl, creating dir as needed.
func writeJSONL(t *testing.T, root, project, sid string, lines []string) string {
	t.Helper()
	dir := filepath.Join(root, project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, sid+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func nameEvent(sid, name string) string {
	return fmt.Sprintf(`{"type":"agent-name","agentName":%q,"sessionId":%q}`, name, sid)
}

func TestClaudeAgentName_EmptyOnNoFile(t *testing.T) {
	withClaudeProjectsDir(t)
	if got := claudeAgentName("nonexistent-uuid"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestClaudeAgentName_EmptySessionIDReturnsEmpty(t *testing.T) {
	withClaudeProjectsDir(t)
	if got := claudeAgentName(""); got != "" {
		t.Errorf("expected empty for empty sessionID, got %q", got)
	}
}

func TestClaudeAgentName_ReturnsLaunchName(t *testing.T) {
	root := withClaudeProjectsDir(t)
	sid := "abc-123"
	writeJSONL(t, root, "-Users-test-repo", sid, []string{
		nameEvent(sid, "_ezstack-foo"),
	})
	if got := claudeAgentName(sid); got != "_ezstack-foo" {
		t.Errorf("expected launch name, got %q", got)
	}
}

func TestClaudeAgentName_LastEventWins(t *testing.T) {
	// Simulates: session launched as `_ezstack-foo`, user runs /rename
	// twice. The last agent-name event in the journal is the live name.
	root := withClaudeProjectsDir(t)
	sid := "abc-123"
	writeJSONL(t, root, "-Users-test-repo", sid, []string{
		nameEvent(sid, "_ezstack-foo"),
		`{"type":"user","message":"hi"}`, // unrelated event between
		nameEvent(sid, "renamed-once"),
		nameEvent(sid, "renamed-twice"),
	})
	if got := claudeAgentName(sid); got != "renamed-twice" {
		t.Errorf("expected last rename, got %q", got)
	}
}

func TestClaudeAgentName_IgnoresOtherSessionIDs(t *testing.T) {
	// Defends against a future where Claude inlines events from sub-agents
	// or sidechain sessions: only events whose sessionId matches ours
	// should influence the name we surface.
	root := withClaudeProjectsDir(t)
	sid := "ours"
	writeJSONL(t, root, "-Users-test-repo", sid, []string{
		nameEvent(sid, "ours-name"),
		nameEvent("someone-else", "not-ours"),
	})
	if got := claudeAgentName(sid); got != "ours-name" {
		t.Errorf("expected own name, got %q", got)
	}
}

func TestClaudeAgentName_ToleratesMalformedLines(t *testing.T) {
	root := withClaudeProjectsDir(t)
	sid := "abc"
	writeJSONL(t, root, "-Users-test-repo", sid, []string{
		`not json at all`,
		`{"type":"agent-name"`, // truncated
		nameEvent(sid, "good"),
		`{}`,
	})
	if got := claudeAgentName(sid); got != "good" {
		t.Errorf("expected good name despite garbage, got %q", got)
	}
}

func TestClaudeAgentName_FindsAcrossProjects(t *testing.T) {
	// Glob keyed on the UUID means we don't have to model Claude's cwd
	// encoding. A session moved or written under any project dir resolves.
	root := withClaudeProjectsDir(t)
	sid := "uuid-roams"
	writeJSONL(t, root, "-some-other-project", sid, []string{
		nameEvent(sid, "found"),
	})
	if got := claudeAgentName(sid); got != "found" {
		t.Errorf("expected glob to find file, got %q", got)
	}
}

// TestClaudeAgentName_PicksUpLateRename pins the failure mode where a long
// session does a `/rename` after a large amount of intervening journal
// activity. The scan must walk past the early launch event and surface the
// most recent rename, not whichever agent-name event happens to appear in
// the first chunk of the file. Without a budget that scales with file
// size, ezstack would silently re-assert the launch label on the next
// resume — defeating the entire feature for any non-trivial session.
func TestClaudeAgentName_PicksUpLateRename(t *testing.T) {
	root := withClaudeProjectsDir(t)
	sid := "long-uuid"
	lines := []string{
		nameEvent(sid, "_ezstack-launch-label"),
	}
	// Pad with enough filler events to exceed any reasonable
	// line-count cap. Each filler line is intentionally short so the
	// budget pressure is on line *count*, not bytes.
	filler := `{"type":"user","message":"x"}`
	for i := 0; i < 20000; i++ {
		lines = append(lines, filler)
	}
	lines = append(lines, nameEvent(sid, "renamed-late"))
	writeJSONL(t, root, "-Users-test-repo", sid, lines)

	if got := claudeAgentName(sid); got != "renamed-late" {
		t.Errorf("expected late rename to win, got %q", got)
	}
}

func TestClaudeAgentName_NoAgentNameEventReturnsEmpty(t *testing.T) {
	root := withClaudeProjectsDir(t)
	sid := "abc"
	writeJSONL(t, root, "-Users-test-repo", sid, []string{
		`{"type":"user","message":"hi"}`,
		`{"type":"assistant","message":"hello"}`,
	})
	if got := claudeAgentName(sid); got != "" {
		t.Errorf("expected empty when no agent-name event, got %q", got)
	}
}

func TestPreferLiveAgentName_ReturnsLiveOnResume(t *testing.T) {
	root := withClaudeProjectsDir(t)
	sid := "stored-uuid"
	writeJSONL(t, root, "-Users-test-repo", sid, []string{
		nameEvent(sid, "user-renamed"),
	})
	got := preferLiveAgentName(sid, false, "_ezstack-fallback")
	if got != "user-renamed" {
		t.Errorf("expected live name on resume, got %q", got)
	}
}

func TestPreferLiveAgentName_FallsBackOnFreshSession(t *testing.T) {
	// forceFresh=true means the user passed --no-resume — the live name
	// from the prior session is no longer relevant.
	root := withClaudeProjectsDir(t)
	sid := "stored-uuid"
	writeJSONL(t, root, "-Users-test-repo", sid, []string{
		nameEvent(sid, "old-rename"),
	})
	got := preferLiveAgentName(sid, true, "_ezstack-fallback")
	if got != "_ezstack-fallback" {
		t.Errorf("expected fallback on forceFresh, got %q", got)
	}
}

func TestPreferLiveAgentName_FallsBackOnEmptyStoredID(t *testing.T) {
	withClaudeProjectsDir(t)
	got := preferLiveAgentName("", false, "_ezstack-fallback")
	if got != "_ezstack-fallback" {
		t.Errorf("expected fallback for empty storedID, got %q", got)
	}
}

func TestPreferLiveAgentName_FallsBackWhenJournalAbsent(t *testing.T) {
	// Non-claude agents don't write Claude's session journal; the helper
	// should silently fall back to the deterministic label.
	withClaudeProjectsDir(t)
	got := preferLiveAgentName("never-launched-uuid", false, "_ezstack-fallback")
	if got != "_ezstack-fallback" {
		t.Errorf("expected fallback when journal missing, got %q", got)
	}
}

func TestResolveDisplayName_PrefersLiveOverFallback(t *testing.T) {
	// `ezs agent ls` integration: a stack/branch row's DisplayName should
	// reflect a /rename done inside Claude, not the deterministic
	// `_ezstack-<...>` label produced at launch time. Without this, a user
	// who renames a session sees a stale label every time they run
	// `agent ls`.
	root := withClaudeProjectsDir(t)
	sid := "stack-uuid"
	writeJSONL(t, root, "-Users-test-repo", sid, []string{
		nameEvent(sid, "_ezstack-original"),
		nameEvent(sid, "user-renamed-this"),
	})
	got := resolveDisplayName(sid, "_ezstack-fallback")
	if got != "user-renamed-this" {
		t.Errorf("expected live rename, got %q", got)
	}
}

func TestResolveDisplayName_FallsBackForNonClaudeOrFresh(t *testing.T) {
	withClaudeProjectsDir(t)
	got := resolveDisplayName("no-such-uuid", "_ezstack-fallback")
	if got != "_ezstack-fallback" {
		t.Errorf("expected fallback, got %q", got)
	}
}
