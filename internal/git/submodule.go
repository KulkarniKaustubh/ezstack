package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ListInitializedSubmodules returns the paths of submodules that are currently
// initialized in g.RepoDir. An empty slice is returned if the repo has no
// submodules or no submodules are initialized.
//
// `git submodule status` output format:
//
//	 <sha> <path> [(<desc>)]   -> initialized and checked out
//	-<sha> <path>              -> not initialized
//	+<sha> <path> [(<desc>)]   -> initialized, SHA differs from recorded commit
//	U<sha> <path>              -> merge conflicts (treat as initialized)
//
// We treat everything except '-' as initialized.
func (g *Git) ListInitializedSubmodules() ([]string, error) {
	// Short-circuit when there's no .gitmodules file. `git submodule status`
	// would just return an empty result anyway, but skipping the subprocess
	// keeps the common case (repo with no submodules) essentially free.
	root, err := g.GetRepoRoot()
	if err != nil {
		return nil, nil
	}
	if _, statErr := os.Stat(filepath.Join(root, ".gitmodules")); os.IsNotExist(statErr) {
		return nil, nil
	}

	out, err := g.run("submodule", "status")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}

	var paths []string
	for _, line := range strings.Split(out, "\n") {
		marker, _, path, ok := parseSubmoduleStatusLine(line)
		if !ok || marker == '-' {
			continue
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// InitSubmodules runs `git submodule update --init -- <paths>...` in g.RepoDir.
// Passing an empty paths slice is a no-op.
func (g *Git) InitSubmodules(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := []string{"submodule", "update", "--init", "--"}
	args = append(args, paths...)
	_, err := g.runWithSpinner("Initializing submodules...", args...)
	return err
}

// HasSubmodules reports whether the repo defines any submodules at all
// (i.e. has a .gitmodules file). It does not require any submodule to be
// initialized.
func (g *Git) HasSubmodules() bool {
	root, err := g.GetRepoRoot()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(root, ".gitmodules"))
	return err == nil
}

// UpdateSubmodulesRecursive runs `git submodule update --recursive`,
// advancing already-initialized submodules to the SHA the parent now
// records. Does NOT pass --init: uninitialized submodules stay
// uninitialized so users who opted out of submodule cloning aren't
// surprised. For initial cloning, see MirrorSubmodules / InitSubmodules.
//
// Safe to call when there are no submodules — short-circuits without
// invoking git. Use after operations that change the parent's HEAD (rebase,
// branch switch) to keep submodule working trees in sync with the recorded
// commits.
func (g *Git) UpdateSubmodulesRecursive() error {
	if !g.HasSubmodules() {
		return nil
	}
	// If no submodules are initialized in this worktree, there's nothing
	// to update — skip the subprocess entirely. This is the common case
	// for users who don't use submodules in a repo that has them.
	paths, err := g.ListInitializedSubmodules()
	if err != nil || len(paths) == 0 {
		return err
	}
	_, err = g.runWithSpinner("Updating submodules...", "submodule", "update", "--recursive")
	return err
}

// SubmoduleStatus categorizes the state of a single initialized submodule.
type SubmoduleStatus struct {
	// Path is the submodule path relative to the parent repo root.
	Path string
	// SHA is the submodule's currently checked-out commit, as reported by
	// `git submodule status`. When PointerChanged is false, this is also
	// the SHA the parent records in its index; when true, the recorded
	// SHA is whatever the parent's index points at instead.
	SHA string
	// PointerChanged is true when the parent's working tree records a
	// different submodule SHA than its HEAD (the gitlink has been moved
	// but not committed). Maps to the leading '+' from `git submodule
	// status`.
	PointerChanged bool
	// MergeConflict is true when the submodule has unresolved merge
	// conflicts. Maps to the leading 'U' from `git submodule status`.
	MergeConflict bool
	// Dirty is true when the submodule's working tree has uncommitted
	// changes (modified, staged, or untracked files relative to its own
	// HEAD).
	Dirty bool
	// DetachedHead is true when the submodule's HEAD is detached. This is
	// the default state after `git submodule update`, but editing in this
	// state risks orphaning commits.
	DetachedHead bool
	// HasUnpushed is true when the submodule's checked-out commit is not
	// reachable from any `origin/*` ref. Catches the case where edits
	// were made in detached HEAD and committed (so no branch points at
	// them), which a branch-tip check would miss. Set to false when the
	// submodule has no `origin` remote — we can't decide, so don't flag.
	HasUnpushed bool
	// UnpushedCount is the number of commits reachable from the checked-out
	// SHA but not from any `origin/*` ref. Surfaced in warnings so the user
	// sees how much work is unpublished. Zero when HasUnpushed is false.
	UnpushedCount int
}

// HasIssues returns true if the submodule has any state worth surfacing.
// PointerChanged and DetachedHead are intentionally NOT issues on their own
// — pointer changes are normal mid-commit, and detached HEAD is the
// default after a submodule update.
func (s SubmoduleStatus) HasIssues() bool {
	return s.MergeConflict || s.Dirty || s.HasUnpushed
}

// SubmoduleStatuses returns one SubmoduleStatus per initialized submodule.
// Returns (nil, nil) when the repo has no submodules. Errors from individual
// submodule probes are surfaced as zero-valued fields rather than aborting
// the whole scan — a single broken submodule should not hide the state of
// the others.
func (g *Git) SubmoduleStatuses() ([]SubmoduleStatus, error) {
	if !g.HasSubmodules() {
		return nil, nil
	}
	root, err := g.GetRepoRoot()
	if err != nil {
		return nil, err
	}

	out, err := g.run("submodule", "status")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}

	var statuses []SubmoduleStatus
	for _, line := range strings.Split(out, "\n") {
		marker, sha, path, ok := parseSubmoduleStatusLine(line)
		if !ok || marker == '-' {
			// Not initialized or unparseable — skip.
			continue
		}

		st := SubmoduleStatus{
			Path:           path,
			SHA:            sha,
			PointerChanged: marker == '+',
			MergeConflict:  marker == 'U',
		}

		sub := New(filepath.Join(root, path))
		if dirty, err := sub.HasUncommittedChanges(); err == nil {
			st.Dirty = dirty
		}
		if detached, err := sub.IsDetachedHead(); err == nil {
			st.DetachedHead = detached
		}
		// Use a SHA-based reachability check rather than branch-tip vs
		// origin/branch. Submodules are commonly on a detached HEAD; a
		// branch-tip check would silently miss commits made there.
		if sha != "" {
			if count, err := sub.commitsNotOnRemote("origin", sha); err == nil && count > 0 {
				st.HasUnpushed = true
				st.UnpushedCount = count
			}
		}

		statuses = append(statuses, st)
	}
	return statuses, nil
}

