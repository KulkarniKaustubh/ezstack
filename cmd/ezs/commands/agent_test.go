package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
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

func TestLoadInstructionsFileNotFound(t *testing.T) {
	content, isOverride := loadInstructionsFile("/nonexistent/path/file.md")
	if content != "" {
		t.Errorf("expected empty content for missing file, got: %s", content)
	}
	if isOverride {
		t.Error("expected isOverride=false for missing file")
	}
}

func TestLoadInstructionsFileNormal(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "instructions.md")
	customContent := "- Use pnpm\n- Run tests before committing\n"
	if err := os.WriteFile(path, []byte(customContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	content, isOverride := loadInstructionsFile(path)
	if content != customContent {
		t.Errorf("expected %q, got: %q", customContent, content)
	}
	if isOverride {
		t.Error("expected isOverride=false for normal file")
	}
}

func TestLoadInstructionsFileOverride(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "instructions.md")
	overrideContent := "override: full\nYou are a custom agent.\n{{STACK_JSON}}\n"
	if err := os.WriteFile(path, []byte(overrideContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	content, isOverride := loadInstructionsFile(path)
	expectedContent := "You are a custom agent.\n{{STACK_JSON}}\n"
	if content != expectedContent {
		t.Errorf("expected %q, got: %q", expectedContent, content)
	}
	if !isOverride {
		t.Error("expected isOverride=true for override file")
	}
}

func TestLoadInstructionsFileOverrideNotFirstLine(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "instructions.md")
	// "override: full" not on the first line should not be treated as override
	content := "- Some instructions\noverride: full\n- More instructions\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	got, isOverride := loadInstructionsFile(path)
	if got != content {
		t.Errorf("expected content unchanged, got: %q", got)
	}
	if isOverride {
		t.Error("expected isOverride=false when marker is not on first line")
	}
}

func TestPromptFilename(t *testing.T) {
	if got := promptFilename("work"); got != "agent-work-prompt.md" {
		t.Errorf("expected agent-work-prompt.md, got: %s", got)
	}
	if got := promptFilename("feature"); got != "agent-feature-prompt.md" {
		t.Errorf("expected agent-feature-prompt.md, got: %s", got)
	}
}

func TestGlobalInstructionsPath(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	path, err := globalInstructionsPath("work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(tmpDir, "agent-work-prompt.md")
	if path != expected {
		t.Errorf("expected %s, got: %s", expected, path)
	}
}

func TestRepoInstructionsPath(t *testing.T) {
	path := repoInstructionsPath("/my/repo", "feature")
	expected := filepath.Join("/my/repo", ".ezstack", "agent-feature-prompt.md")
	if path != expected {
		t.Errorf("expected %s, got: %s", expected, path)
	}
}

func TestEnsureInstructionsFileCreatesNew(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "subdir", "agent-work-prompt.md")

	result, err := ensureInstructionsFile(path, "work", "Custom")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != path {
		t.Errorf("expected path %s, got: %s", path, result)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read created file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Custom instructions") {
		t.Error("expected starter comment to mention 'Custom instructions'")
	}
	if !strings.Contains(content, "override: full") {
		t.Error("expected Custom starter to mention override: full")
	}
	if !strings.Contains(content, "work session") {
		t.Error("expected starter to mention prompt type")
	}
}

func TestEnsureInstructionsFileRepoNoOverrideMention(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "agent-work-prompt.md")

	_, err := ensureInstructionsFile(path, "work", "Repository")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "override: full") {
		t.Error("repo starter should NOT mention override: full")
	}
	if !strings.Contains(content, "Repository instructions") {
		t.Error("expected starter comment to mention 'Repository instructions'")
	}
}

func TestEnsureInstructionsFilePreservesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "agent-work-prompt.md")
	customContent := "my custom instructions"
	os.WriteFile(path, []byte(customContent), 0644)

	_, err := ensureInstructionsFile(path, "work", "Custom")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != customContent {
		t.Errorf("expected existing content to be preserved, got: %s", string(data))
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
	// Work branch template — scoped to a specific branch
	workBranchVars := []string{
		"{{STACK_JSON}}", "{{BRANCH_NAME}}", "{{PARENT_NAME}}",
		"{{WORKTREE_PATH}}", "{{EZS_COMMANDS}}", "{{EZS_DOCS}}",
		"{{CUSTOM_INSTRUCTIONS}}", "{{REPO_INSTRUCTIONS}}",
	}
	for _, v := range workBranchVars {
		if !strings.Contains(defaultWorkBranchPromptTemplate, v) {
			t.Errorf("defaultWorkBranchPromptTemplate missing variable %s", v)
		}
	}

	// Work stack template — works on entire stack, no branch-specific vars needed
	workStackVars := []string{
		"{{STACK_JSON}}", "{{EZS_COMMANDS}}", "{{EZS_DOCS}}",
		"{{CUSTOM_INSTRUCTIONS}}", "{{REPO_INSTRUCTIONS}}",
	}
	for _, v := range workStackVars {
		if !strings.Contains(defaultWorkStackPromptTemplate, v) {
			t.Errorf("defaultWorkStackPromptTemplate missing variable %s", v)
		}
	}

	// Feature template — no branch-specific or stack vars, has feature description
	featureVars := []string{
		"{{FEATURE_DESCRIPTION}}",
		"{{EZS_COMMANDS}}", "{{EZS_DOCS}}",
		"{{CUSTOM_INSTRUCTIONS}}", "{{REPO_INSTRUCTIONS}}",
	}
	for _, v := range featureVars {
		if !strings.Contains(defaultFeaturePromptTemplate, v) {
			t.Errorf("defaultFeaturePromptTemplate missing variable %s", v)
		}
	}
}

// ── Composition tests ─────────────────────────────────────────────────────────

func TestBuildComposedPromptNoInstructions(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	vars := map[string]string{"BRANCH_NAME": "test-branch"}
	template := "Branch: {{BRANCH_NAME}}\n{{CUSTOM_INSTRUCTIONS}}\n{{REPO_INSTRUCTIONS}}"

	result, err := buildComposedPrompt(template, vars, "/nonexistent/repo", "work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Branch: test-branch") {
		t.Error("expected branch name to be rendered")
	}
	// No custom or repo files → headings should be absent
	if strings.Contains(result, "## Custom Instructions") {
		t.Error("should not contain Custom Instructions heading when no custom file")
	}
	if strings.Contains(result, "## Repository Instructions") {
		t.Error("should not contain Repository Instructions heading when no repo file")
	}
}

func TestBuildComposedPromptWithCustomOnly(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Write custom instructions
	customContent := "- Always run tests\n- Use conventional commits\n"
	os.WriteFile(filepath.Join(tmpDir, "agent-work-prompt.md"), []byte(customContent), 0644)

	vars := map[string]string{"BRANCH_NAME": "test-branch"}
	template := "Branch: {{BRANCH_NAME}}\n{{CUSTOM_INSTRUCTIONS}}\n{{REPO_INSTRUCTIONS}}"

	result, err := buildComposedPrompt(template, vars, "/nonexistent/repo", "work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "## Custom Instructions") {
		t.Error("expected Custom Instructions heading")
	}
	if !strings.Contains(result, "Always run tests") {
		t.Error("expected custom instructions content")
	}
	if strings.Contains(result, "## Repository Instructions") {
		t.Error("should not contain Repository Instructions heading when no repo file")
	}
}

func TestBuildComposedPromptWithRepoOnly(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Write repo instructions
	repoDir := t.TempDir()
	ezstackDir := filepath.Join(repoDir, ".ezstack")
	os.MkdirAll(ezstackDir, 0755)
	repoContent := "- Use pnpm\n- Run make test\n"
	os.WriteFile(filepath.Join(ezstackDir, "agent-work-prompt.md"), []byte(repoContent), 0644)

	vars := map[string]string{"BRANCH_NAME": "test-branch"}
	template := "Branch: {{BRANCH_NAME}}\n{{CUSTOM_INSTRUCTIONS}}\n{{REPO_INSTRUCTIONS}}"

	result, err := buildComposedPrompt(template, vars, repoDir, "work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result, "## Custom Instructions") {
		t.Error("should not contain Custom Instructions heading when no custom file")
	}
	if !strings.Contains(result, "## Repository Instructions") {
		t.Error("expected Repository Instructions heading")
	}
	if !strings.Contains(result, "Use pnpm") {
		t.Error("expected repo instructions content")
	}
}

