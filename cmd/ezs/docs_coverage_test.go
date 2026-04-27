package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestEveryTopLevelCommandIsDocumented is a drift gate: every entry
// in `topLevelCommands` (the canonical user-visible CLI surface that
// drives tab-completion) MUST appear as an H3 heading inside the
// `## Commands` section of DOCUMENTATION.md.
//
// This catches the failure mode PR #21 originally hit: `ezs upgrade`
// was added to topLevelCommands and `### ezs upgrade` was written,
// but the heading was placed *after* `## Exit codes` instead of
// inside Commands — so the auto-generated sidebar rendered the link
// in the wrong section and users couldn't discover the new command
// through the docs nav. Sister to TestCommandHeadingsAlphabetical
// in cmd/docsgen, which enforces ordering.
//
// Combined headings of the form "### `ezs commit` / `ezs amend`" count
// as documenting both `commit` and `amend`.
func TestEveryTopLevelCommandIsDocumented(t *testing.T) {
	const docPath = "../../DOCUMENTATION.md"
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	text := string(data)

	commandsStart := strings.Index(text, "\n## Commands\n")
	if commandsStart < 0 {
		t.Fatal("no `## Commands` H2 found in DOCUMENTATION.md")
	}
	body := text[commandsStart+1:]
	if i := strings.Index(body[1:], "\n## "); i >= 0 {
		body = body[:i+1]
	}

	// Walk every line that starts an H3 and harvest every backticked
	// `ezs <name>` reference on it, so a combined heading covers all
	// of its comma/slash-listed forms.
	headingLineRe := regexp.MustCompile("(?m)^### .*$")
	refRe := regexp.MustCompile("`ezs ([^`]+)`")
	documented := make(map[string]bool)
	for _, line := range headingLineRe.FindAllString(body, -1) {
		for _, m := range refRe.FindAllStringSubmatch(line, -1) {
			cmd := strings.SplitN(m[1], " ", 2)[0]
			documented[cmd] = true
		}
	}

	for _, cmd := range topLevelCommands {
		if !documented[cmd] {
			t.Errorf("topLevelCommands lists %q but DOCUMENTATION.md `## Commands` has no `### \\`ezs %s\\`` heading — either move the heading into the Commands section or add one.", cmd, cmd)
		}
	}
}
