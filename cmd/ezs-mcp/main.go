package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs/commands"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/pflag"
)

// version is kept in lock-step with cmd/ezs/main.go via scripts/bump-version.sh.
const version = "4.3.5"

func main() {
	// --repo sets the working directory (git repo root). Useful when the MCP
	// server is launched from a parent workspace directory.
	fs := pflag.NewFlagSet("ezs-mcp", pflag.ContinueOnError)
	repo := fs.String("repo", "", "Git repo root directory")
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "ezs-mcp: %v\n", err)
		os.Exit(2)
	}
	if *repo != "" {
		if err := os.Chdir(*repo); err != nil {
			fmt.Fprintf(os.Stderr, "ezs-mcp: --repo %s: %v\n", *repo, err)
			os.Exit(1)
		}
	}

	s := server.NewMCPServer(
		"ezstack",
		version,
		server.WithElicitation(),
		// Tool list is static, so don't advertise listChanged notifications.
		server.WithToolCapabilities(false),
	)

	registerTools(s)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "ezs-mcp: %v\n", err)
		os.Exit(1)
	}
}

// toolMu serializes tool-handler execution. It is REQUIRED because two
// independent process-global mutations happen per handler:
//
//  1. captureCommand swaps os.Stdout / os.Stderr to pipes
//  2. setupElicitation replaces ui.activeBackend with a request-scoped closure
//
// mcp-go dispatches tool calls on separate goroutines, so without this lock
// two concurrent requests would race on both globals — one request's output
// could land in the other's pipe (or trigger a write-after-close panic) and
// elicitation callbacks would be dispatched against the wrong request context.
//
// Serializing tool calls is acceptable for a single-user MCP server.
var toolMu sync.Mutex

// setupElicitation configures the MCP UI backend for the current request context.
// Must be called at the start of each tool handler while holding toolMu.
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
			content, ok := result.Content.(map[string]interface{})
			if !ok && result.Content != nil {
				// Spec guarantees an object here; log so we can diagnose
				// clients that send something else instead of silently
				// producing zero-value form data downstream.
				fmt.Fprintf(os.Stderr, "ezs-mcp: unexpected elicitation content type: %T\n", result.Content)
			}
			return &ui.ElicitResult{
				Action:  string(result.Action),
				Content: content,
			}, nil
		},
	})
}

// captureCommand runs fn(args) with stdout/stderr redirected to pipes,
// returning the raw captured output from each stream. Caller MUST hold toolMu
// — this mutates process-global os.Stdout / os.Stderr.
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

	// Drain pipes concurrently so that commands writing more than the pipe
	// buffer (~64KB on most systems) don't block on the write side before we
	// get a chance to read.
	var outBuf, errBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(&outBuf, rOut) }()
	go func() { defer wg.Done(); io.Copy(&errBuf, rErr) }()

	os.Stdout = wOut
	os.Stderr = wErr
	cmdErr = fn(args)
	wOut.Close()
	wErr.Close()
	os.Stdout = origStdout
	os.Stderr = origStderr

	wg.Wait()
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
		toolMu.Lock()
		defer toolMu.Unlock()

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
			// ezstack commands write visual output to stderr (stdout is
			// reserved for shell-eval like "cd <path>" emitted by EmitCd),
			// so decorated mode returns stderr content.
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
		toolMu.Lock()
		defer toolMu.Unlock()

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

// yesModeHandler is toolHandler for destructive commands that would otherwise
// block on an internal confirmation prompt. It flips ui.YesMode for the
// duration of the call (safe under toolMu since it's process-global). The
// MCP client layer is responsible for user-facing confirmation via the
// destructive annotation.
func yesModeHandler(fn func([]string) error, buildArgs func(mcp.CallToolRequest) []string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		toolMu.Lock()
		defer toolMu.Unlock()

		setupElicitation(ctx)
		prev := ui.YesMode
		ui.YesMode = true
		defer func() { ui.YesMode = prev }()

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

// stringFlag appends "--flag value" to args when the request has a non-empty string.
func stringFlag(args *[]string, req mcp.CallToolRequest, key, flag string) {
	if v := req.GetString(key, ""); v != "" {
		*args = append(*args, flag, v)
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
			mcp.WithDescription("Sync stack branches with remote. Detects merged parents and rebases branches behind their base. Rewrites local history — on conflict, manual resolution may be required."),
			// Sync rewrites local branch history (rebase) and may leave
			// the working tree in a conflict state requiring manual
			// resolution, so we mark it destructive.
			mcp.WithDestructiveHintAnnotation(true),
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
			mcp.WithString("title", mcp.Description("PR title (defaults to branch name)")),
			mcp.WithBoolean("draft", mcp.Description("Create as draft PR")),
			mcp.WithDestructiveHintAnnotation(false),
		),
		toolHandler(commands.PR, func(req mcp.CallToolRequest) []string {
			args := []string{"create"}
			stringFlag(&args, req, "title", "--title")
			boolFlag(&args, req, "draft", "--draft")
			return args
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
	//
	// For branch-management tools, positional-arg parameters are marked
	// Required() so the commands never fall through to their interactive
	// fzf-backed selection paths — those would hang or error in an MCP
	// context with no attached terminal.

	s.AddTool(
		mcp.NewTool("ezstack_goto",
			mcp.WithDescription("Switch to a branch in the stack."),
			mcp.WithString("branch",
				mcp.Description("Branch name to switch to"),
				mcp.Required(),
			),
			mcp.WithDestructiveHintAnnotation(false),
		),
		toolHandler(commands.Goto, func(req mcp.CallToolRequest) []string {
			return []string{req.GetString("branch", "")}
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_new",
			mcp.WithDescription("Create a new branch in the stack. Without an explicit parent, the new branch stacks on the MCP server's current branch, which may not match the agent's expectation — pass parent explicitly when in doubt."),
			mcp.WithString("name",
				mcp.Description("Branch name to create"),
				mcp.Required(),
			),
			mcp.WithString("parent", mcp.Description("Parent branch (defaults to current branch)")),
			mcp.WithDestructiveHintAnnotation(false),
		),
		toolHandler(commands.New, func(req mcp.CallToolRequest) []string {
			args := []string{req.GetString("name", "")}
			stringFlag(&args, req, "parent", "--parent")
			return args
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_delete",
			mcp.WithDescription("Delete a branch from the stack. Confirmation is handled at the MCP client layer via the destructive annotation; internal confirm prompts are auto-accepted."),
			mcp.WithString("branch",
				mcp.Description("Branch name to delete"),
				mcp.Required(),
			),
			mcp.WithDestructiveHintAnnotation(true),
		),
		yesModeHandler(commands.Delete, func(req mcp.CallToolRequest) []string {
			return []string{req.GetString("branch", "")}
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_reparent",
			mcp.WithDescription("Move a branch to a new parent in the stack."),
			mcp.WithString("branch",
				mcp.Description("Branch to reparent"),
				mcp.Required(),
			),
			mcp.WithString("new_parent",
				mcp.Description("New parent branch"),
				mcp.Required(),
			),
			mcp.WithDestructiveHintAnnotation(false),
		),
		toolHandler(commands.Reparent, func(req mcp.CallToolRequest) []string {
			return []string{
				req.GetString("branch", ""),
				req.GetString("new_parent", ""),
			}
		}),
	)
}
