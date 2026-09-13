// Package ghstack adapts the `gh stack` CLI extension (github/gh-stack) so
// ezstack can delegate stack membership, cascading rebase/sync, push, and PR
// creation/linking to it while keeping ezstack's worktree-per-branch layout
// and its own UX on top.
//
// Design constraints discovered during the Phase 0 spike (see the package
// tests, which reproduce them against a throwaway repo):
//
//   - gh-stack rebases by checking out each branch in ONE working directory.
//     git refuses to check out a branch that is already checked out in another
//     worktree ("fatal: '<b>' is already used by worktree at ..."). So every
//     mutating gh-stack operation (rebase/sync/push) MUST be wrapped in the
//     detach-and-reconcile choreography in worktree.go, which temporarily
//     detaches each per-branch worktree, runs the op in the main worktree, then
//     re-attaches. Read-only ops (view/state) do not need it.
//   - `.git/gh-stack` is gh-stack's on-disk state (schemaVersion 1). Each stack
//     is a strictly LINEAR chain; the file's top-level `stacks` array may hold
//     several. ezstack's tree-shaped stacks are represented as one gh-stack
//     stack per root->leaf path (the "tree overlay").
//   - We read structured state through `gh stack view --json` where possible
//     (a stabler contract than the raw file) and fall back to reading
//     `.git/gh-stack` only to ENUMERATE all stacks, which `view` does not do.
package ghstack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Sentinel errors mapped from gh-stack's documented exit codes so callers can
// branch on failure mode with errors.Is instead of scraping stderr.
var (
	ErrNotInStack     = errors.New("ghstack: not in a stack / stack not found")      // exit 2
	ErrRebaseConflict = errors.New("ghstack: rebase conflict")                       // exit 3
	ErrGitHubAPI      = errors.New("ghstack: GitHub API failure")                    // exit 4
	ErrInvalidArgs    = errors.New("ghstack: invalid arguments")                     // exit 5
	ErrDisambiguation = errors.New("ghstack: disambiguation required")               // exit 6
	ErrRebaseActive   = errors.New("ghstack: a rebase is already in progress")       // exit 7
	ErrLocked         = errors.New("ghstack: stack is locked by another process")    // exit 8
	ErrNotInstalled   = errors.New("ghstack: `gh stack` extension is not installed") // gh reports it as unknown command
)

// exitCodeErr maps a gh-stack process exit code to a sentinel error. Code 0 is
// success (nil); 1 and anything unrecognized fall through to a generic error
// built by the caller (which still carries stderr).
func exitCodeErr(code int) error {
	switch code {
	case 2:
		return ErrNotInStack
	case 3:
		return ErrRebaseConflict
	case 4:
		return ErrGitHubAPI
	case 5:
		return ErrInvalidArgs
	case 6:
		return ErrDisambiguation
	case 7:
		return ErrRebaseActive
	case 8:
		return ErrLocked
	default:
		return nil
	}
}

// stateSchemaVersion is the `.git/gh-stack` schemaVersion this adapter was
// written against. A newer file is refused rather than mis-parsed, mirroring
// how ezstack guards its own stacks.json version.
const stateSchemaVersion = 1

// Client drives the `gh stack` CLI for a single repository. All commands run
// with cmd.Dir set to repoDir because gh-stack operates on the current working
// directory's repo, and the harness resets process cwd between invocations.
type Client struct {
	repoDir string // main worktree / repo root where gh-stack state lives
}

// New returns a Client rooted at repoDir. repoDir should be the main worktree
// (where `.git/gh-stack` is stored), not a linked per-branch worktree.
func New(repoDir string) *Client {
	return &Client{repoDir: repoDir}
}

// Branch is one layer of a stack as reported by `gh stack view --json`.
type Branch struct {
	Name        string `json:"name"`
	Base        string `json:"base"` // NB: gh-stack reports base as a commit SHA, not a branch name
	IsCurrent   bool   `json:"isCurrent"`
	IsMerged    bool   `json:"isMerged"`
	IsQueued    bool   `json:"isQueued"`
	NeedsRebase bool   `json:"needsRebase"`
}

