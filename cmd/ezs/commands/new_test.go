package commands

import (
	"path/filepath"
	"testing"
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
