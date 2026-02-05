package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ezstack/ezstack/internal/ui"
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

// runWithSpinner executes a git command with a delayed loading spinner
// The spinner only shows if the command takes longer than ui.SpinnerDelay
func (g *Git) runWithSpinner(message string, args ...string) (string, error) {
	var result string
	var cmdErr error

	err := ui.WithSpinner(message, func() error {
		cmd := exec.Command("git", args...)
		cmd.Dir = g.RepoDir
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		cmdErr = cmd.Run()
		if cmdErr != nil {
			return fmt.Errorf("git %s failed: %s\n%s", strings.Join(args, " "), cmdErr, stderr.String())
		}
		result = strings.TrimSpace(stdout.String())
		return nil
	})

	if err != nil {
		return "", err
	}
	return result, nil
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
	_, err := g.runWithSpinner(fmt.Sprintf("Creating worktree for %s...", branchName), "worktree", "add", worktreePath, branchName)
	return err
}

// AddWorktree creates a worktree for an existing branch
func (g *Git) AddWorktree(branchName, worktreePath string) error {
	_, err := g.runWithSpinner(fmt.Sprintf("Creating worktree for %s...", branchName), "worktree", "add", worktreePath, branchName)
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
	_, err := g.runWithSpinner("Fetching from remote...", "fetch", "--all", "--prune")
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

// GetCommitsAhead returns the number of commits branch is ahead of target
func (g *Git) GetCommitsAhead(branch, target string) (int, error) {
	output, err := g.run("rev-list", "--count", target+".."+branch)
	if err != nil {
		return 0, err
	}
	var count int
	fmt.Sscanf(output, "%d", &count)
	return count, nil
}

// IsLocalAheadOfOrigin checks if the local branch has commits not in origin
// Returns true if local is ahead (needs push), false if in sync or behind
func (g *Git) IsLocalAheadOfOrigin(branch string) (bool, error) {
	originBranch := "origin/" + branch
	// Check if origin branch exists
	_, err := g.run("rev-parse", "--verify", originBranch)
	if err != nil {
		// Origin branch doesn't exist - local is ahead (needs first push)
		return true, nil
	}
	ahead, err := g.GetCommitsAhead(branch, originBranch)
	if err != nil {
		return false, err
	}
	return ahead > 0, nil
}

// RemoteBranchExists checks if a remote branch exists
func (g *Git) RemoteBranchExists(branch string) bool {
	originBranch := "origin/" + branch
	_, err := g.run("rev-parse", "--verify", originBranch)
	return err == nil
}

// HasDivergedFromOrigin checks if local and remote branches have diverged
// Returns (hasDiverged, localAhead, remoteBehind, error)
// hasDiverged is true if both local has commits not in remote AND remote has commits not in local
func (g *Git) HasDivergedFromOrigin(branch string) (bool, int, int, error) {
	originBranch := "origin/" + branch
	// Check if origin branch exists
	_, err := g.run("rev-parse", "--verify", originBranch)
	if err != nil {
		// Origin branch doesn't exist - not diverged, just needs first push
		return false, 0, 0, nil
	}

	// Get commits local has that remote doesn't
	localAhead, err := g.GetCommitsAhead(branch, originBranch)
	if err != nil {
		return false, 0, 0, err
	}

	// Get commits remote has that local doesn't
	remoteBehind, err := g.GetCommitsBehind(branch, originBranch)
	if err != nil {
		return false, 0, 0, err
	}

	// Diverged if both have unique commits
	hasDiverged := localAhead > 0 && remoteBehind > 0
	return hasDiverged, localAhead, remoteBehind, nil
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
	spinner := ui.NewDelayedSpinner(fmt.Sprintf("Rebasing onto %s...", target))
	spinner.Start()
	defer spinner.Stop()

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
	spinner := ui.NewDelayedSpinner(fmt.Sprintf("Rebasing onto %s...", newBase))
	spinner.Start()
	defer spinner.Stop()

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

// ResetHard performs a hard reset to the given ref
// This is used to fast-forward a branch that has no commits of its own
func (g *Git) ResetHard(ref string) error {
	_, err := g.run("reset", "--hard", ref)
	return err
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
// Explicitly specifies origin and branch name to handle branches without upstream
func (g *Git) PushForce() error {
	branch, err := g.CurrentBranch()
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}
	return g.RunInteractive("push", "--force-with-lease", "origin", branch)
}

// RemoveWorktree removes a worktree and optionally deletes the branch
func (g *Git) RemoveWorktree(worktreePath string, deleteBranch bool, branchName string) error {
	// Check if the worktree directory exists
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		// Worktree directory doesn't exist - just prune stale worktrees and delete branch
		g.run("worktree", "prune")
	} else {
		// First remove the worktree
		_, err := g.run("worktree", "remove", worktreePath)
		if err != nil {
			// Try force remove if regular remove fails
			_, err = g.run("worktree", "remove", "--force", worktreePath)
			if err != nil {
				// Check if the error is because it's not a working tree (already removed)
				if strings.Contains(err.Error(), "is not a working tree") {
					// Worktree already removed, just prune
					g.run("worktree", "prune")
				} else {
					return fmt.Errorf("failed to remove worktree: %w", err)
				}
			}
		}
	}

	// Optionally delete the branch
	if deleteBranch && branchName != "" {
		_, err := g.run("branch", "-D", branchName)
		if err != nil {
			// Branch might already be deleted or not exist
			if !strings.Contains(err.Error(), "not found") {
				return fmt.Errorf("worktree removed but failed to delete branch: %w", err)
			}
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

// GetPRTemplate finds and reads the GitHub PR template from common locations.
// Returns the template content or empty string if no template is found.
// GitHub looks for templates in these locations (in order of priority):
// - .github/pull_request_template.md
// - .github/PULL_REQUEST_TEMPLATE.md
// - docs/pull_request_template.md
// - pull_request_template.md
// - PULL_REQUEST_TEMPLATE.md
func (g *Git) GetPRTemplate() string {
	// Get the repo root
	repoRoot, err := g.GetRepoRoot()
	if err != nil {
		return ""
	}

	// List of possible template locations (in order of priority)
	templatePaths := []string{
		filepath.Join(repoRoot, ".github", "pull_request_template.md"),
		filepath.Join(repoRoot, ".github", "PULL_REQUEST_TEMPLATE.md"),
		filepath.Join(repoRoot, "docs", "pull_request_template.md"),
		filepath.Join(repoRoot, "docs", "PULL_REQUEST_TEMPLATE.md"),
		filepath.Join(repoRoot, "pull_request_template.md"),
		filepath.Join(repoRoot, "PULL_REQUEST_TEMPLATE.md"),
	}

	for _, path := range templatePaths {
		content, err := os.ReadFile(path)
		if err == nil {
			return string(content)
		}
	}

	return ""
}
