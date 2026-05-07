package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/github"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

// prPromote is the entrypoint for `ezs pr promote …`. Promotes
// fork-mode-stack child branches whose parent merged in upstream from
// fork-side PRs to cross-repo PRs in upstream. GitHub's API can't change
// a PR's base repo or head ref, so the only path is close-and-reopen.
//
// Lost on promotion (no API to restore): PR #/URL, inline review comments,
// approvals, CI history, files-viewed checkboxes, merge-queue position.
// Preserved (copied via gh pr edit): title, body, labels, assignees,
// reviewers (re-requested but state reset), milestone.
func prPromote(args []string) error {
	fs := pflag.NewFlagSet("pr promote", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sPromote fork-side PRs to cross-repo upstream PRs%s

%sUSAGE%s
    ezs pr promote [options]

%sDESCRIPTION%s
    When the bottom PR of a public-fork stack merges into upstream, the
    next branch up has a fork-side PR (head=fork-owner:b, base=fork-owner:bottom)
    whose chain to upstream is now broken. Promotion closes the fork-side
    PR and creates a fresh cross-repo PR in upstream (head=fork-owner:b,
    base=upstream:main).

    GitHub's API does not allow changing a PR's base repository, so this
    requires close-and-reopen. The new PR loses the old PR's number, URL,
    inline review comments, approvals, and CI history. Title, body, labels,
    assignees, reviewers, and milestone are copied; the new body is suffixed
    with "Replaces #N" and the old PR is commented "Replaced by …".

%sOPTIONS%s
    --branch <name>    Promote a specific branch (defaults to all promotable
                       branches in the current stack)
    --all              Scan every stack for promotable branches
    --yes              Skip per-branch confirmation prompts
    -h, --help         Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	branchFlag := fs.String("branch", "", "Promote a specific branch")
	allFlag := fs.Bool("all", false, "Scan all stacks")
	yesFlag := fs.Bool("yes", false, "Skip per-branch confirmation")
	helpFlag := fs.BoolP("help", "h", false, "Show help")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return nil
		}
		return err
	}
	if *helpFlag {
		fs.Usage()
		return nil
	}
	if *branchFlag != "" && *allFlag {
		return fmt.Errorf("--branch and --all are mutually exclusive")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	g := git.New(cwd)
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	up, err := EnsureUpstreamDetected(g, mgr)
	if err != nil {
		return err
	}
	if up == nil || !up.Enabled {
		return fmt.Errorf("fork mode is not enabled for this repo. Run `ezs upstream init` first")
	}

	// Collect candidates.
	var candidates []*config.Branch
	switch {
	case *branchFlag != "":
		b := mgr.GetBranch(*branchFlag)
		if b == nil {
			return fmt.Errorf("branch '%s' is not tracked by ezstack", *branchFlag)
		}
		candidates = append(candidates, b)
	case *allFlag:
		for _, s := range mgr.GetStackConfig().Stacks {
			s.PopulateBranchesWithCache(mgr.GetStackConfig().Cache)
			candidates = append(candidates, detectPromoteCandidatesInStack(s)...)
		}
	default:
		s, _, err := mgr.GetCurrentStack()
		if err != nil {
			return err
		}
		candidates = detectPromoteCandidatesInStack(s)
	}

	if len(candidates) == 0 {
		ui.Info("No branches need promotion.")
		return nil
	}

	return runPromote(g, mgr, candidates, *yesFlag)
}

