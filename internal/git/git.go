package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git wraps git operations
type Git struct {
	RepoDir string
}

// New creates a new Git wrapper for the given repo directory
func New(repoDir string) *Git {
	return &Git{RepoDir: repoDir}
}

// run executes a git command and returns the output
func (g *Git) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.RepoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %s\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// RunInteractive runs a git command interactively (for rebase with conflicts)
func (g *Git) RunInteractive(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.RepoDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// CurrentBranch returns the current branch name
func (g *Git) CurrentBranch() (string, error) {
	return g.run("rev-parse", "--abbrev-ref", "HEAD")
}

// GetRepoRoot returns the root directory of the git repository
func (g *Git) GetRepoRoot() (string, error) {
	return g.run("rev-parse", "--show-toplevel")
}

// IsWorktree checks if the current directory is a worktree
func (g *Git) IsWorktree() (bool, error) {
	gitDir, err := g.run("rev-parse", "--git-dir")
	if err != nil {
		return false, err
	}
	return strings.Contains(gitDir, "worktrees"), nil
}

// GetMainWorktree returns the path to the main worktree
func (g *Git) GetMainWorktree() (string, error) {
	gitCommonDir, err := g.run("rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	// The common dir is inside the main repo's .git directory
	mainWorktree := filepath.Dir(gitCommonDir)

	// If it's a relative path (like "."), convert to absolute
	if !filepath.IsAbs(mainWorktree) {
		absPath, err := filepath.Abs(filepath.Join(g.RepoDir, mainWorktree))
		if err != nil {
			return "", err
		}
		mainWorktree = absPath
	}

	// Resolve symlinks to get canonical path (important on macOS where /tmp -> /private/tmp)
	resolved, err := filepath.EvalSymlinks(mainWorktree)
	if err == nil {
		mainWorktree = resolved
	}

	return mainWorktree, nil
}

// CreateWorktree creates a new worktree
func (g *Git) CreateWorktree(branchName, worktreePath, baseBranch string) error {
	// First create the branch from baseBranch
	if _, err := g.run("branch", branchName, baseBranch); err != nil {
		// Branch might already exist
		if !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	// Create the worktree
	_, err := g.run("worktree", "add", worktreePath, branchName)
	return err
}

// AddWorktree creates a worktree for an existing branch
func (g *Git) AddWorktree(branchName, worktreePath string) error {
	_, err := g.run("worktree", "add", worktreePath, branchName)
	return err
}

// ListWorktrees lists all worktrees
func (g *Git) ListWorktrees() ([]Worktree, error) {
	output, err := g.run("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var worktrees []Worktree
	var current Worktree
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			if current.Path != "" {
				worktrees = append(worktrees, current)
			}
			current = Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		} else if strings.HasPrefix(line, "branch ") {
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}
	return worktrees, nil
}

// Worktree represents a git worktree
type Worktree struct {
	Path   string
	Branch string
}

// Fetch fetches from remote
func (g *Git) Fetch() error {
	_, err := g.run("fetch", "--all", "--prune")
	return err
}

// GetBranchCommit gets the commit hash of a branch
func (g *Git) GetBranchCommit(branch string) (string, error) {
	return g.run("rev-parse", branch)
}

// GetLastCommitMessage returns the message of the last commit on the current branch
func (g *Git) GetLastCommitMessage() (string, error) {
	return g.run("log", "-1", "--format=%s")
}

// IsBranchMerged checks if a branch has been merged into target
func (g *Git) IsBranchMerged(branch, target string) (bool, error) {
	// Check if the branch commit is an ancestor of target
	cmd := exec.Command("git", "merge-base", "--is-ancestor", branch, target)
	cmd.Dir = g.RepoDir
	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				return false, nil
			}
		}
		return false, err
	}
	return true, nil
}

// IsBranchBehind checks if branch is behind target (target has commits not in branch)
func (g *Git) IsBranchBehind(branch, target string) (bool, error) {
	// Get the merge base
	mergeBase, err := g.run("merge-base", branch, target)
	if err != nil {
		return false, err
	}

	// Get the commit of target
	targetCommit, err := g.run("rev-parse", target)
	if err != nil {
		return false, err
	}

	// If merge base != target commit, then branch is behind target
	return mergeBase != targetCommit, nil
}

// GetCommitsBehind returns the number of commits branch is behind target
func (g *Git) GetCommitsBehind(branch, target string) (int, error) {
	output, err := g.run("rev-list", "--count", branch+".."+target)
	if err != nil {
		return 0, err
	}
	var count int
	fmt.Sscanf(output, "%d", &count)
	return count, nil
}

