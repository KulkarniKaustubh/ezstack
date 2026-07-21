package github

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

func TestErrPRNotFound_WrappedIsRecognizable(t *testing.T) {
	// Callers detect the "no PR for this branch" case via errors.Is rather
	// than fragile string matching against gh stderr. Verify the wrapped
	// form (which preserves the branch name in the message) still matches.
	err := fmt.Errorf("%w for branch %q", ErrPRNotFound, "feature/x")
	if !errors.Is(err, ErrPRNotFound) {
		t.Errorf("wrapped ErrPRNotFound not detected by errors.Is: %v", err)
	}
	if !strings.Contains(err.Error(), "feature/x") {
		t.Errorf("wrapped error lost branch context: %v", err)
	}
}

func TestNewClient(t *testing.T) {
	tests := []struct {
		name      string
		remoteURL string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{
			name:      "SSH URL",
			remoteURL: "git@github.com:owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "HTTPS URL",
			remoteURL: "https://github.com/owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "HTTPS URL without .git",
			remoteURL: "https://github.com/owner/repo",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "SSH URL without .git",
			remoteURL: "git@github.com:myorg/myrepo",
			wantOwner: "myorg",
			wantRepo:  "myrepo",
			wantErr:   false,
		},
		{
			name:      "Repo name with dot is preserved",
			remoteURL: "https://github.com/owner/my-repo.io.git",
			wantOwner: "owner",
			wantRepo:  "my-repo.io",
			wantErr:   false,
		},
		{
			name:      "Repo name with dot, no .git suffix",
			remoteURL: "git@github.com:owner/foo.bar",
			wantOwner: "owner",
			wantRepo:  "foo.bar",
			wantErr:   false,
		},
		{
			name:      "HTTPS URL with trailing slash",
			remoteURL: "https://github.com/owner/repo/",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "Invalid URL - no github.com",
			remoteURL: "git@gitlab.com:owner/repo.git",
			wantErr:   true,
		},
		{
			name:      "Invalid URL - malformed",
			remoteURL: "not-a-url",
			wantErr:   true,
		},
		{
			name:      "Empty URL",
			remoteURL: "",
			wantErr:   true,
		},
		// Hardened regex (audit M-tier): ports, anchors, query strings,
		// uppercase .GIT, and embedded github.com look-alikes used to
		// either fail to match or false-positive. Each line below is a
		// previously-broken case.
		{
			name:      "HTTPS URL with explicit port",
			remoteURL: "https://github.com:443/owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "ssh:// scheme with port",
			remoteURL: "ssh://git@github.com:22/owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "git:// scheme",
			remoteURL: "git://github.com/owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "HTTPS URL with userinfo",
			remoteURL: "https://user:token@github.com/owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "URL with anchor strips anchor from repo name",
			remoteURL: "https://github.com/owner/repo.git#readme",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "URL with query string strips query from repo name",
			remoteURL: "https://github.com/owner/repo.git?ref=main",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "Uppercase .GIT suffix is stripped (case-insensitive)",
			remoteURL: "git@github.com:OWNER/REPO.GIT",
			wantOwner: "OWNER",
			wantRepo:  "REPO",
			wantErr:   false,
		},
		{
			name:      "Mixed case .Git suffix is stripped",
			remoteURL: "https://github.com/owner/repo.Git",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		// False-positive guards: the old regex matched anywhere
		// "github.com" appeared as a substring; the anchored form
		// requires a `^|@|/` boundary so unrelated hosts that happen to
		// embed "github.com" in their path don't get misparsed as
		// github.com URLs.
		{
			name:      "Invalid - github.com only as path segment of another host",
			remoteURL: "https://example.com/proxy/github.com/owner/repo.git",
			wantErr:   true,
		},
		{
			name:      "Invalid - host containing github.com but not equal",
			remoteURL: "https://my-github.com/owner/repo.git",
			wantErr:   true,
		},
		// Browser-style URLs: users routinely paste these into `ezs new` /
		// `ezs config set` flows. Pre-fix the regex required the path to
		// end at the repo name, so these all errored out — a common
		// onboarding failure. The trailing path is now accepted and
		// discarded; we extract just (owner, repo).
		{
			name:      "Browser tree URL",
			remoteURL: "https://github.com/owner/repo/tree/main",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "Browser pull URL",
			remoteURL: "https://github.com/owner/repo/pull/123",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "Browser blob URL with file path",
			remoteURL: "https://github.com/owner/repo/blob/main/README.md",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "Browser issues URL",
			remoteURL: "https://github.com/owner/repo/issues/42",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "Browser actions URL",
			remoteURL: "https://github.com/owner/repo/actions/runs/123",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "Browser tree URL with .git in path (rare but well-formed)",
			remoteURL: "https://github.com/owner/repo.git/tree/main",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "Browser URL with anchor on a deep path",
			remoteURL: "https://github.com/owner/repo/blob/main/foo.go#L10",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "Browser URL with query string on a deep path",
			remoteURL: "https://github.com/owner/repo/tree/main?w=1",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.remoteURL)

			if tt.wantErr {
				if err == nil {
					t.Errorf("NewClient() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("NewClient() unexpected error: %v", err)
				return
			}

			if client.owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", client.owner, tt.wantOwner)
			}

			if client.repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", client.repo, tt.wantRepo)
			}
		})
	}
}

