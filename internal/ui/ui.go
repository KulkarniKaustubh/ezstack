package ui

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ezstack/ezstack/internal/config"
	"golang.org/x/term"
)

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
)

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
	// \x1b is the hex escape for ESC character
	bold := "\\x1b[1m"
	reset := "\\x1b[0m"
	green := "\\x1b[32m"
	yellow := "\\x1b[33m"
	gray := "\\x1b[90m"
	cyan := "\\x1b[36m"

	// Calculate max branch name width for alignment (capped at MaxBranchNameWidth)
	maxNameLen := 0
	for _, b := range targetStack.Branches {
		nameLen := len(b.Name)
		if nameLen > MaxBranchNameWidth {
			nameLen = MaxBranchNameWidth
		}
		if nameLen > maxNameLen {
			maxNameLen = nameLen
		}
	}

	// Generate preview matching PrintStack output
	var preview strings.Builder
	preview.WriteString(fmt.Sprintf("%s%s Stack: %s%s\\n\\n", bold, cyan, targetStack.Name, reset))

	for i, b := range targetStack.Branches {
		prefix := " "
		color := ""
		if b.Name == branch.Name {
			prefix = IconPointer
			color = green
		}

		// Truncate and pad branch name for alignment
		displayName := truncateBranchName(b.Name, MaxBranchNameWidth)
		paddedName := fmt.Sprintf("%-*s", maxNameLen, displayName)

		prInfo := ""
		if b.PRNumber > 0 {
			prInfo = fmt.Sprintf(" %s[PR #%d]%s", yellow, b.PRNumber, reset)
		} else {
			prInfo = fmt.Sprintf(" %s[no PR]%s", gray, reset)
		}

		connector := "├──"
		if i == len(targetStack.Branches)-1 {
			connector = "└──"
		}

		preview.WriteString(fmt.Sprintf("%s%s%s %s%s%s%s (%s %s)%s\\n",
			prefix, color, connector, bold, paddedName, reset, prInfo, IconArrow, b.Parent, reset))
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
func PrintStackWithStatus(stack *config.Stack, currentBranch string, statusMap map[string]*BranchStatus) {
	fmt.Fprintf(os.Stderr, "\n%s%s Stack: %s%s\n\n", Bold, Cyan, stack.Name, Reset)

	// Calculate max branch name width for alignment (capped at MaxBranchNameWidth)
	maxNameLen := 0
	for _, branch := range stack.Branches {
		nameLen := len(branch.Name)
		if nameLen > MaxBranchNameWidth {
			nameLen = MaxBranchNameWidth
		}
		if nameLen > maxNameLen {
			maxNameLen = nameLen
		}
	}

	for i, branch := range stack.Branches {
		prefix := " "
		color := ""
		if branch.Name == currentBranch {
			prefix = IconPointer
			color = Green
		}

		// Truncate and pad branch name for alignment
		displayName := truncateBranchName(branch.Name, MaxBranchNameWidth)
		paddedName := fmt.Sprintf("%-*s", maxNameLen, displayName)

		// Build PR info with status
		prInfo := ""
		statusInfo := ""
		if branch.PRNumber == 0 {
			prInfo = fmt.Sprintf(" %s[no PR]%s", Gray, Reset)
		} else {
			prInfo = fmt.Sprintf(" %s[PR #%d]%s", Yellow, branch.PRNumber, Reset)

			// Add status if available
			if statusMap != nil {
				if status, ok := statusMap[branch.Name]; ok && status != nil {
					// PR state (merged/open/closed/draft)
					switch status.PRState {
					case "MERGED":
						prInfo = fmt.Sprintf(" %s[PR #%d MERGED]%s", Magenta, branch.PRNumber, Reset)
					case "CLOSED":
						prInfo = fmt.Sprintf(" %s[PR #%d CLOSED]%s", Red, branch.PRNumber, Reset)
					case "DRAFT":
						prInfo = fmt.Sprintf(" %s[PR #%d DRAFT]%s", Gray, branch.PRNumber, Reset)
					}

					// CI status
					ciIcon := ""
					ciColor := ""
					switch status.CIState {
					case "success":
						ciIcon = IconSuccess
						ciColor = Green
					case "failure":
						ciIcon = IconError
						ciColor = Red
					case "pending":
						ciIcon = IconPending
						ciColor = Yellow
					}
					if ciIcon != "" {
						statusInfo += fmt.Sprintf(" %s%s%s", ciColor, ciIcon, Reset)
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
				}
			}
		}

		// Draw the tree structure
		connector := "├──"
		if i == len(stack.Branches)-1 {
			connector = "└──"
		}

		fmt.Fprintf(os.Stderr, "%s%s%s %s%s%s%s%s (%s %s)%s\n",
			prefix, color, connector, Bold, paddedName, Reset, prInfo, statusInfo, IconArrow, branch.Parent, Reset)
	}
	fmt.Fprintln(os.Stderr)
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
