package ui

import (
	"fmt"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

// ElicitResult holds the response from an MCP elicitation request.
type ElicitResult struct {
	Action  string                 // "accept", "decline", or "cancel"
	Content map[string]interface{} // Form data; present only when Action is "accept"
}

// ElicitFunc sends an elicitation request to the MCP client.
// message is displayed to the user; schema is a JSON Schema for the form.
type ElicitFunc func(message string, schema map[string]interface{}) (*ElicitResult, error)

// MCPBackend implements Backend using MCP elicitation.
type MCPBackend struct {
	Elicit ElicitFunc
}

var _ Backend = (*MCPBackend)(nil)

func (m *MCPBackend) Confirm(prompt string) bool {
	return m.ConfirmWithDefault(prompt, true)
}

func (m *MCPBackend) ConfirmWithDefault(prompt string, defaultYes bool) bool {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"confirm": map[string]interface{}{
				"type":    "boolean",
				"title":   prompt,
				"default": defaultYes,
			},
		},
	}
	result, err := m.Elicit(prompt, schema)
	if err != nil || result.Action != "accept" {
		return defaultYes
	}
	v, ok := result.Content["confirm"].(bool)
	if !ok {
		return defaultYes
	}
	return v
}

func (m *MCPBackend) Select(options []string, prompt string, defaultIdx int) int {
	if len(options) == 0 {
		return -1
	}
	defVal := ""
	if defaultIdx >= 0 && defaultIdx < len(options) {
		defVal = options[defaultIdx]
	}
	schema := enumSchema("selection", options, defVal)
	result, err := m.Elicit(prompt, schema)
	if err != nil || result.Action != "accept" {
		return -1
	}
	return matchOption(result.Content["selection"], options)
}

func (m *MCPBackend) SelectOption(options []string, prompt string) (int, error) {
	if len(options) == 0 {
		return -1, fmt.Errorf("no options to select from")
	}
	schema := enumSchema("selection", options, "")
	result, err := m.Elicit(prompt, schema)
	if err != nil {
		return -1, err
	}
	if result.Action != "accept" {
		return -1, fmt.Errorf("cancelled")
	}
	idx := matchOption(result.Content["selection"], options)
	if idx < 0 {
		return -1, fmt.Errorf("invalid selection")
	}
	return idx, nil
}

func (m *MCPBackend) SelectOptionWithBack(options []string, prompt string) (int, error) {
	if len(options) == 0 {
		return -1, fmt.Errorf("no options to select from")
	}
	withBack := make([]string, len(options)+1)
	copy(withBack, options)
	withBack[len(options)] = "\u2190 back"
	schema := enumSchema("selection", withBack, "")
	result, err := m.Elicit(prompt, schema)
	if err != nil {
		return -1, err
	}
	if result.Action != "accept" {
		return -1, ErrBack
	}
	selected, _ := result.Content["selection"].(string)
	if selected == "\u2190 back" {
		return -1, ErrBack
	}
	idx := matchOption(result.Content["selection"], options)
	if idx < 0 {
		return -1, fmt.Errorf("invalid selection")
	}
	return idx, nil
}

func (m *MCPBackend) SelectBranch(branches []*config.Branch, prompt string) (*config.Branch, error) {
	if len(branches) == 0 {
		return nil, fmt.Errorf("no branches to select from")
	}
	names := make([]string, len(branches))
	for i, b := range branches {
		names[i] = b.Name
	}
	schema := enumSchema("branch", names, "")
	result, err := m.Elicit(prompt, schema)
	if err != nil {
		return nil, err
	}
	if result.Action != "accept" {
		return nil, fmt.Errorf("cancelled")
	}
	selected, _ := result.Content["branch"].(string)
	for _, b := range branches {
		if b.Name == selected {
			return b, nil
		}
	}
	return nil, fmt.Errorf("branch not found: %s", selected)
}

func (m *MCPBackend) SelectStack(stacks []*config.Stack, prompt string) (*config.Stack, error) {
	if len(stacks) == 0 {
		return nil, fmt.Errorf("no stacks to select from")
	}
	// Use hash as the enum value since it's unique per stack.
	hashes := make([]string, len(stacks))
	for i, s := range stacks {
		hashes[i] = s.Hash
	}
	// Include display names in the message for context.
	msg := prompt
	for _, s := range stacks {
		msg += fmt.Sprintf("\n  %s — %s (%d branches)", s.Hash, s.DisplayName(), len(s.Branches))
	}
	schema := enumSchema("stack", hashes, "")
	result, err := m.Elicit(msg, schema)
	if err != nil {
		return nil, err
	}
	if result.Action != "accept" {
		return nil, fmt.Errorf("cancelled")
	}
	selected, _ := result.Content["stack"].(string)
	for _, s := range stacks {
		if s.Hash == selected {
			return s, nil
		}
	}
	return nil, fmt.Errorf("stack not found")
}

func (m *MCPBackend) Prompt(prompt, defaultVal string) string {
	prop := map[string]interface{}{
		"type":  "string",
		"title": prompt,
	}
	if defaultVal != "" {
		prop["default"] = defaultVal
	}
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"value": prop,
		},
	}
	result, err := m.Elicit(prompt, schema)
	if err != nil || result.Action != "accept" {
		return defaultVal
	}
	v, ok := result.Content["value"].(string)
	if !ok || v == "" {
		return defaultVal
	}
	return v
}

func (m *MCPBackend) PromptRequired(prompt string) string {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"value": map[string]interface{}{
				"type":      "string",
				"title":     prompt,
				"minLength": 1,
			},
		},
		"required": []string{"value"},
	}
	result, err := m.Elicit(prompt, schema)
	if err != nil || result.Action != "accept" {
		return ""
	}
	v, _ := result.Content["value"].(string)
	return v
}

// enumSchema builds a JSON Schema with a single enum string field.
func enumSchema(field string, options []string, defaultVal string) map[string]interface{} {
	prop := map[string]interface{}{
		"type": "string",
		"enum": options,
	}
	if defaultVal != "" {
		prop["default"] = defaultVal
	}
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			field: prop,
		},
		"required": []string{field},
	}
}

// matchOption returns the index of val in options, or -1 if not found.
func matchOption(val interface{}, options []string) int {
	s, ok := val.(string)
	if !ok {
		return -1
	}
	for i, opt := range options {
		if opt == s {
			return i
		}
	}
	return -1
}
