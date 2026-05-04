package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestRepo creates a temporary git repository for testing
func setupTestRepo(t *testing.T) (string, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "git-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Initialize git repo
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		os.RemoveAll(dir)
		t.Fatalf("Failed to init git repo: %v", err)
	}

	// Configure git
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test User").Run()

	// Create initial commit
	readmePath := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Test\n"), 0644); err != nil {
		os.RemoveAll(dir)
		t.Fatalf("Failed to create README: %v", err)
	}

	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "Initial commit").Run()

	cleanup := func() {
		os.RemoveAll(dir)
	}

	return dir, cleanup
}

func TestNew(t *testing.T) {
	g := New("/some/path")
	if g.RepoDir != "/some/path" {
		t.Errorf("RepoDir = %q, want %q", g.RepoDir, "/some/path")
	}
}

func TestCurrentBranch(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)
	branch, err := g.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch() error = %v", err)
	}

	// Should be main or master depending on git config
	if branch != "main" && branch != "master" {
		t.Errorf("CurrentBranch() = %q, want main or master", branch)
	}
}

func TestGetRepoRoot(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create a subdirectory
	subdir := filepath.Join(dir, "subdir")
	os.MkdirAll(subdir, 0755)

	g := New(subdir)
	root, err := g.GetRepoRoot()
	if err != nil {
		t.Fatalf("GetRepoRoot() error = %v", err)
	}

	// Resolve symlinks for comparison
	expectedRoot, _ := filepath.EvalSymlinks(dir)
	actualRoot, _ := filepath.EvalSymlinks(root)

	if actualRoot != expectedRoot {
		t.Errorf("GetRepoRoot() = %q, want %q", actualRoot, expectedRoot)
	}
}

func TestBranchExists(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// Create a test branch
	exec.Command("git", "-C", dir, "branch", "test-branch").Run()

	tests := []struct {
		name   string
		branch string
		want   bool
	}{
		{"existing branch", "test-branch", true},
		{"main/master branch", "main", true}, // or master depending on default
		{"nonexistent branch", "nonexistent-branch", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := g.BranchExists(tt.branch)
			// Skip main test if it doesn't exist (might be master)
			if tt.branch == "main" && !got {
				if g.BranchExists("master") {
					return // master exists, which is fine
				}
			}
			if got != tt.want && tt.branch != "main" {
				t.Errorf("BranchExists(%q) = %v, want %v", tt.branch, got, tt.want)
			}
		})
	}
}

func TestGetLastCommitMessage(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// Make a commit with known message
	testFile := filepath.Join(dir, "test.txt")
	os.WriteFile(testFile, []byte("test content"), 0644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "Test commit message").Run()

	msg, err := g.GetLastCommitMessage()
	if err != nil {
		t.Fatalf("GetLastCommitMessage() error = %v", err)
	}

	if msg != "Test commit message" {
		t.Errorf("GetLastCommitMessage() = %q, want %q", msg, "Test commit message")
	}
}

func TestGetLastCommitMessageOf(t *testing.T) {
	// Verifies that GetLastCommitMessageOf inspects the named branch's tip
	// rather than HEAD — required by `pr create --branch <other>` so the
	// WIP-detection heuristic looks at the branch the PR is for, not at
	// whatever is currently checked out.
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)
	mainBranch, _ := g.CurrentBranch()

	// Anchor commit on main so the branch ref exists somewhere.
	mainFile := filepath.Join(dir, "main.txt")
	os.WriteFile(mainFile, []byte("main"), 0644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "main: initial").Run()

	// Create a feature branch with a distinct commit, then leave HEAD on main.
	exec.Command("git", "-C", dir, "checkout", "-b", "feature").Run()
	featureFile := filepath.Join(dir, "feature.txt")
	os.WriteFile(featureFile, []byte("feature"), 0644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "wip: in progress on feature").Run()
	exec.Command("git", "-C", dir, "checkout", mainBranch).Run()

	// HEAD is on main; querying feature must return feature's tip message.
	msg, err := g.GetLastCommitMessageOf("feature")
	if err != nil {
		t.Fatalf("GetLastCommitMessageOf(feature) error = %v", err)
	}
	if msg != "wip: in progress on feature" {
		t.Errorf("GetLastCommitMessageOf(feature) = %q, want %q", msg, "wip: in progress on feature")
	}

	// And HEAD's branch should still report main's message — sanity check
	// that we didn't accidentally change the side-effect-free helper.
	headMsg, err := g.GetLastCommitMessage()
	if err != nil {
		t.Fatalf("GetLastCommitMessage() error = %v", err)
	}
	if headMsg != "main: initial" {
		t.Errorf("GetLastCommitMessage() = %q, want main's tip", headMsg)
	}
}

func TestGetBranchCommit(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)
	currentBranch, _ := g.CurrentBranch()

	commit, err := g.GetBranchCommit(currentBranch)
	if err != nil {
		t.Fatalf("GetBranchCommit() error = %v", err)
	}

	if len(commit) != 40 {
		t.Errorf("GetBranchCommit() returned invalid commit hash: %q", commit)
	}
}

