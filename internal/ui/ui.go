package ui

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/chzyer/readline"
	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

// padRight pads a string to the specified display width using runewidth.
// This correctly handles Unicode characters including Nerd Font icons.
func padRight(s string, width int) string {
	w := runewidth.StringWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// Colors for terminal output
const (
	Reset         = "\033[0m"
	Bold          = "\033[1m"
	Strikethrough = "\033[9m"
	Red           = "\033[31m"
	Green         = "\033[32m"
	Yellow        = "\033[33m"
	Blue          = "\033[34m"
	Magenta       = "\033[35m"
	Cyan          = "\033[36m"
	Gray          = "\033[90m"
)

// Icons for terminal rendering. Defaults to Nerd Font glyphs; set
// EZS_ASCII=1 to use plain ASCII fallbacks.
var (
	IconSuccess  = "\uf00c"
	IconError    = "\uf00d"
	IconWarning  = "\uf071"
	IconInfo     = "\uf05a"
	IconPending  = "\uf192"
	IconArrow    = "\uf061"
	IconPointer  = "\uf0a4"
	IconBranch   = "\ue725"
	IconUp       = "\uf062"
	IconDown     = "\uf063"
	IconSync     = "\uf021"
	IconCancel   = "\uf05e"
	IconBullet   = "\uf111"
	IconApproved = "\uf058"
	IconChanges  = "\uf06a"
	IconConflict = "\uf071"
	IconNew      = "\uf067"
	IconPush     = "\uf093"
	IconStack    = "\uf24d"
	IconRocket   = "\uf135"
	IconBack     = "\uf060"
)

func init() {
	if os.Getenv("EZS_ASCII") != "" {
		IconSuccess = "✓"
		IconError = "✗"
		IconWarning = "!"
		IconInfo = "i"
		IconPending = "○"
		IconArrow = "→"
		IconPointer = ">"
		IconBranch = "⎇"
		IconUp = "↑"
		IconDown = "↓"
		IconSync = "⟳"
		IconCancel = "⊘"
		IconBullet = "•"
		IconApproved = "✓"
		IconChanges = "!"
		IconConflict = "!"
		IconNew = "+"
		IconPush = "↑"
		IconStack = "≡"
		IconRocket = "*"
		IconBack = "←"
	}
}

// SuggestCommand returns the candidate with the smallest Levenshtein distance
// to input, provided the distance is plausibly a typo (≤3 and ≤ half the
// longer of the two strings). Returns "" when nothing qualifies, the input is
// empty, or the candidate list is empty.
func SuggestCommand(input string, candidates []string) string {
	if input == "" || len(candidates) == 0 {
		return ""
	}
	best := ""
	bestDist := -1
	for _, c := range candidates {
		if c == "" {
			continue
		}
		d := levenshtein(input, c)
		if bestDist == -1 || d < bestDist {
			bestDist = d
			best = c
		}
	}
	if best == "" {
		return ""
	}
	maxLen := len(input)
	if len(best) > maxLen {
		maxLen = len(best)
	}
	if bestDist <= 3 && bestDist*2 <= maxLen {
		return best
	}
	return ""
}

func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	la, lb := len(ar), len(br)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// Hyperlink wraps text in OSC 8 escape sequence for clickable terminal links
func Hyperlink(url, text string) string {
	if url == "" {
		return text
	}
	// OSC 8 format: \033]8;;URL\033\\TEXT\033]8;;\033\\
	return fmt.Sprintf("\033]8;;%s\033\\%s\033]8;;\033\\", url, text)
}

// ErrBack is returned when the user selects the back option
var ErrBack = fmt.Errorf("back")

// Exit codes for structured error reporting
const (
	ExitSuccess        = 0  // Success
	ExitGeneral        = 1  // General error
	ExitUsage          = 2  // Usage/argument error
	ExitConflict       = 3  // Rebase conflict
	ExitNotInRepo      = 4  // Not in a git repository
	ExitNotInStack     = 5  // Current branch is not in a stack
	ExitAuthRequired   = 6  // GitHub authentication required
	ExitBranchNotFound = 7  // Branch not found
	ExitNetworkError   = 8  // Network/remote error
	ExitUserCancelled  = 10 // User cancelled operation
)

// ExitError wraps an error with a specific exit code
type ExitError struct {
	Code    int
	Message string
}

func (e *ExitError) Error() string { return e.Message }

// NewExitError creates an ExitError with the given code and message
func NewExitError(code int, format string, args ...interface{}) *ExitError {
	return &ExitError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// GetExitCode returns the exit code for an error, defaulting to ExitGeneral
func GetExitCode(err error) int {
	if err == nil {
		return ExitSuccess
	}
	if exitErr, ok := err.(*ExitError); ok {
		return exitErr.Code
	}
	return ExitGeneral
}

// YesMode, when true, makes all ConfirmTUI / Confirm calls auto-return true
// without showing any interactive dialog.
var YesMode bool

// BranchStatus contains status information for a branch
type BranchStatus struct {
	PRState     string // "OPEN", "MERGED", "CLOSED", "DRAFT", ""
	CIState     string // "success", "failure", "pending", "none", ""
	CISummary   string // e.g., "3/3 passed"
	Mergeable   string // "MERGEABLE", "CONFLICTING", "UNKNOWN"
	ReviewState string // "APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED", ""
	Additions   int    // Lines added relative to parent
	Deletions   int    // Lines removed relative to parent
}

// SelectBranch uses fzf to select a branch from a list
func SelectBranch(branches []*config.Branch, prompt string) (*config.Branch, error) {
	return activeBackend.SelectBranch(branches, prompt)
}

func (t *TerminalBackend) SelectBranch(branches []*config.Branch, prompt string) (*config.Branch, error) {
	return SelectBranchWithStacks(branches, nil, prompt)
}

// SelectBranchWithStacks uses fzf to select a branch with optional stack preview.
//
// Under YesMode (MCP / -y) we cannot run fzf — there's no controlling
// terminal — so multi-match calls fail loudly with a structured error
// instead of hanging. The exact-match path (single branch) returns it
// without prompting so callers that already narrowed don't pay the cost.
func SelectBranchWithStacks(branches []*config.Branch, stacks []*config.Stack, prompt string) (*config.Branch, error) {
	if len(branches) == 0 {
		return nil, fmt.Errorf("no branches to select from")
	}
	if YesMode {
		if len(branches) == 1 {
			return branches[0], nil
		}
		names := make([]string, 0, len(branches))
		for _, b := range branches {
			names = append(names, b.Name)
		}
		return nil, fmt.Errorf("multiple branches match (%s) — disambiguate with an exact branch name; interactive selection is unavailable in -y / MCP mode", strings.Join(names, ", "))
	}

	// Build fzf input with preview data embedded
	// Format: branchName|previewData (IconArrow parent) [PR #N]
	var input strings.Builder
	for _, b := range branches {
		prInfo := ""
		if b.PRNumber > 0 {
			prInfo = fmt.Sprintf(" [PR #%d]", b.PRNumber)
		}
		// Embed preview data as hidden field using tab separator
		preview := generateBranchPreview(b, stacks)
		// Use format: display_text\tpreview_data
		displayText := fmt.Sprintf("%s (%s %s)%s", b.Name, IconArrow, b.Parent, prInfo)
		input.WriteString(fmt.Sprintf("%s\t%s\n", displayText, preview))
	}

	selected, err := runFzfWithPreview(input.String(), prompt, stacks != nil)
	if err != nil {
		return nil, err
	}

	// Parse the selected branch name (first field before space)
	parts := strings.SplitN(selected, " ", 2)
	if len(parts) == 0 {
		return nil, fmt.Errorf("no branch selected")
	}
	branchName := parts[0]

	for _, b := range branches {
		if b.Name == branchName {
			return b, nil
		}
	}

	return nil, fmt.Errorf("branch not found: %s", branchName)
}

// generateBranchPreview creates a stack preview matching ezs ls output
// Uses escape codes that printf/echo -e can interpret for ANSI colors
func generateBranchPreview(branch *config.Branch, stacks []*config.Stack) string {
	if stacks == nil {
		return ""
	}

	// Find which stack contains this branch
	var targetStack *config.Stack
	for _, s := range stacks {
		for _, b := range s.Branches {
			if b.Name == branch.Name {
				targetStack = s
				break
			}
		}
		if targetStack != nil {
			break
		}
	}

	if targetStack == nil {
		return ""
	}

	// Use formatStackString with escape codes for fzf preview
	return formatStackString(targetStack, branch.Name)
}

// WorktreeInfo represents a worktree for UI selection
type WorktreeInfo struct {
	Path   string
	Branch string
}

// SelectWorktree uses fzf to select a worktree from a list.
// Under YesMode it returns the lone worktree if there is only one and errors
// loudly otherwise, mirroring SelectWorktreeWithStackPreview.
func SelectWorktree(worktrees []WorktreeInfo, prompt string) (*WorktreeInfo, error) {
	if len(worktrees) == 0 {
		return nil, fmt.Errorf("no worktrees to select from")
	}
	if YesMode {
		if len(worktrees) == 1 {
			return &worktrees[0], nil
		}
		names := make([]string, 0, len(worktrees))
		for _, w := range worktrees {
			names = append(names, w.Branch)
		}
		return nil, fmt.Errorf("multiple worktrees match (%s) — disambiguate with an exact branch name; interactive selection is unavailable in -y / MCP mode", strings.Join(names, ", "))
	}

	var input strings.Builder
	for _, wt := range worktrees {
		input.WriteString(fmt.Sprintf("%s (%s)\n", wt.Branch, wt.Path))
	}

	selected, err := runFzf(input.String(), prompt)
	if err != nil {
		return nil, err
	}

	// Parse the selected branch name (first field before space)
	parts := strings.SplitN(selected, " ", 2)
	if len(parts) == 0 {
		return nil, fmt.Errorf("no worktree selected")
	}
	branchName := parts[0]

	for i := range worktrees {
		if worktrees[i].Branch == branchName {
			return &worktrees[i], nil
		}
	}

	return nil, fmt.Errorf("worktree not found: %s", branchName)
}

// SelectWorktreeWithStackPreview uses fzf to select a worktree with stack preview.
// For worktrees not in any stack, the preview shows "Not part of a stack".
//
// Under YesMode (MCP / -y) we cannot run fzf — there's no controlling terminal
// — so multi-match calls fail loudly with a structured error instead of
// hanging on an interactive prompt. The exact-match path (single worktree)
// still works because callers handle len==1 before reaching here.
func SelectWorktreeWithStackPreview(worktrees []WorktreeInfo, stacks []*config.Stack, prompt string) (*WorktreeInfo, error) {
	if len(worktrees) == 0 {
		return nil, fmt.Errorf("no worktrees to select from")
	}
	if YesMode && len(worktrees) > 1 {
		names := make([]string, 0, len(worktrees))
		for _, w := range worktrees {
			names = append(names, w.Branch)
		}
		return nil, fmt.Errorf("multiple worktrees match (%s) — disambiguate with an exact branch name; interactive selection is unavailable in -y / MCP mode", strings.Join(names, ", "))
	}

	// Build a map of branch -> stack for quick lookup
	branchToStack := make(map[string]*config.Stack)
	for _, s := range stacks {
		for _, b := range s.Branches {
			branchToStack[b.Name] = s
		}
	}

	// Build fzf input with preview data embedded
	var input strings.Builder
	for _, wt := range worktrees {
		displayText := fmt.Sprintf("%s (%s)", wt.Branch, wt.Path)

		// Generate preview
		var preview string
		if stack, ok := branchToStack[wt.Branch]; ok {
			preview = formatStackString(stack, wt.Branch)
		} else {
			// Not in any stack
			gray := "\\x1b[90m"
			reset := "\\x1b[0m"
			preview = fmt.Sprintf("%sNot part of a stack%s\\n\\nThis worktree is not tracked by ezstack.\\nUse 'ezs new -f' to register it as a stack root.", gray, reset)
		}

		input.WriteString(fmt.Sprintf("%s\t%s\n", displayText, preview))
	}

	selected, err := runFzfWithPreview(input.String(), prompt, true)
	if err != nil {
		return nil, err
	}

	// Parse the selected branch name (first field before space)
	parts := strings.SplitN(selected, " ", 2)
	if len(parts) == 0 {
		return nil, fmt.Errorf("no worktree selected")
	}
	branchName := parts[0]

	for i := range worktrees {
		if worktrees[i].Branch == branchName {
			return &worktrees[i], nil
		}
	}

	return nil, fmt.Errorf("worktree not found: %s", branchName)
}

// SelectStack uses fzf to select a stack.
//
// The YesMode guard lives on the TerminalBackend method (below), NOT on
// this package-level wrapper. Backend-aware: MCPBackend.SelectStack uses
// JSON-Schema elicitation (no TTY required), which works correctly under
// YesMode — a package-level guard would force auto-pick / error and
// short-circuit the MCP client's natural disambiguation flow.
// TerminalBackend.SelectStack, in contrast, calls runFzf which DOES hang
// on a missing TTY, so it has its own guard. Same pattern applies to
// SelectBranch and SelectOptionWithBack (also dispatched through Backend).
func SelectStack(stacks []*config.Stack, prompt string) (*config.Stack, error) {
	return activeBackend.SelectStack(stacks, prompt)
}

func (t *TerminalBackend) SelectStack(stacks []*config.Stack, prompt string) (*config.Stack, error) {
	if len(stacks) == 0 {
		return nil, fmt.Errorf("no stacks to select from")
	}
	if YesMode {
		if len(stacks) == 1 {
			return stacks[0], nil
		}
		names := make([]string, 0, len(stacks))
		for _, s := range stacks {
			names = append(names, s.DisplayName())
		}
		return nil, fmt.Errorf("multiple stacks match (%s) — disambiguate with --stack <hash>; interactive selection is unavailable in -y / MCP mode", strings.Join(names, ", "))
	}

	var input strings.Builder
	for i, s := range stacks {
		input.WriteString(fmt.Sprintf("%d. %s (%d branches)\n", i+1, s.DisplayName(), len(s.Branches)))
	}

	selected, err := runFzf(input.String(), prompt)
	if err != nil {
		return nil, err
	}

	var idx int
	if _, err := fmt.Sscanf(selected, "%d.", &idx); err != nil {
		return nil, fmt.Errorf("failed to parse selection")
	}
	idx-- // 0-based

	if idx >= 0 && idx < len(stacks) {
		return stacks[idx], nil
	}

	return nil, fmt.Errorf("stack not found")
}

// runFzf executes fzf with the given input and returns the selected line
func runFzf(input, prompt string) (string, error) {
	cmd := exec.Command("fzf",
		"--prompt", prompt+": ",
		"--height", "40%",
		"--reverse",
		"--border",
		"--ansi",
	)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = os.Stderr

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return "", fmt.Errorf("cancelled")
		}
		return "", err
	}

	return strings.TrimSpace(stdout.String()), nil
}

