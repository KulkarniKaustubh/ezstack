package itests

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// TestPRDraftAll_CreatesDraftPRsForEveryBranch covers the `ezs pr --draft-all`
// shortcut: every branch in the stack without an existing PR gets a new draft
// PR in one go. No coverage existed before — a regression in the walk or in
// the "already has PR" short-circuit could ship silently.
func TestPRDraftAll_CreatesDraftPRsForEveryBranch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX + shell wrappers")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// github.com-style fetch URL so github.NewClient parses it; a separate
	// pushURL points at a local bare repo so actual `git push` succeeds
	// without network access. `git remote get-url` returns the fetch URL by
	// default, which is what newGitHubClient reads.
	bare := filepath.Join(env.TmpDir, "bare.git")
	mustRunGit(t, "", "init", "--bare", "-b", "main", bare)
	mustRunGit(t, env.RepoDir, "remote", "add", "origin", "git@github.com:testorg/testrepo.git")
	mustRunGit(t, env.RepoDir, "remote", "set-url", "--push", "origin", bare)
	mustRunGit(t, env.RepoDir, "push", "-q", "origin", "main")

	CreateBranchWithCommit(t, env, "feat-one", "main")
	CreateBranchWithCommit(t, env, "feat-two", "feat-one")

	// --draft-all uses ConfirmTUI; YesMode auto-accepts.
	prevYes := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = prevYes }()

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(filepath.Join(env.WorktreeDir, "feat-two")); err != nil {
		t.Fatal(err)
	}

	if err := commands.PR([]string{"--draft-all"}); err != nil {
		t.Fatalf("pr --draft-all: %v", err)
	}

	// Both branches must now have PR URLs recorded.
	mgr, err := stack.NewReadOnlyManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewReadOnlyManager: %v", err)
	}
	for _, name := range []string{"feat-one", "feat-two"} {
		b := mgr.GetBranch(name)
		if b == nil {
			t.Errorf("branch %q missing from stack manager", name)
			continue
		}
		if b.PRNumber == 0 {
			t.Errorf("branch %q has PRNumber=0; --draft-all should have created one", name)
		}
		if b.PRUrl == "" {
			t.Errorf("branch %q has empty PRUrl; --draft-all should have recorded it", name)
		}
	}

	// And the stub gh should now carry two PR state files, one per branch.
	prsDir := filepath.Join(env.TmpDir, "gh_state", "prs")
	entries, _ := os.ReadDir(prsDir)
	prFiles := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			prFiles++
		}
	}
	if prFiles < 2 {
		t.Errorf("stub gh recorded %d PRs; expected >= 2", prFiles)
	}

	// Spot-check that at least one was created as a draft (the whole point of --draft-all).
	// The stub stores isDraft in the per-PR JSON; read one and confirm it.
	if entries == nil {
		return
	}
	var sawDraft bool
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(prsDir, e.Name()))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), `"isDraft": true`) {
			sawDraft = true
			break
		}
	}
	if !sawDraft {
		t.Error("no PR state file reports isDraft=true; --draft-all did not request draft creation")
	}
}
