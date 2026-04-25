package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestMCP_Issue19_SevenChain replays the exact issue #19 repro through the
// MCP in-process client: create seven branches rooted on main (each as its
// own single-branch stack via the "Make this a stack root?" elicitation
// defaulting to yes in non-interactive MCP mode), then chain them with six
// reparent calls. The expectation is a single contiguous stack, not the
// seven fragment stacks the reporter observed.
//
// This test exists because the reporter confirmed the bug surfaced on 4.6.0
// specifically via `ezs-mcp`, so we need an MCP-layer regression test — the
// unit + itest suites only cover the direct-Go call paths.
func TestMCP_Issue19_SevenChain(t *testing.T) {
	repoDir := setupEphemeralRepo(t)

	c := newInitializedClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	branches := []string{"new1", "new2", "new3", "new4", "new5", "new6", "new7"}
	for _, b := range branches {
		res, err := c.CallTool(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "ezstack_new",
				Arguments: map[string]any{
					"name":   b,
					"parent": "main",
				},
			},
		})
		if err != nil {
			t.Fatalf("CallTool(ezstack_new %s): %v", b, err)
		}
		if res.IsError {
			t.Fatalf("ezstack_new %s returned error: %+v", b, res.Content)
		}
	}

	// Sanity: after creation we should have exactly 7 single-branch stacks.
	mgr, err := stack.NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if got := len(mgr.ListStacks()); got != 7 {
		// Dump for diagnosis — the stack shape is what we're verifying.
		for _, s := range mgr.ListStacks() {
			j, _ := json.Marshal(s.Tree)
			t.Logf("  pre-reparent stack %s root=%s tree=%s", s.Hash, s.Root, string(j))
		}
		t.Fatalf("expected 7 stacks before reparent, got %d", got)
	}

	pairs := [][2]string{
		{"new2", "new1"},
		{"new3", "new2"},
		{"new4", "new3"},
		{"new5", "new4"},
		{"new6", "new5"},
		{"new7", "new6"},
	}
	for _, p := range pairs {
		res, err := c.CallTool(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "ezstack_reparent",
				Arguments: map[string]any{
					"branch":     p[0],
					"new_parent": p[1],
				},
			},
		})
		if err != nil {
			t.Fatalf("CallTool(ezstack_reparent %s %s): %v", p[0], p[1], err)
		}
		if res.IsError {
			t.Fatalf("ezstack_reparent %s %s returned error: %+v", p[0], p[1], res.Content)
		}

		// Assert invariant after each reparent: no branch may appear in
		// more than one stack. That's the fingerprint the reporter gave.
		mgr, err := stack.NewManager(repoDir)
		if err != nil {
			t.Fatalf("NewManager after reparent: %v", err)
		}
		counts := map[string]int{}
		for _, s := range mgr.ListStacks() {
			seen := map[string]bool{}
			for _, b := range s.Branches {
				if seen[b.Name] {
					continue
				}
				seen[b.Name] = true
				counts[b.Name]++
			}
		}
		for name, n := range counts {
			if n > 1 {
				for _, s := range mgr.ListStacks() {
					j, _ := json.Marshal(s.Tree)
					t.Logf("  after-reparent stack %s root=%s tree=%s", s.Hash, s.Root, string(j))
				}
				t.Fatalf("after reparent %s->%s: branch %q appears in %d stacks", p[0], p[1], name, n)
			}
		}
	}

	mgr, err = stack.NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager final: %v", err)
	}
	stacks := mgr.ListStacks()
	if len(stacks) != 1 {
		for _, s := range stacks {
			j, _ := json.Marshal(s.Tree)
			t.Logf("  final stack %s root=%s tree=%s", s.Hash, s.Root, string(j))
		}
		t.Fatalf("expected 1 collapsed stack, got %d", len(stacks))
	}
	if len(stacks[0].Branches) != 7 {
		t.Errorf("expected 7 branches in collapsed stack, got %d", len(stacks[0].Branches))
	}
	for i, b := range branches {
		got := mgr.GetBranch(b)
		if got == nil {
			t.Errorf("missing branch %s", b)
			continue
		}
		want := "main"
		if i > 0 {
			want = branches[i-1]
		}
		if got.Parent != want {
			t.Errorf("%s parent = %q, want %q", b, got.Parent, want)
		}
	}
}

// TestMCP_Issue19_TwoStackCanonical reproduces the minimal two-stack scenario
// from the issue body via the MCP layer: Stack A = main→branchA1→branchA2,
// Stack B = main→branchB1. Reparent branchB1 onto branchA2 must collapse
// to one stack and must not duplicate any branch across stacks.
func TestMCP_Issue19_TwoStackCanonical(t *testing.T) {
	repoDir := setupEphemeralRepo(t)

	c := newInitializedClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Stack A = main → branchA1 → branchA2
	callNew(t, c, ctx, "branchA1", "main")
	callNew(t, c, ctx, "branchA2", "branchA1")

	// Stack B = main → branchB1 (separate stack — main isn't a tracked branch
	// so ezs new creates a fresh stack when the elicitation default-yes fires).
	callNew(t, c, ctx, "branchB1", "main")

	mgr, err := stack.NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if got := len(mgr.ListStacks()); got != 2 {
		t.Fatalf("expected 2 stacks before reparent, got %d", got)
	}

	res, err := c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "ezstack_reparent",
			Arguments: map[string]any{
				"branch":     "branchB1",
				"new_parent": "branchA2",
			},
		},
	})
	if err != nil {
		t.Fatalf("CallTool(ezstack_reparent): %v", err)
	}
	if res.IsError {
		t.Fatalf("ezstack_reparent returned error: %+v", res.Content)
	}

	mgr, err = stack.NewManager(repoDir)
	if err != nil {
		t.Fatalf("NewManager after reparent: %v", err)
	}
	stacks := mgr.ListStacks()
	if len(stacks) != 1 {
		for _, s := range stacks {
			j, _ := json.Marshal(s.Tree)
			t.Logf("  stack %s root=%s tree=%s", s.Hash, s.Root, string(j))
		}
		t.Fatalf("expected 1 stack after cross-stack reparent, got %d", len(stacks))
	}

	// No branch may appear in more than one stack.
	counts := map[string]int{}
	for _, s := range stacks {
		seen := map[string]bool{}
		for _, b := range s.Branches {
			if seen[b.Name] {
				continue
			}
			seen[b.Name] = true
			counts[b.Name]++
		}
	}
	for name, n := range counts {
		if n > 1 {
			t.Errorf("branch %q appears in %d stacks", name, n)
		}
	}

	if got := mgr.GetBranch("branchB1"); got == nil || got.Parent != "branchA2" {
		t.Errorf("branchB1 parent = %v, want branchA2", got)
	}
}

func callNew(t *testing.T, c mcpCaller, ctx context.Context, name, parent string) {
	t.Helper()
	res, err := c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "ezstack_new",
			Arguments: map[string]any{
				"name":   name,
				"parent": parent,
			},
		},
	})
	if err != nil {
		t.Fatalf("CallTool(ezstack_new %s): %v", name, err)
	}
	if res.IsError {
		t.Fatalf("ezstack_new %s returned error: %+v", name, res.Content)
	}
}

// mcpCaller captures just the CallTool method shape so helper funcs don't
// hard-code a specific client type.
type mcpCaller interface {
	CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)
}
