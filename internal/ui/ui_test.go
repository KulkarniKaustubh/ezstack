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
