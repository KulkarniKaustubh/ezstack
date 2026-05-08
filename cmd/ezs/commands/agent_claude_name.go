package commands

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
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

// claudeAgentNameMaxBytes bounds how much of a session journal we'll read
// looking for `agent-name` events. The launch event is written at the top
// of the file, but `/rename` events are appended whenever the user renames
// — including deep into long-running sessions. A line-count cap would
// silently lose a late rename and re-assert the launch label on resume,
// defeating the whole point of this lookup; a byte cap keeps the worst-
// case cost of `agent ls` bounded.
//
// 256 MiB is well past any session size we've seen in the wild — heavy
// sessions land in the low-MB range — and reading that much off a local
// SSD is sub-second. We read forward from the start; a tail-first scan
// would be cheaper for the rename case but adds enough JSONL chunking
// complexity that the simpler bounded forward read wins for now. If a
// journal grows past this cap we degrade to "use whatever we found in
// the first 256 MiB" — keeps the launch label visible at minimum.
const claudeAgentNameMaxBytes = 256 * 1024 * 1024

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
// We use a cheap substring filter on the raw bytes before decoding because
// the vast majority of lines in a session journal are tool calls / messages
// with no `type` match — full json.Unmarshal on every line is wasteful.
//
// Uses bufio.Reader (not bufio.Scanner) because Scanner returns false on
// any line that exceeds its buffer size, with the error surfaced only via
// scanner.Err(). A single oversized tool-output line in a journal would
// silently truncate the scan and lose every `agent-name` event after it,
// defeating the whole point of the lookup. Reader.ReadBytes('\n') has no
// such cap.
func scanLastAgentName(path, sessionID string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 64*1024)
	needle := []byte(`"agent-name"`)
	last := ""
	bytesSeen := 0
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			bytesSeen += len(line)
			if bytesSeen > claudeAgentNameMaxBytes {
				break
			}
			if bytes.Contains(line, needle) {
				var ev agentNameEvent
				if jerr := json.Unmarshal(bytes.TrimRight(line, "\n"), &ev); jerr == nil {
					if ev.Type == "agent-name" && ev.SessionID == sessionID {
						last = ev.AgentName
					}
				}
			}
		}
		if err != nil {
			// io.EOF is expected at end of file; any other error stops the
			// scan early but we still return whatever `last` we found.
			if !errors.Is(err, io.EOF) {
				break
			}
			break
		}
	}
	return last
}
