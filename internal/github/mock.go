package github

import (
	"fmt"
	"sync"

	"github.com/ezstack/ezstack/internal/config"
)

// MockClient is a mock implementation of ClientInterface for testing
type MockClient struct {
	mu sync.Mutex

	// PRs stores mock PRs keyed by number
	PRs map[int]*PR

	// PRsByBranch stores PR numbers by branch name
	PRsByBranch map[string]int

	// NextPRNumber is the next PR number to assign
	NextPRNumber int

	// Owner and Repo for generating URLs
	Owner string
	Repo  string

	// Error overrides - set these to simulate errors
	CreatePRError   error
	GetPRError      error
	UpdatePRError   error
	ListOpenPRError error

	// Call tracking for assertions
	Calls []MockCall
}

// MockCall records a call to a mock method
type MockCall struct {
	Method string
	Args   []interface{}
}

// NewMockClient creates a new mock client with sensible defaults
func NewMockClient(owner, repo string) *MockClient {
	return &MockClient{
		PRs:          make(map[int]*PR),
		PRsByBranch:  make(map[string]int),
		NextPRNumber: 1,
		Owner:        owner,
		Repo:         repo,
		Calls:        []MockCall{},
	}
}

func (m *MockClient) recordCall(method string, args ...interface{}) {
	m.Calls = append(m.Calls, MockCall{Method: method, Args: args})
}

// CreatePR creates a mock PR
func (m *MockClient) CreatePR(title, body, head, base string, draft bool) (*PR, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recordCall("CreatePR", title, body, head, base, draft)

	if m.CreatePRError != nil {
		return nil, m.CreatePRError
	}

	number := m.NextPRNumber
	m.NextPRNumber++

	pr := &PR{
		Number:  number,
		URL:     fmt.Sprintf("https://github.com/%s/%s/pull/%d", m.Owner, m.Repo, number),
		Title:   title,
		Body:    body,
		State:   "open",
		Base:    base,
		Head:    head,
		IsDraft: draft,
	}

	m.PRs[number] = pr
	m.PRsByBranch[head] = number

	return pr, nil
}

// GetPR retrieves a mock PR by number
func (m *MockClient) GetPR(number int) (*PR, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recordCall("GetPR", number)

	if m.GetPRError != nil {
		return nil, m.GetPRError
	}

	pr, ok := m.PRs[number]
	if !ok {
		return nil, fmt.Errorf("PR #%d not found", number)
	}
	return pr, nil
}

// GetPRByBranch retrieves a mock PR by branch name
func (m *MockClient) GetPRByBranch(branch string) (*PR, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recordCall("GetPRByBranch", branch)

	if m.GetPRError != nil {
		return nil, m.GetPRError
	}

	number, ok := m.PRsByBranch[branch]
	if !ok {
		return nil, fmt.Errorf("no PR found for branch %s", branch)
	}
	return m.PRs[number], nil
}

// GetPRChecks returns mock check status
func (m *MockClient) GetPRChecks(number int) (*CheckStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recordCall("GetPRChecks", number)

	return &CheckStatus{
		State:   "success",
		Summary: "3/3 passed",
	}, nil
}

// UpdatePR updates a mock PR's body
func (m *MockClient) UpdatePR(number int, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recordCall("UpdatePR", number, body)

	if m.UpdatePRError != nil {
		return m.UpdatePRError
	}

	pr, ok := m.PRs[number]
	if !ok {
		return fmt.Errorf("PR #%d not found", number)
	}
	pr.Body = body
	return nil
}

// UpdatePRBase updates a mock PR's base branch
func (m *MockClient) UpdatePRBase(number int, base string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recordCall("UpdatePRBase", number, base)

	pr, ok := m.PRs[number]
	if !ok {
		return fmt.Errorf("PR #%d not found", number)
	}
	pr.Base = base
	return nil
}

// ListOpenPRs returns all mock open PRs
func (m *MockClient) ListOpenPRs() ([]OpenPR, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recordCall("ListOpenPRs")

	if m.ListOpenPRError != nil {
		return nil, m.ListOpenPRError
	}

	var prs []OpenPR
	for _, pr := range m.PRs {
		if pr.State == "open" {
			prs = append(prs, OpenPR{
				Number: pr.Number,
				Title:  pr.Title,
				Branch: pr.Head,
				URL:    pr.URL,
			})
		}
	}
	return prs, nil
}

// UpdateStackDescription updates mock PR descriptions with stack info
func (m *MockClient) UpdateStackDescription(stack *config.Stack, currentBranch string, skipBranches map[string]bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recordCall("UpdateStackDescription", stack, currentBranch, skipBranches)

	// Update each PR in the stack (simulating real behavior)
	for _, branch := range stack.Branches {
		if branch.PRNumber == 0 {
			continue
		}
		if skipBranches != nil && skipBranches[branch.Name] {
			continue
		}
		pr, ok := m.PRs[branch.PRNumber]
		if !ok {
			continue
		}
		// Append stack info to body (simplified)
		pr.Body = pr.Body + "\n\n---\n## PR Stack\n(mock stack info)"
	}

	return nil
}

// Ensure MockClient implements ClientInterface
var _ ClientInterface = (*MockClient)(nil)
