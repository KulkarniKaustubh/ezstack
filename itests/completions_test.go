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

// TestCompletions_DeleteStackFlagNarrowsToStacks pins the narrowing: when
// the user has typed `delete --stack` (or `-s`), the positional must be a
// stack hash, so branch suggestions are noise and are suppressed. Without
// the narrowing, a real branch like `feat-delstack` would leak through
// delete's branchOrStack positional handler.
func TestCompletions_DeleteStackFlagNarrowsToStacks(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()
	CreateBranchWithCommit(t, env, "feat-delstack", "main")

	for _, flag := range []string{"--stack", "-s"} {
		t.Run(flag, func(t *testing.T) {
			got := runCompletions(t, env, "delete", flag, "")
			if itestContains(got, "feat-delstack") {
				t.Errorf("delete %s <TAB> must NOT emit branches (signal is stack-only); got %v", flag, got)
			}
			if len(got) == 0 {
				t.Errorf("delete %s <TAB> should still emit at least one stack identifier; got nothing", flag)
			}
		})
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

// TestCompletions_AgentPromptHasOwnFlags pins the per-subcommand flag fix
// for `agent prompt`. The prompt subcommand declares its own flagset
// (--shipped/--custom/--repo/--edit/--reset). Pre-fix `agent prompt --<TAB>`
// fell through to commandFlags["agent"] and suggested --cmd / --stack /
// --branch — flags from the work/feature flagset that prompt doesn't accept.
func TestCompletions_AgentPromptHasOwnFlags(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	got := runCompletions(t, env, "agent", "prompt", "--")
	for _, want := range []string{"--shipped", "--custom", "--repo", "--edit", "--reset"} {
		if !itestContains(got, want) {
			t.Errorf("agent prompt --<TAB> missing %q; got %v", want, got)
		}
	}
	// Phantom check: agent's main-mode flags (--cmd/--stack/--branch/etc.)
	// are not accepted by the prompt subcommand's flagset, so completing
	// them here would lead users into a wrong invocation.
	for _, phantom := range []string{"--cmd", "--stack", "--dry-run", "--save-prompt", "--no-push", "--preset", "--no-mcp"} {
		if itestContains(got, phantom) {
			t.Errorf("agent prompt --<TAB> leaked main-mode flag %q; got %v", phantom, got)
		}
	}
}

// TestCompletions_PRTopLevelHasDraftAll pins down that `ezs pr --<TAB>`
// surfaces `--draft-all`. The flag is documented in pr.go's help text and
// parsed in pr.go:49 — leaving it out of commandFlags["pr"] silently
// dropped completion for the only top-level pr flag besides --help.
func TestCompletions_PRTopLevelHasDraftAll(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	got := runCompletions(t, env, "pr", "--")
	if !itestContains(got, "--draft-all") {
		t.Errorf("pr --<TAB> missing --draft-all; got %v", got)
	}
}

// TestCompletions_PushHasNoPhantoms guards the v2 fix where push had
// phantom `--all`/`-a`/`--children` entries in the completion table.
// push.go declares neither — completing them sent users to a runtime
// "unknown flag" error.
func TestCompletions_PushHasNoPhantoms(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	got := runCompletions(t, env, "push", "--")
	for _, phantom := range []string{"--all", "-a", "--children"} {
		if itestContains(got, phantom) {
			t.Errorf("push --<TAB> emits phantom %q (push.go declares no such flag); got %v", phantom, got)
		}
	}
	// Positive control: real flags must still be there.
	for _, want := range []string{"--stack", "--branch", "--force", "--verify", "--all-remotes"} {
		if !itestContains(got, want) {
			t.Errorf("push --<TAB> dropped real flag %q; got %v", want, got)
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

// TestCompletions_FlagTableMatchesHelpOutput is the bidirectional drift gate.
// For each command we exercise BOTH directions:
//
//  1. help ⊆ completion: every flag in `<cmd> --help` OPTIONS must appear in
//     the completion table. This catches the v1 regression where `new` grew
//     `--init-submodules` but the completion table forgot it.
//  2. completion ⊆ help ∪ tolerated: every flag the completion table emits
//     must either appear in `<cmd> --help` OR be in the per-command
//     `toleratedExtras` allowlist below. This catches phantom flags — e.g.
//     `push` previously had `--all`/`-a`/`--children` in the table even
//     though push.go declares no such flags. Without the reverse check
//     users tab-complete to a flag and then `push` rejects with
//     "unknown flag" at runtime.
//
// The completion table is fetched live via `ezs --completions <cmd> --`
// rather than re-listed in this test. That removes the duplicate-source-
// of-truth problem (the v1 test had its own copy of the table that
// drifted out of sync with completions.go).
//
// toleratedExtras documents flags that ARE registered with pflag but NOT
// printed in the help OPTIONS section — typically advanced/internal flags.
// Each entry must point at a real declaration in cmd/ezs/commands/*.go.
func TestCompletions_FlagTableMatchesHelpOutput(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Flags declared with pflag but intentionally not printed in --help
	// OPTIONS. Listing them here keeps the bidirectional check honest
	// without forcing every advanced flag to be documented in the help
	// banner. If the underlying command stops registering one of these,
	// remove the entry; if a new advanced flag is added, add it here.
	toleratedExtras := map[string]map[string]bool{
		"agent": {
			// agent.go:93-96 declares these but the help banner only
			// surfaces the user-facing subset.
			"--save-prompt": true,
			"--no-push":     true,
			"--preset":      true,
			"--no-mcp":      true,
		},
	}

	// Commands whose flag completion path doesn't reach commandFlags
	// (handled by a different code branch — config/menu have no flags
	// other than --help; agent prompt/feature use their own subcommand
	// table). They're skipped here and covered by their own tests.
	commands := []string{
		"agent", "delete", "diff", "doctor", "goto", "list", "log",
		"new", "pr", "push", "reparent", "stack", "status", "sync",
		"unstack",
	}

	bin := buildEzs(t)
	for _, cmd := range commands {
		t.Run(cmd, func(t *testing.T) {
			// Fetch the live completion output for `ezs <cmd> --<TAB>`.
			compCmd := exec.Command(bin, "--completions", cmd, "--")
			compCmd.Dir = env.RepoDir
			compCmd.Env = append(compCmd.Environ(), "EZSTACK_HOME="+env.ConfigDir)
			compOut, err := compCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("ezs --completions %s -- failed: %v\n%s", cmd, err, compOut)
			}
			compFlags := splitCompletionLines(compOut)

			// Fetch help OPTIONS for the same command.
			helpCmd := exec.Command(bin, cmd, "--help")
			helpCmd.Dir = env.RepoDir
			helpCmd.Env = append(helpCmd.Environ(), "EZSTACK_HOME="+env.ConfigDir)
			helpOut, err := helpCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("ezs %s --help failed: %v\n%s", cmd, err, helpOut)
			}
			helpFlags := parseHelpOptionsFlags(string(helpOut))

			compSet := map[string]bool{}
			for _, f := range compFlags {
				compSet[f] = true
			}
			helpSet := map[string]bool{}
			for _, f := range helpFlags {
				helpSet[f] = true
			}

			// Direction 1 (missing): every help flag must appear in
			// completion. Catches the "command grew a flag, table didn't"
			// failure mode.
			for _, f := range helpFlags {
				if !compSet[f] {
					t.Errorf("flag %q in `ezs %s --help` is missing from completion; commandFlags[%q] needs updating",
						f, cmd, cmd)
				}
			}

			// Direction 2 (phantom): every completion flag must appear
			// in help OR the tolerated-extras allowlist. Catches phantom
			// entries that don't actually exist in the parser. This is
			// the check that catches `push` having phantom `--all` /
			// `-a` / `--children` in the v2 table.
			for _, f := range compFlags {
				if helpSet[f] {
					continue
				}
				if toleratedExtras[cmd][f] {
					continue
				}
				t.Errorf("phantom flag %q: completion offers it but `ezs %s --help` doesn't list it and it's not in toleratedExtras[%q] — does the parser actually accept it?",
					f, cmd, cmd)
			}
		})
	}
}

// splitCompletionLines splits the stdout of `ezs --completions ...` into
// non-empty trimmed entries. Used by the drift gate to consume completion
// candidates without each test rebuilding the same parser.
func splitCompletionLines(out []byte) []string {
	body := strings.TrimRight(string(out), "\n")
	if body == "" {
		return nil
	}
	var flags []string
	for _, line := range strings.Split(body, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			flags = append(flags, line)
		}
	}
	return flags
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
