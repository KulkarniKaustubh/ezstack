package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPrompt(t *testing.T) {
	template := `Branch: {{BRANCH_NAME}}
Parent: {{PARENT_NAME}}
Stack: {{STACK_JSON}}`

	vars := map[string]string{
		"BRANCH_NAME": "feature-auth",
		"PARENT_NAME": "main",
		"STACK_JSON":  `{"hash":"abc1234"}`,
	}

	result := renderPrompt(template, vars)

	if !strings.Contains(result, "Branch: feature-auth") {
		t.Errorf("expected rendered prompt to contain branch name, got: %s", result)
	}
	if !strings.Contains(result, "Parent: main") {
		t.Errorf("expected rendered prompt to contain parent name, got: %s", result)
	}
	if !strings.Contains(result, `{"hash":"abc1234"}`) {
		t.Errorf("expected rendered prompt to contain stack JSON, got: %s", result)
	}
	if strings.Contains(result, "{{") {
		t.Errorf("expected no unresolved template variables, got: %s", result)
	}
}

func TestRenderPromptUnusedVarsPreserved(t *testing.T) {
	template := `{{BRANCH_NAME}} and {{FEATURE_DESCRIPTION}}`
	vars := map[string]string{
		"BRANCH_NAME": "my-branch",
	}

	result := renderPrompt(template, vars)

	if !strings.Contains(result, "my-branch") {
		t.Errorf("expected branch name to be rendered")
	}
	// Unreplaced variables should remain as-is (useful for showing template to user)
	if !strings.Contains(result, "{{FEATURE_DESCRIPTION}}") {
		t.Errorf("expected unreplaced variable to be preserved, got: %s", result)
	}
}

func TestLoadPromptTemplateDefault(t *testing.T) {
	// When file doesn't exist, should return default content
	content, _, err := loadPromptTemplate("nonexistent-prompt-file.md", "default content here")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "default content here" {
		t.Errorf("expected default content, got: %s", content)
	}
}

func TestLoadPromptTemplateFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	customContent := "# Custom Prompt\nBranch: {{BRANCH_NAME}}\n"
	promptPath := filepath.Join(tmpDir, "agent-work-prompt.md")
	if err := os.WriteFile(promptPath, []byte(customContent), 0644); err != nil {
		t.Fatalf("failed to write test prompt: %v", err)
	}

	content, path, err := loadPromptTemplate("agent-work-prompt.md", "should not use this default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != customContent {
		t.Errorf("expected custom content, got: %s", content)
	}
	if path != promptPath {
		t.Errorf("expected path %s, got: %s", promptPath, path)
	}
}

func TestEnsurePromptFile(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	path, err := ensurePromptFile("agent-work-prompt.md", "default prompt content")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPath := filepath.Join(tmpDir, "agent-work-prompt.md")
	if path != expectedPath {
		t.Errorf("expected path %s, got: %s", expectedPath, path)
	}

	// File should exist with default content
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read created file: %v", err)
	}
	if string(data) != "default prompt content" {
		t.Errorf("expected default content, got: %s", string(data))
	}
}

func TestEnsurePromptFilePreservesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Create a custom prompt file first
	promptPath := filepath.Join(tmpDir, "agent-work-prompt.md")
	customContent := "my custom prompt"
	if err := os.WriteFile(promptPath, []byte(customContent), 0644); err != nil {
		t.Fatalf("failed to write test prompt: %v", err)
	}

	// ensurePromptFile should not overwrite it
	path, err := ensurePromptFile("agent-work-prompt.md", "default prompt content")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(data) != customContent {
		t.Errorf("expected custom content to be preserved, got: %s", string(data))
	}
}

