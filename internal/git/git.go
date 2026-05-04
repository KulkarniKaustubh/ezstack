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

// Git wraps git operations
type Git struct {
	RepoDir string
}

// New creates a new Git wrapper for the given repo directory
func New(repoDir string) *Git {
	return &Git{RepoDir: repoDir}
}

// run executes a git command and returns the output
// RunCapture executes a git command and returns stdout.
func (g *Git) RunCapture(args ...string) (string, error) {
	return g.run(args...)
}

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

// runWithSpinner executes a git command with a delayed progress indicator
// (when ProgressStart is wired). When the hook is nil this is equivalent
// to plain run.
func (g *Git) runWithSpinner(message string, args ...string) (string, error) {
	defer startProgress(message)()
	return g.run(args...)
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

// CreateBranchOnly creates a new branch without a worktree
func (g *Git) CreateBranchOnly(branchName, baseBranch string) error {
	_, err := g.run("branch", branchName, baseBranch)
	return err
}

// CheckoutBranch switches to an existing branch
func (g *Git) CheckoutBranch(branchName string) error {
	_, err := g.run("checkout", branchName)
	return err
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

// CreateWorktreeFromRemoteBranch creates a worktree for a remote branch with tracking.
// If the local branch already exists (and has no worktree), it is force-updated to match the remote.
// The resulting local branch tracks origin/<localBranch>.
func (g *Git) CreateWorktreeFromRemoteBranch(localBranch, worktreePath string) error {
	remoteBranch := "origin/" + localBranch

	if g.BranchExists(localBranch) {
		// Force-update existing local branch to match remote
		if _, err := g.run("branch", "-f", localBranch, remoteBranch); err != nil {
			return fmt.Errorf("failed to update local branch '%s' to match remote: %w", localBranch, err)
		}
		// Ensure tracking is set up. Non-fatal: worktree creation still
		// succeeds without upstream tracking; the user can set it later.
		if _, err := g.run("branch", "--set-upstream-to="+remoteBranch, localBranch); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: failed to set upstream for %s: %v\n", localBranch, err)
		}
	} else {
		// Create new branch tracking remote
		if _, err := g.run("branch", "--track", localBranch, remoteBranch); err != nil {
			return fmt.Errorf("failed to create tracking branch '%s': %w", localBranch, err)
		}
	}

	// Create the worktree
	_, err := g.runWithSpinner(fmt.Sprintf("Creating worktree for %s...", localBranch), "worktree", "add", worktreePath, localBranch)
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
	var isDetached bool
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			if current.Path != "" {
				// If detached and no branch found, try to get branch from rebase state
				if isDetached && current.Branch == "" {
					current.Branch = getBranchFromRebaseState(current.Path)
				}
				worktrees = append(worktrees, current)
			}
			current = Worktree{Path: strings.TrimPrefix(line, "worktree ")}
			isDetached = false
		} else if strings.HasPrefix(line, "branch ") {
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		} else if line == "detached" {
			isDetached = true
		}
	}
	if current.Path != "" {
		// Handle the last worktree
		if isDetached && current.Branch == "" {
			current.Branch = getBranchFromRebaseState(current.Path)
		}
		worktrees = append(worktrees, current)
	}
	return worktrees, nil
}

// BranchFromRebaseState returns the branch name recorded in this repo's
// rebase-state files (.git/rebase-merge/head-name or .git/rebase-apply/head-name).
// During a rebase HEAD is detached, so `git rev-parse --abbrev-ref HEAD`
// returns "HEAD" — but ezstack still needs to know which branch the worktree
// is "really" on so commands like `ezs sync -s --continue` can resolve the
// current stack. Returns "" when no rebase is in progress or the file can't
// be read.
func (g *Git) BranchFromRebaseState() string {
	return getBranchFromRebaseState(g.RepoDir)
}

