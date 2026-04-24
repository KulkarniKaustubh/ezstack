package itests

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
)

// TestCommitsAhead tests counting commits ahead of base
func TestCommitsAhead(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "ahead-branch", "main")

	g := NewGit(env)
	ahead, err := g.GetCommitsAhead("ahead-branch", "main")
	if err != nil {
		t.Fatalf("GetCommitsAhead failed: %v", err)
	}

	if ahead != 1 {
		t.Errorf("Expected 1 commit ahead, got %d", ahead)
	}
}

// TestCommitsBehind tests counting commits behind base
func TestCommitsBehind(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "behind-branch", "main")
	GitCommitMultiple(t, env.RepoDir, 3, "main")

	g := NewGitInWorktree(env, "behind-branch")
	behind, err := g.GetCommitsBehind("behind-branch", "main")
	if err != nil {
		t.Fatalf("GetCommitsBehind failed: %v", err)
	}

	if behind != 3 {
		t.Errorf("Expected 3 commits behind, got %d", behind)
	}
}

// TestIsBranchMerged_SameCommit tests merge detection when at same commit
func TestIsBranchMerged_SameCommit(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "same-commit", "main")

	g := NewGit(env)
	merged, err := g.IsBranchMerged("same-commit", "main")
	if err != nil {
		t.Fatalf("IsBranchMerged failed: %v", err)
	}

	if !merged {
		t.Error("Branch at same commit should be considered merged")
	}
}

// TestIsBranchMerged_WithCommits tests merge detection with unmerged commits
func TestIsBranchMerged_WithCommits(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "unmerged-feature", "main")

	g := NewGit(env)
	merged, err := g.IsBranchMerged("unmerged-feature", "main")
	if err != nil {
		t.Fatalf("IsBranchMerged failed: %v", err)
	}

	if merged {
		t.Error("Branch with unmerged commits should not be merged")
	}
}

// TestBranchExists tests checking if a branch exists
func TestBranchExists(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	g := NewGit(env)

	if !g.BranchExists("main") {
		t.Error("main branch should exist")
	}

	if g.BranchExists("nonexistent-branch") {
		t.Error("nonexistent-branch should not exist")
	}

	CreateBranch(t, env, "new-branch", "main")
	if !g.BranchExists("new-branch") {
		t.Error("new-branch should exist after creation")
	}
}

// TestGetBranchCommit tests getting the commit hash of a branch
func TestGetBranchCommit(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	g := NewGit(env)
	commit, err := g.GetBranchCommit("main")
	if err != nil {
		t.Fatalf("GetBranchCommit failed: %v", err)
	}

	if len(commit) != 40 {
		t.Errorf("Invalid commit hash length: %d", len(commit))
	}
}

// TestGetLastCommitMessage tests getting the last commit message
func TestGetLastCommitMessage(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "msg-test", "main")

	worktreePath := filepath.Join(env.WorktreeDir, "msg-test")
	GitCommit(t, worktreePath, "test.txt", "content", "Test commit message")

	g := git.New(worktreePath)
	msg, err := g.GetLastCommitMessage()
	if err != nil {
		t.Fatalf("GetLastCommitMessage failed: %v", err)
	}

	if msg != "Test commit message" {
		t.Errorf("Message = %q, want %q", msg, "Test commit message")
	}
}

