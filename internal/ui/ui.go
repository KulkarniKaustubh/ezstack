package ui

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
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

// Nerd Font icons for consistent terminal rendering
const (
	IconSuccess  = "\uf00c" // nf-fa-check
	IconError    = "\uf00d" // nf-fa-times
	IconWarning  = "\uf071" // nf-fa-exclamation_triangle
	IconInfo     = "\uf05a" // nf-fa-info_circle
	IconPending  = "\uf192" // nf-fa-dot_circle_o
	IconArrow    = "\uf061" // nf-fa-arrow_right
	IconPointer  = "\uf0a4" // nf-fa-hand_o_right
	IconBranch   = "\ue725" // nf-dev-git_branch
	IconUp       = "\uf062" // nf-fa-arrow_up
	IconDown     = "\uf063" // nf-fa-arrow_down
	IconSync     = "\uf021" // nf-fa-refresh
	IconCancel   = "\uf05e" // nf-fa-ban
	IconBullet   = "\uf111" // nf-fa-circle
	IconApproved = "\uf058" // nf-fa-check_circle
	IconChanges  = "\uf06a" // nf-fa-exclamation_circle
	IconConflict = "\uf071" // nf-fa-exclamation_triangle
	IconNew      = "\uf067" // nf-fa-plus
	IconPush     = "\uf093" // nf-fa-upload
	IconStack    = "\uf24d" // nf-fa-clone (stack of items)
	IconRocket   = "\uf135" // nf-fa-rocket
	IconBack     = "\uf060" // nf-fa-arrow_left
	IconRemote   = "\uf0c1" // nf-fa-link (for remote branches)
)

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

// BranchStatus contains status information for a branch
type BranchStatus struct {
	PRState     string // "OPEN", "MERGED", "CLOSED", "DRAFT", ""
	CIState     string // "success", "failure", "pending", "none", ""
	CISummary   string // e.g., "3/3 passed"
	Mergeable   string // "MERGEABLE", "CONFLICTING", "UNKNOWN"
	ReviewState string // "APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED", ""
}

// SelectBranch uses fzf to select a branch from a list
func SelectBranch(branches []*config.Branch, prompt string) (*config.Branch, error) {
	return SelectBranchWithStacks(branches, nil, prompt)
}

