package main

import (
	"bytes"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

// captureCompletions runs printCompletions with the given args and returns
// the lines emitted to stdout. printCompletions writes one candidate per
// line and never errors, so we just collect everything.
func captureCompletions(t *testing.T, args []string) []string {
	t.Helper()
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()

	printCompletions(args)

	w.Close()
	os.Stdout = orig
	out := <-done
	if out == "" {
		return nil
	}
	// Trim final newline so we don't get a trailing empty entry.
	out = strings.TrimRight(out, "\n")
	return strings.Split(out, "\n")
}

func contains(slice []string, s string) bool {
	return slices.Contains(slice, s)
}

// TestPrintCompletions_TopLevelOnEmpty asserts the bare `ezs <TAB>` case —
// COMP_WORDS is (ezs, "") so we receive a single empty arg. This is the
// most common completion path; pin it down.
func TestPrintCompletions_TopLevelOnEmpty(t *testing.T) {
	got := captureCompletions(t, []string{""})
	for _, want := range []string{"new", "list", "status", "doctor", "config", "pr", "agent"} {
		if !contains(got, want) {
			t.Errorf("expected %q in top-level completions, got %v", want, got)
		}
	}
}

// TestPrintCompletions_TopLevelOnPartial covers `ezs c<TAB>`. Bash filters
// candidates by prefix via compgen, so we still emit the full top-level list.
// Before the rewrite, this case fell through and emitted nothing — bash then
// fell back to filename completion, which was the user-visible bug.
func TestPrintCompletions_TopLevelOnPartial(t *testing.T) {
	got := captureCompletions(t, []string{"c"})
	if !contains(got, "config") {
		t.Errorf("partial 'c' should still emit full list (compgen filters); got %v", got)
	}
	if len(got) < 5 {
		t.Errorf("expected full top-level list, got only %d entries: %v", len(got), got)
	}
}

func TestPrintCompletions_TopLevelIncludesDoctor(t *testing.T) {
	// doctor was missing from the original list — its only entrypoint was the
	// dispatch typo path. Regression guard.
	got := captureCompletions(t, []string{""})
	if !contains(got, "doctor") {
		t.Errorf("doctor must be in top-level completions, got %v", got)
	}
}

func TestPrintCompletions_TopLevelExcludesAliases(t *testing.T) {
	// We deliberately keep `ls`/`st`/`ci`/`del`/`rm`/`go`/`rp`/`n`/`cfg` out
	// of the top-level list to avoid doubling the menu. Aliases still work
	// at runtime; they just don't surface in completion. Pin this so a
	// future contributor doesn't "helpfully" add them back.
	got := captureCompletions(t, []string{""})
	for _, alias := range []string{"ls", "st", "ci", "del", "rm", "go", "rp", "n", "cfg"} {
		if contains(got, alias) {
			t.Errorf("alias %q should not appear in top-level completions: %v", alias, got)
		}
	}
}

func TestPrintCompletions_PRSubcommands(t *testing.T) {
	got := captureCompletions(t, []string{"pr", ""})
	for _, want := range []string{"create", "draft", "merge", "stack", "update"} {
		if !contains(got, want) {
			t.Errorf("missing pr subcommand %q in %v", want, got)
		}
	}
}

func TestPrintCompletions_ConfigSubcommands(t *testing.T) {
	got := captureCompletions(t, []string{"config", ""})
	for _, want := range []string{"set", "show", "export", "import"} {
		if !contains(got, want) {
			t.Errorf("missing config subcommand %q in %v", want, got)
		}
	}
}

func TestPrintCompletions_ConfigSetKeys(t *testing.T) {
	got := captureCompletions(t, []string{"config", "set", ""})
	// All keys mirrored from configSet's switch in commands/config.go.
	for _, want := range []string{
		"worktree_base_dir", "default_base_branch", "github_token",
		"cd_after_new", "use_worktrees", "init_submodules",
		"sync_strategy", "agent_command",
	} {
		if !contains(got, want) {
			t.Errorf("missing config key %q in %v", want, got)
		}
	}
}

// TestPrintCompletions_ConfigShowEmitsNothing locks down that `ezs config
// show <TAB>` doesn't keep handing out config keys — show takes no further
// args, so completion should stop there. This also guards that we don't
// accidentally fall through to a generic positional handler.
func TestPrintCompletions_ConfigShowEmitsNothing(t *testing.T) {
	got := captureCompletions(t, []string{"config", "show", ""})
	if len(got) != 0 {
		t.Errorf("config show <TAB> should emit nothing, got %v", got)
	}
}

func TestPrintCompletions_AgentSubcommands(t *testing.T) {
	got := captureCompletions(t, []string{"agent", ""})
	for _, want := range []string{"feature", "feat", "prompt"} {
		if !contains(got, want) {
			t.Errorf("missing agent subcommand %q in %v", want, got)
		}
	}
}

func TestPrintCompletions_AgentPromptTypes(t *testing.T) {
	got := captureCompletions(t, []string{"agent", "prompt", ""})
	for _, want := range []string{"work", "feature"} {
		if !contains(got, want) {
			t.Errorf("missing prompt type %q in %v", want, got)
		}
	}
}

// TestPrintCompletions_FlagsForCommand asserts `ezs <cmd> --<TAB>` emits the
// flag set declared for that command. Spot-check both a long flag and a short
// flag per command — full coverage is implicit in the table.
func TestPrintCompletions_FlagsForCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want []string
	}{
		{"list", []string{"--all", "-a", "--json", "--help"}},
		{"status", []string{"--all", "-a", "--branch", "--watch"}},
		{"sync", []string{"--all", "--squash", "--dry-run", "--continue"}},
		{"new", []string{"--parent", "--worktree", "--from-remote"}},
		{"agent", []string{"--cmd", "--stack", "--branch", "--dry-run"}},
		{"reparent", []string{"--branch", "--parent"}},
	}
	for _, tc := range tests {
		t.Run(tc.cmd, func(t *testing.T) {
			got := captureCompletions(t, []string{tc.cmd, "--"})
			for _, w := range tc.want {
				if !contains(got, w) {
					t.Errorf("flag %q missing for %s; got %v", w, tc.cmd, got)
				}
			}
		})
	}
}

