package github

import (
	"errors"
	"testing"

	"github.com/ezstack/ezstack/internal/config"
)

func TestMockClient_CreatePR(t *testing.T) {
	mock := NewMockClient("testorg", "testrepo")

	pr, err := mock.CreatePR("Test PR", "Test body", "feature-branch", "main", false)
	if err != nil {
		t.Fatalf("CreatePR() error = %v", err)
	}

	if pr.Number != 1 {
		t.Errorf("PR number = %d, want 1", pr.Number)
	}

	if pr.Title != "Test PR" {
		t.Errorf("PR title = %q, want %q", pr.Title, "Test PR")
	}

	if pr.URL != "https://github.com/testorg/testrepo/pull/1" {
		t.Errorf("PR URL = %q, want %q", pr.URL, "https://github.com/testorg/testrepo/pull/1")
	}

	if pr.IsDraft {
		t.Error("PR should not be draft")
	}

	// Create another PR, should increment number
	pr2, _ := mock.CreatePR("Second PR", "", "feature-2", "main", true)
	if pr2.Number != 2 {
		t.Errorf("Second PR number = %d, want 2", pr2.Number)
	}
	if !pr2.IsDraft {
		t.Error("Second PR should be draft")
	}
}

func TestMockClient_CreatePR_Error(t *testing.T) {
	mock := NewMockClient("testorg", "testrepo")
	mock.CreatePRError = errors.New("simulated error")

	_, err := mock.CreatePR("Test", "", "branch", "main", false)
	if err == nil {
		t.Error("CreatePR() expected error, got nil")
	}
}

func TestMockClient_GetPR(t *testing.T) {
	mock := NewMockClient("testorg", "testrepo")
	mock.CreatePR("Test PR", "body", "feature", "main", false)

	pr, err := mock.GetPR(1)
	if err != nil {
		t.Fatalf("GetPR() error = %v", err)
	}

	if pr.Title != "Test PR" {
		t.Errorf("PR title = %q, want %q", pr.Title, "Test PR")
	}
}

func TestMockClient_GetPR_NotFound(t *testing.T) {
	mock := NewMockClient("testorg", "testrepo")

	_, err := mock.GetPR(999)
	if err == nil {
		t.Error("GetPR() expected error for non-existent PR")
	}
}

func TestMockClient_GetPRByBranch(t *testing.T) {
	mock := NewMockClient("testorg", "testrepo")
	mock.CreatePR("Test PR", "", "my-feature", "main", false)

	pr, err := mock.GetPRByBranch("my-feature")
	if err != nil {
		t.Fatalf("GetPRByBranch() error = %v", err)
	}

	if pr.Head != "my-feature" {
		t.Errorf("PR head = %q, want %q", pr.Head, "my-feature")
	}
}

func TestMockClient_GetPRByBranch_NotFound(t *testing.T) {
	mock := NewMockClient("testorg", "testrepo")

	_, err := mock.GetPRByBranch("nonexistent")
	if err == nil {
		t.Error("GetPRByBranch() expected error for non-existent branch")
	}
}

func TestMockClient_UpdatePR(t *testing.T) {
	mock := NewMockClient("testorg", "testrepo")
	mock.CreatePR("Test PR", "original body", "feature", "main", false)

	err := mock.UpdatePR(1, "new body")
	if err != nil {
		t.Fatalf("UpdatePR() error = %v", err)
	}

	pr, _ := mock.GetPR(1)
	if pr.Body != "new body" {
		t.Errorf("PR body = %q, want %q", pr.Body, "new body")
	}
}

func TestMockClient_UpdatePRBase(t *testing.T) {
	mock := NewMockClient("testorg", "testrepo")
	mock.CreatePR("Test PR", "", "feature", "main", false)

	err := mock.UpdatePRBase(1, "develop")
	if err != nil {
		t.Fatalf("UpdatePRBase() error = %v", err)
	}

	pr, _ := mock.GetPR(1)
	if pr.Base != "develop" {
		t.Errorf("PR base = %q, want %q", pr.Base, "develop")
	}
}

func TestMockClient_ListOpenPRs(t *testing.T) {
	mock := NewMockClient("testorg", "testrepo")
	mock.CreatePR("PR 1", "", "feature-1", "main", false)
	mock.CreatePR("PR 2", "", "feature-2", "main", false)

	prs, err := mock.ListOpenPRs()
	if err != nil {
		t.Fatalf("ListOpenPRs() error = %v", err)
	}

	if len(prs) != 2 {
		t.Errorf("ListOpenPRs() count = %d, want 2", len(prs))
	}
}

func TestMockClient_UpdateStackDescription(t *testing.T) {
	mock := NewMockClient("testorg", "testrepo")
	mock.CreatePR("PR 1", "original", "feature-1", "main", false)
	mock.CreatePR("PR 2", "original", "feature-2", "feature-1", false)

	stack := &config.Stack{
		Name: "feature-1",
		Branches: []*config.Branch{
			{Name: "feature-1", PRNumber: 1},
			{Name: "feature-2", PRNumber: 2},
		},
	}

	err := mock.UpdateStackDescription(stack, "feature-1", nil)
	if err != nil {
		t.Fatalf("UpdateStackDescription() error = %v", err)
	}

	// Check that bodies were updated
	pr1, _ := mock.GetPR(1)
	if pr1.Body == "original" {
		t.Error("PR 1 body should have been updated")
	}
}

