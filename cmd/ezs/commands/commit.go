package commands

import (
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/hooks"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
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
    ezs commit [git-commit-options] [--merge|--rebase]

%sDESCRIPTION%s
    Wraps 'git commit' and then automatically syncs any child branches
    in the stack onto the updated branch. All arguments are passed through
    to git commit.

    Uses the configured sync_strategy (default: rebase). Override with
    --merge or --rebase.

%sEXAMPLES%s
    ezs commit -m "Add feature"
    ezs commit -a -m "Fix bug"
    ezs commit --amend
    ezs commit -m "Fix" --merge

%sOPTIONS%s
    --merge       Use git merge to sync children (overrides config)
    --rebase      Use git rebase to sync children (overrides config)
    -h, --help    Show this help message
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
    ezs amend [git-commit-options] [--merge|--rebase]

%sDESCRIPTION%s
    Wraps 'git commit --amend' and then automatically syncs any child
    branches in the stack onto the updated branch.

    Uses the configured sync_strategy (default: rebase). Override with
    --merge or --rebase.

%sEXAMPLES%s
    ezs amend                        # amend with editor
    ezs amend --no-edit              # amend without changing message
    ezs amend -m "New message"       # amend with new message

%sOPTIONS%s
    --merge       Use git merge to sync children (overrides config)
    --rebase      Use git rebase to sync children (overrides config)
    -h, --help    Show this help message
    All other flags are passed directly to git commit --amend.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
		return nil
	}

	return commitInternal(args, true)
}

// commitInternal handles the shared logic for commit and amend
func commitInternal(args []string, amend bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)

	// Extract --merge/--rebase flags before passing args to git.
	// Skip args that are values of git flags that take a string argument
	// (e.g., -m "--merge" should pass "--merge" as the commit message, not
	// be interpreted as our flag).
	var mergeOverride, rebaseOverride bool
	var gitPassthroughArgs []string
	skipNext := false
	for _, arg := range args {
		if skipNext {
			gitPassthroughArgs = append(gitPassthroughArgs, arg)
			skipNext = false
			continue
		}
		switch arg {
		case "--merge":
			mergeOverride = true
		case "--rebase":
			rebaseOverride = true
		default:
			gitPassthroughArgs = append(gitPassthroughArgs, arg)
			// These git flags take the next arg as a value — don't interpret it as our flag
			switch arg {
			case "-m", "--message", "-F", "--file", "-c", "--reedit-message",
				"-C", "--reuse-message", "--fixup", "--squash", "--author",
				"--date", "--cleanup", "-t", "--template", "--trailer":
				skipNext = true
			}
		}
	}
	if mergeOverride && rebaseOverride {
		return fmt.Errorf("cannot use both --merge and --rebase")
	}

	// Build git commit args
	gitArgs := []string{"commit"}
	if amend {
		gitArgs = append(gitArgs, "--amend")
	}
	gitArgs = append(gitArgs, gitPassthroughArgs...)

	if err := hooks.Run("pre-commit", nil); err != nil {
		return err
	}

	// Run git commit interactively so the user can use their editor
	if err := g.RunInteractive(gitArgs...); err != nil {
		return fmt.Errorf("git commit failed: %w", err)
	}

	if err := hooks.Run("post-commit", nil); err != nil {
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

	currentBranch, _ := g.CurrentBranch()
	if currentBranch != "" && g.RemoteBranchExists(currentBranch) {
		// Look up the branch's configured remote
		remote := "origin"
		if mgr, err := stack.NewReadOnlyManager(cwd); err == nil {
			if b := mgr.GetBranch(currentBranch); b != nil {
				remote = b.EffectiveRemote()
			}
		}

		if remote == config.RemoteNoPush {
			// Fork branch where we can't push — skip push prompt
		} else if amend {
			// Amend rewrites history — regular push will always fail, so offer force push directly
			if ui.ConfirmTUIWithDefault("Force push to remote? (amend rewrites history)", true) {
				if err := g.PushForce(remote); err != nil {
					ui.Warn(fmt.Sprintf("Force push failed: %v", err))
				} else {
					ui.Success("Pushed to remote")
				}
			}
		} else {
			if ui.ConfirmTUIWithDefault("Push to remote?", true) {
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

	// Auto-sync children
	mgr, err := stack.NewManager(cwd)
	if err != nil {
		// Not in a stack or can't load — that's fine, just skip
		return nil
	}

	_, stackBranch, err := mgr.GetCurrentStack()
	if err != nil {
		// Current branch not in a stack — nothing to sync
		return nil
	}

	children := mgr.GetChildren(stackBranch.Name)
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

	for _, result := range results {
		if result.HasConflict {
			ui.Warn(fmt.Sprintf("Conflict in '%s': resolve in %s", result.Branch, result.WorktreePath))
			ui.Info(fmt.Sprintf("To resolve: cd to %s, fix conflicts, run 'git add .' then '%s'", result.WorktreePath, continueCmd))
			return nil
		} else if result.Error != nil {
			ui.Warn(fmt.Sprintf("Failed to sync '%s': %v", result.Branch, result.Error))
		} else if result.Success {
			ui.Success(fmt.Sprintf("Synced '%s'", result.Branch))
		}
	}

	return nil
}
