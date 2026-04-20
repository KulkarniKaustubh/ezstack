package commands

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

func TestValidateWorktreeBaseDir(t *testing.T) {
	tests := []struct {
		name            string
		worktreeBaseDir string
		repoDir         string
		wantErr         bool
		errContains     string
	}{
		{
			name:            "valid - sibling directory",
			worktreeBaseDir: "/home/user/worktrees",
			repoDir:         "/home/user/repo",
			wantErr:         false,
		},
		{
			name:            "valid - parent directory",
			worktreeBaseDir: "/home/user",
			repoDir:         "/home/user/repo",
			wantErr:         false,
		},
		{
			name:            "valid - completely different path",
			worktreeBaseDir: "/tmp/worktrees",
			repoDir:         "/home/user/repo",
			wantErr:         false,
		},
		{
			name:            "valid - empty repo dir",
			worktreeBaseDir: "/home/user/worktrees",
			repoDir:         "",
			wantErr:         false,
		},
		{
			name:            "invalid - same as repo",
			worktreeBaseDir: "/home/user/repo",
			repoDir:         "/home/user/repo",
			wantErr:         true,
			errContains:     "cannot be the repository itself",
		},
		{
			name:            "invalid - inside repo",
			worktreeBaseDir: "/home/user/repo/worktrees",
			repoDir:         "/home/user/repo",
			wantErr:         true,
			errContains:     "cannot be inside the repository",
		},
		{
			name:            "invalid - deeply nested inside repo",
			worktreeBaseDir: "/home/user/repo/some/deep/path",
			repoDir:         "/home/user/repo",
			wantErr:         true,
			errContains:     "cannot be inside the repository",
		},
		{
			name:            "valid - similar prefix but not inside",
			worktreeBaseDir: "/home/user/repo-worktrees",
			repoDir:         "/home/user/repo",
			wantErr:         false,
		},
		{
			name:            "valid - paths with trailing slashes cleaned",
			worktreeBaseDir: "/home/user/worktrees/",
			repoDir:         "/home/user/repo/",
			wantErr:         false,
		},
		{
			name:            "invalid - same path with trailing slash",
			worktreeBaseDir: "/home/user/repo/",
			repoDir:         "/home/user/repo",
			wantErr:         true,
			errContains:     "cannot be the repository itself",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWorktreeBaseDir(tt.worktreeBaseDir, tt.repoDir)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateWorktreeBaseDir() expected error, got nil")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("ValidateWorktreeBaseDir() error = %q, want error containing %q", err.Error(), tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateWorktreeBaseDir() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestValidateWorktreeBaseDirRelativePaths(t *testing.T) {
	// Test with relative-like paths that get cleaned
	repoDir := filepath.Clean("/home/user/repo")

	// Path that looks like it goes up and back in
	worktreeDir := filepath.Clean("/home/user/repo/../worktrees")
	err := ValidateWorktreeBaseDir(worktreeDir, repoDir)
	if err != nil {
		t.Errorf("Expected valid path after cleaning, got error: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// makeStack builds an in-memory *config.Stack for buildParentChoices tests.
// It mirrors what stack.Manager populates at runtime so we don't need a real
// repo or filesystem for these unit tests.
func makeStack(name, hash, root string, branchNames ...string) *config.Stack {
	branches := make([]*config.Branch, len(branchNames))
	parent := root
	for i, n := range branchNames {
		branches[i] = &config.Branch{Name: n, Parent: parent}
		parent = n
	}
	return &config.Stack{
		Hash:     hash,
		Name:     name,
		Root:     root,
		Branches: branches,
	}
}

func TestBuildParentChoices_EmptyStacksOnlyShowsBase(t *testing.T) {
	choices := buildParentChoices("main", nil)
	if len(choices) != 1 {
		t.Fatalf("want 1 choice, got %d: %+v", len(choices), choices)
	}
	if choices[0].branch != "main" {
		t.Errorf("first choice branch = %q, want %q", choices[0].branch, "main")
	}
	if !strings.Contains(choices[0].label, "main") || !strings.Contains(choices[0].label, "NEW stack") {
		t.Errorf("base label %q should mention branch name and 'NEW stack'", choices[0].label)
	}
}

func TestBuildParentChoices_OrdersRootsFirstThenStackMembers(t *testing.T) {
	stacks := []*config.Stack{
		makeStack("feat", "abc1234", "main", "feature-1", "feature-2"),
	}

	choices := buildParentChoices("main", stacks)

	gotBranches := []string{}
	for _, c := range choices {
		gotBranches = append(gotBranches, c.branch)
	}
	want := []string{"main", "feature-1", "feature-2"}
	if len(gotBranches) != len(want) {
		t.Fatalf("got %v, want %v", gotBranches, want)
	}
	for i := range want {
		if gotBranches[i] != want[i] {
			t.Errorf("choice[%d] = %q, want %q", i, gotBranches[i], want[i])
		}
	}
	if !strings.Contains(choices[1].label, "feat") {
		t.Errorf("stack member label %q should reference stack display name", choices[1].label)
	}
	if !strings.Contains(choices[1].label, "adds child") {
		t.Errorf("stack member label %q should warn that it adds a child", choices[1].label)
	}
}

func TestBuildParentChoices_DeduplicatesRoot(t *testing.T) {
	stacks := []*config.Stack{
		makeStack("a", "aaa1111", "main", "branch-a"),
		makeStack("b", "bbb2222", "main", "branch-b"),
	}

	choices := buildParentChoices("main", stacks)

	mainCount := 0
	for _, c := range choices {
		if c.branch == "main" {
			mainCount++
		}
	}
	if mainCount != 1 {
		t.Errorf("main appeared %d times in choices, want 1: %+v", mainCount, choices)
	}
	if choices[0].branch != "main" {
		t.Errorf("base branch should still be first; got %q", choices[0].branch)
	}
}

func TestBuildParentChoices_IncludesNonBaseStackRoots(t *testing.T) {
	stacks := []*config.Stack{
		makeStack("feat", "aaa1111", "main", "branch-a"),
		makeStack("rel", "bbb2222", "release-2.0", "branch-b"),
	}

	choices := buildParentChoices("main", stacks)

	branches := []string{}
	for _, c := range choices {
		branches = append(branches, c.branch)
	}
	// main first (base), then release-2.0 (other root), then stack members.
	want := []string{"main", "release-2.0", "branch-a", "branch-b"}
	if len(branches) != len(want) {
		t.Fatalf("got %v, want %v", branches, want)
	}
	for i := range want {
		if branches[i] != want[i] {
			t.Errorf("choice[%d] = %q, want %q", i, branches[i], want[i])
		}
	}
}

func TestBuildParentChoices_BranchAlsoActingAsRootStaysOnce(t *testing.T) {
	// Pathological but possible: a stack member shares its name with another
	// stack's root. Each branch name should appear exactly once in the picker
	// so the parsed selection index always maps back to one branch.
	stacks := []*config.Stack{
		makeStack("a", "aaa1111", "shared", "branch-x"),
		makeStack("b", "bbb2222", "main", "shared"),
	}

	choices := buildParentChoices("main", stacks)

	count := map[string]int{}
	for _, c := range choices {
		count[c.branch]++
	}
	for name, n := range count {
		if n != 1 {
			t.Errorf("branch %q appeared %d times, want 1", name, n)
		}
	}
}

func TestBuildParentChoices_EmptyBaseBranchSkipped(t *testing.T) {
	stacks := []*config.Stack{
		makeStack("feat", "aaa1111", "develop", "feature-1"),
	}

	choices := buildParentChoices("", stacks)

	if len(choices) == 0 {
		t.Fatal("expected at least the stack root + member")
	}
	if choices[0].branch != "develop" {
		t.Errorf("first choice = %q, want %q (no base branch should fall through to roots)", choices[0].branch, "develop")
	}
}
