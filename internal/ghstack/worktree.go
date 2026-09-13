package ghstack

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
)

// detached records a worktree we temporarily moved off its branch so gh-stack
// could check that branch out in the main worktree.
type detached struct {
	path   string
	branch string
}

// WithDetachedWorktrees runs fn (a mutating gh-stack operation such as rebase,
// sync, or push) with every LINKED worktree that holds one of `branches`
// temporarily detached from its branch, then re-attaches them afterward.
//
// This is mandatory choreography: gh-stack cascades a rebase by checking out
// each branch in the main worktree, and git refuses to check out a branch that
// is already checked out in another worktree. The Phase 0 spike confirmed both
// the failure ("fatal: '<b>' is already used by worktree at ...") and that
// detach -> run -> re-attach cleanly rescues it (re-attaching moves the worktree
// to the branch's rewritten tip, which is exactly the reconcile we want).
//
// The main worktree (repoDir) is never detached — it is the driver gh-stack
// itself checks branches out in. Worktrees already in detached HEAD, or on a
// branch not in `branches`, are left untouched.
//
// Re-attach always runs (even if fn fails or panics) so worktrees are never
// left stranded in detached HEAD. If a required detach fails (e.g. a worktree
// has uncommitted changes) the operation is aborted BEFORE fn runs and any
// already-detached worktrees are restored, because running the op with a
// partial detach would collide.
func WithDetachedWorktrees(repoDir string, branches []string, fn func() error) (err error) {
	want := make(map[string]struct{}, len(branches))
	for _, b := range branches {
		want[b] = struct{}{}
	}

	g := git.New(repoDir)
	worktrees, err := g.ListWorktrees()
	if err != nil {
		return fmt.Errorf("ghstack: list worktrees: %w", err)
	}

	cleanRepo := filepath.Clean(repoDir)
	var moved []detached
	restore := func() error {
		var errs []string
		// Re-attach in reverse so nested/ordering assumptions never bite.
		for i := len(moved) - 1; i >= 0; i-- {
			d := moved[i]
			if _, e := gitAt(d.path, "checkout", d.branch); e != nil {
				errs = append(errs, fmt.Sprintf("%s -> %s: %v", d.path, d.branch, e))
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("ghstack: failed to re-attach worktree(s): %s", strings.Join(errs, "; "))
		}
		return nil
	}

	for _, w := range worktrees {
		if w.Branch == "" { // already detached / bare
			continue
		}
		if filepath.Clean(w.Path) == cleanRepo { // the driver worktree
			continue
		}
		if _, ok := want[w.Branch]; !ok {
			continue
		}
		if _, e := gitAt(w.Path, "checkout", "--detach"); e != nil {
			// Abort: restore what we already detached, then surface the error.
			restoreErr := restore()
			if restoreErr != nil {
				return fmt.Errorf("ghstack: could not detach worktree %s (%v); additionally %v", w.Path, e, restoreErr)
			}
			return fmt.Errorf("ghstack: could not detach worktree %s off %q (uncommitted changes?): %w", w.Path, w.Branch, e)
		}
		moved = append(moved, detached{path: w.Path, branch: w.Branch})
	}

	// Always re-attach, and surface a restore failure if fn itself succeeded.
	defer func() {
		if restoreErr := restore(); restoreErr != nil && err == nil {
			err = restoreErr
		}
	}()

	return fn()
}

// gitAt runs `git -C dir <args...>` and returns trimmed stdout. Small local
// helper so the choreography doesn't need a git.Git bound to each worktree.
func gitAt(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
