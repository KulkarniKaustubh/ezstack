package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
)

// topLevelCommands is what we emit for `ezs <TAB>`. Aliases (ls, st, ci, n,
// rm, del, rp, go, cfg) are intentionally omitted — bash already does prefix
// matching on the long names, and listing both forms doubles the menu without
// helping anyone.
var topLevelCommands = []string{
	"agent", "amend", "commit", "config", "delete", "diff", "doctor", "down",
	"goto", "list", "log", "menu", "new", "pr", "push",
	"reparent", "stack", "status", "sync", "unstack", "up",
}

var prSubcommands = []string{"create", "draft", "merge", "stack", "update"}

var configSubcommands = []string{"set", "show", "export", "import"}

// configKeys mirrors the keys accepted by `configSet` in commands/config.go.
// Keep in sync when a new key is added.
var configKeys = []string{
	"worktree_base_dir",
	"default_base_branch",
	"github_token",
	"cd_after_new",
	"use_worktrees",
	"init_submodules",
	"sync_strategy",
	"agent_command",
}

// commandFlags lists the flags each command accepts. Source of truth lives
// next to each command's pflag set; this map is a hand-maintained copy used
// only for completion. When a command gains/loses a flag, update both spots.
var commandFlags = map[string][]string{
	"agent":    {"--cmd", "--stack", "-s", "--branch", "-b", "--dry-run", "--save-prompt", "--no-push", "--preset", "--no-mcp", "--help", "-h"},
	"amend":    {"--merge", "--rebase", "--push", "--push-children", "--no-push", "--help", "-h"},
	"commit":   {"--merge", "--rebase", "--push", "--push-children", "--no-push", "--help", "-h"},
	"config":   {"--help", "-h"},
	"delete":   {"--force", "-f", "--stack", "-s", "--cascade", "--help", "-h"},
	"diff":     {"--stat", "--help", "-h"},
	"doctor":   {"--help", "-h"},
	"down":     {"--help", "-h"},
	"goto":     {"--search", "--help", "-h"},
	"list":     {"--all", "-a", "--json", "--debug", "-d", "--help", "-h"},
	"log":      {"--help", "-h"},
	"new":      {"--parent", "-p", "--worktree", "-w", "--cd", "-c", "--no-cd", "-C", "--from-worktree", "-f", "--from-remote", "-r", "--help", "-h"},
	"pr":       {"--help", "-h"},
	"push":     {"--all", "-a", "--force", "-f", "--children", "--help", "-h"},
	"reparent": {"--branch", "-b", "--parent", "-p", "--help", "-h"},
	"stack":    {"--branch", "-b", "--parent", "-p", "--help", "-h"},
	"status":   {"--all", "-a", "--branch", "-b", "--debug", "-d", "--json", "--watch", "--help", "-h"},
	"sync":     {"--stats", "--squash", "--stack", "-s", "--all", "-a", "--current", "-c", "--branch", "-b", "--parent", "-p", "--children", "-C", "--merge", "--rebase", "--no-delete-local", "--dry-run", "--continue", "--no-autostash", "--json", "--help", "-h"},
	"unstack":  {"--branch", "-b", "--help", "-h"},
	"up":       {"--help", "-h"},
}

// branchPositionalCommands take a branch name as their first positional arg.
// `new` is intentionally excluded — its first positional is the *new* branch
// name (which by definition doesn't exist yet), so completing existing branch
// names there would be misleading.
var branchPositionalCommands = map[string]bool{
	"goto":     true,
	"go":       true,
	"reparent": true,
	"rp":       true,
	"unstack":  true,
	"stack":    true,
}

// branchOrStackPositionalCommands accept either a branch name or a stack hash.
var branchOrStackPositionalCommands = map[string]bool{
	"delete": true,
	"del":    true,
	"rm":     true,
}

// stackPositionalCommands take a stack hash/name as their first positional arg.
var stackPositionalCommands = map[string]bool{
	"sync": true,
}

// branchValueFlags are flags whose value is a branch name. After one of these
// in the previous word we complete branches.
var branchValueFlags = map[string]bool{
	"-b":       true,
	"--branch": true,
	"-p":       true,
	"--parent": true,
}

// stackValueFlags are flags whose value is a stack hash/name. Note: `-s` is
// boolean for sync/delete, only `agent -s <hash>` takes a value, so we keep
// this map command-aware below rather than a flat lookup.
var stackValueLongFlags = map[string]bool{
	"--stack": true,
}

