package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/term"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/version"
)

// init wires the optional progress hook in internal/git so long-running ops
// (rebase, merge, fetch, worktree add) show a delayed spinner. The hook is
// defined in git as a nil-by-default function variable so the package stays a
// leaf — config, stack, the docs/MCP binaries, and tests all consume git
// without inheriting ui's surface. The interactive `ezs` binary opts in here.
func init() {
	git.ProgressStart = func(message string) func() {
		s := ui.NewDelayedSpinner(message)
		s.Start()
		return s.Stop
	}
}

// knownCommands is the set of dispatchable ezs subcommands (including aliases).
// Used to decide whether to trigger auto-setup before dispatch — we don't want
// to run first-time setup just because the user typed a typo.
var knownCommands = map[string]bool{
	"new": true, "n": true,
	"list": true, "ls": true,
	"status": true, "st": true,
	"sync":   true,
	"pr":     true,
	"config": true, "cfg": true,
	"goto": true, "go": true,
	"delete": true, "del": true, "rm": true,
	"reparent": true, "rp": true,
	"stack":   true,
	"unstack": true,
	"commit":  true, "ci": true,
	"amend":   true,
	"diff":    true,
	"log":     true,
	"push":    true,
	"up":      true,
	"down":    true,
	"agent":   true,
	"menu":    true,
	"doctor":  true,
	"upgrade": true, "update": true,
}

// commandNeedsRepoCheck returns true if the command needs the caller to be in
// a git repository. doctor and upgrade are intentionally excluded so users
// can run them on a fresh machine before cloning anything.
func commandNeedsRepoCheck(cmd string) bool {
	switch cmd {
	case "doctor", "upgrade", "update":
		return false
	}
	return true
}

// knownCommandNames returns the keys of knownCommands as a slice — needed by
// ui.SuggestCommand for did-you-mean hints on unknown input. Aliases are
// included so "ls" / "ci" etc. are valid typo targets.
func knownCommandNames() []string {
	names := make([]string, 0, len(knownCommands))
	for k := range knownCommands {
		names = append(names, k)
	}
	return names
}

