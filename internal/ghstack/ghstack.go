// Package ghstack drives the `gh stack` CLI extension (github/gh-stack), which
// is ezstack's engine for cascading rebases, stack pushes, and GitHub stacked-PR
// linking.
//
// Division of labour:
//
//   - ezstack owns the stack MODEL (stacks.json: tree shape, worktrees, names,
//     PR cache, agent sessions). It is the source of truth for structure.
//   - gh-stack owns EXECUTION: `gh stack rebase` (cascade, merged-PR skipping,
//     --continue/--abort recovery), `gh stack push`, and `gh stack link`.
//   - The gh-stack catalog (`<git-common-dir>/gh-stack`) is a derived
//     projection of the ezstack model, rewritten by Reconcile (reconcile.go)
//     before every gh-stack operation.
//
// gh-stack >= 0.2.0 is required: earlier releases rebased every branch in a
// single checkout, which collides with ezstack's worktree-per-branch layout.
// 0.2.0 runs each rebase step in the worktree that holds the branch.
package ghstack

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// MinVersion is the oldest gh-stack release ezstack can drive (first release
// with cross-worktree rebase and a repository-wide catalog).
var MinVersion = [3]int{0, 2, 0}

// InstallHint is appended to "not installed / too old" errors.
const InstallHint = "Install it with: gh extension install github/gh-stack\nUpgrade it with: gh extension upgrade stack"

// Sentinel errors mapped from gh-stack's exit codes (cmd/utils.go in
// github/gh-stack) so callers can branch with errors.Is.
var (
	ErrNotInStack        = errors.New("gh stack: branch is not in a stack")                          // exit 2
	ErrConflict          = errors.New("gh stack: rebase conflict")                                   // exit 3
	ErrGitHubAPI         = errors.New("gh stack: GitHub API failure")                                // exit 4
	ErrInvalidArgs       = errors.New("gh stack: invalid arguments")                                 // exit 5
	ErrDisambiguation    = errors.New("gh stack: branch belongs to multiple stacks")                 // exit 6
	ErrRebaseActive      = errors.New("gh stack: a rebase is already in progress")                   // exit 7
	ErrLocked            = errors.New("gh stack: stack is locked by another process")                // exit 8
	ErrStacksUnavailable = errors.New("gh stack: stacked PRs are not available for this repository") // exit 9
	ErrModifyRecovery    = errors.New("gh stack: an interrupted `gh stack modify` needs recovery")   // exit 10
	ErrNotInstalled      = errors.New("the `gh stack` extension is not installed")
	ErrTooOld            = errors.New("the installed `gh stack` extension is too old")
)

func exitCodeErr(code int) error {
	switch code {
	case 2:
		return ErrNotInStack
	case 3:
		return ErrConflict
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
	case 9:
		return ErrStacksUnavailable
	case 10:
		return ErrModifyRecovery
	}
	return nil
}

