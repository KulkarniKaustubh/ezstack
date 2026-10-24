package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ghstack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// fileAt reports whether path exists in branch's tree.
func fileAt(t *testing.T, repoDir, branch, path string) bool {
	t.Helper()
	return gitOut(t, repoDir, "ls-tree", "--name-only", branch, path) != ""
}

// TestMoveOntoParent_OntoTrunk: reparenting b from a onto main carries only
// b's own commit onto the latest remote main — none of a's.
func TestMoveOntoParent_OntoTrunk(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	addBranch(t, mgr, "a", "main")
	addBranch(t, mgr, "b", "a")
	commitIn(t, repoDir, "main2.txt", "m", "main advances")
	mustGit(t, repoDir, "push", "-q", "origin", "main")
	mgr = reloadManager(t, repoDir)

	if err := moveOntoParent(mgr, "b", "a", "main"); err != nil {
		t.Fatalf("moveOntoParent: %v", err)
	}
	if n := gitOut(t, repoDir, "rev-list", "--count", "origin/main..b"); n != "1" {
		t.Errorf("b has %s commits over origin/main, want 1", n)
	}
	if !fileAt(t, repoDir, "b", "main2.txt") || fileAt(t, repoDir, "b", "a.txt") {
		t.Error("b must sit on the new main without a's commits")
	}
}

// TestMoveOntoParent_OntoAncestorOwnCommitsOnly: moving c from b onto a (b's
// parent) must drop b's commits, using gh-stack's recorded base when the
// catalog has one and the fork point with the old parent otherwise.
func TestMoveOntoParent_OntoAncestorOwnCommitsOnly(t *testing.T) {
	for _, recorded := range []bool{true, false} {
		name := "fork point"
		if recorded {
			name = "recorded base"
		}
		t.Run(name, func(t *testing.T) {
			repoDir, mgr := setupGHStackEnv(t)
			addBranch(t, mgr, "a", "main")
			addBranch(t, mgr, "b", "a")
			c := addBranch(t, mgr, "c", "b")
			commitIn(t, c.WorktreePath, "c2.txt", "c2", "c second")
			mgr = reloadManager(t, repoDir)
			if recorded {
				if _, err := reconcileGHStack(mgr); err != nil {
					t.Fatal(err)
				}
				if ghstack.RecordedBase(repoDir, "c") != gitOut(t, repoDir, "rev-parse", "b") {
					t.Fatal("fixture: c's recorded base should be b's tip")
				}
			}
			if err := moveOntoParent(mgr, "c", "b", "a"); err != nil {
				t.Fatalf("moveOntoParent: %v", err)
			}
			if n := gitOut(t, repoDir, "rev-list", "--count", "a..c"); n != "2" {
				t.Errorf("c has %s commits over a, want its own 2", n)
			}
			if fileAt(t, repoDir, "c", "b.txt") {
				t.Error("b's commit was carried onto a")
			}
			if got := gitOut(t, c.WorktreePath, "branch", "--show-current"); got != "c" {
				t.Errorf("worktree left on %q", got)
			}
		})
	}
}

func TestMoveOntoParent_ConflictAndDirty(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	a := addBranch(t, mgr, "a", "main")
	commitIn(t, a.WorktreePath, "shared.txt", "a version\n", "a writes shared")
	b := addBranch(t, mgr, "b", "main")
	commitIn(t, b.WorktreePath, "shared.txt", "b version\n", "b writes shared")
	mgr = reloadManager(t, repoDir)

	// Dirty worktree: refuse before touching anything.
	os.WriteFile(filepath.Join(b.WorktreePath, "b.txt"), []byte("dirty"), 0o644)
	if err := moveOntoParent(mgr, "b", "main", "a"); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("dirty worktree: %v", err)
	}
	mustGit(t, b.WorktreePath, "checkout", "--", "b.txt")

	err := moveOntoParent(mgr, "b", "main", "a")
	var exit *ui.ExitError
	if !errors.As(err, &exit) || exit.Code != ui.ExitConflict || !strings.Contains(exit.Message, "git rebase --continue") {
		t.Fatalf("conflict: %v", err)
	}
	if git.New(b.WorktreePath).BranchFromRebaseState() != "b" {
		t.Error("the conflicted rebase should be left in b's worktree")
	}
	mustGit(t, b.WorktreePath, "rebase", "--abort")
}

// TestSyncParent_RootParentIsNoOp: `ezs sync -p` on a branch whose parent is
// the stack root points the user at a stack sync instead.
func TestSyncParent_RootParentIsNoOp(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	addBranch(t, mgr, "a", "main")
	mgr = reloadManager(t, repoDir)
	before := gitOut(t, repoDir, "rev-parse", "a")
	p := newPusher(git.New(repoDir), mgr, pushNever, nil)
	if err := syncOntoParent(git.New(repoDir), mgr, mgr.GetBranch("a"), p, nil); err != nil {
		t.Fatal(err)
	}
	if gitOut(t, repoDir, "rev-parse", "a") != before {
		t.Error("branch moved")
	}
}

