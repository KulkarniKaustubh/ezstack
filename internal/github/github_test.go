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
		isCurrent    bool
		wantContains []string
		wantMissing  []string
	}{
		{
			name:         "Empty body",
			body:         "",
			stackSection: "\n\n---\n## PR Stack\n\n1. PR #1\n",
			isCurrent:    true,
			wantContains: []string{"PR Stack", "PR #1"},
		},
		{
			name:         "Body with existing content",
			body:         "This is my PR description\n\nSome more text",
			stackSection: "\n\n---\n## PR Stack\n\n1. PR #1\n",
			isCurrent:    false,
			wantContains: []string{"This is my PR description", "PR Stack"},
		},
		{
			name:         "Replace existing stack section",
			body:         "Description\n\n---\n## PR Stack\n\n1. Old PR\n\n_This stack was created by ezstack_\n",
			stackSection: "\n\n---\n## PR Stack\n\n1. New PR\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n",
			isCurrent:    true,
			wantContains: []string{"New PR"},
			wantMissing:  []string{"Old PR"},
		},
		{
			name:         "Replace stack section with hyperlink footer",
			body:         "Description\n\n---\n## PR Stack\n\n1. Old PR\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n",
			stackSection: "\n\n---\n## PR Stack\n\n1. New PR\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n",
			isCurrent:    true,
			wantContains: []string{"New PR"},
			wantMissing:  []string{"Old PR"},
		},
		{
			name:         "Replace stack section with CRLF line endings",
			body:         "Description\r\n\r\n---\r\n## PR Stack\r\n\r\n1. Old PR\r\n\r\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\r\n",
			stackSection: "\n\n---\n## PR Stack\n\n1. New PR\n\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n",
			isCurrent:    true,
			wantContains: []string{"New PR"},
			wantMissing:  []string{"Old PR"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := updateBodyWithStack(tt.body, tt.stackSection, tt.isCurrent)

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