// Error is a failed gh-stack invocation. It unwraps to the exit-code sentinel
// (when the code is a known one) so errors.Is(err, ErrConflict) works.
type Error struct {
	Args     []string
	ExitCode int
	Stderr   string // last stderr line, for messages; full output was streamed
	sentinel error
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("gh stack %s failed (exit %d)", strings.Join(e.Args, " "), e.ExitCode)
	if e.sentinel != nil {
		msg = e.sentinel.Error()
	}
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

func (e *Error) Unwrap() error { return e.sentinel }

// Client runs gh-stack commands for one repository.
type Client struct {
	repoDir string    // main worktree; default working directory for commands
	out     io.Writer // where gh-stack's progress output is streamed
}

// New returns a Client rooted at repoDir (the repository's main worktree).
// Progress output is streamed to stderr, matching ezstack's own UI stream.
func New(repoDir string) *Client {
	return &Client{repoDir: repoDir, out: os.Stderr}
}

// WithOutput returns a copy of c that streams gh-stack output to w.
func (c *Client) WithOutput(w io.Writer) *Client {
	cp := *c
	cp.out = w
	return &cp
}

// command builds a non-interactive `gh stack` invocation. gh-stack only
// prompts when its stdout is a terminal, so callers must give it a non-TTY
// stdout; stdin is left empty. The update notifier is disabled because it
// would otherwise print upgrade nags into ezstack's output.
func command(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("gh", append([]string{"stack"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GH_STACK_NO_UPDATE_NOTIFIER=1")
	return cmd
}

// run executes `gh stack <args>` in dir, streaming its output to c.out.
// stdout goes through a pipe (never the terminal) to keep gh-stack
// non-interactive.
func (c *Client) run(dir string, args ...string) error {
	if err := Check(); err != nil {
		return err
	}
	cmd := command(dir, args...)
	tail := &tailBuffer{max: 4096}
	// exec copies stdout and stderr in separate goroutines, so each stream
	// gets its own line buffer; they share a lock on the destination.
	var mu sync.Mutex
	stdout := &hintRewriter{w: c.out, mu: &mu}
	stderr := &hintRewriter{w: c.out, mu: &mu}
	cmd.Stdout = stdout // not an *os.File, so exec gives gh-stack a pipe
	cmd.Stderr = io.MultiWriter(stderr, tail)
	err := cmd.Run()
	stdout.Flush()
	stderr.Flush()
	return wrapRunErr(args, err, tail.String())
}

// hintRewriter rewrites gh-stack's "what to run next" hints into their ezs
// equivalents, line by line, so users aren't sent to `gh stack` commands
// that would bypass ezstack (and drops hints for steps ezs does itself).
type hintRewriter struct {
	w       io.Writer
	mu      *sync.Mutex // guards w, which may be shared with another stream
	partial []byte
}

var hintReplacer = strings.NewReplacer(
	"gh stack rebase --continue", "ezs sync --continue",
	"gh stack rebase --abort", "ezs sync --abort",
)

func (h *hintRewriter) Write(p []byte) (int, error) {
	h.partial = append(h.partial, p...)
	for {
		i := bytes.IndexByte(h.partial, '\n')
		if i < 0 {
			return len(p), nil
		}
		h.emit(string(h.partial[:i+1]))
		h.partial = h.partial[i+1:]
	}
}

// Flush writes any trailing partial line.
func (h *hintRewriter) Flush() {
	if len(h.partial) > 0 {
		h.emit(string(h.partial))
		h.partial = nil
	}
}

func (h *hintRewriter) emit(line string) {
	plain := ansiRe.ReplaceAllString(line, "")
	if strings.Contains(plain, "run `gh stack push`") || strings.Contains(plain, "run `gh stack submit`") {
		return
	}
	if h.mu != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
	}
	_, _ = io.WriteString(h.w, hintReplacer.Replace(line))
}

func wrapRunErr(args []string, err error, stderr string) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("gh stack %s: %w", strings.Join(args, " "), err)
	}
	return &Error{
		Args:     args,
		ExitCode: exitErr.ExitCode(),
		Stderr:   lastLine(stderr),
		sentinel: exitCodeErr(exitErr.ExitCode()),
	}
}

// RebaseOptions selects what `gh stack rebase` cascades.
type RebaseOptions struct {
	Branch    string // any member of the target stack; required
	Remote    string // remote to fetch the trunk from ("" = gh-stack auto-detect)
	Upstack   bool   // only Branch..top
	Downstack bool   // only bottom..Branch
	NoTrunk   bool   // don't fetch/rebase onto the trunk; only inter-branch rebases
}

// Rebase runs a cascading rebase of the stack containing o.Branch. Each step
// runs in the worktree that holds the branch; gh-stack refuses up front when an
// affected worktree is dirty. On conflict it returns an error wrapping
// ErrConflict and leaves the rebase paused for Continue/Abort.
func (c *Client) Rebase(o RebaseOptions) error {
	if o.Branch == "" {
		return fmt.Errorf("gh stack rebase: branch is required")
	}
	args := []string{"rebase", o.Branch}
	if o.Upstack {
		args = append(args, "--upstack")
	}
	if o.Downstack {
		args = append(args, "--downstack")
	}
	if o.NoTrunk {
		args = append(args, "--no-trunk")
	}
	if o.Remote != "" {
		args = append(args, "--remote", o.Remote)
	}
	return c.run(c.repoDir, args...)
}

// Continue resumes a paused rebase after conflicts were resolved and staged.
// gh-stack locates its recovery record through the common git dir, so this
// works from the main worktree regardless of where the conflict is.
func (c *Client) Continue() error {
	return c.run(c.repoDir, "rebase", "--continue")
}

// Abort abandons a paused rebase and restores every branch to its pre-rebase tip.
func (c *Client) Abort() error {
	return c.run(c.repoDir, "rebase", "--abort")
}

// Push pushes the active (non-merged, non-queued) branches of the stack whose
// member is checked out in dir, each with --force-with-lease.
func (c *Client) Push(dir, remote string) error {
	args := []string{"push"}
	if remote != "" {
		args = append(args, "--remote", remote)
	}
	return c.run(dir, args...)
}

// Link creates or updates the GitHub stack object for the given PR numbers
// (bottom to top). Passing PR numbers (not branch names) guarantees gh-stack
// neither pushes nor creates PRs; it only fixes bases and links. Requires at
// least two PRs.
func (c *Client) Link(prNumbers []int, base, remote string) error {
	if len(prNumbers) < 2 {
		return fmt.Errorf("gh stack link: need at least 2 PRs, got %d", len(prNumbers))
	}
	args := []string{"link"}
	if base != "" {
		args = append(args, "--base", base)
	}
	if remote != "" {
		args = append(args, "--remote", remote)
	}
	for _, n := range prNumbers {
		args = append(args, strconv.Itoa(n))
	}
	return c.run(c.repoDir, args...)
}

// ── Installation / version gate ───────────────────────────────────────────────

var (
	checkMu sync.Mutex
	checkOK bool
)

// Check verifies gh-stack is installed and at least MinVersion. Success is
// cached for the life of the process — the probe spawns `gh`, and ezstack may
// call into gh-stack several times per command — but failures are not, so a
// long-lived process (the MCP server) notices an install or upgrade.
func Check() error {
	checkMu.Lock()
	defer checkMu.Unlock()
	if checkOK {
		return nil
	}
	if err := probe(); err != nil {
		return err
	}
	checkOK = true
	return nil
}

func probe() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("%w: the GitHub CLI (gh) is not on PATH.\n%s", ErrNotInstalled, InstallHint)
	}
	var stdout bytes.Buffer
	cmd := command("", "--version")
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w.\n%s", ErrNotInstalled, InstallHint)
	}
	v, ok := ParseVersion(stdout.String())
	if !ok {
		// Local/dev builds report "dev"; trust them rather than blocking.
		return nil
	}
	if versionLess(v, MinVersion) {
		return fmt.Errorf("%w (found %d.%d.%d, need >= %d.%d.%d).\n%s", ErrTooOld,
			v[0], v[1], v[2], MinVersion[0], MinVersion[1], MinVersion[2], InstallHint)
	}
	return nil
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ParseVersion extracts the semantic version from `gh stack --version` output
// ("gh stack version 0.2.1"). ok is false for unversioned (dev) builds.
func ParseVersion(s string) (v [3]int, ok bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return v, false
	}
	for i := 0; i < 3; i++ {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

func versionLess(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// ── helpers ──────────────────────────────────────────────────────────────────

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }

// lastLine returns the last non-empty line of s, with ANSI escapes removed.
func lastLine(s string) string {
	lines := strings.Split(ansiRe.ReplaceAllString(s, ""), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\x1b\]8;;[^\x1b]*\x1b\\`)