func TestBuildComposedPromptWithBoth(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Write custom instructions
	customContent := "- Always run tests\n"
	os.WriteFile(filepath.Join(tmpDir, "agent-work-prompt.md"), []byte(customContent), 0644)

	// Write repo instructions
	repoDir := t.TempDir()
	ezstackDir := filepath.Join(repoDir, ".ezstack")
	os.MkdirAll(ezstackDir, 0755)
	repoContent := "- Use pnpm\n"
	os.WriteFile(filepath.Join(ezstackDir, "agent-work-prompt.md"), []byte(repoContent), 0644)

	vars := map[string]string{"BRANCH_NAME": "test-branch"}
	template := "Branch: {{BRANCH_NAME}}\n{{CUSTOM_INSTRUCTIONS}}\n{{REPO_INSTRUCTIONS}}"

	result, err := buildComposedPrompt(template, vars, repoDir, "work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "## Custom Instructions") {
		t.Error("expected Custom Instructions heading")
	}
	if !strings.Contains(result, "Always run tests") {
		t.Error("expected custom content")
	}
	if !strings.Contains(result, "## Repository Instructions") {
		t.Error("expected Repository Instructions heading")
	}
	if !strings.Contains(result, "Use pnpm") {
		t.Error("expected repo content")
	}

	// Custom should come before repo
	customIdx := strings.Index(result, "## Custom Instructions")
	repoIdx := strings.Index(result, "## Repository Instructions")
	if customIdx >= repoIdx {
		t.Error("expected custom instructions to come before repo instructions")
	}
}

