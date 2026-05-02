package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	count, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return 0, fmt.Errorf("failed to parse commit count: %w", err)
	}
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

// IsMergeInProgress checks if a merge is in progress
func (g *Git) IsMergeInProgress() (bool, error) {
	gitDir, err := g.run("rev-parse", "--git-dir")
	if err != nil {
		return false, err
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(g.RepoDir, gitDir)
	}
	mergeHead := filepath.Join(gitDir, "MERGE_HEAD")
	_, err = os.Stat(mergeHead)
	return err == nil, nil
}

// RebaseContinue continues a rebase in progress
func (g *Git) RebaseContinue() error {
	cmd := exec.Command("git", "rebase", "--continue")
	cmd.Dir = g.RepoDir
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rebase --continue failed: %s", stderr.String())
	}
	return nil
}

// MergeContinue continues a merge in progress (commits the resolved merge)
func (g *Git) MergeContinue() error {
	cmd := exec.Command("git", "commit", "--no-edit")
	cmd.Dir = g.RepoDir
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("merge continue failed: %s", stderr.String())
	}
	return nil
}

// HasUnresolvedConflicts checks if there are unresolved merge conflicts
func (g *Git) HasUnresolvedConflicts() (bool, error) {
	output, err := g.run("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

// Push pushes the current branch to the specified remote.
// If remote is empty, defaults to "origin".
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

// PushSetUpstream pushes and sets upstream.
// If remote is empty, defaults to "origin".
func (g *Git) PushSetUpstream(remote ...string) error {
	r := "origin"
	if len(remote) > 0 && remote[0] != "" {
		r = remote[0]
	}
	branch, err := g.CurrentBranch()
	if err != nil {
		return err
	}
	return g.RunInteractive("push", "-u", r, branch)
}

// PushBranchSetUpstream pushes a specific branch with -u (set upstream).
// Variant of PushBranch for the first-push case where the remote ref doesn't
// exist yet. Necessary because callers like `pr create --branch <other>` can't
// rely on PushSetUpstream — that helper pushes whatever is currently checked
// out, not the named branch. If remote is empty, defaults to "origin".
func (g *Git) PushBranchSetUpstream(branch string, remote ...string) error {
	r := "origin"
	if len(remote) > 0 && remote[0] != "" {
		r = remote[0]
	}
	return g.RunInteractive("push", "-u", r, branch)
}
