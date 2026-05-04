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

// TestAgentLs_NoCrossRepoLeak verifies the deliberate scope choice: agent
// ls is current-repo-only. Even when stacks.json contains entries for
// other repos (a real-world configuration with multiple ezstack-tracked
// repos), `agent ls` from one repo never surfaces sessions from another.
// This was the user-reported regression that drove the redesign — the
// previous --all flag pulled in noise from unrelated repos.
func TestAgentLs_NoCrossRepoLeak(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feat-here", "main")

	// Mint a real session in this repo so we have a positive baseline to
	// distinguish from foreign-repo data.
	logDir := filepath.Join(env.TmpDir, "claude_args")
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), agentStubScript(logDir))
	writeExecutable(t, filepath.Join(env.StubBinDir, "ezs-mcp"), "#!/bin/sh\nexit 0\n")
	if out, err := runEzsStubbed(t, env, "agent", "--branch", "feat-here"); err != nil {
		t.Fatalf("seed run: %v\n%s", err, out)
	}

	// Inject a foreign repo's data directly into stacks.json. Format
	// mirrors what LoadStackConfig/Save would write.
	stacksPath := filepath.Join(env.ConfigDir, "stacks.json")
	raw, err := os.ReadFile(stacksPath)
	if err != nil {
		t.Fatalf("read stacks.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse stacks.json: %v", err)
	}
	repos, _ := doc["repos"].(map[string]any)
	if repos == nil {
		t.Fatalf("stacks.json missing repos: %s", raw)
	}
	otherRepo := "/tmp/other-repo-for-test"
	repos[otherRepo] = map[string]any{
		"stacks": map[string]any{
			"deadbef": map[string]any{
				"name":               "other-feature",
				"root":               "main",
				"agent_session_id":   "11111111-2222-3333-4444-555555555555",
				"agent_session_mode": "feature",
				"tree":               map[string]any{},
			},
		},
		"branches": map[string]any{},
	}
	doc["repos"] = repos
	patched, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("re-marshal stacks.json: %v", err)
	}
	if err := os.WriteFile(stacksPath, patched, 0644); err != nil {
		t.Fatalf("write stacks.json: %v", err)
	}

	// `agent ls --json` from this repo must not surface the foreign session.
	out, err := runEzsStubbed(t, env, "agent", "ls", "--json")
	if err != nil {
		t.Fatalf("agent ls --json: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "11111111-2222") {
		t.Errorf("agent ls leaked foreign-repo session; got:\n%s", out)
	}
	if strings.Contains(string(out), "/tmp/other-repo-for-test") {
		t.Errorf("agent ls leaked foreign repo path; got:\n%s", out)
	}
	// Local session is still there.
	if !strings.Contains(string(out), "feat-here") {
		t.Errorf("agent ls dropped the local session; got:\n%s", out)
	}

	// Repo-path field was removed when --all was retired; pin the absence
	// so a re-introduction trips this test before users are surprised.
	var rows []map[string]any
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("agent ls --json output isn't valid JSON: %v\n%s", err, out)
	}
	for _, r := range rows {
		if _, has := r["repo_path"]; has {
			t.Errorf("row leaked repo_path field (contract: not set in current-repo-only mode); got: %v", r)
		}
	}
}

// TestAgentLs_AllFlagRejected pins the deliberate flag removal: -a / --all
// previously meant "list across every repo recorded in stacks.json", and
// users complained that it surfaced unrelated sessions. The redesign drops
// the flag entirely. Pin the rejection so a future contributor can't
// silently re-introduce it under a different semantic.
func TestAgentLs_AllFlagRejected(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzsStubbed(t, env, "agent", "ls", "-a")
	if err == nil {
		t.Fatalf("expected error for removed -a flag; got success:\n%s", out)
	}
	if !strings.Contains(string(out), "unknown shorthand flag") &&
		!strings.Contains(string(out), "unknown flag") {
		t.Errorf("error should be about unknown flag; got:\n%s", out)
	}

	out, err = runEzsStubbed(t, env, "agent", "ls", "--all")
	if err == nil {
		t.Fatalf("expected error for removed --all flag; got success:\n%s", out)
	}
	if !strings.Contains(string(out), "unknown flag") {
		t.Errorf("error should be about unknown flag; got:\n%s", out)
	}
}

