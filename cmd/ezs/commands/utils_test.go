package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
)

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// setupStackRepo builds a repo with a bare origin, a pushed main, a pushed
// `feat` branch with 1 commit, and a dedicated worktree for `feat`. It also
// returns a Stack matching what ezstack would construct at runtime so we can
// drive fetchDiffStats directly.
func setupStackRepo(t *testing.T) (repoDir, featWT string, stack *config.Stack, cleanup func()) {
	t.Helper()
	tmp, err := os.MkdirTemp("", "ezs-fetchdiff-*")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cleanup = func() { os.RemoveAll(tmp) }
	repoDir = filepath.Join(tmp, "repo")
	bareDir := filepath.Join(tmp, "remote.git")
	wtRoot := filepath.Join(tmp, "worktrees")
	featWT = filepath.Join(wtRoot, "feat")

	if err := os.MkdirAll(repoDir, 0755); err != nil {
		cleanup()
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(wtRoot, 0755); err != nil {
		cleanup()
		t.Fatalf("mkdir wt: %v", err)
	}
	mustGit(t, "", "init", "-b", "main", repoDir)
	mustGit(t, "", "init", "--bare", bareDir)
	mustGit(t, repoDir, "config", "user.email", "test@test.com")
	mustGit(t, repoDir, "config", "user.name", "Test User")
	mustGit(t, repoDir, "remote", "add", "origin", bareDir)

	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("r\n"), 0644); err != nil {
		cleanup()
		t.Fatalf("write: %v", err)
	}
	mustGit(t, repoDir, "add", ".")
	mustGit(t, repoDir, "commit", "-q", "-m", "initial")
	mustGit(t, repoDir, "push", "-q", "origin", "main")

	// Create `feat` branch with a commit, then move it into a worktree.
	mustGit(t, repoDir, "branch", "feat")
	mustGit(t, repoDir, "worktree", "add", "-q", featWT, "feat")
	if err := os.WriteFile(filepath.Join(featWT, "f.txt"), []byte("1\n2\n3\n4\n5\n"), 0644); err != nil {
		cleanup()
		t.Fatalf("write feat: %v", err)
	}
	mustGit(t, featWT, "add", "f.txt")
	mustGit(t, featWT, "commit", "-q", "-m", "feat commit")
	mustGit(t, featWT, "push", "-q", "origin", "feat")

	stack = &config.Stack{
		Hash: "test",
		Root: "main",
		Branches: []*config.Branch{
			{Name: "feat", Parent: "main", WorktreePath: featWT},
		},
	}
	return repoDir, featWT, stack, cleanup
}

// TestFetchDiffStats_CleanWorktree: branch is in sync with origin and the
// worktree is clean → single count, no HasPushedDiff.
func TestFetchDiffStats_CleanWorktree(t *testing.T) {
	repoDir, _, s, cleanup := setupStackRepo(t)
	defer cleanup()

	g := git.New(repoDir)
	stats := fetchDiffStats(g, s)

	feat := stats["feat"]
	if feat == nil {
		t.Fatal("no stats for feat")
	}
	if feat.Additions != 5 || feat.Deletions != 0 {
		t.Errorf("additions/deletions = (%d,%d), want (5,0)", feat.Additions, feat.Deletions)
	}
	if feat.HasPushedDiff {
		t.Errorf("HasPushedDiff should be false when in sync, got true with (+%d,-%d)",
			feat.PushedAdditions, feat.PushedDeletions)
	}
}

// TestFetchDiffStats_DirtyWorktree_FromMainRepo is the headline regression
// test: ezs list invoked from the main repo (not the feat worktree) must
// still report working-tree-accurate counts AND surface the pushed/local
// divergence for the branch.
func TestFetchDiffStats_DirtyWorktree_FromMainRepo(t *testing.T) {
	repoDir, featWT, s, cleanup := setupStackRepo(t)
	defer cleanup()

	// Modify feat's worktree: +2 unstaged lines in f.txt, +1 staged new file.
	if err := os.WriteFile(filepath.Join(featWT, "f.txt"), []byte("1\n2\n3\n4\n5\n6\n7\n"), 0644); err != nil {
		t.Fatalf("modify: %v", err)
	}
	if err := os.WriteFile(filepath.Join(featWT, "new.txt"), []byte("extra\n"), 0644); err != nil {
		t.Fatalf("new: %v", err)
	}
	mustGit(t, featWT, "add", "new.txt")

	// Invoke from the *main* repo dir, not the feat worktree.
	g := git.New(repoDir)
	stats := fetchDiffStats(g, s)

	feat := stats["feat"]
	if feat == nil {
		t.Fatal("no stats for feat")
	}
	// 5 committed + 2 unstaged + 1 staged = 8.
	if feat.Additions != 8 || feat.Deletions != 0 {
		t.Errorf("local counts = (%d,%d), want (8,0)", feat.Additions, feat.Deletions)
	}
	if !feat.HasPushedDiff {
		t.Error("HasPushedDiff should be true when worktree diverges from origin")
	}
	if feat.PushedAdditions != 5 || feat.PushedDeletions != 0 {
		t.Errorf("pushed counts = (%d,%d), want (5,0)", feat.PushedAdditions, feat.PushedDeletions)
	}
}