// runFzfWithPreview executes fzf with preview window for stack visualization
func runFzfWithPreview(input, prompt string, showPreview bool) (string, error) {
	args := []string{
		"--prompt", prompt + ": ",
		"--height", "40%",
		"--reverse",
		"--border",
		"--ansi",
	}

	if showPreview {
		args = append(args,
			"--delimiter", "\t",
			"--with-nth", "1", // Show only first field (display text)
			"--preview", "printf '%b' {2}", // Preview shows second field (preview data) with ANSI colors
			"--preview-window", "down:50%:wrap",
		)
	}

	cmd := exec.Command("fzf", args...)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = os.Stderr

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return "", fmt.Errorf("cancelled")
		}
		return "", err
	}

	result := strings.TrimSpace(stdout.String())
	// Remove the preview data (everything after first tab)
	if idx := strings.Index(result, "\t"); idx != -1 {
		result = result[:idx]
	}
	return result, nil
}

// MaxBranchNameWidth is the maximum width for branch names before truncation
const MaxBranchNameWidth = 50

// truncateBranchName truncates a branch name to maxWidth, appending "..." if needed
func truncateBranchName(name string, maxWidth int) string {
	if runewidth.StringWidth(name) <= maxWidth {
		return name
	}
	if maxWidth <= 3 {
		return runewidth.Truncate(name, maxWidth, "")
	}
	return runewidth.Truncate(name, maxWidth, "...")
}