// commandNeedsRepoConfig returns true if the command requires the current repo
// to be configured in ezstack before it can run. `config`/`cfg` are exempt
// because they *are* the setup path.
func commandNeedsRepoConfig(cmd string) bool {
	if !knownCommands[cmd] {
		return false
	}
	switch cmd {
	case "config", "cfg", "doctor", "upgrade", "update":
		return false
	}
	return true
}

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
	// Resolve a --repo / EZSTACK_REPO override before any cwd-based repo
	// discovery. resolveRepoOverride strips every --repo token from the args
	// (like -y below) so subcommand parsers never see it. Precedence is
	// flag > EZSTACK_REPO env > current directory.
	rest, overridePath, overrideSource, repoErr := resolveRepoOverride(os.Args[1:], os.Getenv("EZSTACK_REPO"))
	if repoErr != nil {
		ui.Error(repoErr.Error())
		os.Exit(ui.ExitUsage)
	}

	// Pre-parse -y/--yes before dispatching so it works in any position,
	// e.g. "ezs -y sync", "ezs sync -y", "ezs delete -y my-branch".
	var cleanArgs []string
	for _, arg := range rest {
		if arg == "-y" || arg == "--yes" {
			ui.YesMode = true
		} else {
			cleanArgs = append(cleanArgs, arg)
		}
	}

	cmd := ""
	args := []string{}
	if len(cleanArgs) >= 1 {
		cmd = cleanArgs[0]
		args = cleanArgs[1:]
	}

	// Commands that don't require a repo. They run before the fatal chdir
	// below so a misconfigured override can never block them. --completions
	// does a best-effort chdir so branch-name completion reflects the
	// override, but never fails on a bad path.
	switch cmd {
	case "--shell-init":
		printShellInit()
		return
	case "--completions":
		if overridePath != "" {
			_ = os.Chdir(overridePath)
		}
		printCompletions(args)
		return
	case "-h", "--help":
		printUsage()
		return
	case "-v", "--version":
		fmt.Printf("ezstack version %s\n", version.Version)
		return
	case "--info":
		commands.Info(version.Version)
		return
	}

	// Apply the repo override for every real command: chdir so all downstream
	// cwd-based discovery behaves as if run from the repo root. Failure names
	// the source (--repo vs EZSTACK_REPO) so misconfiguration is obvious.
	if overridePath != "" {
		if err := os.Chdir(overridePath); err != nil {
			ui.Error(fmt.Sprintf("%s: %v", repoSourceLabel(overrideSource, overridePath), err))
			os.Exit(ui.ExitNotInRepo)
		}
	}

	// Check if we're in a git repo for all other commands. `doctor` is the
	// exception — it's meant to be the first thing users run on a fresh
	// machine, including before they've cloned or initialized any repo.
	repoPath, inRepo := checkRepoRoot()
	if !inRepo && commandNeedsRepoCheck(cmd) {
		if overridePath != "" {
			ui.Error(fmt.Sprintf("%s is not a git repository", repoSourceLabel(overrideSource, overridePath)))
		} else {
			ui.Error("ezs must be run from a git repository root (or a worktree)")
		}
		os.Exit(ui.ExitNotInRepo)
	}

	// If no command given, show usage (or guide through first-time setup)
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
		printUsage()
		return
	}

	// Auto-run first-time setup if this repo isn't configured yet.
	// Skipped for `config`/`cfg` (which perform setup) and unknown commands
	// (which will fall through to the usage error below).
	if commandNeedsRepoConfig(cmd) && !hasRepoConfig(repoPath) {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			ui.Error("ezstack is not configured for this repository. Run 'ezs config' from an interactive shell to set it up.")
			os.Exit(ui.ExitUsage)
		}
		ui.Info("ezstack is not configured for this repository. Let's set it up first.")
		fmt.Println()
		if cfgErr := commands.Config(nil); cfgErr != nil {
			if cfgErr == ui.ErrBack {
				return
			}
			ui.Error(cfgErr.Error())
			os.Exit(ui.GetExitCode(cfgErr))
		}
		// Guard against the user aborting setup without actually configuring.
		if !hasRepoConfig(repoPath) {
			ui.Error("Setup was not completed. Aborting.")
			os.Exit(ui.ExitUsage)
		}
		fmt.Println()
	}

	var err error
	switch cmd {
	case "new", "n":
		err = commands.New(args)
	case "list", "ls":
		err = commands.List(args)
	case "status", "st":
		err = commands.Status(args)
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
	case "reparent", "rp":
		err = commands.Reparent(args)
	case "stack":
		err = commands.Stack(args)
	case "unstack":
		err = commands.Unstack(args)
	case "commit", "ci":
		err = commands.Commit(args)
	case "amend":
		err = commands.Amend(args)
	case "diff":
		err = commands.Diff(args)
	case "log":
		err = commands.Log(args)
	case "push":
		err = commands.Push(args)
	case "up":
		err = commands.Up(args)
	case "down":
		err = commands.Down(args)
	case "agent":
		err = commands.Agent(args)
	case "menu":
		err = runInteractiveMenu()
	case "doctor":
		err = commands.Doctor(args)
	case "upgrade", "update":
		err = commands.Upgrade(args)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		if suggestion := ui.SuggestCommand(cmd, knownCommandNames()); suggestion != "" {
			fmt.Fprintf(os.Stderr, "Did you mean: %s?\n", suggestion)
		}
		printUsage()
		os.Exit(ui.ExitUsage)
	}

	if err != nil {
		if err == ui.ErrBack {
			return
		}
		ui.Error(err.Error())
		os.Exit(ui.GetExitCode(err))
	}
}