func TestChainAnchor(t *testing.T) {
	chains := []ghstack.Chain{
		{Trunk: "main", Branches: []string{"a", "b"}, Merged: map[string]bool{}},
		{Trunk: "main", Branches: []string{"a", "c", "m"}, Merged: map[string]bool{"m": true}, Shared: 1},
		{Trunk: "main", Branches: []string{"a"}, Merged: map[string]bool{}, Shared: 1},
	}
	if got := chainAnchor(chains, chains[0]); got != "b" {
		t.Errorf("anchor = %q, want b", got)
	}
	if got := chainAnchor(chains, chains[1]); got != "c" {
		t.Errorf("merged top skipped: anchor = %q, want c", got)
	}
	if got := chainAnchor(chains, chains[2]); got != "" {
		t.Errorf("all-shared chain: anchor = %q, want none", got)
	}
}

// TestWholeChainSteps_MergedFirstChildUniqueAnchors: with the fork's first
// child merged, every planned step still names a branch in exactly one
// chain (gh-stack refuses an ambiguous one with exit 6).
func TestWholeChainSteps_MergedFirstChildUniqueAnchors(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	addBranch(t, mgr, "a", "main")
	addBranch(t, mgr, "b1", "a")
	addBranch(t, mgr, "b2", "a")
	addBranch(t, mgr, "b3", "a")
	if err := mgr.FlagBranchesMerged([]string{"b1"}); err != nil {
		t.Fatal(err)
	}
	sc := mgr.GetStackConfig()
	sc.Cache.GetBranchCache("b1").PRUrl = "https://github.com/o/r/pull/3"
	if err := sc.Save(repoDir); err != nil {
		t.Fatal(err)
	}
	mgr = reloadManager(t, repoDir)
	chains := ghstack.Chains(mgr.ListStacks(), nil)
	steps := wholeChainSteps(git.New(repoDir), mgr, chains, chains)
	if len(steps) != 2 {
		t.Fatalf("steps = %v", describeSteps(steps))
	}
	for _, s := range steps {
		if !ghstack.Unique(chains, s.opts.Branch) {
			t.Errorf("step anchored on shared branch %q", s.opts.Branch)
		}
	}
}

// TestPusher_MixedRemotesNoWrongRemote: a chain whose bottom branch pushes
// to a fork remote (and is already up to date there) must not be pushed by
// `gh stack push` to the other branch's remote; each branch goes to its own.
func TestPusher_MixedRemotesNoWrongRemote(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	fork := filepath.Join(filepath.Dir(repoDir), "fork.git")
	mustGit(t, filepath.Dir(repoDir), "init", "-q", "--bare", "-b", "main", fork)
	mustGit(t, repoDir, "remote", "add", "fork", fork)
	a := addBranch(t, mgr, "a", "main")
	addBranch(t, mgr, "b", "a")
	if err := mgr.SetBranchRemote("a", "fork"); err != nil {
		t.Fatal(err)
	}
	mustGit(t, a.WorktreePath, "push", "-q", "fork", "a")
	mustGit(t, repoDir, "fetch", "-q", "fork")
	mgr = reloadManager(t, repoDir)
	chains := ghstack.Chains(mgr.ListStacks(), nil)

	if err := newPusher(git.New(repoDir), mgr, pushAlways, chains).pushChain(chains[0]); err != nil {
		t.Fatalf("pushChain: %v", err)
	}
	if got := gitOut(t, repoDir, "ls-remote", "origin", "refs/heads/a"); got != "" {
		t.Errorf("fork branch a was pushed to origin: %q", got)
	}
	if got := gitOut(t, repoDir, "ls-remote", "origin", "refs/heads/b"); !strings.HasPrefix(got, gitOut(t, repoDir, "rev-parse", "b")) {
		t.Errorf("b not pushed to origin: %q", got)
	}
}

// collaboratorPush commits on branch in a fresh clone of origin and pushes,
// like a teammate would.
func collaboratorPush(t *testing.T, repoDir, branch, file string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "collab")
	origin := filepath.Join(filepath.Dir(repoDir), "origin.git")
	mustGit(t, filepath.Dir(clone), "clone", "-q", "-b", branch, origin, clone)
	mustGit(t, clone, "config", "user.email", "c@example.invalid")
	mustGit(t, clone, "config", "user.name", "Collab")
	commitIn(t, clone, file, "collab\n", "collaborator work")
	mustGit(t, clone, "push", "-q", "origin", branch)
}