// sortBranchesTopologically sorts branches so parents come before children
// This ensures the display shows the correct parent -> child order
func sortBranchesTopologically(branches []*config.Branch) []*config.Branch {
	return config.SortBranchesTopologically(branches)
}

// fzfPRText returns the PR label for fzf preview (uses cached PRState)
func fzfPRText(b *config.Branch) string {
	if b.PRNumber == 0 {
		return "[no PR]"
	}
	state := b.PRState
	if state == "" && b.IsMerged {
		state = "MERGED"
	}
	switch state {
	case "MERGED":
		return fmt.Sprintf("[PR #%d MERGED]", b.PRNumber)
	case "CLOSED":
		return fmt.Sprintf("[PR #%d CLOSED]", b.PRNumber)
	case "DRAFT":
		return fmt.Sprintf("[PR #%d DRAFT]", b.PRNumber)
	default:
		return fmt.Sprintf("[PR #%d]", b.PRNumber)
	}
}

// fzfPRColor returns the color escape code for PR text in fzf preview
func fzfPRColor(b *config.Branch, cyan, yellow, gray, red string) string {
	if b.PRNumber == 0 {
		return gray
	}
	state := b.PRState
	if state == "" && b.IsMerged {
		state = "MERGED"
	}
	switch state {
	case "MERGED":
		return cyan
	case "CLOSED":
		return red
	case "DRAFT":
		return gray
	default:
		return yellow
	}
}