// RebaseResult contains the result of a rebase operation
type RebaseResult struct {
	Success     bool
	HasConflict bool
	Error       error
}

// RebaseNonInteractive rebases current branch onto target without interactive mode
// Returns structured result instead of just error for better conflict handling
func (g *Git) RebaseNonInteractive(target string) RebaseResult {
	cmd := exec.Command("git", "rebase", target)
	cmd.Dir = g.RepoDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// Check if it's a conflict
		stderrStr := stderr.String()
		if strings.Contains(stderrStr, "CONFLICT") ||
			strings.Contains(stderrStr, "could not apply") ||
			strings.Contains(stderrStr, "Resolve all conflicts") {
			return RebaseResult{HasConflict: true, Error: fmt.Errorf("rebase conflict")}
		}
		// Check if rebase is in progress
		inProgress, _ := g.IsRebaseInProgress()
		if inProgress {
			return RebaseResult{HasConflict: true, Error: fmt.Errorf("rebase conflict")}
		}
		return RebaseResult{Error: fmt.Errorf("rebase failed: %s", stderrStr)}
	}
	return RebaseResult{Success: true}
}

// RebaseOntoNonInteractive rebases commits from oldBase to current onto newBase
// Returns structured result for better conflict handling
func (g *Git) RebaseOntoNonInteractive(newBase, oldBase string) RebaseResult {
	cmd := exec.Command("git", "rebase", "--onto", newBase, oldBase)
	cmd.Dir = g.RepoDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// Check if it's a conflict
		stderrStr := stderr.String()
		if strings.Contains(stderrStr, "CONFLICT") ||
			strings.Contains(stderrStr, "could not apply") ||
			strings.Contains(stderrStr, "Resolve all conflicts") {
			return RebaseResult{HasConflict: true, Error: fmt.Errorf("rebase conflict")}
		}
		// Check if rebase is in progress
		inProgress, _ := g.IsRebaseInProgress()
		if inProgress {
			return RebaseResult{HasConflict: true, Error: fmt.Errorf("rebase conflict")}
		}
		return RebaseResult{Error: fmt.Errorf("rebase failed: %s", stderrStr)}
	}
	return RebaseResult{Success: true}
}

// Rebase rebases current branch onto target
func (g *Git) Rebase(target string) error {
	return g.RunInteractive("rebase", target)
}

// RebaseOnto rebases commits from oldBase to current onto newBase
func (g *Git) RebaseOnto(newBase, oldBase string) error {
	return g.RunInteractive("rebase", "--onto", newBase, oldBase)
}

// GetRemote gets the remote URL
func (g *Git) GetRemote(name string) (string, error) {
	return g.run("remote", "get-url", name)
}

// PullRebase pulls and rebases the current branch from its upstream
func (g *Git) PullRebase() error {
	return g.RunInteractive("pull", "--rebase")
}

// PushForce force pushes the current branch with lease (safer than --force)
func (g *Git) PushForce() error {
	return g.RunInteractive("push", "--force-with-lease")
}

// RemoveWorktree removes a worktree and optionally deletes the branch
func (g *Git) RemoveWorktree(worktreePath string, deleteBranch bool, branchName string) error {
	// First remove the worktree
	_, err := g.run("worktree", "remove", worktreePath)
	if err != nil {
		// Try force remove if regular remove fails
		_, err = g.run("worktree", "remove", "--force", worktreePath)
		if err != nil {
			return fmt.Errorf("failed to remove worktree: %w", err)
		}
	}

	// Optionally delete the branch
	if deleteBranch && branchName != "" {
		_, err := g.run("branch", "-D", branchName)
		if err != nil {
			return fmt.Errorf("worktree removed but failed to delete branch: %w", err)
		}
	}

	return nil
}

// CreateBranchFromRemote creates a local branch tracking a remote branch and returns the local branch name
func (g *Git) CreateBranchFromRemote(remoteBranch string) (string, error) {
	// Extract the local branch name from remote ref (e.g., "origin/feature" -> "feature")
	parts := strings.SplitN(remoteBranch, "/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid remote branch format: %s (expected origin/branch-name)", remoteBranch)
	}
	localBranch := parts[1]

	// Check if local branch already exists
	_, err := g.run("rev-parse", "--verify", localBranch)
	if err == nil {
		// Branch exists, just return the name
		return localBranch, nil
	}

	// Create the local branch tracking the remote
	_, err = g.run("branch", "--track", localBranch, remoteBranch)
	if err != nil {
		return "", fmt.Errorf("failed to create local branch from %s: %w", remoteBranch, err)
	}

	return localBranch, nil
}
