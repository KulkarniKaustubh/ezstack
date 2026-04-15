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

func TestIsLocalAheadOfOrigin_DelegatesToRemote(t *testing.T) {
	dir, cleanup := setupTestRepo(t)
	defer cleanup()

	g := New(dir)

	// Deprecated wrapper should work the same
	ahead1, err1 := g.IsLocalAheadOfOrigin("main")
	ahead2, err2 := g.IsLocalAheadOfRemote("main", "origin")

	if err1 != nil || err2 != nil {
		t.Fatalf("errors: %v, %v", err1, err2)
	}
	if ahead1 != ahead2 {
		t.Errorf("IsLocalAheadOfOrigin = %v, IsLocalAheadOfRemote = %v, should match", ahead1, ahead2)
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

// TestParseShortstat is pedantic about the output shapes git actually emits
// so we don't regress on the simple parser that powers every diff count.
func TestParseShortstat(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantAdded   int
		wantRemoved int
	}{
		{"empty", "", 0, 0},
		{"whitespace only", "   \n\t  ", 0, 0},
		{"insertions only", " 1 file changed, 42 insertions(+)", 42, 0},
		{"deletions only", " 1 file changed, 7 deletions(-)", 0, 7},
		{"both", " 3 files changed, 42 insertions(+), 7 deletions(-)", 42, 7},
		{"singular insertion", " 1 file changed, 1 insertion(+)", 1, 0},
		{"singular deletion", " 1 file changed, 1 deletion(-)", 0, 1},
		{"both singular", " 2 files changed, 1 insertion(+), 1 deletion(-)", 1, 1},
		{"trailing newline", " 1 file changed, 5 insertions(+), 2 deletions(-)\n", 5, 2},
		{"malformed field is skipped", " 1 file changed, oops insertions(+), 9 deletions(-)", 0, 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, r := parseShortstat(tt.input)
			if a != tt.wantAdded || r != tt.wantRemoved {
				t.Errorf("parseShortstat(%q) = (%d,%d), want (%d,%d)",
					tt.input, a, r, tt.wantAdded, tt.wantRemoved)
			}
		})
	}
}

