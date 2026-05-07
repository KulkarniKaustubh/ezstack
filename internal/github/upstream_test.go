package github

import (
	"strings"
	"testing"
)

func TestParseRepoViewJSON_Fork(t *testing.T) {
	data := []byte(`{
		"isFork": true,
		"parent": {
			"owner": {"login": "kubernetes"},
			"name": "kubernetes",
			"defaultBranchRef": {"name": "master"}
		},
		"defaultBranchRef": {"name": "main"}
	}`)
	u, err := parseRepoViewJSON(data)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !u.IsFork {
		t.Error("IsFork = false, want true")
	}
	if u.Owner != "kubernetes" || u.Repo != "kubernetes" {
		t.Errorf("Owner/Repo = %s/%s, want kubernetes/kubernetes", u.Owner, u.Repo)
	}
	if u.DefaultBranch != "master" {
		t.Errorf("DefaultBranch = %q, want master", u.DefaultBranch)
	}
	if u.ParentSSHURL != "git@github.com:kubernetes/kubernetes.git" {
		t.Errorf("ParentSSHURL = %q", u.ParentSSHURL)
	}
	if u.ParentHTTPSURL != "https://github.com/kubernetes/kubernetes.git" {
		t.Errorf("ParentHTTPSURL = %q", u.ParentHTTPSURL)
	}
}

func TestParseRepoViewJSON_NotAFork(t *testing.T) {
	data := []byte(`{"isFork": false, "defaultBranchRef": {"name": "main"}}`)
	u, err := parseRepoViewJSON(data)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if u.IsFork {
		t.Error("IsFork = true, want false for non-fork repo")
	}
	if u.Owner != "" || u.Repo != "" {
		t.Errorf("non-fork should leave Owner/Repo blank, got %q/%q", u.Owner, u.Repo)
	}
}

func TestParseRepoViewJSON_ForkOfDeletedParent(t *testing.T) {
	// isFork: true but parent fields empty — happens when the parent repo
	// has been deleted on GitHub. Treat as non-fork; there's no upstream.
	data := []byte(`{"isFork": true, "parent": {"owner": {"login": ""}, "name": ""}}`)
	u, err := parseRepoViewJSON(data)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if u.IsFork {
		t.Error("fork-of-deleted-parent should report IsFork = false (no usable upstream)")
	}
}

func TestParseRepoViewJSON_EmptyParentDefaultBranch(t *testing.T) {
	data := []byte(`{
		"isFork": true,
		"parent": {
			"owner": {"login": "org"},
			"name": "repo",
			"defaultBranchRef": {"name": ""}
		}
	}`)
	u, err := parseRepoViewJSON(data)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !u.IsFork {
		t.Error("IsFork should be true even when default branch is empty")
	}
	if u.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want fallback %q", u.DefaultBranch, "main")
	}
}

func TestParseRepoViewJSON_BadJSON(t *testing.T) {
	if _, err := parseRepoViewJSON([]byte("not json")); err == nil {
		t.Error("expected error on malformed JSON")
	}
}

func TestUpstream_ChooseParentURL(t *testing.T) {
	u := &Upstream{
		ParentSSHURL:   "git@github.com:org/repo.git",
		ParentHTTPSURL: "https://github.com/org/repo.git",
	}
	cases := []struct {
		originURL string
		wantSSH   bool
	}{
		{"git@github.com:user/repo.git", true},
		{"ssh://git@github.com/user/repo.git", true},
		{"https://github.com/user/repo.git", false},
		{"https://user:token@github.com/user/repo.git", false},
		{"", false}, // empty → default to HTTPS
	}
	for _, tc := range cases {
		got := u.ChooseParentURL(tc.originURL)
		if tc.wantSSH && !strings.HasPrefix(got, "git@") {
			t.Errorf("origin=%q got %q, want SSH form", tc.originURL, got)
		}
		if !tc.wantSSH && !strings.HasPrefix(got, "https://") {
			t.Errorf("origin=%q got %q, want HTTPS form", tc.originURL, got)
		}
	}
}

func TestUpstream_ChooseParentURL_NilSafe(t *testing.T) {
	var u *Upstream
	if got := u.ChooseParentURL("anything"); got != "" {
		t.Errorf("nil receiver should return empty, got %q", got)
	}
}

func TestNewClientForRepo_AndAccessors(t *testing.T) {
	c := NewClientForRepo("upstream-org", "core")
	if c.Owner() != "upstream-org" || c.Repo() != "core" {
		t.Errorf("Owner/Repo = %s/%s, want upstream-org/core", c.Owner(), c.Repo())
	}
	o, r := c.OwnerRepo()
	if o != "upstream-org" || r != "core" {
		t.Errorf("OwnerRepo = %s/%s, want upstream-org/core", o, r)
	}
}
