package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSyncFlagDriftBetweenHelpAndDocs is a lightweight drift gate: it parses
// the OPTIONS block from cmd/ezs/commands/sync.go's help text and the same
// block from DOCUMENTATION.md, and asserts the set of `--flag` names is
// identical. Catches the common failure mode where a new flag gets added to
// one but not the other.
//
// Doesn't compare the descriptions verbatim — the help line is one-liner
// while the docs may add markdown — but flag presence is a strong-enough
// signal to catch the typical drift.
func TestSyncFlagDriftBetweenHelpAndDocs(t *testing.T) {
	repoRoot := findRepoRoot(t)

	helpFlags := extractFlagsFromBlock(t,
		filepath.Join(repoRoot, "cmd/ezs/commands/sync.go"),
		// Help text is inside fs.Usage's format string; bracketed by the
		// "OPTIONS" header (rendered via %s) and the "DESCRIPTION" header.
		`%sOPTIONS%s`,
		`%sDESCRIPTION%s`,
	)
	docFlags := extractDocSyncFlags(t, filepath.Join(repoRoot, "DOCUMENTATION.md"))

	// --help is a universal flag, not documented per-command. Drop from
	// the help-side set so it doesn't show as drift.
	delete(helpFlags, "--help")

	if !sameSet(helpFlags, docFlags) {
		t.Errorf("sync flag drift between help text and DOCUMENTATION.md\n  help only: %v\n  docs only: %v",
			diff(helpFlags, docFlags), diff(docFlags, helpFlags))
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found upward from cwd")
		}
		dir = parent
	}
}

// extractFlagsFromBlock reads `path`, finds the substring between the first
// occurrence of `start` and the next occurrence of `end` (after start), and
// returns every `--flag` (or `-x, --flag`) it finds in that region.
func extractFlagsFromBlock(t *testing.T, path, start, end string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := string(raw)
	si := strings.Index(content, start)
	if si == -1 {
		t.Fatalf("start marker %q not found in %s", start, path)
	}
	ei := strings.Index(content[si+len(start):], end)
	if ei == -1 {
		t.Fatalf("end marker %q not found after start in %s", end, path)
	}
	block := content[si : si+len(start)+ei]
	flagRe := regexp.MustCompile(`--[a-z][a-z0-9-]*`)
	matches := flagRe.FindAllString(block, -1)
	out := make(map[string]bool, len(matches))
	for _, m := range matches {
		out[m] = true
	}
	return out
}

// extractDocSyncFlags pulls the --flag set out of DOCUMENTATION.md's
// "ezs sync" Options block. The doc has multiple commands each with their
// own Options block, so we anchor on the section heading first.
func extractDocSyncFlags(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := string(raw)
	syncStart := strings.Index(content, "### `ezs sync`")
	if syncStart == -1 {
		t.Fatal("DOCUMENTATION.md missing `### `ezs sync`` heading")
	}
	rest := content[syncStart:]
	// Find "Options:" line within this section.
	optStart := strings.Index(rest, "Options:")
	if optStart == -1 {
		t.Fatal("`ezs sync` section missing `Options:` block")
	}
	// Scan from there to the nearest closing fence.
	optEnd := strings.Index(rest[optStart:], "```")
	if optEnd == -1 {
		t.Fatal("`Options:` block has no closing fence")
	}
	block := rest[optStart : optStart+optEnd]
	flagRe := regexp.MustCompile(`--[a-z][a-z0-9-]*`)
	matches := flagRe.FindAllString(block, -1)
	out := make(map[string]bool, len(matches))
	for _, m := range matches {
		out[m] = true
	}
	return out
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func diff(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	return out
}