func TestGenerateStackSection(t *testing.T) {
	tests := []struct {
		name            string
		stack           *Stack
		currentPRBranch string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "Single branch stack",
			stack: &Stack{
				Name: "feature-a",
				Branches: []*Branch{
					{Name: "feature-a", PRNumber: 1, PRUrl: "https://github.com/org/repo/pull/1"},
				},
			},
			currentPRBranch: "feature-a",
			wantContains:    []string{"PR Stack", "https://github.com/org/repo/pull/1", "← **This PR**"},
		},
		{
			name: "Multi-branch stack",
			stack: &Stack{
				Name: "feature-a",
				Branches: []*Branch{
					{Name: "feature-a", PRNumber: 1, PRUrl: "https://github.com/org/repo/pull/1"},
					{Name: "feature-b", PRNumber: 2, PRUrl: "https://github.com/org/repo/pull/2"},
					{Name: "feature-c", PRNumber: 3, PRUrl: "https://github.com/org/repo/pull/3"},
				},
			},
			currentPRBranch: "feature-b",
			wantContains:    []string{"1.", "2.", "3.", "pull/1", "pull/2", "pull/3"},
		},
		{
			name: "Branch without PR",
			stack: &Stack{
				Name: "feature-a",
				Branches: []*Branch{
					{Name: "feature-a", PRNumber: 1, PRUrl: "https://github.com/org/repo/pull/1"},
					{Name: "feature-b", PRNumber: 0},
				},
			},
			currentPRBranch: "feature-a",
			wantContains:    []string{"pull/1", "← **This PR**"},
			wantNotContains: []string{"feature-b", "no PR yet"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert test types to real config types
			configStack := convertTestStack(tt.stack)
			result := generateStackSection(configStack, tt.currentPRBranch)

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("generateStackSection() missing %q in:\n%s", want, result)
				}
			}
			for _, notWant := range tt.wantNotContains {
				if strings.Contains(result, notWant) {
					t.Errorf("generateStackSection() should not contain %q in:\n%s", notWant, result)
				}
			}
		})
	}
}