// getBranchFromRebaseState tries to get the original branch name from rebase state files
// This is useful when a worktree is in the middle of a rebase (detached HEAD)
func getBranchFromRebaseState(worktreePath string) string {
	// For worktrees, the git dir is in .git file pointing to the actual git dir
	gitDir := filepath.Join(worktreePath, ".git")

	// Check if .git is a file (worktree) or directory (main repo)
	info, err := os.Stat(gitDir)
	if err != nil {
		return ""
	}

	if !info.IsDir() {
		// It's a worktree - read the gitdir from the .git file
		content, err := os.ReadFile(gitDir)
		if err != nil {
			return ""
		}
		// Format: "gitdir: /path/to/git/worktrees/name"
		gitDir = strings.TrimPrefix(strings.TrimSpace(string(content)), "gitdir: ")
	}

	// Try rebase-merge first (interactive rebase), then rebase-apply (git am style)
	for _, rebaseDir := range []string{"rebase-merge", "rebase-apply"} {
		headNameFile := filepath.Join(gitDir, rebaseDir, "head-name")
		content, err := os.ReadFile(headNameFile)
		if err == nil {
			branchRef := strings.TrimSpace(string(content))
			return strings.TrimPrefix(branchRef, "refs/heads/")
		}
	}

	return ""
}

// Worktree represents a git worktree
type Worktree struct {
	Path   string
	Branch string
}

// Fetch fetches from origin remote
func (g *Git) Fetch() error {
	// Use "origin" instead of "--all" to avoid hanging on slow/unreachable remotes
	_, err := g.runWithSpinner("Fetching from remote...", "fetch", "origin", "--prune")
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

// GetLastCommitMessageOf returns the message of the tip commit on a specific
// branch ref. Companion to GetLastCommitMessage for callers like
// `pr create --branch <other>` where HEAD is not the branch the PR is for.
func (g *Git) GetLastCommitMessageOf(branch string) (string, error) {
	return g.run("log", "-1", "--format=%s", branch)
}

// IsAncestor checks whether `ancestor` is reachable from `descendant`. Wraps
// `git merge-base --is-ancestor`. Returns (false, nil) when the answer is no
// (exit code 1) and (false, err) for any other failure (bad refs, etc.).
//
// Used by lookupPreSyncSHA to detect snapshots that have been invalidated by
// out-of-band history rewrites (e.g. a manual `git rebase` between ezstack
// runs): if the recorded SHA is no longer reachable from the branch tip,
// git's `--onto staleSHA` would compute a bogus commit range, so the caller
// must discard the snapshot and fall back to plain rebase.
func (g *Git) IsAncestor(ancestor, descendant string) (bool, error) {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", ancestor, descendant)
	cmd.Dir = g.RepoDir
	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
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

// GetCommitsBehind returns the number of commits branch is behind target
func (g *Git) GetCommitsBehind(branch, target string) (int, error) {
	output, err := g.run("rev-list", "--count", branch+".."+target)
	if err != nil {
		return 0, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return 0, fmt.Errorf("failed to parse commit count: %w", err)
	}
	return count, nil
}

// GetCommitsAhead returns the number of commits branch is ahead of target
func (g *Git) GetCommitsAhead(branch, target string) (int, error) {
	output, err := g.run("rev-list", "--count", target+".."+branch)
	if err != nil {
		return 0, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return 0, fmt.Errorf("failed to parse commit count: %w", err)
	}
	return count, nil
}

// GetAheadBehind returns the divergence between two refs in a single git
// invocation: ahead = commits in `local` not reachable from `remote`,
// behind = commits in `remote` not reachable from `local`.
//
// Use this instead of two GetCommitsAhead/GetCommitsBehind calls when you
// need both numbers — git computes them simultaneously here and the result
// is consistent against a single snapshot of the object database.
func (g *Git) GetAheadBehind(local, remote string) (ahead, behind int, err error) {
	output, err := g.run("rev-list", "--left-right", "--count", local+"..."+remote)
	if err != nil {
		return 0, 0, err
	}
	parts := strings.Fields(strings.TrimSpace(output))
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output: %q", output)
	}
	ahead, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("parse ahead: %w", err)
	}
	behind, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("parse behind: %w", err)
	}
	return ahead, behind, nil
}