// formatStackString formats a stack as a string for fzf preview (with escape codes)
// Always sorts branches topologically (parent -> child order)
func formatStackString(stack *config.Stack, currentBranch string) string {
	// Use escape codes that echo -e can interpret for fzf preview
	bold := "\\x1b[1m"
	reset := "\\x1b[0m"
	strikethrough := "\\x1b[9m"
	green := "\\x1b[32m"
	red := "\\x1b[31m"
	yellow := "\\x1b[33m"
	gray := "\\x1b[90m"
	cyan := "\\x1b[36m"
	var output strings.Builder
	output.WriteString(fmt.Sprintf("%s%s Stack %s%s\\n\\n", bold, cyan, stack.DisplayName(), reset))

	// Build children map for tree rendering
	branches := stack.Branches
	branchMap := make(map[string]*config.Branch)
	childrenMap := make(map[string][]*config.Branch)
	var roots []*config.Branch
	for _, b := range branches {
		branchMap[b.Name] = b
	}
	for _, b := range branches {
		parentKey := b.Parent
		if b.BaseBranch != "" && b.BaseBranch != b.Parent {
			if _, ok := branchMap[b.BaseBranch]; ok {
				parentKey = b.BaseBranch
			}
		}
		if _, ok := branchMap[parentKey]; ok {
			childrenMap[parentKey] = append(childrenMap[parentKey], b)
		} else {
			roots = append(roots, b)
		}
	}

	// Print root branch name
	output.WriteString(fmt.Sprintf("  %s%s%s%s\\n", gray, stack.Root, reset, ""))

	// Recursive tree walker
	var walkTree func(nodes []*config.Branch, prefix string)
	walkTree = func(nodes []*config.Branch, prefix string) {
		for i, b := range nodes {
			isLast := i == len(nodes)-1
			connector := "├── "
			childPrefix := "│   "
			if isLast {
				connector = "└── "
				childPrefix = "    "
			}

			pointer := " "
			color := ""
			if b.Name == currentBranch {
				pointer = ">"
				color = green
			}

			displayName := truncateBranchName(b.Name, MaxBranchNameWidth)
			prText := fzfPRText(b)
			prColor := fzfPRColor(b, cyan, yellow, gray, red)

			shouldStrike := b.IsMerged || b.PRState == "MERGED" || b.PRState == "CLOSED"
			if shouldStrike {
				output.WriteString(fmt.Sprintf("%s%s%s%s%s%s%s  %s%s%s\\n",
					pointer, color, prefix, connector, strikethrough+bold, displayName, reset+strikethrough,
					prColor, prText, reset))
			} else {
				output.WriteString(fmt.Sprintf("%s%s%s%s%s%s%s  %s%s%s\\n",
					pointer, color, prefix, connector, bold, displayName, reset,
					prColor, prText, reset))
			}

			if children, ok := childrenMap[b.Name]; ok {
				walkTree(children, prefix+childPrefix)
			}
		}
	}

	walkTree(roots, "  ")
	return output.String()
}

// PrintStack prints a visual representation of a stack using tree-style indentation.
// If showStatus is true, includes CI/PR status after the PR column.
func PrintStack(stack *config.Stack, currentBranch string, showStatus bool, statusMap map[string]*BranchStatus) {
	fmt.Fprintf(os.Stderr, "\n%s%s Stack %s%s\n\n", Bold, Cyan, stack.DisplayName(), Reset)

	if len(stack.Branches) == 0 {
		fmt.Fprintf(os.Stderr, "  %s(empty)%s\n\n", Gray, Reset)
		return
	}

	// Build children map for tree rendering
	branchMap := make(map[string]*config.Branch)
	childrenMap := make(map[string][]*config.Branch)
	var roots []*config.Branch
	for _, b := range stack.Branches {
		branchMap[b.Name] = b
	}
	for _, b := range stack.Branches {
		parentKey := b.Parent
		if b.BaseBranch != "" && b.BaseBranch != b.Parent {
			if _, ok := branchMap[b.BaseBranch]; ok {
				parentKey = b.BaseBranch
			}
		}
		if _, ok := branchMap[parentKey]; ok {
			childrenMap[parentKey] = append(childrenMap[parentKey], b)
		} else {
			roots = append(roots, b)
		}
	}

	// Print root branch name with optional PR info and diff stats
	rootLine := fmt.Sprintf("  %s%s%s", Gray, stack.Root, Reset)
	if stack.RootPRNumber > 0 {
		prText := fmt.Sprintf("[PR #%d", stack.RootPRNumber)
		if stack.RootBase != "" {
			prText += " \u2192 " + stack.RootBase // → arrow
		}
		prText += "]"
		if stack.RootPRUrl != "" {
			rootLine += "  " + Yellow + Hyperlink(stack.RootPRUrl, prText) + Reset
		} else {
			rootLine += "  " + Yellow + prText + Reset
		}
	} else if stack.RootBase != "" {
		// No PR but we know the base branch (e.g. inferred main/master)
		rootLine += "  " + Gray + "[\u2192 " + stack.RootBase + "]" + Reset
	}
	if statusMap != nil {
		if rootStatus, ok := statusMap[stack.Root]; ok && rootStatus != nil {
			if rootStatus.Additions > 0 || rootStatus.Deletions > 0 {
				rootLine += fmt.Sprintf(" %s+%d%s %s-%d%s", Green, rootStatus.Additions, Reset, Red, rootStatus.Deletions, Reset)
			}
		}
	}
	fmt.Fprintf(os.Stderr, "%s\n", rootLine)

	// Recursive tree walker
	var walkTree func(nodes []*config.Branch, prefix string)
	walkTree = func(nodes []*config.Branch, prefix string) {
		for i, branch := range nodes {
			isLast := i == len(nodes)-1
			connector := "├── "
			childPrefix := "│   "
			if isLast {
				connector = "└── "
				childPrefix = "    "
			}

			// Check if branch should be struck through (merged or closed)
			shouldStrike := branch.IsMerged || branch.PRState == "MERGED" || branch.PRState == "CLOSED"
			if !shouldStrike && statusMap != nil {
				if status, ok := statusMap[branch.Name]; ok && status != nil {
					shouldStrike = status.PRState == "MERGED" || status.PRState == "CLOSED"
				}
			}

			// Pointer for current branch
			pointer := " "
			color := ""
			if branch.Name == currentBranch {
				pointer = ">"
				color = Green
			}

			// Branch name
			name := truncateBranchName(branch.Name, MaxBranchNameWidth)

			// PR info
			prFormatted := getPRFormatted(branch, statusMap, 0)

			// Diff stats
			diffInfo := getDiffStats(branch, statusMap)

			// Status info
			statusInfo := ""
			if showStatus && statusMap != nil {
				statusInfo = getStatusIcons(branch, statusMap)
			}

			remoteTag := ""
			if branch.IsRemote {
				remoteTag = " " + Gray + "(remote)" + Reset
			}

			if shouldStrike {
				prWithStrike := strings.ReplaceAll(prFormatted, Reset, Reset+Strikethrough)
				diffWithStrike := ""
				if diffInfo != "" {
					diffWithStrike = strings.ReplaceAll(diffInfo, Reset, Reset+Strikethrough)
				}
				statusWithStrike := ""
				if statusInfo != "" {
					statusWithStrike = strings.ReplaceAll(statusInfo, Reset, Reset+Strikethrough)
				}
				fmt.Fprintf(os.Stderr, "%s%s%s%s%s%s%s%s  %s%s%s%s\n",
					pointer, color, prefix, connector, Strikethrough+Bold, name, Reset+Strikethrough,
					remoteTag, prWithStrike, diffWithStrike, statusWithStrike, Reset)
			} else {
				fmt.Fprintf(os.Stderr, "%s%s%s%s%s%s%s%s  %s%s%s%s\n",
					pointer, color, prefix, connector, Bold, name, Reset,
					remoteTag, prFormatted, diffInfo, statusInfo, Reset)
			}

			if children, ok := childrenMap[branch.Name]; ok {
				walkTree(children, prefix+childPrefix)
			}
		}
	}

	walkTree(roots, "  ")
	fmt.Fprintln(os.Stderr)
}

