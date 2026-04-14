package itests

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/version"
)

var (
	buildEzsOnce sync.Once
	ezsBinPath   string
	ezsBuildErr  error
)

// buildEzsBinary compiles cmd/ezs once per test run and returns its path.
func buildEzsBinary(t *testing.T) string {
	t.Helper()
	buildEzsOnce.Do(func() {
		repoRoot, err := findRepoRoot()
		if err != nil {
			ezsBuildErr = err
			return
		}
		outDir, err := os.MkdirTemp("", "ezs-bin-*")
		if err != nil {
			ezsBuildErr = err
			return
		}
		bin := filepath.Join(outDir, "ezs")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/ezs")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			ezsBuildErr = &buildError{msg: string(out), err: err}
			return
		}
		ezsBinPath = bin
	})
	if ezsBuildErr != nil {
		t.Fatalf("build ezs: %v", ezsBuildErr)
	}
	return ezsBinPath
}

// writeExecutable writes body to path with 0755 perms.
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// runEzsAgent runs the built ezs binary with the given args in env.RepoDir,
// isolating PATH to env.StubBinDir so stubs are the only claude/ezs-mcp on PATH.
// Returns combined output.
func runEzsAgent(t *testing.T, env *TestEnv, args ...string) ([]byte, error) {
	t.Helper()
	ezsBin := buildEzsBinary(t)
	cmd := exec.Command(ezsBin, append([]string{"agent"}, args...)...)
	cmd.Dir = env.RepoDir
	cmd.Env = append(os.Environ(),
		"EZSTACK_HOME="+env.ConfigDir,
		// StubBinDir first so stub claude/ezs-mcp shadow any real ones.
		// Append the original PATH so `git`, `sh`, `go`, etc. remain available.
		"PATH="+env.StubBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	return cmd.CombinedOutput()
}

// TestAgentMCPMode_PromptContainsStub verifies that when claude + ezs-mcp are
// both on PATH and the ezstack MCP is already registered, `ezs agent` sends
// the short MCP stub to claude (instead of the full embedded DOCUMENTATION.md).
func TestAgentMCPMode_PromptContainsStub(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feature-a", "main")

	promptLog := filepath.Join(env.TmpDir, "claude_agent_args.txt")

	// Stub claude handles three call shapes:
	//   claude mcp get ezstack         → exit 0 (already registered)
	//   claude mcp add ...             → exit 0 (shouldn't be called here)
	//   claude <prompt>                → record argv to promptLog, exit 0
	claudeStub := `#!/bin/sh
if [ "$1" = "mcp" ] && [ "$2" = "get" ] && [ "$3" = "ezstack" ]; then
  exit 0
fi
if [ "$1" = "mcp" ] && [ "$2" = "add" ]; then
  exit 0
fi
# Final agent launch — capture the prompt (passed as last argv) to a file.
printf '%s' "$*" > "` + promptLog + `"
exit 0
`
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), claudeStub)
	// ezs-mcp must exist on PATH so the binary check passes; it is never executed.
	writeExecutable(t, filepath.Join(env.StubBinDir, "ezs-mcp"),
		"#!/bin/sh\nif [ \"$1\" = \"--version\" ] || [ \"$1\" = \"-v\" ]; then echo \""+version.Version+"\"; exit 0; fi\nexit 0\n")

	out, err := runEzsAgent(t, env, "--branch", "feature-a")
	if err != nil {
		t.Fatalf("ezs agent failed: %v\n%s", err, out)
	}

	logged, err := os.ReadFile(promptLog)
	if err != nil {
		t.Fatalf("stub claude was never invoked with a prompt (log missing): %v\nezs output:\n%s", err, out)
	}
	prompt := string(logged)
	if !strings.Contains(prompt, "ezstack MCP server is registered") {
		t.Errorf("expected MCP stub in prompt sent to claude, got (first 500 chars):\n%s", truncate(prompt, 500))
	}
	if strings.Contains(prompt, "# ezstack Documentation") {
		t.Errorf("expected full DOCUMENTATION.md NOT to be embedded when MCP is active")
	}
}

// TestAgentNoMCPFlag_PromptContainsFullDocs verifies the --no-mcp escape hatch:
// even with claude + ezs-mcp stubbed on PATH, --no-mcp forces the legacy
// doc-paste prompt and skips all claude mcp interaction.
func TestAgentNoMCPFlag_PromptContainsFullDocs(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranchWithCommit(t, env, "feature-a", "main")

	promptLog := filepath.Join(env.TmpDir, "claude_agent_args.txt")
	mcpCallLog := filepath.Join(env.TmpDir, "claude_mcp_calls.txt")

	// Stub records any `mcp` subcommand invocations (should see none when --no-mcp).
	claudeStub := `#!/bin/sh
if [ "$1" = "mcp" ]; then
  echo "$@" >> "` + mcpCallLog + `"
  exit 0
fi
printf '%s' "$*" > "` + promptLog + `"
exit 0
`
	writeExecutable(t, filepath.Join(env.StubBinDir, "claude"), claudeStub)
	writeExecutable(t, filepath.Join(env.StubBinDir, "ezs-mcp"),
		"#!/bin/sh\nif [ \"$1\" = \"--version\" ] || [ \"$1\" = \"-v\" ]; then echo \""+version.Version+"\"; exit 0; fi\nexit 0\n")

	out, err := runEzsAgent(t, env, "--branch", "feature-a", "--no-mcp")
	if err != nil {
		t.Fatalf("ezs agent --no-mcp failed: %v\n%s", err, out)
	}

	// claude mcp get/add must NOT have been called under --no-mcp.
	if _, err := os.Stat(mcpCallLog); err == nil {
		body, _ := os.ReadFile(mcpCallLog)
		t.Errorf("--no-mcp should skip claude mcp interaction, but saw:\n%s", body)
	}

	logged, err := os.ReadFile(promptLog)
	if err != nil {
		t.Fatalf("stub claude was never invoked: %v\nezs output:\n%s", err, out)
	}
	prompt := string(logged)
	if strings.Contains(prompt, "ezstack MCP server is registered") {
		t.Error("--no-mcp should not inject the MCP stub")
	}
	// Full docs should be present — assert on a stable string from
	// DOCUMENTATION.md that appears via EZS_DOCS.
	if !strings.Contains(prompt, "ezs") {
		t.Error("--no-mcp prompt should contain embedded docs content")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
