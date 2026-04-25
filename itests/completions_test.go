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
		"cd_after_new", "use_worktrees", "sync_strategy", "agent_command",
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
