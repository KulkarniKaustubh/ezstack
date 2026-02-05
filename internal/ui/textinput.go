package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Styles for the path input prompt
var (
	tiPromptStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true) // Pink/magenta ?
	tiLabelStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true) // Bright white
	tiInputStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	tiPlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	tiHintStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))             // Subtle gray for footer
	tiSuggestedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Italic(true) // Cyan for suggested hint
	tiCursorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))             // Match prompt color
	tiChevronStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))             // Match prompt color
	tiBorderStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))             // Slightly brighter border
	tiOptionStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	tiOptionSelectedBg = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("212")).Bold(true)
	tiOptionDimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	tiErrorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
)

// PathValidator is a function that validates a path and returns an error message if invalid
type PathValidator func(path string) error

type pathInputModel struct {
	textInput      textinput.Model
	prompt         string
	hint           string // optional hint text shown below prompt (e.g., "suggested")
	originalValue  string // original default value to detect modifications
	submitted      bool
	cancelled      bool
	width          int
	pathComplete   bool          // whether to enable path completion
	showOptions    bool          // whether to show completion options
	options        []string      // current completion options
	selectedIdx    int           // currently selected option index
	baseDir        string        // base directory for current completion
	lastInputValue string        // track input changes to hide options
	validator      PathValidator // optional validation function
	errorMsg       string        // current validation error message
}

func newPathInputModel(prompt, hint, defaultValue string, pathComplete bool, validator PathValidator) pathInputModel {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 512
	ti.Width = 70
	ti.PromptStyle = lipgloss.NewStyle()
	ti.Prompt = ""
	ti.TextStyle = tiInputStyle
	ti.PlaceholderStyle = tiPlaceholderStyle
	ti.Cursor.Style = tiCursorStyle

	if defaultValue != "" {
		ti.SetValue(defaultValue)
		ti.CursorEnd()
		ti.Placeholder = ""
	}

	return pathInputModel{
		textInput:      ti,
		prompt:         prompt,
		hint:           hint,
		originalValue:  defaultValue,
		width:          80,
		pathComplete:   pathComplete,
		lastInputValue: defaultValue,
		selectedIdx:    -1,
		validator:      validator,
	}
}

func (m pathInputModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m pathInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Clear error when user types
		if msg.Type != tea.KeyEnter {
			m.errorMsg = ""
		}
		switch msg.Type {
		case tea.KeyEnter:
			// If options are shown and one is selected, apply it
			if m.showOptions && m.selectedIdx >= 0 && m.selectedIdx < len(m.options) {
				m.applySelectedOption()
				m.showOptions = false
				m.selectedIdx = -1
				m.errorMsg = ""
				return m, nil
			}
			// Validate before submitting
			if m.validator != nil {
				value := strings.TrimSpace(m.textInput.Value())
				if value != "" {
					if err := m.validator(value); err != nil {
						m.errorMsg = err.Error()
						return m, nil
					}
				}
			}
			m.submitted = true
			return m, tea.Quit
		case tea.KeyCtrlC, tea.KeyEsc:
			if m.showOptions {
				// First Esc closes options
				m.showOptions = false
				m.selectedIdx = -1
				return m, nil
			}
			m.cancelled = true
			return m, tea.Quit
		case tea.KeyTab:
			if m.pathComplete {
				if m.showOptions && len(m.options) > 0 {
					// Tab cycles through options
					m.selectedIdx = (m.selectedIdx + 1) % len(m.options)
					return m, nil
				}
				// Show options on first tab
				m.options, m.baseDir = m.getCompletionOptions()
				if len(m.options) == 1 {
					// Single match: auto-complete it
					m.selectedIdx = 0
					m.applySelectedOption()
					m.showOptions = false
					m.selectedIdx = -1
				} else if len(m.options) > 1 {
					m.showOptions = true
					m.selectedIdx = 0
				}
				return m, nil
			}
		case tea.KeyShiftTab:
			if m.showOptions && len(m.options) > 0 {
				// Shift+Tab cycles backwards
				m.selectedIdx--
				if m.selectedIdx < 0 {
					m.selectedIdx = len(m.options) - 1
				}
				return m, nil
			}
		case tea.KeyUp:
			if m.showOptions && len(m.options) > 0 {
				m.selectedIdx--
				if m.selectedIdx < 0 {
					m.selectedIdx = len(m.options) - 1
				}
				return m, nil
			}
		case tea.KeyDown, tea.KeyCtrlJ:
			if m.showOptions && len(m.options) > 0 {
				m.selectedIdx = (m.selectedIdx + 1) % len(m.options)
				return m, nil
			}
		case tea.KeyCtrlK:
			if m.showOptions && len(m.options) > 0 {
				m.selectedIdx--
				if m.selectedIdx < 0 {
					m.selectedIdx = len(m.options) - 1
				}
				return m, nil
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		if m.width > 100 {
			m.textInput.Width = 80
		} else if m.width > 60 {
			m.textInput.Width = m.width - 10
		} else {
			m.textInput.Width = 50
		}
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)

	// Hide options if user types something
	if m.textInput.Value() != m.lastInputValue {
		m.showOptions = false
		m.selectedIdx = -1
		m.lastInputValue = m.textInput.Value()
	}

	return m, cmd
}

// applySelectedOption applies the currently selected option to the input
func (m *pathInputModel) applySelectedOption() {
	if m.selectedIdx < 0 || m.selectedIdx >= len(m.options) {
		return
	}

	selected := m.options[m.selectedIdx]
	currentValue := m.textInput.Value()

	// Build the completed path
	var completedPath string
	if strings.HasPrefix(currentValue, "~") {
		// Preserve ~ prefix
		home, _ := os.UserHomeDir()
		fullPath := filepath.Join(m.baseDir, selected)
		relPath, err := filepath.Rel(home, fullPath)
		if err == nil && !strings.HasPrefix(relPath, "..") {
			completedPath = "~/" + relPath
		} else {
			completedPath = fullPath
		}
	} else {
		completedPath = filepath.Join(m.baseDir, selected)
	}

	// Add trailing slash
	completedPath += string(filepath.Separator)

	m.textInput.SetValue(completedPath)
	m.textInput.CursorEnd()
	m.lastInputValue = completedPath
}

// getCompletionOptions returns a list of matching directories and the base directory
func (m *pathInputModel) getCompletionOptions() ([]string, string) {
	currentValue := m.textInput.Value()
	if currentValue == "" {
		return nil, ""
	}

	// Expand ~ to home directory
	expandedPath := currentValue
	if strings.HasPrefix(expandedPath, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if expandedPath == "~" {
				expandedPath = home
			} else {
				expandedPath = filepath.Join(home, expandedPath[1:])
			}
		}
	}

	// Get the directory and prefix to complete
	dir := filepath.Dir(expandedPath)
	prefix := filepath.Base(expandedPath)

	// Check if the path ends with a separator (user wants to list directory contents)
	if strings.HasSuffix(currentValue, string(filepath.Separator)) || currentValue == "~" {
		dir = expandedPath
		prefix = ""
	}

	// Read directory entries
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, ""
	}

	// Find matching entries (directories only)
	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			matches = append(matches, name)
		}
	}

	sort.Strings(matches)
	return matches, dir
}