// getPRText returns the PR text without color codes
func getPRText(branch *config.Branch, statusMap map[string]*BranchStatus) string {
	if branch.PRNumber == 0 {
		return "[no PR]"
	}

	// Resolve PR state: prefer live statusMap, fall back to cached branch.PRState
	prState := branch.PRState
	if statusMap != nil {
		if status, ok := statusMap[branch.Name]; ok && status != nil && status.PRState != "" {
			prState = status.PRState
		}
	}
	// Legacy fallback: IsMerged bool without PRState
	if prState == "" && branch.IsMerged {
		prState = "MERGED"
	}

	switch prState {
	case "MERGED":
		return fmt.Sprintf("[PR #%d MERGED]", branch.PRNumber)
	case "CLOSED":
		return fmt.Sprintf("[PR #%d CLOSED]", branch.PRNumber)
	case "DRAFT":
		return fmt.Sprintf("[PR #%d DRAFT]", branch.PRNumber)
	default:
		return fmt.Sprintf("[PR #%d]", branch.PRNumber)
	}
}

// getPRColor returns the color for PR text
func getPRColor(branch *config.Branch, statusMap map[string]*BranchStatus) string {
	if branch.PRNumber == 0 {
		return Gray
	}

	// Resolve PR state: prefer live statusMap, fall back to cached branch.PRState
	prState := branch.PRState
	if statusMap != nil {
		if status, ok := statusMap[branch.Name]; ok && status != nil && status.PRState != "" {
			prState = status.PRState
		}
	}
	if prState == "" && branch.IsMerged {
		prState = "MERGED"
	}

	switch prState {
	case "MERGED":
		return Cyan
	case "CLOSED":
		return Red
	case "DRAFT":
		return Gray
	default:
		return Yellow
	}
}

// getPRFormatted returns the PR text with color and hyperlink applied
func getPRFormatted(branch *config.Branch, statusMap map[string]*BranchStatus, paddedWidth int) string {
	prText := getPRText(branch, statusMap)
	prColor := getPRColor(branch, statusMap)

	// Calculate padding needed after the text
	textWidth := runewidth.StringWidth(prText)
	padding := ""
	if paddedWidth > textWidth {
		padding = strings.Repeat(" ", paddedWidth-textWidth)
	}

	// Wrap only the actual text in hyperlink, add padding outside
	if branch.PRUrl != "" {
		return prColor + Hyperlink(branch.PRUrl, prText) + Reset + padding
	}
	return prColor + prText + Reset + padding
}

// getStatusIcons returns CI/review status icons
func getStatusIcons(branch *config.Branch, statusMap map[string]*BranchStatus) string {
	if statusMap == nil || branch.PRNumber == 0 {
		return ""
	}
	status, ok := statusMap[branch.Name]
	if !ok || status == nil {
		return ""
	}

	var statusInfo string

	// CI status
	switch status.CIState {
	case "success":
		statusInfo += fmt.Sprintf(" %s%s%s", Green, IconSuccess, Reset)
	case "failure":
		statusInfo += fmt.Sprintf(" %s%s%s", Red, IconError, Reset)
	case "pending":
		statusInfo += fmt.Sprintf(" %s%s%s", Yellow, IconPending, Reset)
	}

	// Review state
	switch status.ReviewState {
	case "APPROVED":
		statusInfo += fmt.Sprintf(" %s%s approved%s", Green, IconApproved, Reset)
	case "CHANGES_REQUESTED":
		statusInfo += fmt.Sprintf(" %s%s changes%s", Red, IconChanges, Reset)
	}

	// Merge conflicts
	if status.Mergeable == "CONFLICTING" {
		statusInfo += fmt.Sprintf(" %s%s conflict%s", Red, IconConflict, Reset)
	}

	return statusInfo
}

// getDiffStats returns formatted diff stats (e.g., " +45 -12") with colors
func getDiffStats(branch *config.Branch, statusMap map[string]*BranchStatus) string {
	if statusMap == nil {
		return ""
	}
	status, ok := statusMap[branch.Name]
	if !ok || status == nil {
		return ""
	}
	if status.Additions == 0 && status.Deletions == 0 {
		return ""
	}
	// Hide diff stats for merged/closed branches since they're meaningless
	if branch.IsMerged || branch.PRState == "MERGED" || branch.PRState == "CLOSED" {
		return ""
	}
	return fmt.Sprintf(" %s+%d%s %s-%d%s", Green, status.Additions, Reset, Red, status.Deletions, Reset)
}

// getStatusText returns CI/review status text WITHOUT color codes (for width calculation)
func getStatusText(branch *config.Branch, statusMap map[string]*BranchStatus) string {
	if statusMap == nil || branch.PRNumber == 0 {
		return ""
	}
	status, ok := statusMap[branch.Name]
	if !ok || status == nil {
		return ""
	}

	var statusText string

	// CI status
	switch status.CIState {
	case "success":
		statusText += " " + IconSuccess
	case "failure":
		statusText += " " + IconError
	case "pending":
		statusText += " " + IconPending
	}

	// Review state
	switch status.ReviewState {
	case "APPROVED":
		statusText += " " + IconApproved + " approved"
	case "CHANGES_REQUESTED":
		statusText += " " + IconChanges + " changes"
	}

	// Merge conflicts
	if status.Mergeable == "CONFLICTING" {
		statusText += " " + IconConflict + " conflict"
	}

	return statusText
}

// Confirm asks the user for confirmation (simple text-based)
func Confirm(prompt string) bool {
	if YesMode {
		fmt.Fprintf(os.Stderr, "%s%s?%s %s %s→ yes%s\n", Bold, Yellow, Reset, prompt, Green, Reset)
		return true
	}
	fmt.Fprintf(os.Stderr, "%s [y/N]: ", prompt)
	var response string
	fmt.Scanln(&response)
	return strings.ToLower(response) == "y" || strings.ToLower(response) == "yes"
}

