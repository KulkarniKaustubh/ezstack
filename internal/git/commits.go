package git

import (
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

// HasConflicts checks if there are merge conflicts
func (g *Git) HasConflicts() (bool, error) {
	output, err := g.run("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		// If the command fails, check if we're in a rebase
		return false, nil
	}
	return output != "", nil
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

// AbortRebase aborts an in-progress rebase
func (g *Git) AbortRebase() error {
	_, err := g.run("rebase", "--abort")
	return err
}

// ContinueRebase continues a paused rebase
func (g *Git) ContinueRebase() error {
	return g.RunInteractive("rebase", "--continue")
}

// Push pushes the current branch to remote
func (g *Git) Push(force bool) error {
	args := []string{"push"}
	if force {
		args = append(args, "--force-with-lease")
	}
	args = append(args, "origin", "HEAD")
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
