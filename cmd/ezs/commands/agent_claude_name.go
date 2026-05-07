package commands

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Claude Code persists per-session display names as `agent-name` events
// inside `~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl`. The first
// such event is written when the session is launched with `claude --name X`;
// `/rename` inside the session appends another. The journal is append-only,
// so the *last* `agent-name` event for the matching session ID is the name
// the user currently sees.
//
// We surface that name in `ezs agent ls` (instead of the deterministic
// `_ezstack-<identifier>` label) and reuse it on the next `ezs agent` resume,
// so a user who runs `/rename foo` doesn't get clobbered the next time.
//
// Using a glob keyed only on the session UUID lets us skip modeling Claude's
// cwd encoding (which collapses `/` and `.` into `-`, but we'd rather not
// duplicate the rules and silently drift). Session UUIDs are globally
// unique, so the glob resolves to at most one file in practice.

// claudeProjectsDir is the directory where Claude Code stores per-project
// session journals. Overridable for tests.
var claudeProjectsDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}()

// claudeAgentNameMaxLines bounds how far we'll scan into a session journal
// looking for `agent-name` events. The events are written at session start
// and on every `/rename`, so they cluster near the top. A few thousand lines
// is generous: it covers the launch event reliably and any rename done in a
// long-running session, while keeping the cost of an `agent ls` invocation
// linear in the number of tracked sessions, not in transcript length.
const claudeAgentNameMaxLines = 5000

// agentNameEvent matches the JSON shape Claude writes for `claude --name X`
// and `/rename Y`. We deliberately only decode the fields we need so future
// schema additions don't trip the parser.
type agentNameEvent struct {
	Type      string `json:"type"`
	AgentName string `json:"agentName"`
	SessionID string `json:"sessionId"`
}

// claudeAgentName returns the most recent name set on the Claude session
// identified by sessionID — either by `claude --name X` at launch or by a
// `/rename` invocation inside the session. Returns "" when:
//
//   - sessionID is empty
//   - the session journal file doesn't exist (non-claude agents, fresh UUID
//     that hasn't been launched yet, or a session that lives outside the
//     standard Claude data dir)
//   - the file exists but has no `agent-name` event for sessionID within
//     claudeAgentNameMaxLines
//
// Empty return is the signal to fall back to the deterministic
// `_ezstack-<identifier>` label, so non-claude agents and never-launched
// sessions render exactly as they did before.
func claudeAgentName(sessionID string) string {
	if sessionID == "" || claudeProjectsDir == "" {
		return ""
	}
	matches, err := filepath.Glob(filepath.Join(claudeProjectsDir, "*", sessionID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	// Multiple files for the same UUID would mean two sessions collided,
	// which UUIDv4 effectively rules out. If it ever happens, prefer the
	// most recently modified — that's the session the user is most likely
	// looking at.
	path := matches[0]
	if len(matches) > 1 {
		var newest os.FileInfo
		for _, m := range matches {
			fi, statErr := os.Stat(m)
			if statErr != nil {
				continue
			}
			if newest == nil || fi.ModTime().After(newest.ModTime()) {
				newest = fi
				path = m
			}
		}
	}
	return scanLastAgentName(path, sessionID)
}

// scanLastAgentName walks the JSONL file linewise, decoding only lines that
// look like `agent-name` events. Returns the agentName from the last event
// matching sessionID, or "" if none found within the scan budget.
//
// We use a cheap substring filter before decoding because the vast majority
// of lines in a session journal are tool calls / messages with no `type`
// match — full json.Unmarshal on every line is wasteful at 5k lines.
func scanLastAgentName(path, sessionID string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Lines in claude session journals can be very long (tool outputs,
	// diffs). Bump the buffer so we don't fail with bufio.ErrTooLong on a
	// fat line and miss any agent-name events that come after it.
	const maxLine = 4 * 1024 * 1024
	scanner.Buffer(make([]byte, 0, 64*1024), maxLine)

	last := ""
	count := 0
	for scanner.Scan() {
		count++
		if count > claudeAgentNameMaxLines {
			break
		}
		line := scanner.Bytes()
		if !strings.Contains(string(line), `"agent-name"`) {
			continue
		}
		var ev agentNameEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Type != "agent-name" || ev.SessionID != sessionID {
			continue
		}
		last = ev.AgentName
	}
	return last
}
