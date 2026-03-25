package commands

import (
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
	"github.com/spf13/pflag"
)

// Commit wraps git commit and auto-syncs child branches
func Commit(args []string) error {
	fs := pflag.NewFlagSet("commit", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sCommit changes and auto-sync child branches%s

%sUSAGE%s
    ezs commit [git-commit-options]

%sDESCRIPTION%s
    Wraps 'git commit' and then automatically rebases any child branches
    in the stack onto the updated branch. All arguments are passed through
    to git commit.

%sEXAMPLES%s
    ezs commit -m "Add feature"
    ezs commit -a -m "Fix bug"
    ezs commit --amend

%sOPTIONS%s
    -h, --help    Show this help message
    All other flags are passed directly to git commit.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	// Only parse --help ourselves; pass everything else to git
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fs.Usage()
		return nil
	}

	return commitInternal(args, false)
}

// Amend wraps git commit --amend and auto-syncs child branches
func Amend(args []string) error {
	fs := pflag.NewFlagSet("amend", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sAmend the last commit and auto-sync child branches%s

%sUSAGE%s
    ezs amend [git-commit-options]

%sDESCRIPTION%s
    Wraps 'git commit --amend' and then automatically rebases any child
    branches in the stack onto the updated branch.

%sEXAMPLES%s
    ezs amend                        # amend with editor
    ezs amend --no-edit              # amend without changing message
    ezs amend -m "New message"       # amend with new message

%sOPTIONS%s
    -h, --help    Show this help message
    All other flags are passed directly to git commit --amend.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}

	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fs.Usage()
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

	// Build git commit args
	gitArgs := []string{"commit"}
	if amend {
		gitArgs = append(gitArgs, "--amend")
	}
	gitArgs = append(gitArgs, args...)

	// Run git commit interactively so the user can use their editor
	if err := g.RunInteractive(gitArgs...); err != nil {
		return fmt.Errorf("git commit failed: %w", err)
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
		if ui.ConfirmTUIWithDefault("Push to remote?", true) {
			if err := g.Push(false); err != nil {
				ui.Warn(fmt.Sprintf("Push failed: %v", err))
				if ui.ConfirmTUI("Force push?") {
					if err := g.PushForce(); err != nil {
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

	ui.Info(fmt.Sprintf("Syncing %d child branch(es)...", len(children)))
	results, err := mgr.RebaseChildren()
	if err != nil {
		ui.Warn(fmt.Sprintf("Failed to sync children: %v", err))
		return nil
	}

	for _, result := range results {
		if result.HasConflict {
			ui.Warn(fmt.Sprintf("Conflict in '%s': resolve in %s", result.Branch, result.WorktreePath))
			ui.Info("To resolve: cd to the worktree, fix conflicts, run 'git add .' then 'git rebase --continue'")
			return nil
		} else if result.Error != nil {
			ui.Warn(fmt.Sprintf("Failed to sync '%s': %v", result.Branch, result.Error))
		} else if result.Success {
			ui.Success(fmt.Sprintf("Synced '%s'", result.Branch))
		}
	}

	return nil
}
