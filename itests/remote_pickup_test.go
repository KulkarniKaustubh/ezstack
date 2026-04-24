package itests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
)

// TestNewFromRemoteRef_NoPR_DoesNotFlagIsRemote is the end-to-end regression
// test for the bug where `ezs new origin/<branch>` always marked the picked-up
// branch as IsRemote=true, even for branches that simply didn't have a PR yet.
// That spurious flag was then interpreted by `ezs push` as "this branch came
// from another contributor's fork" and latched the branch into config.RemoteNoPush,
// silently blocking every subsequent push to the user's own branch.
//
// The fix in cmd/ezs/commands/new.go only calls MarkBranchRemote when a fork
// was actually detected (detectForkRemote returned non-empty). For same-repo
// pickups — which is the overwhelming common case when users pull up their own
// in-flight branches with `ezs new origin/<name>` — the branch is registered
// like any normal branch, with IsRemote=false.
func TestNewFromRemoteRef_NoPR_DoesNotFlagIsRemote(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	useStubBackend(t)

	// Set up a bare upstream with a branch that has no PR. The upstream URL is a
	// local filesystem path, which is deliberate: it causes newGitHubClient() to
	// fail (no "github.com" in the URL) and short-circuits fork detection — the
	// exact shape that was triggering the bug in practice.
	upstream := filepath.Join(env.TmpDir, "upstream.git")
	mustRunGit(t, "", "init", "--bare", "-b", "main", upstream)

	scratch := filepath.Join(env.TmpDir, "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatalf("mkdir scratch: %v", err)
	}
	mustRunGit(t, scratch, "init", "-q", "-b", "main")
	mustRunGit(t, scratch, "config", "user.email", TestUserEmail)
	mustRunGit(t, scratch, "config", "user.name", TestUserName)
	mustRunGit(t, scratch, "remote", "add", "origin", upstream)
	if err := os.WriteFile(filepath.Join(scratch, "SEED"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	mustRunGit(t, scratch, "add", ".")
	mustRunGit(t, scratch, "commit", "-qm", "seed")
	mustRunGit(t, scratch, "push", "-q", "origin", "main")

	const pickupBranch = "feat-picked-up"
	mustRunGit(t, scratch, "checkout", "-qb", pickupBranch)
	if err := os.WriteFile(filepath.Join(scratch, "feat.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatalf("feat file: %v", err)
	}
	mustRunGit(t, scratch, "add", ".")
	mustRunGit(t, scratch, "commit", "-qm", "feature work")
	mustRunGit(t, scratch, "push", "-q", "origin", pickupBranch)

	// Point the test repo's origin at the bare upstream and align main with it so
	// `ezs new origin/<branch>` can create a worktree from the shared history.
	mustRunGit(t, env.RepoDir, "remote", "add", "origin", upstream)
	mustRunGit(t, env.RepoDir, "fetch", "-q", "origin")
	mustRunGit(t, env.RepoDir, "reset", "--hard", "-q", "origin/main")

	prevCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(env.RepoDir); err != nil {
		t.Fatalf("chdir to repo: %v", err)
	}
	defer os.Chdir(prevCwd)

	if err := commands.New([]string{"origin/" + pickupBranch}); err != nil {
		t.Fatalf("commands.New(origin/%s): %v", pickupBranch, err)
	}

	mgr, err := stack.NewReadOnlyManager(env.RepoDir)
	if err != nil {
		t.Fatalf("NewReadOnlyManager: %v", err)
	}
	b := mgr.GetBranch(pickupBranch)
	if b == nil {
		t.Fatalf("branch %q was not registered", pickupBranch)
	}
	if b.IsRemote {
		t.Errorf("branch %q was flagged IsRemote=true despite no PR and no fork — the bug regressed", pickupBranch)
	}
	if b.Remote != "" {
		t.Errorf("branch %q has Remote=%q persisted; nothing should have been latched", pickupBranch, b.Remote)
	}
	if !b.CanPush() {
		t.Errorf("branch %q CanPush()=false — a plain same-repo pickup must remain pushable", pickupBranch)
	}

	// Belt-and-suspenders: the _nopush sentinel must not appear anywhere in the
	// persisted stacks.json for this repo. The literal string check catches any
	// accidental persistence, even if the Branch struct decoding drifts later.
	stacksPath := filepath.Join(env.ConfigDir, "stacks.json")
	data, err := os.ReadFile(stacksPath)
	if err != nil {
		t.Fatalf("read stacks.json: %v", err)
	}
	if strings.Contains(string(data), "_nopush") {
		t.Errorf("stacks.json contains _nopush after plain pickup:\n%s", string(data))
	}
	if strings.Contains(string(data), `"is_remote": true`) {
		t.Errorf("stacks.json contains is_remote=true after plain pickup:\n%s", string(data))
	}
}

// mustRunGit runs a git command in an optional directory, failing the test on
// error. Named to avoid collision with the package's existing helpers.
func mustRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v (dir=%q): %v\n%s", args, dir, err, out)
	}
}
