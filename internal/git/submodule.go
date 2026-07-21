package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	// Path is the submodule path relative to the active worktree's repo root.
	// Nested submodules carry their full path from the top (e.g.
	// "vendor/x/inner/y").
	Path string
	// CheckoutSHA is the submodule's working-tree HEAD, as reported by
	// `git submodule status`. This is the commit the user is currently
	// editing inside the submodule; it may differ from what the parent
	// commits.
	CheckoutSHA string
	// GitlinkSHA is the SHA the parent's HEAD records for this submodule
	// (i.e. what `git ls-tree HEAD <path>` returns). This is the SHA that
	// gets *published* when the parent is pushed. Empty when the parent's
	// HEAD has no entry for this path (e.g. unborn HEAD).
	GitlinkSHA string
	// PointerChanged is true when CheckoutSHA and GitlinkSHA disagree:
	// either the user moved the submodule HEAD without committing the
	// gitlink change, or staged a gitlink change without checking it out.
	// Maps roughly to the leading '+' from `git submodule status` but is
	// computed from gitlink-vs-checkout to be reliable across staged-but-
	// not-committed cases.
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
	// HasUnpushed is true when CheckoutSHA has commits not reachable from
	// any `origin/*` ref — i.e. the submodule's working tree contains local
	// work that hasn't been published. Informational; surfaced by the
	// doctor. Set to false when the submodule has no `origin` remote (we
	// can't decide, so don't flag) or when the SHA-reachability probe
	// errored.
	HasUnpushed bool
	// UnpushedCount is the number of commits reachable from CheckoutSHA
	// but not from any `origin/*` ref. Zero when HasUnpushed is false.
	UnpushedCount int
	// GitlinkUnpushed is true when GitlinkSHA has commits not reachable
	// from any `origin/*` ref. This is the precise push-gate condition:
	// pushing the parent in this state records a submodule SHA teammates
	// can't fetch. When PointerChanged=false, equals HasUnpushed; when
	// true, only this matters for the push gate. False when GitlinkSHA is
	// empty (e.g. `git submodule add` not yet committed).
	GitlinkUnpushed bool
	// GitlinkUnpushedCount is the commit count for GitlinkUnpushed. Zero
	// when GitlinkUnpushed is false.
	GitlinkUnpushedCount int
}

// HasIssues returns true if the submodule has any state worth surfacing.
// PointerChanged and DetachedHead are intentionally NOT issues on their own
// — pointer changes are normal mid-commit, and detached HEAD is the
// default after a submodule update.
func (s SubmoduleStatus) HasIssues() bool {
	return s.MergeConflict || s.Dirty || s.HasUnpushed
}

// SubmoduleStatuses returns one SubmoduleStatus per initialized submodule
// reachable from g, including nested submodules (depth-first). Nested paths
// are rendered relative to g (e.g. "vendor/x/inner/y") so the doctor and
// push-gate UI can show a single flat list. Returns (nil, nil) when the
// repo has no submodules. Errors from individual submodule probes are
// surfaced as zero-valued fields rather than aborting the whole scan — a
// single broken submodule should not hide the state of the others.
func (g *Git) SubmoduleStatuses() ([]SubmoduleStatus, error) {
	return g.submoduleStatusesAt("")
}