func TestBuildTemplateVars(t *testing.T) {
	ctx := &agentContext{
		branchName:   "feature-auth",
		parentName:   "main",
		worktreePath: "/path/to/worktree",
		stackJSON:    `{"hash":"abc1234"}`,
		hasStack:     true,
		repoPath:     "/path/to/repo",
	}

	vars := buildTemplateVars(ctx)

	if vars["BRANCH_NAME"] != "feature-auth" {
		t.Errorf("expected BRANCH_NAME=feature-auth, got: %s", vars["BRANCH_NAME"])
	}
	if vars["PARENT_NAME"] != "main" {
		t.Errorf("expected PARENT_NAME=main, got: %s", vars["PARENT_NAME"])
	}
	if vars["WORKTREE_PATH"] != "/path/to/worktree" {
		t.Errorf("expected WORKTREE_PATH=/path/to/worktree, got: %s", vars["WORKTREE_PATH"])
	}
	if vars["STACK_JSON"] != `{"hash":"abc1234"}` {
		t.Errorf("expected STACK_JSON, got: %s", vars["STACK_JSON"])
	}
	if vars["EZS_COMMANDS"] == "" {
		t.Error("expected EZS_COMMANDS to be non-empty")
	}
	if vars["EZS_DOCS"] == "" {
		t.Error("expected EZS_DOCS to be non-empty")
	}
}

func TestDefaultPromptTemplatesHaveAllVariables(t *testing.T) {
	// Ensure default templates reference expected variables
	workVars := []string{"{{STACK_JSON}}", "{{BRANCH_NAME}}", "{{PARENT_NAME}}", "{{WORKTREE_PATH}}", "{{EZS_COMMANDS}}", "{{EZS_DOCS}}"}
	for _, v := range workVars {
		if !strings.Contains(defaultWorkPromptTemplate, v) {
			t.Errorf("defaultWorkPromptTemplate missing variable %s", v)
		}
	}

	featureVars := append(workVars, "{{FEATURE_DESCRIPTION}}")
	for _, v := range featureVars {
		if !strings.Contains(defaultFeaturePromptTemplate, v) {
			t.Errorf("defaultFeaturePromptTemplate missing variable %s", v)
		}
	}
}

func TestBuildRenderedWorkPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	ctx := &agentContext{
		branchName:   "feature-auth",
		parentName:   "main",
		worktreePath: "/path/to/worktree",
		stackJSON:    `{"hash":"abc1234","root":"main","branches":[]}`,
		hasStack:     true,
		repoPath:     "/path/to/repo",
	}

	prompt, err := buildRenderedWorkPrompt(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain rendered values, not template variables
	if strings.Contains(prompt, "{{BRANCH_NAME}}") {
		t.Error("expected BRANCH_NAME to be rendered")
	}
	if !strings.Contains(prompt, "feature-auth") {
		t.Error("expected prompt to contain branch name")
	}
	if !strings.Contains(prompt, "main") {
		t.Error("expected prompt to contain parent name")
	}
	// Should not contain feature description variable (work mode)
	if strings.Contains(prompt, "{{FEATURE_DESCRIPTION}}") {
		t.Error("work prompt should not contain FEATURE_DESCRIPTION variable")
	}
}

func TestBuildRenderedFeaturePrompt(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	ctx := &agentContext{
		branchName:   "feature-auth",
		parentName:   "main",
		worktreePath: "/path/to/worktree",
		stackJSON:    `{"hash":"abc1234","root":"main","branches":[]}`,
		hasStack:     true,
		repoPath:     "/path/to/repo",
	}

	prompt, err := buildRenderedFeaturePrompt(ctx, "Add JWT authentication")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(prompt, "{{FEATURE_DESCRIPTION}}") {
		t.Error("expected FEATURE_DESCRIPTION to be rendered")
	}
	if !strings.Contains(prompt, "Add JWT authentication") {
		t.Error("expected prompt to contain feature description")
	}
	if !strings.Contains(prompt, "feature-auth") {
		t.Error("expected prompt to contain branch name")
	}
}

