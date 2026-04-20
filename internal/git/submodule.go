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
