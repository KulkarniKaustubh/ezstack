package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
)

// setupContinueScopeEnv mirrors the integration-test setup but is local to
// this file so it can stay narrowly focused on resolveContinueScope's wiring.
// It returns a Manager pointed at a repo with two stacks:
//   - "stk-x" containing branches "x-feat" off main
//   - "stk-y" containing branches "y-feat" off main
//
// Both branches have worktrees, neither has anything in conflict — these tests
// exercise scope resolution logic, not git state.
func setupContinueScopeEnv(t *testing.T) (mgr *stack.Manager, cleanup func()) {
	t.Helper()
	tmp, err := os.MkdirTemp("", "scope-test-*")
	if err != nil {
		t.Fatal(err)
	}
	tmp, _ = filepath.EvalSymlinks(tmp)
	repo := filepath.Join(tmp, "repo")
	wts := filepath.Join(tmp, "worktrees")
	cfgDir := filepath.Join(tmp, "config")
	for _, d := range []string{repo, wts, cfgDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("EZSTACK_HOME", cfgDir)
	exec.Command("git", "-C", repo, "init", "-b", "main").Run()
	exec.Command("git", "-C", repo, "config", "user.email", "t@t.com").Run()
	exec.Command("git", "-C", repo, "config", "user.name", "t").Run()
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0644)
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "commit", "-m", "seed").Run()
	repo, _ = filepath.EvalSymlinks(repo)

	cfg := &config.Config{
		DefaultBaseBranch: "main",
		Repos: map[string]*config.RepoConfig{
			repo: {WorktreeBaseDir: wts},
		},
	}
	cfg.Save()

	mgr, err = stack.NewManager(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CreateBranch("x-feat", "main", filepath.Join(wts, "x-feat"), ""); err != nil {
		t.Fatal(err)
	}
	mgr, _ = stack.NewManager(repo)
	if _, err := mgr.CreateBranch("y-feat", "main", filepath.Join(wts, "y-feat"), ""); err != nil {
		t.Fatal(err)
	}
	mgr, _ = stack.NewManager(repo)

	cleanup = func() {
		os.RemoveAll(tmp)
	}
	return mgr, cleanup
}

func TestResolveContinueScope_BranchFlag(t *testing.T) {
	mgr, cleanup := setupContinueScopeEnv(t)
	defer cleanup()

	scope, err := resolveContinueScope(mgr, nil, false, false, false, "x-feat")
	if err != nil {
		t.Fatal(err)
	}
	if scope.mode != continueModeBranch || scope.branchName != "x-feat" {
		t.Errorf("scope = %+v, want mode=branch, branchName=x-feat", scope)
	}
}

func TestResolveContinueScope_BranchFlagUnknownBranch(t *testing.T) {
	mgr, cleanup := setupContinueScopeEnv(t)
	defer cleanup()

	if _, err := resolveContinueScope(mgr, nil, false, false, false, "nope"); err == nil {
		t.Error("expected error for unknown branch")
	}
}

func TestResolveContinueScope_PositionalHash(t *testing.T) {
	mgr, cleanup := setupContinueScopeEnv(t)
	defer cleanup()

	stacks := mgr.ListStacks()
	if len(stacks) < 1 {
		t.Fatalf("expected ≥1 stack, got %d", len(stacks))
	}
	hash := stacks[0].Hash[:4]

	scope, err := resolveContinueScope(mgr, []string{hash}, false, false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if scope.mode != continueModeSpecificStack || scope.stack == nil {
		t.Errorf("scope = %+v, want mode=specificStack with non-nil stack", scope)
	}
}

func TestResolveContinueScope_AllFlag(t *testing.T) {
	mgr, cleanup := setupContinueScopeEnv(t)
	defer cleanup()

	scope, err := resolveContinueScope(mgr, nil, true, false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if scope.mode != continueModeAll {
		t.Errorf("scope.mode = %d, want continueModeAll", scope.mode)
	}
}

func TestResolveContinueScope_StackFlagNotInStack(t *testing.T) {
	mgr, cleanup := setupContinueScopeEnv(t)
	defer cleanup()

	// repoDir is on main, not in any stack worktree, so -s must error.
	if _, err := resolveContinueScope(mgr, nil, false, true, false, ""); err == nil {
		t.Error("expected error when -s used outside a stack worktree")
	}
}

func TestResolveContinueScope_MultipleSelectorsRejected(t *testing.T) {
	mgr, cleanup := setupContinueScopeEnv(t)
	defer cleanup()

	if _, err := resolveContinueScope(mgr, nil, true, true, false, ""); err == nil {
		t.Error("expected error when both -a and -s are passed")
	}
	if _, err := resolveContinueScope(mgr, nil, true, false, false, "x-feat"); err == nil {
		t.Error("expected error when both -a and -b are passed")
	}
	if _, err := resolveContinueScope(mgr, []string{"deadbeef"}, true, false, false, ""); err == nil {
		t.Error("expected error when both -a and a positional hash are passed")
	}
}

func TestResolveContinueScope_DefaultOnMainIsAll(t *testing.T) {
	mgr, cleanup := setupContinueScopeEnv(t)
	defer cleanup()

	// No flags, no positional, on main → default to all-stacks.
	scope, err := resolveContinueScope(mgr, nil, false, false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if scope.mode != continueModeAll {
		t.Errorf("scope.mode = %d, want continueModeAll (default on main)", scope.mode)
	}
}
