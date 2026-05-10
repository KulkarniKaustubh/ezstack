package ui

import (
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/mattn/go-runewidth"
)

// PrintStack used to render the stack root as a bare name with PR info; the
// (remote) tag was only emitted for tree branches. This left `ezs new -r`
// stacks (where the root *is* the remote PR branch) without any visual
// indicator that the root wasn't the user's own branch. Verify the tag now
// shows up exactly when the stack opts in via RootIsRemote.
func TestPrintStack_RemoteTagOnRoot(t *testing.T) {
	// `ezs new -r` always creates a user-owned child on top of the remote root,
	// so the realistic shape is one tree branch parented on the remote.
	stack := &config.Stack{
		Hash:         "abc1234",
		Root:         "alice/feature",
		RootBase:     "main",
		RootIsRemote: true,
		Branches: []*config.Branch{
			{Name: "my-feature", Parent: "alice/feature"},
		},
	}

	out := captureStderr(t, func() {
		PrintStack(stack, "", false, nil)
	})
	rootLineIdx := strings.Index(out, "alice/feature")
	if rootLineIdx == -1 {
		t.Fatalf("root branch not rendered, got:\n%s", out)
	}
	rootLineEnd := rootLineIdx + strings.Index(out[rootLineIdx:], "\n")
	rootLine := out[rootLineIdx:rootLineEnd]
	if !strings.Contains(rootLine, "(remote)") {
		t.Errorf("expected (remote) tag on root line %q, full output:\n%s", rootLine, out)
	}
	// Negative: the user-owned child is not flagged remote.
	myLineIdx := strings.Index(out, "my-feature")
	if myLineIdx == -1 {
		t.Fatalf("child branch not rendered, got:\n%s", out)
	}
	myLineEnd := myLineIdx + strings.Index(out[myLineIdx:], "\n")
	if strings.Contains(out[myLineIdx:myLineEnd], "(remote)") {
		t.Errorf("child branch should not show (remote); got %q", out[myLineIdx:myLineEnd])
	}
}

// Mirror: a normal user-owned stack (root is the user's own branch, even after
// PR creation populates RootBase/RootPRNumber) must NOT pick up the tag —
// otherwise every stack would show (remote) once it has a PR.
func TestPrintStack_NoRemoteTagOnUserOwnedRoot(t *testing.T) {
	stack := &config.Stack{
		Hash:         "abc1234",
		Root:         "main",
		RootIsRemote: false,
		Branches: []*config.Branch{
			{Name: "feature", Parent: "main"},
		},
	}

	out := captureStderr(t, func() {
		PrintStack(stack, "", false, nil)
	})
	rootIdx := strings.Index(out, "main")
	if rootIdx == -1 {
		t.Fatalf("root not rendered, got:\n%s", out)
	}
	rootLineEnd := rootIdx + strings.Index(out[rootIdx:], "\n")
	if strings.Contains(out[rootIdx:rootLineEnd], "(remote)") {
		t.Errorf("user-owned root should not show (remote); got %q", out[rootIdx:rootLineEnd])
	}
}

// formatStackString feeds the fzf preview pane shown in SelectBranch /
// SelectWorktreeWithStackPreview / SelectStack. PrintStack already renders
// the (remote) tag, but the fzf preview historically rendered a bare root
// name with no tag — so a user picking a branch from fzf saw the same stack
// described differently than `ezs ls` describes it. Lock parity here to keep
// the two renderers in lockstep.
func TestFormatStackString_RootRemoteTag(t *testing.T) {
	stack := &config.Stack{
		Hash:         "abc1234",
		Root:         "alice/feature",
		RootBase:     "main",
		RootIsRemote: true,
		Branches: []*config.Branch{
			{Name: "alice/feature", Parent: "main", IsRemote: true},
			{Name: "my-feature", Parent: "alice/feature"},
		},
	}

	out := formatStackString(stack, "my-feature")
	rootIdx := strings.Index(out, "alice/feature")
	if rootIdx == -1 {
		t.Fatalf("root not rendered in fzf preview, got:\n%s", out)
	}
	// Lines in formatStackString are joined with the literal \n escape (fzf
	// renders them via `printf '%b'`), so split on that — not real newlines.
	rootSegEnd := rootIdx + strings.Index(out[rootIdx:], `\n`)
	rootSeg := out[rootIdx:rootSegEnd]
	if !strings.Contains(rootSeg, "(remote)") {
		t.Errorf("formatStackString root segment %q missing (remote) tag, full preview:\n%s", rootSeg, out)
	}
	if !strings.Contains(out, "my-feature") {
		t.Fatalf("user branch not rendered, got:\n%s", out)
	}
}

