package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

// EditWithEditor opens the user's preferred editor with the initial content
// and returns the edited content. If the user saves and exits, the content is returned.
// If the user aborts (empty file or error), an error is returned.
// The editor is determined by $EDITOR, $VISUAL, or falls back to "vim".
//
// Under YesMode there's no controlling terminal for $EDITOR to draw on, so
// the editor would hang indefinitely. Skip it and return the initial
// content unchanged — MCP / -y callers that want to edit should pass the
// content via the relevant flag instead.
func EditWithEditor(initialContent, fileExtension string) (string, error) {
	if YesMode {
		fmt.Fprintf(os.Stderr, "%sezs: skipping interactive editor in non-interactive mode (using initial content)%s\n", Gray, Reset)
		return strings.TrimSpace(initialContent), nil
	}
	// Belt-and-braces: even when YesMode is off, an MCP-style invocation
	// has no TTY for $EDITOR to draw on. Detect that and short-circuit
	// rather than launch vim against /dev/null and hang. The TTY check
	// covers stdin/stdout — most editors require both. We deliberately
	// require BOTH to be terminals so a user redirecting only stdout
	// (e.g. `ezs ... > file`) is not misclassified.
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintf(os.Stderr, "%sezs: no terminal attached, skipping interactive editor%s\n", Gray, Reset)
		return strings.TrimSpace(initialContent), nil
	}
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
