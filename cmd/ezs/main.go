package main

import (
	"fmt"
	"os"

	"github.com/ezstack/ezstack/cmd/ezs/commands"
	"github.com/ezstack/ezstack/internal/ui"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		// Interactive mode - show menu
		err := runInteractiveMenu()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "--shell-init":
		printShellInit()
		return
	case "new", "n":
		err = commands.New(args)
	case "list", "ls":
		err = commands.List(args)
	case "status", "st":
		err = commands.Status(args)
	case "rebase", "rb":
		err = commands.Rebase(args)
	case "sync":
		err = commands.Sync(args)
	case "pr":
		err = commands.PR(args)
	case "config", "cfg":
		err = commands.Config(args)
	case "goto", "go":
		err = commands.Goto(args)
	case "delete", "del", "rm":
		err = commands.Delete(args)
	case "-h", "--help":
		printUsage()
	case "-v", "--version":
		fmt.Printf("ezstack version %s\n", version)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
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
			ui.IconSync + "  rebase   - Rebase branches in the stack",
			ui.IconDown + "  sync     - Sync stack when parent branches are merged",
			ui.IconBranch + "  pr       - Manage pull requests",
			ui.IconArrow + "  goto     - Navigate to a branch worktree",
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
			cmdErr = commands.Rebase(nil)
		case 3:
			cmdErr = commands.Sync(nil)
		case 4:
			cmdErr = commands.PR(nil)
		case 5:
			cmdErr = commands.Goto(nil)
		case 6:
			cmdErr = commands.Delete(nil)
		case 7:
			cmdErr = commands.Config(nil)
		case 8:
			printUsage()
			return nil
		}

		// If the command returned ErrBack, loop back to main menu
		if cmdErr == ui.ErrBack {
			continue
		}
		if cmdErr != nil {
			return cmdErr
		}
		return nil
	}
}

const (
	bold   = "\033[1m"
	cyan   = "\033[36m"
	reset  = "\033[0m"
	yellow = "\033[33m"
)

func printUsage() {
	fmt.Printf(`%sezstack (ezs)%s - Manage stacked PRs with git worktrees

%sUSAGE%s
    ezs <command> [options]

%sCOMMANDS%s
    new, n        Create a new branch in the stack
    list, ls      List all stacks and branches
    status, st    Show status of current stack
    rebase, rb    Rebase branches in the stack
    sync          Sync stack when parent branches are merged
    goto, go      Navigate to a branch worktree
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

    %s# Create PRs for the stack%s
    ezs pr create -t "Part 1: Add feature"

    %s# Navigate between worktrees%s
    ezs goto feature-part2

Run 'ezs <command> --help' for more information on a command.
`, bold, reset, cyan, reset, cyan, reset, cyan, reset, cyan, reset, cyan, reset,
		yellow, reset, yellow, reset, yellow, reset, yellow, reset, yellow, reset)
}

func printShellInit() {
	fmt.Print(`# ezs shell function for cd support
# Add this to your shell config: eval "$(ezs --shell-init)"
ezs() {
    local ezs_bin
    ezs_bin="$(command -v ezs 2>/dev/null)"

    case "${1:-}" in
        --shell-init)
            "$ezs_bin" --shell-init
            ;;
        goto|go|new|n)
            # These commands may output "cd <path>" which we need to eval
            eval "$("$ezs_bin" "$@")"
            ;;
        *)
            "$ezs_bin" "$@"
            ;;
    esac
}
`)
}
