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
	"github.com/KulkarniKaustubh/ezstack/v4/internal/version"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/pflag"
)

func main() {
	// --repo sets the working directory (git repo root). Useful when the MCP
	// server is launched from a parent workspace directory.
	fs := pflag.NewFlagSet("ezs-mcp", pflag.ContinueOnError)
	repo := fs.String("repo", "", "Git repo root directory")
	showVersion := fs.BoolP("version", "v", false, "Print version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "ezs-mcp: %v\n", err)
		os.Exit(2)
	}
	if *showVersion {
		fmt.Println(version.Version)
		return
	}
	if *repo != "" {
		if err := os.Chdir(*repo); err != nil {
			fmt.Fprintf(os.Stderr, "ezs-mcp: --repo %s: %v\n", *repo, err)
			os.Exit(1)
		}
	}

	s := server.NewMCPServer(
		"ezstack",
		version.Version,
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

// tristateBoolFlag appends flagTrue or flagFalse depending on the boolean
// value when key is present in the request arguments. When key is absent,
// nothing is appended — letting the underlying command use its config
// default. Used to expose paired CLI flags like --foo / --no-foo through a
// single tri-state MCP parameter.
func tristateBoolFlag(args *[]string, req mcp.CallToolRequest, key, flagTrue, flagFalse string) {
	raw := req.GetArguments()
	if raw == nil {
		return
	}
	v, ok := raw[key]
	if !ok {
		return
	}
	b, isBool := v.(bool)
	if !isBool {
		return
	}
	if b {
		*args = append(*args, flagTrue)
	} else {
		*args = append(*args, flagFalse)
	}
}

func registerTools(s *server.MCPServer) {
	// ---- Read-only tools ----

	s.AddTool(
		mcp.NewTool("ezstack_status",
			mcp.WithDescription("Show the current stack with PR and CI status for each branch. Returns JSON by default, or decorated terminal output with decorated=true. Use branch to target a specific branch's stack when not on a stack branch (e.g. when on main)."),
			mcp.WithBoolean("all", mcp.Description("Show all stacks, not just the current one")),
			mcp.WithString("branch", mcp.Description("Show status for a specific branch's stack (useful when not on a stack branch)")),
			mcp.WithBoolean("decorated", mcp.Description("Return decorated terminal output (with colors/icons) instead of JSON")),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
		),
		readOnlyHandler(commands.Status, func(req mcp.CallToolRequest) []string {
			var args []string
			boolFlag(&args, req, "all", "--all")
			stringFlag(&args, req, "branch", "--branch")
			return args
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_list",
			mcp.WithDescription("List all stacks and their branches. Returns JSON by default, or decorated terminal output with decorated=true. When running from a non-stack branch (e.g. main), pass all=true to see all stacks."),
			mcp.WithBoolean("all", mcp.Description("Show all stacks (required when not on a stack branch)")),
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
			mcp.WithBoolean("resume", mcp.Description("Resume a sync after the user has resolved rebase conflicts (maps to ezs sync --continue)")),
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
			boolFlag(&args, req, "resume", "--continue")
			return args
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_push",
			mcp.WithDescription("Push current branch or entire stack to remote. Use branch to push a specific branch when not on a stack branch."),
			mcp.WithString("branch", mcp.Description("Push a specific branch by name (useful when not on a stack branch)")),
			mcp.WithBoolean("stack", mcp.Description("Push all branches in the current stack")),
			mcp.WithBoolean("force", mcp.Description("Force push")),
			mcp.WithDestructiveHintAnnotation(true),
		),
		toolHandler(commands.Push, func(req mcp.CallToolRequest) []string {
			var args []string
			stringFlag(&args, req, "branch", "--branch")
			boolFlag(&args, req, "stack", "--stack")
			boolFlag(&args, req, "force", "--force")
			return args
		}),
	)

	// ---- PR tools ----

	s.AddTool(
		mcp.NewTool("ezstack_pr_create",
			mcp.WithDescription("Create a pull request for a branch. Use branch to target a specific branch when not on a stack branch."),
			mcp.WithString("branch", mcp.Description("Branch to create PR for (defaults to current branch)")),
			mcp.WithString("title", mcp.Description("PR title (defaults to branch name)")),
			mcp.WithBoolean("draft", mcp.Description("Create as draft PR")),
			mcp.WithDestructiveHintAnnotation(false),
		),
		toolHandler(commands.PR, func(req mcp.CallToolRequest) []string {
			args := []string{"create"}
			stringFlag(&args, req, "branch", "--branch")
			stringFlag(&args, req, "title", "--title")
			boolFlag(&args, req, "draft", "--draft")
			return args
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_pr_stack",
			mcp.WithDescription("Update all PR descriptions in the stack with navigation links. Use branch to target a specific branch's stack when not on a stack branch."),
			mcp.WithString("branch", mcp.Description("Branch whose stack to update (defaults to current branch's stack)")),
			mcp.WithDestructiveHintAnnotation(false),
		),
		toolHandler(commands.PR, func(req mcp.CallToolRequest) []string {
			args := []string{"stack"}
			stringFlag(&args, req, "branch", "--branch")
			return args
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_pr_merge",
			mcp.WithDescription("Merge the pull request for a branch. Use branch to target a specific branch when not on a stack branch."),
			mcp.WithString("branch", mcp.Description("Branch whose PR to merge (defaults to current branch)")),
			mcp.WithString("method", mcp.Description("Merge method: merge, squash, rebase (default: squash)")),
			mcp.WithDestructiveHintAnnotation(true),
		),
		yesModeHandler(commands.PR, func(req mcp.CallToolRequest) []string {
			args := []string{"merge"}
			stringFlag(&args, req, "branch", "--branch")
			stringFlag(&args, req, "method", "--method")
			return args
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
			mcp.WithBoolean("init_submodules", mcp.Description("Whether to mirror the main worktree's initialized submodules into the new worktree. Omit to use the configured default (true unless overridden).")),
			mcp.WithDestructiveHintAnnotation(false),
		),
		toolHandler(commands.New, func(req mcp.CallToolRequest) []string {
			args := []string{req.GetString("name", "")}
			stringFlag(&args, req, "parent", "--parent")
			tristateBoolFlag(&args, req, "init_submodules", "--init-submodules", "--no-init-submodules")
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

	// ---- Commit & amend ----
	//
	// message is Required for ezstack_commit and --no-edit is forced for
	// ezstack_amend (unless a new message is supplied), because ezs commit
	// shells out via RunInteractive which inherits the parent's stdin. In
	// the MCP server the parent stdin is the JSON-RPC transport, so letting
	// git launch an editor would corrupt the protocol stream.
	//
	// Both handlers use yesModeHandler so the internal "Push to remote?"
	// confirmation (ConfirmTUIWithDefault) is auto-accepted — the MCP
	// client has no way to answer a stdin prompt. Commit will therefore
	// push automatically when the branch already exists on the remote;
	// agents that want a commit-without-push should use ezstack_commit
	// on branches that have not been pushed yet.

	s.AddTool(
		mcp.NewTool("ezstack_commit",
			mcp.WithDescription("Commit staged (or all) changes and auto-sync child branches. Auto-pushes when the branch already exists on the remote. Refuses to open an editor: message is required."),
			mcp.WithString("message",
				mcp.Description("Commit message — passed directly to git commit -m"),
				mcp.Required(),
			),
			mcp.WithBoolean("all", mcp.Description("Stage all tracked modified files (git commit -a)")),
			mcp.WithBoolean("merge", mcp.Description("Use merge instead of rebase when syncing children")),
			mcp.WithBoolean("rebase", mcp.Description("Use rebase instead of merge when syncing children")),
			mcp.WithDestructiveHintAnnotation(true),
		),
		yesModeHandler(commands.Commit, func(req mcp.CallToolRequest) []string {
			var args []string
			boolFlag(&args, req, "all", "-a")
			args = append(args, "-m", req.GetString("message", ""))
			boolFlag(&args, req, "merge", "--merge")
			boolFlag(&args, req, "rebase", "--rebase")
			return args
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_amend",
			mcp.WithDescription("Amend the last commit and auto-sync child branches. Force-pushes when the branch already exists on the remote (amend rewrites history). If message is omitted, --no-edit is forced to avoid launching an editor."),
			mcp.WithString("message", mcp.Description("New commit message. If omitted, the existing message is preserved via --no-edit.")),
			mcp.WithBoolean("all", mcp.Description("Stage all tracked modified files before amending (git commit -a)")),
			mcp.WithBoolean("merge", mcp.Description("Use merge instead of rebase when syncing children")),
			mcp.WithBoolean("rebase", mcp.Description("Use rebase instead of merge when syncing children")),
			mcp.WithDestructiveHintAnnotation(true),
		),
		yesModeHandler(commands.Amend, func(req mcp.CallToolRequest) []string {
			var args []string
			boolFlag(&args, req, "all", "-a")
			if msg := req.GetString("message", ""); msg != "" {
				args = append(args, "-m", msg)
			} else {
				args = append(args, "--no-edit")
			}
			boolFlag(&args, req, "merge", "--merge")
			boolFlag(&args, req, "rebase", "--rebase")
			return args
		}),
	)

	// ---- Inspection ----

	s.AddTool(
		mcp.NewTool("ezstack_diff",
			mcp.WithDescription("Show diff between a branch and its parent in the stack. Returns structured JSON numstat (file list with additions/deletions and totals) by default, or a human-readable diffstat summary with stat=true."),
			mcp.WithString("branch", mcp.Description("Branch to diff (defaults to current)")),
			mcp.WithBoolean("stat", mcp.Description("Return a diffstat summary instead of the JSON numstat view")),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
		),
		// Explicit toolHandler (not readOnlyHandler) because readOnlyHandler
		// unconditionally appends --json, which would override --stat inside
		// commands.Diff and silently return JSON when the caller asked for
		// stat output.
		toolHandler(commands.Diff, func(req mcp.CallToolRequest) []string {
			var args []string
			stringFlag(&args, req, "branch", "--branch")
			if req.GetBool("stat", false) {
				args = append(args, "--stat")
			} else {
				args = append(args, "--json")
			}
			return args
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_log",
			mcp.WithDescription("Show commits in a branch since its parent. Always returns JSON (hash, message, author, ISO date, count)."),
			mcp.WithString("branch", mcp.Description("Branch to show commits for (defaults to current)")),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
		),
		// toolHandler + explicit --json: we always want JSON from Log, and
		// keeping the flag in buildArgs makes the contract explicit in one
		// place rather than depending on readOnlyHandler's injection.
		toolHandler(commands.Log, func(req mcp.CallToolRequest) []string {
			args := []string{"--json"}
			stringFlag(&args, req, "branch", "--branch")
			return args
		}),
	)

	// ---- Additional PR operations ----

	s.AddTool(
		mcp.NewTool("ezstack_pr_update",
			mcp.WithDescription("Push the latest commits and update the PR base branch and stack description to match the current stack structure."),
			mcp.WithString("branch", mcp.Description("Branch whose PR to update (defaults to current)")),
			mcp.WithDestructiveHintAnnotation(true),
		),
		yesModeHandler(commands.PR, func(req mcp.CallToolRequest) []string {
			args := []string{"update"}
			stringFlag(&args, req, "branch", "--branch")
			return args
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_pr_draft",
			mcp.WithDescription("Toggle a pull request between draft and ready-for-review."),
			mcp.WithString("branch", mcp.Description("Branch whose PR to toggle (defaults to current)")),
			mcp.WithDestructiveHintAnnotation(false),
		),
		yesModeHandler(commands.PR, func(req mcp.CallToolRequest) []string {
			args := []string{"draft"}
			stringFlag(&args, req, "branch", "--branch")
			return args
		}),
	)

	// ---- Stack membership ----

	s.AddTool(
		mcp.NewTool("ezstack_stack",
			mcp.WithDescription("Add a branch to a stack by setting its parent. Pass either parent (to attach under an existing branch) or base (to start a new stack rooted on a non-default base branch like develop)."),
			mcp.WithString("branch",
				mcp.Description("Branch to add to the stack"),
				mcp.Required(),
			),
			mcp.WithString("parent", mcp.Description("Parent branch in an existing stack")),
			mcp.WithString("base", mcp.Description("Base branch for a new stack (mutually exclusive with parent)")),
			mcp.WithDestructiveHintAnnotation(false),
		),
		yesModeHandler(commands.Stack, func(req mcp.CallToolRequest) []string {
			args := []string{"--branch", req.GetString("branch", "")}
			stringFlag(&args, req, "parent", "--parent")
			stringFlag(&args, req, "base", "--base")
			return args
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_unstack",
			mcp.WithDescription("Remove a branch from ezstack tracking without deleting the git branch or worktree. Children are reparented to the untracked branch's parent."),
			mcp.WithString("branch",
				mcp.Description("Branch to untrack"),
				mcp.Required(),
			),
			mcp.WithDestructiveHintAnnotation(false),
		),
		yesModeHandler(commands.Unstack, func(req mcp.CallToolRequest) []string {
			return []string{"--branch", req.GetString("branch", "")}
		}),
	)

	// ---- Configuration ----

	s.AddTool(
		mcp.NewTool("ezstack_config_show",
			mcp.WithDescription("Show the current ezstack configuration, including the config directory, global settings, and per-repo settings for the active repository."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
		),
		// toolHandler (not readOnlyHandler) because `ezs config` has no
		// --json mode — readOnlyHandler would inject `--json`, which the
		// strict Config arg parser now rejects as an unknown subcommand.
		toolHandler(commands.Config, func(req mcp.CallToolRequest) []string {
			return []string{"show"}
		}),
	)

	s.AddTool(
		mcp.NewTool("ezstack_config_set",
			mcp.WithDescription("Set an ezstack configuration value. Valid keys: worktree_base_dir, default_base_branch, github_token, cd_after_new, use_worktrees, sync_strategy, agent_command."),
			mcp.WithString("key",
				mcp.Description("Config key to set"),
				mcp.Required(),
			),
			mcp.WithString("value",
				mcp.Description("New value for the key"),
				mcp.Required(),
			),
			mcp.WithDestructiveHintAnnotation(false),
		),
		toolHandler(commands.Config, func(req mcp.CallToolRequest) []string {
			return []string{
				"set",
				req.GetString("key", ""),
				req.GetString("value", ""),
			}
		}),
	)
}
