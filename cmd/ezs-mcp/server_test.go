package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// newTestServer builds an MCP server with the same tool registration used in
// main(), suitable for in-process client tests. Mirrors main() verbatim — if
// they drift, this helper should drift with main().
func newTestServer() *server.MCPServer {
	s := server.NewMCPServer(
		"ezstack",
		version,
		server.WithElicitation(),
		server.WithToolCapabilities(false),
	)
	registerTools(s)
	return s
}

func newInitializedClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.NewInProcessClient(newTestServer())
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("client.Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "ezs-mcp-test",
				Version: "0.0.0",
			},
		},
	}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return c
}

// TestListTools_Registered asserts that every expected tool is registered
// with the right shape — required params, destructive annotations, and the
// boolean flags the tool's args-builder consumes. This is the schema-level
// contract that the MCP client (Claude) sees.
func TestListTools_Registered(t *testing.T) {
	c := newInitializedClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	byName := map[string]mcp.Tool{}
	for _, tl := range res.Tools {
		byName[tl.Name] = tl
	}

	want := []string{
		"ezstack_status",
		"ezstack_list",
		"ezstack_sync",
		"ezstack_push",
		"ezstack_pr_create",
		"ezstack_pr_stack",
		"ezstack_pr_merge",
		"ezstack_goto",
		"ezstack_new",
		"ezstack_delete",
		"ezstack_reparent",
	}
	sort.Strings(want)
	var got []string
	for n := range byName {
		got = append(got, n)
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Errorf("tool count = %d, want %d\n got:  %v\n want: %v", len(got), len(want), got, want)
	}
	for _, n := range want {
		if _, ok := byName[n]; !ok {
			t.Errorf("missing tool: %s", n)
		}
	}

	// Required params must be present so the commands never fall through to
	// their fzf-backed interactive paths (which hang in MCP context).
	requiredChecks := []struct {
		tool  string
		field string
	}{
		{"ezstack_goto", "branch"},
		{"ezstack_new", "name"},
		{"ezstack_delete", "branch"},
		{"ezstack_reparent", "branch"},
		{"ezstack_reparent", "new_parent"},
	}
	for _, rc := range requiredChecks {
		tl, ok := byName[rc.tool]
		if !ok {
			continue // already reported above
		}
		found := false
		for _, r := range tl.InputSchema.Required {
			if r == rc.field {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tool %q: field %q is not Required (schema.Required = %v)",
				rc.tool, rc.field, tl.InputSchema.Required)
		}
	}

	// Destructive annotation — sync rewrites history and must be flagged.
	destructiveWant := map[string]bool{
		"ezstack_status":    false,
		"ezstack_list":      false,
		"ezstack_sync":      true,
		"ezstack_push":      true,
		"ezstack_pr_create": false,
		"ezstack_pr_stack":  false,
		"ezstack_pr_merge":  true,
		"ezstack_goto":      false,
		"ezstack_new":       false,
		"ezstack_delete":    true,
		"ezstack_reparent":  false,
	}
	for name, want := range destructiveWant {
		tl, ok := byName[name]
		if !ok {
			continue
		}
		// Annotations use a pointer-ish optional; mcp-go exposes as
		// *bool or bool via Annotations.DestructiveHint.
		got := false
		if tl.Annotations.DestructiveHint != nil {
			got = *tl.Annotations.DestructiveHint
		}
		if got != want {
			t.Errorf("tool %q: DestructiveHint = %v, want %v", name, got, want)
		}
	}
}

// TestListTools_PrCreateHasTitleAndDraft is the regression test for a review
// comment that the PR create tool should expose title + draft params so
// agents can drive creation non-interactively.
func TestListTools_PrCreateHasTitleAndDraft(t *testing.T) {
	c := newInitializedClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var pr *mcp.Tool
	for i := range res.Tools {
		if res.Tools[i].Name == "ezstack_pr_create" {
			pr = &res.Tools[i]
			break
		}
	}
	if pr == nil {
		t.Fatal("ezstack_pr_create not registered")
	}
	for _, field := range []string{"title", "draft"} {
		if _, ok := pr.InputSchema.Properties[field]; !ok {
			t.Errorf("ezstack_pr_create missing %q param", field)
		}
	}
}

// TestCallTool_List drives a real read-only tool call end-to-end: spin up an
// ephemeral git repo + ezstack config, chdir into it, invoke ezstack_list via
// the in-process client, and assert the JSON response shape. This is the
// narrowest end-to-end test that exercises the full pipeline:
// client → transport → server → registered handler → captureCommand → commands.List.
func TestCallTool_List(t *testing.T) {
	env := setupEphemeralRepo(t)

	c := newInitializedClient(t)
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
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty content")
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want TextContent", res.Content[0])
	}
	// Default mode (decorated=false) appends --json, so the payload should
	// parse as JSON. We don't pin the exact schema (that's commands.List's
	// contract, not the MCP layer's), just that we got well-formed JSON
	// from the command pipeline.
	var parsed any
	if err := json.Unmarshal([]byte(text.Text), &parsed); err != nil {
		t.Fatalf("stdout is not JSON: %v\npayload: %q", err, text.Text)
	}
	_ = env
}

// TestCallTool_ListDecorated verifies the decorated=true path returns the
// ANSI-laden stderr fallback instead of JSON.
func TestCallTool_ListDecorated(t *testing.T) {
	setupEphemeralRepo(t)

	c := newInitializedClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	res, err := c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "ezstack_list",
			Arguments: map[string]any{"decorated": true},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want TextContent", res.Content[0])
	}
	// Decorated output is human-readable stderr — must NOT be JSON, and
	// must contain at least one ANSI escape to prove the ANSI codes are
	// passed through (unlike the default path which strips them).
	var parsed any
	if json.Unmarshal([]byte(text.Text), &parsed) == nil {
		t.Errorf("decorated output parsed as JSON (expected ANSI text): %q", text.Text)
	}
	if !containsRaw(text.Text, "\x1b[") {
		t.Errorf("decorated output has no ANSI escapes: %q", text.Text)
	}
}

// setupEphemeralRepo builds a minimal git repo + ezstack config in a temp
// dir, chdirs into it, and arranges cleanup. Kept self-contained (no
// itests import) so that the ezs-mcp test binary has no test-package deps.
func setupEphemeralRepo(t *testing.T) string {
	t.Helper()

	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)
	repoDir := filepath.Join(tmp, "repo")
	wtDir := filepath.Join(tmp, "worktrees")
	cfgDir := filepath.Join(tmp, "config")
	for _, d := range []string{repoDir, wtDir, cfgDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	origHome := os.Getenv("EZSTACK_HOME")
	os.Setenv("EZSTACK_HOME", cfgDir)
	origWd, _ := os.Getwd()
	t.Cleanup(func() {
		os.Setenv("EZSTACK_HOME", origHome)
		_ = os.Chdir(origWd)
	})

	gitCmds := [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	}
	for _, c := range gitCmds {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, c...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", c, err, out)
		}
	}
	readme := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readme, []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{{"add", "."}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, c...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", c, err, out)
		}
	}

	// Minimal ezstack config: point at this repo's worktree base dir.
	cfgPath := filepath.Join(cfgDir, "config.json")
	cfgJSON := `{
	  "default_base_branch": "main",
	  "repos": {
	    "` + repoDir + `": { "worktree_base_dir": "` + wtDir + `" }
	  }
	}`
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return repoDir
}

func containsRaw(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