// FastForwardMerge runs `git merge --ff-only target` in the current repo.
// Used by sync's remote-integration step to advance a branch to its remote's
// new tip when the local has no diverging commits. Returns RebaseResult so
// callers can use the same conflict-handling shape as RebaseNonInteractive.
//
// The --ff-only flag rejects any merge that would require a real merge
// commit, so this is safe to call even when the caller hasn't pre-checked
// divergence — git will refuse rather than create unexpected history.
func (g *Git) FastForwardMerge(target string) RebaseResult {
	cmd := exec.Command("git", "merge", "--ff-only", target)
	cmd.Dir = g.RepoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		combined := stdout.String() + stderr.String()
		// --ff-only doesn't produce a CONFLICT in the merge sense — it just
		// refuses on divergence — but a worktree-state error or refusal is
		// still a real failure for our purposes.
		return RebaseResult{Error: fmt.Errorf("fast-forward failed: %s", strings.TrimSpace(combined))}
	}
	return RebaseResult{Success: true}
}

// GetDiffStat returns the total lines added and removed between base and head.
// Uses three-dot diff (merge-base) to show only changes introduced by head,
// not changes on base that head doesn't have.
func (g *Git) GetDiffStat(base, head string) (added int, removed int, err error) {
	output, err := g.run("diff", "--shortstat", base+"..."+head)
	if err != nil {
		return 0, 0, err
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return 0, 0, nil
	}
	// Parse "N insertions(+)" and "N deletions(-)" from the shortstat line
	for _, part := range strings.Split(output, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "insertion") {
			if n, parseErr := strconv.Atoi(strings.Fields(part)[0]); parseErr == nil {
				added = n
			}
		} else if strings.Contains(part, "deletion") {
			if n, parseErr := strconv.Atoi(strings.Fields(part)[0]); parseErr == nil {
				removed = n
			}
		}
	}
	return added, removed, nil
}

// IsLocalAheadOfRemote checks if the local branch has commits not in the remote.
// If remote is empty, defaults to "origin".
// Returns true if local is ahead (needs push), false if in sync or behind.
//
// Distinguishes "remote ref doesn't exist" (treat as ahead — first push) from
// transient or unrelated rev-parse failures (lock contention, repo corruption,
// bad ref name) which are surfaced as errors. Returning (true, nil) on any
// error would let downstream code push to a remote ref that may not be the
// one we expected.
func (g *Git) IsLocalAheadOfRemote(branch string, remote string) (bool, error) {
	if remote == "" {
		remote = "origin"
	}
	remoteBranch := remote + "/" + branch
	if _, err := g.run("rev-parse", "--verify", remoteBranch); err != nil {
		if isMissingRefError(err) {
			return true, nil
		}
		return false, fmt.Errorf("rev-parse %s: %w", remoteBranch, err)
	}
	ahead, err := g.GetCommitsAhead(branch, remoteBranch)
	if err != nil {
		return false, err
	}
	return ahead > 0, nil
}

// isMissingRefError returns true if err's message looks like git's
// "ref not found" output from `rev-parse --verify`. Git emits one of:
//
//	"fatal: Needed a single revision"
//	"fatal: ambiguous argument 'X': unknown revision or path not in the working tree."
//	"fatal: bad revision 'X'"
//
// These all mean "the ref doesn't exist". Anything else (lock contention,
// repo corruption, transient errors) is propagated as a real error so the
// caller can decide how to react.
func isMissingRefError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unknown revision") ||
		strings.Contains(msg, "Needed a single revision") ||
		strings.Contains(msg, "bad revision") ||
		strings.Contains(msg, "not a valid object name")
}

// RemoteBranchExists checks if a branch exists on `origin`.
// Use RemoteHasBranch when the remote may not be `origin` (e.g. fork remotes).
func (g *Git) RemoteBranchExists(branch string) bool {
	return g.RemoteHasBranch("origin", branch)
}

// RemoteHasBranch checks if `branch` exists on the given remote. Defaults to
// `origin` if remote is empty.
func (g *Git) RemoteHasBranch(remote, branch string) bool {
	if remote == "" {
		remote = "origin"
	}
	_, err := g.run("rev-parse", "--verify", remote+"/"+branch)
	return err == nil
}

// RefExists checks whether an arbitrary git ref resolves to a real object
// in the repository. Useful for symbolic refs (branches, tags). Combines
// `rev-parse --verify` (parses the ref to a SHA) with `cat-file -e` (proves
// the SHA names an actual object) — `rev-parse --verify` alone accepts any
// 40-char hex string as a "valid" SHA even when the object doesn't exist,
// which is too lenient for snapshot validity checks.
func (g *Git) RefExists(ref string) bool {
	sha, err := g.run("rev-parse", "--verify", ref)
	if err != nil {
		return false
	}
	cmd := exec.Command("git", "cat-file", "-e", strings.TrimSpace(sha))
	cmd.Dir = g.RepoDir
	return cmd.Run() == nil
}

