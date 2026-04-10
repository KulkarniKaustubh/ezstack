package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	// Optional --repo flag to set the working directory (git repo root).
	// Useful when the MCP server is launched from a parent workspace directory.
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "--repo" && i+1 < len(os.Args) {
			if err := os.Chdir(os.Args[i+1]); err != nil {
				fmt.Fprintf(os.Stderr, "ezs-mcp: --repo %s: %v\n", os.Args[i+1], err)
				os.Exit(1)
			}
			break
		}
	}

	s := server.NewMCPServer(
		"ezstack",
		"1.0.0",
		server.WithElicitation(),
		server.WithToolCapabilities(false),
	)

	registerTools(s)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "ezs-mcp: %v\n", err)
		os.Exit(1)
	}
}

// setupElicitation configures the MCP UI backend for the current request context.
// Must be called at the start of each tool handler.
func setupElicitation(ctx context.Context) {
	session := server.ClientSessionFromContext(ctx)
	elicitSession, hasElicit := session.(server.SessionWithElicitation)

	ui.SetBackend(&ui.MCPBackend{
		Elicit: func(message string, schema map[string]interface{}) (*ui.ElicitResult, error) {
			if !hasElicit {
				return &ui.ElicitResult{Action: "decline"}, nil
			}
			result, err := elicitSession.RequestElicitation(ctx, mcp.ElicitationRequest{
				Params: mcp.ElicitationParams{
					Message:         message,
					RequestedSchema: schema,
				},
			})
			if err != nil {
				return nil, err
			}
			content, _ := result.Content.(map[string]interface{})
			return &ui.ElicitResult{
				Action:  string(result.Action),
				Content: content,
			}, nil
		},
	})
}

// captureCommand runs fn(args) with stdout/stderr redirected to pipes,
// returning the raw captured output from each stream.
func captureCommand(fn func([]string) error, args []string) (stdout, stderr string, cmdErr error) {
	origStdout, origStderr := os.Stdout, os.Stderr

	rOut, wOut, err := os.Pipe()
	if err != nil {
		return "", "", err
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		rOut.Close()
		wOut.Close()
		return "", "", err
	}

	os.Stdout = wOut
	os.Stderr = wErr
	cmdErr = fn(args)
	wOut.Close()
	wErr.Close()
	os.Stdout = origStdout
	os.Stderr = origStderr

	var outBuf, errBuf bytes.Buffer
	io.Copy(&outBuf, rOut)
	io.Copy(&errBuf, rErr)
	rOut.Close()
	rErr.Close()

	return strings.TrimSpace(outBuf.String()), strings.TrimSpace(errBuf.String()), cmdErr
}

