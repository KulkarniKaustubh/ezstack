package itests

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// claudeAutoStub returns a shell script body for a `claude -p` stub that
// emits the supplied JSON response on every -p invocation. Non-`-p` calls
// (interactive launches, mcp probes) exit 0 with no output. We also record
// the prompt text to inspectPath so tests can assert what was sent.
func claudeAutoStub(jsonResponse, inspectPath string) string {
	return `#!/bin/sh
# Walk argv looking for -p; the next token is the prompt.
prompt=""
saw_p=0
for arg in "$@"; do
  if [ $saw_p -eq 1 ]; then
    prompt="$arg"
    saw_p=0
  fi
  if [ "$arg" = "-p" ]; then
    saw_p=1
  fi
done

# Record the prompt for assertions, then emit the canned JSON. When the agent
# is launched without -p (interactive ezs agent), exit silently.
if [ -n "$prompt" ]; then
  printf '%s' "$prompt" > "` + inspectPath + `"
  cat <<'__JSON_END__'
` + jsonResponse + `
__JSON_END__
fi
exit 0
`
}

// TestPRCreateAuto_HappyPath covers the end-to-end --auto flow: stub claude
// returns a {title,body} JSON, ezs creates the PR with that title and body,
// and we can read it back from the stub gh state.
func TestPRCreateAuto_HappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX + shell wrappers")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Bare git remote so push succeeds offline.
	bare := filepath.Join(env.TmpDir, "bare.git")
	mustRunGit(t, "", "init", "--bare", "-b", "main", bare)
	mustRunGit(t, env.RepoDir, "remote", "add", "origin", "git@github.com:testorg/testrepo.git")
	mustRunGit(t, env.RepoDir, "remote", "set-url", "--push", "origin", bare)
	mustRunGit(t, env.RepoDir, "push", "-q", "origin", "main")

	CreateBranchWithCommit(t, env, "feat-auth", "main")

	// Configure agent_command so --auto picks up our stub.
	cfg, _ := config.Load()
	repoCfg := cfg.GetRepoConfig(env.RepoDir)
	if repoCfg == nil {
		t.Fatal("repo not configured; SetupTestEnv should have done it")
	}
	repoCfg.AgentCommand = "claude"
	cfg.SetRepoConfig(env.RepoDir, repoCfg)
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	// Stub claude that returns a deterministic JSON response.
	// Body is single-line because the gh stub interpolates --body into a JSON
	// file via shell var, and a multi-line body would corrupt that template.
	// The PR-content parser handles multi-line just fine; that's covered in
	// the unit tests.
	promptInspect := filepath.Join(env.TmpDir, "claude_prompt.txt")
	jsonResp := `{"title":"Add JWT auth","body":"Adds JWT authentication with tests."}`
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), claudeAutoStub(jsonResp, promptInspect))

	// Run pr create --auto in-process so we don't need to rebuild the binary.
	prevYes := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = prevYes }()

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(filepath.Join(env.WorktreeDir, "feat-auth")); err != nil {
		t.Fatal(err)
	}

	if err := commands.PR([]string{"create", "--auto", "--draft", "--branch", "feat-auth"}); err != nil {
		t.Fatalf("pr create --auto: %v", err)
	}

	// The branch should now have a PR with the AI-supplied title and body.
	mgr, err := stack.NewReadOnlyManager(env.RepoDir)
	if err != nil {
		t.Fatal(err)
	}
	b := mgr.GetBranch("feat-auth")
	if b == nil {
		t.Fatal("branch missing")
	}
	if b.PRNumber == 0 {
		t.Fatal("PR not created")
	}

	// Read the stub gh's stored PR JSON to verify title/body landed.
	prFile := filepath.Join(env.TmpDir, "gh_state", "prs", "1.json")
	body, err := os.ReadFile(prFile)
	if err != nil {
		t.Fatalf("read PR file: %v", err)
	}
	bs := string(body)
	if !strings.Contains(bs, "Add JWT auth") {
		t.Errorf("PR JSON missing AI-supplied title; got:\n%s", bs)
	}
	if !strings.Contains(bs, "Adds JWT authentication with tests") {
		t.Errorf("PR JSON missing AI-supplied body; got:\n%s", bs)
	}

	// And the stub claude should have received a prompt mentioning the diff
	// + branch context — proving we shipped the right inputs.
	inspect, err := os.ReadFile(promptInspect)
	if err != nil {
		t.Fatalf("prompt inspect file missing: %v", err)
	}
	prompt := string(inspect)
	for _, want := range []string{"feat-auth", "main", "Diff"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("agent prompt missing %q (first 400 chars):\n%s", want, truncateForTest(prompt, 400))
		}
	}
}

