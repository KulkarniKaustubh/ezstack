package ui

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ezstack/ezstack/internal/config"
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
	Reset   = "\033[0m"
	Bold    = "\033[1m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	Gray    = "\033[90m"
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

	// Use escape codes that echo -e can interpret
	bold := "\\x1b[1m"
	reset := "\\x1b[0m"
	green := "\\x1b[32m"
	yellow := "\\x1b[33m"
	gray := "\\x1b[90m"
	cyan := "\\x1b[36m"
	magenta := "\\x1b[35m"

	// Calculate max widths using runewidth
	maxNameWidth := 0
	maxPRWidth := 0
	maxParentWidth := 0
	hasRemoteBranches := false
	for _, b := range targetStack.Branches {
		displayName := truncateBranchName(b.Name, MaxBranchNameWidth)
		if w := runewidth.StringWidth(displayName); w > maxNameWidth {
			maxNameWidth = w
		}

		var prText string
		if b.PRNumber > 0 {
			prText = fmt.Sprintf("[PR #%d]", b.PRNumber)
		} else {
			prText = "[no PR]"
		}
		if w := runewidth.StringWidth(prText); w > maxPRWidth {
			maxPRWidth = w
		}

		parentText := fmt.Sprintf("(%s %s)", IconArrow, b.Parent)
		if w := runewidth.StringWidth(parentText); w > maxParentWidth {
			maxParentWidth = w
		}

		if b.IsRemote {
			hasRemoteBranches = true
		}
	}

	// Generate preview matching PrintStack output
	var preview strings.Builder
	preview.WriteString(fmt.Sprintf("%s%s Stack: %s%s\\n\\n", bold, cyan, targetStack.Name, reset))

	for i, b := range targetStack.Branches {
		// Build prefix (use ASCII ">" for guaranteed alignment)
		var prefix string
		color := ""
		if b.Name == branch.Name {
			prefix = "> "
			color = green
		} else {
			prefix = "  "
		}

		connector := "├──"
		if i == len(targetStack.Branches)-1 {
			connector = "└──"
		}

		// Truncate and pad branch name
		displayName := truncateBranchName(b.Name, MaxBranchNameWidth)
		paddedName := padRight(displayName, maxNameWidth)

		// Build PR info and pad it
		var prText, prColor string
		if b.PRNumber > 0 {
			prText = fmt.Sprintf("[PR #%d]", b.PRNumber)
			prColor = yellow
		} else {
			prText = "[no PR]"
			prColor = gray
		}
		paddedPRText := padRight(prText, maxPRWidth)

		// Parent info (padded)
		parentText := fmt.Sprintf("(%s %s)", IconArrow, b.Parent)
		paddedParent := padRight(parentText, maxParentWidth)

		// Remote tag (separate column at the end)
		remoteTag := ""
		if hasRemoteBranches && b.IsRemote {
			remoteTag = fmt.Sprintf("  %s%s[remote]%s", magenta, IconRemote, reset)
		}

		preview.WriteString(fmt.Sprintf("%s%s%s %s%s%s  %s%s%s  %s%s%s\\n",
			prefix, color, connector, bold, paddedName, reset,
			prColor, paddedPRText, reset, paddedParent, reset, remoteTag))
	}

	return preview.String()
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

// SelectStack uses fzf to select a stack
func SelectStack(stacks []*config.Stack, prompt string) (*config.Stack, error) {
	if len(stacks) == 0 {
		return nil, fmt.Errorf("no stacks to select from")
	}

	var input strings.Builder
	for _, s := range stacks {
		input.WriteString(fmt.Sprintf("%s (%d branches)\n", s.Name, len(s.Branches)))
	}

	selected, err := runFzf(input.String(), prompt)
	if err != nil {
		return nil, err
	}

	parts := strings.SplitN(selected, " ", 2)
	if len(parts) == 0 {
		return nil, fmt.Errorf("no stack selected")
	}
	stackName := parts[0]

	for _, s := range stacks {
		if s.Name == stackName {
			return s, nil
		}
	}

	return nil, fmt.Errorf("stack not found: %s", stackName)
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

// PrintStack prints a visual representation of a stack to stderr (without status)
func PrintStack(stack *config.Stack, currentBranch string) {
	PrintStackWithStatus(stack, currentBranch, nil)
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

// PrintStackWithStatus prints a visual representation of a stack with PR/CI status
// Column layout:
// - ezs ls (statusMap=nil): 4 columns - branch name, pr number, parent branch, remote tag
// - ezs status: 5 columns - branch name, pr number, ci status, parent branch, remote tag
func PrintStackWithStatus(stack *config.Stack, currentBranch string, statusMap map[string]*BranchStatus) {
	fmt.Fprintf(os.Stderr, "\n%s%s Stack: %s%s\n\n", Bold, Cyan, stack.Name, Reset)

	// Calculate max widths for alignment using runewidth
	maxNameWidth := 0
	maxPRWidth := 0
	maxStatusWidth := 0
	maxParentWidth := 0
	hasRemoteBranches := false

	for _, branch := range stack.Branches {
		name := truncateBranchName(branch.Name, MaxBranchNameWidth)
		if w := runewidth.StringWidth(name); w > maxNameWidth {
			maxNameWidth = w
		}

		prText := getPRText(branch, statusMap)
		if w := runewidth.StringWidth(prText); w > maxPRWidth {
			maxPRWidth = w
		}

		if statusMap != nil {
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

	for i, branch := range stack.Branches {
		// Pointer for current branch (fixed 2-char width)
		pointer := "  "
		color := ""
		if branch.Name == currentBranch {
			pointer = "> "
			color = Green
		}

		// Tree connector
		connector := "├──"
		if i == len(stack.Branches)-1 {
			connector = "└──"
		}

		// Branch name (truncated and padded)
		name := truncateBranchName(branch.Name, MaxBranchNameWidth)
		paddedName := padRight(name, maxNameWidth)

		// PR info (padded)
		prText := getPRText(branch, statusMap)
		paddedPR := padRight(prText, maxPRWidth)
		prColor := getPRColor(branch, statusMap)

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

		if statusMap != nil {
			// 5 columns: branch, PR, status, parent, remote
			statusText := getStatusText(branch, statusMap)
			paddedStatus := padRight(statusText, maxStatusWidth)
			statusColored := getStatusIcons(branch, statusMap)
			// Use paddedStatus length but display statusColored (with colors)
			statusPadding := strings.Repeat(" ", runewidth.StringWidth(paddedStatus)-runewidth.StringWidth(statusText))

			fmt.Fprintf(os.Stderr, "%s%s%s %s%s%s  %s%s%s  %s%s  %s%s%s\n",
				pointer, color, connector, Bold, paddedName, Reset,
				prColor, paddedPR, Reset,
				statusColored, statusPadding,
				paddedParent, Reset, remoteTag)
		} else {
			// 4 columns: branch, PR, parent, remote
			fmt.Fprintf(os.Stderr, "%s%s%s %s%s%s  %s%s%s  %s%s%s\n",
				pointer, color, connector, Bold, paddedName, Reset,
				prColor, paddedPR, Reset,
				paddedParent, Reset, remoteTag)
		}
	}
	fmt.Fprintln(os.Stderr)
}

// getPRText returns the PR text without color codes
func getPRText(branch *config.Branch, statusMap map[string]*BranchStatus) string {
	if branch.PRNumber == 0 {
		return "[no PR]"
	}
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
	if statusMap != nil {
		if status, ok := statusMap[branch.Name]; ok && status != nil {
			switch status.PRState {
			case "MERGED":
				return Magenta
			case "CLOSED":
				return Red
			case "DRAFT":
				return Gray
			}
		}
	}
	return Yellow
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
			case 3, 27: // Ctrl+C or Escape (single byte)
				if n == 1 && buf[0] == 27 {
					// Could be start of escape sequence, try to read more
					// with a short timeout - but for single ESC, treat as cancel
					// We'll check if more bytes follow
				}
				if buf[0] == 3 { // Ctrl+C
					fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
					return false
				}
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
			case 3, 27: // Ctrl+C or Escape (single byte)
				if buf[0] == 3 { // Ctrl+C
					fmt.Fprint(os.Stderr, "\033[4B\r\033[K")
					return defaultYes // Return default on cancel
				}
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
	if len(options) == 0 {
		return -1, fmt.Errorf("no options to select from")
	}

	var input strings.Builder
	for i, opt := range options {
		input.WriteString(fmt.Sprintf("%d. %s\n", i+1, opt))
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