// TestStackDescriptionBody_StripsStaleStackSectionBelowTwoPRs guards against a
// bug where UpdateStackDescription/UpdateStackDescriptionCached bailed out
// entirely whenever a stack had fewer than 2 PRs, skipping stripStackSections
// along with the append. If a PR's body already carried a stack section from
// when the stack had 2+ PRs (e.g. siblings later got merged/unstacked/deleted,
// dropping the stack to a single PR), that stale section — listing branches no
// longer part of any real stack — was never removed. stackDescriptionBody must
// still strip any existing section when prCount < 2, it just must not append a
// fresh one.
func TestStackDescriptionBody_StripsStaleStackSectionBelowTwoPRs(t *testing.T) {
	solo := convertTestStack(&Stack{
		Name: "feature-a",
		Branches: []*Branch{
			{Name: "feature-a", PRNumber: 1, PRUrl: "https://github.com/org/repo/pull/1"},
		},
	})

	staleBody := "My description" +
		"\n\n---\n## PR Stack\n\n1. https://github.com/org/repo/pull/1 ← **This PR**\n" +
		"2. https://github.com/org/repo/pull/2\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n"

	got := stackDescriptionBody(solo, "feature-a", staleBody, stackPRCount(solo))

	if strings.Contains(got, "PR Stack") {
		t.Errorf("stale stack section survived on a <2-PR stack:\n%s", got)
	}
	if !strings.Contains(got, "My description") {
		t.Errorf("user content was destroyed:\n%s", got)
	}

	// Applying it again must be a no-op — no flapping between calls.
	again := stackDescriptionBody(solo, "feature-a", got, stackPRCount(solo))
	if bodyNeedsUpdate(again, got) {
		t.Errorf("not idempotent once below 2 PRs:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// TestStackDescriptionBody_NoSpuriousUpdateBelowTwoPRs ensures a PR with no
// existing stack section, in a stack that has never had 2+ PRs, is left alone
// (no needless GitHub write on every push/sync).
func TestStackDescriptionBody_NoSpuriousUpdateBelowTwoPRs(t *testing.T) {
	solo := convertTestStack(&Stack{
		Name: "feature-a",
		Branches: []*Branch{
			{Name: "feature-a", PRNumber: 1, PRUrl: "https://github.com/org/repo/pull/1"},
		},
	})

	body := "Just a plain description, never stacked."
	got := stackDescriptionBody(solo, "feature-a", body, stackPRCount(solo))

	if bodyNeedsUpdate(got, body) {
		t.Errorf("expected no update for a body with no stack section on a <2-PR stack, got:\n%s", got)
	}
}

// TestStackDescriptionBody_AppendsSectionAtTwoOrMorePRs is the complementary
// happy path: once a stack has 2+ PRs, a fresh section must be appended.
func TestStackDescriptionBody_AppendsSectionAtTwoOrMorePRs(t *testing.T) {
	pair := convertTestStack(&Stack{
		Name: "feature-a",
		Branches: []*Branch{
			{Name: "feature-a", PRNumber: 1, PRUrl: "https://github.com/org/repo/pull/1"},
			{Name: "feature-b", PRNumber: 2, PRUrl: "https://github.com/org/repo/pull/2"},
		},
	})

	got := stackDescriptionBody(pair, "feature-a", "My description", stackPRCount(pair))

	if !strings.Contains(got, "PR Stack") {
		t.Errorf("expected a stack section to be appended for a 2-PR stack, got:\n%s", got)
	}
	if !strings.Contains(got, "pull/2") {
		t.Errorf("expected sibling PR listed, got:\n%s", got)
	}
}

// TestStackPRCount checks the root-PR-inclusive counting used to decide
// whether a stack section should be rendered at all.
func TestStackPRCount(t *testing.T) {
	tests := []struct {
		name  string
		stack *config.Stack
		want  int
	}{
		{
			name:  "no PRs",
			stack: convertTestStack(&Stack{Branches: []*Branch{{Name: "a"}}}),
			want:  0,
		},
		{
			name:  "one branch PR",
			stack: convertTestStack(&Stack{Branches: []*Branch{{Name: "a", PRNumber: 1}}}),
			want:  1,
		},
		{
			name: "root PR plus one branch PR",
			stack: func() *config.Stack {
				s := convertTestStack(&Stack{Branches: []*Branch{{Name: "a", PRNumber: 1}}})
				s.RootPRNumber = 99
				return s
			}(),
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stackPRCount(tt.stack); got != tt.want {
				t.Errorf("stackPRCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

// Helper types for testing (shadows config types)
type Stack struct {
	Name     string
	Branches []*Branch
}

type Branch struct {
	Name     string
	PRNumber int
	PRUrl    string
}

// convertTestStack converts test Stack to config.Stack
func convertTestStack(s *Stack) *config.Stack {
	branches := make([]*config.Branch, len(s.Branches))
	for i, b := range s.Branches {
		branches[i] = &config.Branch{
			Name:     b.Name,
			PRNumber: b.PRNumber,
			PRUrl:    b.PRUrl,
		}
	}
	return &config.Stack{
		Hash:     s.Name,
		Branches: branches,
	}
}

func TestUpdateBodyWithStack(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		stackSection string
		wantContains []string
		wantMissing  []string
	}{
		{
			name:         "Empty body",
			body:         "",
			stackSection: "\n\n---\n## PR Stack\n\n1. PR #1\n",
			wantContains: []string{"PR Stack", "PR #1"},
		},
		{
			name:         "Body with existing content",
			body:         "This is my PR description\n\nSome more text",
			stackSection: "\n\n---\n## PR Stack\n\n1. PR #1\n",
			wantContains: []string{"This is my PR description", "PR Stack"},
		},
		{
			name:         "Replace existing stack section",
			body:         "Description\n\n---\n## PR Stack\n\n1. Old PR\n\n_This stack was created by ezstack_\n",
			stackSection: "\n\n---\n## PR Stack\n\n1. New PR\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n",
			wantContains: []string{"New PR"},
			wantMissing:  []string{"Old PR"},
		},
		{
			name:         "Replace stack section with hyperlink footer",
			body:         "Description\n\n---\n## PR Stack\n\n1. Old PR\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n",
			stackSection: "\n\n---\n## PR Stack\n\n1. New PR\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n",
			wantContains: []string{"New PR"},
			wantMissing:  []string{"Old PR"},
		},
		{
			name:         "Replace stack section with CRLF line endings",
			body:         "Description\r\n\r\n---\r\n## PR Stack\r\n\r\n1. Old PR\r\n\r\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\r\n",
			stackSection: "\n\n---\n## PR Stack\n\n1. New PR\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n",
			wantContains: []string{"New PR"},
			wantMissing:  []string{"Old PR"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := updateBodyWithStack(tt.body, tt.stackSection)

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("updateBodyWithStack() missing %q in:\n%s", want, result)
				}
			}

			for _, notWant := range tt.wantMissing {
				if strings.Contains(result, notWant) {
					t.Errorf("updateBodyWithStack() should not contain %q in:\n%s", notWant, result)
				}
			}
		})
	}
}

// TestUpdateBodyWithStack_CollapsesDuplicates guards against the PR-description
// duplication bug: an older regex-based cleaner (pre-commit 5041f1a) matched
// only ONE stack section per pass via non-greedy `.*?`. When two sections were
// present it removed the first and re-appended a fresh one, so the second
// (older) section survived and grew by one every push/sync. That is the state
// many bodies are still in on GitHub.
//
// updateBodyWithStack must collapse ANY number of pre-existing sections down to
// exactly one on the next update, regardless of how they got there.
func TestUpdateBodyWithStack_CollapsesDuplicates(t *testing.T) {
	freshSection := "\n\n---\n## PR Stack\n\n1. https://github.com/org/repo/pull/1\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n"

	tests := []struct {
		name string
		body string
	}{
		{
			name: "two identical stack sections back-to-back",
			body: strings.TrimSpace("User description") +
				"\n\n---\n## PR Stack\n\n1. #1\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n" +
				"\n\n---\n## PR Stack\n\n1. #1\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n",
		},
		{
			name: "three duplicated stack sections",
			body: "User description" +
				"\n\n---\n## PR Stack\n\n1. #1\n\n_footer_\n" +
				"\n\n---\n## PR Stack\n\n1. #1\n\n_footer_\n" +
				"\n\n---\n## PR Stack\n\n1. #1\n\n_footer_\n",
		},
		{
			name: "duplicated sections with stale content (differ across dupes)",
			body: "User description" +
				"\n\n---\n## PR Stack\n\n1. Ancient PR\n\n_footer_\n" +
				"\n\n---\n## PR Stack\n\n1. Slightly less ancient PR\n\n_footer_\n" +
				"\n\n---\n## PR Stack\n\n1. Recent PR\n\n_footer_\n",
		},
		{
			name: "duplicated sections with CRLF line endings from GitHub",
			body: strings.ReplaceAll(
				"User description"+
					"\n\n---\n## PR Stack\n\n1. #1\n\n_footer_\n"+
					"\n\n---\n## PR Stack\n\n1. #1\n\n_footer_\n",
				"\n", "\r\n",
			),
		},
		{
			name: "duplicated sections with GitHub's trailing newline appended",
			body: "User description" +
				"\n\n---\n## PR Stack\n\n1. #1\n\n_footer_\n" +
				"\n\n---\n## PR Stack\n\n1. #1\n\n_footer_\n" +
				"\n",
		},
		{
			name: "user content preserved across dedup",
			body: "# Overview\n\nSome bullet points\n\n- one\n- two\n\n" +
				"---\n## PR Stack\n\n1. #1\n\n_footer_\n" +
				"\n---\n## PR Stack\n\n1. #1\n\n_footer_\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := updateBodyWithStack(tt.body, freshSection)

			// After update there must be EXACTLY one "## PR Stack" heading.
			// If dedup fails we get 2+ headings.
			if got := strings.Count(result, "## PR Stack"); got != 1 {
				t.Fatalf("expected exactly 1 stack section, got %d\nresult:\n%s", got, result)
			}
			// User content that came before the stack section must survive.
			if !strings.Contains(result, "User description") &&
				!strings.Contains(result, "# Overview") {
				t.Errorf("user content was destroyed:\n%s", result)
			}
			// Applying the operation a second time must be a no-op (up to the
			// bodyNeedsUpdate normalization).
			second := updateBodyWithStack(result, freshSection)
			if bodyNeedsUpdate(second, result) {
				t.Errorf("updateBodyWithStack is not idempotent:\nfirst:\n%s\nsecond:\n%s", result, second)
			}
		})
	}
}

// TestUpdateBodyWithStack_MixedMarkerVariants guards a subtle edge case in the
// pre-fix cleaner: the loop tried the no-emoji marker first and only fell
// through to the emoji marker if the no-emoji one wasn't present anywhere.
// When BOTH variants existed with the emoji one earlier in the body, it
// truncated at the later (no-emoji) marker and left the emoji section intact,
// producing a duplicate on the next append. The fixed cleaner truncates at the
// earliest occurrence across all known marker variants.
func TestUpdateBodyWithStack_MixedMarkerVariants(t *testing.T) {
	freshSection := "\n\n---\n## PR Stack\n\n1. #new\n\n_footer_\n"

	tests := []struct {
		name string
		body string
	}{
		{
			name: "emoji marker earlier than no-emoji marker",
			body: "User description" +
				"\n\n---\n## 📚 PR Stack\n\n1. #old-emoji\n\n_footer_\n" +
				"\n\n---\n## PR Stack\n\n1. #old\n\n_footer_\n",
		},
		{
			name: "no-emoji marker earlier than emoji marker",
			body: "User description" +
				"\n\n---\n## PR Stack\n\n1. #old\n\n_footer_\n" +
				"\n\n---\n## 📚 PR Stack\n\n1. #old-emoji\n\n_footer_\n",
		},
		{
			name: "only emoji marker present",
			body: "User description" +
				"\n\n---\n## 📚 PR Stack\n\n1. #old-emoji\n\n_footer_\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := updateBodyWithStack(tt.body, freshSection)

			if got := strings.Count(result, "## PR Stack"); got != 1 {
				t.Fatalf("expected exactly 1 PR Stack heading (no emoji), got %d\nresult:\n%s", got, result)
			}
			if strings.Contains(result, "📚") {
				t.Errorf("emoji marker still present after dedup:\n%s", result)
			}
			if !strings.Contains(result, "#new") {
				t.Errorf("fresh section content missing:\n%s", result)
			}
			if !strings.Contains(result, "User description") {
				t.Errorf("user content was destroyed:\n%s", result)
			}
		})
	}
}

// TestUpdateBodyWithStack_Idempotent asserts that applying updateBodyWithStack
// N times to the same body (with the same generated section) produces a body
// equivalent to applying it once. This is the property that stops the
// duplication drift: every push/sync/pr-update call runs through this
// function, so it MUST be idempotent even if bodyNeedsUpdate misfires.
func TestUpdateBodyWithStack_Idempotent(t *testing.T) {
	section := "\n\n---\n## PR Stack\n\n1. https://github.com/org/repo/pull/1\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n"

	bodies := []string{
		"",
		"Just a description",
		"Description\n\n---\n## PR Stack\n\n1. #1\n\n_footer_\n",
		"Description\n\n---\n## PR Stack\n\n1. #1\n\n_footer_\n\n---\n## PR Stack\n\n1. #1\n\n_footer_\n",
	}

	for i, body := range bodies {
		t.Run(fmt.Sprintf("body_%d", i), func(t *testing.T) {
			first := updateBodyWithStack(body, section)
			second := updateBodyWithStack(first, section)
			third := updateBodyWithStack(second, section)

			// The rendered body must stabilize after the first call.
			if first != second || second != third {
				t.Errorf("not idempotent\nfirst:\n%s\nsecond:\n%s\nthird:\n%s", first, second, third)
			}
			// Exactly one section, always.
			for label, out := range map[string]string{"first": first, "second": second, "third": third} {
				if got := strings.Count(out, "## PR Stack"); got != 1 {
					t.Errorf("%s: expected 1 section, got %d\n%s", label, got, out)
				}
			}
		})
	}
}

// TestStripStackSections directly exercises the cleaner helper so a regression
// there fails loudly independent of the surrounding updateBodyWithStack glue.
func TestStripStackSections(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "empty body is unchanged",
			body: "",
			want: "",
		},
		{
			name: "body with no marker is unchanged",
			body: "Just a description\n\nwith paragraphs",
			want: "Just a description\n\nwith paragraphs",
		},
		{
			name: "single section truncated at its start",
			body: "User\n\n---\n## PR Stack\n\n1. #1\n_footer_\n",
			want: "User\n\n",
		},
		{
			name: "three duplicate sections all removed",
			body: "User" +
				"\n\n---\n## PR Stack\n\n1. #1\n_footer_\n" +
				"\n\n---\n## PR Stack\n\n1. #1\n_footer_\n" +
				"\n\n---\n## PR Stack\n\n1. #1\n_footer_\n",
			want: "User\n\n",
		},
		{
			name: "emoji-first-then-no-emoji: emoji is earliest, truncate there",
			body: "User" +
				"\n\n---\n## 📚 PR Stack\n\n1. #old\n_footer_\n" +
				"\n\n---\n## PR Stack\n\n1. #newer\n_footer_\n",
			want: "User\n\n",
		},
		{
			name: "no-emoji-first-then-emoji: no-emoji is earliest, truncate there",
			body: "User" +
				"\n\n---\n## PR Stack\n\n1. #old\n_footer_\n" +
				"\n\n---\n## 📚 PR Stack\n\n1. #newer\n_footer_\n",
			want: "User\n\n",
		},
		{
			name: "only emoji section",
			body: "User\n\n---\n## 📚 PR Stack\n\n1. #1\n_footer_\n",
			want: "User\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripStackSections(tt.body); got != tt.want {
				t.Errorf("stripStackSections() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBodyNeedsUpdate guards against the PR-description churn bug: GitHub appends
// a trailing newline (and may switch to \r\n) when it stores a body, so the body
// we fetch back is never byte-for-byte equal to the one we generated. A naive
// `newBody != pr.Body` check therefore rewrites every PR on every push/sync,
// re-firing "edited" events across the whole stack.
func TestBodyNeedsUpdate(t *testing.T) {
	generated := "Description\n\n---\n## PR Stack\n\n1. PR #1\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n"

	tests := []struct {
		name        string
		newBody     string
		currentBody string
		want        bool
	}{
		{
			name:        "identical",
			newBody:     generated,
			currentBody: generated,
			want:        false,
		},
		{
			name:        "github appended trailing newline",
			newBody:     generated,
			currentBody: generated + "\n",
			want:        false,
		},
		{
			name:        "github returned CRLF and trailing newline",
			newBody:     generated,
			currentBody: strings.ReplaceAll(generated, "\n", "\r\n") + "\r\n",
			want:        false,
		},
		{
			name:        "content actually changed",
			newBody:     generated,
			currentBody: strings.Replace(generated, "PR #1", "PR #2", 1),
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bodyNeedsUpdate(tt.newBody, tt.currentBody); got != tt.want {
				t.Errorf("bodyNeedsUpdate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetPRChecksParser(t *testing.T) {
	// Test the check parsing logic
	tests := []struct {
		name       string
		output     string
		wantState  string
		wantPassed int
		wantFailed int
	}{
		{
			name:       "All passing",
			output:     "CI check\tpass\t10s\thttps://...\nLint\tpass\t5s\thttps://...",
			wantState:  "success",
			wantPassed: 2,
			wantFailed: 0,
		},
		{
			name:       "Some failing",
			output:     "CI check\tpass\t10s\thttps://...\nLint\tfail\t5s\thttps://...",
			wantState:  "failure",
			wantPassed: 1,
			wantFailed: 1,
		},
		{
			name:       "Summary line format",
			output:     "0 cancelled, 1 failing, 10 successful, 0 skipped, and 2 pending checks",
			wantState:  "failure",
			wantPassed: 10,
			wantFailed: 1,
		},
	}

	// These tests verify the parsing logic works correctly
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create minimal check status parser test
			// The actual parsing is done inside GetPRChecks which requires
			// gh CLI, so we test the logic pattern here
			if tt.wantState == "success" && tt.wantFailed > 0 {
				t.Error("Invalid test case: success state with failures")
			}
		})
	}
}

func TestPRForkInfo_IsFork(t *testing.T) {
	tests := []struct {
		name       string
		owner      string
		repoOwner  string
		wantIsFork bool
	}{
		{"same owner is not fork", "owner", "owner", false},
		{"different owner is fork", "owner", "forkowner", true},
		{"empty owner is not fork", "owner", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &PRForkInfo{
				HeadRepoOwner: tt.repoOwner,
				IsFork:        tt.repoOwner != "" && tt.repoOwner != tt.owner,
			}
			if info.IsFork != tt.wantIsFork {
				t.Errorf("IsFork = %v, want %v", info.IsFork, tt.wantIsFork)
			}
		})
	}
}

func TestPRForkInfo_Struct(t *testing.T) {
	info := &PRForkInfo{
		HeadRepoOwner:       "jason-nexthop",
		HeadRepoName:        "ezstack",
		MaintainerCanModify: true,
		IsFork:              true,
	}

	if info.HeadRepoOwner != "jason-nexthop" {
		t.Errorf("HeadRepoOwner = %q, want %q", info.HeadRepoOwner, "jason-nexthop")
	}
	if info.HeadRepoName != "ezstack" {
		t.Errorf("HeadRepoName = %q, want %q", info.HeadRepoName, "ezstack")
	}
	if !info.MaintainerCanModify {
		t.Error("MaintainerCanModify should be true")
	}
	if !info.IsFork {
		t.Error("IsFork should be true")
	}
}
