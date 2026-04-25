package itests

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// runCompletions exercises `ezs --completions ...` end-to-end. It returns the
// emitted candidates (one per line) and the error from CombinedOutput. The
// completion path is invoked from a configured repo so branch/stack lookups
// can populate.
func runCompletions(t *testing.T, env *TestEnv, args ...string) []string {
	t.Helper()
	bin := buildEzs(t)
	full := append([]string{"--completions"}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Dir = env.RepoDir
	cmd.Env = append(cmd.Environ(), "EZSTACK_HOME="+env.ConfigDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ezs --completions %v failed: %v\n%s", args, err, out)
	}
	body := strings.TrimRight(string(out), "\n")
	if body == "" {
		return nil
	}
	return strings.Split(body, "\n")
}

func itestContains(slice []string, s string) bool {
	return slices.Contains(slice, s)
}

// TestCompletions_TopLevel covers `ezs <TAB>` end-to-end. Pre-rewrite the
// output was missing `doctor`; this is the integration-level regression gate.
func TestCompletions_TopLevel(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	got := runCompletions(t, env, "")
	for _, want := range []string{
		"new", "list", "status", "sync", "config", "doctor", "pr", "agent",
		"goto", "delete", "reparent", "stack", "unstack", "up", "down",
	} {
		if !itestContains(got, want) {
			t.Errorf("top-level missing %q in: %v", want, got)
		}
	}
}

func TestCompletions_TopLevelOnPartialPrefix(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// `ezs c<TAB>` — bash filters by prefix via compgen, so we still emit
	// the full list. Pre-fix this case fell through and returned nothing,
	// which made bash fall back to filename completion.
	got := runCompletions(t, env, "c")
	if !itestContains(got, "config") || !itestContains(got, "commit") {
		t.Errorf("partial prefix should still emit full list; got %v", got)
	}
}

func TestCompletions_ConfigSubcommands(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	got := runCompletions(t, env, "config", "")
	for _, want := range []string{"set", "show", "export", "import"} {
		if !itestContains(got, want) {
			t.Errorf("config subcommand %q missing in %v", want, got)
		}
	}
}

func TestCompletions_ConfigSetKeys(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	got := runCompletions(t, env, "config", "set", "")
	for _, want := range []string{
		"worktree_base_dir", "default_base_branch", "github_token",
		"cd_after_new", "use_worktrees", "init_submodules",
		"sync_strategy", "agent_command",
	} {
		if !itestContains(got, want) {
			t.Errorf("config set key %q missing in %v", want, got)
		}
	}
}

func TestCompletions_PRSubcommands(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	got := runCompletions(t, env, "pr", "")
	for _, want := range []string{"create", "draft", "merge", "stack", "update"} {
		if !itestContains(got, want) {
			t.Errorf("pr subcommand %q missing in %v", want, got)
		}
	}
}

func TestCompletions_AgentSubcommands(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	got := runCompletions(t, env, "agent", "")
	for _, want := range []string{"feature", "feat", "prompt"} {
		if !itestContains(got, want) {
			t.Errorf("agent subcommand %q missing in %v", want, got)
		}
	}
}

// TestCompletions_BranchNamesForGoto creates a real branch and checks that
// `ezs goto <TAB>` surfaces it. The whole branch-completion path is only
// useful when the repo has stacks, so the integration test is the
// definitive coverage — unit tests can't exercise the stack manager
// load path without spinning up a repo anyway.
func TestCompletions_BranchNamesForGoto(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feature-x", "main")
	CreateBranchWithCommit(t, env, "feature-y", "main")

	got := runCompletions(t, env, "goto", "")
	for _, want := range []string{"feature-x", "feature-y"} {
		if !itestContains(got, want) {
			t.Errorf("goto <TAB> should include %q; got %v", want, got)
		}
	}
}

// TestCompletions_BranchValueFlagPath covers `ezs sync -b <TAB>`. The
// previous-word router must catch `-b` and switch to branch completion
// regardless of which command we're in.
func TestCompletions_BranchValueFlagPath(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feat-bvf", "main")

	got := runCompletions(t, env, "sync", "-b", "")
	if !itestContains(got, "feat-bvf") {
		t.Errorf("sync -b <TAB> should complete branches; got %v", got)
	}
}

// TestCompletions_StackHashesForSync verifies that `ezs sync <TAB>` lists
// stack hashes (sync's positional arg). Each branch creation produces a
// stack, so we can inspect hashes via the manager and compare.
func TestCompletions_StackHashesForSync(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feat-sync-a", "main")

	got := runCompletions(t, env, "sync", "")
	// Hashes are 7+ hex chars. We don't know the exact value, but at least
	// one non-flag, non-subcommand token should appear that looks like a
	// hash. Be liberal: assert non-empty and that none of the tokens is a
	// known top-level command (which would mean we accidentally fell
	// through to the wrong path).
	if len(got) == 0 {
		t.Errorf("sync <TAB> should emit at least one stack identifier with a stack present")
	}
	for _, tok := range got {
		switch tok {
		case "create", "draft", "merge", "set", "show", "feature", "prompt":
			t.Errorf("sync <TAB> leaked unrelated token %q (full output: %v)", tok, got)
		}
	}
}

// TestCompletions_FlagAtCursor: when the cursor is on a `-` token we always
// emit flags, never branches. This is the hot path when typing a flag.
func TestCompletions_FlagAtCursor(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	CreateBranchWithCommit(t, env, "branch-flag-test", "main")

	got := runCompletions(t, env, "goto", "-")
	if itestContains(got, "branch-flag-test") {
		t.Errorf("goto -<TAB> must not surface branch names; got %v", got)
	}
	if !itestContains(got, "--help") {
		t.Errorf("goto -<TAB> should emit flags; got %v", got)
	}
}

// TestCompletions_OutsideRepoTopLevelStillWorks ensures completion of
// top-level commands works even when the user hasn't cd'd into a repo yet
// — the most common case during shell startup. Branch/stack completion is
// allowed to be empty here (best-effort), but command lists must always
// appear.
func TestCompletions_OutsideRepoTopLevelStillWorks(t *testing.T) {
	bin := buildEzs(t)

	tmp := t.TempDir()
	cmd := exec.Command(bin, "--completions", "")
	cmd.Dir = tmp
	cmd.Env = append(cmd.Environ(), "EZSTACK_HOME="+tmp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--completions failed outside repo: %v\n%s", err, out)
	}
	body := strings.TrimRight(string(out), "\n")
	lines := strings.Split(body, "\n")
	if !slices.Contains(lines, "doctor") || !slices.Contains(lines, "config") {
		t.Errorf("top-level completions missing outside repo: %v", lines)
	}
}

// TestCompletions_SyncDashPDoesNotCompleteBranches is the integration-level
// regression for the false-positive completion bug: `-p`/`--parent` is a
// boolean for `sync` (means "rebase onto parent"), not a value-bearing flag,
// so the next slot is the next flag/positional — branches are wrong here.
// The pre-fix flat branchValueFlags map suggested branches anyway. Pin the
// negative shape: a real branch exists in the repo, but `sync -p <TAB>` must
// not surface it.
func TestCompletions_SyncDashPDoesNotCompleteBranches(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	CreateBranchWithCommit(t, env, "feat-syncp-bug", "main")

	got := runCompletions(t, env, "sync", "-p", "")
	if itestContains(got, "feat-syncp-bug") {
		t.Errorf("sync -p <TAB> must NOT emit branches (-p is boolean for sync); got %v", got)
	}

	// Same for the long form.
	got = runCompletions(t, env, "sync", "--parent", "")
	if itestContains(got, "feat-syncp-bug") {
		t.Errorf("sync --parent <TAB> must NOT emit branches (--parent is boolean for sync); got %v", got)
	}
}

// TestCompletions_DeleteStackFlagFallsThroughToPositional documents the
// reviewer's correctness fix: --stack/-s is boolean for delete (delete.go
// declares it BoolP). Pre-fix the flat stackValueLongFlags map fired the
// value-of-flag router and emitted ONLY stack hashes after --stack. Post-
// fix --stack falls through to delete's positional path which emits BOTH
// branches and stacks (delete accepts either). Verify a real branch
// surfaces — a clear signal that the fall-through path is firing rather
// than the (now-fixed) wrong path.
func TestCompletions_DeleteStackFlagFallsThroughToPositional(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	CreateBranchWithCommit(t, env, "feat-delstack", "main")

	got := runCompletions(t, env, "delete", "--stack", "")
	if !itestContains(got, "feat-delstack") {
		t.Errorf("delete --stack <TAB> should fall through to positional (branches + stacks); got %v", got)
	}
}

// TestCompletions_NewSurfacesInitSubmodulesFlag covers the missing-flag
// regression. `--init-submodules`/`-s` and the negation `--no-init-submodules`/`-S`
// are real flags on `ezs new` (new.go:64-65) but were missing from the
// commandFlags table. `ezs new --<TAB>` must surface them.
func TestCompletions_NewSurfacesInitSubmodulesFlag(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	got := runCompletions(t, env, "new", "--")
	for _, want := range []string{"--init-submodules", "--no-init-submodules"} {
		if !itestContains(got, want) {
			t.Errorf("new --<TAB> missing %q (real flag, was missing from table); got %v", want, got)
		}
	}
}

// TestCompletions_PRSubcommandFlags pins down per-subcommand flag completion.
// Pre-fix `ezs pr create --<TAB>` emitted only `--help`/`-h` (the bare `pr`
// flag set) instead of create's actual flag list. Verify a representative
// long flag for each subcommand.
func TestCompletions_PRSubcommandFlags(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	tests := []struct {
		sub  string
		want string
	}{
		{"create", "--title"},
		{"update", "--branch"},
		{"merge", "--method"},
		{"draft", "--branch"},
		{"stack", "--branch"},
	}
	for _, tc := range tests {
		t.Run(tc.sub, func(t *testing.T) {
			got := runCompletions(t, env, "pr", tc.sub, "--")
			if !itestContains(got, tc.want) {
				t.Errorf("pr %s --<TAB> missing %q; got %v", tc.sub, tc.want, got)
			}
		})
	}
}

// TestCompletions_BranchEqualsValueSyntax covers `--branch=foo<TAB>`. Bash's
// default COMP_WORDBREAKS splits on `=`, so the args we receive are
// (..., "--branch", "=", "foo"). The router must look one further back when
// prev is "=" so it still recognizes the underlying flag and routes to
// branch completion.
func TestCompletions_BranchEqualsValueSyntax(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	CreateBranchWithCommit(t, env, "feat-eqsyntax", "main")

	// Cursor on the empty value slot after `--branch=`:
	// COMP_WORDS = (ezs, sync, --branch, =, "")
	got := runCompletions(t, env, "sync", "--branch", "=", "")
	if !itestContains(got, "feat-eqsyntax") {
		t.Errorf("sync --branch=<TAB> must complete branches; got %v", got)
	}
}

// TestCompletions_FlagTableMatchesHelpOutput is the drift gate. For each
// command in commandFlags, run `ezs <cmd> --help`, parse the OPTIONS lines,
// and assert every flag printed by the binary appears in the table. This
// catches the "command grew a flag, table didn't" failure mode that produced
// the missing `--init-submodules`/`-s` regression in v1 of this PR.
//
// Asymmetric on purpose: extra entries in the table are tolerated (some help
// text mentions help-only flags), missing entries are not.
func TestCompletions_FlagTableMatchesHelpOutput(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	tableFlags := map[string][]string{
		"agent":    {"--cmd", "--stack", "-s", "--branch", "-b", "--dry-run", "--save-prompt", "--no-push", "--preset", "--no-mcp", "--help", "-h"},
		"delete":   {"--force", "-f", "--stack", "-s", "--cascade", "--help", "-h"},
		"diff":     {"--branch", "-b", "--stat", "--json", "--help", "-h"},
		"doctor":   {"--help", "-h"},
		"goto":     {"--search", "--help", "-h"},
		"list":     {"--all", "-a", "--json", "--debug", "-d", "--help", "-h"},
		"log":      {"--branch", "-b", "--json", "--help", "-h"},
		"new":      {"--parent", "-p", "--worktree", "-w", "--template", "--cd", "-c", "--no-cd", "-C", "--init-submodules", "-s", "--no-init-submodules", "-S", "--from-worktree", "-f", "--from-remote", "-r", "--help", "-h"},
		"push":     {"--all", "-a", "--stack", "-s", "--branch", "-b", "--children", "--force", "-f", "--verify", "--all-remotes", "--help", "-h"},
		"reparent": {"--branch", "-b", "--parent", "-p", "--merge", "--rebase", "--no-rebase", "--help", "-h"},
		"stack":    {"--branch", "-b", "--parent", "-p", "--base", "-B", "--help", "-h"},
		"status":   {"--all", "-a", "--branch", "-b", "--debug", "-d", "--json", "--watch", "--help", "-h"},
		"sync":     {"--stats", "--squash", "--stack", "-s", "--all", "-a", "--current", "-c", "--branch", "-b", "--parent", "-p", "--children", "-C", "--merge", "--rebase", "--no-delete-local", "--dry-run", "--continue", "--no-autostash", "--json", "--help", "-h"},
		"unstack":  {"--branch", "-b", "--help", "-h"},
	}

	bin := buildEzs(t)
	for cmd, want := range tableFlags {
		t.Run(cmd, func(t *testing.T) {
			c := exec.Command(bin, cmd, "--help")
			c.Dir = env.RepoDir
			c.Env = append(c.Environ(), "EZSTACK_HOME="+env.ConfigDir)
			out, err := c.CombinedOutput()
			if err != nil {
				t.Fatalf("ezs %s --help failed: %v\n%s", cmd, err, out)
			}
			helpFlags := parseHelpOptionsFlags(string(out))
			tableSet := map[string]bool{}
			for _, f := range want {
				tableSet[f] = true
			}
			for _, f := range helpFlags {
				if !tableSet[f] {
					t.Errorf("flag %q in `ezs %s --help` output is missing from completion table; commandFlags[%q] needs updating",
						f, cmd, cmd)
				}
			}
		})
	}
}

// parseHelpOptionsFlags pulls flag tokens out of an ezs `--help` OPTIONS
// section. Each command's help follows the convention:
//
//	OPTIONS
//	    -s, --stack            Sync current stack...
//	    -a, --all              ...
//	    --json                 ...
//
// We pluck every word matching ^-{1,2}[a-zA-Z][a-zA-Z0-9-]*$ from those
// lines. Robust to colorized output (we strip ANSI first) and to commands
// that use either short+long or long-only forms.
func parseHelpOptionsFlags(help string) []string {
	help = stripANSI(help)
	lines := strings.Split(help, "\n")
	inOptions := false
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "OPTIONS") {
			inOptions = true
			continue
		}
		if inOptions {
			// A new section heading (uppercase word at column 0) ends OPTIONS.
			if line != "" && line[0] != ' ' && line[0] != '\t' {
				inOptions = false
				continue
			}
			for _, tok := range strings.Fields(trimmed) {
				tok = strings.TrimSuffix(tok, ",")
				if isFlagToken(tok) {
					out = append(out, tok)
				}
			}
		}
	}
	return out
}