// TestPRCreateAuto_TitleBodyOverrideAI pins the documented contract: when
// --auto is combined with -t and/or -b, the user-supplied values WIN over
// the AI's output for the field they specify. This is what lets users pin
// a title and let the AI handle just the body (or vice versa).
//
// Before this test, the priority logic in pr.go:614-619 was a documented
// promise without a regression — easy to flip by accident.
func TestPRCreateAuto_TitleBodyOverrideAI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX + shell wrappers")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	bare := filepath.Join(env.TmpDir, "bare.git")
	mustRunGit(t, "", "init", "--bare", "-b", "main", bare)
	mustRunGit(t, env.RepoDir, "remote", "add", "origin", "git@github.com:testorg/testrepo.git")
	mustRunGit(t, env.RepoDir, "remote", "set-url", "--push", "origin", bare)
	mustRunGit(t, env.RepoDir, "push", "-q", "origin", "main")

	CreateBranchWithCommit(t, env, "feat-pinned", "main")

	cfg, _ := config.Load()
	repoCfg := cfg.GetRepoConfig(env.RepoDir)
	repoCfg.AgentCommand = "claude"
	cfg.SetRepoConfig(env.RepoDir, repoCfg)
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	// AI returns "AI Title" + "AI body content". User passes --title "Pinned
	// Title" so only the body should come from the AI.
	promptInspect := filepath.Join(env.TmpDir, "claude_prompt.txt")
	jsonResp := `{"title":"AI Title","body":"AI body content"}`
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), claudeAutoStub(jsonResp, promptInspect))

	prevYes := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = prevYes }()

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(filepath.Join(env.WorktreeDir, "feat-pinned")); err != nil {
		t.Fatal(err)
	}

	if err := commands.PR([]string{"create", "--auto", "--draft", "--title", "Pinned Title", "--branch", "feat-pinned"}); err != nil {
		t.Fatalf("pr create --auto: %v", err)
	}

	prFile := filepath.Join(env.TmpDir, "gh_state", "prs", "1.json")
	body, err := os.ReadFile(prFile)
	if err != nil {
		t.Fatalf("read PR file: %v", err)
	}
	bs := string(body)
	if !strings.Contains(bs, "Pinned Title") {
		t.Errorf("PR JSON should use user-supplied title (\"Pinned Title\"); got:\n%s", bs)
	}
	if strings.Contains(bs, "AI Title") {
		t.Errorf("PR JSON should NOT use AI title when -t is given; got:\n%s", bs)
	}
	if !strings.Contains(bs, "AI body content") {
		t.Errorf("PR JSON should use AI body when only -t is given; got:\n%s", bs)
	}
}