// submoduleStatusesAt is the recursive engine for SubmoduleStatuses. It
// produces statuses for submodules reachable from g, prefixing each Path
// with pathPrefix so callers see the path from the top-level worktree.
func (g *Git) submoduleStatusesAt(pathPrefix string) ([]SubmoduleStatus, error) {
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
		marker, checkoutSHA, path, ok := parseSubmoduleStatusLine(line)
		if !ok || marker == '-' {
			// Not initialized or unparseable — skip. We don't recurse into
			// uninitialized submodules either; they have no on-disk repo.
			continue
		}

		displayPath := path
		if pathPrefix != "" {
			displayPath = filepath.ToSlash(filepath.Join(pathPrefix, path))
		}

		// Gitlink SHA is what `ezs push` will publish. Falls back to "" on
		// any error (e.g. unborn HEAD), in which case we use CheckoutSHA.
		gitlinkSHA, _ := g.gitlinkSHA(path)

		st := SubmoduleStatus{
			Path:           displayPath,
			CheckoutSHA:    checkoutSHA,
			GitlinkSHA:     gitlinkSHA,
			PointerChanged: marker == '+' || (gitlinkSHA != "" && gitlinkSHA != checkoutSHA),
			MergeConflict:  marker == 'U',
		}

		sub := New(filepath.Join(root, path))
		if dirty, err := sub.HasUncommittedChanges(); err == nil {
			st.Dirty = dirty
		}
		if detached, err := sub.IsDetachedHead(); err == nil {
			st.DetachedHead = detached
		}
		// Two unpushed checks against `origin/*` refs in the submodule:
		//
		//   - CheckoutSHA: drives the doctor's informational "submodule has
		//     local commits" warning. SHA-based reachability (vs. branch-
		//     tip) catches detached-HEAD edits.
		//   - GitlinkSHA:  drives the push gate. When the parent is pushed
		//     it publishes this SHA; if it isn't on origin, teammates can't
		//     fetch it. Only meaningful when GitlinkSHA is set.
		//
		// When PointerChanged=false the two SHAs match and the two checks
		// give the same answer; when true (uncommitted gitlink change) they
		// can diverge and the consumer cares about the right one.
		if checkoutSHA != "" {
			if count, err := sub.commitsNotOnRemote("origin", checkoutSHA); err == nil && count > 0 {
				st.HasUnpushed = true
				st.UnpushedCount = count
			}
		}
		if gitlinkSHA != "" {
			if gitlinkSHA == checkoutSHA && st.HasUnpushed {
				// Optimisation: both SHAs match; skip the second probe.
				st.GitlinkUnpushed = true
				st.GitlinkUnpushedCount = st.UnpushedCount
			} else if gitlinkSHA != checkoutSHA {
				if count, err := sub.commitsNotOnRemote("origin", gitlinkSHA); err == nil && count > 0 {
					st.GitlinkUnpushed = true
					st.GitlinkUnpushedCount = count
				}
			}
		}

		statuses = append(statuses, st)

		// Recurse into the submodule. A failure here (e.g. nested
		// submodule's repo is missing) is logged-but-not-fatal: the outer
		// scan still returns what it found at this level.
		if nested, err := sub.submoduleStatusesAt(displayPath); err == nil {
			statuses = append(statuses, nested...)
		}
	}
	return statuses, nil
}

// gitlinkSHA returns the SHA the parent's HEAD records for the submodule at
// path. Empty string with nil error when the path isn't recorded as a
// gitlink in HEAD (e.g. fresh `git submodule add` not yet committed, or an
// unborn HEAD). Errors propagate when ls-tree itself fails.
//
// `git ls-tree HEAD -- <path>` output for a gitlink is:
//
//	"160000 commit <sha>\t<path>"
func (g *Git) gitlinkSHA(path string) (string, error) {
	out, err := g.run("ls-tree", "HEAD", "--", path)
	if err != nil {
		// Most common cause: HEAD is unborn. Treat as "no recorded SHA".
		return "", nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", nil
	}
	// Mode + type + sha + \t + path. Use Fields so any whitespace works
	// before the tab; everything after the tab is the path (which we
	// don't need here).
	fields := strings.Fields(out)
	if len(fields) < 3 || fields[0] != "160000" {
		// Not a gitlink — could be a regular file or directory if the user
		// converted a submodule to a tracked path. Caller falls back to
		// CheckoutSHA.
		return "", nil
	}
	return fields[2], nil
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
//
// Important: callers commonly run `git submodule status` through helpers
// that strip leading whitespace from the overall output. After such a
// trim, the FIRST line of a multi-line clean status loses its leading
// space marker. We detect this by treating any non-{+,-,U} first byte as
// an implicit ' ' marker — clean submodules' SHAs always start with a hex
// digit, never one of the marker characters.
func parseSubmoduleStatusLine(line string) (marker byte, sha, path string, ok bool) {
	if line == "" {
		return 0, "", "", false
	}
	var rest string
	switch line[0] {
	case '+', '-', 'U', ' ':
		marker = line[0]
		rest = line[1:]
	default:
		// Leading ' ' marker was stripped upstream — assume clean.
		marker = ' '
		rest = line
	}
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
// "unpushed" — caller should not flag) or when any underlying git command
// fails (so the caller sees "unknown" rather than a false positive).
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
	n, err := strconv.Atoi(out)
	if err != nil {
		return 0, fmt.Errorf("unexpected rev-list count %q: %w", out, err)
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