func (m pathInputModel) View() string {
	var sb strings.Builder

	// Top border with rounded corners
	sb.WriteString(tiBorderStyle.Render("  ╭" + strings.Repeat("─", 76) + "╮"))
	sb.WriteString("\n")

	// Empty line for top padding
	sb.WriteString(tiBorderStyle.Render("  │") + strings.Repeat(" ", 76) + tiBorderStyle.Render("│"))
	sb.WriteString("\n")

	// Prompt line with question mark
	promptLine := fmt.Sprintf("  │   %s %s", tiPromptStyle.Render("?"), tiLabelStyle.Render(m.prompt))
	padding := 78 - lipgloss.Width(promptLine) + 2
	if padding < 0 {
		padding = 0
	}
	sb.WriteString(promptLine + strings.Repeat(" ", padding) + tiBorderStyle.Render("│"))
	sb.WriteString("\n")

	// Empty line before input
	sb.WriteString(tiBorderStyle.Render("  │") + strings.Repeat(" ", 76) + tiBorderStyle.Render("│"))
	sb.WriteString("\n")

	// Input line with chevron
	inputLine := fmt.Sprintf("  │   %s %s", tiChevronStyle.Render("›"), m.textInput.View())
	inputPadding := 78 - lipgloss.Width(inputLine) + 2
	if inputPadding < 0 {
		inputPadding = 0
	}
	sb.WriteString(inputLine + strings.Repeat(" ", inputPadding) + tiBorderStyle.Render("│"))
	sb.WriteString("\n")

	// Suggested hint line - only show if value hasn't been modified from original
	if m.hint != "" && m.textInput.Value() == m.originalValue {
		hintLine := fmt.Sprintf("  │      %s", tiSuggestedStyle.Render(m.hint))
		hintPad := 78 - lipgloss.Width(hintLine) + 2
		if hintPad < 0 {
			hintPad = 0
		}
		sb.WriteString(hintLine + strings.Repeat(" ", hintPad) + tiBorderStyle.Render("│"))
		sb.WriteString("\n")
	}

	// Show completion options
	if m.showOptions && len(m.options) > 0 {
		// Empty line before options
		sb.WriteString(tiBorderStyle.Render("  │") + strings.Repeat(" ", 76) + tiBorderStyle.Render("│"))
		sb.WriteString("\n")

		// Show options (max 8, with scroll window if selected is beyond)
		maxShow := 8
		startIdx := 0
		if m.selectedIdx >= maxShow {
			startIdx = m.selectedIdx - maxShow + 1
		}
		endIdx := startIdx + maxShow
		if endIdx > len(m.options) {
			endIdx = len(m.options)
		}

		for i := startIdx; i < endIdx; i++ {
			opt := m.options[i]
			var optLine string
			if i == m.selectedIdx {
				// Highlighted selected option
				optText := tiOptionSelectedBg.Render(" " + opt + "/ ")
				optLine = fmt.Sprintf("  │   %s %s", tiChevronStyle.Render("▸"), optText)
			} else {
				optLine = fmt.Sprintf("  │     %s", tiOptionStyle.Render(opt+"/"))
			}
			optPad := 78 - lipgloss.Width(optLine) + 2
			if optPad < 0 {
				optPad = 0
			}
			sb.WriteString(optLine + strings.Repeat(" ", optPad) + tiBorderStyle.Render("│"))
			sb.WriteString("\n")
		}

		// Show scroll indicator if there are more options
		if len(m.options) > maxShow {
			scrollInfo := fmt.Sprintf("%d/%d", m.selectedIdx+1, len(m.options))
			moreLine := fmt.Sprintf("  │     %s", tiOptionDimStyle.Render(scrollInfo))
			morePad := 78 - lipgloss.Width(moreLine) + 2
			if morePad < 0 {
				morePad = 0
			}
			sb.WriteString(moreLine + strings.Repeat(" ", morePad) + tiBorderStyle.Render("│"))
			sb.WriteString("\n")
		}
	}

	// Show error message if any
	if m.errorMsg != "" {
		// Empty line before error
		sb.WriteString(tiBorderStyle.Render("  │") + strings.Repeat(" ", 76) + tiBorderStyle.Render("│"))
		sb.WriteString("\n")

		// Error message with red color
		errLine := fmt.Sprintf("  │  %s %s", tiErrorStyle.Render("✗"), tiErrorStyle.Render(m.errorMsg))
		errPadding := 78 - lipgloss.Width(errLine) + 2
		if errPadding < 0 {
			errPadding = 0
		}
		sb.WriteString(errLine + strings.Repeat(" ", errPadding) + tiBorderStyle.Render("│"))
		sb.WriteString("\n")
	}

	// Empty line
	// Empty line before footer
	sb.WriteString(tiBorderStyle.Render("  │") + strings.Repeat(" ", 76) + tiBorderStyle.Render("│"))
	sb.WriteString("\n")

	// Footer hint line
	hintText := "enter confirm • esc cancel"
	hintLine := fmt.Sprintf("  │   %s", tiHintStyle.Render(hintText))
	hintPadding := 78 - lipgloss.Width(hintLine) + 2
	if hintPadding < 0 {
		hintPadding = 0
	}
	sb.WriteString(hintLine + strings.Repeat(" ", hintPadding) + tiBorderStyle.Render("│"))
	sb.WriteString("\n")

	// Empty line for bottom padding
	sb.WriteString(tiBorderStyle.Render("  │") + strings.Repeat(" ", 76) + tiBorderStyle.Render("│"))
	sb.WriteString("\n")

	// Bottom border with rounded corners
	sb.WriteString(tiBorderStyle.Render("  ╰" + strings.Repeat("─", 76) + "╯"))
	sb.WriteString("\n")

	return sb.String()
}