// View is the parsed output of `gh stack view --json` for the CURRENT stack.
type View struct {
	Trunk         string   `json:"trunk"`
	CurrentBranch string   `json:"currentBranch"`
	Branches      []Branch `json:"branches"`
}

// State mirrors the on-disk `.git/gh-stack` file. Used only to enumerate ALL
// stacks in the repo (view reports just the current one). Treat as opaque
// beyond the fields declared here — the schemaVersion guard protects us if the
// format changes under a newer gh-stack.
type State struct {
	SchemaVersion int          `json:"schemaVersion"`
	Repository    string       `json:"repository"`
	Stacks        []StackState `json:"stacks"`
}

// StackState is a single linear stack within State.
type StackState struct {
	Trunk struct {
		Branch string `json:"branch"`
		Head   string `json:"head"`
	} `json:"trunk"`
	Branches []struct {
		Branch string `json:"branch"`
		Base   string `json:"base"`
	} `json:"branches"`
}

// BranchNames returns the ordered (bottom->top) branch names of the stack.
func (s StackState) BranchNames() []string {
	names := make([]string, 0, len(s.Branches))
	for _, b := range s.Branches {
		names = append(names, b.Branch)
	}
	return names
}

// run executes `gh stack <args...>` in repoDir and returns stdout. On failure
// it maps the exit code to a sentinel error where possible, otherwise returns
// a generic error carrying stderr. gh reports a missing extension by writing an
// "unknown command" style message to stderr with a non-mapped code; we detect
// that and return ErrNotInstalled.
func (c *Client) run(args ...string) (string, error) {
	full := append([]string{"stack"}, args...)
	cmd := exec.Command("gh", full...)
	cmd.Dir = c.repoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}

	stderrStr := strings.TrimSpace(stderr.String())
	if strings.Contains(stderrStr, "unknown command") || strings.Contains(stderrStr, "not a gh command") {
		return "", ErrNotInstalled
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if mapped := exitCodeErr(exitErr.ExitCode()); mapped != nil {
			// Preserve stderr context alongside the sentinel.
			return stdout.String(), fmt.Errorf("%w: %s", mapped, stderrStr)
		}
	}
	return stdout.String(), fmt.Errorf("gh stack %s failed: %v\n%s", strings.Join(args, " "), err, stderrStr)
}

// Installed reports whether the `gh stack` extension is available.
func Installed() bool {
	cmd := exec.Command("gh", "stack", "--help")
	return cmd.Run() == nil
}

// Init creates a new linear stack of the given branches (bottom->top). Existing
// branches are adopted; missing ones are created. base overrides the trunk when
// non-empty.
func (c *Client) Init(base string, branches ...string) error {
	args := []string{"init"}
	if base != "" {
		args = append(args, "--base", base)
	}
	args = append(args, branches...)
	_, err := c.run(args...)
	return err
}

// Add puts a new branch on top of the current stack. When branch is empty,
// gh-stack auto-names it (only meaningful together with a commit message, which
// this adapter does not drive — ezstack always passes an explicit name).
func (c *Client) Add(branch string) error {
	args := []string{"add"}
	if branch != "" {
		args = append(args, branch)
	}
	_, err := c.run(args...)
	return err
}

// RebaseOptions controls a cascading rebase.
type RebaseOptions struct {
	NoTrunk   bool // skip fetching/rebasing with trunk; only inter-branch rebases
	Downstack bool // only trunk..current
	Upstack   bool // only current..top
	Continue  bool // resume after conflict resolution
	Abort     bool // abort and restore all branches
	Remote    string
}