// TestPRCreateAuto_StackMode_BulkDraft covers `pr create -s --auto`: the AI
// generator is invoked once per branch in the stack, and the resulting PRs
// each carry the AI-supplied title/body. This is the headline path PR #27
// shipped but never exercised end-to-end.
func TestPRCreateAuto_StackMode_BulkDraft(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX + shell wrappers")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	bare := filepath.Join(env.TmpDir, "bare.git")
	mustRunGit(t, "", "init", "--bare", "-b", "main", bare)
	mustRunGit(t, env.RepoDir, "remote", "add", "origin", "git@github.com:testorg/testrepo.git")
	mustRunGit(t, env.RepoDir, "remote", "set-url", "--push", "origin", bare)
	mustRunGit(t, env.RepoDir, "push", "-q", "origin", "main")

	// Two-branch stack: feat-a (parent: main), feat-b (parent: feat-a).
	CreateBranchWithCommit(t, env, "feat-a", "main")
	CreateBranchWithCommit(t, env, "feat-b", "feat-a")

	cfg, _ := config.Load()
	repoCfg := cfg.GetRepoConfig(env.RepoDir)
	repoCfg.AgentCommand = "claude"
	cfg.SetRepoConfig(env.RepoDir, repoCfg)
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	// Per-branch claude stub: emits a different JSON depending on which branch
	// the prompt targets. We disambiguate by grepping the prompt for the
	// branch name string ezs writes into the prompt header. Single-line bodies
	// because the gh stub interpolates --body via shell vars.
	stub := `#!/bin/sh
prompt=""
saw_p=0
for arg in "$@"; do
  if [ $saw_p -eq 1 ]; then prompt="$arg"; saw_p=0; fi
  if [ "$arg" = "-p" ]; then saw_p=1; fi
done
[ -z "$prompt" ] && exit 0

case "$prompt" in
  *Name:\ feat-a*)
    cat <<'__JSON_END__'
{"title":"AI: feat-a title","body":"AI: feat-a body"}
__JSON_END__
    ;;
  *Name:\ feat-b*)
    cat <<'__JSON_END__'
{"title":"AI: feat-b title","body":"AI: feat-b body"}
__JSON_END__
    ;;
  *)
    cat <<'__JSON_END__'
{"title":"AI: unknown","body":"AI: unknown"}
__JSON_END__
    ;;
esac
exit 0
`
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), stub)

	prevYes := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = prevYes }()

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(filepath.Join(env.WorktreeDir, "feat-a")); err != nil {
		t.Fatal(err)
	}

	if err := commands.PR([]string{"create", "-s", "--auto"}); err != nil {
		t.Fatalf("pr create -s --auto: %v", err)
	}

	// Both branches should now have PRs whose stored title/body came from the
	// per-branch stub.
	mgr, err := stack.NewReadOnlyManager(env.RepoDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, branchName := range []string{"feat-a", "feat-b"} {
		b := mgr.GetBranch(branchName)
		if b == nil {
			t.Fatalf("branch %s missing from stack", branchName)
		}
		if b.PRNumber == 0 {
			t.Errorf("branch %s did not get a PR", branchName)
		}
	}

	// Inspect PR #1 and #2 contents (created in the order of stack iteration).
	for _, n := range []int{1, 2} {
		path := filepath.Join(env.TmpDir, "gh_state", "prs", fmt.Sprintf("%d.json", n))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read PR #%d: %v", n, err)
		}
		s := string(data)
		// Each PR must carry an AI-drafted title (one of the two branch-
		// specific responses) — proves the AI generator was actually invoked
		// per branch rather than falling back to the formatBranchTitle.
		hasAIA := strings.Contains(s, "AI: feat-a title") && strings.Contains(s, "AI: feat-a body")
		hasAIB := strings.Contains(s, "AI: feat-b title") && strings.Contains(s, "AI: feat-b body")
		if !hasAIA && !hasAIB {
			t.Errorf("PR #%d missing per-branch AI content; got:\n%s", n, s)
		}
	}
}

