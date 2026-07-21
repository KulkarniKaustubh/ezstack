package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Upstream describes the parent repo of a fork on github.com.
//
// Used by ezstack's public-fork stacking: when origin is a fork, the bottom
// PR of a stack targets the upstream parent (cross-repo); intermediates stay
// inside the fork. Detection is performed once and cached in RepoConfig.
type Upstream struct {
	Owner          string // GitHub login that owns the upstream repo
	Repo           string // upstream repo name
	IsFork         bool   // false ⇒ origin is not a fork; the rest of the fields are unset
	DefaultBranch  string // upstream's default branch (typically "main")
	ParentSSHURL   string // git@github.com:owner/repo.git
	ParentHTTPSURL string // https://github.com/owner/repo.git
}

// DetectUpstream queries `gh repo view <originOwner>/<originRepo>` for fork
// metadata. Returns Upstream{IsFork: false} when origin isn't a fork (this
// is a successful detection — caller should persist "disabled" so we don't
// re-query). Returns a non-nil error only for auth/network/parse failures
// so the caller can distinguish "not a fork" from "we don't know yet".
func DetectUpstream(originOwner, originRepo string) (*Upstream, error) {
	if originOwner == "" || originRepo == "" {
		return nil, fmt.Errorf("DetectUpstream: empty origin owner/repo")
	}
	args := []string{
		"repo", "view", fmt.Sprintf("%s/%s", originOwner, originRepo),
		"--json", "isFork,parent,defaultBranchRef",
	}
	cmd := exec.Command("gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr == "" {
			return nil, fmt.Errorf("gh repo view %s/%s failed: %w", originOwner, originRepo, err)
		}
		return nil, fmt.Errorf("gh repo view %s/%s: %s", originOwner, originRepo, stderrStr)
	}
	return parseRepoViewJSON(stdout.Bytes())
}

// parseRepoViewJSON parses the JSON output of `gh repo view --json
// isFork,parent,defaultBranchRef`. Extracted from DetectUpstream so the
// fork-detection logic is testable without a real gh binary.
func parseRepoViewJSON(data []byte) (*Upstream, error) {
	var raw struct {
		IsFork bool `json:"isFork"`
		Parent struct {
			Owner struct {
				Login string `json:"login"`
			} `json:"owner"`
			Name             string `json:"name"`
			DefaultBranchRef struct {
				Name string `json:"name"`
			} `json:"defaultBranchRef"`
		} `json:"parent"`
		DefaultBranchRef struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse gh repo view output: %w", err)
	}
	// Treat both "not a fork" and "fork-of-deleted-parent" (isFork=true but
	// parent.name empty) as a non-fork: there's no upstream to target.
	if !raw.IsFork || raw.Parent.Name == "" || raw.Parent.Owner.Login == "" {
		return &Upstream{IsFork: false}, nil
	}
	parentDefault := raw.Parent.DefaultBranchRef.Name
	if parentDefault == "" {
		// Empty repo or detached parent — fall back to "main" so downstream
		// flows have a usable target. Doctor catches drift on next run.
		parentDefault = "main"
	}
	return &Upstream{
		Owner:          raw.Parent.Owner.Login,
		Repo:           raw.Parent.Name,
		IsFork:         true,
		DefaultBranch:  parentDefault,
		ParentSSHURL:   fmt.Sprintf("git@github.com:%s/%s.git", raw.Parent.Owner.Login, raw.Parent.Name),
		ParentHTTPSURL: fmt.Sprintf("https://github.com/%s/%s.git", raw.Parent.Owner.Login, raw.Parent.Name),
	}, nil
}

// ChooseParentURL picks the parent URL whose scheme matches origin's URL,
// so adding the upstream remote doesn't surprise the user with HTTPS when
// they cloned over SSH (or vice-versa).
func (u *Upstream) ChooseParentURL(originURL string) string {
	if u == nil {
		return ""
	}
	if strings.HasPrefix(originURL, "git@") || strings.HasPrefix(originURL, "ssh://") {
		return u.ParentSSHURL
	}
	return u.ParentHTTPSURL
}