// formatStackString must also flag the per-branch (remote) tag for
// contributors checked in via `ezs new origin/<branch>` — same parity gap
// the root-level fix above covered, just for tree members.
func TestFormatStackString_PerBranchRemoteTag(t *testing.T) {
	stack := &config.Stack{
		Hash: "abc1234",
		Root: "main",
		Branches: []*config.Branch{
			{Name: "alice/feature", Parent: "main", IsRemote: true},
			{Name: "my-feature", Parent: "main"},
		},
	}

	out := formatStackString(stack, "my-feature")
	aliceIdx := strings.Index(out, "alice/feature")
	if aliceIdx == -1 {
		t.Fatalf("contributor branch not rendered, got:\n%s", out)
	}
	aliceSegEnd := aliceIdx + strings.Index(out[aliceIdx:], `\n`)
	aliceSeg := out[aliceIdx:aliceSegEnd]
	if !strings.Contains(aliceSeg, "(remote)") {
		t.Errorf("contributor branch line %q missing (remote) tag", aliceSeg)
	}

	// Negative: the user's own branch must not pick up the tag.
	myIdx := strings.Index(out, "my-feature")
	if myIdx == -1 {
		t.Fatalf("user branch not rendered, got:\n%s", out)
	}
	mySegEnd := myIdx + strings.Index(out[myIdx:], `\n`)
	mySeg := out[myIdx:mySegEnd]
	if strings.Contains(mySeg, "(remote)") {
		t.Errorf("user-owned branch line %q must not show (remote) tag", mySeg)
	}
}

// TestPrintStack_AlignsMetadataColumn locks the per-column alignment contract
// for `ezs ls` and `ezs status` output: across every row that carries a PR /
// diff cell, those cells start at the same visual column. The mix of PR
// labels exercises the cases where alignment used to break — `[PR #N]`,
// `[PR #N MERGED]` (longer), and `[no PR]` (shorter) — plus a short and a
// long branch name so the name section also varies.
func TestPrintStack_AlignsMetadataColumn(t *testing.T) {
	stack := &config.Stack{
		Hash: "abc1234",
		Root: "main",
		Branches: []*config.Branch{
			{Name: "feat-a", Parent: "main", PRNumber: 101},
			{Name: "feat-a-much-longer-branch-name", Parent: "main", PRNumber: 102, PRState: "MERGED"},
			{Name: "short", Parent: "feat-a-much-longer-branch-name"},
			{Name: "feat-c", Parent: "feat-a-much-longer-branch-name", PRNumber: 104},
		},
	}
	statusMap := map[string]*BranchStatus{
		// Open PR with diff — both PR and diff cells render.
		"feat-a": {Additions: 5, Deletions: 2},
		// Merged PR — diff is suppressed by getDiffText's merged/closed gate,
		// but the wide `[PR #102 MERGED]` label drives maxPRWidth and so sets
		// the diff column for every other row.
		"feat-a-much-longer-branch-name": {Additions: 1234, Deletions: 999, PRState: "MERGED"},
		// Second open-PR row with diff stats — needed to assert diff-column
		// alignment requires at least two diff-bearing rows.
		"feat-c": {Additions: 42, Deletions: 7},
	}

	out := captureStderr(t, func() {
		PrintStack(stack, "", true, statusMap)
	})

	var prCols, diffCols []int
	for _, line := range strings.Split(out, "\n") {
		clean := stripANSI(line)
		pr := indexOfAny(clean, "[PR #", "[no PR]")
		if pr == -1 {
			continue
		}
		// Measure DISPLAY column, not byte offset — the gap fill uses U+00B7
		// middle dots (2 bytes in UTF-8 each), so byte indices diverge across
		// rows even when they're visually aligned.
		prCols = append(prCols, runewidth.StringWidth(clean[:pr]))
		// Diff cells start with " +N" right after the (padded) PR cell. Only
		// rows that actually have diff stats contribute to diff alignment.
		if d := strings.Index(clean[pr:], " +"); d != -1 {
			diffCols = append(diffCols, runewidth.StringWidth(clean[:pr+d]))
		}
	}
	if len(prCols) < 2 {
		t.Fatalf("expected at least 2 metadata rows, got %d in:\n%s", len(prCols), out)
	}
	for i, c := range prCols {
		if c != prCols[0] {
			t.Errorf("PR column row %d = %d, want %d (jagged), output:\n%s", i, c, prCols[0], out)
		}
	}
	if len(diffCols) < 2 {
		t.Fatalf("expected at least 2 rows with diff stats to verify diff alignment, got %d in:\n%s", len(diffCols), out)
	}
	for i, c := range diffCols {
		if c != diffCols[0] {
			t.Errorf("diff column row %d = %d, want %d (PR cell not padded), output:\n%s", i, c, diffCols[0], out)
		}
	}
}

