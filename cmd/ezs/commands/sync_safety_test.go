package commands

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v5/internal/ghstack"
	"github.com/KulkarniKaustubh/ezstack/v5/internal/git"
)

// TestIntegrate_RebasedBranchIsPushed: a branch that differs from its remote
// only because it was rebased locally (the remote tip is in its reflog) is
// not held — it must still be force-pushed, e.g. after `--continue`.
func TestIntegrate_RebasedBranchIsPushed(t *testing.T) {
	requireGHStackForTest(t)
	repoDir, mgr := setupGHStackEnv(t)
	b := addBranch(t, mgr, "b", "main")
	mustGit(t, repoDir, "push", "-q", "origin", "b")
	commitIn(t, repoDir, "main2.txt", "m2\n", "main advances")
	mustGit(t, b.WorktreePath, "rebase", "-q", "main")

	mgr = reloadManager(t, repoDir)
	g := git.New(repoDir)
	chains, err := reconcileGHStack(mgr)
	if err != nil {
		t.Fatal(err)
	}
	p := newPusher(g, mgr, pushAlways, chains)
	integrateRemoteCommits(g, mgr, chains, p)
	if p.hold["b"] {
		t.Fatal("locally rebased branch was held back from pushing")
	}
	c, _ := ghstack.ChainFor(chains, "b")
	if err := p.pushChain(c); err != nil {
		t.Fatal(err)
	}
	if gitOut(t, repoDir, "rev-parse", "b") != strings.Fields(gitOut(t, repoDir, "ls-remote", "origin", "refs/heads/b"))[0] {
		t.Fatal("rebased branch was not pushed")
	}
}

// TestIntegrate_FetchFailureHolds: when a branch's remote can't be fetched,
// its branches are not pushed (the lease would be checked against a stale
// ref).
func TestIntegrate_FetchFailureHolds(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	addBranch(t, mgr, "b", "main")
	mustGit(t, repoDir, "remote", "add", "fork", filepath.Join(t.TempDir(), "missing.git"))
	if err := mgr.SetBranchRemote("b", "fork"); err != nil {
		t.Fatal(err)
	}
	mgr = reloadManager(t, repoDir)
	g := git.New(repoDir)
	chains := ghstack.Chains(mgr.ListStacks(), nil)
	p := newPusher(g, mgr, pushAlways, chains)
	integrateRemoteCommits(g, mgr, chains, p)
	if !p.hold["b"] {
		t.Fatal("branch on an unfetchable remote was not held")
	}
}

// TestPushStack_HoldsCollaboratorCommits: `ezs push -s` must not erase a
// commit someone else pushed (even after a fetch made the lease match);
// -f overrides.
func TestPushStack_HoldsCollaboratorCommits(t *testing.T) {
	requireGHStackForTest(t)
	repoDir, mgr := setupGHStackEnv(t)
	b := addBranch(t, mgr, "b", "main")
	mustGit(t, repoDir, "push", "-q", "origin", "b")
	collaboratorPush(t, repoDir, "b", "theirs.txt")
	commitIn(t, b.WorktreePath, "mine.txt", "mine\n", "my work") // diverges
	mustGit(t, repoDir, "fetch", "-q", "origin")
	theirs := gitOut(t, repoDir, "rev-parse", "origin/b")

	mgr = reloadManager(t, repoDir)
	s := mgr.GetStackForBranch("b")
	if err := pushStackChains(git.New(repoDir), mgr, s, false); err == nil {
		t.Fatal("expected an error: nothing pushed, branch held")
	}
	if got := strings.Fields(gitOut(t, repoDir, "ls-remote", "origin", "refs/heads/b"))[0]; got != theirs {
		t.Fatalf("collaborator commit overwritten: remote b = %s, want %s", got, theirs)
	}
	if err := pushStackChains(git.New(repoDir), mgr, s, true); err != nil {
		t.Fatalf("push -s -f: %v", err)
	}
	if got := strings.Fields(gitOut(t, repoDir, "ls-remote", "origin", "refs/heads/b"))[0]; got != gitOut(t, repoDir, "rev-parse", "b") {
		t.Fatal("-f did not push the held branch")
	}
}

// TestWithMergedHints_DoesNotTouchModel: merged hints flag branches only in
// the copy handed to the projection, never in ezstack's model (a persisted
// IsMerged would make a later sync delete the branch without asking).
func TestWithMergedHints_DoesNotTouchModel(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	addBranch(t, mgr, "a", "main")
	addBranch(t, mgr, "b", "a")
	stacks := mgr.ListStacks()
	hinted := withMergedHints(stacks, map[string]bool{"a": true})
	var flagged bool
	for _, b := range hinted[0].Branches {
		if b.Name == "a" {
			flagged = b.IsMerged
		}
	}
	if !flagged {
		t.Fatal("hint not applied to the copy")
	}
	if mgr.GetBranch("a").IsMerged || reloadManager(t, repoDir).GetBranch("a").IsMerged {
		t.Fatal("hint leaked into the model")
	}
	if got := withMergedHints(stacks, nil); got[0] != stacks[0] {
		t.Fatal("no hints should return the stacks unchanged")
	}
}