// confirmTUICore implements the raw-terminal yes/no dialog loop.
// defaultYes controls which option is highlighted initially.
// escValue is the value returned when the user presses Escape.
// Callers must handle YesMode and non-terminal fallback before calling this.
func confirmTUICore(prompt string, defaultYes bool, escValue bool) bool {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return Confirm(prompt)
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return Confirm(prompt)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	selected := 1 // 0 = Yes, 1 = No
	if defaultYes {
		selected = 0
	}

	renderConfirm := func() {
		fmt.Fprint(os.Stderr, "\r\033[K")
		fmt.Fprintf(os.Stderr, "%s%s?%s %s\n\r", Bold, Yellow, Reset, prompt)
		fmt.Fprint(os.Stderr, "\033[K")

		var yesStyle, noStyle string
		if selected == 0 {
			yesStyle = fmt.Sprintf("%s▸ %s%sYes%s", Green, Bold, Green, Reset)
			noStyle = fmt.Sprintf("  %sNo%s", Reset, Reset)
		} else {
			yesStyle = fmt.Sprintf("  %sYes%s", Reset, Reset)
			noStyle = fmt.Sprintf("%s▸ %s%sNo%s", Red, Bold, Red, Reset)
		}

		fmt.Fprintf(os.Stderr, "  %s\n\r", yesStyle)
		fmt.Fprintf(os.Stderr, "  %s\n\r", noStyle)
		fmt.Fprintf(os.Stderr, "\033[K%s(Use ↑/↓ arrows to select, Enter to confirm)%s\r", Magenta, Reset)
		fmt.Fprint(os.Stderr, "\033[3A")
	}

	fmt.Fprintln(os.Stderr)
	renderConfirm()

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			break
		}

		if n == 1 {
			switch buf[0] {
			case 13, 10: // Enter
				fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
				return selected == 0
			case 3: // Ctrl+C
				fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
				term.Restore(int(os.Stdin.Fd()), oldState)
				os.Exit(130)
			case 27: // ESC
				fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
				return escValue
			case 'k', 'K': // vim-style up
				selected = 0
				renderConfirm()
			case 'j', 'J': // vim-style down
				selected = 1
				renderConfirm()
			case 'y', 'Y':
				fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
				return true
			case 'n', 'N':
				fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
				return false
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == 91 {
			switch buf[2] {
			case 65: // Up arrow
				selected = 0
				renderConfirm()
			case 66: // Down arrow
				selected = 1
				renderConfirm()
			}
		}
	}

	return selected == 0
}

// ConfirmTUI shows a TUI yes/no dialog with Yes highlighted by default.
// ESC cancels (returns false).
func ConfirmTUI(prompt string) bool {
	if YesMode {
		fmt.Fprintf(os.Stderr, "%s%s?%s %s %s→ yes%s\n", Bold, Yellow, Reset, prompt, Green, Reset)
		return true
	}
	return activeBackend.Confirm(prompt)
}

func (t *TerminalBackend) Confirm(prompt string) bool {
	return confirmTUICore(prompt, true, false)
}

// ConfirmTUIWithDefault shows a TUI yes/no dialog.
// defaultYes controls which option is highlighted; ESC returns the default.
func ConfirmTUIWithDefault(prompt string, defaultYes bool) bool {
	if YesMode {
		fmt.Fprintf(os.Stderr, "%s%s?%s %s %s→ yes%s\n", Bold, Yellow, Reset, prompt, Green, Reset)
		return true
	}
	return activeBackend.ConfirmWithDefault(prompt, defaultYes)
}

func (t *TerminalBackend) ConfirmWithDefault(prompt string, defaultYes bool) bool {
	return confirmTUICore(prompt, defaultYes, defaultYes)
}

// SelectTUI shows a TUI selection menu with arrow key navigation
// options is the list of options to display
// prompt is the question to ask
// defaultIdx is the 0-based index of the default selected option
// Returns the 0-based index of the selected option, or -1 if cancelled.
//
// Honors YesMode: under MCP / -y the menu auto-resolves to defaultIdx
// instead of trying to drive a TTY that isn't there. Without this guard,
// MCP tool calls that route through SelectTUI hang indefinitely waiting
// for keystrokes from a controlling terminal.
func SelectTUI(options []string, prompt string, defaultIdx int) int {
	if YesMode {
		if len(options) == 0 {
			return -1
		}
		if defaultIdx < 0 || defaultIdx >= len(options) {
			defaultIdx = 0
		}
		return defaultIdx
	}
	return activeBackend.Select(options, prompt, defaultIdx)
}

func (t *TerminalBackend) Select(options []string, prompt string, defaultIdx int) int {
	if len(options) == 0 {
		return -1
	}

	// Clamp defaultIdx to valid range
	if defaultIdx < 0 || defaultIdx >= len(options) {
		defaultIdx = 0
	}

	// Save terminal state and set raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		// Fallback: just return default
		return defaultIdx
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	selected := defaultIdx
	numOptions := len(options)
	// Total lines: 1 prompt + numOptions + 1 hint line
	totalLines := numOptions + 1

	renderMenu := func() {
		// Move to start of line and clear
		fmt.Fprint(os.Stderr, "\r\033[K")

		// Print prompt
		fmt.Fprintf(os.Stderr, "%s%s?%s %s\n\r", Bold, Yellow, Reset, prompt)
		fmt.Fprint(os.Stderr, "\033[K")

		// Print each option
		for i, opt := range options {
			if i == selected {
				fmt.Fprintf(os.Stderr, "  %s▸ %s%s%s\n\r", Cyan, Bold, opt, Reset)
			} else {
				fmt.Fprintf(os.Stderr, "    %s\n\r", opt)
			}
			fmt.Fprint(os.Stderr, "\033[K")
		}

		fmt.Fprintf(os.Stderr, "%s(Use ↑/↓ arrows to select, Enter to confirm)%s\r", Magenta, Reset)

		// Move cursor up to start position (totalLines up from hint line)
		fmt.Fprintf(os.Stderr, "\033[%dA", totalLines)
	}

	// Initial render
	fmt.Fprintln(os.Stderr) // Add space before dialog
	renderMenu()

	// Read input
	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			break
		}

		if n == 1 {
			switch buf[0] {
			case 13, 10: // Enter key
				// Move cursor down past the dialog and clear
				fmt.Fprintf(os.Stderr, "\033[%dB\r\033[K", totalLines+1)
				return selected
			case 3: // Ctrl+C - exit immediately
				fmt.Fprintf(os.Stderr, "\033[%dB\r\033[K", totalLines+1)
				term.Restore(int(os.Stdin.Fd()), oldState)
				os.Exit(130) // Standard exit code for Ctrl+C
			case 'k', 'K': // vim-style up
				if selected > 0 {
					selected--
					renderMenu()
				}
			case 27: // ESC key - cancel
				fmt.Fprintf(os.Stderr, "\033[%dB\r\033[K", totalLines+1)
				return -1
			case 'j', 'J': // vim-style down
				if selected < numOptions-1 {
					selected++
					renderMenu()
				}
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == 91 {
			// Arrow key escape sequence
			switch buf[2] {
			case 65: // Up arrow
				if selected > 0 {
					selected--
					renderMenu()
				}
			case 66: // Down arrow
				if selected < numOptions-1 {
					selected++
					renderMenu()
				}
			}
		}
	}

	return selected
}

