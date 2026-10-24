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
// catalog and returns the resulting chains (in tree order). forget names
// branches that were just removed from ezstack's model.
func reconcileGHStack(mgr *stack.Manager, forget ...string) ([]ghstack.Chain, error) {
	return reconcileGHStackMerged(mgr, nil, forget...)
}

// reconcileGHStackMerged is reconcileGHStack with extra branches treated as
// merged (PRs found merged on GitHub this run) without recording that in
// ezstack's model — the user hasn't confirmed their cleanup yet.
func reconcileGHStackMerged(mgr *stack.Manager, merged map[string]bool, forget ...string) ([]ghstack.Chain, error) {
	chains, err := ghstack.Reconcile(mgr.GetRepoDir(), withMergedHints(mgr.ListStacks(), merged), forget...)
	if errors.Is(err, ghstack.ErrRebasePaused) {
		return nil, ui.NewExitError(ui.ExitConflict, "%s", err.Error())
	}
	return chains, err
}

// withMergedHints returns stacks with the named branches marked merged, on
// copies, leaving the manager's model untouched.
func withMergedHints(stacks []*config.Stack, merged map[string]bool) []*config.Stack {
	if len(merged) == 0 {
		return stacks
	}
	out := make([]*config.Stack, len(stacks))
	for i, s := range stacks {
		hit := false
		for _, b := range s.Branches {
			if merged[b.Name] && !b.IsMerged {
				hit = true
				break
			}
		}
		if !hit {
			out[i] = s
			continue
		}
		cp := *s
		cp.Branches = make([]*config.Branch, len(s.Branches))
		for j, b := range s.Branches {
			bc := *b
			if merged[b.Name] {
				bc.IsMerged = true
			}
			cp.Branches[j] = &bc
		}
		out[i] = &cp
	}
	return out
}

// refreshGHStack is the best-effort reconcile run after structural changes
// (new, reparent, merged cleanup, and — via stack.BranchesRemoved — any
// branch removal, passed as forget) so the catalog, which `gh stack`
// commands run by hand also read, never lags ezstack's model. It never fails
// the calling command: the next sync reconciles again and reports real
// problems.
func refreshGHStack(mgr *stack.Manager, forget ...string) {
	if ghstack.Check() != nil {
		return
	}
	if _, err := ghstack.Reconcile(mgr.GetRepoDir(), mgr.ListStacks(), forget...); err != nil &&
		!errors.Is(err, ghstack.ErrRebasePaused) {
		ui.Warn(fmt.Sprintf("Could not update gh stack tracking: %v", err))
	}
}