// TestPRCreateAuto_StackMode_AIFailureFallsBack covers the per-branch
// fallback path: when the AI generator errors (malformed JSON, agent crash,
// empty response), the bulk creator must NOT abort — it falls back to
// formatBranchTitle and continues with the next branch. This is the
// `ui.Warn(...)/using fallback title)` path in pr.go.
func TestPRCreateAuto_StackMode_AIFailureFallsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX + shell wrappers")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	bare := filepath.Join(env.TmpDir, "bare.git")
	mustRunGit(t, "", "init", "--bare", "-b", "main", bare)
	mustRunGit(t, env.RepoDir, "remote", "add", "origin", "git@github.com:testorg/testrepo.git")
	mustRunGit(t, env.RepoDir, "remote", "set-url", "--push", "origin", bare)
	mustRunGit(t, env.RepoDir, "push", "-q", "origin", "main")

	CreateBranchWithCommit(t, env, "feat-broken", "main")

	cfg, _ := config.Load()
	repoCfg := cfg.GetRepoConfig(env.RepoDir)
	repoCfg.AgentCommand = "claude"
	cfg.SetRepoConfig(env.RepoDir, repoCfg)
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	// Stub claude: emit garbage that will fail JSON parsing. The generator
	// will surface "no JSON object found" via parsePRResponse and the bulk
	// path must keep going.
	garbageStub := `#!/bin/sh
saw_p=0
for arg in "$@"; do
  if [ $saw_p -eq 1 ]; then saw_p=0; fi
  if [ "$arg" = "-p" ]; then saw_p=1; fi
done
echo "this is not valid json"
exit 0
`
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), garbageStub)

	prevYes := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = prevYes }()

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(filepath.Join(env.WorktreeDir, "feat-broken")); err != nil {
		t.Fatal(err)
	}

	// `-s --auto` against a single-branch stack — bulk path, AI fails for
	// the only branch, expect the PR to still be created with the fallback
	// title.
	if err := commands.PR([]string{"create", "-s", "--auto"}); err != nil {
		t.Fatalf("pr create -s --auto with broken AI: %v", err)
	}

	mgr, err := stack.NewReadOnlyManager(env.RepoDir)
	if err != nil {
		t.Fatal(err)
	}
	b := mgr.GetBranch("feat-broken")
	if b == nil || b.PRNumber == 0 {
		t.Fatal("PR was not created — bulk path must fall back when AI fails, not abort")
	}

	// formatBranchTitle("feat-broken") → "Feat broken" (strip prefix dir,
	// dashes→spaces, capitalize first). The stored PR JSON should carry
	// that fallback title, NOT a parsed-AI title.
	prFile := filepath.Join(env.TmpDir, "gh_state", "prs", "1.json")
	body, err := os.ReadFile(prFile)
	if err != nil {
		t.Fatalf("read PR file: %v", err)
	}
	if !strings.Contains(string(body), "Feat broken") {
		t.Errorf("PR title should fall back to formatBranchTitle output; got:\n%s", body)
	}
}

// TestPRCreateAuto_RejectsNonClaudeAgent verifies the friendly error path
// when --auto is used with an agent ezs can't drive non-interactively. We
// don't want a silent hang or empty PR body.
func TestPRCreateAuto_RejectsNonClaudeAgent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX + shell wrappers")
	}
	env := SetupTestEnv(t)
	defer env.Cleanup()

	bare := filepath.Join(env.TmpDir, "bare.git")
	mustRunGit(t, "", "init", "--bare", "-b", "main", bare)
	mustRunGit(t, env.RepoDir, "remote", "add", "origin", "git@github.com:testorg/testrepo.git")
	mustRunGit(t, env.RepoDir, "remote", "set-url", "--push", "origin", bare)
	mustRunGit(t, env.RepoDir, "push", "-q", "origin", "main")

	CreateBranchWithCommit(t, env, "feat-x", "main")

	cfg, _ := config.Load()
	repoCfg := cfg.GetRepoConfig(env.RepoDir)
	repoCfg.AgentCommand = "aider"
	cfg.SetRepoConfig(env.RepoDir, repoCfg)
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	// Aider stub exists so the lookup doesn't fail on PATH check.
	writeExecutable(t, filepath.Join(env.StubBinDir, "aider"), "#!/bin/sh\nexit 0\n")

	prevYes := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = prevYes }()

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(filepath.Join(env.WorktreeDir, "feat-x")); err != nil {
		t.Fatal(err)
	}

	err := commands.PR([]string{"create", "--auto", "--draft", "--branch", "feat-x"})
	if err == nil {
		t.Fatal("expected error when --auto is used with non-claude agent")
	}
	if !strings.Contains(err.Error(), "Claude-family") {
		t.Errorf("error should mention Claude-family; got %v", err)
	}
}

func truncateForTest(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
