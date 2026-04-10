package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
	"github.com/spf13/pflag"
)

const (
	workPromptFilename    = "agent-work-prompt.md"
	featurePromptFilename = "agent-feature-prompt.md"
)

// Agent launches an AI agent with stack context
func Agent(args []string) error {
	fs := pflag.NewFlagSet("agent", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sLaunch AI agent with stack context%s

%sUSAGE%s
    ezs agent [options]              Launch agent scoped to a stack
    ezs agent feature "description"  Launch agent to build a feature as stacked branches
    ezs agent prompt [options]       View or edit agent prompt templates

%sMODES%s
    (default)   Work session — agent is scoped to a stack with full context
    feature     Feature builder — agent breaks a feature into incremental stacked branches
    prompt      View or edit the prompt templates used by the agent

%sOPTIONS%s
    --cmd <command>      Agent CLI to use (default: configured or "claude")
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

    %s# View both prompt templates%s
    ezs agent prompt

    %s# Edit the work session prompt%s
    ezs agent prompt --edit --work

    %s# Edit the feature builder prompt%s
    ezs agent prompt --edit --feature
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset,
			ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset,
			ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset)
	}

	// Check for prompt subcommand early — before parsing agent flags,
	// so that prompt-specific flags (--edit, --work, --feature, --reset)
	// are not rejected by the agent flag set.
	// Only match "prompt" as a positional arg (not a flag value like -s prompt).
	if sub, rest := firstPositionalArg(args); sub == "prompt" {
		return agentPrompt(rest)
	}

	cmdFlag := fs.String("cmd", "", "Agent CLI to use (overrides config)")
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
	agentCmd := *cmdFlag
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

// ── Prompt subcommand ──────────────────────────────────────────────────────────

func agentPrompt(args []string) error {
	fs := pflag.NewFlagSet("agent prompt", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sView or edit agent prompt templates%s

%sUSAGE%s
    ezs agent prompt [options]

%sOPTIONS%s
    --edit               Open the prompt file in your editor ($EDITOR)
    --work               Target the work session prompt only
    --feature            Target the feature builder prompt only
    --reset              Reset prompt(s) to the built-in default
    -h, --help           Show this help message

    Without --work or --feature, both prompts are shown (or both are edited/reset).

%sTEMPLATE VARIABLES%s
    The following variables are replaced at runtime when the agent launches:

    {{STACK_JSON}}             Current stack structure as JSON
    {{BRANCH_NAME}}            Current branch name
    {{PARENT_NAME}}            Parent branch name
    {{WORKTREE_PATH}}          Path to the current worktree
    {{EZS_COMMANDS}}           Available ezs commands reference
    {{EZS_DOCS}}               Full ezstack documentation for AI agents
    {{FEATURE_DESCRIPTION}}    Feature description (feature mode only)

%sFILES%s
    Work session prompt:    ~/.ezstack/%s
    Feature builder prompt: ~/.ezstack/%s

    The recommended way to edit these prompts is through 'ezs agent prompt --edit'.

%sEXAMPLES%s
    %s# View both prompts with variable placeholders%s
    ezs agent prompt

    %s# Edit the work session prompt in your editor%s
    ezs agent prompt --edit --work

    %s# Edit the feature builder prompt%s
    ezs agent prompt --edit --feature

    %s# Edit both prompts (opens editor twice)%s
    ezs agent prompt --edit

    %s# Reset the work prompt to the built-in default%s
    ezs agent prompt --reset --work

    %s# Reset both prompts to defaults%s
    ezs agent prompt --reset
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset,
			workPromptFilename, featurePromptFilename,
			ui.Cyan, ui.Reset,
			ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset,
			ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset)
	}

	editFlag := fs.Bool("edit", false, "Open prompt in editor")
	workFlag := fs.Bool("work", false, "Target work session prompt only")
	featureFlag := fs.Bool("feature", false, "Target feature builder prompt only")
	resetFlag := fs.Bool("reset", false, "Reset prompt(s) to built-in default")
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

	// If neither --work nor --feature, target both
	showWork := *workFlag || (!*workFlag && !*featureFlag)
	showFeature := *featureFlag || (!*workFlag && !*featureFlag)

	if *resetFlag {
		return resetPrompts(showWork, showFeature)
	}

	if *editFlag {
		return editPrompts(showWork, showFeature)
	}

	return showPrompts(showWork, showFeature)
}