func TestFirstPositionalArg(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantArg  string
		wantRest []string
	}{
		{
			name:     "prompt as first arg",
			args:     []string{"prompt", "--edit"},
			wantArg:  "prompt",
			wantRest: []string{"--edit"},
		},
		{
			name:     "prompt after -s flag value",
			args:     []string{"-s", "prompt"},
			wantArg:  "",
			wantRest: nil,
		},
		{
			name:     "prompt after --stack flag value",
			args:     []string{"--stack", "prompt"},
			wantArg:  "",
			wantRest: nil,
		},
		{
			name:     "prompt after --cmd flag value",
			args:     []string{"--cmd", "prompt"},
			wantArg:  "",
			wantRest: nil,
		},
		{
			name:     "feature as first positional",
			args:     []string{"-s", "abc123", "feature", "build auth"},
			wantArg:  "feature",
			wantRest: []string{"build auth"},
		},
		{
			name:     "no positional args",
			args:     []string{"-s", "abc123", "-b", "my-branch"},
			wantArg:  "",
			wantRest: nil,
		},
		{
			name:     "empty args",
			args:     []string{},
			wantArg:  "",
			wantRest: nil,
		},
		{
			name:     "prompt with -b before it",
			args:     []string{"-b", "my-branch", "prompt", "--work"},
			wantArg:  "prompt",
			wantRest: []string{"--work"},
		},
		{
			name:     "help flag does not consume next arg",
			args:     []string{"-h", "prompt"},
			wantArg:  "prompt",
			wantRest: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotArg, gotRest := firstPositionalArg(tt.args)
			if gotArg != tt.wantArg {
				t.Errorf("firstPositionalArg(%v) arg = %q, want %q", tt.args, gotArg, tt.wantArg)
			}
			if tt.wantRest == nil && gotRest != nil {
				t.Errorf("firstPositionalArg(%v) rest = %v, want nil", tt.args, gotRest)
			}
			if tt.wantRest != nil {
				if len(gotRest) != len(tt.wantRest) {
					t.Errorf("firstPositionalArg(%v) rest = %v, want %v", tt.args, gotRest, tt.wantRest)
				} else {
					for i := range tt.wantRest {
						if gotRest[i] != tt.wantRest[i] {
							t.Errorf("firstPositionalArg(%v) rest[%d] = %q, want %q", tt.args, i, gotRest[i], tt.wantRest[i])
						}
					}
				}
			}
		})
	}
}

func TestResetPrompts(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Reset work prompt
	if err := resetPrompts(true, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, workPromptFilename))
	if err != nil {
		t.Fatalf("work prompt file not created: %v", err)
	}
	if string(data) != defaultWorkPromptTemplate {
		t.Error("work prompt content doesn't match default")
	}

	// Feature prompt should not exist yet
	if _, err := os.Stat(filepath.Join(tmpDir, featurePromptFilename)); !os.IsNotExist(err) {
		t.Error("feature prompt should not exist after resetting only work")
	}

	// Reset feature prompt
	if err := resetPrompts(false, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err = os.ReadFile(filepath.Join(tmpDir, featurePromptFilename))
	if err != nil {
		t.Fatalf("feature prompt file not created: %v", err)
	}
	if string(data) != defaultFeaturePromptTemplate {
		t.Error("feature prompt content doesn't match default")
	}
}

func TestResetOverwritesCustomPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Write a custom prompt
	customContent := "my custom prompt that should be overwritten"
	os.WriteFile(filepath.Join(tmpDir, workPromptFilename), []byte(customContent), 0644)

	// Reset should overwrite it
	if err := resetPrompts(true, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpDir, workPromptFilename))
	if string(data) == customContent {
		t.Error("reset should have overwritten custom prompt")
	}
	if string(data) != defaultWorkPromptTemplate {
		t.Error("reset should restore default template")
	}
}

func TestResolveWorkDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Valid worktree path should be returned
	got := resolveWorkDir(tmpDir, "/fallback")
	if got != tmpDir {
		t.Errorf("expected %s, got %s", tmpDir, got)
	}

	// Non-existent worktree should fall back to repo path
	got = resolveWorkDir("/nonexistent/path", "/fallback")
	if got != "/fallback" {
		t.Errorf("expected /fallback, got %s", got)
	}

	// Empty worktree should fall back to repo path
	got = resolveWorkDir("", "/fallback")
	if got != "/fallback" {
		t.Errorf("expected /fallback, got %s", got)
	}
}