// TestAgentLs_BranchFilter pins `agent ls --branch` (or -b): filter to
// the session bound to the user's current branch. Run from the worktree
// of feat-target — the filter must surface that branch's row only.
func TestAgentLs_BranchFilter(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Two branches in the same stack so we can verify the filter narrows.
	CreateBranchWithCommit(t, env, "feat-target", "main")
	CreateBranchWithCommit(t, env, "feat-other", "feat-target")

	logDir := filepath.Join(env.TmpDir, "claude_args")
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), agentStubScript(logDir))
	writeExecutable(t, filepath.Join(env.StubBinDir, "ezs-mcp"), "#!/bin/sh\nexit 0\n")

	// Mint sessions on both branches.
	for _, br := range []string{"feat-target", "feat-other"} {
		if out, err := runEzsStubbed(t, env, "agent", "--branch", br); err != nil {
			t.Fatalf("seed agent run for %s: %v\n%s", br, err, out)
		}
	}

	// Run --branch from feat-target's worktree → should match feat-target.
	runner := runEzsFromDir(t, env, filepath.Join(env.WorktreeDir, "feat-target"))
	out, err := runner("agent", "ls", "--branch", "--json")
	if err != nil {
		t.Fatalf("agent ls --branch --json: %v\n%s", err, out)
	}
	var rows []map[string]any
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row matching feat-target, got %d: %v", len(rows), rows)
	}
	if got, _ := rows[0]["branch_name"].(string); got != "feat-target" {
		t.Errorf("branch_name = %q, want feat-target", got)
	}

	// Short alias -b works too.
	out, err = runner("agent", "ls", "-b", "--json")
	if err != nil {
		t.Fatalf("agent ls -b --json: %v\n%s", err, out)
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("invalid JSON for -b: %v\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row from -b alias, got %d", len(rows))
	}
}

// TestAgentLs_BranchFilter_ErrorOffStackBranch covers the "user is on main
// when they ran --branch" case. We surface a clear error rather than
// emitting an empty list — silence reads as "broken" for filter commands.
func TestAgentLs_BranchFilter_ErrorOffStackBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// No branches set up; cwd is env.RepoDir which is on main.
	out, err := runEzsStubbed(t, env, "agent", "ls", "--branch")
	if err == nil {
		t.Fatalf("expected error from --branch on untracked branch; got success:\n%s", out)
	}
	if !strings.Contains(string(out), "not tracked") && !strings.Contains(string(out), "current branch") {
		t.Errorf("error should mention current branch / tracking; got:\n%s", out)
	}
}

// TestAgentLs_StackFilter pins `agent ls --stack`: only sessions
// belonging to the user's current stack appear. Set up two stacks in the
// same repo and verify the filter narrows to the cwd's stack.
func TestAgentLs_StackFilter(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feat-stack-a", "main")
	CreateBranchWithCommit(t, env, "feat-stack-b", "main") // separate stack rooted on main

	logDir := filepath.Join(env.TmpDir, "claude_args")
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), agentStubScript(logDir))
	writeExecutable(t, filepath.Join(env.StubBinDir, "ezs-mcp"), "#!/bin/sh\nexit 0\n")

	// Stack-scoped session for each stack: run `agent` (no --branch) from
	// each branch's worktree.
	for _, br := range []string{"feat-stack-a", "feat-stack-b"} {
		runner := runEzsFromDir(t, env, filepath.Join(env.WorktreeDir, br))
		if out, err := runner("agent"); err != nil {
			t.Fatalf("seed agent for %s: %v\n%s", br, err, out)
		}
	}

	// Filter from feat-stack-a's worktree.
	runner := runEzsFromDir(t, env, filepath.Join(env.WorktreeDir, "feat-stack-a"))
	out, err := runner("agent", "ls", "--stack", "--json")
	if err != nil {
		t.Fatalf("agent ls --stack --json: %v\n%s", err, out)
	}
	var rows []map[string]any
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(rows) == 0 {
		t.Fatalf("expected at least one row in stack scope, got 0: %v", rows)
	}
	// All rows must share the same stack hash.
	wantHash, _ := rows[0]["stack_hash"].(string)
	if wantHash == "" {
		t.Fatal("first row missing stack_hash")
	}
	for _, r := range rows {
		if got, _ := r["stack_hash"].(string); got != wantHash {
			t.Errorf("--stack filter leaked row from another stack: got %q, want %q in %v", got, wantHash, r)
		}
	}
}