// runPromote drives close-and-reopen promotion for one or more branches.
// Per-branch failures are logged but do not abort the batch — partial
// progress is preferable to leaving the stack half-promoted.
func runPromote(g *git.Git, mgr *stack.Manager, branches []*config.Branch, yes bool) error {
	cfg := mgr.GetConfig()
	repoPath := mgr.GetRepoDir()
	upOwner, upRepo, _, upDefBr := cfg.GetUpstream(repoPath)
	if upOwner == "" {
		return fmt.Errorf("upstream not configured (run `ezs upstream init`)")
	}

	originClient, err := newGitHubClient(g)
	if err != nil {
		return err
	}
	originOwner, _ := originClient.OwnerRepo()
	upstreamClient := newGitHubClientForTarget(upOwner, upRepo)

	promoted := 0
	failed := 0
	for _, b := range branches {
		if !yes {
			ui.Warn(fmt.Sprintf("Promoting '%s' will close PR #%d in fork and create a new PR in %s/%s.", b.Name, b.PRNumber, upOwner, upRepo))
			ui.Warn("This loses inline review comments, approvals, and CI history.")
			if !ui.ConfirmTUI("Continue?") {
				ui.Info(fmt.Sprintf("Skipped %s", b.Name))
				continue
			}
		}
		if err := promoteBranch(g, mgr, b, originClient, upstreamClient, originOwner, upDefBr); err != nil {
			ui.Error(fmt.Sprintf("Promote %s failed: %v", b.Name, err))
			failed++
			continue
		}
		promoted++
	}

	switch {
	case promoted > 0 && failed > 0:
		ui.Warn(fmt.Sprintf("Promoted %d branch(es); %d failed", promoted, failed))
	case promoted > 0:
		ui.Success(fmt.Sprintf("Promoted %d branch(es)", promoted))
	case failed > 0:
		return fmt.Errorf("promote failed for %d branch(es)", failed)
	}
	return nil
}

// promoteBranch executes the close-and-reopen flow for a single branch.
// Steps (with idempotent pre-check first):
//  0. If a cross-repo PR already exists for this head ref in upstream,
//     adopt it and skip create — handles ctrl-C-after-create and racing
//     concurrent promotes.
//  1. Fetch existing fork-side PR's preservable metadata.
//  2. Force-push branch to its fork remote (idempotent, --force-with-lease).
//  3. Create new cross-repo PR in upstream with body suffixed
//     "Replaces <fork>/<repo>#<old>".
//  4. Best-effort apply preserved metadata (labels, assignees, milestone,
//     reviewers) to the new PR.
//  5. Comment "Replaced by <upstream>/<repo>#<new>" on old PR.
//  6. Close old PR in fork.
//  7. Persist PR# / URL / PRTargetRepo=upstream / PreviousPRNumber.
//
// If steps 0-3 succeed but a later step fails, persist anyway and warn —
// leaving both PRs alive is recoverable; losing the new association is not.
func promoteBranch(g *git.Git, mgr *stack.Manager, b *config.Branch,
	originClient, upstreamClient *github.Client,
	forkOwner, upstreamDefaultBranch string) error {
	if !b.CanPush() {
		return fmt.Errorf("cannot promote %s: push not allowed (fork does not allow maintainer push)", b.Name)
	}

	// Step 0: idempotency pre-check. If we (or a previous interrupted run)
	// already opened a cross-repo PR for this fork:branch, reuse it.
	headSpec := forkOwner + ":" + b.Name
	if existing, err := upstreamClient.FindOpenPRByHead(headSpec); err == nil && existing != nil {
		ui.Warn(fmt.Sprintf("Found existing cross-repo PR #%d for %s — adopting", existing.Number, b.Name))
		oldNum := b.PRNumber
		updateBranchPromoted(mgr, b, existing, oldNum)
		// Best-effort: close the old fork-side PR if still open.
		_ = originClient.ClosePR(oldNum)
		return nil
	}

	// Step 1: fetch metadata from the existing fork-side PR.
	meta, err := originClient.FetchPRMetadata(b.PRNumber)
	if err != nil {
		return fmt.Errorf("fetch existing PR meta for #%d: %w", b.PRNumber, err)
	}

	// Step 2: push to fork (idempotent).
	if err := g.PushForceBranch(b.Name, b.EffectiveRemote()); err != nil {
		return fmt.Errorf("push %s to %s: %w", b.Name, b.EffectiveRemote(), err)
	}

	// Step 3: create new cross-repo PR.
	newBody := buildPromoteBody(meta.Body, originClient.Owner(), originClient.Repo(), b.PRNumber)
	newPR, err := upstreamClient.CreatePR(meta.Title, newBody, headSpec, upstreamDefaultBranch, meta.IsDraft)
	if err != nil {
		return fmt.Errorf("create cross-repo PR in %s/%s: %w", upstreamClient.Owner(), upstreamClient.Repo(), err)
	}

	// From here on, the new PR exists. Persist the association FIRST so a
	// later failure leaves us in a recoverable state (both PRs alive,
	// pointing at upstream's PR as authoritative).
	oldNum := b.PRNumber
	updateBranchPromoted(mgr, b, newPR, oldNum)

	// Step 4: best-effort apply preserved metadata.
	if err := upstreamClient.ApplyPRMetadata(newPR.Number, meta); err != nil {
		ui.Warn(fmt.Sprintf("Some metadata didn't transfer to new PR #%d: %v", newPR.Number, err))
	}

	// Step 5: comment on old PR.
	oldComment := fmt.Sprintf("Replaced by %s/%s#%d", upstreamClient.Owner(), upstreamClient.Repo(), newPR.Number)
	if err := originClient.CommentOnPR(oldNum, oldComment); err != nil {
		ui.Warn(fmt.Sprintf("Could not comment on old PR #%d: %v", oldNum, err))
	}

	// Step 6: close old PR.
	if err := originClient.ClosePR(oldNum); err != nil {
		ui.Warn(fmt.Sprintf("Could not close old PR #%d: %v — close it manually", oldNum, err))
	}

	ui.Success(fmt.Sprintf("Promoted %s: #%d (fork) → #%d (upstream): %s", b.Name, oldNum, newPR.Number, newPR.URL))

	// Step 7: refresh stack-nav descriptions.
	if currentStack := mgr.GetStackForBranch(b.Name); currentStack != nil {
		up := UpstreamFromConfig(mgr)
		if err := updateStackDescriptionsRouted(originClient, currentStack, b.Name, up); err != nil {
			ui.Warn(fmt.Sprintf("Failed to update stack descriptions: %v", err))
		}
	}
	return nil
}