func TestIsBranchMerged(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)
	mainBranch, _ := g.CurrentBranch()

	// Create a feature branch
	exec.Command("git", "-C", dir, "branch", "feature").Run()

	// Feature should be "merged" into main (it's at the same commit)
	merged, err := g.IsBranchMerged("feature", mainBranch)
	if err != nil {
		t.Fatalf("IsBranchMerged() error = %v", err)
	}
	if !merged {
		t.Error("IsBranchMerged() = false, want true for same commit")
	}

	// Add a commit to feature
	exec.Command("git", "-C", dir, "checkout", "feature").Run()
	testFile := filepath.Join(dir, "feature.txt")
	os.WriteFile(testFile, []byte("feature"), 0644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "Feature commit").Run()

	// Now feature is ahead of main, so not merged
	merged, err = g.IsBranchMerged("feature", mainBranch)
	if err != nil {
		t.Fatalf("IsBranchMerged() error = %v", err)
	}
	if merged {
		t.Error("IsBranchMerged() = true, want false for branch with new commits")
	}
}

func TestGetCommitsAhead(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)
	mainBranch, _ := g.CurrentBranch()

	// Create feature branch with 2 commits
	exec.Command("git", "-C", dir, "checkout", "-b", "feature").Run()
	for i := 0; i < 2; i++ {
		testFile := filepath.Join(dir, "file"+string(rune('0'+i))+".txt")
		os.WriteFile(testFile, []byte("content"), 0644)
		exec.Command("git", "-C", dir, "add", ".").Run()
		exec.Command("git", "-C", dir, "commit", "-m", "Commit "+string(rune('1'+i))).Run()
	}

	ahead, err := g.GetCommitsAhead("feature", mainBranch)
	if err != nil {
		t.Fatalf("GetCommitsAhead() error = %v", err)
	}

	if ahead != 2 {
		t.Errorf("GetCommitsAhead() = %d, want 2", ahead)
	}
}

func TestGetCommitsBehind(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// Create feature branch
	exec.Command("git", "-C", dir, "branch", "feature").Run()

	// Add commits to main
	for i := 0; i < 3; i++ {
		testFile := filepath.Join(dir, "main-file"+string(rune('0'+i))+".txt")
		os.WriteFile(testFile, []byte("content"), 0644)
		exec.Command("git", "-C", dir, "add", ".").Run()
		exec.Command("git", "-C", dir, "commit", "-m", "Main commit "+string(rune('1'+i))).Run()
	}

	mainBranch, _ := g.CurrentBranch()
	behind, err := g.GetCommitsBehind("feature", mainBranch)
	if err != nil {
		t.Fatalf("GetCommitsBehind() error = %v", err)
	}

	if behind != 3 {
		t.Errorf("GetCommitsBehind() = %d, want 3", behind)
	}
}

func TestListWorktrees(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	worktrees, err := g.ListWorktrees()
	if err != nil {
		t.Fatalf("ListWorktrees() error = %v", err)
	}

	// Should have at least the main worktree
	if len(worktrees) < 1 {
		t.Error("ListWorktrees() returned no worktrees")
	}
}

func TestCreateWorktree(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)
	mainBranch, _ := g.CurrentBranch()

	worktreePath := filepath.Join(dir, "..", "test-worktree")
	defer os.RemoveAll(worktreePath)

	err := g.CreateWorktree("test-branch", worktreePath, mainBranch)
	if err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}

	// Verify worktree was created
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		t.Error("CreateWorktree() did not create worktree directory")
	}

	// Verify branch exists
	if !g.BranchExists("test-branch") {
		t.Error("CreateWorktree() did not create branch")
	}
}

func TestGetPRTemplate(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// No template exists yet
	template := g.GetPRTemplate()
	if template != "" {
		t.Error("GetPRTemplate() should return empty when no template exists")
	}

	// Create a template
	templateDir := filepath.Join(dir, ".github")
	os.MkdirAll(templateDir, 0755)
	templatePath := filepath.Join(templateDir, "pull_request_template.md")
	templateContent := "## Description\n\n## Checklist\n- [ ] Tests\n"
	os.WriteFile(templatePath, []byte(templateContent), 0644)

	template = g.GetPRTemplate()
	if template != templateContent {
		t.Errorf("GetPRTemplate() = %q, want %q", template, templateContent)
	}
}

func TestRebaseResult(t *testing.T) {
	// Test the RebaseResult struct
	result := RebaseResult{
		Success:     true,
		HasConflict: false,
		Error:       nil,
	}

	if !result.Success {
		t.Error("RebaseResult.Success should be true")
	}

	conflictResult := RebaseResult{
		Success:     false,
		HasConflict: true,
		Error:       nil,
	}

	if conflictResult.Success {
		t.Error("Conflict result should not be success")
	}
	if !conflictResult.HasConflict {
		t.Error("Conflict result should have HasConflict=true")
	}
}