// runInteractiveMenu shows the main interactive menu
func runInteractiveMenu() error {
	for {
		options := []string{
			ui.IconBullet + "  config   - Configure ezstack",
			ui.IconCancel + "  delete   - Delete a branch and its worktree",
			ui.IconArrow + "  goto     - Navigate to a branch worktree",
			ui.IconInfo + "  help     - Show help",
			ui.IconNew + "  new      - Create a new branch in the stack",
			ui.IconBranch + "  pr       - Manage pull requests",
			ui.IconUp + "  reparent - Change the parent of a branch",
			ui.IconNew + "  stack    - Add a branch to a stack",
			ui.IconInfo + "  status   - Show status of current stack",
			ui.IconSync + "  sync     - Sync stack with remote (rebase onto main)",
			ui.IconCancel + "  unstack  - Remove a branch from tracking",
		}

		selected, err := ui.SelectOption(options, "Select a command:")
		if err != nil {
			return err
		}

		var cmdErr error
		switch selected {
		case 0: // config
			cmdErr = commands.Config(nil)
		case 1: // delete
			cmdErr = commands.Delete(nil)
		case 2: // goto
			cmdErr = commands.Goto(nil)
		case 3: // help
			printUsage()
			return nil
		case 4: // new
			cmdErr = commands.New(nil)
		case 5: // pr
			cmdErr = commands.PR(nil)
		case 6: // reparent
			cmdErr = commands.Reparent(nil)
		case 7: // stack
			cmdErr = commands.Stack(nil)
		case 8: // status
			cmdErr = commands.Status(nil)
		case 9: // sync
			cmdErr = commands.Sync(nil)
		case 10: // unstack
			cmdErr = commands.Unstack(nil)
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
    agent         Launch AI agent with stack context
    amend         Amend last commit and auto-sync children
    commit, ci    Commit and auto-sync child branches
    config        Configure ezstack
    delete, del, rm  Delete a branch and its worktree
    diff          Show diff against parent branch
    down          Navigate down the stack (toward children)
    goto, go      Navigate to a branch worktree
    list, ls      List all stacks and branches
    menu          Interactive command menu
    new, n        Create a new branch in the stack
    pr            Manage pull requests
    push          Push current branch or entire stack
    reparent, rp  Change the parent of a branch
    stack         Add a branch to a stack
    status, st    Show status of current stack
    sync          Sync stack with remote (accepts stack hash prefix, min 3 chars)
    unstack       Remove a branch from tracking (keeps git branch)
    up            Navigate up the stack (toward parent)
    upgrade, update  Self-update ezs and ezs-mcp from the latest GitHub release

%sOPTIONS%s
    -h, --help       Show this help message
    -v, --version    Show version
    -y, --yes        Auto-confirm all yes/no prompts (selection menus still show)
    --repo <path>    Run against this repo instead of the current directory
                     (or set EZSTACK_REPO; the flag wins)
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
    # Resolve the effective command word past any leading global flags
    # (--repo <path>, --repo=<path>, -y/--yes) so navigation commands still
    # get their "cd <path>" output eval'd when a flag precedes the command,
    # e.g. "ezs --repo /path goto feature".
    local _ezs_cmd="" _ezs_skip=0 _ezs_arg
    for _ezs_arg in "$@"; do
        if [ "$_ezs_skip" = "1" ]; then
            _ezs_skip=0
            continue
        fi
        case "$_ezs_arg" in
            --repo) _ezs_skip=1 ;;
            --repo=*|-y|--yes) ;;
            *) _ezs_cmd="$_ezs_arg"; break ;;
        esac
    done
    case "$_ezs_cmd" in
        goto|go|new|n|delete|del|rm|sync|up|down|menu)
            # These commands may output "cd <path>" which we need to eval
            eval "$(EZS_SHELL_WRAPPER=1 command ezs "$@")"
            ;;
        *)
            EZS_SHELL_WRAPPER=1 command ezs "$@"
            ;;
    esac
}

# Tab completion
_ezs_completions() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local prev="${COMP_WORDS[COMP_CWORD-1]}"
    COMPREPLY=($(compgen -W "$(command ezs --completions "${COMP_WORDS[@]:1}" 2>/dev/null)" -- "$cur"))
}
complete -F _ezs_completions ezs 2>/dev/null

# Zsh completion via bashcompinit
if [ -n "$ZSH_VERSION" ]; then
    autoload -Uz bashcompinit && bashcompinit 2>/dev/null
    complete -F _ezs_completions ezs 2>/dev/null
fi
`)
}