// PromptTUI shows a nice Bubble Tea text input prompt
// Returns the user input, or empty string if cancelled
func PromptTUI(prompt, defaultVal string) (string, bool) {
	return PromptPathTUI(prompt, defaultVal, false)
}

// PromptPathTUI shows a Bubble Tea text input with optional path tab completion
func PromptPathTUI(prompt, defaultVal string, pathComplete bool) (string, bool) {
	return PromptPathTUIWithValidatorAndHint(prompt, "", defaultVal, pathComplete, nil)
}

// PromptPathTUIWithValidator shows a Bubble Tea text input with path completion and validation
// The validator is called when the user presses Enter; if it returns an error, it's displayed in-place
func PromptPathTUIWithValidator(prompt, defaultVal string, pathComplete bool, validator PathValidator) (string, bool) {
	return PromptPathTUIWithValidatorAndHint(prompt, "", defaultVal, pathComplete, validator)
}

// PromptPathTUIWithValidatorAndHint shows a Bubble Tea text input with path completion, validation, and a hint
func PromptPathTUIWithValidatorAndHint(prompt, hint, defaultVal string, pathComplete bool, validator PathValidator) (string, bool) {
	model := newPathInputModel(prompt, hint, defaultVal, pathComplete, validator)

	// Run the Bubble Tea program
	p := tea.NewProgram(model, tea.WithOutput(os.Stderr))
	finalModel, err := p.Run()
	if err != nil {
		// Fallback to simple prompt
		return Prompt(prompt, defaultVal), true
	}

	result := finalModel.(pathInputModel)
	if result.cancelled {
		return "", false
	}

	value := strings.TrimSpace(result.textInput.Value())
	if value == "" && defaultVal != "" {
		return defaultVal, true
	}

	return value, true
}

// PromptTUIRequired shows a Bubble Tea text input that requires a non-empty value
func PromptTUIRequired(prompt string) (string, bool) {
	for {
		value, ok := PromptTUI(prompt, "")
		if !ok {
			return "", false
		}
		if value != "" {
			return value, true
		}
		fmt.Fprintf(os.Stderr, "%s  (required)%s\n", Red, Reset)
	}
}