// SelectBranchWithStacks uses fzf to select a branch with optional stack preview
func SelectBranchWithStacks(branches []*config.Branch, stacks []*config.Stack, prompt string) (*config.Branch, error) {
	if len(branches) == 0 {
		return nil, fmt.Errorf("no branches to select from")
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

// SelectWorktree uses fzf to select a worktree from a list
func SelectWorktree(worktrees []WorktreeInfo, prompt string) (*WorktreeInfo, error) {
	if len(worktrees) == 0 {
		return nil, fmt.Errorf("no worktrees to select from")
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

// SelectWorktreeWithStackPreview uses fzf to select a worktree with stack preview
// For worktrees not in any stack, the preview shows "Not part of a stack"
func SelectWorktreeWithStackPreview(worktrees []WorktreeInfo, stacks []*config.Stack, prompt string) (*WorktreeInfo, error) {
	if len(worktrees) == 0 {
		return nil, fmt.Errorf("no worktrees to select from")
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

// SelectStack uses fzf to select a stack
func SelectStack(stacks []*config.Stack, prompt string) (*config.Stack, error) {
	if len(stacks) == 0 {
		return nil, fmt.Errorf("no stacks to select from")
	}

	var input strings.Builder
	for _, s := range stacks {
		hashDisplay := s.Hash
		if hashDisplay == "" {
			hashDisplay = s.Name
		}
		input.WriteString(fmt.Sprintf("%s (%d branches)\n", hashDisplay, len(s.Branches)))
	}

	selected, err := runFzf(input.String(), prompt)
	if err != nil {
		return nil, err
	}

	parts := strings.SplitN(selected, " ", 2)
	if len(parts) == 0 {
		return nil, fmt.Errorf("no stack selected")
	}
	selectedID := parts[0]

	for _, s := range stacks {
		hashDisplay := s.Hash
		if hashDisplay == "" {
			hashDisplay = s.Name
		}
		if hashDisplay == selectedID {
			return s, nil
		}
	}

	return nil, fmt.Errorf("stack not found: %s", selectedID)
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
	if len(name) <= maxWidth {
		return name
	}
	if maxWidth <= 3 {
		return name[:maxWidth]
	}
	return name[:maxWidth-3] + "..."
}

// sortBranchesTopologically sorts branches so parents come before children
// This ensures the display shows the correct parent -> child order
func sortBranchesTopologically(branches []*config.Branch) []*config.Branch {
	return config.SortBranchesTopologically(branches)
}

// formatStackString formats a stack as a string for fzf preview (with escape codes)
// Always sorts branches topologically (parent -> child order)
func formatStackString(stack *config.Stack, currentBranch string) string {
	// Use escape codes that echo -e can interpret for fzf preview
	bold := "\\x1b[1m"
	reset := "\\x1b[0m"
	strikethrough := "\\x1b[9m"
	green := "\\x1b[32m"
	yellow := "\\x1b[33m"
	gray := "\\x1b[90m"
	cyan := "\\x1b[36m"
	magenta := "\\x1b[35m"

	var output strings.Builder
	hashDisplay := stack.Hash
	if hashDisplay == "" {
		hashDisplay = stack.Name
	}
	output.WriteString(fmt.Sprintf("%s%s Stack %s%s\\n\\n", bold, cyan, hashDisplay, reset))

	// Sort branches topologically (parent -> child order)
	sortedBranches := sortBranchesTopologically(stack.Branches)

	// Calculate max widths
	maxNameWidth := 0
	maxPRWidth := 0
	hasRemoteBranches := false
	for _, b := range sortedBranches {
		displayName := truncateBranchName(b.Name, MaxBranchNameWidth)
		if w := runewidth.StringWidth(displayName); w > maxNameWidth {
			maxNameWidth = w
		}

		var prText string
		if b.IsMerged {
			prText = fmt.Sprintf("[PR #%d MERGED]", b.PRNumber)
		} else if b.PRNumber > 0 {
			prText = fmt.Sprintf("[PR #%d]", b.PRNumber)
		} else {
			prText = "[no PR]"
		}
		if w := runewidth.StringWidth(prText); w > maxPRWidth {
			maxPRWidth = w
		}

		if b.IsRemote {
			hasRemoteBranches = true
		}
	}

	maxParentWidth := 0
	for _, b := range sortedBranches {
		parentText := fmt.Sprintf("(%s %s)", IconArrow, b.Parent)
		if w := runewidth.StringWidth(parentText); w > maxParentWidth {
			maxParentWidth = w
		}
	}

	// Format each branch
	for i, b := range sortedBranches {
		var prefix string
		color := ""
		if b.Name == currentBranch {
			prefix = "> "
			color = green
		} else {
			prefix = "  "
		}

		connector := "├──"
		if i == len(sortedBranches)-1 {
			connector = "└──"
		}

		displayName := truncateBranchName(b.Name, MaxBranchNameWidth)
		paddedName := padRight(displayName, maxNameWidth)

		var prText, prColor string
		if b.IsMerged {
			prText = fmt.Sprintf("[PR #%d MERGED]", b.PRNumber)
			prColor = cyan
		} else if b.PRNumber > 0 {
			prText = fmt.Sprintf("[PR #%d]", b.PRNumber)
			prColor = yellow
		} else {
			prText = "[no PR]"
			prColor = gray
		}
		paddedPRText := padRight(prText, maxPRWidth)

		parentText := fmt.Sprintf("(%s %s)", IconArrow, b.Parent)
		paddedParent := padRight(parentText, maxParentWidth)

		remoteTag := ""
		if hasRemoteBranches && b.IsRemote {
			remoteTag = fmt.Sprintf("  %s%s[remote]%s", magenta, IconRemote, reset)
		}

		if b.IsMerged {
			output.WriteString(fmt.Sprintf("%s%s%s%s %s%s%s  %s%s%s  %s%s%s\\n",
				strikethrough, prefix, color, connector, bold, paddedName, reset+strikethrough,
				prColor, paddedPRText, reset+strikethrough, paddedParent, reset, remoteTag))
		} else {
			output.WriteString(fmt.Sprintf("%s%s%s %s%s%s  %s%s%s  %s%s%s\\n",
				prefix, color, connector, bold, paddedName, reset,
				prColor, paddedPRText, reset, paddedParent, reset, remoteTag))
		}
	}

	return output.String()
}

// PrintStack prints a visual representation of a stack, always sorting topologically
// If showStatus is true, includes CI/PR status column
// Column layout:
// - showStatus=false: 4 columns - branch name, pr number, parent branch, remote tag
// - showStatus=true: 5 columns - branch name, pr number, ci status, parent branch, remote tag
func PrintStack(stack *config.Stack, currentBranch string, showStatus bool, statusMap map[string]*BranchStatus) {
	hashDisplay := stack.Hash
	if hashDisplay == "" {
		hashDisplay = stack.Name
	}
	fmt.Fprintf(os.Stderr, "\n%s%s Stack %s%s\n\n", Bold, Cyan, hashDisplay, Reset)

	// Sort branches topologically (parent -> child order)
	sortedBranches := sortBranchesTopologically(stack.Branches)

	// Calculate max widths for alignment using runewidth
	maxNameWidth := 0
	maxPRWidth := 0
	maxStatusWidth := 0
	maxParentWidth := 0
	hasRemoteBranches := false

	for _, branch := range sortedBranches {
		name := truncateBranchName(branch.Name, MaxBranchNameWidth)
		if w := runewidth.StringWidth(name); w > maxNameWidth {
			maxNameWidth = w
		}

		prText := getPRText(branch, statusMap)
		if w := runewidth.StringWidth(prText); w > maxPRWidth {
			maxPRWidth = w
		}

		if showStatus && statusMap != nil {
			statusText := getStatusText(branch, statusMap)
			if w := runewidth.StringWidth(statusText); w > maxStatusWidth {
				maxStatusWidth = w
			}
		}

		parentText := fmt.Sprintf("(%s %s)", IconArrow, branch.Parent)
		if w := runewidth.StringWidth(parentText); w > maxParentWidth {
			maxParentWidth = w
		}

		if branch.IsRemote {
			hasRemoteBranches = true
		}
	}

	for i, branch := range sortedBranches {
		// Check if branch is merged (from cached state or live status)
		isMerged := branch.IsMerged
		if !isMerged && statusMap != nil {
			if status, ok := statusMap[branch.Name]; ok && status != nil {
				isMerged = status.PRState == "MERGED" || status.PRState == "CLOSED"
			}
		}

		// Pointer for current branch (fixed 2-char width)
		pointer := "  "
		color := ""
		if branch.Name == currentBranch {
			pointer = "> "
			color = Green
		}

		// Tree connector
		connector := "├──"
		if i == len(sortedBranches)-1 {
			connector = "└──"
		}

		// Branch name (truncated and padded)
		name := truncateBranchName(branch.Name, MaxBranchNameWidth)
		paddedName := padRight(name, maxNameWidth)

		// PR info (with color and hyperlink)
		prFormatted := getPRFormatted(branch, statusMap, maxPRWidth)

		// Parent info (padded)
		parentInfo := fmt.Sprintf("(%s %s)", IconArrow, branch.Parent)
		paddedParent := padRight(parentInfo, maxParentWidth)

		// Remote indicator (separate column at the end)
		remoteTag := ""
		if hasRemoteBranches {
			if branch.IsRemote {
				remoteTag = fmt.Sprintf("  %s%s[remote]%s", Magenta, IconRemote, Reset)
			}
		}

		if showStatus && statusMap != nil {
			// 5 columns: branch, PR, status, parent, remote
			statusText := getStatusText(branch, statusMap)
			paddedStatus := padRight(statusText, maxStatusWidth)
			statusColored := getStatusIcons(branch, statusMap)
			// Use paddedStatus length but display statusColored (with colors)
			statusPadding := strings.Repeat(" ", runewidth.StringWidth(paddedStatus)-runewidth.StringWidth(statusText))

			if isMerged {
				// For merged branches, apply strikethrough to entire line
				// Replace all Reset codes with Reset+Strikethrough to maintain strikethrough
				prWithStrike := strings.ReplaceAll(prFormatted, Reset, Reset+Strikethrough)
				statusWithStrike := strings.ReplaceAll(statusColored, Reset, Reset+Strikethrough)
				fmt.Fprintf(os.Stderr, "%s%s%s%s %s%s%s  %s  %s%s  %s%s%s\n",
					Strikethrough, pointer, color, connector, Bold, paddedName, Reset+Strikethrough,
					prWithStrike,
					statusWithStrike, statusPadding,
					paddedParent, Reset, remoteTag)
			} else {
				fmt.Fprintf(os.Stderr, "%s%s%s %s%s%s  %s  %s%s  %s%s%s\n",
					pointer, color, connector, Bold, paddedName, Reset,
					prFormatted,
					statusColored, statusPadding,
					paddedParent, Reset, remoteTag)
			}
		} else {
			// 4 columns: branch, PR, parent, remote
			if isMerged {
				// For merged branches, apply strikethrough to entire line
				prWithStrike := strings.ReplaceAll(prFormatted, Reset, Reset+Strikethrough)
				fmt.Fprintf(os.Stderr, "%s%s%s%s %s%s%s  %s  %s%s%s\n",
					Strikethrough, pointer, color, connector, Bold, paddedName, Reset+Strikethrough,
					prWithStrike,
					paddedParent, Reset, remoteTag)
			} else {
				fmt.Fprintf(os.Stderr, "%s%s%s %s%s%s  %s  %s%s%s\n",
					pointer, color, connector, Bold, paddedName, Reset,
					prFormatted,
					paddedParent, Reset, remoteTag)
			}
		}
	}
	fmt.Fprintln(os.Stderr)
}

// getPRText returns the PR text without color codes
func getPRText(branch *config.Branch, statusMap map[string]*BranchStatus) string {
	if branch.PRNumber == 0 {
		return "[no PR]"
	}
	// Check cached IsMerged first (for ezs ls without API calls)
	if branch.IsMerged {
		return fmt.Sprintf("[PR #%d MERGED]", branch.PRNumber)
	}
	// Then check statusMap (for ezs status with live API data)
	if statusMap != nil {
		if status, ok := statusMap[branch.Name]; ok && status != nil {
			switch status.PRState {
			case "MERGED":
				return fmt.Sprintf("[PR #%d MERGED]", branch.PRNumber)
			case "CLOSED":
				return fmt.Sprintf("[PR #%d CLOSED]", branch.PRNumber)
			case "DRAFT":
				return fmt.Sprintf("[PR #%d DRAFT]", branch.PRNumber)
			}
		}
	}
	return fmt.Sprintf("[PR #%d]", branch.PRNumber)
}

// getPRColor returns the color for PR text
func getPRColor(branch *config.Branch, statusMap map[string]*BranchStatus) string {
	if branch.PRNumber == 0 {
		return Gray
	}
	// Check cached IsMerged first (for ezs ls without API calls)
	if branch.IsMerged {
		return Cyan // Light blue for merged PRs
	}
	// Then check statusMap (for ezs status with live API data)
	if statusMap != nil {
		if status, ok := statusMap[branch.Name]; ok && status != nil {
			switch status.PRState {
			case "MERGED":
				return Cyan // Light blue for merged PRs
			case "CLOSED":
				return Red
			case "DRAFT":
				return Gray
			}
		}
	}
	return Yellow
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
	fmt.Fprintf(os.Stderr, "%s [y/N]: ", prompt)
	var response string
	fmt.Scanln(&response)
	return strings.ToLower(response) == "y" || strings.ToLower(response) == "yes"
}

// ConfirmTUI shows a nice TUI confirmation dialog with arrow key navigation
// Returns true if user confirms, false otherwise
// Yes is selected by default
func ConfirmTUI(prompt string) bool {
	// Check if stdin is a terminal - if not, use simple confirm
	// This enables tests to pipe input
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return Confirm(prompt)
	}

	// Save terminal state and set raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		// Fallback to simple confirm if raw mode fails
		return Confirm(prompt)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	selected := 0 // 0 = Yes, 1 = No (Yes is default)

	renderConfirm := func() {
		// Clear current line and move cursor up
		// Use stderr for all TUI output so stdout stays clean for eval
		fmt.Fprint(os.Stderr, "\r\033[K")

		// Print prompt
		fmt.Fprintf(os.Stderr, "%s%s?%s %s\n\r", Bold, Yellow, Reset, prompt)
		fmt.Fprint(os.Stderr, "\033[K")

		// Print options with nice styling
		yesStyle := fmt.Sprintf("  %s", Reset)
		noStyle := fmt.Sprintf("  %s", Reset)

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

		// Move cursor up 3 lines to prepare for re-render
		fmt.Fprint(os.Stderr, "\033[3A")
	}

	// Initial render
	fmt.Fprintln(os.Stderr) // Add space before dialog
	renderConfirm()

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
				fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
				return selected == 0
			case 3: // Ctrl+C - exit immediately
				fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
				term.Restore(int(os.Stdin.Fd()), oldState)
				os.Exit(130) // Standard exit code for Ctrl+C
			case 27: // Escape (single byte - could be start of escape sequence)
				// Will be handled below if it's a single ESC
			case 'k', 'K': // vim-style up
				selected = 0
				renderConfirm()
			case 'j', 'J': // vim-style down
				selected = 1
				renderConfirm()
			case 'y', 'Y': // Quick yes
				fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
				return true
			case 'n', 'N': // Quick no
				fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
				return false
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == 91 {
			// Arrow key escape sequence
			switch buf[2] {
			case 65: // Up arrow
				selected = 0
				renderConfirm()
			case 66: // Down arrow
				selected = 1
				renderConfirm()
			}
		} else if n == 1 && buf[0] == 27 {
			// Single ESC key - treat as cancel
			fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
			return false
		}
	}

	return selected == 0
}

// ConfirmTUIWithDefault shows a nice TUI confirmation dialog with arrow key navigation
// Returns true if user confirms, false otherwise
// defaultYes controls which option is selected by default
func ConfirmTUIWithDefault(prompt string, defaultYes bool) bool {
	// Check if stdin is a terminal - if not, use simple confirm
	// This enables tests to pipe input
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return Confirm(prompt)
	}

	// Save terminal state and set raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		// Fallback to simple confirm if raw mode fails
		return Confirm(prompt)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	selected := 1 // 0 = Yes, 1 = No
	if defaultYes {
		selected = 0
	}

	renderConfirm := func() {
		// Clear current line and move cursor up
		// Use stderr for all TUI output so stdout stays clean for eval
		fmt.Fprint(os.Stderr, "\r\033[K")

		// Print prompt
		fmt.Fprintf(os.Stderr, "%s%s?%s %s\n\r", Bold, Yellow, Reset, prompt)
		fmt.Fprint(os.Stderr, "\033[K")

		// Print options with nice styling
		yesStyle := fmt.Sprintf("  %s", Reset)
		noStyle := fmt.Sprintf("  %s", Reset)

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

		// Move cursor up 3 lines to prepare for re-render
		fmt.Fprint(os.Stderr, "\033[3A")
	}

	// Initial render
	fmt.Fprintln(os.Stderr) // Add space before dialog
	renderConfirm()

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
				fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
				return selected == 0
			case 3: // Ctrl+C - exit immediately
				fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
				term.Restore(int(os.Stdin.Fd()), oldState)
				os.Exit(130) // Standard exit code for Ctrl+C
			case 27: // Escape (single byte - could be start of escape sequence)
				// Will be handled below if it's a single ESC
			case 'k', 'K': // vim-style up
				selected = 0
				renderConfirm()
			case 'j', 'J': // vim-style down
				selected = 1
				renderConfirm()
			case 'y', 'Y': // Quick yes
				fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
				return true
			case 'n', 'N': // Quick no
				fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
				return false
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == 91 {
			// Arrow key escape sequence
			switch buf[2] {
			case 65: // Up arrow
				selected = 0
				renderConfirm()
			case 66: // Down arrow
				selected = 1
				renderConfirm()
			}
		} else if n == 1 && buf[0] == 27 {
			// Single ESC key - return default
			fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
			return defaultYes
		}
	}

	return selected == 0
}

// SelectTUI shows a TUI selection menu with arrow key navigation
// options is the list of options to display
// prompt is the question to ask
// defaultIdx is the 0-based index of the default selected option
// Returns the 0-based index of the selected option, or -1 if cancelled
func SelectTUI(options []string, prompt string, defaultIdx int) int {
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
		} else if n == 1 && buf[0] == 27 {
			// Single ESC key - cancel
			fmt.Fprintf(os.Stderr, "\033[%dB\r\033[K", totalLines+1)
			return -1
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
func Prompt(prompt, defaultVal string) string {
	// Open /dev/tty directly to ensure we read fresh input from the terminal
	tty, err := os.Open("/dev/tty")
	if err != nil {
		tty = os.Stdin
	} else {
		defer tty.Close()
	}

	reader := bufio.NewReader(tty)
	if defaultVal != "" {
		fmt.Fprintf(os.Stderr, "%s%s?%s %s [%s]: ", Bold, Yellow, Reset, prompt, defaultVal)
	} else {
		fmt.Fprintf(os.Stderr, "%s%s?%s %s: ", Bold, Yellow, Reset, prompt)
	}
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

// PromptPath asks for a file path with tab completion support
// Returns the user input or the default if empty input is given
func PromptPath(promptText, defaultVal string) string {
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

// PromptRequired asks for text input and keeps asking until a non-empty value is provided
func PromptRequired(prompt string) string {
	// Open /dev/tty directly to ensure we read fresh input from the terminal
	// This avoids issues with buffered input from previous fzf sessions
	tty, err := os.Open("/dev/tty")
	if err != nil {
		// Fallback to stdin if /dev/tty is not available
		tty = os.Stdin
	} else {
		defer tty.Close()
	}

	reader := bufio.NewReader(tty)
	for {
		fmt.Fprintf(os.Stderr, "%s%s?%s %s: ", Bold, Yellow, Reset, prompt)
		response, err := reader.ReadString('\n')
		if err != nil {
			continue
		}
		response = strings.TrimSpace(response)
		if response != "" {
			return response
		}
		fmt.Fprintf(os.Stderr, "%s  (required)%s\n", Red, Reset)
	}
}

// SelectOption uses fzf to select from a list of options
// Returns the 0-based index of the selected option
func SelectOption(options []string, prompt string) (int, error) {
	return SelectOptionWithSuggested(options, prompt, -1)
}

// SelectOptionWithSuggested uses fzf to select from a list of options
// suggestedIdx is the 0-based index of the suggested option (-1 for none)
// The suggested option will be marked with "(suggested)" and appear first
// Returns the 0-based index of the selected option
func SelectOptionWithSuggested(options []string, prompt string, suggestedIdx int) (int, error) {
	if len(options) == 0 {
		return -1, fmt.Errorf("no options to select from")
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
func SelectOptionWithBack(options []string, prompt string) (int, error) {
	if len(options) == 0 {
		return -1, fmt.Errorf("no options to select from")
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

// SpinnerDelay is the delay before showing a spinner (only show for slow operations)
const SpinnerDelay = 1500 * time.Millisecond

// Spinner represents a simple loading spinner
type Spinner struct {
	message string
	stop    chan bool
	wg      sync.WaitGroup
	started bool
	mu      sync.Mutex
}

// NewSpinner creates a new spinner with the given message
func NewSpinner(message string) *Spinner {
	return &Spinner{
		message: message,
		stop:    make(chan bool),
	}
}

// Start begins the spinner animation
func (s *Spinner) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		for {
			select {
			case <-s.stop:
				// Clear the spinner line if it was shown
				s.mu.Lock()
				if s.started {
					fmt.Fprintf(os.Stderr, "\r\033[K")
				}
				s.mu.Unlock()
				return
			default:
				s.mu.Lock()
				s.started = true
				s.mu.Unlock()
				fmt.Fprintf(os.Stderr, "\r%s%s%s %s", Cyan, frames[i], Reset, s.message)
				i = (i + 1) % len(frames)
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
}

// Stop stops the spinner
func (s *Spinner) Stop() {
	close(s.stop)
	s.wg.Wait()
}

// DelayedSpinner represents a spinner that only shows after a delay
type DelayedSpinner struct {
	message string
	delay   time.Duration
	spinner *Spinner
	timer   *time.Timer
	mu      sync.Mutex
	stopped bool
}

// NewDelayedSpinner creates a spinner that only shows after the configured delay
func NewDelayedSpinner(message string) *DelayedSpinner {
	return &DelayedSpinner{
		message: message,
		delay:   SpinnerDelay,
	}
}

// Start begins the delayed spinner (will only show if not stopped before delay)
func (ds *DelayedSpinner) Start() {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	ds.timer = time.AfterFunc(ds.delay, func() {
		ds.mu.Lock()
		defer ds.mu.Unlock()
		if !ds.stopped {
			ds.spinner = NewSpinner(ds.message)
			ds.spinner.Start()
		}
	})
}

// Stop stops the delayed spinner
func (ds *DelayedSpinner) Stop() {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	ds.stopped = true
	if ds.timer != nil {
		ds.timer.Stop()
	}
	if ds.spinner != nil {
		ds.spinner.Stop()
	}
}

// WithSpinner runs a function with a delayed spinner
// The spinner only shows if the function takes longer than SpinnerDelay
func WithSpinner(message string, fn func() error) error {
	spinner := NewDelayedSpinner(message)
	spinner.Start()
	defer spinner.Stop()
	return fn()
}

// EditWithEditor opens the user's preferred editor with the initial content
// and returns the edited content. If the user saves and exits, the content is returned.
// If the user aborts (empty file or error), an error is returned.
// The editor is determined by $EDITOR, $VISUAL, or falls back to "vim".
func EditWithEditor(initialContent, fileExtension string) (string, error) {
	// Get the editor from environment
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vim"
	}

	// Create a temporary file with the specified extension
	tmpFile, err := os.CreateTemp("", "ezstack-*"+fileExtension)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	// Write initial content to the file
	if _, err := tmpFile.WriteString(initialContent); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("failed to write to temp file: %w", err)
	}
	tmpFile.Close()

	// Open the editor
	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("editor exited with error: %w", err)
	}

	// Read the edited content
	content, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("failed to read edited file: %w", err)
	}

	result := strings.TrimSpace(string(content))
	return result, nil
}