// TestIntegrateRemoteCommits fast-forwards branches others pushed to (in
// their worktree, or by ref when there is none) and holds back from pushing
// branches that diverged or can't be fast-forwarded.
func TestIntegrateRemoteCommits(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	ff := addBranch(t, mgr, "ff", "main")
	div := addBranch(t, mgr, "div", "main")
	dirty := addBranch(t, mgr, "dirty", "main")
	if _, err := mgr.CreateBranchNoWorktree("nowt", "main", ""); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"ff", "div", "dirty", "nowt"} {
		mustGit(t, repoDir, "push", "-q", "origin", b)
		collaboratorPush(t, repoDir, b, b+"-collab.txt")
	}
	commitIn(t, div.WorktreePath, "local.txt", "x", "local work") // diverges
	os.WriteFile(filepath.Join(dirty.WorktreePath, "dirty.txt"), []byte("wip"), 0o644)

	mgr = reloadManager(t, repoDir)
	g := git.New(repoDir)
	chains := ghstack.Chains(mgr.ListStacks(), nil)
	p := newPusher(g, mgr, pushAlways, chains)
	integrateRemoteCommits(g, mgr, chains, p)

	for _, b := range []string{"ff", "nowt"} {
		if gitOut(t, repoDir, "rev-parse", b) != gitOut(t, repoDir, "rev-parse", "origin/"+b) {
			t.Errorf("%s not fast-forwarded to origin/%s", b, b)
		}
		if p.hold[b] {
			t.Errorf("%s held after a clean fast-forward", b)
		}
	}
	if !fileAt(t, repoDir, "ff", "ff-collab.txt") || gitOut(t, ff.WorktreePath, "status", "--porcelain") != "" {
		t.Error("ff worktree not updated cleanly")
	}
	for _, b := range []string{"div", "dirty"} {
		if !p.hold[b] {
			t.Errorf("%s should be held from pushing", b)
		}
		if fileAt(t, repoDir, b, b+"-collab.txt") {
			t.Errorf("%s was moved despite diverging / a dirty worktree", b)
		}
	}

	// A held branch is never force-pushed (that would erase the
	// collaborator's commit).
	remoteDiv := gitOut(t, repoDir, "rev-parse", "origin/div")
	divChain, _ := ghstack.ChainFor(chains, "div")
	if err := p.pushChain(divChain); err != nil {
		t.Fatal(err)
	}
	if got := gitOut(t, repoDir, "ls-remote", "origin", "refs/heads/div"); !strings.HasPrefix(got, remoteDiv) {
		t.Errorf("held branch was pushed: %q", got)
	}
}

func TestEnsureUpstream(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	addBranch(t, mgr, "a", "main")
	g := git.New(repoDir)

	ensureUpstream(g, "a", "origin") // not on the remote yet: no-op
	if _, err := g.RunCapture("rev-parse", "--abbrev-ref", "a@{upstream}"); err == nil {
		t.Fatal("upstream set for an unpushed branch")
	}
	mustGit(t, repoDir, "push", "-q", "origin", "a")
	ensureUpstream(g, "a", "origin")
	if up, err := g.RunCapture("rev-parse", "--abbrev-ref", "a@{upstream}"); err != nil || up != "origin/a" {
		t.Errorf("upstream = %q, %v", up, err)
	}
}

func TestPusher_LimitTo(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	addBranch(t, mgr, "a", "main")
	addBranch(t, mgr, "b", "a")
	addBranch(t, mgr, "c", "b")
	mgr = reloadManager(t, repoDir)
	chains := ghstack.Chains(mgr.ListStacks(), nil)
	p := newPusher(git.New(repoDir), mgr, pushAlways, chains)
	p.limitTo("b")
	if err := p.pushChain(chains[0]); err != nil {
		t.Fatal(err)
	}
	for b, want := range map[string]bool{"a": false, "b": true, "c": true} {
		if got := gitOut(t, repoDir, "ls-remote", "origin", "refs/heads/"+b) != ""; got != want {
			t.Errorf("%s pushed = %v, want %v", b, got, want)
		}
	}
}

