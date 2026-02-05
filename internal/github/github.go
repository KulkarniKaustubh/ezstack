package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/ezstack/ezstack/internal/config"
)

// Client wraps GitHub operations using gh CLI
type Client struct {
	owner string
	repo  string
}

// NewClient creates a new GitHub client by parsing the remote URL
func NewClient(remoteURL string) (*Client, error) {
	// Parse owner/repo from URL
	// Handles: git@github.com:owner/repo.git or https://github.com/owner/repo.git
	re := regexp.MustCompile(`github\.com[:/]([^/]+)/([^/.]+)`)
	matches := re.FindStringSubmatch(remoteURL)
	if len(matches) != 3 {
		return nil, fmt.Errorf("could not parse GitHub URL: %s", remoteURL)
	}

	return &Client{
		owner: matches[1],
		repo:  matches[2],
	}, nil
}

// PR represents a pull request
type PR struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	State       string `json:"state"`
	Base        string `json:"baseRefName"`
	Head        string `json:"headRefName"`
	MergedAt    string `json:"mergedAt"` // non-empty if merged
	Merged      bool   // computed from MergedAt
	Mergeable   string `json:"mergeable"`
	IsDraft     bool   `json:"isDraft"`
	ReviewState string `json:"reviewDecision"`
}

// CheckStatus represents CI check status
type CheckStatus struct {
	State   string // "success", "failure", "pending", "error"
	Summary string // e.g., "3/3 checks passed"
}

// CreatePR creates a new pull request
func (c *Client) CreatePR(title, body, head, base string, draft bool) (*PR, error) {
	args := []string{
		"pr", "create",
		"--title", title,
		"--body", body,
		"--base", base,
		"--head", head,
	}

	if draft {
		args = append(args, "--draft")
	}

	// gh pr create doesn't support --json, so we create first then fetch details
	_, err := c.runGH(args...)
	if err != nil {
		return nil, err
	}

	// Fetch the PR we just created using the head branch
	return c.GetPRByBranch(head)
}

// GetPRByBranch gets a PR by its head branch name
func (c *Client) GetPRByBranch(branch string) (*PR, error) {
	output, err := c.runGH("pr", "view", branch,
		"--json", "number,url,title,body,state,baseRefName,headRefName,mergedAt,mergeable,isDraft,reviewDecision")
	if err != nil {
		return nil, err
	}

	var pr PR
	if err := json.Unmarshal([]byte(output), &pr); err != nil {
		return nil, err
	}
	pr.Merged = pr.MergedAt != ""
	return &pr, nil
}

// GetPR gets a PR by number
func (c *Client) GetPR(number int) (*PR, error) {
	output, err := c.runGH("pr", "view", fmt.Sprintf("%d", number),
		"--json", "number,url,title,body,state,baseRefName,headRefName,mergedAt,mergeable,isDraft,reviewDecision")
	if err != nil {
		return nil, err
	}

	var pr PR
	if err := json.Unmarshal([]byte(output), &pr); err != nil {
		return nil, err
	}
	// Set Merged based on whether mergedAt is present
	pr.Merged = pr.MergedAt != ""
	return &pr, nil
}

// GetPRForBranch gets the PR for a branch
func (c *Client) GetPRForBranch(branch string) (*PR, error) {
	output, err := c.runGH("pr", "view", branch,
		"--json", "number,url,title,body,state,baseRefName,headRefName,mergedAt,mergeable,isDraft,reviewDecision")
	if err != nil {
		return nil, err
	}

	var pr PR
	if err := json.Unmarshal([]byte(output), &pr); err != nil {
		return nil, err
	}
	// Set Merged based on whether mergedAt is present
	pr.Merged = pr.MergedAt != ""
	return &pr, nil
}

// GetPRChecks gets the CI check status for a PR
func (c *Client) GetPRChecks(number int) (*CheckStatus, error) {
	output, err := c.runGH("pr", "checks", fmt.Sprintf("%d", number))
	if err != nil {
		// If checks fail to fetch, return unknown status
		return &CheckStatus{State: "unknown", Summary: "checks unavailable"}, nil
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return &CheckStatus{State: "none", Summary: "no checks"}, nil
	}

	passed := 0
	failed := 0
	pending := 0
	total := len(lines)

	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "pass") || strings.Contains(lower, "success") || strings.Contains(lower, "✓") {
			passed++
		} else if strings.Contains(lower, "fail") || strings.Contains(lower, "error") || strings.Contains(lower, "✗") || strings.Contains(lower, "x") {
			failed++
		} else if strings.Contains(lower, "pending") || strings.Contains(lower, "running") || strings.Contains(lower, "-") {
			pending++
		}
	}

	status := &CheckStatus{}
	if failed > 0 {
		status.State = "failure"
		status.Summary = fmt.Sprintf("%d/%d failed", failed, total)
	} else if pending > 0 {
		status.State = "pending"
		status.Summary = fmt.Sprintf("%d/%d pending", pending, total)
	} else if passed > 0 {
		status.State = "success"
		status.Summary = fmt.Sprintf("%d/%d passed", passed, total)
	} else {
		status.State = "none"
		status.Summary = "no checks"
	}

	return status, nil
}