// TestPrintStack_StrikethroughSpansDotLeader covers the gap-with-strike
// substitution: when a merged/closed branch is shorter than the widest row,
// its name-to-PR gap fills with leader dots whose escape sequence embeds a
// Reset. Without re-applying Strikethrough after that Reset the PR cell on
// merged rows would print without the strike, since strikethrough is a
// separate SGR attribute that propagates until cleared.
//
// The widest branch deliberately carries no PR so its gap stays at the
// 2-space floor — which forces the merged row's gap to actually contain
// dots (vs. plain spaces, which would skip the substitution path entirely).
func TestPrintStack_StrikethroughSpansDotLeader(t *testing.T) {
	stack := &config.Stack{
		Hash: "abc1234",
		Root: "main",
		Branches: []*config.Branch{
			{Name: "feat-very-long-branch-name", Parent: "main"},
			{Name: "merged", Parent: "main", PRNumber: 99, PRState: "MERGED"},
		},
	}

	out := captureStderr(t, func() {
		PrintStack(stack, "", false, nil)
	})

	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(stripANSI(l), "merged") && strings.Contains(stripANSI(l), "[PR #") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("merged-row line not found in:\n%s", out)
	}
	if !strings.Contains(stripANSI(line), "·") {
		t.Fatalf("merged row's gap should contain leader dots; got %q", stripANSI(line))
	}
	prByte := strings.Index(line, "[PR #")
	if prByte == -1 {
		t.Fatalf("PR cell not found in merged-row line: %q", line)
	}
	if !sgrStrikethroughActiveAt(line, prByte) {
		t.Errorf("strikethrough dropped before PR cell on merged row — gapWithStrike substitution broke; got line: %q", line)
	}
}

// TestPrintStack_RootCurrentPointerIsGreen pins that when the root is the
// current branch, the leading `>` pointer actually renders in green. The
// previous code emitted the Green SGR AFTER the pointer character, so `>`
// printed in the terminal default and only the (invisible) padding spaces
// between pointer and name picked up the green tint. The inner Gray on the
// name still wins, but the pointer itself must be green to match branch-row
// behavior and to give the user a visible "this is your current location"
// signal on root rows.
func TestPrintStack_RootCurrentPointerIsGreen(t *testing.T) {
	stack := &config.Stack{
		Hash: "abc1234",
		Root: "main",
		Branches: []*config.Branch{
			{Name: "feat", Parent: "main"},
		},
	}

	out := captureStderr(t, func() {
		PrintStack(stack, "main", false, nil)
	})

	// Find the root line (contains "main" but not the connector chars used
	// for branch rows).
	var rootLine string
	for _, l := range strings.Split(out, "\n") {
		clean := stripANSI(l)
		if strings.Contains(clean, "main") && !strings.Contains(clean, "feat") {
			rootLine = l
			break
		}
	}
	if rootLine == "" {
		t.Fatalf("root line not found; full output:\n%s", out)
	}

	// The pointer is the first non-SGR character on the line. We expect:
	//   <Green SGR><pointer ">"><Reset SGR>...
	// i.e. the Green escape must precede the literal `>`.
	pointerByte := strings.Index(rootLine, ">")
	if pointerByte == -1 {
		t.Fatalf("expected `>` pointer on current-root line, got %q", rootLine)
	}
	if !sgrColorActiveAt(rootLine, pointerByte, "32") {
		t.Errorf("`>` pointer not rendered in green on current-root line; got %q", rootLine)
	}
}