func TestPausedScope_Roundtrip(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	addBranch(t, mgr, "a", "main")
	addBranch(t, mgr, "b", "a")
	mgr = reloadManager(t, repoDir)
	s := mgr.GetStackForBranch("a")

	if loadPausedScope(mgr) != nil {
		t.Fatal("nothing saved yet")
	}
	savePausedScope(mgr, &syncTarget{mode: modeChildren, branch: mgr.GetBranch("a"), stacks: []*config.Stack{s}})
	got := loadPausedScope(mgr)
	if got == nil || got.mode != modeChildren || got.branch == nil || got.branch.Name != "a" ||
		len(got.stacks) != 1 || got.stacks[0].Hash != s.Hash {
		t.Fatalf("roundtrip = %+v", got)
	}
	clearPausedScope(mgr)
	if loadPausedScope(mgr) != nil {
		t.Error("cleared scope still loads")
	}
	clearPausedScope(mgr) // idempotent

	// Scopes that no longer resolve are ignored.
	savePausedScope(mgr, &syncTarget{mode: modeCurrent, branch: &config.Branch{Name: "gone"}})
	if loadPausedScope(mgr) != nil {
		t.Error("scope naming a removed branch should not load")
	}
	savePausedScope(mgr, &syncTarget{mode: modeStacks, stacks: []*config.Stack{{Hash: "deadbee"}}})
	if loadPausedScope(mgr) != nil {
		t.Error("scope naming a removed stack should not load")
	}
	os.WriteFile(pausedScopePath(mgr), []byte("{corrupt"), 0o644)
	if loadPausedScope(mgr) != nil {
		t.Error("corrupt scope file should not load")
	}
	if dir, _ := ghstack.CommonDir(repoDir); filepath.Dir(pausedScopePath(mgr)) != dir {
		t.Errorf("scope file %s not in the git common dir", pausedScopePath(mgr))
	}
}

func TestWarnLegacySyncStrategy(t *testing.T) {
	for _, tc := range []struct {
		value    string
		wantWarn bool
	}{{"merge", true}, {"rebase", false}} {
		t.Run(tc.value, func(t *testing.T) {
			repoDir, mgr := setupCLITestEnv(t)
			cfg := mgr.GetConfig()
			rc := cfg.GetRepoConfig(repoDir)
			rc.LegacySyncStrategy = tc.value
			cfg.SetRepoConfig(repoDir, rc)
			if err := cfg.Save(); err != nil {
				t.Fatal(err)
			}
			mgr = reloadManager(t, repoDir)

			_, stderr := captureStdAndErr(t, func() { warnLegacySyncStrategy(mgr) })
			if got := strings.Contains(stderr, "sync_strategy=merge is no longer supported"); got != tc.wantWarn {
				t.Errorf("warned = %v, want %v (stderr %q)", got, tc.wantWarn, stderr)
			}
			loaded, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			if rc := loaded.GetRepoConfig(repoDir); rc == nil || rc.LegacySyncStrategy != "" {
				t.Errorf("sync_strategy not cleared: %+v", rc)
			}
			// Only once.
			_, stderr = captureStdAndErr(t, func() { warnLegacySyncStrategy(reloadManager(t, repoDir)) })
			if stderr != "" {
				t.Errorf("second call warned again: %q", stderr)
			}
		})
	}
}

// TestRunRestack_LeavesPickupBranches (needs gh-stack): a pickup branch at
// the bottom of a chain isn't rebased onto main; one mid-chain limits the
// restack to the layers below it.
func TestRunRestack_LeavesPickupBranches(t *testing.T) {
	if err := ghstack.Check(); err != nil {
		t.Skipf("gh stack unavailable: %v", err)
	}
	repoDir, mgr := setupGHStackEnv(t)
	addBranch(t, mgr, "p", "main") // pickup at the bottom
	addBranch(t, mgr, "q", "p")
	addBranch(t, mgr, "x", "main") // ordinary bottom
	addBranch(t, mgr, "y", "x")    // pickup mid-chain
	addBranch(t, mgr, "z", "y")
	for _, b := range []string{"p", "y"} {
		if err := mgr.MarkBranchRemote(b, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	commitIn(t, repoDir, "main2.txt", "m", "main advances")
	mustGit(t, repoDir, "push", "-q", "origin", "main")
	mgr = reloadManager(t, repoDir)
	before := map[string]string{}
	for _, b := range []string{"p", "q", "y", "z"} {
		before[b] = gitOut(t, repoDir, "rev-parse", b)
	}

	g := git.New(repoDir)
	chains, err := reconcileGHStack(mgr)
	if err != nil {
		t.Fatal(err)
	}
	steps := wholeChainSteps(g, mgr, chains, chains)
	if err := runRestack(g, mgr, steps, newPusher(g, mgr, pushNever, chains)); err != nil {
		t.Fatalf("runRestack: %v", err)
	}
	for b, sha := range before {
		if got := gitOut(t, repoDir, "rev-parse", b); got != sha {
			t.Errorf("%s moved (%s -> %s)", b, sha, got)
		}
	}
	if !fileAt(t, repoDir, "x", "main2.txt") {
		t.Error("x (below the mid-chain pickup) should be restacked onto main")
	}
}