// TestPrintCompletions_FlagsForUnknownCommand: a command we don't know about
// (someone might be typing a typo) should emit nothing for `--<TAB>` rather
// than fall through to positional logic. Otherwise typos would surface
// branch names, which is misleading.
func TestPrintCompletions_FlagsForUnknownCommand(t *testing.T) {
	got := captureCompletions(t, []string{"xyzzy", "--"})
	if len(got) != 0 {
		t.Errorf("unknown command should emit no flag completions, got %v", got)
	}
}

// TestPrintCompletions_BranchValueFlagAfterDashB pins down that a previous
// word of `-b` triggers branch-name completion. We can't assert specific
// branch names without a real repo — but we can verify the function is
// driven by the prev-word lookup and not the positional path. The
// "out-of-repo emits nothing" semantics are best-effort by design, so we
// only verify *no* unexpected non-branch entries leak through (e.g. flags
// or subcommands).
func TestPrintCompletions_BranchValueFlagPathDoesNotLeakSubcommands(t *testing.T) {
	got := captureCompletions(t, []string{"sync", "-b", ""})
	for _, leaked := range []string{"create", "draft", "set", "show", "feature", "prompt", "--all"} {
		if contains(got, leaked) {
			t.Errorf("`-b` value-of-flag path leaked %q: %v", leaked, got)
		}
	}
}

func TestPrintCompletions_LongBranchFlagPath(t *testing.T) {
	// `--branch` is the long form of `-b` and must trigger the same path.
	got := captureCompletions(t, []string{"unstack", "--branch", ""})
	for _, leaked := range []string{"set", "show", "create"} {
		if contains(got, leaked) {
			t.Errorf("`--branch` should not leak unrelated completions: %v (saw %q)", got, leaked)
		}
	}
}

// TestPrintCompletions_AgentDashSCompletesStacks: `agent -s <hash>` is the
// only command where `-s` takes a stack value (sync/delete have `-s` as a
// boolean flag). This test pins the command-aware branch in the lookup.
func TestPrintCompletions_AgentDashSCompletesStacks(t *testing.T) {
	got := captureCompletions(t, []string{"agent", "-s", ""})
	for _, leaked := range []string{"feature", "feat", "prompt", "--cmd"} {
		if contains(got, leaked) {
			t.Errorf("agent -s <TAB> leaked non-stack completion %q: %v", leaked, got)
		}
	}
}

// TestPrintCompletions_FlagAtCursorBeatsPositional asserts that when the
// current word starts with `-`, we always emit flags — never branch names
// or subcommands. This is the "user is mid-flag" hot path.
func TestPrintCompletions_FlagAtCursorBeatsPositional(t *testing.T) {
	got := captureCompletions(t, []string{"goto", "-"})
	// goto is a branch-positional command, but cursor is on a flag — only
	// flags should come back, never branch names.
	if !contains(got, "--help") {
		t.Errorf("goto -<TAB> should emit flags including --help, got %v", got)
	}
}

