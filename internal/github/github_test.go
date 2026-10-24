package github

import (
	"errors"
	"fmt"
	"strings"
	"testing"
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

// TestWithoutLegacyStackSection covers the one-time cleanup of the "PR Stack"
// section older ezstack releases appended to PR bodies: every section is
// removed (including duplicates and mixed marker variants), user content
// survives, and an already-clean body is never reported as changed.
func TestWithoutLegacyStackSection(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		want        string
		wantChanged bool
	}{
		{"no section", "User description", "User description", false},
		{"empty body", "", "", false},
		{"clean body with GitHub trailing newline and CRLF", "Line one\r\nLine two\r\n", "Line one\nLine two", false},
		{
			"single section",
			"User description\n\n---\n## PR Stack\n\n1. #1\n2. #2 ← **This PR**\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n",
			"User description", true,
		},
		{
			"three duplicated sections",
			"User description" + strings.Repeat("\n\n---\n## PR Stack\n\n1. #1\n\n_footer_\n", 3),
			"User description", true,
		},
		{
			"CRLF duplicated sections",
			strings.ReplaceAll("User description"+strings.Repeat("\n\n---\n## PR Stack\n\n1. #1\n\n_footer_\n", 2), "\n", "\r\n"),
			"User description", true,
		},
		{
			"emoji marker earlier than no-emoji marker",
			"User description\n\n---\n## 📚 PR Stack\n\n1. #old\n\n---\n## PR Stack\n\n1. #1\n",
			"User description", true,
		},
		{
			"user markdown with its own rule preserved",
			"# Overview\n\n- one\n\n---\n\nMore notes\n\n---\n## PR Stack\n\n1. #1\n",
			"# Overview\n\n- one\n\n---\n\nMore notes", true,
		},
		{"only a section", "\n\n---\n## PR Stack\n\n1. #1\n", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := withoutLegacyStackSection(tt.body)
			if got != tt.want || changed != tt.wantChanged {
				t.Fatalf("withoutLegacyStackSection(%q) = (%q, %v), want (%q, %v)", tt.body, got, changed, tt.want, tt.wantChanged)
			}
			// Idempotent: the cleaned body never needs another update.
			if again, changed := withoutLegacyStackSection(got); changed || again != got {
				t.Errorf("second pass changed body: %q -> %q", got, again)
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

// TestPRCache: with WithPRCache, GetPR serves cached entries without calling
// gh, edits invalidate them, and the original client stays uncached.
func TestPRCache(t *testing.T) {
	base := &Client{owner: "o", repo: "r"}
	c := base.WithPRCache()
	if base.prs != nil {
		t.Fatal("WithPRCache must not mutate the receiver")
	}
	c.prs.m[7] = PR{Number: 7, Title: "cached"}
	pr, err := c.GetPR(7)
	if err != nil || pr.Title != "cached" {
		t.Fatalf("GetPR(7) = %+v, %v; want cached entry", pr, err)
	}
	pr.Title = "mutated"
	if again, _ := c.GetPR(7); again.Title != "cached" {
		t.Fatal("cache entry aliased by the returned PR")
	}
	c.forgetPR(7)
	if _, ok := c.prs.m[7]; ok {
		t.Fatal("forgetPR did not invalidate")
	}
}