func TestListLocalBranches(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// Create some branches
	exec.Command("git", "-C", dir, "branch", "develop").Run()
	exec.Command("git", "-C", dir, "branch", "staging").Run()
	exec.Command("git", "-C", dir, "branch", "feature-x").Run()

	branches, err := g.ListLocalBranches()
	if err != nil {
		t.Fatalf("ListLocalBranches() error = %v", err)
	}

	// Should contain all branches including main/master
	branchSet := map[string]bool{}
	for _, b := range branches {
		branchSet[b] = true
	}

	for _, expected := range []string{"develop", "staging", "feature-x"} {
		if !branchSet[expected] {
			t.Errorf("ListLocalBranches() missing branch %q, got %v", expected, branches)
		}
	}

	if len(branches) < 4 {
		t.Errorf("ListLocalBranches() returned %d branches, expected at least 4", len(branches))
	}
}

func TestListLocalBranches_Empty(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// Even with just the default branch, should return at least 1
	branches, err := g.ListLocalBranches()
	if err != nil {
		t.Fatalf("ListLocalBranches() error = %v", err)
	}

	if len(branches) < 1 {
		t.Errorf("ListLocalBranches() returned %d branches, expected at least 1", len(branches))
	}
}

func TestValidateBranchName(t *testing.T) {
	valid := []string{"feature-foo", "fix/bar", "my_branch", "a"}
	for _, name := range valid {
		if err := ValidateBranchName(name); err != nil {
			t.Errorf("ValidateBranchName(%q) unexpected error: %v", name, err)
		}
	}

	invalid := []string{"", "-starts-with-dash", "has space", "has..dots", "ends.lock"}
	for _, name := range invalid {
		if err := ValidateBranchName(name); err == nil {
			t.Errorf("ValidateBranchName(%q) expected error, got nil", name)
		}
	}
}

// TestStashPush_FailsOnLockedIndex documents the precondition behind the
// autostash error-handling fix in cmd/ezs/commands/sync.go (syncCurrentBranch)
// and internal/stack/sync.go: StashPush *can* fail in real-world conditions,
// and callers must propagate that error rather than silently rebase over
// uncommitted changes. The deterministic failure mode used here is a
// pre-existing .git/index.lock — git refuses to write the index while another
// process appears to hold the lock.
func TestStashPush_FailsOnLockedIndex(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// Make the working tree dirty so stash has something to push.
	filePath := filepath.Join(dir, "dirty.txt")
	if err := os.WriteFile(filePath, []byte("uncommitted\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Pre-create the index lock. git stash push tries to acquire it,
	// finds it occupied, and bails out with "could not write index".
	lockPath := filepath.Join(dir, ".git", "index.lock")
	if err := os.WriteFile(lockPath, []byte("squatter"), 0644); err != nil {
		t.Fatalf("write index.lock: %v", err)
	}
	defer os.Remove(lockPath)

	err := g.StashPush()
	if err == nil {
		t.Fatal("StashPush should fail with a locked index, but returned nil")
	}
	// Don't pin the exact stderr (varies by git version) — just confirm
	// the error surfaces. The point of this test is that callers cannot
	// assume StashPush always succeeds on a dirty tree.
}

func TestFindEzstackStash_Found(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// Create a file with uncommitted changes, then stash with ezstack message
	filePath := filepath.Join(dir, "test.txt")
	os.WriteFile(filePath, []byte("changes"), 0644)
	exec.Command("git", "-C", dir, "add", "test.txt").Run()

	if err := g.StashPush(); err != nil {
		t.Fatalf("StashPush failed: %v", err)
	}

	branch, _ := g.CurrentBranch()
	idx, found := g.FindEzstackStash(branch)
	if !found {
		t.Fatal("FindEzstackStash should find the stash")
	}
	if idx != 0 {
		t.Errorf("expected index 0, got %d", idx)
	}
}

func TestFindEzstackStash_NotFound(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	_, found := g.FindEzstackStash("main")
	if found {
		t.Error("FindEzstackStash should not find anything in empty stash list")
	}
}

func TestFindEzstackStash_IgnoresOtherBranches(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// Create stash on current branch (main/master)
	filePath := filepath.Join(dir, "test.txt")
	os.WriteFile(filePath, []byte("changes"), 0644)
	exec.Command("git", "-C", dir, "add", "test.txt").Run()
	g.StashPush()

	// Search for a different branch name — should not find it
	_, found := g.FindEzstackStash("some-other-branch")
	if found {
		t.Error("FindEzstackStash should not find stash from a different branch")
	}
}

func TestFindEzstackStash_IgnoresUserStashes(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)
	branch, _ := g.CurrentBranch()

	// Create a user stash (not ezstack)
	filePath := filepath.Join(dir, "user.txt")
	os.WriteFile(filePath, []byte("user work"), 0644)
	exec.Command("git", "-C", dir, "add", "user.txt").Run()
	exec.Command("git", "-C", dir, "stash", "push", "-m", "my user stash").Run()

	// Should NOT find an ezstack stash
	_, found := g.FindEzstackStash(branch)
	if found {
		t.Error("FindEzstackStash should not match user stashes")
	}
}

func TestFindEzstackStash_WithMixedStashes(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)
	branch, _ := g.CurrentBranch()

	// Create user stash first (will be at higher index)
	filePath := filepath.Join(dir, "user.txt")
	os.WriteFile(filePath, []byte("user work"), 0644)
	exec.Command("git", "-C", dir, "add", "user.txt").Run()
	exec.Command("git", "-C", dir, "stash", "push", "-m", "my user stash").Run()

	// Create ezstack stash (will be at index 0, user stash at index 1)
	os.WriteFile(filePath, []byte("ezstack work"), 0644)
	exec.Command("git", "-C", dir, "add", "user.txt").Run()
	g.StashPush()

	idx, found := g.FindEzstackStash(branch)
	if !found {
		t.Fatal("FindEzstackStash should find the ezstack stash")
	}
	if idx != 0 {
		t.Errorf("expected ezstack stash at index 0, got %d", idx)
	}
}

