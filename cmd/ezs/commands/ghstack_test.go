package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ghstack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// setupGHStackEnv is setupCLITestEnv plus a bare `origin` that main is pushed
// to, so remote resolution and remote-tracking refs behave like a real clone.
func setupGHStackEnv(t *testing.T) (repoDir string, mgr *stack.Manager) {
	t.Helper()
	repoDir, mgr = setupCLITestEnv(t)
	origin := filepath.Join(filepath.Dir(repoDir), "origin.git")
	mustGit(t, filepath.Dir(repoDir), "init", "-q", "--bare", "-b", "main", origin)
	mustGit(t, repoDir, "remote", "add", "origin", origin)
	mustGit(t, repoDir, "push", "-q", "-u", "origin", "main")
	return repoDir, mgr
}

// addBranch creates name off parent through the manager (with a worktree)
// and gives it one commit touching name.txt.
func addBranch(t *testing.T, mgr *stack.Manager, name, parent string) *config.Branch {
	t.Helper()
	b, err := mgr.CreateBranch(name, parent, "", "")
	if err != nil {
		t.Fatalf("CreateBranch(%s): %v", name, err)
	}
	commitIn(t, b.WorktreePath, name+".txt", name+"\n", name+" work")
	return b
}

func commitIn(t *testing.T, dir, file, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", file)
	mustGit(t, dir, "commit", "-q", "-m", msg)
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func describeSteps(steps []restackStep) []string {
	var out []string
	for _, s := range steps {
		d := s.opts.Branch + "@" + s.opts.Remote
		if s.opts.Downstack {
			d += " down"
		}
		if s.opts.Upstack {
			d += " up"
		}
		if s.opts.NoTrunk {
			d += " notrunk"
		}
		out = append(out, d)
	}
	return out
}

// TestRestackSteps covers how every sync mode maps onto `gh stack rebase`
// invocations for the tree   main ← a ← {b1 ← c, b2},   i.e. the chains
// [a b1 c] and [(a) b2]. The fork point a sits in both chains, so gh-stack
// can't be anchored on it.
func TestRestackSteps(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	addBranch(t, mgr, "a", "main")
	addBranch(t, mgr, "b1", "a")
	addBranch(t, mgr, "c", "b1")
	addBranch(t, mgr, "b2", "a")
	mgr = reloadManager(t, repoDir)
	g := git.New(repoDir)
	chains := ghstack.Chains(mgr.ListStacks(), nil)
	if len(chains) != 2 {
		t.Fatalf("chains = %+v", chains)
	}
	s := mgr.GetStackForBranch("a")

	tests := []struct {
		name   string
		mode   syncMode
		branch string
		want   []string
	}{
		{"whole stack", modeStacks, "", []string{"c@origin", "b2@origin"}},
		{"current, unique anchor", modeCurrent, "b1", []string{"b1@origin down"}},
		{"current, fork point restacks all its subtrees", modeCurrent, "a", []string{"c@origin", "b2@origin"}},
		// Children: only what is above the branch, never the branch itself.
		{"children of the fork point cover every subtree", modeChildren, "a", []string{"b1@origin up notrunk", "b2@origin up notrunk"}},
		{"children of a mid-chain branch", modeChildren, "b1", []string{"c@origin up notrunk"}},
		{"children of a leaf", modeChildren, "c", nil},
		{"branch mode restacks every chain through it", modeBranch, "a", []string{"c@origin", "b2@origin"}},
		{"branch mode on a leaf chain", modeBranch, "b2", []string{"b2@origin"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &syncTarget{mode: tt.mode, stacks: []*config.Stack{s}}
			if tt.branch != "" {
				target.branch = mgr.GetBranch(tt.branch)
			}
			steps, err := restackSteps(g, mgr, chains, target)
			if err != nil {
				t.Fatalf("restackSteps: %v", err)
			}
			if got := describeSteps(steps); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("steps = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("untracked branch errors", func(t *testing.T) {
		_, err := restackSteps(g, mgr, chains, &syncTarget{mode: modeCurrent, branch: &config.Branch{Name: "nope"}})
		if err == nil || !strings.Contains(err.Error(), "nothing to sync") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestChainsForStacks(t *testing.T) {
	chains := []ghstack.Chain{{StackHash: "s1"}, {StackHash: "s2"}, {StackHash: "s1"}}
	got := chainsForStacks(chains, []*config.Stack{{Hash: "s1"}})
	if len(got) != 2 || got[0].StackHash != "s1" || got[1].StackHash != "s1" {
		t.Errorf("chainsForStacks = %+v", got)
	}
	if got := chainsForStacks(chains, nil); len(got) != 0 {
		t.Errorf("no stacks selected: %+v", got)
	}
}

func TestDefaultRemote(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	g := git.New(repoDir)
	if r := defaultRemote(g); r != "" {
		t.Errorf("no remotes: %q", r)
	}
	mustGit(t, repoDir, "remote", "add", "upstream", "https://example.invalid/u.git")
	if r := defaultRemote(g); r != "upstream" {
		t.Errorf("single remote: %q", r)
	}
	mustGit(t, repoDir, "remote", "add", "fork", "https://example.invalid/f.git")
	if r := defaultRemote(g); r != "" {
		t.Errorf("ambiguous remotes must be left to gh-stack: %q", r)
	}
	mustGit(t, repoDir, "remote", "add", "origin", "https://example.invalid/o.git")
	if r := defaultRemote(g); r != "origin" {
		t.Errorf("origin preferred: %q", r)
	}
}

func TestBranchDiffersFromRemote(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	b := addBranch(t, mgr, "feat", "main")
	g := git.New(repoDir)

	if !branchDiffersFromRemote(g, "feat", "origin") {
		t.Error("never-pushed branch must need a push")
	}
	mustGit(t, b.WorktreePath, "push", "-q", "origin", "feat")
	mustGit(t, repoDir, "fetch", "-q", "origin")
	if branchDiffersFromRemote(g, "feat", "origin") {
		t.Error("pushed branch reported as differing")
	}
	commitIn(t, b.WorktreePath, "more.txt", "x", "more")
	if !branchDiffersFromRemote(g, "feat", "origin") {
		t.Error("branch ahead of remote must need a push")
	}
	if branchDiffersFromRemote(g, "does-not-exist", "origin") {
		t.Error("missing local branch must not be pushed")
	}
}

// TestTrunkDiverged: a stack rooted on another stack's branch can't be
// restacked while that branch has diverged from its remote — gh-stack would
// rebase onto the stale remote tip.
func TestTrunkDiverged(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	base := addBranch(t, mgr, "base", "main")
	mgr = reloadManager(t, repoDir)
	g := git.New(repoDir)
	c := ghstack.Chain{Trunk: "base"}

	if trunkDiverged(g, mgr, c, "origin") {
		t.Error("unpushed trunk is not diverged")
	}
	mustGit(t, base.WorktreePath, "push", "-q", "origin", "base")
	mustGit(t, repoDir, "fetch", "-q", "origin")
	if trunkDiverged(g, mgr, c, "origin") {
		t.Error("trunk equal to remote is not diverged")
	}
	commitIn(t, base.WorktreePath, "ahead.txt", "x", "ahead")
	if trunkDiverged(g, mgr, c, "origin") {
		t.Error("trunk ahead of remote is not diverged")
	}
	mustGit(t, base.WorktreePath, "push", "-q", "origin", "base")
	mustGit(t, repoDir, "fetch", "-q", "origin")
	mustGit(t, base.WorktreePath, "commit", "-q", "--amend", "-m", "amended after push")
	if !trunkDiverged(g, mgr, c, "origin") {
		t.Error("amended-after-push trunk must be diverged")
	}
	if trunkDiverged(g, mgr, c, "") {
		t.Error("no remote → never diverged")
	}
	if trunkDiverged(g, mgr, ghstack.Chain{Trunk: "main"}, "origin") {
		t.Error("a non-ezstack trunk (main) is never guarded")
	}
}

// TestPusher_OnlyPublished: commit/reparent restacks push only branches
// that already exist on the remote, one by one.
func TestPusher_OnlyPublished(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	a := addBranch(t, mgr, "a", "main")
	addBranch(t, mgr, "b", "a")
	mustGit(t, a.WorktreePath, "push", "-q", "origin", "a")
	commitIn(t, a.WorktreePath, "a2.txt", "x", "a2")
	mgr = reloadManager(t, repoDir)
	g := git.New(repoDir)
	chains := ghstack.Chains(mgr.ListStacks(), nil)

	p := newPusher(g, mgr, pushAlways, chains)
	p.onlyPublished = true
	if err := p.pushChain(chains[0]); err != nil {
		t.Fatalf("pushChain: %v", err)
	}
	if got, want := gitOut(t, repoDir, "ls-remote", "origin", "refs/heads/a"), gitOut(t, repoDir, "rev-parse", "a"); !strings.HasPrefix(got, want) {
		t.Errorf("published a not pushed: remote %q, local %s", got, want)
	}
	if got := gitOut(t, repoDir, "ls-remote", "origin", "refs/heads/b"); got != "" {
		t.Errorf("unpublished b was pushed: %q", got)
	}
}

func TestPusher_NeverAndAgentNoPush(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	addBranch(t, mgr, "a", "main")
	mgr = reloadManager(t, repoDir)
	g := git.New(repoDir)
	chains := ghstack.Chains(mgr.ListStacks(), nil)

	if err := newPusher(g, mgr, pushNever, chains).pushChain(chains[0]); err != nil {
		t.Fatal(err)
	}
	t.Setenv(agentNoPushEnv, "1")
	if p := newPusher(g, mgr, pushAlways, chains); p.policy != pushNever {
		t.Fatal("EZS_AGENT_NO_PUSH must force pushNever")
	} else if err := p.pushChain(chains[0]); err != nil {
		t.Fatal(err)
	}
	if got := gitOut(t, repoDir, "ls-remote", "origin", "refs/heads/a"); got != "" {
		t.Errorf("branch pushed despite no-push: %q", got)
	}
}

// TestPusher_GHStackPush routes a first chain through `gh stack push`.
func TestPusher_GHStackPush(t *testing.T) {
	if err := ghstack.Check(); err != nil {
		t.Skipf("gh stack unavailable: %v", err)
	}
	repoDir, mgr := setupGHStackEnv(t)
	addBranch(t, mgr, "a", "main")
	addBranch(t, mgr, "b", "a")
	mgr = reloadManager(t, repoDir)
	g := git.New(repoDir)
	chains, err := reconcileGHStack(mgr)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := newPusher(g, mgr, pushAlways, chains).pushChain(chains[0]); err != nil {
		t.Fatalf("pushChain: %v", err)
	}
	for _, b := range []string{"a", "b"} {
		if got := gitOut(t, repoDir, "ls-remote", "origin", "refs/heads/"+b); !strings.HasPrefix(got, gitOut(t, repoDir, "rev-parse", b)) {
			t.Errorf("%s not pushed: %q", b, got)
		}
	}
}

func TestRestackError(t *testing.T) {
	if err := restackError(fmt.Errorf("x: %w", ghstack.ErrConflict)); err != errSyncConflict {
		t.Errorf("conflict → %v", err)
	}
	err := restackError(fmt.Errorf("x: %w", ghstack.ErrRebaseActive))
	var exit *ui.ExitError
	if !errors.As(err, &exit) || exit.Code != ui.ExitConflict || !strings.Contains(exit.Message, "--continue") {
		t.Errorf("rebase active → %v", err)
	}
	other := errors.New("boom")
	if restackError(other) != other {
		t.Error("other errors pass through unchanged")
	}
	if ui.GetExitCode(errSyncConflict) != ui.ExitConflict {
		t.Error("conflict must exit 3")
	}
}

func TestDescribeChain(t *testing.T) {
	c := ghstack.Chain{Trunk: "main", Branches: []string{"a", "b"}, Merged: map[string]bool{"a": true}}
	if got := describeChain(c); got != "(main) ← a [merged] ← b" {
		t.Errorf("describeChain = %q", got)
	}
}

// TestSyncDryRun_JSON previews without touching branches or the gh-stack
// catalog: a bottom branch behind the remote trunk, a child behind its
// parent, and a child whose parent's PR merged.
func TestSyncDryRun_JSON(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	a := addBranch(t, mgr, "a", "main")
	addBranch(t, mgr, "b", "a")
	addBranch(t, mgr, "x", "main")
	addBranch(t, mgr, "y", "x")
	commitIn(t, a.WorktreePath, "a2.txt", "x", "a advances") // b now behind a
	commitIn(t, repoDir, "main2.txt", "m", "main advances")
	mustGit(t, repoDir, "push", "-q", "origin", "main") // a and x behind origin/main

	// x's PR merged: y should report a merged parent, x nothing.
	sc := mgr.GetStackConfig()
	bc := sc.Cache.GetBranchCache("x")
	bc.IsMerged = true
	bc.PRUrl = "https://github.com/o/r/pull/9"
	if err := sc.Save(repoDir); err != nil {
		t.Fatal(err)
	}
	mgr = reloadManager(t, repoDir)
	before := gitOut(t, repoDir, "for-each-ref", "refs/heads")

	out := captureStdoutForTest(t, func() {
		if err := syncDryRun(git.New(repoDir), mgr, &syncTarget{mode: modeStacks, stacks: mgr.ListStacks()}, true); err != nil {
			t.Errorf("syncDryRun: %v", err)
		}
	})
	var infos []syncInfo
	if err := json.Unmarshal([]byte(out), &infos); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	got := map[string]syncInfo{}
	for _, i := range infos {
		got[i.Branch] = i
	}
	if i := got["a"]; !i.NeedsSync || i.BehindBy != 1 || i.BehindParent != "" || i.StackRoot != "main" {
		t.Errorf("a = %+v, want 1 behind main", i)
	}
	if i := got["b"]; !i.NeedsSync || i.BehindBy != 1 || i.BehindParent != "a" {
		t.Errorf("b = %+v, want 1 behind parent a", i)
	}
	if i := got["y"]; !i.NeedsSync || i.MergedParent != "x" {
		t.Errorf("y = %+v, want merged parent x", i)
	}
	if _, ok := got["x"]; ok || len(infos) != 3 {
		t.Errorf("unexpected entries: %+v", infos)
	}

	if after := gitOut(t, repoDir, "for-each-ref", "refs/heads"); after != before {
		t.Error("dry run moved branches")
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".git", "gh-stack")); !os.IsNotExist(err) {
		t.Error("dry run wrote the gh-stack catalog")
	}
}

func TestSyncDryRun_UpToDateIsEmptyJSONArray(t *testing.T) {
	repoDir, mgr := setupGHStackEnv(t)
	b := addBranch(t, mgr, "a", "main")
	if err := os.Chdir(b.WorktreePath); err != nil {
		t.Fatal(err)
	}
	_ = repoDir
	out := captureStdoutForTest(t, func() {
		if err := Sync([]string{"--dry-run", "--json"}); err != nil {
			t.Errorf("Sync: %v", err)
		}
	})
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("up-to-date dry run = %q, want []", out)
	}
}

func TestSync_FlagValidation(t *testing.T) {
	setupCLITestEnv(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--json"}, "--json requires --dry-run"},
		{[]string{"--continue", "--abort"}, "both --continue and --abort"},
		{[]string{"--dry-run", "--continue"}, "--dry-run cannot be combined"},
		{[]string{"--dry-run", "--abort"}, "--dry-run cannot be combined"},
	} {
		if err := Sync(tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Sync(%v) = %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestSync_ContinueAbortWithNothingPaused(t *testing.T) {
	if err := ghstack.Check(); err != nil {
		t.Skipf("gh stack unavailable: %v", err)
	}
	setupCLITestEnv(t)
	if err := Sync([]string{"--continue"}); err == nil || !strings.Contains(err.Error(), "no stopped sync to continue") {
		t.Errorf("--continue: %v", err)
	}
	if err := Sync([]string{"--abort"}); err == nil || !strings.Contains(err.Error(), "no stopped sync to abort") {
		t.Errorf("--abort: %v", err)
	}
}

// TestSquashStackChildren_DoesNotRevertParent: the parent gained a commit the
// child was never rebased onto. Squashing the child must collapse only the
// child's own commits onto its fork point — resetting onto the parent's
// current tip (the old behaviour) turned the squash into a revert of a3.
func TestSquashStackChildren_DoesNotRevertParent(t *testing.T) {
	_, mgr := setupCLITestEnv(t)
	a := addBranch(t, mgr, "a", "main")
	b := addBranch(t, mgr, "b", "a")
	commitIn(t, b.WorktreePath, "b2.txt", "b2", "b second")
	forkPoint := gitOut(t, b.WorktreePath, "rev-parse", "a")
	commitIn(t, a.WorktreePath, "a3.txt", "a3", "a advances after b forked")

	if err := os.Chdir(b.WorktreePath); err != nil {
		t.Fatal(err)
	}
	m, err := stack.NewManager(b.WorktreePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := squashStackChildren(m); err != nil {
		t.Fatalf("squash: %v", err)
	}

	if n := gitOut(t, b.WorktreePath, "rev-list", "--count", forkPoint+"..b"); n != "1" {
		t.Errorf("b has %s commits over its fork point, want 1", n)
	}
	changed := gitOut(t, b.WorktreePath, "diff", "--name-only", forkPoint, "b")
	if changed != "b.txt\nb2.txt" {
		t.Errorf("squash commit touches %q, want only b's files (a3.txt reverted?)", changed)
	}
}
