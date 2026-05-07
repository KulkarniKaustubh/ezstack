package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/github"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
)

// setupForkResolveRepo creates an isolated ezstack environment whose origin
// URL points to a (fake) personal fork on github.com. Tests can then layer
// upstream config on top to exercise resolvePRTarget across modes.
func setupForkResolveRepo(t *testing.T) (string, *stack.Manager, *git.Git) {
	t.Helper()
	tmpDir, _ := filepath.EvalSymlinks(t.TempDir())
	repoDir := filepath.Join(tmpDir, "repo")
	configDir := filepath.Join(tmpDir, "config")
	for _, d := range []string{repoDir, configDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	t.Setenv("EZSTACK_HOME", configDir)

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "init")
	// The origin URL is a github.com URL pointing at the contributor's fork.
	run("remote", "add", "origin", "git@github.com:contributor/myproject.git")

	repoDir, _ = filepath.EvalSymlinks(repoDir)

	cfg := &config.Config{
		DefaultBaseBranch: "main",
		Repos: map[string]*config.RepoConfig{
			repoDir: {WorktreeBaseDir: tmpDir},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	mgr, err := stack.NewManager(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	return repoDir, mgr, git.New(repoDir)
}

func TestResolvePRTarget_NoForkMode_ClassicSameRepo(t *testing.T) {
	_, mgr, g := setupForkResolveRepo(t)
	b := &config.Branch{Name: "feat-a", Parent: "main"}
	pt, err := resolvePRTarget(g, mgr, b, nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if pt.IsCrossRepo {
		t.Error("nil upstream → IsCrossRepo should be false")
	}
	if pt.TargetOwner != "contributor" || pt.TargetRepo != "myproject" {
		t.Errorf("target = %s/%s, want contributor/myproject", pt.TargetOwner, pt.TargetRepo)
	}
	if pt.HeadRef != "feat-a" || pt.BaseRef != "main" {
		t.Errorf("head/base = %q/%q, want feat-a/main", pt.HeadRef, pt.BaseRef)
	}
}

func TestResolvePRTarget_ForkMode_BottomBranchIsCrossRepo(t *testing.T) {
	_, mgr, g := setupForkResolveRepo(t)
	up := &upstreamInfo{
		Owner: "upstreamOrg", Repo: "myproject",
		Remote: "upstream", DefaultBranch: "main",
		Enabled: true,
	}
	b := &config.Branch{Name: "feat-a", Parent: "main"}
	pt, err := resolvePRTarget(g, mgr, b, up)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !pt.IsCrossRepo {
		t.Error("bottom of fork stack should be cross-repo")
	}
	if pt.TargetOwner != "upstreamOrg" || pt.TargetRepo != "myproject" {
		t.Errorf("target = %s/%s, want upstreamOrg/myproject", pt.TargetOwner, pt.TargetRepo)
	}
	if pt.HeadRef != "contributor:feat-a" {
		t.Errorf("HeadRef = %q, want contributor:feat-a", pt.HeadRef)
	}
	if pt.BaseRef != "main" {
		t.Errorf("BaseRef = %q, want main", pt.BaseRef)
	}
}

func TestResolvePRTarget_ForkMode_IntermediateBranchStaysInFork(t *testing.T) {
	_, mgr, g := setupForkResolveRepo(t)
	up := &upstreamInfo{
		Owner: "upstreamOrg", Repo: "myproject",
		Remote: "upstream", DefaultBranch: "main",
		Enabled: true,
	}
	// Parent is another local branch, NOT main → fork-side intermediate.
	b := &config.Branch{Name: "feat-b", Parent: "feat-a"}
	pt, err := resolvePRTarget(g, mgr, b, up)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if pt.IsCrossRepo {
		t.Error("intermediate branch should NOT be cross-repo")
	}
	if pt.TargetOwner != "contributor" || pt.TargetRepo != "myproject" {
		t.Errorf("target = %s/%s, want contributor/myproject (fork)", pt.TargetOwner, pt.TargetRepo)
	}
	if pt.HeadRef != "feat-b" || pt.BaseRef != "feat-a" {
		t.Errorf("head/base = %q/%q, want feat-b/feat-a", pt.HeadRef, pt.BaseRef)
	}
}

func TestResolvePRTarget_SameOrgFork_ReturnsHardError(t *testing.T) {
	// Origin = contributor/myproject; if upstream owner == contributor (same
	// org / personal-fork-of-self via API quirk), we return a hard error
	// because the cross-fork-same-org flow needs GraphQL.
	_, mgr, g := setupForkResolveRepo(t)
	up := &upstreamInfo{
		Owner: "contributor", Repo: "myproject",
		Remote: "upstream", DefaultBranch: "main",
		Enabled: true,
	}
	b := &config.Branch{Name: "feat-a", Parent: "main"}
	if _, err := resolvePRTarget(g, mgr, b, up); err == nil {
		t.Fatal("expected error for same-org fork")
	}
}

func TestResolvePRTarget_ForkModeDisabledUpstream_FallsBackToOrigin(t *testing.T) {
	// up != nil but Enabled=false → behave like classic flow.
	_, mgr, g := setupForkResolveRepo(t)
	up := &upstreamInfo{
		Owner: "upstreamOrg", Repo: "myproject",
		Remote: "upstream", DefaultBranch: "main",
		Enabled: false,
	}
	b := &config.Branch{Name: "feat-a", Parent: "main"}
	pt, err := resolvePRTarget(g, mgr, b, up)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if pt.IsCrossRepo {
		t.Error("disabled upstream → should not be cross-repo")
	}
	if pt.TargetOwner != "contributor" {
		t.Errorf("target owner = %q, want contributor", pt.TargetOwner)
	}
}

func TestResolvePRTarget_BranchParentMatchesUpstreamDefault(t *testing.T) {
	// If stack root is upstream's default branch but mgr.IsMainBranch returns
	// false (configured base branch differs), the fallback `b.Parent ==
	// up.DefaultBranch` should still route cross-repo.
	_, mgr, g := setupForkResolveRepo(t)
	up := &upstreamInfo{
		Owner: "upstreamOrg", Repo: "myproject",
		Remote: "upstream", DefaultBranch: "trunk",
		Enabled: true,
	}
	b := &config.Branch{Name: "feat-a", Parent: "trunk"}
	pt, err := resolvePRTarget(g, mgr, b, up)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !pt.IsCrossRepo {
		t.Error("parent matching upstream default branch should be cross-repo")
	}
	if pt.BaseRef != "trunk" {
		t.Errorf("BaseRef = %q, want trunk", pt.BaseRef)
	}
}

func TestClientForBranch_RoutesUpstreamWhenTargetIsUpstream(t *testing.T) {
	originClient, err := github.NewClient("git@github.com:contributor/myproject.git")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	up := &upstreamInfo{Owner: "upstreamOrg", Repo: "myproject", Enabled: true}

	// Default (PRTargetRepo unset) → origin client.
	if got := clientForBranch(&config.Branch{}, originClient, up); got != originClient {
		t.Error("default branch should use origin client")
	}

	// fork-target → origin client (the fork *is* origin).
	bFork := &config.Branch{PRTargetRepo: config.PRTargetRepoFork}
	if got := clientForBranch(bFork, originClient, up); got != originClient {
		t.Error("fork-target branch should use origin client")
	}

	// upstream-target → new client targeted at upstream owner/repo.
	bUp := &config.Branch{PRTargetRepo: config.PRTargetRepoUpstream}
	got := clientForBranch(bUp, originClient, up)
	if got == originClient {
		t.Error("upstream-target branch should NOT use origin client")
	}
	if got.Owner() != "upstreamOrg" {
		t.Errorf("upstream client owner = %q, want upstreamOrg", got.Owner())
	}
}