// Success prints a success message to stderr
func Success(msg string) {
	fmt.Fprintf(os.Stderr, "%s%s %s%s\n", Green, IconSuccess, msg, Reset)
}

// Error prints an error message to stderr
func Error(msg string) {
	fmt.Fprintf(os.Stderr, "%s%s %s%s\n", Red, IconError, msg, Reset)
}

// Warn prints a warning message to stderr
func Warn(msg string) {
	fmt.Fprintf(os.Stderr, "%s%s %s%s\n", Yellow, IconWarning, msg, Reset)
}

// Info prints an info message to stderr
func Info(msg string) {
	fmt.Fprintf(os.Stderr, "%s%s %s%s\n", Blue, IconInfo, msg, Reset)
}

// Prompt asks for text input with a prompt and optional default value
// Returns the user input or the default if empty input is given
// Prompt asks the user a question and returns their answer, or `defaultVal`
// if they pressed Enter. Honors YesMode: under MCP / -y the function returns
// the default immediately rather than blocking on readline against a stdin
// that no terminal will ever feed. Without this guard, MCP tool calls that
// route through Prompt — most notably `ezstack_pr_create` without a `title`
// arg — hang forever.
func Prompt(prompt, defaultVal string) string {
	if YesMode {
		fmt.Fprintf(os.Stderr, "%s%s?%s %s %s→ %q%s\n", Bold, Yellow, Reset, prompt, Green, defaultVal, Reset)
		return defaultVal
	}
	return activeBackend.Prompt(prompt, defaultVal)
}

func (t *TerminalBackend) Prompt(prompt, defaultVal string) string {
	var rlPrompt string
	if defaultVal != "" {
		rlPrompt = fmt.Sprintf("%s%s?%s %s [%s]: ", Bold, Yellow, Reset, prompt, defaultVal)
	} else {
		rlPrompt = fmt.Sprintf("%s%s?%s %s: ", Bold, Yellow, Reset, prompt)
	}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          rlPrompt,
		InterruptPrompt: "^C",
		EOFPrompt:       "",
		Stdin:           os.Stdin,
		Stdout:          os.Stderr,
		Stderr:          os.Stderr,
	})
	if err != nil {
		// Fallback to basic input if readline fails
		tty, err := os.Open("/dev/tty")
		if err != nil {
			tty = os.Stdin
		} else {
			defer tty.Close()
		}
		reader := bufio.NewReader(tty)
		fmt.Fprint(os.Stderr, rlPrompt)
		response, err := reader.ReadString('\n')
		if err != nil {
			return defaultVal
		}
		response = strings.TrimSpace(response)
		if response == "" {
			return defaultVal
		}
		return response
	}
	defer rl.Close()

	line, err := rl.Readline()
	if err != nil {
		return defaultVal
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}

// PromptPath asks for a file path with tab completion support
// Returns the user input or the default if empty input is given. Like
// Prompt, returns the default immediately under YesMode so MCP / -y callers
// don't hang on a missing TTY.
func PromptPath(promptText, defaultVal string) string {
	if YesMode {
		fmt.Fprintf(os.Stderr, "%s%s?%s %s %s→ %q%s\n", Bold, Yellow, Reset, promptText, Green, defaultVal, Reset)
		return defaultVal
	}
	// Print the question on its own line first
	fmt.Fprintf(os.Stderr, "%s%s?%s %s\n", Bold, Yellow, Reset, promptText)

	// Build the actual readline prompt (just the hint line)
	var promptStr string
	if defaultVal != "" {
		promptStr = fmt.Sprintf("%s(Enter for %s)%s: ", Gray, defaultVal, Reset)
	} else {
		promptStr = ": "
	}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          promptStr,
		AutoComplete:    readline.NewPrefixCompleter(pathCompleterFunc("")),
		InterruptPrompt: "^C",
		EOFPrompt:       "",
		Stdin:           os.Stdin,
		Stdout:          os.Stderr,
		Stderr:          os.Stderr,
	})
	if err != nil {
		// Fallback to regular prompt if readline fails
		return Prompt(promptText, defaultVal)
	}
	defer rl.Close()

	// Set custom completer that updates dynamically
	rl.Config.AutoComplete = &pathCompleter{}

	line, err := rl.Readline()
	if err != nil {
		return defaultVal
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}

// pathCompleter implements readline.AutoCompleter for file path completion
type pathCompleter struct{}

func (p *pathCompleter) Do(line []rune, pos int) (newLine [][]rune, length int) {
	lineStr := string(line[:pos])

	// Expand ~ to home directory
	searchPath := lineStr
	if strings.HasPrefix(searchPath, "~") {
		home, _ := os.UserHomeDir()
		searchPath = home + searchPath[1:]
	}

	// Get directory and prefix
	dir := searchPath
	prefix := ""
	if !strings.HasSuffix(searchPath, "/") {
		dir = filepath.Dir(searchPath)
		prefix = filepath.Base(searchPath)
	}

	// Read directory entries
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0
	}

	var matches [][]rune
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, prefix) {
			suffix := name[len(prefix):]
			if entry.IsDir() {
				suffix += "/"
			}
			matches = append(matches, []rune(suffix))
		}
	}

	return matches, len(prefix)
}

// pathCompleterFunc is a helper for readline.NewPrefixCompleter
func pathCompleterFunc(prefix string) *readline.PrefixCompleter {
	return readline.PcItem(prefix)
}