// parseSubmoduleStatusLine pulls the marker, SHA, and path out of one line of
// `git submodule status` output. Lines look like:
//
//	" <sha> <path>"             — initialized, clean
//	"+<sha> <path> (<desc>)"    — initialized, working tree differs from index
//	"-<sha> <path>"             — not initialized
//	"U<sha> <path>"             — merge conflicts
//
// The optional `(<desc>)` suffix is `git describe` output, not part of the
// path. Returns ok=false when the line can't be parsed (empty, malformed).
// Path parsing preserves spaces inside the path itself.
func parseSubmoduleStatusLine(line string) (marker byte, sha, path string, ok bool) {
	if line == "" {
		return 0, "", "", false
	}
	marker = line[0]
	rest := line[1:]
	shaPart, after, found := strings.Cut(rest, " ")
	if !found || shaPart == "" || after == "" {
		return 0, "", "", false
	}
	path = after
	// Strip the optional " (<desc>)" suffix. Anchor on " (" — paths that
	// happen to contain "(" elsewhere are preserved.
	if strings.HasSuffix(path, ")") {
		if i := strings.LastIndex(path, " ("); i >= 0 {
			path = path[:i]
		}
	}
	if path == "" {
		return 0, "", "", false
	}
	return marker, shaPart, path, true
}

// commitsNotOnRemote returns the number of commits reachable from `sha` but
// not reachable from any `<remote>/*` ref. Returns (0, nil) when the remote
// has no tracked refs at all (in that case we can't decide whether `sha` is
// "unpushed" — caller should not flag).
//
// Used by SubmoduleStatuses to drive the push gate: any positive count means
// pushing the parent now would publish a recorded SHA that teammates can't
// fetch.
func (g *Git) commitsNotOnRemote(remote, sha string) (int, error) {
	if remote == "" || sha == "" {
		return 0, fmt.Errorf("remote and sha must be non-empty")
	}
	// Cheap existence check: if we have no remote-tracking refs at all for
	// this remote, the rev-list below would treat `sha` as wholly unpushed
	// and return its full ancestry — a misleading false positive. Skip.
	refs, err := g.run("for-each-ref", "--format=%(refname)", "refs/remotes/"+remote+"/")
	if err != nil || refs == "" {
		return 0, err
	}
	out, err := g.run("rev-list", "--count", sha, "--not", "--remotes="+remote)
	if err != nil {
		return 0, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return 0, nil
	}
	n := 0
	for _, c := range out {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("unexpected rev-list count %q", out)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// HasUncommittedChanges reports whether g.RepoDir has any modified, staged,
// or untracked files.
func (g *Git) HasUncommittedChanges() (bool, error) {
	out, err := g.run("status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// IsDetachedHead reports whether HEAD is detached (no symbolic ref).
// Distinguishes the "detached" exit code from real failures (missing repo,
// permission errors): only exit code 1 with `-q` means "not a symbolic ref";
// anything else is propagated so callers don't misclassify a broken repo as
// detached. Bypasses g.run because that wrapper formats the error and loses
// the underlying *exec.ExitError needed to inspect the exit code.
func (g *Git) IsDetachedHead() (bool, error) {
	cmd := exec.Command("git", "symbolic-ref", "-q", "HEAD")
	cmd.Dir = g.RepoDir
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return true, nil
		}
		return false, fmt.Errorf("symbolic-ref HEAD: %w", err)
	}
	return false, nil
}

// MirrorSubmodules initializes in destPath the same submodules that are
// currently initialized in sourcePath. No-op when sourcePath has no
// initialized submodules. destPath must be a checked-out worktree of the
// same repository (submodules are per-worktree in Git).
func MirrorSubmodules(sourcePath, destPath string) error {
	if sourcePath == "" || destPath == "" {
		return fmt.Errorf("source and destination paths must be non-empty")
	}
	src := New(sourcePath)
	paths, err := src.ListInitializedSubmodules()
	if err != nil {
		return fmt.Errorf("failed to list submodules in %s: %w", sourcePath, err)
	}
	if len(paths) == 0 {
		return nil
	}
	dst := New(destPath)
	if err := dst.InitSubmodules(paths); err != nil {
		return fmt.Errorf("failed to initialize submodules in %s: %w", destPath, err)
	}
	return nil
}