// updateBranchPromoted updates a Branch and its persisted cache after a
// successful promotion (or after adopting an existing cross-repo PR).
// Surfaces persistence errors as warnings — the in-memory PR association
// is the authoritative copy until the next save.
func updateBranchPromoted(mgr *stack.Manager, b *config.Branch, newPR *github.PR, oldNum int) {
	b.PRNumber = newPR.Number
	b.PRUrl = newPR.URL
	b.PRState = prStateFromGitHub(newPR)
	b.IsMerged = newPR.Merged
	b.PRTargetRepo = config.PRTargetRepoUpstream
	b.PreviousPRNumber = oldNum

	mainWorktree := getMainWorktreePathFromMgr(mgr)
	if mainWorktree != "" {
		savePRToCache(mainWorktree, b.Name, newPR)
	}
	if err := mgr.SetBranchPRTarget(b.Name, config.PRTargetRepoUpstream, oldNum); err != nil {
		ui.Warn(fmt.Sprintf("Could not persist promotion for %s: %v", b.Name, err))
	}
}

// buildPromoteBody appends a "Replaces <fork>/<repo>#<oldNum>" line to
// the existing PR body iff one isn't already present, so reviewers can
// trace the chain across the close-and-reopen seam.
func buildPromoteBody(body, forkOwner, forkRepo string, oldPRNum int) string {
	marker := fmt.Sprintf("Replaces %s/%s#%d", forkOwner, forkRepo, oldPRNum)
	if strings.Contains(body, marker) {
		return body
	}
	if body == "" {
		return marker
	}
	return strings.TrimRight(body, "\n") + "\n\n" + marker
}

// getMainWorktreePathFromMgr is a convenience for promote.go: callers
// typically don't already have a *git.Git instance handy.
func getMainWorktreePathFromMgr(mgr *stack.Manager) string {
	if mgr == nil {
		return ""
	}
	return mgr.GetRepoDir()
}
