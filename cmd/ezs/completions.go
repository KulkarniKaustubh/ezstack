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
	"reparent", "stack", "status", "sync", "unstack", "up", "upgrade",
}

var prSubcommands = []string{"create", "draft", "merge", "refresh", "stack", "unlink", "update"}

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
//
// TestCompletions_FlagTableMatchesHelpOutput in itests/completions_test.go is
// a *bidirectional* drift gate. For every command it asserts:
//
//  1. help ⊆ completion: every flag in `<cmd> --help` OPTIONS appears in
//     this table. Catches "command grew a flag, table didn't".
//  2. completion ⊆ help ∪ toleratedExtras: every flag this table emits is
//     either documented in `<cmd> --help` OR explicitly allowlisted in the
//     test's toleratedExtras map (for advanced flags that the parser
//     accepts but the help banner intentionally omits, e.g. agent's
//     internal --save-prompt/--no-push/--preset/--no-mcp).
//
// So both "missing entry" and "phantom entry" fail CI. Don't add a flag
// here that the parser doesn't actually accept — it'll trip direction (2)
// and confuse users who tab-complete to a flag the binary then rejects.
var commandFlags = map[string][]string{
	"agent":    {"--cmd", "--stack", "-s", "--branch", "-b", "--dry-run", "--save-prompt", "--no-push", "--preset", "--no-mcp", "--no-resume", "--help", "-h"},
	"amend":    {"--merge", "--rebase", "--push", "--push-children", "--no-push", "--help", "-h"},
	"commit":   {"--merge", "--rebase", "--push", "--push-children", "--no-push", "--help", "-h"},
	"config":   {"--help", "-h"},
	"delete":   {"--force", "-f", "--stack", "-s", "--cascade", "--help", "-h"},
	"diff":     {"--branch", "-b", "--stat", "--json", "--help", "-h"},
	"doctor":   {"--help", "-h"},
	"down":     {"--help", "-h"},
	"goto":     {"--search", "--help", "-h"},
	"list":     {"--all", "-a", "--json", "--debug", "-d", "--help", "-h"},
	"log":      {"--branch", "-b", "--json", "--help", "-h"},
	"new":      {"--parent", "-p", "--worktree", "-w", "--template", "--cd", "-c", "--no-cd", "-C", "--init-submodules", "-s", "--no-init-submodules", "-S", "--from-worktree", "-f", "--from-remote", "-r", "--help", "-h"},
	"pr":       {"--draft-all", "--help", "-h"},
	"push":     {"--stack", "-s", "--branch", "-b", "--force", "-f", "--verify", "--all-remotes", "--help", "-h"},
	"reparent": {"--branch", "-b", "--parent", "-p", "--merge", "--rebase", "--no-rebase", "--help", "-h"},
	"stack":    {"--branch", "-b", "--parent", "-p", "--base", "-B", "--help", "-h"},
	"status":   {"--all", "-a", "--branch", "-b", "--debug", "-d", "--json", "--watch", "--help", "-h"},
	"sync":     {"--stats", "--squash", "--stack", "-s", "--all", "-a", "--current", "-c", "--branch", "-b", "--parent", "-p", "--children", "-C", "--merge", "--rebase", "--no-delete-local", "--dry-run", "--continue", "--no-autostash", "--json", "--help", "-h"},
	"unstack":  {"--branch", "-b", "--help", "-h"},
	"up":       {"--help", "-h"},
	"upgrade":  {"--check", "--version", "--force", "--no-mcp", "--yes", "-y", "--help", "-h"},
}

// prSubcommandFlags lists the flags each `pr <subcommand>` accepts. Used so
// `ezs pr create --<TAB>` surfaces create's flags rather than just the
// top-level `--help`/`-h` from `pr` itself. Same drift-gate test applies.
var prSubcommandFlags = map[string][]string{
	"create":  {"--stack", "-s", "--draft-all", "--title", "-t", "--body", "-b", "--draft", "-d", "--branch", "--auto", "--ai", "--force", "-f", "--recreate", "--help", "-h"},
	"update":  {"--branch", "--help", "-h"},
	"merge":   {"--method", "-m", "--branch", "--no-delete-branch", "--help", "-h"},
	"draft":   {"--branch", "--help", "-h"},
	"stack":   {"--branch", "--help", "-h"},
	"refresh": {"--branch", "--stack", "-s", "--help", "-h"},
	"unlink":  {"--branch", "--all", "--yes", "-y", "--help", "-h"},
}