// TestSync_ConfigDriftHealedAndPushUsesBranch is a regression test for two
// related bugs discovered when ezstack's config has an empty WorktreePath
// for a branch that nevertheless has a real git worktree:
//
//  1. Sync fell into syncViaCheckout, which tried to `git checkout <branch>`
//     in the main repo and failed with "branch is already used by worktree at …".
//  2. When syncViaCheckout succeeded (rarer case), it restored HEAD to main
//     in the main repo, and the subsequent OfferPush call derived the ref
//     to push from CurrentBranch() → pushed main to origin.
//
// This test sets up that exact drift, runs a sync, and asserts:
//   - No worktree collision error.
//   - The sync used the real worktree (no HEAD hopping in main repo).
//   - Config was healed so branch.WorktreePath is back-filled.
func TestSync_ConfigDriftHealedAndPushUsesBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create a bare remote and point origin at it so sync's fetch works.
	bareDir := filepath.Join(env.TmpDir, "origin.git")
	if err := exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run(); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	if err := exec.Command("git", "-C", env.RepoDir, "remote", "add", "origin", bareDir).Run(); err != nil {
		t.Fatalf("add remote: %v", err)
	}
	if err := exec.Command("git", "-C", env.RepoDir, "push", "-u", "origin", "main").Run(); err != nil {
		t.Fatalf("push main: %v", err)
	}

	// Stack: main -> feat-x (dedicated worktree).
	CreateBranchWithCommit(t, env, "feat-x", "main")
	worktreePath := filepath.Join(env.WorktreeDir, "feat-x")
	worktreePath, _ = filepath.EvalSymlinks(worktreePath)

	// Push feat-x to origin so IsLocalAheadOfRemote has a baseline.
	if err := exec.Command("git", "-C", worktreePath, "push", "-u", "origin", "feat-x").Run(); err != nil {
		t.Fatalf("push feat-x: %v", err)
	}

	// Advance main so sync has work to do.
	GitCommit(t, env.RepoDir, "main-update.txt", "main update", "Update main")
	if err := exec.Command("git", "-C", env.RepoDir, "push", "origin", "main").Run(); err != nil {
		t.Fatalf("push main update: %v", err)
	}

	// Simulate config drift: clear WorktreePath on the branch config entry,
	// but leave the git worktree intact. This is the bug-triggering state.
	mgr, err := stack.NewManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	branch := mgr.GetBranch("feat-x")
	if branch == nil {
		t.Fatalf("branch feat-x missing")
	}
	branch.WorktreePath = ""

	// Confirm the git worktree still exists and has feat-x checked out.
	wts, _ := git.New(env.RepoDir).ListWorktrees()
	foundFeatWorktree := false
	for _, wt := range wts {
		if wt.Branch == "feat-x" {
			foundFeatWorktree = true
			break
		}
	}
	if !foundFeatWorktree {
		t.Fatalf("test precondition: git worktree for feat-x not found")
	}

	// Non-interactive callbacks: proceed with rebase, skip the actual push
	// prompt (the push behavior is covered by the unit test
	// TestPushBranch_PushesNamedBranchNotCurrent in internal/git).
	callbacks := &stack.SyncCallbacks{
		BeforeRebase: func(stack.SyncInfo) bool { return true },
		AfterRebase:  func(stack.RebaseResult, *git.Git) bool { return true },
	}

	// Capture the main repo's HEAD before sync so we can verify sync did
	// NOT hop HEAD in the main repo (which is the syncViaCheckout smell).
	mainHeadBefore := getHead(t, env.RepoDir)

	results, err := mgr.SyncStackAll(nil, callbacks)
	if err != nil {
		// Bug #1 surfaced here as: "failed to checkout feat-x: ... branch
		// is already used by worktree at ..."
		t.Fatalf("SyncStackAll: %v", err)
	}

	// One branch should have been synced (feat-x).
	if len(results) == 0 {
		t.Fatal("SyncStackAll returned no results; expected feat-x to need sync")
	}
	var featResult *stack.RebaseResult
	for i := range results {
		if results[i].Branch == "feat-x" {
			featResult = &results[i]
			break
		}
	}
	if featResult == nil {
		t.Fatalf("no result for feat-x; got %+v", results)
	}
	if featResult.HasConflict {
		t.Fatalf("unexpected conflict syncing feat-x: %v", featResult.Error)
	}
	if featResult.Error != nil {
		t.Fatalf("sync error for feat-x: %v", featResult.Error)
	}
	if !featResult.Success {
		t.Fatalf("sync did not succeed for feat-x; result=%+v", featResult)
	}

	// WorktreePath on the result should be the real worktree, not m.repoDir
	// — that's the specific field OfferPush uses. Pre-fix, this was
	// env.RepoDir (the main worktree) which pushed main.
	resolvedResultPath, _ := filepath.EvalSymlinks(featResult.WorktreePath)
	if resolvedResultPath != worktreePath {
		t.Errorf("RebaseResult.WorktreePath = %q, want %q (sync should use the real worktree, not main repo)",
			resolvedResultPath, worktreePath)
	}

	// Config should be healed.
	mgrAfter, _ := stack.NewManager(env.RepoDir)
	branchAfter := mgrAfter.GetBranch("feat-x")
	if branchAfter == nil || branchAfter.WorktreePath == "" {
		t.Errorf("config not healed: branch.WorktreePath is still empty after sync")
	}

	// Main repo HEAD should not have been perturbed.
	mainHeadAfter := getHead(t, env.RepoDir)
	if mainHeadBefore != mainHeadAfter {
		t.Errorf("main repo HEAD changed during sync (%s -> %s); sync should have operated in the branch's own worktree",
			mainHeadBefore, mainHeadAfter)
	}
}

func getHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return string(out)
}