func TestStashPopIndex(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// Create a stash
	filePath := filepath.Join(dir, "test.txt")
	os.WriteFile(filePath, []byte("stashed content"), 0644)
	exec.Command("git", "-C", dir, "add", "test.txt").Run()
	g.StashPush()

	// Pop by index
	if err := g.StashPopIndex(0); err != nil {
		t.Fatalf("StashPopIndex(0) failed: %v", err)
	}

	// Verify stash is gone
	output, _ := g.run("stash", "list")
	if output != "" {
		t.Errorf("stash list should be empty after pop, got: %s", output)
	}

	// Verify file was restored
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(content) != "stashed content" {
		t.Errorf("expected 'stashed content', got %q", string(content))
	}
}

func TestStashPop_Targeted(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// Create user stash first
	filePath := filepath.Join(dir, "file.txt")
	os.WriteFile(filePath, []byte("user data"), 0644)
	exec.Command("git", "-C", dir, "add", "file.txt").Run()
	exec.Command("git", "-C", dir, "stash", "push", "-m", "user stash").Run()

	// Create ezstack stash (now at index 0, user stash at index 1)
	os.WriteFile(filePath, []byte("ezstack data"), 0644)
	exec.Command("git", "-C", dir, "add", "file.txt").Run()
	g.StashPush()

	// Pop should target the ezstack stash, not the user stash
	if err := g.StashPop(); err != nil {
		t.Fatalf("StashPop failed: %v", err)
	}

	// Verify user stash is still there
	output, _ := g.run("stash", "list")
	if output == "" {
		t.Fatal("user stash should still exist after targeted pop")
	}
	if !strings.Contains(output, "user stash") {
		t.Errorf("user stash should be preserved, got: %s", output)
	}
	if strings.Contains(output, "ezstack-autostash") {
		t.Error("ezstack stash should have been popped")
	}
}

// TestStashPop_DetachedHEAD_DoesNotPopUserStash verifies the fix for
// blind `git stash pop` fallback: when HEAD is detached and a user stash
// sits on top of the ezstack autostash, StashPop must NOT pop the user
// stash. It must scan for an ezstack-autostash entry by message.
func TestStashPop_DetachedHEAD_DoesNotPopUserStash(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// Create ezstack stash first (at index 0 initially).
	filePath := filepath.Join(dir, "file.txt")
	os.WriteFile(filePath, []byte("ezstack data"), 0644)
	exec.Command("git", "-C", dir, "add", "file.txt").Run()
	if err := g.StashPush(); err != nil {
		t.Fatalf("StashPush: %v", err)
	}

	// Create a user stash on top of the ezstack stash. Now:
	//   stash@{0} = user stash
	//   stash@{1} = ezstack-autostash
	os.WriteFile(filePath, []byte("user data"), 0644)
	exec.Command("git", "-C", dir, "add", "file.txt").Run()
	exec.Command("git", "-C", dir, "stash", "push", "-m", "user stash").Run()

	// Detach HEAD so StashPop can't determine branch.
	head, _ := g.run("rev-parse", "HEAD")
	if err := exec.Command("git", "-C", dir, "checkout", strings.TrimSpace(head)).Run(); err != nil {
		t.Fatalf("failed to detach HEAD: %v", err)
	}

	if cur, _ := g.CurrentBranch(); cur != "" && cur != "HEAD" {
		t.Fatalf("expected detached HEAD, got %q", cur)
	}

	if err := g.StashPop(); err != nil {
		t.Fatalf("StashPop (detached): %v", err)
	}

	// User stash must still be present.
	out, _ := g.run("stash", "list")
	if !strings.Contains(out, "user stash") {
		t.Errorf("user stash was popped by detached-HEAD fallback; stash list:\n%s", out)
	}
	if strings.Contains(out, "ezstack-autostash") {
		t.Errorf("ezstack-autostash should have been popped, got:\n%s", out)
	}
}