// runCommand is a convenience wrapper: prefers JSON stdout, falls back to ANSI-stripped stderr.
func runCommand(fn func([]string) error, args []string) (string, error) {
	stdout, stderr, err := captureCommand(fn, args)
	output := stdout
	if output == "" {
		output = stripAnsi(stderr)
	}
	if output == "" && err != nil {
		output = err.Error()
	}
	return output, err
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\]8;;[^\x1b]*\x1b\\|[\x{e000}-\x{f8ff}]`)
var multiSpace = regexp.MustCompile(`  +`)

func stripAnsi(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	return multiSpace.ReplaceAllString(s, " ")
}

// readOnlyHandler wraps a read-only command that supports --json.
// When the request has "decorated: true", runs without --json and returns
// raw terminal output (ANSI codes intact) for display in the user's terminal.
// Otherwise appends --json and returns structured data.
func readOnlyHandler(fn func([]string) error, buildArgs func(mcp.CallToolRequest) []string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		setupElicitation(ctx)
		args := buildArgs(req)
		decorated := req.GetBool("decorated", false)

		if !decorated {
			args = append(args, "--json")
		}

		stdout, stderr, err := captureCommand(fn, args)
		if err != nil {
			return mcp.NewToolResultError(stripAnsi(stderr)), nil
		}

		if decorated {
			if stderr == "" {
				stderr = "done"
			}
			return mcp.NewToolResultText(stderr), nil
		}

		output := stdout
		if output == "" {
			output = stripAnsi(stderr)
		}
		if output == "" {
			output = "done"
		}
		return mcp.NewToolResultText(output), nil
	}
}

// toolHandler wraps a command function as an MCP tool handler.
func toolHandler(fn func([]string) error, buildArgs func(mcp.CallToolRequest) []string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		setupElicitation(ctx)
		output, err := runCommand(fn, buildArgs(req))
		if err != nil {
			msg := output
			if msg == "" {
				msg = err.Error()
			}
			return mcp.NewToolResultError(msg), nil
		}
		if output == "" {
			output = "done"
		}
		return mcp.NewToolResultText(output), nil
	}
}

// boolFlag appends --flag to args when the request has a truthy boolean.
func boolFlag(args *[]string, req mcp.CallToolRequest, key, flag string) {
	if req.GetBool(key, false) {
		*args = append(*args, flag)
	}
}

func registerTools(s *server.MCPServer) {
	// ---- Read-only tools ----

	s.AddTool(
		mcp.NewTool("ezstack_status",
			mcp.WithDescription("Show the current stack with PR and CI status for each branch. Returns JSON by default, or decorated terminal output with decorated=true."),
			mcp.WithBoolean("all", mcp.Description("Show all stacks, not just the current one")),
			mcp.WithBoolean("decorated", mcp.Description("Return decorated terminal output (with colors/icons) instead of JSON")),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
		),
		readOnlyHandler(commands.Status, func(req mcp.CallToolRequest) []string {
			var args []string
			boolFlag(&args, req, "all", "--all")
			return args
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_list",
			mcp.WithDescription("List all stacks and their branches. Returns JSON by default, or decorated terminal output with decorated=true."),
			mcp.WithBoolean("all", mcp.Description("Show all stacks")),
			mcp.WithBoolean("decorated", mcp.Description("Return decorated terminal output (with colors/icons) instead of JSON")),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
		),
		readOnlyHandler(commands.List, func(req mcp.CallToolRequest) []string {
			var args []string
			boolFlag(&args, req, "all", "--all")
			return args
		}),
	)

	// ---- Sync & push ----

	s.AddTool(
		mcp.NewTool("ezstack_sync",
			mcp.WithDescription("Sync stack branches with remote. Detects merged parents, rebases branches behind base. Without flags shows interactive selection."),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithBoolean("stack", mcp.Description("Sync current stack (auto-detect)")),
			mcp.WithBoolean("all", mcp.Description("Sync all stacks")),
			mcp.WithBoolean("current", mcp.Description("Sync current branch only")),
			mcp.WithBoolean("parent", mcp.Description("Rebase current branch onto parent")),
			mcp.WithBoolean("children", mcp.Description("Rebase children onto current branch")),
			mcp.WithBoolean("merge", mcp.Description("Use merge instead of rebase")),
			mcp.WithBoolean("dry_run", mcp.Description("Preview what would be synced without making changes")),
		),
		toolHandler(commands.Sync, func(req mcp.CallToolRequest) []string {
			var args []string
			boolFlag(&args, req, "stack", "--stack")
			boolFlag(&args, req, "all", "--all")
			boolFlag(&args, req, "current", "--current")
			boolFlag(&args, req, "parent", "--parent")
			boolFlag(&args, req, "children", "--children")
			boolFlag(&args, req, "merge", "--merge")
			boolFlag(&args, req, "dry_run", "--dry-run")
			return args
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_push",
			mcp.WithDescription("Push current branch or entire stack to remote."),
			mcp.WithBoolean("stack", mcp.Description("Push all branches in the current stack")),
			mcp.WithBoolean("force", mcp.Description("Force push")),
			mcp.WithDestructiveHintAnnotation(true),
		),
		toolHandler(commands.Push, func(req mcp.CallToolRequest) []string {
			var args []string
			boolFlag(&args, req, "stack", "--stack")
			boolFlag(&args, req, "force", "--force")
			return args
		}),
	)

	// ---- PR tools ----

	s.AddTool(
		mcp.NewTool("ezstack_pr_create",
			mcp.WithDescription("Create a pull request for the current branch."),
			mcp.WithDestructiveHintAnnotation(false),
		),
		toolHandler(commands.PR, func(req mcp.CallToolRequest) []string {
			return []string{"create"}
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_pr_stack",
			mcp.WithDescription("Update all PR descriptions in the stack with navigation links."),
			mcp.WithDestructiveHintAnnotation(false),
		),
		toolHandler(commands.PR, func(req mcp.CallToolRequest) []string {
			return []string{"stack"}
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_pr_merge",
			mcp.WithDescription("Merge the pull request for the current branch."),
			mcp.WithDestructiveHintAnnotation(true),
		),
		toolHandler(commands.PR, func(req mcp.CallToolRequest) []string {
			return []string{"merge"}
		}),
	)

	// ---- Branch management ----

	s.AddTool(
		mcp.NewTool("ezstack_goto",
			mcp.WithDescription("Switch to a branch in the stack. Omit branch for interactive selection."),
			mcp.WithString("branch", mcp.Description("Branch name to switch to")),
			mcp.WithDestructiveHintAnnotation(false),
		),
		toolHandler(commands.Goto, func(req mcp.CallToolRequest) []string {
			if b := req.GetString("branch", ""); b != "" {
				return []string{b}
			}
			return nil
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_new",
			mcp.WithDescription("Create a new branch in the stack."),
			mcp.WithString("name", mcp.Description("Branch name to create")),
			mcp.WithDestructiveHintAnnotation(false),
		),
		toolHandler(commands.New, func(req mcp.CallToolRequest) []string {
			if n := req.GetString("name", ""); n != "" {
				return []string{n}
			}
			return nil
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_delete",
			mcp.WithDescription("Delete a branch from the stack."),
			mcp.WithString("branch", mcp.Description("Branch name to delete")),
			mcp.WithDestructiveHintAnnotation(true),
		),
		toolHandler(commands.Delete, func(req mcp.CallToolRequest) []string {
			if b := req.GetString("branch", ""); b != "" {
				return []string{b}
			}
			return nil
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_reparent",
			mcp.WithDescription("Move a branch to a new parent in the stack."),
			mcp.WithDestructiveHintAnnotation(false),
		),
		toolHandler(commands.Reparent, func(req mcp.CallToolRequest) []string {
			return nil
		}),
	)
}