// agentSubcommandFlags lists the flags `ezs agent prompt` accepts. Without
// this, `agent prompt --<TAB>` falls through to commandFlags["agent"] and
// suggests --cmd / --stack / --branch — flags from a different parser that
// `prompt` doesn't accept. (`feature` / `feat` reuse the same flagset as
// the default agent mode, so they pick up commandFlags["agent"] correctly.)
var agentSubcommandFlags = map[string][]string{
	"prompt": {"--shipped", "--custom", "--repo", "--edit", "--reset", "--help", "-h"},
	"ls":     {"--branch", "-b", "--stack", "-s", "--feature", "--json", "--help", "-h"},
	"list":   {"--branch", "-b", "--stack", "-s", "--feature", "--json", "--help", "-h"},
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

// branchValueFlagsByCmd lists the flags whose value is a branch name, scoped
// per command. Some flags share a name across commands but have different
// types — e.g. `-p`/`--parent` is a string (parent branch) for `new`, `stack`,
// and `reparent`, but a *boolean* (rebase onto parent) for `sync`. A flat map
// would emit branch completions after `ezs sync -p`, where the next slot is
// not a value at all. Same for `-b`/`--branch`: value-bearing for most
// commands but never used boolean — kept per-command for consistency and to
// avoid surprises if a future command repurposes the short form.
var branchValueFlagsByCmd = map[string]map[string]bool{
	"new":      {"-p": true, "--parent": true},
	"stack":    {"-b": true, "--branch": true, "-p": true, "--parent": true},
	"reparent": {"-b": true, "--branch": true, "-p": true, "--parent": true},
	"unstack":  {"-b": true, "--branch": true},
	"sync":     {"-b": true, "--branch": true}, // -p is BOOL for sync; do NOT add
	"delete":   {"-b": true, "--branch": true},
	"status":   {"-b": true, "--branch": true},
	"list":     {"-b": true, "--branch": true},
	"diff":     {"-b": true, "--branch": true},
	"log":      {"-b": true, "--branch": true},
	"push":     {"-b": true, "--branch": true},
	"agent":    {"-b": true, "--branch": true},
}

// stackValueFlagsByCmd lists the flags whose value is a stack hash/name,
// scoped per command. `--stack`/`-s` is a *boolean* for `sync`, `delete`, and
// `push` (means "operate on the current stack" or "treat positional as
// stack"), but a *string* (stack hash/name to target) for `agent`. The flat
// previous map mistakenly fired stack completion in all four commands.
var stackValueFlagsByCmd = map[string]map[string]bool{
	"agent": {"-s": true, "--stack": true},
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
	// Bash splits on `=` per default COMP_WORDBREAKS, so `--branch=foo<TAB>`
	// arrives as ("--branch", "=", "foo"). Look one further back when the
	// previous word is "=" so the value-of-flag router still recognizes the
	// underlying flag. `--branch=` (no value yet) lands the cursor on "" with
	// prev="=" and prev-prev="--branch", which is exactly what we want.
	prev := ""
	if len(args) >= 2 {
		prev = args[len(args)-2]
		if prev == "=" && len(args) >= 3 {
			prev = args[len(args)-3]
		}
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
		// `ezs pr <sub> --<TAB>` — surface the subcommand's own flags rather
		// than the bare `--help`/`-h` from `pr` itself. Without this branch,
		// `pr create --<TAB>` was emitting just `--help`/`-h` despite create
		// having a rich flag set.
		if cmd == "pr" && len(args) >= 3 {
			if flags, ok := prSubcommandFlags[args[1]]; ok {
				for _, f := range flags {
					fmt.Println(f)
				}
				return
			}
		}
		// `ezs agent prompt --<TAB>` — the prompt subcommand has its own
		// flagset (--shipped/--custom/--repo/--edit/--reset). Without this
		// branch we'd fall through to commandFlags["agent"] and suggest
		// flags from the work/feature flagset that prompt doesn't accept.
		if cmd == "agent" && len(args) >= 3 {
			if flags, ok := agentSubcommandFlags[args[1]]; ok {
				for _, f := range flags {
					fmt.Println(f)
				}
				return
			}
		}
		if flags, ok := commandFlags[cmd]; ok {
			for _, f := range flags {
				fmt.Println(f)
			}
		}
		return
	}

	// Value-of-flag completion: if the previous word is a flag whose value
	// names a branch or stack, complete that. Per-command lookup so that
	// flags that share a name across commands but differ in type (e.g. -p
	// is string for `new`/`reparent` but bool for `sync`) don't surface
	// false-positive completions.
	if branchValueFlagsByCmd[cmd][prev] {
		printBranchNames()
		return
	}
	if stackValueFlagsByCmd[cmd][prev] {
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
			fmt.Println("ls")
			fmt.Println("list")
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
		// `delete --stack` / `delete -s` narrows the positional to a stack
		// hash (see delete.go); skip branches so the menu matches intent.
		for _, a := range args[1 : len(args)-1] {
			if a == "--stack" || a == "-s" {
				printStackIdentifiers()
				return
			}
		}
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
// useful. De-dupes because Hash == Name (or two stacks sharing a name) would
// otherwise emit the same string twice. Best-effort: silent on lookup failure.
func printStackIdentifiers() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	mgr, err := stack.NewReadOnlyManager(cwd)
	if err != nil {
		return
	}
	seen := map[string]struct{}{}
	emit := func(id string) {
		if id == "" {
			return
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		fmt.Println(id)
	}
	for _, s := range mgr.ListStacks() {
		if s == nil {
			continue
		}
		emit(s.Hash)
		emit(s.Name)
	}
}
