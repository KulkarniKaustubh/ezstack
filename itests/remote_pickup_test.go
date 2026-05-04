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

// TestNewFromRemoteRef_NoPR_PickupRemainsPushable guards the regression that
// previously came from `ezs new origin/<branch>` calling MarkBranchRemote with
// an empty Remote string: that combination (IsRemote=true, Remote="") sent
// every subsequent push through fork-detection, which on a same-repo PR with no
// fork would persist the _nopush sentinel and silently block all pushes.
//
// Today the pickup IS marked IsRemote=true so the (remote) tag renders in
// `ezs ls`/`ezs status` (the user explicitly asked for that tag on origin/*
// and -r flows). The push-safety contract is preserved by also persisting
// Remote="origin" — ResolveBranchRemote short-circuits on a non-empty Remote
// without re-running fork detection, so the branch stays pushable and never
// flips into _nopush.
func TestNewFromRemoteRef_NoPR_PickupRemainsPushable(t *testing.T) {
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
	// Pickup is intentionally flagged IsRemote so the (remote) tag renders.
	if !b.IsRemote {
		t.Errorf("branch %q expected IsRemote=true on pickup so PrintStack can show (remote)", pickupBranch)
	}
	// And Remote is anchored to "origin" so nothing re-runs fork detection.
	if b.Remote != "origin" {
		t.Errorf("branch %q expected Remote=%q (sentinel that anchors push to origin), got %q",
			pickupBranch, "origin", b.Remote)
	}
	if !b.CanPush() {
		t.Errorf("branch %q CanPush()=false — a plain same-repo pickup must remain pushable", pickupBranch)
	}

	// The _nopush sentinel must not appear anywhere in the persisted stacks.json
	// for this repo: that's the literal regression we were guarding against.
	// The literal string check catches any accidental persistence, even if the
	// Branch struct decoding drifts later.
	stacksPath := filepath.Join(env.ConfigDir, "stacks.json")
	data, err := os.ReadFile(stacksPath)
	if err != nil {
		t.Fatalf("read stacks.json: %v", err)
	}
	if strings.Contains(string(data), "_nopush") {
		t.Errorf("stacks.json contains _nopush after plain pickup:\n%s", string(data))
	}
	// Positive: the `(remote)` tag is driven off `is_remote: true` in the
	// cache. Lock the literal string so a future cache-encoding change can't
	// silently drop the field and erase the tag in `ezs ls`.
	if !strings.Contains(string(data), `"is_remote": true`) {
		t.Errorf("stacks.json missing %q after pickup (drives the (remote) tag):\n%s",
			`"is_remote": true`, string(data))
	}
	// And the explicit Remote=origin sentinel that prevents re-detection.
	if !strings.Contains(string(data), `"remote": "origin"`) {
		t.Errorf("stacks.json missing %q after pickup (prevents fork-detection re-runs):\n%s",
			`"remote": "origin"`, string(data))
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
