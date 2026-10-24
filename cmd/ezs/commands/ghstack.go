package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ghstack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// This file is the bridge between ezstack commands and the gh-stack engine
// (internal/ghstack): projecting stacks into gh-stack, running cascades,
// pushing, and turning gh-stack failures into actionable ezstack errors.

// errSyncConflict is returned when a cascade stops on a conflict. Exit code 3
// lets scripts distinguish "resolve and continue" from other failures.
var errSyncConflict = ui.NewExitError(ui.ExitConflict,
	"rebase stopped on a conflict: resolve it in the worktree named above, `git add` the files, then run `ezs sync --continue` (or `ezs sync --abort` to undo the whole sync)")

// requireGHStack fails fast with install guidance when gh-stack is unusable.
func requireGHStack() error {
	if err := ghstack.Check(); err != nil {
		return fmt.Errorf("ezstack uses `gh stack` to rebase and push stacks: %w", err)
	}
	return nil
}

// reconcileGHStack projects every stack of mgr's repository into the gh-stack
// catalog and returns the resulting chains (parent chains first). forget names
// branches that were just removed from ezstack's model.
func reconcileGHStack(mgr *stack.Manager, forget ...string) ([]ghstack.Chain, error) {
	chains, err := ghstack.Reconcile(mgr.GetRepoDir(), mgr.ListStacks(), forget...)
	if errors.Is(err, ghstack.ErrRebasePaused) {
		return nil, ui.NewExitError(ui.ExitConflict, "%s", err.Error())
	}
	return chains, err
}

// refreshGHStack is the best-effort reconcile run after structural changes
// (new, reparent, merged cleanup) so the catalog — which `gh stack` commands
// run by hand also read — never lags ezstack's model. Removed branches need
// no hint: Reconcile drops catalog stacks that reference a missing branch.
// It never fails the calling command: the next sync reconciles again and
// reports real problems.
func refreshGHStack(mgr *stack.Manager, forget ...string) {
	if ghstack.Check() != nil {
		return
	}
	if _, err := ghstack.Reconcile(mgr.GetRepoDir(), mgr.ListStacks(), forget...); err != nil &&
		!errors.Is(err, ghstack.ErrRebasePaused) {
		ui.Warn(fmt.Sprintf("Could not update gh stack tracking: %v", err))
	}
}

// restackStep is one `gh stack rebase` invocation over a chain.
type restackStep struct {
	chain ghstack.Chain
	opts  ghstack.RebaseOptions
}

// wholeChainSteps restacks each of targets fully (trunk fetch included).
// all is every chain of the repository, used to pick anchors gh-stack accepts.
func wholeChainSteps(g *git.Git, mgr *stack.Manager, targets, all []ghstack.Chain) []restackStep {
	steps := make([]restackStep, 0, len(targets))
	for _, c := range targets {
		anchor := chainAnchor(all, c)
		if anchor == "" {
			continue // every active layer is shared: another chain restacks it
		}
		steps = append(steps, restackStep{chain: c, opts: ghstack.RebaseOptions{
			Branch: anchor,
			Remote: trunkRemote(g, mgr, c),
		}})
	}
	return steps
}

// chainAnchor returns the topmost active layer of c that is in no other
// chain. gh-stack resolves the stack to rebase from that branch and refuses
// a branch that sits in several stacks (a tree fork point). "" means every
// active layer is shared — the chains below the fork restack them.
func chainAnchor(all []ghstack.Chain, c ghstack.Chain) string {
	active := c.Active()
	for i := len(active) - 1; i >= 0; i-- {
		if ghstack.Unique(all, active[i]) {
			return active[i]
		}
	}
	return ""
}

// chainsForStacks filters chains to the given ezstack stacks.
func chainsForStacks(chains []ghstack.Chain, stacks []*config.Stack) []ghstack.Chain {
	want := map[string]bool{}
	for _, s := range stacks {
		want[s.Hash] = true
	}
	var out []ghstack.Chain
	for _, c := range chains {
		if want[c.StackHash] {
			out = append(out, c)
		}
	}
	return out
}

// trunkRemote picks the remote gh-stack fetches a chain's trunk from: the
// trunk's own push remote when the stack is rooted on another stack's branch,
// otherwise origin (or the only remote). "" lets gh-stack auto-detect.
func trunkRemote(g *git.Git, mgr *stack.Manager, c ghstack.Chain) string {
	if b := mgr.GetBranch(c.Trunk); b != nil && b.CanPush() {
		return b.EffectiveRemote()
	}
	return defaultRemote(g)
}