func showPrompts(showWork, showFeature bool) error {
	if showWork {
		content, path, err := loadPromptTemplate(workPromptFilename, defaultWorkPromptTemplate)
		if err != nil {
			return err
		}
		fmt.Printf("%s── Work Session Prompt (%s) ──%s\n\n", ui.Cyan, path, ui.Reset)
		fmt.Println(content)
		if showFeature {
			fmt.Println()
		}
	}

	if showFeature {
		content, path, err := loadPromptTemplate(featurePromptFilename, defaultFeaturePromptTemplate)
		if err != nil {
			return err
		}
		fmt.Printf("%s── Feature Builder Prompt (%s) ──%s\n\n", ui.Cyan, path, ui.Reset)
		fmt.Println(content)
	}

	return nil
}

func editPrompts(editWork, editFeature bool) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}

	if editWork {
		path, err := ensurePromptFile(workPromptFilename, defaultWorkPromptTemplate)
		if err != nil {
			return err
		}
		ui.Info(fmt.Sprintf("Opening work session prompt: %s", path))
		if err := openEditor(editor, path); err != nil {
			return err
		}
	}

	if editFeature {
		path, err := ensurePromptFile(featurePromptFilename, defaultFeaturePromptTemplate)
		if err != nil {
			return err
		}
		ui.Info(fmt.Sprintf("Opening feature builder prompt: %s", path))
		if err := openEditor(editor, path); err != nil {
			return err
		}
	}

	return nil
}

func resetPrompts(resetWork, resetFeature bool) error {
	configDir, err := config.ConfigDir()
	if err != nil {
		return err
	}

	if resetWork {
		path := filepath.Join(configDir, workPromptFilename)
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(defaultWorkPromptTemplate), 0644); err != nil {
			return err
		}
		ui.Success(fmt.Sprintf("Reset work session prompt to default: %s", path))
	}

	if resetFeature {
		path := filepath.Join(configDir, featurePromptFilename)
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(defaultFeaturePromptTemplate), 0644); err != nil {
			return err
		}
		ui.Success(fmt.Sprintf("Reset feature builder prompt to default: %s", path))
	}

	return nil
}

func openEditor(editor, filePath string) error {
	// Use sh -c so that $EDITOR values like "code --wait" or "subl -w" work.
	cmd := exec.Command("sh", "-c", editor+" "+ShellQuote(filePath))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ── Prompt file management ─────────────────────────────────────────────────────

// loadPromptTemplate loads a prompt template from ~/.ezstack/<filename>.
// If the file doesn't exist, it returns the built-in default.
func loadPromptTemplate(filename, defaultContent string) (content string, path string, err error) {
	configDir, err := config.ConfigDir()
	if err != nil {
		return defaultContent, "", nil
	}

	path = filepath.Join(configDir, filename)
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		// File doesn't exist — return default (don't create it until user explicitly edits/resets)
		return defaultContent, path, nil
	}

	return string(data), path, nil
}

// ensurePromptFile creates the prompt file from the default template if it doesn't exist.
// Returns the file path.
func ensurePromptFile(filename, defaultContent string) (string, error) {
	configDir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(configDir, filename)
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(defaultContent), 0644); err != nil {
			return "", err
		}
	}

	return path, nil
}

// firstPositionalArg finds the first positional (non-flag) argument in args.
// It skips flags (--foo, -f) and their values so that e.g. "-s prompt" does not
// match "prompt" as a subcommand.  Returns the matched arg and the remaining
// args after it, or ("", nil) if there are no positional args.
func firstPositionalArg(args []string) (string, []string) {
	// Flags whose values we know consume the next token.
	valuedFlags := map[string]bool{
		"--cmd": true, "-s": true, "--stack": true, "-b": true, "--branch": true,
	}
	skip := false
	for i, a := range args {
		if skip {
			skip = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			if valuedFlags[a] {
				skip = true // next token is the flag value
			}
			continue
		}
		return a, args[i+1:]
	}
	return "", nil
}