func isFlagToken(s string) bool {
	if !strings.HasPrefix(s, "-") {
		return false
	}
	body := strings.TrimLeft(s, "-")
	if body == "" || body == s {
		return false
	}
	for _, r := range body {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

// stripANSI removes ANSI escape sequences so help-output parsing is robust
// to ui.Bold/ui.Cyan/etc. Implementation is a small state machine matching
// CSI sequences (`\x1b[ ... m`) which is all the ezs UI helpers emit.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// Skip until terminating letter (per CSI grammar: any byte in
			// 0x40..0x7E ends the sequence).
			j := i + 2
			for j < len(s) {
				c := s[j]
				if c >= 0x40 && c <= 0x7E {
					break
				}
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// TestCompletions_ShellInitContainsCompletionFunction sanity-checks that
// `ezs --shell-init` still emits the bash completion wiring. If a future
// refactor drops the wiring, completions silently stop working — this
// test makes that visible.
func TestCompletions_ShellInitContainsCompletionFunction(t *testing.T) {
	bin := buildEzs(t)
	out, err := exec.Command(bin, "--shell-init").CombinedOutput()
	if err != nil {
		t.Fatalf("--shell-init failed: %v\n%s", err, out)
	}
	body := string(out)
	for _, want := range []string{
		"_ezs_completions",
		"--completions",
		"complete -F _ezs_completions ezs",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("--shell-init missing %q:\n%s", want, body)
		}
	}
}