// ListLocalBranches returns all local branch names
func (g *Git) ListLocalBranches() ([]string, error) {
	output, err := g.run("for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, nil
	}
	return strings.Split(output, "\n"), nil
}

// BranchExists checks if a local branch exists
func (g *Git) BranchExists(branch string) bool {
	_, err := g.run("rev-parse", "--verify", "refs/heads/"+branch)
	return err == nil
}

// ValidateBranchName checks if a name is valid for a git branch.
func ValidateBranchName(name string) error {
	if name == "" {
		return fmt.Errorf("branch name cannot be empty")
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("branch name cannot start with '-'")
	}
	cmd := exec.Command("git", "check-ref-format", "--branch", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("invalid branch name '%s': contains forbidden characters or patterns", name)
	}
	return nil
}

// HasDivergedFromRemote checks if local and remote branches have diverged.
// Returns (hasDiverged, localAhead, remoteBehind, error). hasDiverged is
// true only when local has commits not in remote AND remote has commits
// not in local. If the remote ref doesn't exist yet, returns (false, 0, 0,
// nil) — the branch just hasn't been pushed. Other rev-parse failures are
// surfaced as errors so callers can distinguish "no remote yet" from real
// repo problems. If remote is empty, defaults to "origin".
//
// Uses a single `git rev-list --left-right --count` invocation so the
// ahead/behind counts are computed against one consistent snapshot of the
// object database. The previous two-call form (GetCommitsAhead +
// GetCommitsBehind) could see a stale upper bound if a concurrent fetch /
// prune landed between calls.
func (g *Git) HasDivergedFromRemote(branch, remote string) (bool, int, int, error) {
	if remote == "" {
		remote = "origin"
	}
	remoteBranch := remote + "/" + branch
	if _, err := g.run("rev-parse", "--verify", remoteBranch); err != nil {
		if isMissingRefError(err) {
			return false, 0, 0, nil
		}
		return false, 0, 0, fmt.Errorf("rev-parse %s: %w", remoteBranch, err)
	}

	localAhead, remoteBehind, err := g.GetAheadBehind(branch, remoteBranch)
	if err != nil {
		return false, 0, 0, err
	}

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
	defer startProgress(fmt.Sprintf("Rebasing onto %s...", target))()

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
	defer startProgress(fmt.Sprintf("Rebasing onto %s...", newBase))()

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

// RebaseOnto interactively rebases commits in `oldBase..HEAD` onto newBase.
// Used by stack-aware callers that know the OLD parent SHA the current branch
// was sitting on top of, so they can avoid replaying the parent's own commits
// when the parent has been rewritten. When oldBase equals newBase this is
// equivalent to plain `git rebase newBase`.
func (g *Git) RebaseOnto(newBase, oldBase string) error {
	return g.RunInteractive("rebase", "--onto", newBase, oldBase)
}

// MergeNonInteractive merges target into the current branch without interactive mode
// Returns structured result for conflict handling, matching RebaseResult for compatibility
func (g *Git) MergeNonInteractive(target string) RebaseResult {
	defer startProgress(fmt.Sprintf("Merging %s...", target))()

	cmd := exec.Command("git", "merge", target, "--no-edit")
	cmd.Dir = g.RepoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// git merge outputs conflict info to stdout, not stderr
		combined := stdout.String() + stderr.String()
		if strings.Contains(combined, "CONFLICT") ||
			strings.Contains(combined, "Automatic merge failed") ||
			strings.Contains(combined, "fix conflicts") {
			return RebaseResult{HasConflict: true, Error: fmt.Errorf("merge conflict")}
		}
		return RebaseResult{Error: fmt.Errorf("merge failed: %s", combined)}
	}
	return RebaseResult{Success: true}
}

// Merge merges target into the current branch interactively
func (g *Git) Merge(target string) error {
	return g.RunInteractive("merge", target)
}