func init() {
	// Whatever removes branches from ezstack's model, gh-stack's catalog
	// forgets them too.
	stack.BranchesRemoved = func(m *stack.Manager, names []string) { refreshGHStack(m, names...) }
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
		// anchor == "": every active layer is shared, so the chains through
		// it restack them; the step only pushes (runRestack skips the rebase).
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
	pushAsk              // confirm once (auto-yes under -y)
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
	// pushed counts branches pushed this run.
	pushed int
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
				ui.Info(fmt.Sprintf("Not pushing pickup branch '%s' (another contributor's; push it with: ezs push -b %s)", b, b))
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
			err := ghstack.New(p.mgr.GetRepoDir()).Push(dir, targets[0].remote)
			if err == nil {
				p.pushed += len(targets)
				return nil
			}
			if !errors.Is(err, ghstack.ErrNotInStack) {
				return fmt.Errorf("push %s: %w", strings.Join(c.Own(), ", "), err)
			}
			// gh-stack doesn't track the chain (yet): push branch by branch.
		}
	}
	// Otherwise push the branches one by one.
	var failed []string
	for _, t := range targets {
		ui.Info(fmt.Sprintf("Pushing %s to %s...", t.branch, t.remote))
		if err := p.g.PushBranch(t.branch, true, t.remote); err != nil {
			ui.Warn(fmt.Sprintf("Push failed for %s: %v", t.branch, err))
			failed = append(failed, t.branch)
			continue
		}
		p.pushed++
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

// integrateRemoteCommits protects commits other people pushed to a branch's
// remote before the branch is rebased and force-pushed:
//
//   - a branch strictly behind its remote is fast-forwarded to it;
//   - a branch whose remote tip it never had (the tip isn't reachable from
//     any entry of the local branch's reflog — the check behind
//     `git push --force-if-includes`) is held back from pushing, since a
//     lease against the fetched ref would erase those commits. A branch that
//     merely differs because it was rebased locally still has the old remote
//     tip in its reflog and is pushed normally;
//   - if a remote can't be fetched, its branches are held too (the lease
//     would be checked against a stale ref).
func integrateRemoteCommits(g *git.Git, mgr *stack.Manager, chains []ghstack.Chain, p *pusher) {
	type item struct{ branch, remote string }
	var items []item
	seen := map[string]bool{}
	fetchFailed := map[string]bool{}
	fetched := map[string]bool{}
	for _, c := range chains {
		for _, b := range c.Own() {
			br := mgr.GetBranch(b)
			if br == nil || !br.CanPush() || seen[b] {
				continue
			}
			seen[b] = true
			r := br.EffectiveRemote()
			if !fetched[r] {
				fetched[r] = true
				if _, err := g.RunCapture("fetch", "--quiet", r); err != nil {
					ui.Warn(fmt.Sprintf("Could not fetch %s: %v — not pushing its branches this run (a stale lease could overwrite others' commits)", r, err))
					fetchFailed[r] = true
				}
			}
			items = append(items, item{b, r})
		}
	}
	dirOf := worktreeDirs(g)
	hold := func(b, why string) {
		if p.hold == nil {
			p.hold = map[string]bool{}
		}
		p.hold[b] = true
		if why != "" {
			ui.Info(fmt.Sprintf("Not pushing %s: %s", b, why))
		}
	}
	for _, it := range items {
		if fetchFailed[it.remote] {
			hold(it.branch, "")
			continue
		}
		ref := "refs/remotes/" + it.remote + "/" + it.branch
		if !g.RefExists(ref) {
			continue
		}
		ahead, behind, err := g.GetAheadBehind("refs/heads/"+it.branch, ref)
		if err != nil || behind == 0 {
			continue
		}
		if ahead > 0 {
			if !remoteTipIntegrated(g, it.branch, ref) {
				hold(it.branch, fmt.Sprintf("%s/%s has commits this branch never had (someone else pushed). Integrate them first: `git pull --rebase` in its worktree.", it.remote, it.branch))
			}
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
			local, _ := g.GetBranchCommit(it.branch)
			remote, _ := g.RunCapture("rev-parse", ref)
			if _, err := g.RunCapture("update-ref", "refs/heads/"+it.branch, remote, local); err != nil {
				hold(it.branch, fmt.Sprintf("could not fast-forward to %s/%s: %v", it.remote, it.branch, err))
				continue
			}
		}
		ui.Success(fmt.Sprintf("Fast-forwarded %s to %s/%s (%d new commit(s))", it.branch, it.remote, it.branch, behind))
	}
}

// remoteTipIntegrated reports whether remoteRef's tip is reachable from some
// entry of branch's reflog, i.e. the local branch once contained it (the
// condition `git push --force-if-includes` enforces).
func remoteTipIntegrated(g *git.Git, branch, remoteRef string) bool {
	tip, err := g.RunCapture("rev-parse", remoteRef)
	if err != nil || tip == "" {
		return false
	}
	out, err := g.RunCapture("reflog", "show", "--format=%H", "-n", "200", "refs/heads/"+branch)
	if err != nil {
		return false
	}
	for _, sha := range strings.Fields(out) {
		if sha == tip {
			return true
		}
		if ok, _ := g.IsAncestor(tip, sha); ok {
			return true
		}
	}
	return false
}

// worktreeDirs maps each checked-out branch to its worktree.
func worktreeDirs(g *git.Git) map[string]string {
	dirs := map[string]string{}
	if wts, err := g.ListWorktrees(); err == nil {
		for _, w := range wts {
			if w.Branch != "" {
				dirs[w.Branch] = w.Path
			}
		}
	}
	return dirs
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
	dirs := worktreeDirs(g)
	for i := len(branches) - 1; i >= 0; i-- {
		if dir, ok := dirs[branches[i]]; ok {
			return dir
		}
	}
	return ""
}

// runRestack rebases every step in order, then pushes each step's chain.
// A step without a branch only pushes (its layers are restacked through
// other chains).
func runRestack(g *git.Git, mgr *stack.Manager, steps []restackStep, p *pusher) error {
	client := ghstack.New(mgr.GetRepoDir())
	for _, s := range steps {
		if s.opts.Branch == "" {
			continue // push-only
		}
		if !p.includeRemote {
			var ok bool
			if s.opts, ok = avoidPickups(mgr, p.chains, s); !ok {
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
	}
	for _, s := range steps {
		if err := p.pushChain(s.chain); err != nil {
			ui.Warn(err.Error())
		}
	}
	return nil
}

// avoidPickups narrows a step so it doesn't rebase a pickup branch (another
// contributor's PR branch, IsRemote) — only --include-remote-worktrees may.
// It works on the range of active layers the step would rewrite: a pickup at
// the bottom is kept by --no-trunk; one at the start of an --upstack range
// is skipped by starting above it; one inside the range ends the range just
// below it. ok is false when nothing of the step survives.
func avoidPickups(mgr *stack.Manager, chains []ghstack.Chain, s restackStep) (ghstack.RebaseOptions, bool) {
	o := s.opts
	active := s.chain.Active()
	anchor := indexOf(active, o.Branch)
	if anchor < 0 {
		return o, true
	}
	start, end := 0, anchor // whole chain / --downstack
	if o.Upstack {
		start, end = anchor, len(active)-1
	}
	if o.NoTrunk && start == 0 {
		start = 1
	}
	isPickup := func(i int) bool { b := mgr.GetBranch(active[i]); return b != nil && b.IsRemote }
	if isPickup(0) && start == 0 {
		ui.Info(fmt.Sprintf("Leaving pickup branch '%s' as-is (use --include-remote-worktrees to rebase it)", active[0]))
		o.NoTrunk, start = true, 1
	}
	for i := start; i <= end; i++ {
		if !isPickup(i) {
			continue
		}
		note := fmt.Sprintf("pickup branch '%s' (use --include-remote-worktrees to rebase it)", active[i])
		if i == start && o.Upstack {
			// Restack what's above it onto it instead.
			if i+1 > end || !ghstack.Unique(chains, active[i+1]) {
				ui.Info("Leaving " + note)
				return o, false
			}
			ui.Info("Restacking above " + note)
			o.Branch, o.NoTrunk = active[i+1], true
			start = i + 1
			continue
		}
		if i-1 < start || !ghstack.Unique(chains, active[i-1]) {
			ui.Warn(fmt.Sprintf("Skipping %s: it contains %s", describeChain(s.chain), note))
			return o, false
		}
		ui.Info("Restacking only below " + note)
		o.Branch, o.Upstack, o.Downstack = active[i-1], false, true
		break
	}
	return o, true
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