// TestBranchRemoval_ForgetsInGHStack: deleting a branch through ezstack drops
// it from the gh-stack catalog (via stack.BranchesRemoved), without
// touching stacks the user made with gh stack directly.
func TestBranchRemoval_ForgetsInGHStack(t *testing.T) {
	requireGHStackForTest(t)
	repoDir, mgr := setupGHStackEnv(t)
	addBranch(t, mgr, "a", "main")
	addBranch(t, mgr, "b", "a")
	if _, err := reconcileGHStack(mgr); err != nil {
		t.Fatal(err)
	}
	if err := mgr.DeleteBranch("b", true); err != nil {
		t.Fatal(err)
	}
	dir, _ := ghstack.CommonDir(repoDir)
	cat, err := ghstack.LoadCatalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range cat.Stacks {
		for _, n := range s.BranchNames() {
			if n == "b" {
				t.Fatalf("deleted branch still tracked by gh stack: %v", s.BranchNames())
			}
		}
	}
}

// TestMoveOntoParent_PrefersOldParentTip: when the branch already contains
// its old parent's current tip, the move starts there — not at an older
// recorded base, which would carry the parent's newer commits along.
func TestMoveOntoParent_PrefersOldParentTip(t *testing.T) {
	requireGHStackForTest(t)
	repoDir, mgr := setupGHStackEnv(t)
	a := addBranch(t, mgr, "a", "main")
	b := addBranch(t, mgr, "b", "a")
	if _, err := reconcileGHStack(mgr); err != nil { // records b's base = a1
		t.Fatal(err)
	}
	commitIn(t, a.WorktreePath, "a2.txt", "a2\n", "a2")
	mustGit(t, b.WorktreePath, "rebase", "-q", "a") // b = a1, a2, b1

	if err := moveOntoParent(mgr, "b", "a", "main"); err != nil {
		t.Fatal(err)
	}
	if fileAt(t, repoDir, "b", "a2.txt") || fileAt(t, repoDir, "b", "a.txt") {
		t.Fatal("old parent's commits were carried onto main")
	}
	if n := gitOut(t, repoDir, "rev-list", "--count", "origin/main..b"); n != "1" {
		t.Fatalf("b has %s commits over main, want 1", n)
	}
}

// TestAvoidPickups covers every position of a pickup branch relative to a
// step's range in chain main ← a ← p(pickup) ← c.
func TestAvoidPickups(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	addBranch(t, mgr, "a", "main")
	addBranch(t, mgr, "p", "a")
	addBranch(t, mgr, "c", "p")
	if err := mgr.MarkBranchRemote("p", "", "origin"); err != nil {
		t.Fatal(err)
	}
	mgr = reloadManager(t, repoDir)
	chains := ghstack.Chains(mgr.ListStacks(), nil)
	c := chains[0]
	tests := []struct {
		name string
		in   ghstack.RebaseOptions
		want string // branch/flags, "" = skipped
	}{
		{"whole chain stops below the pickup", ghstack.RebaseOptions{Branch: "c"}, "a down"},
		{"upstack starting above the pickup is untouched", ghstack.RebaseOptions{Branch: "c", Upstack: true, NoTrunk: true}, "c up notrunk"},
		{"upstack starting at the pickup skips it", ghstack.RebaseOptions{Branch: "p", Upstack: true, NoTrunk: true}, "c up notrunk"},
		{"downstack below the pickup is untouched", ghstack.RebaseOptions{Branch: "a", Downstack: true}, "a down"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, ok := avoidPickups(mgr, chains, restackStep{chain: c, opts: tt.in})
			got := ""
			if ok {
				got = describeOpts(o)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	// A pickup at the bottom is kept with --no-trunk.
	if err := mgr.MarkBranchRemote("a", "", "origin"); err != nil {
		t.Fatal(err)
	}
	mgr = reloadManager(t, repoDir)
	o, ok := avoidPickups(mgr, chains, restackStep{chain: c, opts: ghstack.RebaseOptions{Branch: "a", Downstack: true}})
	if !ok || !o.NoTrunk {
		t.Errorf("bottom pickup: got %q ok=%v, want no-trunk", describeOpts(o), ok)
	}
}

func describeOpts(o ghstack.RebaseOptions) string {
	parts := []string{o.Branch}
	if o.Upstack {
		parts = append(parts, "up")
	}
	if o.Downstack {
		parts = append(parts, "down")
	}
	if o.NoTrunk {
		parts = append(parts, "notrunk")
	}
	return strings.Join(parts, " ")
}

func requireGHStackForTest(t *testing.T) {
	t.Helper()
	if err := ghstack.Check(); err != nil {
		t.Skipf("gh stack unavailable: %v", err)
	}
}