// TestFetchDiffStats_LocalAheadOfRemote: new commits in the worktree that
// haven't been pushed — HasPushedDiff true, pushed count matches remote.
func TestFetchDiffStats_LocalAheadOfRemote(t *testing.T) {
	repoDir, featWT, s, cleanup := setupStackRepo(t)
	defer cleanup()

	// Add a commit that doubles the line count, without pushing.
	if err := os.WriteFile(filepath.Join(featWT, "f.txt"), []byte("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustGit(t, featWT, "add", "f.txt")
	mustGit(t, featWT, "commit", "-q", "-m", "more")

	g := git.New(repoDir)
	stats := fetchDiffStats(g, s)

	feat := stats["feat"]
	if feat == nil {
		t.Fatal("no stats for feat")
	}
	if feat.Additions != 10 {
		t.Errorf("local additions = %d, want 10", feat.Additions)
	}
	if !feat.HasPushedDiff {
		t.Error("HasPushedDiff should be true when local commits ahead of origin")
	}
	if feat.PushedAdditions != 5 {
		t.Errorf("pushed additions = %d, want 5", feat.PushedAdditions)
	}
}

// TestFetchDiffStats_UnpushedBranch: brand-new stack branch with commits but
// no origin ref yet — single count, HasPushedDiff false.
func TestFetchDiffStats_UnpushedBranch(t *testing.T) {
	repoDir, featWT, _, cleanup := setupStackRepo(t)
	defer cleanup()

	// Create feat-b stacked on feat, in its own worktree, with 3 lines.
	featBWT := filepath.Join(filepath.Dir(featWT), "feat-b")
	mustGit(t, repoDir, "worktree", "add", "-q", "-b", "feat-b", featBWT, "feat")
	if err := os.WriteFile(filepath.Join(featBWT, "b.txt"), []byte("a\nb\nc\n"), 0644); err != nil {
		t.Fatalf("write b: %v", err)
	}
	mustGit(t, featBWT, "add", "b.txt")
	mustGit(t, featBWT, "commit", "-q", "-m", "b commit")

	s := &config.Stack{
		Hash: "test2",
		Root: "main",
		Branches: []*config.Branch{
			{Name: "feat", Parent: "main", WorktreePath: featWT},
			{Name: "feat-b", Parent: "feat", WorktreePath: featBWT},
		},
	}

	g := git.New(repoDir)
	stats := fetchDiffStats(g, s)

	b := stats["feat-b"]
	if b == nil {
		t.Fatal("no stats for feat-b")
	}
	if b.Additions != 3 || b.Deletions != 0 {
		t.Errorf("feat-b = (%d,%d), want (3,0)", b.Additions, b.Deletions)
	}
	if b.HasPushedDiff {
		t.Error("unpushed branch should not report HasPushedDiff")
	}
}

// TestFetchDiffStats_MergedBranchSkipped: merged branches get no stats at all.
func TestFetchDiffStats_MergedBranchSkipped(t *testing.T) {
	repoDir, featWT, _, cleanup := setupStackRepo(t)
	defer cleanup()

	s := &config.Stack{
		Hash: "test3",
		Root: "main",
		Branches: []*config.Branch{
			{Name: "feat", Parent: "main", WorktreePath: featWT, IsMerged: true},
		},
	}
	g := git.New(repoDir)
	stats := fetchDiffStats(g, s)
	if _, ok := stats["feat"]; ok {
		t.Error("merged branch should not appear in stats map")
	}
}


func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"/path/to/dir", "'/path/to/dir'"},
		{"/path/with spaces/dir", "'/path/with spaces/dir'"},
		{"/path/with'quote", "'/path/with'\\''quote'"},
		{"", "''"},
		{"$(rm -rf /)", "'$(rm -rf /)'"},
		{"`whoami`", "'`whoami`'"},
		{"a;b", "'a;b'"},
		{"a\nb", "'a\nb'"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ShellQuote(tt.input)
			if got != tt.want {
				t.Errorf("ShellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