// StashPush stashes all changes including untracked files
func (g *Git) StashPush() error {
	_, err := g.run("stash", "push", "-u", "-m", "ezstack-autostash")
	return err
}

// StashPop pops the ezstack autostash entry for the current branch.
// Uses targeted lookup to avoid popping user stashes or stashes from other branches.
// Returns nil if no ezstack stash is found (nothing to pop).
func (g *Git) StashPop() error {
	branch, _ := g.CurrentBranch()
	if branch == "" || branch == "HEAD" {
		// Detached HEAD (e.g. mid-rebase): don't do a blind `git stash pop`
		// — that would pop the top of the stash stack, which could be an
		// unrelated user stash or a stash from another worktree. Instead,
		// look for the most recent ezstack-autostash entry by message only.
		idx, found := g.FindAnyEzstackStash()
		if !found {
			return nil
		}
		return g.StashPopIndex(idx)
	}
	idx, found := g.FindEzstackStash(branch)
	if !found {
		return nil // no ezstack stash to pop
	}
	return g.StashPopIndex(idx)
}

// FindAnyEzstackStash returns the most recent stash entry tagged
// "ezstack-autostash" regardless of branch. Used only in the detached-HEAD
// fallback path where the current branch is unknown.
func (g *Git) FindAnyEzstackStash() (int, bool) {
	output, err := g.run("stash", "list")
	if err != nil || output == "" {
		return -1, false
	}
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "ezstack-autostash") {
			continue
		}
		start := strings.Index(line, "stash@{")
		if start == -1 {
			continue
		}
		end := strings.Index(line[start:], "}")
		if end == -1 {
			continue
		}
		idx, err := strconv.Atoi(line[start+7 : start+end])
		if err != nil {
			continue
		}
		return idx, true
	}
	return -1, false
}

// FindEzstackStash finds the stash index of an ezstack autostash entry for a specific branch.
// Git stash entries look like: "stash@{N}: On <branch>: ezstack-autostash"
// Returns (index, true) if found, (-1, false) if not found.
// Matches both branch name and message to avoid touching stashes from other worktrees.
func (g *Git) FindEzstackStash(branchName string) (int, bool) {
	output, err := g.run("stash", "list")
	if err != nil || output == "" {
		return -1, false
	}

	needle := "On " + branchName + ": ezstack-autostash"
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, needle) {
			// Parse stash index from "stash@{N}: ..."
			start := strings.Index(line, "stash@{")
			if start == -1 {
				continue
			}
			end := strings.Index(line[start:], "}")
			if end == -1 {
				continue
			}
			idxStr := line[start+7 : start+end]
			idx, err := strconv.Atoi(idxStr)
			if err != nil {
				continue
			}
			return idx, true
		}
	}
	return -1, false
}

// StashPopIndex pops a specific stash entry by index.
func (g *Git) StashPopIndex(index int) error {
	_, err := g.run("stash", "pop", fmt.Sprintf("stash@{%d}", index))
	return err
}

// HasChanges returns true if the working directory has uncommitted changes
func (g *Git) HasChanges() (bool, error) {
	output, err := g.run("status", "--porcelain")
	if err != nil {
		return false, err
	}
	return output != "", nil
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

// FindRemoteByOwner finds a git remote that points to a repo owned by the given GitHub owner.
// Returns the remote name and URL, or empty strings if not found.
func (g *Git) FindRemoteByOwner(owner string) (string, string, error) {
	output, err := g.run("remote", "-v")
	if err != nil {
		return "", "", err
	}
	lowerOwner := strings.ToLower(owner)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		remoteName := fields[0]
		remoteURL := fields[1]
		// Match owner in SSH (git@github.com:owner/) or HTTPS (github.com/owner/)
		lowerURL := strings.ToLower(remoteURL)
		if strings.Contains(lowerURL, ":"+lowerOwner+"/") || strings.Contains(lowerURL, "/"+lowerOwner+"/") {
			return remoteName, remoteURL, nil
		}
	}
	return "", "", nil
}

// AddRemote adds a new git remote
func (g *Git) AddRemote(name, url string) error {
	_, err := g.run("remote", "add", name, url)
	return err
}