func TestBuildComposedPromptCustomOverride(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Write custom with override: full
	overrideContent := "override: full\nYou are a custom agent.\nBranch: {{BRANCH_NAME}}\n{{REPO_INSTRUCTIONS}}\n"
	os.WriteFile(filepath.Join(tmpDir, "agent-work-prompt.md"), []byte(overrideContent), 0644)

	// Write repo instructions
	repoDir := t.TempDir()
	ezstackDir := filepath.Join(repoDir, ".ezstack")
	os.MkdirAll(ezstackDir, 0755)
	repoContent := "- Use pnpm\n"
	os.WriteFile(filepath.Join(ezstackDir, "agent-work-prompt.md"), []byte(repoContent), 0644)

	vars := map[string]string{"BRANCH_NAME": "test-branch"}
	shippedTemplate := "THIS SHIPPED TEMPLATE SHOULD NOT APPEAR\n{{BRANCH_NAME}}\n{{CUSTOM_INSTRUCTIONS}}\n{{REPO_INSTRUCTIONS}}"

	result, err := buildComposedPrompt(shippedTemplate, vars, repoDir, "work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Shipped template should be replaced by override content
	if strings.Contains(result, "THIS SHIPPED TEMPLATE SHOULD NOT APPEAR") {
		t.Error("shipped template should be replaced by override")
	}
	if !strings.Contains(result, "You are a custom agent.") {
		t.Error("expected override content")
	}
	// Branch var should still be rendered
	if !strings.Contains(result, "Branch: test-branch") {
		t.Error("expected template vars to be rendered in override content")
	}
	// Repo instructions should be injected
	if !strings.Contains(result, "## Repository Instructions") {
		t.Error("expected repo instructions to be injected even with custom override")
	}
	if !strings.Contains(result, "Use pnpm") {
		t.Error("expected repo content in override mode")
	}
}

func TestBuildComposedPromptCustomOverrideNoRepoPlaceholder(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Override without {{REPO_INSTRUCTIONS}} placeholder
	overrideContent := "override: full\nYou are a custom agent.\nBranch: {{BRANCH_NAME}}\n"
	os.WriteFile(filepath.Join(tmpDir, "agent-work-prompt.md"), []byte(overrideContent), 0644)

	// Write repo instructions
	repoDir := t.TempDir()
	ezstackDir := filepath.Join(repoDir, ".ezstack")
	os.MkdirAll(ezstackDir, 0755)
	os.WriteFile(filepath.Join(ezstackDir, "agent-work-prompt.md"), []byte("- Use pnpm\n"), 0644)

	vars := map[string]string{"BRANCH_NAME": "test-branch"}
	shippedTemplate := "unused"

	result, err := buildComposedPrompt(shippedTemplate, vars, repoDir, "work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Repo instructions should be silently dropped (user's choice — they omitted the placeholder)
	if strings.Contains(result, "Use pnpm") {
		t.Error("repo instructions should be dropped when override has no {{REPO_INSTRUCTIONS}} placeholder")
	}
}

func TestBuildComposedPromptRepoOverrideMarkerIgnored(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Repo file with "override: full" — should be treated as normal content
	repoDir := t.TempDir()
	ezstackDir := filepath.Join(repoDir, ".ezstack")
	os.MkdirAll(ezstackDir, 0755)
	repoContent := "override: full\n- This is repo content\n"
	os.WriteFile(filepath.Join(ezstackDir, "agent-work-prompt.md"), []byte(repoContent), 0644)

	vars := map[string]string{"BRANCH_NAME": "test-branch"}
	template := "Branch: {{BRANCH_NAME}}\n{{CUSTOM_INSTRUCTIONS}}\n{{REPO_INSTRUCTIONS}}"

	result, err := buildComposedPrompt(template, vars, repoDir, "work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The shipped template should still be used (repo override: full is not special)
	if !strings.Contains(result, "Branch: test-branch") {
		t.Error("expected shipped template to be used")
	}
	// The "override: full" line is stripped by loadInstructionsFile, but the remaining
	// content is injected as repo instructions (repo isOverride is ignored by buildComposedPrompt)
	if !strings.Contains(result, "This is repo content") {
		t.Error("expected repo content to be injected")
	}
}

// ── Full prompt rendering tests ───────────────────────────────────────────────

func TestBuildRenderedWorkPromptBranchScoped(t *testing.T) {
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
		branchScoped: true,
		repoPath:     "/path/to/repo",
	}

	prompt, err := buildRenderedWorkPrompt(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain rendered branch name value
	if !strings.Contains(prompt, "feature-auth") {
		t.Error("expected prompt to contain branch name")
	}
	if !strings.Contains(prompt, "main") {
		t.Error("expected prompt to contain parent name")
	}
	// Should contain branch-scoped constraint
	if !strings.Contains(prompt, "THIS BRANCH ONLY") {
		t.Error("expected branch-scoped constraint")
	}
	// Should not have instruction headings when no files exist
	if strings.Contains(prompt, "## Custom Instructions") {
		t.Error("should not contain Custom Instructions heading without file")
	}
}

func TestBuildRenderedWorkPromptStackScoped(t *testing.T) {
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
		branchScoped: false,
		repoPath:     "/path/to/repo",
	}

	prompt, err := buildRenderedWorkPrompt(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain stack-scoped language
	if !strings.Contains(prompt, "ENTIRE STACK") {
		t.Error("expected stack-scoped prompt to mention ENTIRE STACK")
	}
	// Should NOT contain branch-scoped constraint
	if strings.Contains(prompt, "THIS BRANCH ONLY") {
		t.Error("stack-scoped prompt should not contain branch-only constraint")
	}
}

func TestBuildRenderedFeaturePrompt(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	prompt, err := buildRenderedFeaturePrompt("/path/to/repo", "Add JWT authentication", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain the actual feature description
	if !strings.Contains(prompt, "Add JWT authentication") {
		t.Error("expected prompt to contain feature description")
	}
	// Feature prompt should mention creating branches, not scoping to one
	if !strings.Contains(prompt, "Plan and implement") {
		t.Error("expected feature prompt to describe building process")
	}
	// Feature prompt should not have branch-scoped constraint
	if strings.Contains(prompt, "THIS BRANCH ONLY") {
		t.Error("feature prompt should not be branch-scoped")
	}
	// Without an existing stack, the feature template should not contain "Existing Stack" section
	if strings.Contains(prompt, "Existing Stack") {
		t.Error("feature prompt without stack should not have existing stack section")
	}
	// Without an existing stack, should describe creating new branches
	if !strings.Contains(prompt, "Create it: ezs -y new") {
		t.Error("feature prompt without stack should describe creating branches")
	}
}

func TestBuildRenderedFeaturePromptWithExistingStack(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	existingStack := &config.Stack{
		Hash: "abc1234",
		Name: "my-feature",
		Root: "main",
		Branches: []*config.Branch{
			{Name: "add-user-model", Parent: "main", WorktreePath: "/tmp/wt/add-user-model"},
			{Name: "add-user-api", Parent: "add-user-model", WorktreePath: "/tmp/wt/add-user-api"},
		},
	}

	prompt, err := buildRenderedFeaturePrompt("/path/to/repo", "Add user management", existingStack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(prompt, "Add user management") {
		t.Error("expected prompt to contain feature description")
	}
	// Should include existing stack section
	if !strings.Contains(prompt, "Existing Stack") {
		t.Error("expected prompt to contain existing stack section")
	}
	if !strings.Contains(prompt, "add-user-model") {
		t.Error("expected prompt to contain existing branch names")
	}
	if !strings.Contains(prompt, "add-user-api") {
		t.Error("expected prompt to contain existing branch names")
	}
	// Should adapt process to use existing branches
	if !strings.Contains(prompt, "Review the existing stack branches") {
		t.Error("expected process to mention reviewing existing branches")
	}
	// Should also mention creating new branches if needed
	if !strings.Contains(prompt, "additional branches are needed") {
		t.Error("expected process to mention creating additional branches if needed")
	}
	// Should still have all standard sections
	if !strings.Contains(prompt, "Plan and implement") {
		t.Error("expected feature prompt to describe building process")
	}
}

func TestBuildRenderedFeaturePromptWithEmptyStack(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Stack with no branches — should behave like no stack
	emptyStack := &config.Stack{
		Hash: "abc1234",
		Name: "empty-stack",
		Root: "main",
	}

	prompt, err := buildRenderedFeaturePrompt("/path/to/repo", "Add feature", emptyStack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT include existing stack section
	if strings.Contains(prompt, "Existing Stack") {
		t.Error("empty stack should not produce an existing stack section")
	}
	// Should use the normal process (creating branches from scratch)
	if !strings.Contains(prompt, "Create it: ezs -y new") {
		t.Error("empty stack should use normal branch creation process")
	}
}

func TestBuildRenderedWorkPromptWithInstructions(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Write custom instructions
	os.WriteFile(filepath.Join(tmpDir, "agent-work-prompt.md"), []byte("- Be concise\n"), 0644)

	// Write repo instructions
	repoDir := t.TempDir()
	ezstackDir := filepath.Join(repoDir, ".ezstack")
	os.MkdirAll(ezstackDir, 0755)
	os.WriteFile(filepath.Join(ezstackDir, "agent-work-prompt.md"), []byte("- Use Go\n"), 0644)

	ctx := &agentContext{
		branchName:   "feature-auth",
		parentName:   "main",
		worktreePath: "/path/to/worktree",
		stackJSON:    `{"hash":"abc1234","root":"main","branches":[]}`,
		hasStack:     true,
		branchScoped: true,
		repoPath:     repoDir,
	}

	prompt, err := buildRenderedWorkPrompt(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(prompt, "Be concise") {
		t.Error("expected custom instructions in rendered prompt")
	}
	if !strings.Contains(prompt, "Use Go") {
		t.Error("expected repo instructions in rendered prompt")
	}
	// Shipped content should still be present
	if !strings.Contains(prompt, "ezstack-managed repository") {
		t.Error("expected shipped prompt content")
	}
}

// ── Positional arg parsing ────────────────────────────────────────────────────

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

func TestResolveWorkDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Branch-scoped: valid worktree path should be returned
	got := resolveWorkDir("my-branch", tmpDir, "/fallback", nil)
	if got != tmpDir {
		t.Errorf("expected %s, got %s", tmpDir, got)
	}

	// Branch-scoped: non-existent worktree should fall back to repo path
	got = resolveWorkDir("my-branch", "/nonexistent/path", "/fallback", nil)
	if got != "/fallback" {
		t.Errorf("expected /fallback, got %s", got)
	}

	// Stack-scoped: empty branch should fall back to repo path
	got = resolveWorkDir("", "", "/fallback", nil)
	if got != "/fallback" {
		t.Errorf("expected /fallback, got %s", got)
	}

	// Stack-scoped: with stack that has branches with worktrees
	stackWithWT := &config.Stack{
		Branches: []*config.Branch{
			{Name: "b1", WorktreePath: tmpDir},
		},
	}
	got = resolveWorkDir("", "", "/fallback", stackWithWT)
	if got != tmpDir {
		t.Errorf("expected first branch worktree %s, got %s", tmpDir, got)
	}

	// Stack-scoped: with stack whose worktree doesn't exist
	stackBadWT := &config.Stack{
		Branches: []*config.Branch{
			{Name: "b1", WorktreePath: "/nonexistent"},
		},
	}
	got = resolveWorkDir("", "", "/fallback", stackBadWT)
	if got != "/fallback" {
		t.Errorf("expected /fallback, got %s", got)
	}
}

// ── Reset tests ───────────────────────────────────────────────────────────────

func TestResetCustomPromptDeletesFile(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Create a custom prompt file
	path := filepath.Join(tmpDir, "agent-work-prompt.md")
	os.WriteFile(path, []byte("custom content"), 0644)

	// Enable YesMode to auto-confirm
	originalYesMode := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = originalYesMode }()

	if err := resetCustomPrompt("work"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected file to be deleted after reset")
	}
}

func TestResetCustomPromptNoFile(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Should not error when file doesn't exist
	if err := resetCustomPrompt("work"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResetRepoPromptDeletesFile(t *testing.T) {
	repoDir := t.TempDir()
	ezstackDir := filepath.Join(repoDir, ".ezstack")
	os.MkdirAll(ezstackDir, 0755)
	path := filepath.Join(ezstackDir, "agent-feature-prompt.md")
	os.WriteFile(path, []byte("repo content"), 0644)

	originalYesMode := ui.YesMode
	ui.YesMode = true
	defer func() { ui.YesMode = originalYesMode }()

	if err := resetRepoPrompt("feature", repoDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected file to be deleted after reset")
	}
}

// ── Show functions ────────────────────────────────────────────────────────────

func TestShowShippedPrompt(t *testing.T) {
	// Just verify it doesn't error
	// (output goes to stdout, we just confirm no panic/error)
	if err := showShippedPrompt("work"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := showShippedPrompt("feature"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShowCustomPromptNoFile(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	// Should not error when no file exists
	if err := showCustomPrompt("work"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShowRepoPromptNoFile(t *testing.T) {
	// Should not error when no file exists
	if err := showRepoPrompt("work", "/nonexistent/repo"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── agentPrompt CLI parsing ───────────────────────────────────────────────────

func TestAgentPromptRequiresPositionalArg(t *testing.T) {
	err := agentPrompt([]string{"--shipped"})
	if err == nil {
		t.Fatal("expected error when positional arg is missing")
	}
	if !strings.Contains(err.Error(), "missing prompt type") {
		t.Errorf("expected 'missing prompt type' error, got: %v", err)
	}
}

func TestAgentPromptInvalidPromptType(t *testing.T) {
	err := agentPrompt([]string{"--shipped", "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid prompt type")
	}
	if !strings.Contains(err.Error(), "invalid prompt type") {
		t.Errorf("expected 'invalid prompt type' error, got: %v", err)
	}
}

func TestAgentPromptShippedWork(t *testing.T) {
	// Should succeed without error
	if err := agentPrompt([]string{"--shipped", "work"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAgentPromptShippedFeature(t *testing.T) {
	if err := agentPrompt([]string{"--shipped", "feature"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAgentPromptCustomWork(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("EZSTACK_HOME")
	defer os.Setenv("EZSTACK_HOME", originalHome)
	os.Setenv("EZSTACK_HOME", tmpDir)

	if err := agentPrompt([]string{"--custom", "work"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetEditor(t *testing.T) {
	// Save and restore env
	origEditor := os.Getenv("EDITOR")
	origVisual := os.Getenv("VISUAL")
	defer func() {
		os.Setenv("EDITOR", origEditor)
		os.Setenv("VISUAL", origVisual)
	}()

	os.Setenv("EDITOR", "nano")
	os.Setenv("VISUAL", "")
	if got := getEditor(); got != "nano" {
		t.Errorf("expected nano, got: %s", got)
	}

	os.Setenv("EDITOR", "")
	os.Setenv("VISUAL", "code --wait")
	if got := getEditor(); got != "code --wait" {
		t.Errorf("expected 'code --wait', got: %s", got)
	}

	os.Setenv("EDITOR", "")
	os.Setenv("VISUAL", "")
	if got := getEditor(); got != "vi" {
		t.Errorf("expected vi as default, got: %s", got)
	}
}
