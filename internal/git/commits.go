package git

import (
	"fmt"
	"strings"
)

// Commit represents a git commit
type Commit struct {
	Hash    string
	Subject string
	Author  string
}

// GetCommitsBetween returns commits between base and head (exclusive of base)
func (g *Git) GetCommitsBetween(base, head string) ([]Commit, error) {
	// Get commits that are in head but not in base
	output, err := g.run("log", "--pretty=format:%H|%s|%an", base+".."+head)
	if err != nil {
		return nil, err
	}
	if output == "" {
		return nil, nil
	}

	var commits []Commit
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) == 3 {
			commits = append(commits, Commit{
				Hash:    parts[0],
				Subject: parts[1],
				Author:  parts[2],
			})
		}
	}
	return commits, nil
}

// IsRebaseInProgress checks if a rebase is in progress
func (g *Git) IsRebaseInProgress() (bool, error) {
	output, err := g.run("status")
	if err != nil {
		return false, err
	}
	return strings.Contains(output, "rebase in progress") ||
		strings.Contains(output, "interactive rebase in progress"), nil
}

// Push pushes the current branch to the specified remote.
// If remote is empty, defaults to "origin".
//
// Deprecated: prefer PushBranch(branchName, force, remote). Push consults
// CurrentBranch() at call time, which means after a transient checkout (e.g.
// the syncViaCheckout codepath that restores HEAD to main) it will push the
// wrong branch. New callers should pass an explicit branch name.
func (g *Git) Push(force bool, remote ...string) error {
	r := "origin"
	if len(remote) > 0 && remote[0] != "" {
		r = remote[0]
	}
	branch, err := g.CurrentBranch()
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}
	args := []string{"push"}
	if force {
		args = append(args, "--force-with-lease")
	}
	args = append(args, r, branch)
	return g.RunInteractive(args...)
}

// PushBranch pushes a specific branch to a remote (not necessarily the current branch).
// If remote is empty, defaults to "origin".
func (g *Git) PushBranch(branch string, force bool, remote ...string) error {
	r := "origin"
	if len(remote) > 0 && remote[0] != "" {
		r = remote[0]
	}
	args := []string{"push"}
	if force {
		args = append(args, "--force-with-lease")
	}
	args = append(args, r, branch)
	return g.RunInteractive(args...)
}

// PushBranchSetUpstream pushes a specific branch with -u (set upstream).
// Variant of PushBranch for the first-push case where the remote ref doesn't
// exist yet. Necessary because callers like `pr create --branch <other>` need
// to push by explicit branch name (not whatever HEAD happens to be).
// If remote is empty, defaults to "origin".
func (g *Git) PushBranchSetUpstream(branch string, remote ...string) error {
	r := "origin"
	if len(remote) > 0 && remote[0] != "" {
		r = remote[0]
	}
	return g.RunInteractive("push", "-u", r, branch)
}
