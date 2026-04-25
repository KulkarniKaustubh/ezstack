package itests

import (
	"os/exec"
	"strings"
	"testing"
)

// runEzs executes the cached ezs binary in env.RepoDir with the given args.
// Returns combined output + the exit error so each test can assert exit
// status and stderr message together — the two we care about for flag
// validation.
func runEzs(t *testing.T, env *TestEnv, args ...string) (string, error) {
	t.Helper()
	bin := buildEzs(t)
	cmd := exec.Command(bin, args...)
	cmd.Dir = env.RepoDir
	cmd.Env = append(cmd.Environ(), "EZSTACK_HOME="+env.ConfigDir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestFlagValidation_DoctorRejectsUnknownFlag is the integration-level
// regression for the silent-doctor bug: prior to the pflag refactor,
// `ezs doctor --bogus` ran a full health check and exited 0, masking
// typos. End-to-end binary invocation pins that this is fixed both at
// the command level and through the dispatch path.
func TestFlagValidation_DoctorRejectsUnknownFlag(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzs(t, env, "doctor", "--bogus-flag")
	if err == nil {
		t.Fatalf("doctor with unknown flag should fail; output:\n%s", out)
	}
	if !strings.Contains(out, "unknown flag") {
		t.Errorf("expected 'unknown flag' in output:\n%s", out)
	}
	// Health check banner ("ezstack doctor") must NOT appear — the parse
	// error has to short-circuit before any check runs.
	if strings.Contains(out, "git:") || strings.Contains(out, "fzf:") {
		t.Errorf("health checks ran despite unknown flag:\n%s", out)
	}
}

func TestFlagValidation_DoctorRejectsExtraPositional(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzs(t, env, "doctor", "junk")
	if err == nil {
		t.Fatalf("doctor with extra positional should fail:\n%s", out)
	}
	if !strings.Contains(out, "unexpected") {
		t.Errorf("expected 'unexpected' in output:\n%s", out)
	}
}

// TestFlagValidation_ConfigSetRejectsExtraArg is the most important itest
// in this file. The pre-fix `ezs config set <key> <val> --bogus` joined the
// trailing `--bogus` into the value with strings.Join, silently corrupting
// the user's worktree_base_dir / agent_command / etc. End-to-end test
// confirms the fix at the binary boundary.
func TestFlagValidation_ConfigSetRejectsExtraArg(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzs(t, env, "config", "set", "sync_strategy", "merge", "--bogus")
	if err == nil {
		t.Fatalf("config set with extra arg should fail:\n%s", out)
	}
	if !strings.Contains(out, "usage: ezs config set") {
		t.Errorf("expected usage hint in output:\n%s", out)
	}
}

func TestFlagValidation_ConfigShowRejectsExtraArg(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzs(t, env, "config", "show", "junk")
	if err == nil {
		t.Fatalf("config show with extra arg should fail:\n%s", out)
	}
	if !strings.Contains(out, "usage: ezs config show") {
		t.Errorf("expected usage hint in output:\n%s", out)
	}
}

func TestFlagValidation_ConfigShowRejectsBogusFlag(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzs(t, env, "config", "show", "--bogus")
	if err == nil {
		t.Fatalf("config show with bogus flag should fail:\n%s", out)
	}
	if !strings.Contains(out, "usage: ezs config show") {
		t.Errorf("expected usage hint in output:\n%s", out)
	}
}

func TestFlagValidation_ConfigExportRejectsExtraArg(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	dest := env.TmpDir + "/export.json"
	out, err := runEzs(t, env, "config", "export", dest, "junk")
	if err == nil {
		t.Fatalf("config export with extra arg should fail:\n%s", out)
	}
	if !strings.Contains(out, "usage: ezs config export") {
		t.Errorf("expected usage hint in output:\n%s", out)
	}
}

// TestFlagValidation_UpRejectsExtraArg pins down the navigation cardinality
// fix. `ezs up 1 typo` used to silently no-op the `typo`. Now any extra
// positional must surface.
func TestFlagValidation_UpRejectsExtraArg(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzs(t, env, "up", "1", "extra")
	if err == nil {
		t.Fatalf("up 1 extra should fail:\n%s", out)
	}
	if !strings.Contains(out, "at most one argument") {
		t.Errorf("expected cardinality error:\n%s", out)
	}
}

func TestFlagValidation_DownRejectsBogusFlag(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzs(t, env, "down", "--bogus")
	if err == nil {
		t.Fatalf("down --bogus should fail:\n%s", out)
	}
	// Could be "unknown flag" (since it starts with "-") or
	// "invalid step count" depending on which guard catches it. Both are
	// acceptable rejections; the bad case is silent acceptance.
	if !strings.Contains(out, "unknown flag") && !strings.Contains(out, "invalid step count") {
		t.Errorf("expected rejection for `down --bogus`:\n%s", out)
	}
}

// TestFlagValidation_ListRejectsUnknownFlag is a positive control: list
// already used pflag with ContinueOnError pre-fix. This test ensures we
// haven't accidentally broken the working commands while fixing the
// silent ones.
func TestFlagValidation_ListRejectsUnknownFlag(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzs(t, env, "list", "--bogus")
	if err == nil {
		t.Fatalf("list --bogus should fail:\n%s", out)
	}
	if !strings.Contains(out, "unknown flag") {
		t.Errorf("expected 'unknown flag':\n%s", out)
	}
}

// TestFlagValidation_HelpStillWorks asserts that the legitimate -h/--help
// path still short-circuits. After the doctor refactor I want to confirm
// it didn't accidentally become a hard error.
func TestFlagValidation_HelpStillWorks(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	tests := []struct {
		args   []string
		banner string
	}{
		{[]string{"doctor", "--help"}, "Check ezstack health"},
		{[]string{"doctor", "-h"}, "Check ezstack health"},
		{[]string{"config", "--help"}, "Configure ezstack"},
		{[]string{"up", "--help"}, "Navigate up the stack"},
		{[]string{"down", "--help"}, "Navigate down the stack"},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			out, err := runEzs(t, env, tc.args...)
			if err != nil {
				t.Errorf("%v unexpectedly failed: %v\n%s", tc.args, err, out)
			}
			if !strings.Contains(out, tc.banner) {
				t.Errorf("expected banner %q in:\n%s", tc.banner, out)
			}
		})
	}
}

