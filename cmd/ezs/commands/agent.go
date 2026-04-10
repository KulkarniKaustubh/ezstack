package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
	"github.com/spf13/pflag"
)

// Agent launches an AI agent with stack context
func Agent(args []string) error {
	fs := pflag.NewFlagSet("agent", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sLaunch AI agent with stack context%s

%sUSAGE%s
    ezs agent [options]              Launch agent scoped to a stack
    ezs agent feature "description"  Launch agent to build a feature as stacked branches

%sMODES%s
    (default)   Work session — agent is scoped to a stack with full context
    feature     Feature builder — agent breaks a feature into incremental stacked branches

%sOPTIONS%s
    --agent <cmd>        Agent CLI command (default: configured or "claude")
    -s, --stack <hash>   Stack to work on (hash prefix or "name")
    -b, --branch <name>  Branch to work in (implies --stack from branch's stack)
    -h, --help           Show this help message

    If both --stack and --branch are specified, --branch takes priority.
    If neither is specified and you're not on a stacked branch, an interactive
    stack picker is shown.

%sCONFIGURATION%s
    Set default agent: ezs config set agent_command <command>

%sEXAMPLES%s
    %s# Interactive stack selection%s
    ezs agent

    %s# Launch agent on a specific stack by hash prefix%s
    ezs agent -s a1b2c

    %s# Launch agent on a specific stack by name%s
    ezs agent -s "my-feature"

    %s# Launch agent on a specific branch%s
    ezs agent --branch feature-auth

    %s# Build a feature as stacked branches%s
    ezs agent feature "Add user authentication with JWT tokens"
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset,
			ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset)
	}

	agentFlag := fs.String("agent", "", "Agent CLI command (overrides config)")
	stackFlag := fs.StringP("stack", "s", "", "Stack hash prefix or name")
	branchFlag := fs.StringP("branch", "b", "", "Branch to work in")
	helpFlag := fs.BoolP("help", "h", false, "Show help")

	if err := fs.Parse(args); err != nil {
		if err == pflag.ErrHelp {
			return nil
		}
		return err
	}
	if *helpFlag {
		fs.Usage()
		return nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)
	mgr, err := stack.NewReadOnlyManager(cwd)
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	repoPath := getMainWorktreePath(g)

	// Determine agent command
	agentCmd := *agentFlag
	if agentCmd == "" {
		repoCfg := cfg.GetRepoConfig(repoPath)
		agentCmd = repoCfg.GetAgentCommand()
	}

	// Verify agent CLI exists
	if _, err := exec.LookPath(agentCmd); err != nil {
		return fmt.Errorf("agent CLI '%s' not found in PATH.\nInstall it or configure a different agent: ezs config set agent_command <command>", agentCmd)
	}

	// Resolve target stack
	targetStack, err := resolveAgentStack(mgr, *stackFlag, *branchFlag)
	if err != nil {
		return err
	}

	// Validate: stack must have at least one branch
	if len(targetStack.Branches) == 0 {
		return fmt.Errorf("stack '%s' has no branches. Create one with: ezs new <branch-name>", targetStack.DisplayName())
	}

	// Dispatch to mode
	if fs.NArg() > 0 && fs.Arg(0) == "feature" {
		description := strings.Join(fs.Args()[1:], " ")
		if description == "" {
			return fmt.Errorf("feature mode requires a description.\nUsage: ezs agent feature \"description of the feature\"")
		}
		return agentFeature(g, agentCmd, repoPath, targetStack, *branchFlag, description)
	}

	return agentWork(g, agentCmd, repoPath, targetStack, *branchFlag)
}

// resolveAgentStack determines which stack to use for the agent.
// Priority: --branch flag (find its stack) > --stack flag > current branch's stack > interactive selection.
func resolveAgentStack(mgr *stack.Manager, stackRef, branchName string) (*config.Stack, error) {
	stacks := mgr.ListStacks()
	if len(stacks) == 0 {
		return nil, fmt.Errorf("no stacks found. Create one with: ezs new <branch-name>")
	}

	// If --branch is specified, find the stack containing that branch
	if branchName != "" {
		s := mgr.FindStackForBranch(branchName)
		if s == nil {
			return nil, ui.NewExitError(ui.ExitBranchNotFound, "branch '%s' not found in any stack", branchName)
		}
		return s, nil
	}

	// If --stack is specified, resolve by hash prefix or name
	if stackRef != "" {
		return resolveStackByRef(mgr, stacks, stackRef)
	}

	// Try current branch's stack
	currentStack, _, err := mgr.GetCurrentStack()
	if err == nil && currentStack != nil {
		return currentStack, nil
	}

	// Interactive selection
	if len(stacks) == 1 {
		return stacks[0], nil
	}

	return ui.SelectStack(stacks, "Select a stack to work on")
}

