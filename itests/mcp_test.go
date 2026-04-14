package itests

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

var (
	buildMCPOnce sync.Once
	mcpBinPath   string
	mcpBuildErr  error
)

// buildMCPBinary compiles cmd/ezs-mcp once per test run into a temp dir and
// returns the absolute path. Shared across all MCP itests so we don't pay the
// compile cost per-test.
func buildMCPBinary(t *testing.T) string {
	t.Helper()
	buildMCPOnce.Do(func() {
		repoRoot, err := findRepoRoot()
		if err != nil {
			mcpBuildErr = err
			return
		}
		outDir, err := os.MkdirTemp("", "ezs-mcp-bin-*")
		if err != nil {
			mcpBuildErr = err
			return
		}
		bin := filepath.Join(outDir, "ezs-mcp")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/ezs-mcp")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			mcpBuildErr = &buildError{msg: string(out), err: err}
			return
		}
		mcpBinPath = bin
	})
	if mcpBuildErr != nil {
		t.Fatalf("build ezs-mcp: %v", mcpBuildErr)
	}
	return mcpBinPath
}

type buildError struct {
	msg string
	err error
}

func (b *buildError) Error() string { return b.err.Error() + ": " + b.msg }

// findRepoRoot walks up from the test working directory until it finds go.mod.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// startMCPStdioClient spawns ezs-mcp as a subprocess with --repo pointed at
// the itest repo and returns an initialized MCP client over stdio. The
// subprocess inherits the test's EZSTACK_HOME so it reads the same config
// SetupTestEnv wrote, and its PATH is augmented with the stub gh so any
// GitHub operations resolve to the stub.
func startMCPStdioClient(t *testing.T, env *TestEnv) *client.Client {
	t.Helper()
	bin := buildMCPBinary(t)

	// mcp-go's stdio transport takes a command + args + env. Passing env as
	// a slice of KEY=VAL overrides the subprocess environment wholesale, so
	// we forward the full current environment plus the itest-specific bits.
	envVars := append(os.Environ(),
		"EZSTACK_HOME="+env.ConfigDir,
		"PATH="+env.StubBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)

	stdio := transport.NewStdio(bin, envVars, "--repo", env.RepoDir)
	c := client.NewClient(stdio)
	t.Cleanup(func() { _ = c.Close() })

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("client.Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "ezs-mcp-itest", Version: "0.0.0"},
		},
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return c
}

// TestMCPServer_ListTools_StdioTransport is the smoke test that the real
// binary boots, honors --repo, and responds to tools/list over stdio. If
// this fails, the server isn't shippable.
func TestMCPServer_ListTools_StdioTransport(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	c := startMCPStdioClient(t, env)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("no tools registered")
	}
	have := map[string]bool{}
	for _, tl := range res.Tools {
		have[tl.Name] = true
	}
	// A small sample of the expected surface — the full schema contract is
	// unit-tested in cmd/ezs-mcp/server_test.go; here we just verify the
	// binary actually registered the tools.
	for _, name := range []string{"ezstack_list", "ezstack_status", "ezstack_sync", "ezstack_new"} {
		if !have[name] {
			t.Errorf("tool %q not registered via stdio transport", name)
		}
	}
}

// TestMCPServer_EzstackList_Empty drives a real tool call through the real
// binary: initializes, then calls ezstack_list against the test repo which
// has no stacks yet. The result should be valid JSON (empty stacks).
func TestMCPServer_EzstackList_Empty(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	c := startMCPStdioClient(t, env)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	res, err := c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "ezstack_list",
			Arguments: map[string]any{"all": true},
		},
	})
	if err != nil {
		t.Fatalf("CallTool(ezstack_list): %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want TextContent", res.Content[0])
	}
	var parsed any
	if err := json.Unmarshal([]byte(text.Text), &parsed); err != nil {
		t.Fatalf("stdout not JSON: %v\npayload: %q", err, text.Text)
	}
}

// TestMCPServer_EzstackList_WithStack creates a real stack via the existing
// itest helpers, then asks the MCP server to list it. Verifies that the
// subprocess actually sees the config SetupTestEnv wrote (i.e. EZSTACK_HOME
// + --repo wiring is correct end to end).
func TestMCPServer_EzstackList_WithStack(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	CreateBranch(t, env, "feature-a", "main")

	c := startMCPStdioClient(t, env)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	res, err := c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "ezstack_list",
			Arguments: map[string]any{"all": true},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T", res.Content[0])
	}
	if !contains(text.Text, "feature-a") {
		t.Errorf("ezstack_list output missing feature-a branch\n---\n%s\n---", text.Text)
	}
}

// TestMCPServer_MissingRequiredArg_Rejected verifies the schema Required
// constraint is enforced — calling ezstack_goto without a branch should be
// rejected before the handler ever runs.
func TestMCPServer_MissingRequiredArg_Rejected(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	c := startMCPStdioClient(t, env)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "ezstack_goto",
			Arguments: map[string]any{},
		},
	})
	// mcp-go may surface required-param failures either as a transport
	// error or as an IsError=true result — accept either, but fail if the
	// call looks successful.
	if err == nil && res != nil && !res.IsError {
		t.Fatalf("expected schema error for missing branch arg; got success: %+v", res.Content)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
