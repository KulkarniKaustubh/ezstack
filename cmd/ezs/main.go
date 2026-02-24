package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/KulkarniKaustubh/ezstack/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
)

const version = "0.1.0"

// checkRepoRoot checks if we're in a git repo root and returns the repo path.
// Returns ("", false) if not in a git repo.
func checkRepoRoot() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	g := git.New(cwd)

	// Try to get main worktree first (handles worktree case)
	mainWorktree, err := g.GetMainWorktree()
	if err != nil {
		// Try to get repo root instead
		repoRoot, err := g.GetRepoRoot()
		if err != nil {
			return "", false
		}
		// Resolve symlinks for consistency
		resolved, err := filepath.EvalSymlinks(repoRoot)
		if err == nil {
			repoRoot = resolved
		}
		return repoRoot, true
	}
	return mainWorktree, true
}

// hasRepoConfig checks if the current repo has been configured in ezstack
func hasRepoConfig(repoPath string) bool {
	cfg, err := config.Load()
	if err != nil {
		return false
	}
	repoCfg := cfg.GetRepoConfig(repoPath)
	return repoCfg != nil && repoCfg.WorktreeBaseDir != ""
}

func main() {
	cmd := ""
	args := []string{}
	if len(os.Args) >= 2 {
		cmd = os.Args[1]
		args = os.Args[2:]
	}

	// Commands that don't require repo check
	switch cmd {
	case "--shell-init":
		printShellInit()
		return
	case "-h", "--help":
		printUsage()
		return
	case "-v", "--version":
		fmt.Printf("ezstack version %s\n", version)
		return
	}

	// Check if we're in a git repo for all other commands
	repoPath, inRepo := checkRepoRoot()
	if !inRepo {
		ui.Error("ezs must be run from a git repository root (or a worktree)")
		os.Exit(1)
	}

	// If no command given (interactive mode) and no config, guide through setup
	if cmd == "" {
		if !hasRepoConfig(repoPath) {
			ui.Info("Welcome to ezstack! Let's set up this repository.")
			fmt.Println()
			if err := commands.Config(nil); err != nil {
				if err == ui.ErrBack {
					return
				}
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}
		// Config exists, show interactive menu
		if err := runInteractiveMenu(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	var err error
	switch cmd {
	case "new", "n":
		err = commands.New(args)
	case "list", "ls":
		err = commands.List(args)
	case "status", "st":
		err = commands.Status(args)
	case "sync", "rebase", "rb":
		err = commands.Sync(args)
	case "pr":
		err = commands.PR(args)
	case "config", "cfg":
		err = commands.Config(args)
	case "goto", "go":
		err = commands.Goto(args)
	case "delete", "del", "rm":
		err = commands.Delete(args)
	case "reparent", "rp":
		err = commands.Reparent(args)
	case "update", "up":
		err = commands.Update(args)
	case "stack":
		err = commands.Stack(args)
	case "unstack":
		err = commands.Unstack(args)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		if err == ui.ErrBack {
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// runInteractiveMenu shows the main interactive menu
func runInteractiveMenu() error {
	for {
		options := []string{
			ui.IconNew + "  new      - Create a new branch in the stack",
			ui.IconInfo + "  status   - Show status of current stack",
			ui.IconSync + "  sync     - Sync stack with remote (rebase onto main)",
			ui.IconBranch + "  pr       - Manage pull requests",
			ui.IconArrow + "  goto     - Navigate to a branch worktree",
			ui.IconUp + "  reparent - Change the parent of a branch",
			ui.IconNew + "  stack    - Add a branch to a stack",
			ui.IconCancel + "  unstack  - Remove a branch from tracking",
			ui.IconSync + "  update   - Sync config with git (detect changes)",
			ui.IconCancel + "  delete   - Delete a branch and its worktree",
			ui.IconBullet + "  config   - Configure ezstack",
			ui.IconInfo + "  help     - Show help",
		}

		selected, err := ui.SelectOption(options, "Select a command:")
		if err != nil {
			return err
		}

		var cmdErr error
		switch selected {
		case 0:
			cmdErr = commands.New(nil)
		case 1:
			cmdErr = commands.Status(nil)
		case 2:
			cmdErr = commands.Sync(nil)
		case 3:
			cmdErr = commands.PR(nil)
		case 4:
			cmdErr = commands.Goto(nil)
		case 5:
			cmdErr = commands.Reparent(nil)
		case 6:
			cmdErr = commands.Stack(nil)
		case 7:
			cmdErr = commands.Unstack(nil)
		case 8:
			cmdErr = commands.Update(nil)
		case 9:
			cmdErr = commands.Delete(nil)
		case 10:
			cmdErr = commands.Config(nil)
		case 11:
			printUsage()
			return nil
		}

		if cmdErr == ui.ErrBack {
			continue
		}
		if cmdErr != nil {
			return cmdErr
		}
		return nil
	}
}

func printUsage() {
	fmt.Printf(`%sezstack (ezs)%s - Manage stacked PRs with git worktrees

%sUSAGE%s
    ezs <command> [options]

%sCOMMANDS%s
    new, n        Create a new branch in the stack
    list, ls      List all stacks and branches
    status, st    Show status of current stack
    sync          Sync stack with remote (accepts stack hash prefix, min 3 chars)
    goto, go      Navigate to a branch worktree
    reparent, rp  Change the parent of a branch
    stack         Add a branch to a stack
    unstack       Remove a branch from tracking (keeps git branch)
    update, up    Sync config with git (detect manual changes)
    delete, rm    Delete a branch and its worktree
    pr            Manage pull requests
    config        Configure ezstack

%sOPTIONS%s
    -h, --help       Show this help message
    -v, --version    Show version
    --shell-init     Output shell function for cd support

%sSETUP%s
    Add to ~/.bashrc or ~/.zshrc:
        eval "$(ezs --shell-init)"

%sEXAMPLES%s
    %s# Create a new stack starting from main%s
    ezs new feature-part1

    %s# Add to the stack%s
    ezs new feature-part2 --parent feature-part1

    %s# View the stack%s
    ezs status

    %s# Sync a specific stack by hash prefix (min 3 chars)%s
    ezs sync a1b2c

    %s# Create PRs for the stack%s
    ezs pr create -t "Part 1: Add feature"

    %s# Navigate between worktrees%s
    ezs goto feature-part2

Run 'ezs <command> --help' for more information on a command.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset,
		ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset)
}

func printShellInit() {
	fmt.Print(`# ezs shell function for cd support
# Add this to your shell config: eval "$(ezs --shell-init)"
ezs() {
    case "${1:-}" in
        goto|go|new|n|delete|del|rm|sync)
            # These commands may output "cd <path>" which we need to eval
            eval "$(command ezs "$@")"
            ;;
        *)
            command ezs "$@"
            ;;
    esac
}
`)
}