// resolveStackByRef resolves a stack reference that could be a hash prefix or a name.
func resolveStackByRef(mgr *stack.Manager, stacks []*config.Stack, ref string) (*config.Stack, error) {
	// Try hash prefix first (existing behavior, requires 3+ chars)
	if len(ref) >= 3 {
		s, err := mgr.GetStackByHash(ref)
		if err == nil {
			return s, nil
		}
	}

	// Try exact name match
	for _, s := range stacks {
		if s.Name == ref {
			return s, nil
		}
	}

	// Try case-insensitive name match
	refLower := strings.ToLower(ref)
	for _, s := range stacks {
		if strings.ToLower(s.Name) == refLower {
			return s, nil
		}
	}

	return nil, fmt.Errorf("no stack found matching '%s' (tried hash prefix and name)", ref)
}

// agentWork launches the agent in work session mode, scoped to a stack.
func agentWork(g *git.Git, agentCmd, repoPath string, targetStack *config.Stack, branchName string) error {
	ctx := buildAgentContext(g, repoPath, targetStack, branchName)

	systemPrompt := buildWorkPrompt(ctx)
	workDir := resolveWorkDir(ctx.worktreePath, repoPath)

	ui.Info(fmt.Sprintf("Launching %s in %s mode on branch '%s'...", agentCmd, ui.Bold+"work"+ui.Reset, ctx.branchName))
	return spawnAgentProcess(agentCmd, workDir, systemPrompt, "")
}

// agentFeature launches the agent in feature builder mode.
func agentFeature(g *git.Git, agentCmd, repoPath string, targetStack *config.Stack, branchName, description string) error {
	ctx := buildAgentContext(g, repoPath, targetStack, branchName)

	systemPrompt := buildFeaturePrompt(ctx)
	workDir := resolveWorkDir(ctx.worktreePath, repoPath)

	initialPrompt := fmt.Sprintf(`Implement the following feature using stacked branches:

%s

Start by exploring the codebase, then present a plan of stacked branches before implementing anything.`, description)

	ui.Info(fmt.Sprintf("Launching %s in %s mode...", agentCmd, ui.Bold+"feature builder"+ui.Reset))
	return spawnAgentProcess(agentCmd, workDir, systemPrompt, initialPrompt)
}

// resolveWorkDir returns the best working directory for the agent, validating it exists.
func resolveWorkDir(worktreePath, repoPath string) string {
	if worktreePath != "" {
		if info, err := os.Stat(worktreePath); err == nil && info.IsDir() {
			return worktreePath
		}
	}
	return repoPath
}

// agentContext holds the resolved context for prompt building.
type agentContext struct {
	branchName   string
	parentName   string
	worktreePath string
	stackJSON    string
	hasStack     bool
	repoPath     string
}

// buildAgentContext resolves the branch and stack state for prompts.
// targetStack must not be nil and must have at least one branch (validated by caller).
func buildAgentContext(g *git.Git, repoPath string, targetStack *config.Stack, branchName string) *agentContext {
	ctx := &agentContext{
		repoPath: repoPath,
		hasStack: true,
		stackJSON: buildStackJSON(targetStack),
	}

	// If explicit branch requested, find it within the target stack
	if branchName != "" {
		for _, b := range targetStack.Branches {
			if b.Name == branchName {
				ctx.branchName = b.Name
				ctx.parentName = b.Parent
				ctx.worktreePath = b.WorktreePath
				return ctx
			}
		}
	}

	// Try current branch if it's in the target stack
	if currentBranch, err := g.CurrentBranch(); err == nil {
		for _, b := range targetStack.Branches {
			if b.Name == currentBranch {
				ctx.branchName = b.Name
				ctx.parentName = b.Parent
				ctx.worktreePath = b.WorktreePath
				return ctx
			}
		}
	}

	// Default to first branch in the stack (guaranteed non-empty by caller)
	first := targetStack.Branches[0]
	ctx.branchName = first.Name
	ctx.parentName = first.Parent
	ctx.worktreePath = first.WorktreePath
	return ctx
}

