package itests

import (
	"strconv"
	"strings"
	"testing"
)

// TestPRCreate tests creating a PR using stub gh
func TestPRCreate(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	output, err := RunGhCommand(t, env,
		"pr", "create",
		"--head", "feature-branch",
		"--base", "main",
		"--title", "Test PR Title",
		"--body", "Test PR Body",
	)

	if err != nil {
		t.Fatalf("PR create failed: %v", err)
	}

	if !strings.Contains(output, "github.com") || !strings.Contains(output, "/pull/") {
		t.Errorf("Expected PR URL, got: %s", output)
	}
}

// TestPRCreateDraft tests creating a draft PR
func TestPRCreateDraft(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	RunGhCommand(t, env,
		"pr", "create",
		"--head", "draft-feature",
		"--base", "main",
		"--title", "Draft PR",
		"--draft",
	)

	output, _ := RunGhCommand(t, env, "pr", "view", "1")

	if !strings.Contains(output, `"isDraft": true`) {
		t.Errorf("Expected isDraft=true, got: %s", output)
	}
}

// TestPRView tests viewing a PR by number
func TestPRView(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	RunGhCommand(t, env,
		"pr", "create",
		"--head", "view-test",
		"--base", "main",
		"--title", "View Test PR",
	)

	output, err := RunGhCommand(t, env, "pr", "view", "1")
	if err != nil {
		t.Fatalf("PR view failed: %v", err)
	}

	if !strings.Contains(output, `"title": "View Test PR"`) {
		t.Errorf("Title mismatch, got: %s", output)
	}
}

// TestPRViewByBranch tests viewing a PR by branch name
func TestPRViewByBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	RunGhCommand(t, env,
		"pr", "create",
		"--head", "my-feature",
		"--base", "main",
		"--title", "Feature PR",
	)

	output, err := RunGhCommand(t, env, "pr", "view", "my-feature")
	if err != nil {
		t.Fatalf("PR view by branch failed: %v", err)
	}

	if !strings.Contains(output, `"headRefName": "my-feature"`) {
		t.Errorf("Branch name mismatch, got: %s", output)
	}
}

// TestPRViewNonexistent tests viewing a PR that doesn't exist
func TestPRViewNonexistent(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	_, err := RunGhCommand(t, env, "pr", "view", "999")
	if err == nil {
		t.Error("Expected error when viewing nonexistent PR")
	}
}

// TestPREdit tests editing a PR body
func TestPREdit(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	RunGhCommand(t, env,
		"pr", "create",
		"--head", "edit-test",
		"--base", "main",
	)

	RunGhCommand(t, env,
		"pr", "edit", "1",
		"--body", "Updated body content",
	)

	output, _ := RunGhCommand(t, env, "pr", "view", "1")
	if !strings.Contains(output, "Updated body content") {
		t.Errorf("Body not updated, got: %s", output)
	}
}

// TestPRList tests listing PRs
func TestPRList(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	for i := 0; i < 3; i++ {
		RunGhCommand(t, env,
			"pr", "create",
			"--head", "feature-"+strconv.Itoa(i),
			"--base", "main",
			"--title", "PR "+strconv.Itoa(i),
		)
	}

	output, err := RunGhCommand(t, env, "pr", "list", "--state", "open")
	if err != nil {
		t.Fatalf("PR list failed: %v", err)
	}

	for i := 0; i < 3; i++ {
		if !strings.Contains(output, "PR "+strconv.Itoa(i)) {
			t.Errorf("PR %d not found in list", i)
		}
	}
}

// TestPRCreateMissingHead tests PR creation without required --head flag
func TestPRCreateMissingHead(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	_, err := RunGhCommand(t, env, "pr", "create", "--base", "main")
	if err == nil {
		t.Error("Expected error when --head is not provided")
	}
}

// TestPRChecks tests PR checks command
func TestPRChecks(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	RunGhCommand(t, env,
		"pr", "create",
		"--head", "checks-test",
		"--base", "main",
	)

	output, err := RunGhCommand(t, env, "pr", "checks", "1")
	if err != nil {
		t.Fatalf("PR checks failed: %v", err)
	}

	if !strings.Contains(output, "pass") {
		t.Errorf("Expected pass in checks output, got: %s", output)
	}
}

// TestPRSequentialNumbers tests that PRs get sequential numbers
func TestPRSequentialNumbers(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	for i := 1; i <= 5; i++ {
		output, err := RunGhCommand(t, env,
			"pr", "create",
			"--head", "branch-"+strconv.Itoa(i),
			"--base", "main",
		)
		if err != nil {
			t.Fatalf("Failed to create PR %d: %v", i, err)
		}

		expectedSuffix := "/pull/" + strconv.Itoa(i)
		if !strings.HasSuffix(output, expectedSuffix) {
			t.Errorf("PR %d URL should end with %s, got: %s", i, expectedSuffix, output)
		}
	}
}

// TestAuthStatus tests auth status command
func TestAuthStatus(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	output, err := RunGhCommand(t, env, "auth", "status")
	if err != nil {
		t.Fatalf("Auth status failed: %v", err)
	}

	if !strings.Contains(output, "testuser") {
		t.Errorf("Expected testuser in auth output, got: %s", output)
	}
}

// TestPRViewNonexistentBranch tests viewing a PR by branch that doesn't exist
func TestPRViewNonexistentBranch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	_, err := RunGhCommand(t, env, "pr", "view", "nonexistent-branch")
	if err == nil {
		t.Error("Expected error when viewing PR for nonexistent branch")
	}
}
