package itests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentLs_EmptyRepo covers the "no sessions yet" message: a fresh repo
// with no agent runs should print a helpful hint instead of a blank line
// or a confusing error. This is one of those discoverability tests where
// the message wording matters.
func TestAgentLs_EmptyRepo(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzsStubbed(t, env, "agent", "ls")
	if err != nil {
		t.Fatalf("ezs agent ls on empty repo: %v\n%s", err, out)
	}
	o := string(out)
	if !strings.Contains(o, "No tracked AI sessions") {
		t.Errorf("expected 'No tracked AI sessions' message; got:\n%s", o)
	}
	if !strings.Contains(o, "Run 'ezs agent'") {
		t.Errorf("expected next-step hint; got:\n%s", o)
	}
}

// TestAgentLs_AfterSessionPersisted verifies that running `ezs agent` once
// (which mints + persists a session UUID) makes that session show up in
// `ezs agent ls`. End-to-end: persist → re-list → assert.
func TestAgentLs_AfterSessionPersisted(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feat-tracked", "main")

	logDir := filepath.Join(env.TmpDir, "claude_args")
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), agentStubScript(logDir))
	writeExecutable(t, filepath.Join(env.StubBinDir, "ezs-mcp"), "#!/bin/sh\nexit 0\n")

	// Mint a session for feat-tracked (branch-scoped).
	if out, err := runEzsStubbed(t, env, "agent", "--branch", "feat-tracked"); err != nil {
		t.Fatalf("agent run: %v\n%s", err, out)
	}
	first := readArgsLog(t, logDir, 0)
	mintedID := flagValue(first, "--session-id")
	if mintedID == "" {
		t.Fatal("setup failure: agent run did not mint a session ID")
	}

	// JSON output is the easiest to assert on — no ANSI color codes, no
	// table layout drift.
	out, err := runEzsStubbed(t, env, "agent", "ls", "--json")
	if err != nil {
		t.Fatalf("ezs agent ls --json: %v\n%s", err, out)
	}

	var rows []map[string]any
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("agent ls --json output is not valid JSON: %v\nraw: %s", err, out)
	}

	var match map[string]any
	for _, r := range rows {
		if id, _ := r["session_id"].(string); id == mintedID {
			match = r
			break
		}
	}
	if match == nil {
		t.Fatalf("session %s missing from agent ls output: %v", mintedID, rows)
	}

	// Exact field assertions for the row we just minted.
	if got, _ := match["scope"].(string); got != "branch" {
		t.Errorf("scope = %q, want branch", got)
	}
	if got, _ := match["branch_name"].(string); got != "feat-tracked" {
		t.Errorf("branch_name = %q, want feat-tracked", got)
	}
	if got, _ := match["display_name"].(string); got != "_ezstack-feat-tracked" {
		t.Errorf("display_name = %q, want _ezstack-feat-tracked", got)
	}
	if got, _ := match["resume_cmd"].(string); got != "ezs agent --branch feat-tracked" {
		t.Errorf("resume_cmd = %q, want 'ezs agent --branch feat-tracked'", got)
	}
}

// TestAgentLs_StackScopedSession verifies the stack-scoped row shape: when
// a session is bound to a stack (the default `ezs agent` flow), agent ls
// reports it under scope=stack with the stack hash.
func TestAgentLs_StackScopedSession(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feat-s1", "main")

	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"),
		agentStubScript(filepath.Join(env.TmpDir, "claude_args")))
	writeExecutable(t, filepath.Join(env.StubBinDir, "ezs-mcp"), "#!/bin/sh\nexit 0\n")

	// Run from inside the worktree with no --branch so the session is
	// stack-scoped (default behavior).
	stackedRunner := runEzsFromDir(t, env, filepath.Join(env.WorktreeDir, "feat-s1"))
	if out, err := stackedRunner("agent"); err != nil {
		t.Fatalf("agent run: %v\n%s", err, out)
	}

	out, err := runEzsStubbed(t, env, "agent", "ls", "--json")
	if err != nil {
		t.Fatalf("agent ls --json: %v\n%s", err, out)
	}
	var rows []map[string]any
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}

	stackRows := 0
	for _, r := range rows {
		if scope, _ := r["scope"].(string); scope == "stack" {
			stackRows++
			if hash, _ := r["stack_hash"].(string); hash == "" {
				t.Errorf("stack-scoped row missing stack_hash: %v", r)
			}
			if cmd, _ := r["resume_cmd"].(string); !strings.HasPrefix(cmd, "ezs agent -s ") {
				t.Errorf("stack resume_cmd should start with 'ezs agent -s '; got %q", cmd)
			}
		}
	}
	if stackRows == 0 {
		t.Fatalf("expected at least one stack-scoped session in: %v", rows)
	}
}

// TestAgentLs_RejectsExtraPositional pins the command's argument contract:
// no positional args. Without this, a typo like `ezs agent ls some-stack`
// could silently succeed and confuse users into thinking it filtered.
func TestAgentLs_RejectsExtraPositional(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzsStubbed(t, env, "agent", "ls", "extra-arg")
	if err == nil {
		t.Fatalf("expected error for extra positional; got success:\n%s", out)
	}
	if !strings.Contains(string(out), "no positional arguments") {
		t.Errorf("error should mention 'no positional arguments'; got:\n%s", out)
	}
}

// TestAgentLs_ListAlias verifies `ezs agent list` works the same as `ezs
// agent ls` — both are the documented entry points, both must dispatch.
func TestAgentLs_ListAlias(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzsStubbed(t, env, "agent", "list")
	if err != nil {
		t.Fatalf("agent list (alias): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "No tracked AI sessions") {
		t.Errorf("expected same empty-repo message from 'list' alias as from 'ls'; got:\n%s", out)
	}
}

// runEzsFromDir returns a runner that executes ezs from the given working
// directory (instead of env.RepoDir). Used to exercise the "agent run from
// inside a worktree" case so the session lands as stack-scoped.
func runEzsFromDir(t *testing.T, env *TestEnv, dir string) func(args ...string) ([]byte, error) {
	t.Helper()
	bin := buildEzsBinary(t)
	return func(args ...string) ([]byte, error) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"EZSTACK_HOME="+env.ConfigDir,
			"PATH="+env.StubBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		)
		return cmd.CombinedOutput()
	}
}