// stackInfo is a lightweight struct for JSON serialization of stack context.
type stackInfo struct {
	Hash     string       `json:"hash"`
	Name     string       `json:"name,omitempty"`
	Root     string       `json:"root"`
	Branches []branchInfo `json:"branches"`
}

type branchInfo struct {
	Name         string `json:"name"`
	Parent       string `json:"parent"`
	WorktreePath string `json:"worktree_path,omitempty"`
	PRState      string `json:"pr_state,omitempty"`
	IsMerged     bool   `json:"is_merged,omitempty"`
}

func buildStackJSON(s *config.Stack) string {
	si := stackInfo{
		Hash:     s.Hash,
		Name:     s.Name,
		Root:     s.Root,
		Branches: make([]branchInfo, 0, len(s.Branches)),
	}
	for _, b := range s.Branches {
		si.Branches = append(si.Branches, branchInfo{
			Name:         b.Name,
			Parent:       b.Parent,
			WorktreePath: b.WorktreePath,
			PRState:      b.PRState,
			IsMerged:     b.IsMerged,
		})
	}
	data, err := json.MarshalIndent(si, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func buildWorkPrompt(ctx *agentContext) string {
	var sb strings.Builder

	sb.WriteString(`You are working inside an ezstack-managed repository that uses stacked PRs with git worktrees.
`)

	if ctx.hasStack {
		sb.WriteString(fmt.Sprintf(`
## Current Stack
%s
`, ctx.stackJSON))
	}

	sb.WriteString(fmt.Sprintf(`
## Your Branch
Branch: %s`, ctx.branchName))
	if ctx.parentName != "" {
		sb.WriteString(fmt.Sprintf("\nParent: %s", ctx.parentName))
	}
	if ctx.worktreePath != "" {
		sb.WriteString(fmt.Sprintf("\nWorktree: %s", ctx.worktreePath))
	}

	sb.WriteString(`

## ezs Commands (always use -y to skip confirmations)
- ezs -y commit -m "msg" — Commit and auto-sync children
- ezs -y amend — Amend last commit and auto-sync children
- ezs diff — Show diff against parent branch
- ezs -y push — Push current branch
- ezs -y sync -c — Sync current branch with parent
- ezs ls --json — Get current stack state as JSON
- ezs -y new <name> — Create a child branch stacked on current
- ezs -y pr create -t "title" — Create a PR

## Rules
- Only make changes relevant to this branch's purpose.
- Keep changes small and focused for easy review.
- Do not modify files outside this worktree.
- Commit with "ezs commit", not "git commit" (ezs commit auto-syncs children).
`)

	return sb.String()
}

func buildFeaturePrompt(ctx *agentContext) string {
	base := buildWorkPrompt(ctx)

	return base + `
## Feature Builder Mode
You are implementing a feature as a series of small, stacked, reviewable branches.

### Process
1. Explore the codebase to understand the architecture.
2. Plan a series of incremental branches — present the plan to the user FIRST.
3. For each branch after user approves:
   a. Create it: ezs -y new <descriptive-branch-name>
   b. cd to the worktree path printed in the output
   c. Implement the focused change for this branch
   d. Commit: ezs -y commit -m "descriptive message"
   e. Push: ezs -y push
4. After all branches are created, show the final stack with: ezs ls

### Guidelines
- Each branch should be one reviewable unit of work (~100-300 lines of diff is ideal).
- Use descriptive branch names: e.g., "add-user-model", "add-user-api", "add-user-tests".
- Earlier branches must not depend on later ones — each builds on its parent.
- Include tests in the same branch as the code they test, when practical.
`
}

// spawnAgentProcess launches the agent CLI with the given context.
func spawnAgentProcess(agentCmd, workDir, systemPrompt, initialPrompt string) error {
	cmdArgs := []string{"--append-system-prompt", systemPrompt}
	if initialPrompt != "" {
		cmdArgs = append(cmdArgs, initialPrompt)
	}

	cmd := exec.Command(agentCmd, cmdArgs...)
	cmd.Dir = workDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// If the agent exited with a non-zero code, propagate it cleanly
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}