// Rebase runs `gh stack rebase` with the given options. The caller is
// responsible for wrapping this in WithDetachedWorktrees when any stack branch
// lives in a linked worktree (see worktree.go) — otherwise git will refuse the
// checkout with an "already used by worktree" error.
func (c *Client) Rebase(o RebaseOptions) error {
	args := []string{"rebase"}
	switch {
	case o.Continue:
		args = append(args, "--continue")
	case o.Abort:
		args = append(args, "--abort")
	}
	if o.NoTrunk {
		args = append(args, "--no-trunk")
	}
	if o.Downstack {
		args = append(args, "--downstack")
	}
	if o.Upstack {
		args = append(args, "--upstack")
	}
	if o.Remote != "" {
		args = append(args, "--remote", o.Remote)
	}
	_, err := c.run(args...)
	return err
}

// SyncOptions controls a full fetch/rebase/push/link sync.
type SyncOptions struct {
	Prune  bool
	Remote string
}

// Sync runs `gh stack sync`. Note: gh-stack sync performs live network I/O and
// may prompt interactively when local and remote stacks diverge; ezstack must
// only invoke it from an interactive context (or after ruling out divergence).
// Must be wrapped in WithDetachedWorktrees, same as Rebase.
func (c *Client) Sync(o SyncOptions) error {
	args := []string{"sync"}
	if o.Prune {
		args = append(args, "--prune")
	}
	if o.Remote != "" {
		args = append(args, "--remote", o.Remote)
	}
	_, err := c.run(args...)
	return err
}

// Push pushes all active branches (`--force-with-lease --atomic` internally).
// Must be wrapped in WithDetachedWorktrees.
func (c *Client) Push() error {
	_, err := c.run("push")
	return err
}

// SubmitOptions controls PR creation.
type SubmitOptions struct {
	Auto   bool // skip editor, auto-generate titles
	Open   bool // mark PRs ready for review (vs draft)
	Remote string
	Base   string
}

// Submit creates/updates PRs for the stack and links them on GitHub. It owns
// the PR-body stack section — ezstack no longer stitches that itself.
func (c *Client) Submit(o SubmitOptions) error {
	args := []string{"submit"}
	if o.Auto {
		args = append(args, "--auto")
	}
	if o.Open {
		args = append(args, "--open")
	}
	if o.Remote != "" {
		args = append(args, "--remote", o.Remote)
	}
	if o.Base != "" {
		args = append(args, "--base", o.Base)
	}
	_, err := c.run(args...)
	return err
}

// View parses `gh stack view --json` for the current stack.
func (c *Client) View() (*View, error) {
	out, err := c.run("view", "--json")
	if err != nil {
		return nil, err
	}
	var v View
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, fmt.Errorf("ghstack: parse view --json: %w", err)
	}
	return &v, nil
}

// ReadState reads and parses `.git/gh-stack`, enumerating every stack in the
// repo. Returns (nil, nil) when the file does not exist (no stacks yet).
// Refuses a schemaVersion newer than this adapter understands.
func (c *Client) ReadState() (*State, error) {
	gitDir, err := c.resolveGitDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(gitDir, "gh-stack")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("ghstack: read state: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("ghstack: parse state: %w", err)
	}
	if st.SchemaVersion > stateSchemaVersion {
		return nil, fmt.Errorf("ghstack: .git/gh-stack schemaVersion %d is newer than supported (%d); upgrade ezstack", st.SchemaVersion, stateSchemaVersion)
	}
	return &st, nil
}

// resolveGitDir returns the repo's .git directory. Uses `git rev-parse
// --git-common-dir` so it resolves correctly whether repoDir is the main
// worktree or (defensively) a linked one — gh-stack stores its state in the
// common dir.
func (c *Client) resolveGitDir() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = c.repoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ghstack: resolve git dir: %v\n%s", err, strings.TrimSpace(stderr.String()))
	}
	dir := strings.TrimSpace(stdout.String())
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(c.repoDir, dir)
	}
	return dir, nil
}