// UpdatePR updates a PR's body
func (c *Client) UpdatePR(number int, body string) error {
	_, err := c.runGH("pr", "edit", fmt.Sprintf("%d", number), "--body", body)
	return err
}

// UpdatePRBase updates a PR's base branch
func (c *Client) UpdatePRBase(number int, base string) error {
	_, err := c.runGH("pr", "edit", fmt.Sprintf("%d", number), "--base", base)
	return err
}

// runGH executes a gh CLI command with the repository context
func (c *Client) runGH(args ...string) (string, error) {
	// Build args with -R flag after the subcommand (e.g., "pr view -R owner/repo ...")
	// gh expects: gh <command> <subcommand> -R owner/repo [args...]
	repoFlag := fmt.Sprintf("%s/%s", c.owner, c.repo)
	var fullArgs []string
	if len(args) >= 2 {
		// Insert -R after the subcommand (e.g., "pr view" -> "pr view -R owner/repo")
		fullArgs = append(fullArgs, args[0], args[1], "-R", repoFlag)
		fullArgs = append(fullArgs, args[2:]...)
	} else {
		// Fallback for single-arg commands
		fullArgs = append(args, "-R", repoFlag)
	}

	cmd := exec.Command("gh", fullArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		stderrStr := stderr.String()
		// Check for authentication errors
		if strings.Contains(stderrStr, "auth login") ||
			strings.Contains(stderrStr, "not logged") ||
			strings.Contains(stderrStr, "authentication") ||
			strings.Contains(stderrStr, "401") {
			return "", fmt.Errorf("GitHub authentication required. Run: gh auth login")
		}
		// Check for repository access errors
		if strings.Contains(stderrStr, "Could not resolve to a Repository") {
			return "", fmt.Errorf("cannot access repository %s. Check that your gh account has access. Run: gh auth status", repoFlag)
		}
		return "", fmt.Errorf("gh %s failed: %s\n%s", strings.Join(fullArgs, " "), err, stderrStr)
	}
	return stdout.String(), nil
}

// UpdateStackDescription updates PR descriptions with stack info
func (c *Client) UpdateStackDescription(stack *config.Stack, currentBranch string) error {
	for _, branch := range stack.Branches {
		if branch.PRNumber == 0 {
			continue
		}

		pr, err := c.GetPR(branch.PRNumber)
		if err != nil {
			continue
		}

		// Generate stack section with arrow pointing to THIS PR
		stackSection := generateStackSection(stack, branch.Name)

		// Update the body with the stack section
		newBody := updateBodyWithStack(pr.Body, stackSection, branch.Name == currentBranch)
		if newBody != pr.Body {
			if err := c.UpdatePR(branch.PRNumber, newBody); err != nil {
				return fmt.Errorf("failed to update PR #%d: %w", branch.PRNumber, err)
			}
		}
	}

	return nil
}

func generateStackSection(stack *config.Stack, currentPRBranch string) string {
	var sb strings.Builder
	sb.WriteString("\n\n---\n## PR Stack\n\n")

	for i, branch := range stack.Branches {
		// Use markdown list format - GitHub unfurls PR URLs in lists to show title
		suffix := ""
		if branch.Name == currentPRBranch {
			suffix = " ← **This PR**"
		}

		if branch.PRUrl != "" {
			sb.WriteString(fmt.Sprintf("%d. %s%s\n", i+1, branch.PRUrl, suffix))
		} else if branch.PRNumber > 0 {
			sb.WriteString(fmt.Sprintf("%d. #%d%s\n", i+1, branch.PRNumber, suffix))
		} else {
			sb.WriteString(fmt.Sprintf("%d. `%s` _(no PR yet)_%s\n", i+1, branch.Name, suffix))
		}
	}
	sb.WriteString("\n_This stack was created by `ezstack` (beta)_\n")

	return sb.String()
}

func updateBodyWithStack(body, stackSection string, isCurrent bool) string {
	// Remove existing stack section (matches various footer formats: *text*, _text_)
	re := regexp.MustCompile(`(?s)\n*---\n## (?:📚 )?PR Stack\n.*?[_*](?:Managed by|This stack was created by).*?ezstack.*?[_*]\n?`)
	body = re.ReplaceAllString(body, "")

	// Add new stack section
	return strings.TrimSpace(body) + stackSection
}