// TestFlagValidation_LegitimateConfigSetStillWorks is a positive control to
// make sure the cardinality tightening didn't break the happy path.
// It also implicitly covers that the value isn't being mutated by the
// stricter parsing.
func TestFlagValidation_LegitimateConfigSetStillWorks(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// sync_strategy is an enum field with simple validation — perfect for
	// asserting end-to-end set+show roundtrip without touching the path
	// expansion paths used by worktree_base_dir.
	out, err := runEzs(t, env, "config", "set", "sync_strategy", "merge")
	if err != nil {
		t.Fatalf("legitimate config set failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Set sync_strategy = merge") {
		t.Errorf("expected success line in output:\n%s", out)
	}

	out, err = runEzs(t, env, "config", "show")
	if err != nil {
		t.Fatalf("config show failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "sync_strategy: merge") {
		t.Errorf("config show didn't reflect set value:\n%s", out)
	}
}

// TestFlagValidation_AgentCommandMultiWordRequiresQuoting pins the strict
// cardinality contract for agent_command. Multi-word shell command lines
// MUST be quoted: `ezs config set agent_command "claude --flag"`. The old
// unquoted form silently joined trailing tokens, which made it impossible
// to distinguish a legitimate flag-bearing command line from a typo like
// `set worktree_base_dir /tmp/foo --bogus`. We now reject both shapes the
// same way and put the quoting hint in the error message.
func TestFlagValidation_AgentCommandMultiWordRequiresQuoting(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzs(t, env, "config", "set", "agent_command", "claude", "--dangerously-skip-permissions")
	if err == nil {
		t.Fatalf("unquoted multi-word agent_command must be rejected:\n%s", out)
	}
	if !strings.Contains(out, "wrap it in quotes") {
		t.Errorf("error message should hint at quoting:\n%s", out)
	}
}

// TestFlagValidation_AgentCommandQuotedWorks documents the supported way
// to set a multi-word agent_command after the strict-quoting change. The
// shell collapses the quoted string to a single argv entry, so it lands
// in the cardinality-3 happy path.
func TestFlagValidation_AgentCommandQuotedWorks(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzs(t, env, "config", "set", "agent_command", "aider --model gpt-4")
	if err != nil {
		t.Fatalf("quoted multi-word agent_command should work: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Set agent_command = aider --model gpt-4") {
		t.Errorf("expected quoted value in output:\n%s", out)
	}

	out, err = runEzs(t, env, "config", "show")
	if err != nil {
		t.Fatalf("config show failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "agent_command: aider --model gpt-4") {
		t.Errorf("config show should reflect quoted multi-word value:\n%s", out)
	}
}

// TestFlagValidation_NonAgentKeyRejectsTrailing is the targeted regression
// test for the original bug: `config set worktree_base_dir /tmp/foo --bogus`
// used to silently store the literal string "/tmp/foo --bogus" as the path.
// The cardinality fix MUST keep rejecting this for non-agent keys.
func TestFlagValidation_NonAgentKeyRejectsTrailing(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzs(t, env, "config", "set", "worktree_base_dir", "/tmp/ezs-test", "--bogus")
	if err == nil {
		t.Fatalf("worktree_base_dir with trailing --bogus must fail:\n%s", out)
	}
	if !strings.Contains(out, "usage: ezs config set") {
		t.Errorf("expected usage hint:\n%s", out)
	}

	// And the good path must not have happened — no "Set worktree_base_dir"
	// success line should appear.
	if strings.Contains(out, "Set worktree_base_dir") {
		t.Errorf("partial success leaked through despite usage error:\n%s", out)
	}
}
