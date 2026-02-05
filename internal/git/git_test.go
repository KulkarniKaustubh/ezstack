package git

import (
	"os"
	"os/exec"
	"path/filepath"
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
	cmd := exec.Command("git", "init")
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