func TestFindAnyEzstackStash(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// No stashes → not found.
	if _, found := g.FindAnyEzstackStash(); found {
		t.Error("expected not found on empty stash list")
	}

	// Create a user stash — still should not match.
	filePath := filepath.Join(dir, "a.txt")
	os.WriteFile(filePath, []byte("x"), 0644)
	exec.Command("git", "-C", dir, "add", "a.txt").Run()
	exec.Command("git", "-C", dir, "stash", "push", "-m", "user only").Run()
	if _, found := g.FindAnyEzstackStash(); found {
		t.Error("expected not found when only user stashes exist")
	}

	// Add an ezstack stash — must be found.
	os.WriteFile(filePath, []byte("y"), 0644)
	exec.Command("git", "-C", dir, "add", "a.txt").Run()
	if err := g.StashPush(); err != nil {
		t.Fatalf("StashPush: %v", err)
	}
	if _, found := g.FindAnyEzstackStash(); !found {
		t.Error("expected to find ezstack stash")
	}
}

func TestIsLocalAheadOfRemote(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// When there's no remote, local should be considered ahead
	ahead, err := g.IsLocalAheadOfRemote("main", "origin")
	if err != nil {
		t.Fatalf("IsLocalAheadOfRemote() error = %v", err)
	}
	if !ahead {
		t.Error("expected branch to be ahead when remote doesn't exist")
	}

	// Also test with empty remote (should default to origin)
	ahead, err = g.IsLocalAheadOfRemote("main", "")
	if err != nil {
		t.Fatalf("IsLocalAheadOfRemote() with empty remote error = %v", err)
	}
	if !ahead {
		t.Error("expected branch to be ahead with empty remote when origin doesn't exist")
	}
}

func TestFindRemoteByOwner(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// Add remotes for testing
	exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/mainowner/repo.git").Run()
	exec.Command("git", "-C", dir, "remote", "add", "fork-user", "https://github.com/forkuser/repo.git").Run()
	exec.Command("git", "-C", dir, "remote", "add", "ssh-fork", "git@github.com:sshfork/repo.git").Run()

	tests := []struct {
		name       string
		owner      string
		wantRemote string
		wantFound  bool
	}{
		{"find HTTPS remote", "forkuser", "fork-user", true},
		{"find SSH remote", "sshfork", "ssh-fork", true},
		{"find origin", "mainowner", "origin", true},
		{"case insensitive", "ForkUser", "fork-user", true},
		{"not found", "nonexistent", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remoteName, _, err := g.FindRemoteByOwner(tt.owner)
			if err != nil {
				t.Fatalf("FindRemoteByOwner(%q) error = %v", tt.owner, err)
			}
			if tt.wantFound && remoteName == "" {
				t.Errorf("FindRemoteByOwner(%q) returned empty, want %q", tt.owner, tt.wantRemote)
			}
			if tt.wantFound && remoteName != tt.wantRemote {
				t.Errorf("FindRemoteByOwner(%q) = %q, want %q", tt.owner, remoteName, tt.wantRemote)
			}
			if !tt.wantFound && remoteName != "" {
				t.Errorf("FindRemoteByOwner(%q) = %q, want empty", tt.owner, remoteName)
			}
		})
	}
}

func TestAddRemote(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// Add a remote
	err := g.AddRemote("myremote", "https://github.com/user/repo.git")
	if err != nil {
		t.Fatalf("AddRemote() error = %v", err)
	}

	// Verify it exists
	url, err := g.GetRemote("myremote")
	if err != nil {
		t.Fatalf("GetRemote() error = %v", err)
	}
	if !strings.Contains(url, "user/repo") {
		t.Errorf("GetRemote() = %q, expected to contain user/repo", url)
	}

	// Adding duplicate should fail
	err = g.AddRemote("myremote", "https://github.com/other/repo.git")
	if err == nil {
		t.Error("AddRemote() should fail for duplicate remote name")
	}
}

func TestPushForce_VariadicRemote(t *testing.T) {
	// Test that Push and PushForce compile and accept variadic args
	// (Can't test actual push without a real remote, but verify the API works)
	g := New("/nonexistent")

	// These should not panic even though the directory doesn't exist
	// They'll return errors, which is fine
	_ = g.Push(false)
	_ = g.Push(false, "custom-remote")
	_ = g.Push(false, "")
	_ = g.PushForce()
	_ = g.PushForce("custom-remote")
	_ = g.PushForce("")
}

