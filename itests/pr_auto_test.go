package itests

import (
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