// renderPrompt replaces template variables in a prompt template with actual values.
func renderPrompt(template string, vars map[string]string) string {
	result := template
	for key, value := range vars {
		result = strings.ReplaceAll(result, "{{"+key+"}}", value)
	}
	return result
}

// ── Stack resolution ───────────────────────────────────────────────────────────

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

// ── Agent modes ────────────────────────────────────────────────────────────────

// agentWork launches the agent in work session mode, scoped to a stack.
func agentWork(g *git.Git, agentCmd, repoPath string, targetStack *config.Stack, branchName string) error {
	ctx := buildAgentContext(g, repoPath, targetStack, branchName)

	prompt, err := buildRenderedWorkPrompt(ctx)
	if err != nil {
		return err
	}
	workDir := resolveWorkDir(ctx.worktreePath, repoPath)

	ui.Info(fmt.Sprintf("Launching %s in %s mode on branch '%s'...", agentCmd, ui.Bold+"work"+ui.Reset, ctx.branchName))
	return spawnAgentProcess(agentCmd, workDir, prompt, "")
}

// agentFeature launches the agent in feature builder mode.
func agentFeature(g *git.Git, agentCmd, repoPath string, targetStack *config.Stack, branchName, description string) error {
	ctx := buildAgentContext(g, repoPath, targetStack, branchName)

	prompt, err := buildRenderedFeaturePrompt(ctx, description)
	if err != nil {
		return err
	}
	workDir := resolveWorkDir(ctx.worktreePath, repoPath)

	initialPrompt := fmt.Sprintf(`Implement the following feature using stacked branches:

%s

Start by exploring the codebase, then present a plan of stacked branches before implementing anything.`, description)

	ui.Info(fmt.Sprintf("Launching %s in %s mode...", agentCmd, ui.Bold+"feature builder"+ui.Reset))
	return spawnAgentProcess(agentCmd, workDir, prompt, initialPrompt)
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

// ── Context building ───────────────────────────────────────────────────────────

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

// ── Prompt rendering ───────────────────────────────────────────────────────────

func buildRenderedWorkPrompt(ctx *agentContext) (string, error) {
	template, _, err := loadPromptTemplate(workPromptFilename, defaultWorkPromptTemplate)
	if err != nil {
		return "", err
	}

	vars := buildTemplateVars(ctx)
	return renderPrompt(template, vars), nil
}

func buildRenderedFeaturePrompt(ctx *agentContext, description string) (string, error) {
	template, _, err := loadPromptTemplate(featurePromptFilename, defaultFeaturePromptTemplate)
	if err != nil {
		return "", err
	}

	vars := buildTemplateVars(ctx)
	vars["FEATURE_DESCRIPTION"] = description
	return renderPrompt(template, vars), nil
}

func buildTemplateVars(ctx *agentContext) map[string]string {
	vars := map[string]string{
		"STACK_JSON":    ctx.stackJSON,
		"BRANCH_NAME":  ctx.branchName,
		"PARENT_NAME":  ctx.parentName,
		"WORKTREE_PATH": ctx.worktreePath,
		"EZS_COMMANDS":  ezsCommandsReference,
		"EZS_DOCS":      ezsDocsReference,
	}
	return vars
}

// ── Agent process ──────────────────────────────────────────────────────────────

// spawnAgentProcess launches the agent CLI with the rendered prompt as a system
// prompt (via --append-system-prompt) so the agent has full context without
// cluttering the conversation.  An optional initialPrompt is passed as the
// first visible user message (positional argument).
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

// ── Default prompt templates ───────────────────────────────────────────────────

const ezsCommandsReference = `- ezs -y commit -m "msg" — Commit and auto-sync children
- ezs -y amend — Amend last commit and auto-sync children
- ezs diff — Show diff against parent branch
- ezs -y push — Push current branch
- ezs -y sync -c — Sync current branch with parent
- ezs ls --json — Get current stack state as JSON
- ezs -y new <name> — Create a child branch stacked on current
- ezs -y pr create -t "title" — Create a PR`

const ezsDocsReference = `ezstack is a CLI tool for managing stacked pull requests with git worktrees.

Key Concepts:
- Stack: A chain of branches where each branch builds on its parent
- Worktree: A separate working directory for each branch
- Sync: Rebase/merge branches when parents are updated
- Auto-restack: "ezs commit" and "ezs amend" automatically rebase children

Commands Quick Reference:
  ezs new <name>              Create a new branch in the stack
  ezs new <name> -p <parent>  Create branch with explicit parent
  ezs commit -m "msg"         Commit and auto-sync children
  ezs amend                   Amend last commit and auto-sync children
  ezs diff                    Show diff against parent branch
  ezs push                    Push current branch to remote
  ezs push -s                 Push all branches in the current stack
  ezs sync -c                 Sync current branch with parent
  ezs sync -s                 Sync entire current stack
  ezs sync -a                 Sync all stacks
  ezs pr create -t "title"    Create a pull request
  ezs pr update               Push and update PR metadata
  ezs pr merge -m squash      Merge a PR
  ezs pr stack                Update all PR descriptions with stack info
  ezs ls                      List all stacks and branches
  ezs ls --json               Machine-readable stack output
  ezs goto <branch>           Navigate to a branch worktree
  ezs up / ezs down           Navigate up/down the stack
  ezs delete <branch>         Delete a branch and its worktree
  ezs reparent <branch> <new-parent>  Change parent of a branch

Non-Interactive Mode:
  Use -y / --yes to skip confirmation prompts (always use this in agent mode).

Exit Codes:
  0  Success
  1  General error
  2  Usage error
  3  Rebase conflict — resolve conflicts, then "git rebase --continue"
  4  Not in a git repo
  5  Not in a stack
  6  Auth required — run "gh auth login"
  7  Branch not found
  8  Network error
  10 User cancelled`

const defaultWorkPromptTemplate = `You are working inside an ezstack-managed repository that uses stacked PRs with git worktrees.

## Current Stack
{{STACK_JSON}}

## Your Branch
Branch: {{BRANCH_NAME}}
Parent: {{PARENT_NAME}}
Worktree: {{WORKTREE_PATH}}

## IMPORTANT: Scope Constraint
You are scoped to THIS stack only. Do not create branches outside this stack.
Do not modify files in other worktrees or branches not in this stack.
All your changes must be relevant to the branch you are working on.

## ezs Commands (always use -y to skip confirmations)
{{EZS_COMMANDS}}

## ezstack Reference
{{EZS_DOCS}}

## Rules
- Only make changes relevant to this branch's purpose.
- Keep changes small and focused for easy review.
- Do not modify files outside this worktree.
- Commit with "ezs -y commit", not "git commit" (ezs commit auto-syncs children).
- Push with "ezs -y push", not "git push".
- Always use the -y flag with ezs commands to skip confirmations.
`

const defaultFeaturePromptTemplate = `You are working inside an ezstack-managed repository that uses stacked PRs with git worktrees.

## Current Stack
{{STACK_JSON}}

## Your Branch
Branch: {{BRANCH_NAME}}
Parent: {{PARENT_NAME}}
Worktree: {{WORKTREE_PATH}}

## IMPORTANT: Scope Constraint
You are scoped to THIS stack only. Do not create branches outside this stack.
Do not modify files in other worktrees or branches not in this stack.
All your changes must be relevant to the branch you are working on.

## ezs Commands (always use -y to skip confirmations)
{{EZS_COMMANDS}}

## ezstack Reference
{{EZS_DOCS}}

## Feature Builder Mode
You are implementing the following feature as a series of small, stacked, reviewable branches:

{{FEATURE_DESCRIPTION}}

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

## Rules
- Only make changes relevant to this branch's purpose.
- Keep changes small and focused for easy review.
- Do not modify files outside this worktree.
- Commit with "ezs -y commit", not "git commit" (ezs commit auto-syncs children).
- Push with "ezs -y push", not "git push".
- Always use the -y flag with ezs commands to skip confirmations.
`