// TestAgentLs_FeatureFilter pins `agent ls --feature`: only sessions
// created via `ezs agent feature` (mode=feature) appear. Without the
// AgentSessionMode schema field this test would have nothing to filter on.
func TestAgentLs_FeatureFilter(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Seed a stack so feature mode has somewhere to bind.
	CreateBranchWithCommit(t, env, "feat-base", "main")

	logDir := filepath.Join(env.TmpDir, "claude_args")
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), agentStubScript(logDir))
	writeExecutable(t, filepath.Join(env.StubBinDir, "ezs-mcp"), "#!/bin/sh\nexit 0\n")

	// Run a work-mode session on feat-base (default `agent`).
	runner := runEzsFromDir(t, env, filepath.Join(env.WorktreeDir, "feat-base"))
	if out, err := runner("agent"); err != nil {
		t.Fatalf("seed work-mode run: %v\n%s", err, out)
	}

	// `--feature` filter must yield zero rows now (only work-mode session
	// exists). Empty list is `[]` — not an error.
	out, err := runner("agent", "ls", "--feature", "--json")
	if err != nil {
		t.Fatalf("agent ls --feature --json (work-only): %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "[]" {
		t.Errorf("expected empty list when no feature sessions exist, got:\n%s", out)
	}

	// Now run a feature-mode session against another stack so we have one
	// to surface. Use a different branch so feat-base's stack stays work-mode.
	CreateBranchWithCommit(t, env, "feat-other", "main")
	otherStackHash := ""
	statusOut, err := runner("ls", "-a", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v\n%s", err, statusOut)
	}
	{
		var stacks []map[string]any
		if err := json.Unmarshal(statusOut, &stacks); err != nil {
			t.Fatalf("ls --json parse: %v\n%s", err, statusOut)
		}
		for _, s := range stacks {
			if branches, ok := s["branches"].([]any); ok {
				for _, br := range branches {
					if bm, ok := br.(map[string]any); ok {
						if bm["name"] == "feat-other" {
							if h, ok := s["hash"].(string); ok {
								otherStackHash = h
							}
						}
					}
				}
			}
		}
	}
	if otherStackHash == "" {
		t.Fatal("could not resolve hash for feat-other's stack")
	}

	if out, err := runner("agent", "feature", "-s", otherStackHash, "Test feature"); err != nil {
		t.Fatalf("seed feature run: %v\n%s", err, out)
	}

	out, err = runner("agent", "ls", "--feature", "--json")
	if err != nil {
		t.Fatalf("agent ls --feature --json: %v\n%s", err, out)
	}
	var rows []map[string]any
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 feature-mode row, got %d: %v", len(rows), rows)
	}
	if got, _ := rows[0]["mode"].(string); got != "feature" {
		t.Errorf("mode = %q, want feature", got)
	}
	if got, _ := rows[0]["stack_hash"].(string); got != otherStackHash {
		t.Errorf("stack_hash = %q, want %q (feature-mode stack)", got, otherStackHash)
	}
	// Display name should carry the "feature-" infix.
	if got, _ := rows[0]["display_name"].(string); !strings.HasPrefix(got, "_ezstack-feature-") {
		t.Errorf("display_name = %q, want _ezstack-feature- prefix", got)
	}
}

// TestAgentLs_FilterMutualExclusion pins the rejection of combined
// filters. --branch + --stack is ambiguous (different scopes); silently
// coercing to one would surprise users.
func TestAgentLs_FilterMutualExclusion(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	out, err := runEzsStubbed(t, env, "agent", "ls", "--branch", "--stack")
	if err == nil {
		t.Fatalf("expected error for combined filters; got success:\n%s", out)
	}
	if !strings.Contains(string(out), "mutually exclusive") {
		t.Errorf("error should mention mutual exclusion; got:\n%s", out)
	}

	out, err = runEzsStubbed(t, env, "agent", "ls", "--stack", "--feature")
	if err == nil {
		t.Fatalf("expected error for --stack + --feature; got success:\n%s", out)
	}
	if !strings.Contains(string(out), "mutually exclusive") {
		t.Errorf("error should mention mutual exclusion; got:\n%s", out)
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
