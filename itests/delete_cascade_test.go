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

// TestDelete_CascadeRemovesDescendants is the end-to-end regression for
// `ezs delete --cascade`. The feature had no coverage before; if the
// cascade walk ever drops a descendant or fails to recurse, this test
// surfaces the regression before users lose data in a surprise.
func TestDelete_CascadeRemovesDescendants(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Stack: main -> a -> b -> c
	CreateBranchWithCommit(t, env, "branch-a", "main")
	CreateBranchWithCommit(t, env, "branch-b", "branch-a")
	CreateBranchWithCommit(t, env, "branch-c", "branch-b")

	// --cascade prompts "Proceed with cascade delete?" via ConfirmTUI. The
	// CLI's `-y` flag sets YesMode, so we do the same here.
	prevYes := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = prevYes }()

	// Run `ezs delete branch-a --cascade`. We invoke from the repo root,
	// not one of the worktrees we're about to delete.
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(env.RepoDir); err != nil {
		t.Fatal(err)
	}

	if err := commands.Delete([]string{"branch-a", "--cascade"}); err != nil {
		t.Fatalf("Delete --cascade: %v", err)
	}

	// All three branches must be gone from the stack config.
	mgr, err := stack.NewReadOnlyManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewReadOnlyManager: %v", err)
	}
	for _, name := range []string{"branch-a", "branch-b", "branch-c"} {
		if b := mgr.GetBranch(name); b != nil {
			t.Errorf("branch %q survived --cascade", name)
		}
	}

	// And their worktree directories must be removed.
	for _, name := range []string{"branch-a", "branch-b", "branch-c"} {
		wt := filepath.Join(env.WorktreeDir, name)
		if _, err := os.Stat(wt); err == nil {
			t.Errorf("worktree %q still exists after --cascade", wt)
		}
	}
}

// TestDelete_CascadeRefusesOnDirtyDescendant covers the safety guard: if any
// descendant has uncommitted changes, the whole cascade must abort before
// deleting anything. This protects users from accidentally destroying
// in-progress work.
func TestDelete_CascadeRefusesOnDirtyDescendant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree semantics")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "parent", "main")
	CreateBranchWithCommit(t, env, "child", "parent")

	// Leave an uncommitted change in the child worktree.
	dirtyFile := filepath.Join(env.WorktreeDir, "child", "DIRTY.txt")
	if err := os.WriteFile(dirtyFile, []byte("unsaved work"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	prevYes := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = prevYes }()

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(env.RepoDir); err != nil {
		t.Fatal(err)
	}

	err := commands.Delete([]string{"parent", "--cascade"})
	if err == nil {
		t.Fatal("cascade succeeded with a dirty descendant — safety guard failed")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("error %q should mention uncommitted changes", err.Error())
	}

	// Both branches must still exist — cascade aborted before any deletion.
	mgr, _ := stack.NewReadOnlyManager(env.RepoDir)
	for _, name := range []string{"parent", "child"} {
		if b := mgr.GetBranch(name); b == nil {
			t.Errorf("branch %q was deleted despite aborted cascade", name)
		}
	}
}