func defaultRemote(g *git.Git) string {
	out, err := g.RunCapture("remote")
	if err != nil {
		return ""
	}
	remotes := strings.Fields(out)
	for _, r := range remotes {
		if r == "origin" {
			return r
		}
	}
	if len(remotes) == 1 {
		return remotes[0]
	}
	return ""
}

// pushPolicy decides whether rebased chains are pushed.
type pushPolicy int

const (
	pushNever pushPolicy = iota
	pushAsk              // confirm once (auto-yes under -y / MCP)
	pushAlways
)

// pusher pushes chains under a policy, asking at most once per run.
type pusher struct {
	g       *git.Git
	mgr     *stack.Manager
	chains  []ghstack.Chain // every chain of the repo (for stack resolution)
	policy  pushPolicy
	decided bool
	allowed bool
	// onlyPublished restricts pushes to branches that already exist on
	// their remote.
	onlyPublished bool
	// includeRemote lets the run rebase and push pickup branches (another
	// contributor's PR branch); by default they are left untouched.
	includeRemote bool
	// only, when non-nil, limits pushes to these branches (e.g. the subtree
	// a commit or reparent restacked, not the ancestors in its chain).
	only map[string]bool
	// hold names branches that must not be force-pushed this run: they had
	// diverged from their remote before the sync (someone else pushed), so a
	// lease against the fetched ref would erase those commits.
	hold map[string]bool
	// noted records pickup branches already reported as skipped.
	noted map[string]bool
}

// limitTo restricts the pusher to branch and its descendants.
func (p *pusher) limitTo(branch string) {
	p.only = map[string]bool{branch: true}
	for _, d := range p.mgr.GetDescendants(branch) {
		p.only[d.Name] = true
	}
}

func newPusher(g *git.Git, mgr *stack.Manager, policy pushPolicy, chains []ghstack.Chain) *pusher {
	if os.Getenv(agentNoPushEnv) == "1" {
		policy = pushNever
	}
	return &pusher{g: g, mgr: mgr, policy: policy, chains: chains}
}