// printCompletions writes one candidate per line to stdout based on the
// position of the cursor, encoded in args. Bash's compgen does the prefix
// filtering, so we always dump the full candidate set for the current slot.
//
// args is exactly `${COMP_WORDS[@]:1}` from the shell function — every word
// after `ezs`, including the (possibly empty) word under the cursor.
func printCompletions(args []string) {
	// `ezs <TAB>` or `ezs <prefix><TAB>` — only one word so far, complete a
	// top-level command.
	if len(args) <= 1 {
		for _, cmd := range topLevelCommands {
			fmt.Println(cmd)
		}
		return
	}

	cmd := args[0]
	cur := args[len(args)-1]
	prev := ""
	if len(args) >= 2 {
		prev = args[len(args)-2]
	}

	// Flag completion: when the user is typing a flag, show the flag set for
	// this command. We do this before any positional logic so `-<TAB>` always
	// resolves to flags rather than being interpreted as a value.
	if strings.HasPrefix(cur, "-") {
		// `config set agent_command claude --<TAB>` — the user is typing a
		// flag *inside the value*, not for ezs itself. Suggesting ezs's own
		// flags here would be actively wrong. Same applies to any `config
		// set` value slot.
		if cmd == "config" && len(args) >= 4 && args[1] == "set" {
			return
		}
		if flags, ok := commandFlags[cmd]; ok {
			for _, f := range flags {
				fmt.Println(f)
			}
		}
		return
	}

	// Value-of-flag completion: if the previous word is a flag whose value
	// names a branch or stack, complete that.
	if branchValueFlags[prev] {
		printBranchNames()
		return
	}
	if stackValueLongFlags[prev] || (cmd == "agent" && prev == "-s") {
		printStackIdentifiers()
		return
	}

	// Subcommand-aware completion.
	switch cmd {
	case "pr":
		// `ezs pr <TAB>` — only the immediate slot after `pr` is a subcommand.
		if len(args) == 2 {
			for _, sub := range prSubcommands {
				fmt.Println(sub)
			}
		}
		return
	case "config":
		switch len(args) {
		case 2:
			for _, sub := range configSubcommands {
				fmt.Println(sub)
			}
		case 3:
			if args[1] == "set" {
				for _, k := range configKeys {
					fmt.Println(k)
				}
			}
		}
		return
	case "agent":
		// `ezs agent <TAB>` — first positional is a mode subcommand.
		if len(args) == 2 {
			fmt.Println("feature")
			fmt.Println("feat")
			fmt.Println("prompt")
			return
		}
		// `ezs agent prompt <TAB>` — second positional is a prompt type.
		if args[1] == "prompt" && len(args) == 3 {
			fmt.Println("work")
			fmt.Println("feature")
		}
		return
	}

	// Generic positional completion. Only the *first* positional after the
	// command is meaningful for branch/stack completion in the current
	// commands; subsequent positionals (e.g. `ezs reparent <branch> <parent>`)
	// also want a branch name, so we check the count loosely.
	if branchPositionalCommands[cmd] {
		printBranchNames()
		return
	}
	if branchOrStackPositionalCommands[cmd] {
		printBranchNames()
		printStackIdentifiers()
		return
	}
	if stackPositionalCommands[cmd] {
		printStackIdentifiers()
	}
}

// printBranchNames emits all branches known to ezstack across every stack.
// Best-effort: silently emits nothing if we can't load the stack manager
// (e.g. running from outside a configured repo). De-dupes because the same
// branch can appear in cross-stack contexts.
func printBranchNames() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	mgr, err := stack.NewReadOnlyManager(cwd)
	if err != nil {
		return
	}
	seen := map[string]struct{}{}
	for _, b := range mgr.GetAllBranchesInAllStacks() {
		if b == nil || b.Name == "" {
			continue
		}
		if _, dup := seen[b.Name]; dup {
			continue
		}
		seen[b.Name] = struct{}{}
		fmt.Println(b.Name)
	}
}

// printStackIdentifiers emits each stack's hash and (when set) its name.
// Both forms are accepted by `ezs sync`/`ezs agent -s` so completing both is
// useful. Best-effort: silent on lookup failure.
func printStackIdentifiers() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	mgr, err := stack.NewReadOnlyManager(cwd)
	if err != nil {
		return
	}
	for _, s := range mgr.ListStacks() {
		if s == nil {
			continue
		}
		if s.Hash != "" {
			fmt.Println(s.Hash)
		}
		if s.Name != "" {
			fmt.Println(s.Name)
		}
	}
}
