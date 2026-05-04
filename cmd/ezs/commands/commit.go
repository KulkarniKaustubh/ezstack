package commands

import (
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/hooks"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// Commit wraps git commit and auto-syncs child branches
func Commit(args []string) error {
	if HasExamplesFlag("commit", args) {
		return nil
	}
	// Only parse --help ourselves; pass everything else to git
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintf(os.Stderr, `%sCommit changes and auto-sync child branches%s

%sUSAGE%s
    ezs commit [git-commit-options] [--merge|--rebase] [--push [--push-children] | --no-push]

%sDESCRIPTION%s
    Wraps 'git commit' and then automatically syncs any child branches
    in the stack onto the updated branch. All arguments are passed through
    to git commit.

    Uses the configured sync_strategy (default: rebase). Override with
    --merge or --rebase.

    By default, prompts whether to push the current branch (and, if the
    branch has children in the stack, whether to push those too). Use
    --push / --push-children / --no-push to skip the prompt.

%sEXAMPLES%s
    ezs commit -m "Add feature"
    ezs commit -a -m "Fix bug"
    ezs commit --amend
    ezs commit -m "Fix" --merge
    ezs commit -m "Fix" --push                    # non-interactive: push current only
    ezs commit -m "Fix" --push --push-children    # non-interactive: push current + children
    ezs commit -m "Fix" --no-push                 # non-interactive: don't push

%sOPTIONS%s
    --merge           Use git merge to sync children (overrides config)
    --rebase          Use git rebase to sync children (overrides config)
    --push            Push current branch (no prompt)
    --push-children   Also push synced child branches (implies --push)
    --no-push         Skip pushing (no prompt)
    -h, --help        Show this help message
    All other flags are passed directly to git commit.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
		return nil
	}

	return commitInternal(args, false)
}

// Amend wraps git commit --amend and auto-syncs child branches
func Amend(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintf(os.Stderr, `%sAmend the last commit and auto-sync child branches%s

%sUSAGE%s
    ezs amend [git-commit-options] [--merge|--rebase] [--push [--push-children] | --no-push]

%sDESCRIPTION%s
    Wraps 'git commit --amend' and then automatically syncs any child
    branches in the stack onto the updated branch.

    Uses the configured sync_strategy (default: rebase). Override with
    --merge or --rebase.

    By default, prompts whether to force-push the current branch (and, if
    the branch has children in the stack, whether to push those too). Use
    --push / --push-children / --no-push to skip the prompt.

%sEXAMPLES%s
    ezs amend                                   # amend with editor
    ezs amend --no-edit                         # amend without changing message
    ezs amend -m "New message"                  # amend with new message
    ezs amend --no-edit --push                  # non-interactive: force-push current only
    ezs amend --no-edit --push --push-children  # non-interactive: force-push current + children

%sOPTIONS%s
    --merge           Use git merge to sync children (overrides config)
    --rebase          Use git rebase to sync children (overrides config)
    --push            Force-push current branch (no prompt)
    --push-children   Also push synced child branches (implies --push)
    --no-push         Skip pushing (no prompt)
    -h, --help        Show this help message
    All other flags are passed directly to git commit --amend.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
		return nil
	}

	return commitInternal(args, true)
}

// commitFlags holds the parsed ezs-specific flags for `ezs commit`/`ezs amend`.
type commitFlags struct {
	mergeOverride    bool
	rebaseOverride   bool
	pushFlag         bool
	pushChildrenFlag bool
	noPushFlag       bool
	gitPassthrough   []string
}

// parseCommitArgs splits ezs-specific flags out of the argument list and
// returns the remainder to pass through to `git commit`. It also validates
// mutually exclusive combinations. The boundary between ezs flags and git
// flags is subtle because some git options take a value (e.g. `-m foo`) that
// could look like one of our flag names.
func parseCommitArgs(args []string) (commitFlags, error) {
	var f commitFlags
	skipNext := false
	for _, arg := range args {
		if skipNext {
			f.gitPassthrough = append(f.gitPassthrough, arg)
			skipNext = false
			continue
		}
		switch arg {
		case "--merge":
			f.mergeOverride = true
		case "--rebase":
			f.rebaseOverride = true
		case "--push":
			f.pushFlag = true
		case "--push-children":
			f.pushChildrenFlag = true
		case "--no-push":
			f.noPushFlag = true
		default:
			f.gitPassthrough = append(f.gitPassthrough, arg)
			switch arg {
			case "-m", "--message", "-F", "--file", "-c", "--reedit-message",
				"-C", "--reuse-message", "--fixup", "--squash", "--author",
				"--date", "--cleanup", "-t", "--template", "--trailer":
				skipNext = true
			}
		}
	}
	if f.mergeOverride && f.rebaseOverride {
		return f, fmt.Errorf("cannot use both --merge and --rebase")
	}
	if f.noPushFlag && (f.pushFlag || f.pushChildrenFlag) {
		return f, fmt.Errorf("cannot combine --no-push with --push or --push-children")
	}
	// --push-children implies --push
	if f.pushChildrenFlag {
		f.pushFlag = true
	}
	return f, nil
}

// commitInternal handles the shared logic for commit and amend
func commitInternal(args []string, amend bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)

	flags, err := parseCommitArgs(args)
	if err != nil {
		return err
	}
	mergeOverride := flags.mergeOverride
	rebaseOverride := flags.rebaseOverride
	pushFlag := flags.pushFlag
	pushChildrenFlag := flags.pushChildrenFlag
	noPushFlag := flags.noPushFlag
	gitPassthroughArgs := flags.gitPassthrough

	// Build git commit args
	gitArgs := []string{"commit"}
	if amend {
		gitArgs = append(gitArgs, "--amend")
	}
	gitArgs = append(gitArgs, gitPassthroughArgs...)

	hookCtx := BuildHookContext()
	if err := hooks.Run("pre-commit", hookCtx); err != nil {
		return err
	}

	// Run git commit interactively so the user can use their editor
	if err := g.RunInteractive(gitArgs...); err != nil {
		return fmt.Errorf("git commit failed: %w", err)
	}

	if err := hooks.Run("post-commit", hookCtx); err != nil {
		ui.Warn(err.Error())
	}

	action := "Committed"
	if amend {
		action = "Amended"
	}

	msg, _ := g.GetLastCommitMessage()
	if msg != "" {
		ui.Success(fmt.Sprintf("%s: %s", action, msg))
	} else {
		ui.Success(action)
	}

	// Load the stack manager before prompting so the push prompt can offer to
	// push child branches in the same step.
	var mgr *stack.Manager
	var children []*config.Branch
	if m, err := stack.NewManager(cwd); err == nil {
		mgr = m
		if _, stackBranch, err := mgr.GetCurrentStack(); err == nil {
			children = mgr.GetChildren(stackBranch.Name)
		}
	}

	pushChildren := false
	currentBranch, _ := g.CurrentBranch()
	if currentBranch != "" && g.RemoteBranchExists(currentBranch) {
		// Look up the branch's configured remote
		remote := "origin"
		if mgr != nil {
			if b := mgr.GetBranch(currentBranch); b != nil {
				remote = b.EffectiveRemote()
			}
		}

		if remote == config.RemoteNoPush {
			// Fork branch where we can't push — skip push prompt
		} else {
			pushCurrent := false
			hasChildren := len(children) > 0
			basePrompt := "Push to remote?"
			if amend {
				basePrompt = "Force push to remote? (amend rewrites history)"
			}

			switch {
			case noPushFlag:
				// user asked us to skip pushing entirely
			case pushFlag:
				pushCurrent = true
				pushChildren = pushChildrenFlag && hasChildren
			case hasChildren:
				opts := []string{
					"Yes — current and child branches",
					"Yes — current branch only",
					"No",
				}
				idx := ui.SelectTUI(opts, basePrompt, 0)
				switch idx {
				case 0:
					pushCurrent = true
					pushChildren = true
				case 1:
					pushCurrent = true
				}
			default:
				if ui.ConfirmTUIWithDefault(basePrompt, true) {
					pushCurrent = true
				}
			}

			if pushCurrent {
				if amend {
					if err := g.PushForce(remote); err != nil {
						ui.Warn(fmt.Sprintf("Force push failed: %v", err))
					} else {
						ui.Success("Pushed to remote")
					}
				} else {
					if err := g.Push(false, remote); err != nil {
						ui.Warn(fmt.Sprintf("Push failed: %v", err))
						if ui.ConfirmTUI("Force push?") {
							if err := g.PushForce(remote); err != nil {
								ui.Warn(fmt.Sprintf("Force push failed: %v", err))
							} else {
								ui.Success("Pushed to remote")
							}
						}
					} else {
						ui.Success("Pushed to remote")
					}
				}
			}
		}
	}

	// Auto-sync children
	if mgr == nil {
		return nil
	}
	if len(children) == 0 {
		return nil
	}

	// Resolve merge vs rebase: flags override config
	useMerge := false
	if mergeOverride {
		useMerge = true
	} else if rebaseOverride {
		useMerge = false
	} else {
		useMerge = mgr.GetConfig().GetSyncStrategy(mgr.GetRepoDir()) == "merge"
	}

	ui.Info(fmt.Sprintf("Syncing %d child branch(es)...", len(children)))
	results, err := mgr.RebaseChildren(useMerge)
	if err != nil {
		ui.Warn(fmt.Sprintf("Failed to sync children: %v", err))
		return nil
	}

	continueCmd := "git rebase --continue"
	if useMerge {
		continueCmd = "git merge --continue"
	}

	var syncedChildren []stack.RebaseResult
	for _, result := range results {
		if result.HasConflict {
			ui.Warn(fmt.Sprintf("Conflict in '%s': resolve in %s", result.Branch, result.WorktreePath))
			ui.Info(fmt.Sprintf("To resolve: cd to %s, fix conflicts, run 'git add .' then '%s'", result.WorktreePath, continueCmd))
			return nil
		} else if result.Error != nil {
			ui.Warn(fmt.Sprintf("Failed to sync '%s': %v", result.Branch, result.Error))
		} else if result.Success {
			ui.Success(fmt.Sprintf("Synced '%s'", result.Branch))
			syncedChildren = append(syncedChildren, result)
		}
	}

	if pushChildren && len(syncedChildren) > 0 {
		// Rebase rewrites the child's history so we need --force-with-lease;
		// merge only adds commits and a fast-forward push is enough.
		pushChildBranches(g, syncedChildren, !useMerge)
	}

	return nil
}

// pushChildBranches pushes each synced child branch to its remote. Children
// that have never been pushed are skipped so `ezs commit` never silently
// publishes a branch the user hasn't shared before.
func pushChildBranches(g *git.Git, synced []stack.RebaseResult, force bool) {
	ui.Info(fmt.Sprintf("Pushing %d synced child branch(es)...", len(synced)))
	for _, r := range synced {
		remote := r.Remote
		if remote == "" {
			remote = "origin"
		}
		if remote == config.RemoteNoPush {
			continue
		}
		if !g.RemoteHasBranch(remote, r.Branch) {
			continue
		}
		if err := g.PushBranch(r.Branch, force, remote); err != nil {
			ui.Warn(fmt.Sprintf("Push of '%s' failed: %v", r.Branch, err))
		} else {
			ui.Success(fmt.Sprintf("Pushed '%s'", r.Branch))
		}
	}
}
