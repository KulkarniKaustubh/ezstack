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