// setupRepoWithBareRemote creates a repo wired to a local bare origin, with an
// initial commit on main already pushed. Used by the diff-stat and divergence
// tests below.
func setupRepoWithBareRemote(t *testing.T) (repoDir, bareDir string, cleanup func()) {
	t.Helper()
	tmp, err := os.MkdirTemp("", "ezs-diff-test-*")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cleanup = func() { os.RemoveAll(tmp) }
	repoDir = filepath.Join(tmp, "repo")
	bareDir = filepath.Join(tmp, "remote.git")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		cleanup()
		t.Fatalf("mkdir repo: %v", err)
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
	return repoDir, bareDir, cleanup
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestGetStackDiffStat_Committed covers the baseline case: committed lines
// on a feature branch relative to a local parent.
func TestGetStackDiffStat_Committed(t *testing.T) {
	dir, _, cleanup := setupRepoWithBareRemote(t)
	defer cleanup()
	g := New(dir)

	mustGit(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "f.txt", "a\nb\nc\n")
	mustGit(t, dir, "add", "f.txt")
	mustGit(t, dir, "commit", "-q", "-m", "feat")

	added, removed, err := g.GetStackDiffStat([]string{"main"}, "feature", false)
	if err != nil {
		t.Fatalf("GetStackDiffStat: %v", err)
	}
	if added != 3 || removed != 0 {
		t.Errorf("committed counts = (%d,%d), want (3,0)", added, removed)
	}
}

// TestGetStackDiffStat_WorkingTree asserts that includeWorkingTree picks up
// unstaged + staged changes — the whole point of the fix.
func TestGetStackDiffStat_WorkingTree(t *testing.T) {
	dir, _, cleanup := setupRepoWithBareRemote(t)
	defer cleanup()
	g := New(dir)

	mustGit(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "f.txt", "a\nb\nc\n")
	mustGit(t, dir, "add", "f.txt")
	mustGit(t, dir, "commit", "-q", "-m", "feat")

	// Two unstaged line additions + one staged new file with one line.
	writeFile(t, dir, "f.txt", "a\nb\nc\nd\ne\n")
	writeFile(t, dir, "g.txt", "new\n")
	mustGit(t, dir, "add", "g.txt")

	// Sanity: committed-only still reports 3.
	added, removed, err := g.GetStackDiffStat([]string{"main"}, "feature", false)
	if err != nil {
		t.Fatalf("committed: %v", err)
	}
	if added != 3 || removed != 0 {
		t.Errorf("committed = (%d,%d), want (3,0)", added, removed)
	}

	// Working-tree mode: 3 committed + 2 unstaged + 1 staged = 6.
	added, removed, err = g.GetStackDiffStat([]string{"main"}, "feature", true)
	if err != nil {
		t.Fatalf("working tree: %v", err)
	}
	if added != 6 || removed != 0 {
		t.Errorf("working tree = (%d,%d), want (6,0)", added, removed)
	}
}

// TestGetStackDiffStat_PicksNewestMergeBase is the post-sync regression test:
// when multiple parent candidates resolve, we must pick the merge-base that
// is newest in history so a stale local parent ref doesn't inflate counts.
func TestGetStackDiffStat_PicksNewestMergeBase(t *testing.T) {
	dir, _, cleanup := setupRepoWithBareRemote(t)
	defer cleanup()
	g := New(dir)

	// Timeline:
	//   main ──── A ──── B ──── C (feature)
	//             ▲       ▲
	//             │       └ parent-new
	//             └ parent-old
	// "parent-old" is ancestor of "parent-new"; feature is at C, 1 commit
	// past parent-new. If we use parent-old as the base we get 2 commits
	// of diff; parent-new gives 1. GetStackDiffStat must pick parent-new.
	writeFile(t, dir, "a.txt", "A\n")
	mustGit(t, dir, "add", "a.txt")
	mustGit(t, dir, "commit", "-q", "-m", "A")
	mustGit(t, dir, "branch", "parent-old")

	writeFile(t, dir, "b.txt", "B\n")
	mustGit(t, dir, "add", "b.txt")
	mustGit(t, dir, "commit", "-q", "-m", "B")
	mustGit(t, dir, "branch", "parent-new")

	mustGit(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "c.txt", "C\n")
	mustGit(t, dir, "add", "c.txt")
	mustGit(t, dir, "commit", "-q", "-m", "C")

	// Using only parent-old: 2 lines added (b.txt + c.txt).
	aOld, _, err := g.GetStackDiffStat([]string{"parent-old"}, "feature", false)
	if err != nil {
		t.Fatalf("parent-old only: %v", err)
	}
	if aOld != 2 {
		t.Errorf("parent-old only added = %d, want 2", aOld)
	}

	// Using both: must pick parent-new, so 1 line (c.txt).
	aBoth, _, err := g.GetStackDiffStat([]string{"parent-old", "parent-new"}, "feature", false)
	if err != nil {
		t.Fatalf("both: %v", err)
	}
	if aBoth != 1 {
		t.Errorf("both-candidates added = %d, want 1 (should prefer newest merge-base)", aBoth)
	}

	// Same result when order is reversed.
	aRev, _, err := g.GetStackDiffStat([]string{"parent-new", "parent-old"}, "feature", false)
	if err != nil {
		t.Fatalf("reversed: %v", err)
	}
	if aRev != aBoth {
		t.Errorf("order-dependent: got %d then %d", aBoth, aRev)
	}
}

// TestGetStackDiffStat_SkipsInvalidCandidates verifies that nonexistent parent
// candidates are silently skipped so a partial candidate list still works.
func TestGetStackDiffStat_SkipsInvalidCandidates(t *testing.T) {
	dir, _, cleanup := setupRepoWithBareRemote(t)
	defer cleanup()
	g := New(dir)

	mustGit(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "f.txt", "x\ny\n")
	mustGit(t, dir, "add", "f.txt")
	mustGit(t, dir, "commit", "-q", "-m", "feat")

	added, _, err := g.GetStackDiffStat([]string{"no-such-branch", "main"}, "feature", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}
}

// TestGetStackDiffStat_NoValidCandidates errors cleanly when nothing resolves.
func TestGetStackDiffStat_NoValidCandidates(t *testing.T) {
	dir, _, cleanup := setupRepoWithBareRemote(t)
	defer cleanup()
	g := New(dir)

	_, _, err := g.GetStackDiffStat([]string{"nope", ""}, "main", false)
	if err == nil {
		t.Error("expected error for no valid candidates, got nil")
	}
}

// TestGetStackDiffStat_InvalidBranch errors cleanly when the branch ref is bogus.
func TestGetStackDiffStat_InvalidBranch(t *testing.T) {
	dir, _, cleanup := setupRepoWithBareRemote(t)
	defer cleanup()
	g := New(dir)

	_, _, err := g.GetStackDiffStat([]string{"main"}, "not-a-ref", false)
	if err == nil {
		t.Error("expected error for invalid branch, got nil")
	}
}

// TestLocalDiffersFromRemote covers every combination the dual-count renderer
// cares about: no remote, in sync, diverged by commit, and dirty working tree.
func TestLocalDiffersFromRemote(t *testing.T) {
	dir, _, cleanup := setupRepoWithBareRemote(t)
	defer cleanup()
	g := New(dir)

	mustGit(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "f.txt", "hello\n")
	mustGit(t, dir, "add", "f.txt")
	mustGit(t, dir, "commit", "-q", "-m", "f")

	// 1) Branch has never been pushed → treated as diverged.
	differs, err := g.LocalDiffersFromRemote("feature", false)
	if err != nil {
		t.Fatalf("unpushed: %v", err)
	}
	if !differs {
		t.Error("unpushed branch should be reported as diverged")
	}

	// 2) Push it → in sync.
	mustGit(t, dir, "push", "-q", "origin", "feature")
	differs, err = g.LocalDiffersFromRemote("feature", false)
	if err != nil {
		t.Fatalf("in sync: %v", err)
	}
	if differs {
		t.Error("pushed branch in sync should not be reported as diverged")
	}

	// 3) Dirty working tree on a pushed branch.
	//    With includeWorkingTree=false we ignore the WT (in sync).
	//    With includeWorkingTree=true the dirty WT counts as divergence.
	writeFile(t, dir, "f.txt", "hello\nworld\n")
	if differs, err := g.LocalDiffersFromRemote("feature", false); err != nil {
		t.Fatalf("dirty committed-only: %v", err)
	} else if differs {
		t.Error("dirty WT with includeWT=false should not diverge")
	}
	if differs, err := g.LocalDiffersFromRemote("feature", true); err != nil {
		t.Fatalf("dirty WT: %v", err)
	} else if !differs {
		t.Error("dirty WT with includeWT=true should diverge")
	}

	// 4) New local commit → diverged from origin regardless of includeWT.
	mustGit(t, dir, "add", "f.txt")
	mustGit(t, dir, "commit", "-q", "-m", "more")
	if differs, err := g.LocalDiffersFromRemote("feature", false); err != nil {
		t.Fatalf("ahead: %v", err)
	} else if !differs {
		t.Error("local ahead of origin should diverge even without WT check")
	}
}

// TestGetStackDiffStat_DeletionsCounted makes sure removals also flow through
// the parser correctly — it's easy for a regex-style parser to miss the
// singular-form "deletion(-)".
func TestGetStackDiffStat_DeletionsCounted(t *testing.T) {
	dir, _, cleanup := setupRepoWithBareRemote(t)
	defer cleanup()
	g := New(dir)

	writeFile(t, dir, "big.txt", "1\n2\n3\n4\n5\n")
	mustGit(t, dir, "add", "big.txt")
	mustGit(t, dir, "commit", "-q", "-m", "seed")
	mustGit(t, dir, "push", "-q", "origin", "main")

	mustGit(t, dir, "checkout", "-q", "-b", "shrink")
	writeFile(t, dir, "big.txt", "1\n")
	mustGit(t, dir, "add", "big.txt")
	mustGit(t, dir, "commit", "-q", "-m", "shrink")

	added, removed, err := g.GetStackDiffStat([]string{"main"}, "shrink", false)
	if err != nil {
		t.Fatalf("GetStackDiffStat: %v", err)
	}
	if added != 0 || removed != 4 {
		t.Errorf("counts = (%d,%d), want (0,4)", added, removed)
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
