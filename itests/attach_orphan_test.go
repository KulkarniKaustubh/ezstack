package itests

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
)

// captureStderr replaces os.Stderr with a pipe for the duration of fn and
// returns everything written to it. Used by tests that assert against
// human-facing output (orphan list, error messages, etc.).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = orig
	return <-done
}

// gitBareBranch creates a local branch via plain git (no ezs metadata, no
// worktree). Mirrors what a user does with `git checkout -b` outside of ezs.
func gitBareBranch(t *testing.T, repoDir, name, base string) {
	t.Helper()
	if err := exec.Command("git", "-C", repoDir, "branch", name, base).Run(); err != nil {
		t.Fatalf("git branch %s %s: %v", name, base, err)
	}
}

// TestNewBranch_ExistingBranchErrors locks down the discoverability fix:
// running `ezs new <name>` on an already-existing local branch must error
// out instead of accidentally adopting the branch (worktree mode) or
// failing opaquely (no-worktree mode).
func TestNewBranch_ExistingBranchErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	gitBareBranch(t, env.RepoDir, "feature-x", "main")

	chdirForTest(t, env.RepoDir)
	err := commands.New([]string{"feature-x", "-p", "main"})
	if err == nil {
		t.Fatalf("expected error when branch already exists; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "already exists") {
		t.Errorf("error should mention branch already exists; got: %s", msg)
	}
	if !strings.Contains(msg, "ezs stack") {
		t.Errorf("error should signpost `ezs stack`; got: %s", msg)
	}
}

// TestCollectUntrackedBranches_IncludesBareBranches verifies that the picker
// surface (CollectUntrackedBranches) sees branches with no worktree, not just
// unregistered worktrees.
func TestCollectUntrackedBranches_IncludesBareBranches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Tracked branch (worktree) — should NOT appear.
	CreateBranch(t, env, "tracked-feature", "main")
	// Bare branch — should appear.
	gitBareBranch(t, env.RepoDir, "bare-feature", "main")

	g := git.New(env.RepoDir)
	mgr, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	got := commands.CollectUntrackedBranches(g, mgr)

	var sawBare, sawTracked bool
	for _, c := range got {
		if c.Name == "bare-feature" {
			sawBare = true
			if c.WorktreePath != "" {
				t.Errorf("bare-feature WorktreePath = %q, want empty", c.WorktreePath)
			}
		}
		if c.Name == "tracked-feature" {
			sawTracked = true
		}
	}
	if !sawBare {
		t.Errorf("bare-feature missing from CollectUntrackedBranches result: %+v", got)
	}
	if sawTracked {
		t.Errorf("tracked-feature should be filtered out (it's in a stack); got: %+v", got)
	}
}

// TestStatus_OrphanSection verifies that `ezs status -a` surfaces local
// branches that exist in git but aren't part of any stack, with the
// adoption hint pointing at `ezs stack`.
func TestStatus_OrphanSection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// One real stack so Status doesn't bail on "no stacks found".
	CreateBranch(t, env, "tracked-feature", "main")
	// Orphan that should surface.
	gitBareBranch(t, env.RepoDir, "manual-branch", "main")

	chdirForTest(t, env.RepoDir)

	var statusErr error
	out := captureStderr(t, func() {
		statusErr = commands.Status([]string{"-a"})
	})
	if statusErr != nil {
		t.Fatalf("Status(-a): %v", statusErr)
	}

	if !strings.Contains(out, "Branches not in any stack") {
		t.Errorf("orphan header missing from status output; got:\n%s", out)
	}
	if !strings.Contains(out, "manual-branch") {
		t.Errorf("orphan branch missing from status output; got:\n%s", out)
	}
	if !strings.Contains(out, "ezs stack") {
		t.Errorf("adoption hint missing from status output; got:\n%s", out)
	}
	if strings.Contains(out, "tracked-feature\n") && strings.Contains(out, "Branches not in any stack:\n  tracked-feature") {
		t.Errorf("tracked branch leaked into orphan list; got:\n%s", out)
	}
}
