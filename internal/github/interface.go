package github

import "github.com/ezstack/ezstack/internal/config"

// ClientInterface defines the interface for GitHub operations
// This allows for mocking in tests
type ClientInterface interface {
	// CreatePR creates a new pull request
	CreatePR(title, body, head, base string, draft bool) (*PR, error)

	// GetPR gets a PR by number
	GetPR(number int) (*PR, error)

	// GetPRByBranch gets a PR by its head branch name
	GetPRByBranch(branch string) (*PR, error)

	// GetPRChecks gets the CI check status for a PR
	GetPRChecks(number int) (*CheckStatus, error)

	// UpdatePR updates a PR's body
	UpdatePR(number int, body string) error

	// UpdatePRBase updates a PR's base branch
	UpdatePRBase(number int, base string) error

	// ListOpenPRs returns all open PRs in the repository
	ListOpenPRs() ([]OpenPR, error)

	// UpdateStackDescription updates PR descriptions with stack info
	UpdateStackDescription(stack *config.Stack, currentBranch string, skipBranches map[string]bool) error
}

// Ensure Client implements ClientInterface
var _ ClientInterface = (*Client)(nil)
