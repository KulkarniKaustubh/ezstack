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
	"time"
)

// On-disk names inside the git common dir (github/gh-stack internal/stack and
// cmd/rebase.go). Keep in sync with gh-stack.
const (
	catalogFile        = "gh-stack"
	catalogLockFile    = "gh-stack.lock"
	operationLockFile  = "gh-stack-operation.lock"
	rebaseStateFile    = "gh-stack-rebase-state"
	migrationStateFile = "gh-stack-migration"
)

// catalogSchemaVersion is the gh-stack catalog schema this package reads and
// writes. A newer catalog is refused rather than rewritten lossily.
const catalogSchemaVersion = 1

// lockTimeout mirrors gh-stack's own lock wait.
var lockTimeout = 5 * time.Second

// Catalog mirrors gh-stack's `<common-dir>/gh-stack` file (schemaVersion 1).
type Catalog struct {
	SchemaVersion int     `json:"schemaVersion"`
	Repository    string  `json:"repository"`
	Stacks        []Stack `json:"stacks"`
}

// Stack is one strictly linear gh-stack stack.
type Stack struct {
	ID       string      `json:"id,omitempty"`
	Number   int         `json:"number,omitempty"`
	Trunk    BranchRef   `json:"trunk"`
	Branches []BranchRef `json:"branches"`
}

// BranchRef is a stack layer (or the trunk). For layers, Base is the parent's
// head SHA as of the last rebase — gh-stack's `--onto` anchor, which is why
// Reconcile preserves it whenever a branch's parent is unchanged.
type BranchRef struct {
	Branch      string          `json:"branch"`
	Head        string          `json:"head,omitempty"`
	Base        string          `json:"base,omitempty"`
	PullRequest *PullRequestRef `json:"pullRequest,omitempty"`
}

// PullRequestRef is gh-stack's cached PR metadata for a layer.
type PullRequestRef struct {
	Number int    `json:"number"`
	ID     string `json:"id,omitempty"`
	URL    string `json:"url,omitempty"`
	Merged bool   `json:"merged,omitempty"`
}

// IsMerged reports whether gh-stack considers the layer's PR merged.
func (b BranchRef) IsMerged() bool { return b.PullRequest != nil && b.PullRequest.Merged }

// BranchNames returns the stack's layers bottom→top.
func (s Stack) BranchNames() []string {
	names := make([]string, len(s.Branches))
	for i, b := range s.Branches {
		names[i] = b.Branch
	}
	return names
}

// CommonDir resolves the repository's git common dir (where gh-stack keeps its
// state, shared by every worktree).
func CommonDir(repoDir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Dir = repoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("resolve git common dir: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// LoadCatalog reads the catalog in commonDir. A missing file yields an empty
// catalog.
func LoadCatalog(commonDir string) (*Catalog, error) {
	data, err := os.ReadFile(filepath.Join(commonDir, catalogFile))
	if errors.Is(err, os.ErrNotExist) {
		return &Catalog{SchemaVersion: catalogSchemaVersion, Stacks: []Stack{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read gh-stack catalog: %w", err)
	}
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse gh-stack catalog: %w", err)
	}
	if c.SchemaVersion > catalogSchemaVersion {
		return nil, fmt.Errorf("gh-stack catalog schemaVersion %d is newer than this ezstack understands (%d); upgrade ezstack",
			c.SchemaVersion, catalogSchemaVersion)
	}
	if c.Stacks == nil {
		c.Stacks = []Stack{}
	}
	return &c, nil
}

// RebaseInProgress reports whether gh-stack has a paused cascade rebase
// (resolved with Continue or Abort).
func RebaseInProgress(commonDir string) bool {
	_, err := os.Stat(filepath.Join(commonDir, rebaseStateFile))
	return err == nil
}

// ErrRebasePaused is returned when the catalog can't be rewritten because a
// gh-stack rebase is paused on a conflict.
var ErrRebasePaused = errors.New("a stack rebase is paused on a conflict: resolve it, then run `ezs sync --continue` (or `ezs sync --abort`)")

// errLegacyCatalog is returned while pre-0.2 per-worktree catalogs await
// gh-stack's own migration; rewriting the shared file first would make that
// migration see conflicting definitions.
var errLegacyCatalog = errors.New("gh-stack has per-worktree state from a pre-0.2 release that must be migrated first; run `gh stack view` once in this repository")

// updateCatalog applies fn to the catalog under gh-stack's locking protocol
// (operation lock, then catalog lock — the same order gh-stack mutations use)
// and writes the result atomically when fn reports a change.
func updateCatalog(commonDir string, fn func(*Catalog) (bool, error)) error {
	if RebaseInProgress(commonDir) {
		return ErrRebasePaused
	}
	if hasLegacyCatalogs(commonDir) {
		return errLegacyCatalog
	}
	opLock, err := acquireLock(filepath.Join(commonDir, operationLockFile))
	if err != nil {
		return err
	}
	defer opLock.release()
	catLock, err := acquireLock(filepath.Join(commonDir, catalogLockFile))
	if err != nil {
		return err
	}
	defer catLock.release()

	// Re-check under the lock: a rebase may have started while we waited.
	if RebaseInProgress(commonDir) {
		return ErrRebasePaused
	}
	cat, err := LoadCatalog(commonDir)
	if err != nil {
		return err
	}
	changed, err := fn(cat)
	if err != nil || !changed {
		return err
	}
	cat.SchemaVersion = catalogSchemaVersion
	data, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return fmt.Errorf("encode gh-stack catalog: %w", err)
	}
	return writeAtomic(filepath.Join(commonDir, catalogFile), data)
}

// hasLegacyCatalogs reports whether any linked worktree still carries a
// pre-0.2 gh-stack catalog that gh-stack hasn't migrated yet.
func hasLegacyCatalogs(commonDir string) bool {
	if _, err := os.Stat(filepath.Join(commonDir, migrationStateFile)); err == nil {
		return true
	}
	matches, _ := filepath.Glob(filepath.Join(commonDir, "worktrees", "*", catalogFile))
	return len(matches) > 0
}

// writeAtomic replaces path with data via a fully written sibling temp file,
// so a concurrent reader (gh-stack reads without the lock) never sees a torn
// file.
func writeAtomic(path string, data []byte) (err error) {
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// fileLock is an advisory exclusive lock compatible with gh-stack's
// (flock on unix, LockFileEx on windows).
type fileLock struct{ f *os.File }

func acquireLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		busy, err := tryLock(f)
		if err == nil && !busy {
			return &fileLock{f: f}, nil
		}
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", filepath.Base(path), err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("%w: timed out waiting for %s", ErrLocked, filepath.Base(path))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (l *fileLock) release() {
	if l == nil || l.f == nil {
		return
	}
	unlock(l.f)
	l.f.Close()
	l.f = nil
}