// TestPushBranch_PushesNamedBranchNotCurrent is a regression test for the
// sync-pushes-main bug: OfferPush was calling g.Push(false, remote), which
// derives the ref from CurrentBranch(). If HEAD in the worktree was main
// (e.g. after syncViaCheckout restored it), that pushed main. PushBranch
// names the branch explicitly and must push that specific branch regardless
// of what HEAD is currently on.
func TestPushBranch_PushesNamedBranchNotCurrent(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create a sibling branch "feature" on top of the initial commit.
	if out, err := exec.Command("git", "-C", dir, "branch", "feature").CombinedOutput(); err != nil {
		t.Fatalf("create feature branch: %v: %s", err, out)
	}
	// Add a commit only on feature so we can distinguish it from main.
	if err := exec.Command("git", "-C", dir, "checkout", "feature").Run(); err != nil {
		t.Fatalf("checkout feature: %v", err)
	}
	featureFile := filepath.Join(dir, "feature.txt")
	if err := os.WriteFile(featureFile, []byte("feature-only\n"), 0644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "feature commit").Run()
	// Leave HEAD on main so CurrentBranch() would return "main".
	if err := exec.Command("git", "-C", dir, "checkout", "main").Run(); err != nil {
		t.Fatalf("checkout main: %v", err)
	}

	// Create a bare remote.
	bare, err := os.MkdirTemp("", "push-branch-remote-*")
	if err != nil {
		t.Fatalf("mkdtemp remote: %v", err)
	}
	defer os.RemoveAll(bare)
	if err := exec.Command("git", "init", "--bare", "-b", "main", bare).Run(); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "remote", "add", "origin", bare).Run(); err != nil {
		t.Fatalf("add remote: %v", err)
	}

	// Sanity: HEAD is main.
	g := New(dir)
	cur, _ := g.CurrentBranch()
	if cur != "main" {
		t.Fatalf("test setup: expected HEAD on main, got %q", cur)
	}

	// Act: push "feature" explicitly.
	if err := g.PushBranch("feature", false, "origin"); err != nil {
		t.Fatalf("PushBranch(feature): %v", err)
	}

	// Assert: feature ref exists on remote.
	if out, err := exec.Command("git", "-C", bare, "rev-parse", "--verify", "refs/heads/feature").CombinedOutput(); err != nil {
		t.Errorf("feature was not pushed to remote: %v: %s", err, out)
	}

	// Assert: main ref does NOT exist on remote (we never pushed it — if
	// PushBranch incorrectly used CurrentBranch, main would have been pushed
	// instead).
	if err := exec.Command("git", "-C", bare, "rev-parse", "--verify", "refs/heads/main").Run(); err == nil {
		t.Error("main was pushed to remote despite PushBranch being called with 'feature'; regression: push used CurrentBranch() instead of the named branch")
	}
}

// TestPushBranchSetUpstream_PushesNamedBranchAndSetsUpstream is the upstream
// counterpart to TestPushBranch_PushesNamedBranchNotCurrent. It locks in two
// contract points required by `pr create --branch <other>`: (1) the push
// targets the explicitly-named branch, not HEAD, and (2) -u actually sets
// upstream tracking on the named branch. Without this regression coverage,
// PushBranchSetUpstream silently regresses if the helper ever goes back to
// using CurrentBranch() instead of the named-branch argument.
func TestPushBranchSetUpstream_PushesNamedBranchAndSetsUpstream(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create "feature" with a distinct commit, then leave HEAD on main.
	if err := exec.Command("git", "-C", dir, "checkout", "-b", "feature").Run(); err != nil {
		t.Fatalf("checkout -b feature: %v", err)
	}
	featureFile := filepath.Join(dir, "feature.txt")
	if err := os.WriteFile(featureFile, []byte("feature-only\n"), 0644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "feature commit").Run()
	if err := exec.Command("git", "-C", dir, "checkout", "main").Run(); err != nil {
		t.Fatalf("checkout main: %v", err)
	}

	// Bare remote.
	bare, err := os.MkdirTemp("", "push-branch-upstream-remote-*")
	if err != nil {
		t.Fatalf("mkdtemp remote: %v", err)
	}
	defer os.RemoveAll(bare)
	if err := exec.Command("git", "init", "--bare", "-b", "main", bare).Run(); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "remote", "add", "origin", bare).Run(); err != nil {
		t.Fatalf("add remote: %v", err)
	}

	g := New(dir)
	if cur, _ := g.CurrentBranch(); cur != "main" {
		t.Fatalf("test setup: expected HEAD on main, got %q", cur)
	}

	if err := g.PushBranchSetUpstream("feature", "origin"); err != nil {
		t.Fatalf("PushBranchSetUpstream(feature): %v", err)
	}

	// 1. feature got pushed.
	if out, err := exec.Command("git", "-C", bare, "rev-parse", "--verify", "refs/heads/feature").CombinedOutput(); err != nil {
		t.Errorf("feature was not pushed: %v: %s", err, out)
	}

	// 2. main was NOT pushed (would catch a regression where the helper went
	// back to using CurrentBranch).
	if err := exec.Command("git", "-C", bare, "rev-parse", "--verify", "refs/heads/main").Run(); err == nil {
		t.Error("main was pushed to remote despite PushBranchSetUpstream being called with 'feature'")
	}

	// 3. Upstream tracking was actually set on feature (the -u part). git
	// stores this in branch.<name>.remote / branch.<name>.merge.
	out, err := exec.Command("git", "-C", dir, "config", "--get", "branch.feature.remote").CombinedOutput()
	if err != nil {
		t.Errorf("upstream remote not configured for feature: %v: %s", err, out)
	} else if got := strings.TrimSpace(string(out)); got != "origin" {
		t.Errorf("branch.feature.remote = %q, want %q", got, "origin")
	}
	out, err = exec.Command("git", "-C", dir, "config", "--get", "branch.feature.merge").CombinedOutput()
	if err != nil {
		t.Errorf("upstream merge ref not configured for feature: %v: %s", err, out)
	} else if got := strings.TrimSpace(string(out)); got != "refs/heads/feature" {
		t.Errorf("branch.feature.merge = %q, want %q", got, "refs/heads/feature")
	}
}

