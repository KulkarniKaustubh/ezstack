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

// GetMergeBase finds the common ancestor between two branches
func (g *Git) GetMergeBase(branch1, branch2 string) (string, error) {
	return g.run("merge-base", branch1, branch2)
}

// GetCommitCount returns the number of commits between base and head
// This is useful to check if a branch has any commits of its own
func (g *Git) GetCommitCount(base, head string) (int, error) {
	output, err := g.run("rev-list", "--count", base+".."+head)
	if err != nil {
		return 0, err
	}
	var count int
	fmt.Sscanf(output, "%d", &count)
	return count, nil
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

// Push pushes the current branch to remote
func (g *Git) Push(force bool) error {
	branch, err := g.CurrentBranch()
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}
	args := []string{"push"}
	if force {
		args = append(args, "--force-with-lease")
	}
	args = append(args, "origin", branch)
	return g.RunInteractive(args...)
}

// PushSetUpstream pushes and sets upstream
func (g *Git) PushSetUpstream() error {
	branch, err := g.CurrentBranch()
	if err != nil {
		return err
	}
	return g.RunInteractive("push", "-u", "origin", branch)
}