// TestPrintCompletions_ConfigSetValueFlagSuppressed: when the cursor is on a
// flag inside the value slot of `config set`, we must NOT suggest ezs's own
// flags. The agent_command value is a shell command line — completing
// `--<TAB>` to `--help` would silently corrupt it.
func TestPrintCompletions_ConfigSetValueFlagSuppressed(t *testing.T) {
	got := captureCompletions(t, []string{"config", "set", "agent_command", "claude", "--"})
	if len(got) != 0 {
		t.Errorf("config set value-flag position must emit no completions; got %v", got)
	}
}

// TestPrintCompletions_NewExcludedFromBranchPositional: the first positional
// of `ezs new` is the *new* branch name (does not exist yet). Completing
// existing branch names there would be misleading. Pin the exclusion.
func TestPrintCompletions_NewExcludedFromBranchPositional(t *testing.T) {
	got := captureCompletions(t, []string{"new", ""})
	// In a non-repo test process there are no branches anyway, but the
	// value of this test is structural — `new` must not be in
	// branchPositionalCommands. The fact that it's not present is enforced
	// indirectly by the table-driven flag test above; here we just make
	// the intent explicit.
	if branchPositionalCommands["new"] {
		t.Error("`new` must NOT be in branchPositionalCommands — its first positional is the new branch name")
	}
	_ = got
}

// TestPrintCompletions_SyncDashPNotBranchValue locks down that `-p`/`--parent`
// is NOT routed to branch completion under `sync` — sync.go declares them
// boolean (BoolP "Rebase onto parent"). The previous flat branchValueFlags
// map fired branch completion regardless of command, which would have
// surfaced false positives in a real repo.
func TestPrintCompletions_SyncDashPNotBranchValue(t *testing.T) {
	if branchValueFlagsByCmd["sync"]["-p"] {
		t.Error("sync's -p must NOT be in branchValueFlagsByCmd['sync'] — it's a boolean flag in sync.go")
	}
	if branchValueFlagsByCmd["sync"]["--parent"] {
		t.Error("sync's --parent must NOT be in branchValueFlagsByCmd['sync'] — it's a boolean flag in sync.go")
	}
	// And conversely, structural pin: `new`/`reparent`/`stack` SHOULD have
	// it (they take a parent branch name as the value).
	for _, cmd := range []string{"new", "reparent", "stack"} {
		if !branchValueFlagsByCmd[cmd]["-p"] {
			t.Errorf("%s's -p must be in branchValueFlagsByCmd[%q] — takes a branch value", cmd, cmd)
		}
		if !branchValueFlagsByCmd[cmd]["--parent"] {
			t.Errorf("%s's --parent must be in branchValueFlagsByCmd[%q] — takes a branch value", cmd, cmd)
		}
	}
}

// TestPrintCompletions_StackFlagOnlyValueBearingForAgent locks down the
// `--stack`/`-s` typing fix. agent.go uses StringP (value-bearing); sync,
// delete, push use BoolP. The pre-fix flat stackValueLongFlags fired stack
// completion in all four commands.
func TestPrintCompletions_StackFlagOnlyValueBearingForAgent(t *testing.T) {
	if !stackValueFlagsByCmd["agent"]["--stack"] {
		t.Error("agent's --stack must be value-bearing (StringP in agent.go)")
	}
	if !stackValueFlagsByCmd["agent"]["-s"] {
		t.Error("agent's -s must be value-bearing (StringP in agent.go)")
	}
	for _, cmd := range []string{"sync", "delete", "push"} {
		if stackValueFlagsByCmd[cmd]["--stack"] {
			t.Errorf("%s's --stack must NOT be value-bearing — it's BoolP in %s.go", cmd, cmd)
		}
		if stackValueFlagsByCmd[cmd]["-s"] {
			t.Errorf("%s's -s must NOT be value-bearing — it's BoolP in %s.go", cmd, cmd)
		}
	}
}

// TestPrintCompletions_NewSurfacesAllFlags pins the regression that v1 of
// this PR shipped: `--init-submodules`/`-s` and `--no-init-submodules`/`-S`
// were defined in new.go:64-65 but missing from commandFlags["new"], so
// `ezs new --<TAB>` silently dropped them. The integration-test drift
// gate (TestCompletions_FlagTableMatchesHelpOutput) catches this for any
// command going forward; this is the explicit case-by-case guard.
func TestPrintCompletions_NewSurfacesAllFlags(t *testing.T) {
	got := captureCompletions(t, []string{"new", "--"})
	for _, want := range []string{
		"--parent", "-p",
		"--worktree", "-w",
		"--cd", "-c",
		"--no-cd", "-C",
		"--init-submodules", "-s",
		"--no-init-submodules", "-S",
		"--from-worktree", "-f",
		"--from-remote", "-r",
		"--help", "-h",
	} {
		if !contains(got, want) {
			t.Errorf("new --<TAB> missing %q; got %v", want, got)
		}
	}
}