// TestPushForceBranch_PushesNamedBranchNotCurrent mirrors the PushBranch
// regression coverage for the force-with-lease path. PushForce reads
// CurrentBranch(); PushForceBranch must not.
func TestPushForceBranch_PushesNamedBranchNotCurrent(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	if out, err := exec.Command("git", "-C", dir, "branch", "feature").CombinedOutput(); err != nil {
		t.Fatalf("create feature branch: %v: %s", err, out)
	}
	if err := exec.Command("git", "-C", dir, "checkout", "feature").Run(); err != nil {
		t.Fatalf("checkout feature: %v", err)
	}
	featureFile := filepath.Join(dir, "feature.txt")
	if err := os.WriteFile(featureFile, []byte("feature-only\n"), 0644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "feature commit").Run()
	if err := exec.Command("git", "-C", dir, "checkout", "main").Run(); err != nil {
		t.Fatalf("checkout main: %v", err)
	}

	bare, err := os.MkdirTemp("", "push-force-branch-remote-*")
	if err != nil {
		t.Fatalf("mkdtemp remote: %v", err)
	}
	defer os.RemoveAll(bare)
	if err := exec.Command("git", "init", "--bare", "-b", "main", bare).Run(); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "remote", "add", "origin", bare).Run(); err != nil {
		t.Fatalf("add remote: %v", err)
	}

	g := New(dir)
	if err := g.PushForceBranch("feature", "origin"); err != nil {
		t.Fatalf("PushForceBranch(feature): %v", err)
	}

	if out, err := exec.Command("git", "-C", bare, "rev-parse", "--verify", "refs/heads/feature").CombinedOutput(); err != nil {
		t.Errorf("feature was not force-pushed: %v: %s", err, out)
	}
	if err := exec.Command("git", "-C", bare, "rev-parse", "--verify", "refs/heads/main").Run(); err == nil {
		t.Error("main was pushed despite PushForceBranch being called with 'feature'; force-push leaked CurrentBranch()")
	}
}

