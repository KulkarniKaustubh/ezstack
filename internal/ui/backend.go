package ui

import "github.com/KulkarniKaustubh/ezstack/v4/internal/config"

// Backend defines the interface for interactive user prompts.
// Implementations include the terminal TUI (default) and MCP elicitation.
type Backend interface {
	// Confirm asks a yes/no question, defaulting to yes.
	Confirm(prompt string) bool

	// ConfirmWithDefault asks a yes/no question with a configurable default.
	ConfirmWithDefault(prompt string, defaultYes bool) bool

	// Select shows a selection menu and returns the 0-based index, or -1 if cancelled.
	Select(options []string, prompt string, defaultIdx int) int

	// SelectOption presents options via fzf-style selection and returns the 0-based index.
	SelectOption(options []string, prompt string) (int, error)

	// SelectOptionWithBack is like SelectOption but includes a "back" option.
	// Returns ErrBack if back was selected.
	SelectOptionWithBack(options []string, prompt string) (int, error)

	// SelectBranch selects a branch from a list.
	SelectBranch(branches []*config.Branch, prompt string) (*config.Branch, error)

	// SelectStack selects a stack from a list.
	SelectStack(stacks []*config.Stack, prompt string) (*config.Stack, error)

	// Prompt asks for text input with an optional default value.
	Prompt(prompt, defaultVal string) string

	// PromptRequired asks for text input, requiring a non-empty response.
	PromptRequired(prompt string) string
}

// TerminalBackend is the default Backend backed by the terminal TUI.
type TerminalBackend struct{}

// activeBackend is the current UI backend. Defaults to TerminalBackend.
var activeBackend Backend = &TerminalBackend{}

// SetBackend replaces the active UI backend (e.g. to MCPBackend for MCP mode).
func SetBackend(b Backend) {
	activeBackend = b
}