// TestPrintCompletions_PRSubcommandFlagsSurface covers `ezs pr <sub> --<TAB>`.
// Pre-fix this emitted only `--help`/`-h` (the bare pr commandFlags entry)
// because the routing didn't know about per-subcommand flag sets.
func TestPrintCompletions_PRSubcommandFlagsSurface(t *testing.T) {
	tests := []struct {
		sub  string
		want []string
	}{
		{"create", []string{"--title", "-t", "--body", "-b", "--draft", "-d", "--stack", "-s", "--draft-all", "--branch"}},
		{"update", []string{"--branch", "--help", "-h"}},
		{"merge", []string{"--method", "-m", "--branch", "--no-delete-branch"}},
		{"draft", []string{"--branch", "--help"}},
		{"stack", []string{"--branch", "--help"}},
	}
	for _, tc := range tests {
		t.Run(tc.sub, func(t *testing.T) {
			got := captureCompletions(t, []string{"pr", tc.sub, "--"})
			for _, w := range tc.want {
				if !contains(got, w) {
					t.Errorf("pr %s --<TAB> missing %q; got %v", tc.sub, w, got)
				}
			}
		})
	}
}

// TestPrintCompletions_BranchEqualsValueRoutesCorrectly covers the bash
// COMP_WORDBREAKS-on-`=` quirk: `--branch=foo<TAB>` arrives as
// (..., "--branch", "=", "foo"). The previous-word router must look one
// further back when prev is "=" so it still fires branch completion.
func TestPrintCompletions_BranchEqualsValueRoutesCorrectly(t *testing.T) {
	// Out-of-repo, branch lookup is silent (best-effort), so we can't
	// assert specific branch names. What we CAN assert is that we don't
	// fall through to the wrong code path: stack hashes for sync, or
	// pr subcommands, or anything else.
	got := captureCompletions(t, []string{"sync", "--branch", "=", ""})
	for _, leaked := range []string{
		"--all", "--squash", "create", "draft", "set", "show", "feature",
	} {
		if contains(got, leaked) {
			t.Errorf("`--branch=<TAB>` leaked %q (should be branch-completion path): %v", leaked, got)
		}
	}
}

// TestPrintCompletions_StackBoolFlagSyncFallsThrough: pre-fix, `sync --stack
// <TAB>` fired the value-of-flag path (treated --stack as value-bearing) and
// emitted stack hashes for the wrong reason. Post-fix, --stack is recognized
// as boolean for sync, so we fall through to the positional path —
// stackPositionalCommands["sync"] still emits stack hashes, just for the
// right reason. We can't tell the two paths apart from output alone (both
// emit stacks), so the structural test
// TestPrintCompletions_StackFlagOnlyValueBearingForAgent above is the real
// gate; this one just guards that we don't accidentally regress to emitting
// branches (which the value-of-flag path never did, but which a future
// refactor might wrongly add).
func TestPrintCompletions_StackBoolFlagSyncFallsThrough(t *testing.T) {
	// We deliberately don't seed branches here — out-of-repo, branch lookup
	// is silent. The test value is asserting that no _other_ token leaks
	// (e.g. flags or subcommand names) — a cleanliness check on the
	// fall-through path.
	got := captureCompletions(t, []string{"sync", "--stack", ""})
	for _, leaked := range []string{"--all", "--squash", "create", "set", "feature", "prompt"} {
		if contains(got, leaked) {
			t.Errorf("sync --stack <TAB> leaked %q from a wrong path: %v", leaked, got)
		}
	}
}

// TestCompletionsKnownCommandsAlignment is a drift gate: every command
// the tab-completer offers (topLevelCommands) and every command that
// has a flag spec (commandFlags) MUST also be a real dispatch target
// in knownCommands — otherwise tab-completing the entry would land on
// the "Unknown command" handler. The two maps are declared in separate
// files (main.go and completions.go), and the original PR for `ezs
// upgrade` had to remember to update both; this gate makes the
// invariant explicit so future commands can't silently regress.
func TestCompletionsKnownCommandsAlignment(t *testing.T) {
	for _, cmd := range topLevelCommands {
		if !knownCommands[cmd] {
			t.Errorf("topLevelCommands lists %q but knownCommands does not — typing it dispatches to 'Unknown command'", cmd)
		}
	}
	for cmd := range commandFlags {
		if !knownCommands[cmd] {
			t.Errorf("commandFlags lists flags for %q but knownCommands does not — flag completion is unreachable", cmd)
		}
	}
}