// TestPrintStack_BranchCurrentPointerIsGreen mirrors the root-pointer
// contract for tree branch rows: when a non-root branch is current, the
// leading `>` must also render in green. Pre-fix, branch rows emitted
// the pointer character BEFORE the green SGR, so `>` printed in the
// terminal default while the rest of the row picked up green via the
// non-resetting Bold that follows. Visually inconsistent with the root
// row and unhelpful as a cursor marker — pin both rows to the same
// ordering so future renderer churn can't silently regress one without
// the other.
func TestPrintStack_BranchCurrentPointerIsGreen(t *testing.T) {
	stack := &config.Stack{
		Hash: "abc1234",
		Root: "main",
		Branches: []*config.Branch{
			{Name: "feat", Parent: "main"},
		},
	}

	out := captureStderr(t, func() {
		PrintStack(stack, "feat", false, nil)
	})

	var branchLine string
	for _, l := range strings.Split(out, "\n") {
		clean := stripANSI(l)
		if strings.Contains(clean, "feat") && !strings.Contains(clean, "main") {
			branchLine = l
			break
		}
	}
	if branchLine == "" {
		t.Fatalf("branch line not found; full output:\n%s", out)
	}

	pointerByte := strings.Index(branchLine, ">")
	if pointerByte == -1 {
		t.Fatalf("expected `>` pointer on current-branch line, got %q", branchLine)
	}
	if !sgrColorActiveAt(branchLine, pointerByte, "32") {
		t.Errorf("`>` pointer not rendered in green on current-branch line; got %q", branchLine)
	}
}

// sgrColorActiveAt walks the SGR (CSI ... m) escape sequences in s up to byte
// position pos and returns whether the given color code (e.g. "32" for green)
// is the active foreground color. Used to pin the green-pointer contract for
// the root-current row in PrintStack.
func sgrColorActiveAt(s string, pos int, code string) bool {
	active := ""
	i := 0
	for i < pos && i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ';') {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				params := s[i+2 : j]
				if params == "" {
					active = ""
				} else {
					for _, p := range strings.Split(params, ";") {
						if p == "0" || p == "" {
							active = ""
						} else if (len(p) == 2 && p[0] == '3' && p[1] >= '0' && p[1] <= '9') ||
							(len(p) == 2 && p[0] == '9' && p[1] >= '0' && p[1] <= '9') {
							active = p
						}
					}
				}
				i = j + 1
				continue
			}
		}
		i++
	}
	return active == code
}

// sgrStrikethroughActiveAt walks the SGR (CSI ... m) escape sequences in s up
// to byte position pos and returns whether strikethrough (SGR 9) is the
// active text-decoration state. Used to assert that the dot-leader gap
// preserves strikethrough across its embedded Reset on merged-row renders.
func sgrStrikethroughActiveAt(s string, pos int) bool {
	strike := false
	i := 0
	for i < pos && i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ';') {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				params := s[i+2 : j]
				if params == "" {
					strike = false
				} else {
					for _, p := range strings.Split(params, ";") {
						switch p {
						case "0", "":
							strike = false
						case "9":
							strike = true
						case "29":
							strike = false
						}
					}
				}
				i = j + 1
				continue
			}
		}
		i++
	}
	return strike
}

// indexOfAny returns the lowest index in s where any of the given substrings
// occurs, or -1 if none match.
func indexOfAny(s string, needles ...string) int {
	best := -1
	for _, n := range needles {
		if i := strings.Index(s, n); i != -1 && (best == -1 || i < best) {
			best = i
		}
	}
	return best
}

// stripANSI strips CSI ("ESC[...letter") and OSC 8 ("ESC]8;;...ESC\\") sequences
// so column-position assertions are meaningful regardless of color or hyperlink
// markup.
func stripANSI(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) {
			next := s[i+1]
			if next == '[' {
				// CSI: skip until a final byte in 0x40..0x7E
				j := i + 2
				for j < len(s) {
					b := s[j]
					if b >= 0x40 && b <= 0x7e {
						j++
						break
					}
					j++
				}
				i = j
				continue
			}
			if next == ']' {
				// OSC: skip until BEL (0x07) or ST (ESC \\)
				j := i + 2
				for j < len(s) {
					if s[j] == 0x07 {
						j++
						break
					}
					if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				i = j
				continue
			}
			if next == '\\' {
				i += 2
				continue
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func TestSuggestCommand(t *testing.T) {
	cands := []string{"status", "stack", "sync", "new", "push", "pull"}

	tests := []struct {
		name       string
		input      string
		candidates []string
		want       string
	}{
		{"empty input", "", cands, ""},
		{"nil candidates", "status", nil, ""},
		{"empty candidates slice", "status", []string{}, ""},
		{"exact match", "status", cands, "status"},
		{"one typo", "statu", cands, "status"},
		{"swap typo", "snyc", cands, "sync"},
		{"too far", "xyzzy", cands, ""},
		{"skips empty strings", "status", []string{"", "", "status"}, "status"},
		{"all empty candidates", "status", []string{"", ""}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SuggestCommand(tc.input, tc.candidates)
			if got != tc.want {
				t.Errorf("SuggestCommand(%q, %v) = %q, want %q",
					tc.input, tc.candidates, got, tc.want)
			}
		})
	}
}
