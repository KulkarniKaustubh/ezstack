package main

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestCommandHeadingsAlphabetical is a drift gate: the H3 command
// headings inside the `## Commands` section of DOCUMENTATION.md MUST
// be in alphabetical order, because:
//
//   - the auto-generated sidebar in docs/documentation.html mirrors
//     source order, so any reordering in the markdown immediately
//     shows up in the rendered website nav;
//
//   - the hand-curated **Commands:** summary at the top of the file
//     has always been alphabetical, and section-body drift makes the
//     two go out of sync invisibly.
//
// Two real bugs that would have been caught here:
//   - PR #21 originally placed `### ezs upgrade` *outside* the
//     Commands section entirely (after `## Exit codes`), so it never
//     appeared in this list at all — that's a separate failure the
//     regex below would surface as a missing entry.
//   - `### ezs menu` had been parked at the end of the section for
//     many releases instead of in alphabetical position between log
//     and new.
//
// Sorting is by the FIRST command name in each heading, so combined
// headings like "### `ezs commit` / `ezs amend`" sort under "commit",
// matching how a reader scans the sidebar.
func TestCommandHeadingsAlphabetical(t *testing.T) {
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

	// Match `### \`ezs <name>\`` at the start of a line. Captures
	// only the first backticked ezs invocation in the heading so
	// combined headings collapse to their lead command for sorting.
	headingRe := regexp.MustCompile("(?m)^### `ezs ([^`]+)`")
	matches := headingRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatal("no `### `ezs ...`` headings found in Commands section")
	}

	got := make([]string, len(matches))
	for i, m := range matches {
		got[i] = strings.SplitN(m[1], " ", 2)[0]
	}

	want := append([]string(nil), got...)
	sort.Strings(want)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("DOCUMENTATION.md `## Commands` H3 headings are not alphabetical:\n got:  %v\n want: %v\n\nMove the out-of-place command(s) into alphabetical position and re-run `make docs`.", got, want)
	}
}