// pushChain force-with-lease pushes the chain's own branches (shared fork
// layers are pushed with the chain that owns them) that differ from their
// remote.
func (p *pusher) pushChain(c ghstack.Chain) error {
	if p.policy == pushNever {
		return nil
	}
	type target struct{ branch, remote string }
	var targets []target
	remotes := map[string]bool{}
	partial := false // some own branch is excluded, so gh-stack can't push the chain
	for _, b := range c.Own() {
		br := p.mgr.GetBranch(b)
		switch {
		case p.only != nil && !p.only[b]:
			partial = true
			continue
		case p.hold[b]:
			partial = true
			continue
		case br != nil && br.IsRemote && !p.includeRemote:
			if !p.noted[b] {
				if p.noted == nil {
					p.noted = map[string]bool{}
				}
				p.noted[b] = true
				ui.Info(fmt.Sprintf("Not pushing pickup branch '%s' (another contributor's; use --include-remote-worktrees or ezs push -b %s)", b, b))
			}
			partial = true
			continue
		}
		r := ResolveBranchRemote(p.g, p.mgr, b)
		if r == config.RemoteNoPush {
			ui.Warn(fmt.Sprintf("Skipping push for %s (fork does not allow maintainer push)", b))
			partial = true
			continue
		}
		// Every candidate's remote counts, pushed or not: gh-stack pushes
		// the whole chain to a single remote.
		remotes[r] = true
		if !branchDiffersFromRemote(p.g, b, r) {
			continue
		}
		if p.onlyPublished && !p.g.RefExists("refs/remotes/"+r+"/"+b) {
			partial = true
			continue
		}
		targets = append(targets, target{b, r})
	}
	if len(targets) == 0 {
		return nil
	}
	if !p.decided {
		p.decided = true
		p.allowed = p.policy == pushAlways ||
			ui.ConfirmTUI("Push rebased branches to their remotes (--force-with-lease)")
		if !p.allowed {
			ui.Info("Not pushing. Push later with: ezs push -s")
		}
	}
	if !p.allowed {
		return nil
	}
	defer func() {
		for _, t := range targets {
			ensureUpstream(p.g, t.branch, t.remote)
		}
	}()

	// One remote, nothing excluded, a first chain (gh-stack pushes every
	// active layer, shared ones included), and a checkout of a branch only
	// this chain has — gh-stack resolves the stack from the checkout.
	if len(remotes) == 1 && !partial && c.Shared == 0 {
		var unique []string
		for _, b := range c.Own() {
			if ghstack.Unique(p.chains, b) {
				unique = append(unique, b)
			}
		}
		if dir := checkedOutDir(p.g, unique); dir != "" {
			if err := ghstack.New(p.mgr.GetRepoDir()).Push(dir, targets[0].remote); err != nil {
				return fmt.Errorf("push %s: %w", strings.Join(c.Own(), ", "), err)
			}
			return nil
		}
	}
	// Otherwise push the branches one by one.
	var failed []string
	for _, t := range targets {
		ui.Info(fmt.Sprintf("Pushing %s to %s...", t.branch, t.remote))
		if err := p.g.PushBranch(t.branch, true, t.remote); err != nil {
			ui.Warn(fmt.Sprintf("Push failed for %s: %v", t.branch, err))
			failed = append(failed, t.branch)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("push failed for: %s", strings.Join(failed, ", "))
	}
	return nil
}

// ensureUpstream sets branch's upstream to remote/branch when it has none,
// so plain `git push`/`git status` work after a first push (gh-stack's push
// doesn't set one).
func ensureUpstream(g *git.Git, branch, remote string) {
	if _, err := g.RunCapture("rev-parse", "--abbrev-ref", branch+"@{upstream}"); err == nil {
		return
	}
	if g.RefExists("refs/remotes/" + remote + "/" + branch) {
		_, _ = g.RunCapture("branch", "--set-upstream-to="+remote+"/"+branch, branch)
	}
}

// integrateRemoteCommits brings commits others pushed to a branch's remote
// into the local branch before it is rebased (a strict fast-forward), and
// holds back from pushing any branch whose remote has commits the local
// branch lacks but can't fast-forward to — force-pushing it would erase them.
func integrateRemoteCommits(g *git.Git, mgr *stack.Manager, chains []ghstack.Chain, p *pusher) {
	type item struct{ branch, remote string }
	var items []item
	fetched := map[string]bool{}
	for _, c := range chains {
		for _, b := range c.Own() {
			br := mgr.GetBranch(b)
			if br == nil || !br.CanPush() {
				continue
			}
			r := br.EffectiveRemote()
			if !fetched[r] {
				fetched[r] = true
				if _, err := g.RunCapture("fetch", "--quiet", r); err != nil {
					ui.Warn(fmt.Sprintf("Could not fetch %s: %v — not pushing its branches this run (a stale lease could overwrite others' commits)", r, err))
				}
			}
			items = append(items, item{b, r})
		}
	}
	wts, _ := g.ListWorktrees()
	dirOf := map[string]string{}
	for _, w := range wts {
		if w.Branch != "" {
			dirOf[w.Branch] = w.Path
		}
	}
	hold := func(b, why string) {
		if p.hold == nil {
			p.hold = map[string]bool{}
		}
		p.hold[b] = true
		ui.Info(fmt.Sprintf("Not pushing %s: %s", b, why))
	}
	for _, it := range items {
		ref := "refs/remotes/" + it.remote + "/" + it.branch
		if !g.RefExists(ref) {
			continue
		}
		ahead, behind, err := g.GetAheadBehind("refs/heads/"+it.branch, ref)
		if err != nil || behind == 0 {
			continue
		}
		if ahead > 0 {
			hold(it.branch, fmt.Sprintf("it diverged from %s/%s (%d ahead, %d behind). Run `git pull --rebase` in its worktree to integrate.", it.remote, it.branch, ahead, behind))
			continue
		}
		if dir := dirOf[it.branch]; dir != "" {
			wg := git.New(dir)
			if dirty, _ := wg.HasChanges(); dirty {
				hold(it.branch, fmt.Sprintf("%s/%s has new commits but the worktree has uncommitted changes", it.remote, it.branch))
				continue
			}
			if _, err := wg.RunCapture("merge", "--ff-only", "--quiet", ref); err != nil {
				hold(it.branch, fmt.Sprintf("could not fast-forward to %s/%s: %v", it.remote, it.branch, err))
				continue
			}
		} else {
			local, _ := g.RunCapture("rev-parse", "refs/heads/"+it.branch)
			remote, _ := g.RunCapture("rev-parse", ref)
			if _, err := g.RunCapture("update-ref", "refs/heads/"+it.branch, remote, local); err != nil {
				hold(it.branch, fmt.Sprintf("could not fast-forward to %s/%s: %v", it.remote, it.branch, err))
				continue
			}
		}
		ui.Success(fmt.Sprintf("Fast-forwarded %s to %s/%s (%d new commit(s))", it.branch, it.remote, it.branch, behind))
	}
}

// branchDiffersFromRemote reports whether branch needs pushing to remote
// (never pushed, or local and remote tips differ).
func branchDiffersFromRemote(g *git.Git, branch, remote string) bool {
	local, err := g.RunCapture("rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil || local == "" {
		return false
	}
	remoteSHA, err := g.RunCapture("rev-parse", "--verify", "--quiet", "refs/remotes/"+remote+"/"+branch)
	return err != nil || remoteSHA != local
}

// checkedOutDir returns a worktree that has one of branches checked out
// (preferring the last, i.e. the top of the chain), or "".
func checkedOutDir(g *git.Git, branches []string) string {
	wts, err := g.ListWorktrees()
	if err != nil {
		return ""
	}
	byBranch := map[string]string{}
	for _, w := range wts {
		if w.Branch != "" {
			byBranch[w.Branch] = w.Path
		}
	}
	for i := len(branches) - 1; i >= 0; i-- {
		if dir, ok := byBranch[branches[i]]; ok {
			return dir
		}
	}
	return ""
}

// runRestack executes steps in order (parent chains first), pushing each
// chain after it is rebased so the chains nested under it see the new tip.
func runRestack(g *git.Git, mgr *stack.Manager, steps []restackStep, p *pusher) error {
	client := ghstack.New(mgr.GetRepoDir())
	for _, s := range steps {
		// Pickup branches belong to another contributor and aren't rebased
		// unless asked. At the bottom of a chain, --no-trunk leaves it alone
		// and restacks what's above it; higher up, the chain is restacked
		// only below it.
		if !p.includeRemote {
			active := s.chain.Active()
			for i, name := range active {
				b := mgr.GetBranch(name)
				if b == nil || !b.IsRemote {
					continue
				}
				if i == 0 {
					if !s.opts.NoTrunk {
						ui.Info(fmt.Sprintf("Leaving pickup branch '%s' as-is (use --include-remote-worktrees to rebase it)", name))
						s.opts.NoTrunk = true
					}
					continue
				}
				below := active[i-1]
				if !ghstack.Unique(p.chains, below) {
					ui.Warn(fmt.Sprintf("Skipping %s: it contains pickup branch '%s' (use --include-remote-worktrees to rebase it)", describeChain(s.chain), name))
					s.opts.Branch = ""
				} else {
					ui.Info(fmt.Sprintf("Restacking only below pickup branch '%s' (use --include-remote-worktrees to rebase it)", name))
					s.opts.Branch, s.opts.Downstack, s.opts.Upstack = below, true, false
				}
				break
			}
			if s.opts.Branch == "" {
				continue
			}
		}
		if !s.opts.NoTrunk && trunkDiverged(g, mgr, s.chain, s.opts.Remote) {
			ui.Warn(fmt.Sprintf("Skipping %s: %s has diverged from %s/%s. Push %s, then run `ezs sync -s`.",
				describeChain(s.chain), s.chain.Trunk, s.opts.Remote, s.chain.Trunk, s.chain.Trunk))
			continue
		}
		ui.Info(fmt.Sprintf("Restacking %s", describeChain(s.chain)))
		if err := client.Rebase(s.opts); err != nil {
			return restackError(err)
		}
		if err := p.pushChain(s.chain); err != nil {
			ui.Warn(err.Error())
		}
	}
	return nil
}

// trunkDiverged reports whether a chain's trunk is an ezstack branch (the
// stack is rooted on another stack's branch) that has diverged from its
// remote-tracking ref. gh-stack then rebases the chain onto the REMOTE trunk,
// i.e. onto the branch as it was before the local rewrite — never what the
// user wants — so the step is skipped instead.
func trunkDiverged(g *git.Git, mgr *stack.Manager, c ghstack.Chain, remote string) bool {
	if remote == "" || mgr.GetBranch(c.Trunk) == nil {
		return false
	}
	remoteRef := "refs/remotes/" + remote + "/" + c.Trunk
	if !g.RefExists(remoteRef) {
		return false
	}
	localAhead, _ := g.IsAncestor(remoteRef, "refs/heads/"+c.Trunk)
	localBehind, _ := g.IsAncestor("refs/heads/"+c.Trunk, remoteRef)
	return !localAhead && !localBehind
}

// restackError maps gh-stack failures onto ezstack's guidance and exit codes.
func restackError(err error) error {
	switch {
	case errors.Is(err, ghstack.ErrConflict):
		return errSyncConflict
	case errors.Is(err, ghstack.ErrRebaseActive):
		return ui.NewExitError(ui.ExitConflict, "a stack rebase is already paused on a conflict: run `ezs sync --continue` or `ezs sync --abort`")
	}
	return err
}

func describeChain(c ghstack.Chain) string {
	parts := []string{"(" + c.Trunk + ")"}
	for _, b := range c.Branches {
		if c.Merged[b] {
			parts = append(parts, b+" [merged]")
		} else {
			parts = append(parts, b)
		}
	}
	return strings.Join(parts, " ← ")
}
