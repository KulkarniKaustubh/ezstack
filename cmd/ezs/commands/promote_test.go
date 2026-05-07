package commands

import (
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

func TestBuildPromoteBody_AppendsMarker(t *testing.T) {
	got := buildPromoteBody("My change description.", "alice", "myrepo", 42)
	if !strings.HasSuffix(got, "Replaces alice/myrepo#42") {
		t.Errorf("body = %q, missing Replaces suffix", got)
	}
	if !strings.HasPrefix(got, "My change description.") {
		t.Errorf("body should preserve original text, got %q", got)
	}
}

func TestBuildPromoteBody_DoesNotDuplicate(t *testing.T) {
	original := "My change description.\n\nReplaces alice/myrepo#42"
	got := buildPromoteBody(original, "alice", "myrepo", 42)
	if got != original {
		t.Errorf("body should be unchanged when marker already present\ngot:  %q\nwant: %q", got, original)
	}
}

func TestBuildPromoteBody_EmptyBody(t *testing.T) {
	got := buildPromoteBody("", "alice", "myrepo", 42)
	if got != "Replaces alice/myrepo#42" {
		t.Errorf("empty body should yield just the marker, got %q", got)
	}
}

func TestDetectPromoteCandidatesInStack(t *testing.T) {
	stack := &config.Stack{
		Root: "main",
		Branches: []*config.Branch{
			// Bottom: PR in upstream, merged → triggers promote on its child.
			{Name: "feat-a", Parent: "main", PRNumber: 100,
				PRTargetRepo: config.PRTargetRepoUpstream, IsMerged: true},
			// Child of feat-a: fork-side PR → CANDIDATE.
			{Name: "feat-b", Parent: "feat-a", PRNumber: 101,
				PRTargetRepo: config.PRTargetRepoFork},
			// Grandchild: fork-side; parent (feat-b) is fork-side, NOT a candidate.
			{Name: "feat-c", Parent: "feat-b", PRNumber: 102,
				PRTargetRepo: config.PRTargetRepoFork},
		},
	}
	got := detectPromoteCandidatesInStack(stack)
	if len(got) != 1 || got[0].Name != "feat-b" {
		names := make([]string, len(got))
		for i, b := range got {
			names[i] = b.Name
		}
		t.Errorf("candidates = %v, want [feat-b]", names)
	}
}

func TestDetectPromoteCandidatesInStack_AlreadyUpstreamSkipped(t *testing.T) {
	stack := &config.Stack{
		Root: "main",
		Branches: []*config.Branch{
			{Name: "feat-a", Parent: "main", PRNumber: 100,
				PRTargetRepo: config.PRTargetRepoUpstream, IsMerged: true},
			// Already cross-repo → skip.
			{Name: "feat-b", Parent: "feat-a", PRNumber: 101,
				PRTargetRepo: config.PRTargetRepoUpstream},
		},
	}
	if got := detectPromoteCandidatesInStack(stack); len(got) != 0 {
		t.Errorf("already-upstream child should not be a candidate, got %d", len(got))
	}
}

func TestDetectPromoteCandidatesInStack_ParentNotMerged(t *testing.T) {
	stack := &config.Stack{
		Root: "main",
		Branches: []*config.Branch{
			// Parent in upstream but not merged → child not a candidate yet.
			{Name: "feat-a", Parent: "main", PRNumber: 100,
				PRTargetRepo: config.PRTargetRepoUpstream, IsMerged: false},
			{Name: "feat-b", Parent: "feat-a", PRNumber: 101,
				PRTargetRepo: config.PRTargetRepoFork},
		},
	}
	if got := detectPromoteCandidatesInStack(stack); len(got) != 0 {
		t.Errorf("unmerged parent should yield no candidates, got %d", len(got))
	}
}

func TestDetectPromoteCandidatesInStack_NoPROnChild(t *testing.T) {
	stack := &config.Stack{
		Root: "main",
		Branches: []*config.Branch{
			{Name: "feat-a", Parent: "main", PRNumber: 100,
				PRTargetRepo: config.PRTargetRepoUpstream, IsMerged: true},
			// No PR yet → not a promote candidate (would be a fresh `pr create`).
			{Name: "feat-b", Parent: "feat-a", PRNumber: 0,
				PRTargetRepo: config.PRTargetRepoFork},
		},
	}
	if got := detectPromoteCandidatesInStack(stack); len(got) != 0 {
		t.Errorf("child without a PR should not be a candidate, got %d", len(got))
	}
}

func TestDetectPromoteCandidatesInStack_NilSafe(t *testing.T) {
	if got := detectPromoteCandidatesInStack(nil); got != nil {
		t.Errorf("nil stack should yield nil candidates, got %v", got)
	}
}