// ErrPromptRequiredInYesMode is returned (panic-free) by callers that opt
// into the error-returning variant of PromptRequired. Direct PromptRequired
// callers under YesMode get an os.Exit since they have no way to recover.
var ErrPromptRequiredInYesMode = errors.New("required prompt cannot be answered in non-interactive mode")

// PromptRequired asks for text input and keeps asking until a non-empty
// value is provided. Under YesMode there is no human to keep asking, so the
// loop would spin forever on closed stdin. Print a clear diagnostic and exit
// rather than hang the MCP server / scripted run indefinitely.
func PromptRequired(prompt string) string {
	if YesMode {
		fmt.Fprintf(os.Stderr,
			"ezs: error: required prompt %q cannot be answered in non-interactive mode (YesMode/-y/MCP). "+
				"Re-run interactively or pass the value explicitly via the relevant flag.\n", prompt)
		os.Exit(2)
	}
	return activeBackend.PromptRequired(prompt)
}

func (t *TerminalBackend) PromptRequired(prompt string) string {
	for {
		rl, err := readline.NewEx(&readline.Config{
			Prompt:          fmt.Sprintf("%s%s?%s %s: ", Bold, Yellow, Reset, prompt),
			InterruptPrompt: "^C",
			EOFPrompt:       "",
			Stdin:           os.Stdin,
			Stdout:          os.Stderr,
			Stderr:          os.Stderr,
		})
		if err != nil {
			// Fallback to basic input if readline fails
			tty, err := os.Open("/dev/tty")
			if err != nil {
				tty = os.Stdin
			}
			reader := bufio.NewReader(tty)
			fmt.Fprintf(os.Stderr, ": ")
			response, readErr := reader.ReadString('\n')
			if tty != os.Stdin {
				tty.Close()
			}
			if readErr != nil {
				continue
			}
			response = strings.TrimSpace(response)
			if response != "" {
				return response
			}
			fmt.Fprintf(os.Stderr, "%s  (required)%s\n", Red, Reset)
			continue
		}

		line, err := rl.Readline()
		rl.Close()
		if err != nil {
			// Ctrl+C should exit
			fmt.Fprintln(os.Stderr)
			os.Exit(130)
		}
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
		fmt.Fprintf(os.Stderr, "%s  (required)%s\n", Red, Reset)
	}
}

// SelectOption uses fzf to select from a list of options
// Returns the 0-based index of the selected option
func SelectOption(options []string, prompt string) (int, error) {
	return activeBackend.SelectOption(options, prompt)
}

func (t *TerminalBackend) SelectOption(options []string, prompt string) (int, error) {
	return SelectOptionWithSuggested(options, prompt, -1)
}

// SelectOptionWithSuggested uses fzf to select from a list of options.
// suggestedIdx is the 0-based index of the suggested option (-1 for none).
// The suggested option will be marked with "(suggested)" and appear first.
// Returns the 0-based index of the selected option.
//
// Under YesMode it auto-resolves: if a suggested option is provided, that's
// the answer; if a single option is the only choice, return it; otherwise
// fail loudly so MCP / -y callers surface a structured error rather than
// hanging on fzf with no TTY.
func SelectOptionWithSuggested(options []string, prompt string, suggestedIdx int) (int, error) {
	if len(options) == 0 {
		return -1, fmt.Errorf("no options to select from")
	}
	if YesMode {
		if suggestedIdx >= 0 && suggestedIdx < len(options) {
			return suggestedIdx, nil
		}
		if len(options) == 1 {
			return 0, nil
		}
		return -1, fmt.Errorf("multiple options for %q (%s) and no suggested default — provide an explicit choice; interactive selection is unavailable in -y / MCP mode", prompt, strings.Join(options, ", "))
	}

	var input strings.Builder
	// If there's a suggested option, put it first
	if suggestedIdx >= 0 && suggestedIdx < len(options) {
		input.WriteString(fmt.Sprintf("%d. %s %s(suggested)%s\n", suggestedIdx+1, options[suggestedIdx], Gray, Reset))
		for i, opt := range options {
			if i != suggestedIdx {
				input.WriteString(fmt.Sprintf("%d. %s\n", i+1, opt))
			}
		}
	} else {
		for i, opt := range options {
			input.WriteString(fmt.Sprintf("%d. %s\n", i+1, opt))
		}
	}

	selected, err := runFzf(input.String(), prompt)
	if err != nil {
		return -1, err
	}

	// Parse the selected option number
	var idx int
	if _, err := fmt.Sscanf(selected, "%d.", &idx); err != nil {
		return -1, fmt.Errorf("failed to parse selection")
	}

	return idx - 1, nil
}

// SelectOptionWithBack uses fzf to select from a list of options with a back option.
// Returns the 0-based index of the selected option, or ErrBack if back was selected.
// The back option is displayed as an unnumbered "← back" at the end of the list.
//
// As with SelectStack and SelectBranch, the YesMode guard lives on the
// TerminalBackend method (below), not here. MCPBackend.SelectOptionWithBack
// uses elicitation rather than fzf and does not hang on a missing TTY,
// so a package-level guard would short-circuit the elicitation path
// and break MCP tools that depend on user disambiguation.
func SelectOptionWithBack(options []string, prompt string) (int, error) {
	return activeBackend.SelectOptionWithBack(options, prompt)
}

func (t *TerminalBackend) SelectOptionWithBack(options []string, prompt string) (int, error) {
	if len(options) == 0 {
		return -1, fmt.Errorf("no options to select from")
	}
	if YesMode {
		if len(options) == 1 {
			return 0, nil
		}
		return -1, fmt.Errorf("multiple options for %q (%s) — provide an explicit choice; interactive selection is unavailable in -y / MCP mode", prompt, strings.Join(options, ", "))
	}

	var input strings.Builder
	for i, opt := range options {
		input.WriteString(fmt.Sprintf("%d. %s\n", i+1, opt))
	}
	// Add unnumbered back option
	backOption := fmt.Sprintf("%s  back", IconBack)
	input.WriteString(backOption + "\n")

	selected, err := runFzf(input.String(), prompt)
	if err != nil {
		return -1, err
	}

	// Check if back was selected
	if strings.HasPrefix(selected, IconBack) {
		return -1, ErrBack
	}

	// Parse the selected option number
	var idx int
	if _, err := fmt.Sscanf(selected, "%d.", &idx); err != nil {
		return -1, fmt.Errorf("failed to parse selection")
	}

	return idx - 1, nil
}