// FetchRemote fetches from a specific named remote (e.g. a fork). Used by
// integrate flows for non-origin remotes; the default Fetch() only handles
// origin to avoid hanging on unreachable remotes by default. Adds --prune
// so deleted remote branches stop showing as tracking refs.
func (g *Git) FetchRemote(remote string) error {
	if remote == "" {
		return fmt.Errorf("FetchRemote: empty remote name")
	}
	_, err := g.runWithSpinner("Fetching from "+remote+"...", "fetch", remote, "--prune")
	return err
}

// PushForce force pushes the current branch with lease (safer than --force).
// If remote is empty, defaults to "origin".
//
// Deprecated: prefer PushForceBranch(branch, remote). PushForce consults
// CurrentBranch() at call time, which is wrong when the active checkout
// has been transiently swapped (e.g. syncViaCheckout restored HEAD to
// main) or when the caller acts on a branch other than HEAD.
func (g *Git) PushForce(remote ...string) error {
	r := "origin"
	if len(remote) > 0 && remote[0] != "" {
		r = remote[0]
	}
	branch, err := g.CurrentBranch()
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}
	return g.RunInteractive("push", "--force-with-lease", r, branch)
}

// PushForceBranch force-pushes a specific branch with lease (safer than
// --force). If remote is empty, defaults to "origin". Prefer this over
// PushForce — it never reads CurrentBranch().
func (g *Git) PushForceBranch(branch string, remote ...string) error {
	r := "origin"
	if len(remote) > 0 && remote[0] != "" {
		r = remote[0]
	}
	return g.RunInteractive("push", "--force-with-lease", r, branch)
}

// PruneWorktrees prunes stale worktree metadata from git
func (g *Git) PruneWorktrees() error {
	_, err := g.run("worktree", "prune")
	return err
}

// DeleteBranch deletes a local git branch
func (g *Git) DeleteBranch(branchName string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := g.run("branch", flag, branchName)
	return err
}

// RemoveWorktree removes a worktree and optionally deletes the branch
func (g *Git) RemoveWorktree(worktreePath string, deleteBranch bool, branchName string) error {
	// Capture the main worktree *before* we remove anything. Once we remove
	// the worktree, g.RepoDir may point at a directory that no longer exists,
	// and then `git rev-parse --git-common-dir` will fail with chdir errors,
	// which previously broke the branch-deletion step below.
	mainWT, _ := g.GetMainWorktree()

	// Check if the worktree directory exists
	_, statErr := os.Stat(worktreePath)
	if statErr != nil && !os.IsNotExist(statErr) {
		// Non-ENOENT error (permission denied, broken symlink, etc.)
		return fmt.Errorf("failed to access worktree path '%s': %w", worktreePath, statErr)
	}

	if os.IsNotExist(statErr) {
		// Worktree directory doesn't exist - just prune stale worktrees and delete branch
		if _, err := g.run("worktree", "prune"); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to prune worktrees: %v\n", err)
		}
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
					if _, pruneErr := g.run("worktree", "prune"); pruneErr != nil {
						fmt.Fprintf(os.Stderr, "Warning: failed to prune worktrees: %v\n", pruneErr)
					}
				} else {
					return fmt.Errorf("failed to remove worktree: %w", err)
				}
			}
		}
	}

	// Optionally delete the branch.
	// Use the main worktree for this command since g.RepoDir may point to
	// the worktree we just deleted (causing chdir errors).
	if deleteBranch && branchName != "" {
		branchGit := g
		// Prefer the pre-captured main worktree if g.RepoDir is gone or
		// differs from main. This keeps us working even if the worktree we
		// just removed was the one g was pointing at.
		if mainWT != "" && mainWT != g.RepoDir {
			branchGit = New(mainWT)
		} else if _, err := os.Stat(g.RepoDir); err != nil {
			// g.RepoDir vanished and we don't know main — last-ditch fallback.
			if recovered, err := New(".").GetMainWorktree(); err == nil && recovered != "" {
				branchGit = New(recovered)
			}
		}
		_, err := branchGit.run("branch", "-D", branchName)
		if err != nil {
			// Branch might already be deleted or not exist
			if !strings.Contains(err.Error(), "not found") {
				return fmt.Errorf("worktree removed but failed to delete branch: %w", err)
			}
		}
	}

	return nil
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