// TestHasDivergedFromRemote covers the four states a branch can be in
// relative to a remote ref: no remote, in-sync, local-ahead, true
// divergence. Each case is exercised against a non-"origin" remote so the
// fork-aware path is real, not just hardcoded to "origin".
func TestHasDivergedFromRemote(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// No remote yet -> not diverged, no error.
	diverged, ahead, behind, err := g.HasDivergedFromRemote("main", "fork")
	if err != nil {
		t.Fatalf("missing remote returned error: %v", err)
	}
	if diverged || ahead != 0 || behind != 0 {
		t.Errorf("missing remote: got diverged=%v ahead=%d behind=%d, want all zero/false", diverged, ahead, behind)
	}

	// Set up bare and push main so we have a baseline.
	bare, err := os.MkdirTemp("", "diverged-remote-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer os.RemoveAll(bare)
	if err := exec.Command("git", "init", "--bare", "-b", "main", bare).Run(); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "remote", "add", "fork", bare).Run(); err != nil {
		t.Fatalf("add fork remote: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "push", "fork", "main").Run(); err != nil {
		t.Fatalf("push to fork: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "fetch", "fork").Run(); err != nil {
		t.Fatalf("fetch fork: %v", err)
	}

	// In-sync -> not diverged, ahead/behind zero.
	diverged, ahead, behind, err = g.HasDivergedFromRemote("main", "fork")
	if err != nil {
		t.Fatalf("in-sync error: %v", err)
	}
	if diverged || ahead != 0 || behind != 0 {
		t.Errorf("in-sync: got diverged=%v ahead=%d behind=%d, want all zero/false", diverged, ahead, behind)
	}

	// Add a local commit that fork doesn't have -> ahead, not diverged.
	if err := os.WriteFile(filepath.Join(dir, "local-only.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "local only").Run()
	diverged, ahead, behind, err = g.HasDivergedFromRemote("main", "fork")
	if err != nil {
		t.Fatalf("ahead error: %v", err)
	}
	if diverged || ahead == 0 || behind != 0 {
		t.Errorf("local-ahead: got diverged=%v ahead=%d behind=%d, want diverged=false ahead>0 behind=0", diverged, ahead, behind)
	}

	// Now actually diverge: rewind local main, push a different commit to
	// fork, fetch, leave local with the old "local only" commit. This is the
	// hardest path for the function — it must report both ahead AND behind.
	headBeforeDiverge, _ := g.run("rev-parse", "HEAD")
	exec.Command("git", "-C", dir, "reset", "--hard", "HEAD~1").Run()
	if err := os.WriteFile(filepath.Join(dir, "fork-only.txt"), []byte("y\n"), 0644); err != nil {
		t.Fatalf("write fork-only: %v", err)
	}
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "fork only").Run()
	exec.Command("git", "-C", dir, "push", "-f", "fork", "main").Run()
	exec.Command("git", "-C", dir, "reset", "--hard", strings.TrimSpace(headBeforeDiverge)).Run()
	exec.Command("git", "-C", dir, "fetch", "fork").Run()

	diverged, ahead, behind, err = g.HasDivergedFromRemote("main", "fork")
	if err != nil {
		t.Fatalf("diverged error: %v", err)
	}
	if !diverged || ahead == 0 || behind == 0 {
		t.Errorf("diverged: got diverged=%v ahead=%d behind=%d, want diverged=true ahead>0 behind>0", diverged, ahead, behind)
	}
}

// TestRemoteHasBranch_OriginAndFork pins the contract that pushChildBranches
// uses to gate "never publish a branch the user hasn't shared". Pre-fix, the
// existence check was hardcoded to origin and would return false for a
// fork-tracked branch even when the branch did exist on the user's fork —
// which would either resurrect the silent-publish bug (if the gate's polarity
// flips) or block legitimate fork pushes.
//
// Three cases:
//  1. Branch exists on origin → RemoteHasBranch("origin", b) == true,
//     RemoteHasBranch("fork-user", b) == false.
//  2. Branch exists on a fork remote ("fork-user") but not origin → reverse.
//  3. Empty remote string → defaults to origin (consistency with the rest of
//     the API). This is the migration-friendly default; a future caller that
//     accidentally passes "" still gets the historical origin-only behavior.
func TestRemoteHasBranch_OriginAndFork(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	// Two bare remotes: origin and fork-user. Push different branches to each.
	originBare, err := os.MkdirTemp("", "remote-has-branch-origin-*")
	if err != nil {
		t.Fatalf("mkdtemp origin: %v", err)
	}
	defer os.RemoveAll(originBare)
	forkBare, err := os.MkdirTemp("", "remote-has-branch-fork-*")
	if err != nil {
		t.Fatalf("mkdtemp fork: %v", err)
	}
	defer os.RemoveAll(forkBare)

	for _, b := range []string{originBare, forkBare} {
		if err := exec.Command("git", "init", "--bare", "-b", "main", b).Run(); err != nil {
			t.Fatalf("init bare %s: %v", b, err)
		}
	}
	if err := exec.Command("git", "-C", dir, "remote", "add", "origin", originBare).Run(); err != nil {
		t.Fatalf("add origin: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "remote", "add", "fork-user", forkBare).Run(); err != nil {
		t.Fatalf("add fork-user: %v", err)
	}

	// Create "shared" on top of main, push to origin.
	if err := exec.Command("git", "-C", dir, "checkout", "-b", "shared").Run(); err != nil {
		t.Fatalf("co -b shared: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s.txt"), []byte("s\n"), 0644); err != nil {
		t.Fatalf("write s: %v", err)
	}
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "shared").Run()
	if err := exec.Command("git", "-C", dir, "push", "origin", "shared").Run(); err != nil {
		t.Fatalf("push shared origin: %v", err)
	}

	// Create "fork-only" on top of main, push to fork-user only.
	exec.Command("git", "-C", dir, "checkout", "main").Run()
	if err := exec.Command("git", "-C", dir, "checkout", "-b", "fork-only").Run(); err != nil {
		t.Fatalf("co -b fork-only: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("f\n"), 0644); err != nil {
		t.Fatalf("write f: %v", err)
	}
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "fork-only").Run()
	if err := exec.Command("git", "-C", dir, "push", "fork-user", "fork-only").Run(); err != nil {
		t.Fatalf("push fork-only fork-user: %v", err)
	}

	g := New(dir)

	// Case 1: shared exists on origin only.
	if !g.RemoteHasBranch("origin", "shared") {
		t.Errorf("origin/shared should exist, got false")
	}
	if g.RemoteHasBranch("fork-user", "shared") {
		t.Errorf("fork-user/shared should NOT exist, got true")
	}

	// Case 2: fork-only exists on fork-user only.
	if g.RemoteHasBranch("origin", "fork-only") {
		t.Errorf("origin/fork-only should NOT exist, got true")
	}
	if !g.RemoteHasBranch("fork-user", "fork-only") {
		t.Errorf("fork-user/fork-only should exist, got false")
	}

	// Case 3: empty remote defaults to origin.
	if !g.RemoteHasBranch("", "shared") {
		t.Errorf("RemoteHasBranch(\"\", shared) should default to origin and return true")
	}
	if g.RemoteHasBranch("", "fork-only") {
		t.Errorf("RemoteHasBranch(\"\", fork-only) should default to origin and return false")
	}

	// Case 4: nonexistent branch returns false.
	if g.RemoteHasBranch("origin", "never-existed") {
		t.Errorf("RemoteHasBranch on missing branch should return false")
	}

	// Case 5: nonexistent remote returns false (does NOT panic / error).
	if g.RemoteHasBranch("ghost-remote", "shared") {
		t.Errorf("RemoteHasBranch on missing remote should return false")
	}
}
