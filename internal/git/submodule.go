package git

import (
	"fmt"
	"os"
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
		if line == "" {
			continue
		}
		// Status char is always the first byte. Lines are non-empty here.
		status := line[0]
		if status == '-' {
			continue
		}
		// Format: "<status><sha> <path>[ (<desc>)]". Strip the status char,
		// take the SHA up to the first space, then the path is the rest with
		// the optional " (<desc>)" suffix removed. Parsing this way (rather
		// than splitting on whitespace) preserves spaces inside submodule
		// paths.
		_, after, ok := strings.Cut(line[1:], " ")
		if !ok || after == "" {
			continue
		}
		path := after
		if strings.HasSuffix(path, ")") {
			if i := strings.LastIndex(path, " ("); i >= 0 {
				path = path[:i]
			}
		}
		if path == "" {
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
	// HasUnpushed is true when the submodule has local commits on its
	// current branch that are not present on its `origin` remote. Only set
	// when the submodule has an `origin` remote and HEAD is on a branch.
	HasUnpushed bool
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
		if line == "" {
			continue
		}
		marker := line[0]
		if marker == '-' {
			// Not initialized — skip. We can't probe a submodule that
			// has never been cloned.
			continue
		}
		_, after, ok := strings.Cut(line[1:], " ")
		if !ok || after == "" {
			continue
		}
		path := after
		if strings.HasSuffix(path, ")") {
			if i := strings.LastIndex(path, " ("); i >= 0 {
				path = path[:i]
			}
		}
		if path == "" {
			continue
		}

		st := SubmoduleStatus{
			Path:           path,
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
		if unpushed, err := sub.HasUnpushedCommits(); err == nil {
			st.HasUnpushed = unpushed
		}

		statuses = append(statuses, st)
	}
	return statuses, nil
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
func (g *Git) IsDetachedHead() (bool, error) {
	_, err := g.run("symbolic-ref", "-q", "HEAD")
	if err == nil {
		return false, nil
	}
	// `git symbolic-ref -q` exits 1 (no message) when HEAD is detached.
	// Anything else is a real failure, but we can't distinguish without
	// parsing — treat any error as "detached" since the caller's worst
	// case is a false positive on a broken repo.
	return true, nil
}

// HasUnpushedCommits reports whether the current branch has commits that
// are not reachable from `origin/<same-name>`. Returns (false, nil) when
// there is no `origin` remote, no matching remote-tracking ref, or HEAD is
// detached — those states can't be checked, so they're not flagged.
func (g *Git) HasUnpushedCommits() (bool, error) {
	branch, err := g.CurrentBranch()
	if err != nil || branch == "" || branch == "HEAD" {
		return false, nil
	}
	remoteRef := "refs/remotes/origin/" + branch
	if _, err := g.run("rev-parse", "--verify", "--quiet", remoteRef); err != nil {
		// No matching remote-tracking ref — can't decide. Don't flag.
		return false, nil
	}
	out, err := g.run("rev-list", "--count", remoteRef+".."+branch)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "0", nil
}

// SubmoduleHasCommit reports whether commit `sha` is reachable from any ref
// on remote `remoteName` for the submodule rooted at `submodulePath`. Used
// as a pre-push check: if the parent repo records a submodule SHA that
// nobody else can fetch, pushing the parent breaks teammates.
//
// Returns (true, nil) when the SHA is on the remote, (false, nil) when it
// is not. An error means we couldn't determine; the caller should treat
// that as inconclusive rather than blocking the push.
func SubmoduleHasCommit(submodulePath, remoteName, sha string) (bool, error) {
	if submodulePath == "" || sha == "" {
		return false, fmt.Errorf("submodule path and sha must be non-empty")
	}
	if remoteName == "" {
		remoteName = "origin"
	}
	sub := New(submodulePath)
	// `branch -r --contains <sha>` lists remote-tracking refs that
	// contain the commit. If any of them belong to <remoteName>, the
	// commit is on that remote.
	out, err := sub.run("branch", "-r", "--contains", sha)
	if err != nil {
		// Most common cause: <sha> is unknown locally. That can mean the
		// submodule's working tree is stale and we never fetched the
		// commit — inconclusive, not a hard fail.
		return false, err
	}
	prefix := remoteName + "/"
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// `branch -r` prefixes the active line with '*' — strip it.
		line = strings.TrimPrefix(line, "* ")
		if strings.HasPrefix(line, prefix) {
			return true, nil
		}
	}
	return false, nil
}

// SubmodulePointerSHA returns the submodule's currently-checked-out SHA at
// `submodulePath` (relative to no specific repo — caller passes an absolute
// path). This is the SHA `git submodule status` would print.
func SubmodulePointerSHA(submodulePath string) (string, error) {
	sub := New(submodulePath)
	return sub.run("rev-parse", "HEAD")
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
