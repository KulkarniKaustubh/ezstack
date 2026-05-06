package ui

import (
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
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

// PrintStack renders the PR / line-diff / status column at varying offsets
// when each branch is allowed to print "name + 2 spaces + PR" independently —
// short branch names produced metadata earlier on the line than long ones, so
// the column zig-zagged across the tree. Lock the new alignment contract:
// the metadata column for every branch row sits at the same visual offset.
func TestPrintStack_AlignsMetadataColumn(t *testing.T) {
	stack := &config.Stack{
		Hash: "abc1234",
		Root: "main",
		Branches: []*config.Branch{
			{Name: "feat-a", Parent: "main", PRNumber: 101},
			{Name: "feat-a-much-longer-branch-name", Parent: "main", PRNumber: 102},
			{Name: "short", Parent: "feat-a-much-longer-branch-name", PRNumber: 103},
		},
	}

	out := captureStderr(t, func() {
		PrintStack(stack, "", false, nil)
	})

	// stripANSI removes CSI/OSC escape sequences so column counts reflect what
	// the user actually sees, not the wire bytes (color codes, hyperlinks).
	prCols := []int{}
	for _, line := range strings.Split(out, "\n") {
		clean := stripANSI(line)
		idx := strings.Index(clean, "[PR #")
		if idx == -1 {
			continue
		}
		prCols = append(prCols, idx)
	}
	if len(prCols) < 2 {
		t.Fatalf("expected at least 2 [PR #] markers, got %d in:\n%s", len(prCols), out)
	}
	first := prCols[0]
	for i, c := range prCols {
		if c != first {
			t.Errorf("PR column %d=%d differs from first=%d (jagged alignment), full output:\n%s", i, c, first, out)
		}
	}
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
