package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
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

// CheckAuth verifies that the gh CLI is authenticated and returns an error if not.
func CheckAuth() error {
	cmd := exec.Command("gh", "auth", "status")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("GitHub authentication required. Run: gh auth login")
	}
	return nil
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

	// Fork-related fields
	HeadRepoOwner       string `json:"headRepositoryOwner_login"` // set via custom parsing
	HeadRepoName        string `json:"headRepository_name"`       // set via custom parsing
	MaintainerCanModify bool   // whether the repo maintainer can push to the fork's PR branch
	IsFork              bool   // computed: true if head repo owner differs from base repo owner
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
		// gh pr view <branch> doesn't find PRs from forks.
		// Fall back to gh pr list --head <branch> which does.
		return c.getPRByHeadBranch(branch)
	}

	var pr PR
	if err := json.Unmarshal([]byte(output), &pr); err != nil {
		return nil, fmt.Errorf("failed to parse PR response for branch %q: %w", branch, err)
	}
	pr.Merged = pr.MergedAt != ""
	return &pr, nil
}

// getPRByHeadBranch finds a PR by head branch name using gh pr list.
// This works for fork PRs where gh pr view <branch> fails.
func (c *Client) getPRByHeadBranch(branch string) (*PR, error) {
	output, err := c.runGH("pr", "list", "--head", branch, "--state", "all", "--limit", "1",
		"--json", "number,url,title,body,state,baseRefName,headRefName,mergedAt,mergeable,isDraft,reviewDecision")
	if err != nil {
		return nil, err
	}

	var prs []PR
	if err := json.Unmarshal([]byte(output), &prs); err != nil {
		return nil, fmt.Errorf("failed to parse PR list response for branch %q: %w", branch, err)
	}
	if len(prs) == 0 {
		return nil, fmt.Errorf("no pull requests found for branch %q", branch)
	}
	prs[0].Merged = prs[0].MergedAt != ""
	return &prs[0], nil
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
		return nil, fmt.Errorf("failed to parse PR response for #%d: %w", number, err)
	}
	// Set Merged based on whether mergedAt is present
	pr.Merged = pr.MergedAt != ""
	return &pr, nil
}

// PRForkInfo contains fork-related information for a PR
type PRForkInfo struct {
	HeadRepoOwner       string // GitHub username/org that owns the head (source) repo
	HeadRepoName        string // Name of the head repo
	MaintainerCanModify bool   // Whether the repo maintainer can push to the fork branch
	IsFork              bool   // True if the PR is from a fork (head owner != base repo owner)
}

// GetPRForkInfo fetches fork-related metadata for a PR.
// Returns info about whether the PR is from a fork and whether maintainers can push.
func (c *Client) GetPRForkInfo(number int) (*PRForkInfo, error) {
	output, err := c.runGH("pr", "view", fmt.Sprintf("%d", number),
		"--json", "headRepositoryOwner,headRepository,maintainerCanModify")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch PR fork info for #%d: %w", number, err)
	}

	var raw struct {
		HeadRepoOwner struct {
			Login string `json:"login"`
		} `json:"headRepositoryOwner"`
		HeadRepo struct {
			Name string `json:"name"`
		} `json:"headRepository"`
		MaintainerCanModify bool `json:"maintainerCanModify"`
	}
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse PR fork info for #%d: %w", number, err)
	}

	info := &PRForkInfo{
		HeadRepoOwner:       raw.HeadRepoOwner.Login,
		HeadRepoName:        raw.HeadRepo.Name,
		MaintainerCanModify: raw.MaintainerCanModify,
		IsFork:              raw.HeadRepoOwner.Login != "" && raw.HeadRepoOwner.Login != c.owner,
	}
	return info, nil
}

// GetCurrentUser returns the GitHub username of the currently authenticated user.
func GetCurrentUser() (string, error) {
	cmd := exec.Command("gh", "api", "user", "--jq", ".login")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to get current user: %s", strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// CanPushToRepo checks whether the given GitHub user has push (write) access to
// a specific repository. Returns true if the user has write, maintain, or admin
// permission. Returns false if the user has no push access or if the API call fails
// (e.g., 403 means no push access).
func CanPushToRepo(repoOwner, repoName, username string) bool {
	endpoint := fmt.Sprintf("repos/%s/%s/collaborators/%s/permission", repoOwner, repoName, username)
	cmd := exec.Command("gh", "api", endpoint, "--jq", ".permission")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// 403 means no push access. Network errors and other failures also
		// return false — it's safer to skip push (user can push manually)
		// than to assume access and fail confusingly at push time.
		return false
	}
	perm := strings.TrimSpace(stdout.String())
	return perm == "write" || perm == "maintain" || perm == "admin"
}

// GetPRChecks gets the CI check status for a PR
func (c *Client) GetPRChecks(number int) (*CheckStatus, error) {
	output, err := c.runGH("pr", "checks", fmt.Sprintf("%d", number))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch PR checks for #%d: %w", number, err)
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return &CheckStatus{State: "none", Summary: "no checks"}, nil
	}

	passed := 0
	failed := 0
	pending := 0

	for _, line := range lines {
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)

		// Try to parse summary line first (when output is piped, gh may include this):
		// "0 cancelled, 1 failing, 10 successful, 0 skipped, and 0 pending checks"
		if strings.Contains(lower, "successful") && strings.Contains(lower, "pending checks") {
			re := regexp.MustCompile(`(\d+)\s+failing`)
			if matches := re.FindStringSubmatch(lower); len(matches) > 1 {
				if n, err := strconv.Atoi(matches[1]); err == nil {
					failed = n
				}
			}
			re = regexp.MustCompile(`(\d+)\s+successful`)
			if matches := re.FindStringSubmatch(lower); len(matches) > 1 {
				if n, err := strconv.Atoi(matches[1]); err == nil {
					passed = n
				}
			}
			re = regexp.MustCompile(`(\d+)\s+pending`)
			if matches := re.FindStringSubmatch(lower); len(matches) > 1 {
				if n, err := strconv.Atoi(matches[1]); err == nil {
					pending = n
				}
			}
			// Found summary line, use these counts
			break
		}

		// Otherwise parse individual check lines (space or tab separated):
		// "Check Name      pass    10s     https://..."
		// Split by whitespace and look for status keywords
		fields := strings.Fields(lower)
		for _, field := range fields {
			if field == "pass" {
				passed++
				break
			} else if field == "fail" {
				failed++
				break
			} else if field == "pending" || field == "running" {
				pending++
				break
			}
		}
	}

	total := passed + failed + pending
	status := &CheckStatus{}
	if total == 0 {
		status.State = "none"
		status.Summary = "no checks"
	} else if failed > 0 {
		status.State = "failure"
		status.Summary = fmt.Sprintf("%d/%d failed", failed, total)
	} else if pending > 0 {
		status.State = "pending"
		status.Summary = fmt.Sprintf("%d/%d pending", pending, total)
	} else {
		status.State = "success"
		status.Summary = fmt.Sprintf("%d/%d passed", passed, total)
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

// MergePR merges a pull request using the specified method (merge, squash, rebase)
func (c *Client) MergePR(number int, method string, deleteRemoteBranch bool) error {
	args := []string{"pr", "merge", fmt.Sprintf("%d", number), "--" + method}
	if deleteRemoteBranch {
		args = append(args, "--delete-branch")
	}
	_, err := c.runGH(args...)
	return err
}

// SetPRDraft marks a PR as draft
func (c *Client) SetPRDraft(number int) error {
	_, err := c.runGH("pr", "ready", fmt.Sprintf("%d", number), "--undo")
	return err
}

// SetPRReady marks a draft PR as ready for review
func (c *Client) SetPRReady(number int) error {
	_, err := c.runGH("pr", "ready", fmt.Sprintf("%d", number))
	return err
}

// OpenPR represents a minimal PR for listing
type OpenPR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Branch string `json:"headRefName"`
	Author string `json:"author"`
	URL    string `json:"url"`
}

// ListOpenPRs returns all open PRs in the repository
func (c *Client) ListOpenPRs() ([]OpenPR, error) {
	output, err := c.runGH("pr", "list", "--state", "open", "--json", "number,title,headRefName,url,author", "--limit", "300")
	if err != nil {
		return nil, err
	}

	var prs []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Branch string `json:"headRefName"`
		URL    string `json:"url"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if err := json.Unmarshal([]byte(output), &prs); err != nil {
		return nil, fmt.Errorf("failed to parse PR list response: %w", err)
	}

	result := make([]OpenPR, len(prs))
	for i, pr := range prs {
		result[i] = OpenPR{
			Number: pr.Number,
			Title:  pr.Title,
			Branch: pr.Branch,
			Author: pr.Author.Login,
			URL:    pr.URL,
		}
	}
	return result, nil
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
		// gh pr checks returns exit code 1 when there are failing checks,
		// but still outputs valid data we can parse
		if len(args) >= 2 && args[0] == "pr" && args[1] == "checks" && stdout.Len() > 0 {
			return stdout.String(), nil
		}
		return "", fmt.Errorf("gh %s failed: %s\n%s", strings.Join(fullArgs, " "), err, stderrStr)
	}
	return stdout.String(), nil
}

// UpdateStackDescription updates PR descriptions with stack info.
func (c *Client) UpdateStackDescription(stack *config.Stack, currentBranch string) error {
	// Count how many PRs are in the stack (including root PR if present)
	prCount := 0
	if stack.RootPRNumber > 0 {
		prCount++
	}
	for _, branch := range stack.Branches {
		if branch.PRNumber > 0 {
			prCount++
		}
	}

	// Only update descriptions when there are 2+ PRs in the stack
	if prCount < 2 {
		return nil
	}

	for _, branch := range stack.Branches {
		if branch.PRNumber == 0 {
			continue
		}

		// Fetch current PR body to update it
		pr, err := c.GetPR(branch.PRNumber)
		if err != nil {
			continue
		}

		// Generate stack section with arrow pointing to THIS PR
		// Uses PR numbers/URLs from the config cache (.ezstack.json)
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

// UpdateStackDescriptionCached updates PR descriptions using pre-fetched PR data to avoid extra API calls.
func (c *Client) UpdateStackDescriptionCached(stack *config.Stack, currentBranch string, prMap map[int]*PR) error {
	// Count how many PRs are in the stack (including root PR if present)
	prCount := 0
	if stack.RootPRNumber > 0 {
		prCount++
	}
	for _, branch := range stack.Branches {
		if branch.PRNumber > 0 {
			prCount++
		}
	}

	// Only update descriptions when there are 2+ PRs in the stack
	if prCount < 2 {
		return nil
	}

	for _, branch := range stack.Branches {
		if branch.PRNumber == 0 {
			continue
		}

		// Use pre-fetched PR data instead of making another API call
		pr := prMap[branch.PRNumber]
		if pr == nil {
			continue
		}

		stackSection := generateStackSection(stack, branch.Name)
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

	num := 1

	// Include root PR first if present (e.g., remote base branch PR)
	if stack.RootPRNumber > 0 {
		if stack.RootPRUrl != "" {
			sb.WriteString(fmt.Sprintf("%d. %s (base)\n", num, stack.RootPRUrl))
		} else {
			sb.WriteString(fmt.Sprintf("%d. #%d (base)\n", num, stack.RootPRNumber))
		}
		num++
	}

	// Sort branches topologically (parent -> child order)
	sortedBranches := config.SortBranchesTopologically(stack.Branches)

	for _, branch := range sortedBranches {
		// Skip branches that don't have a PR yet
		if branch.PRNumber == 0 && branch.PRUrl == "" {
			continue
		}

		// Use markdown list format - GitHub unfurls PR URLs in lists to show title
		suffix := ""
		if branch.Name == currentPRBranch {
			suffix = " ← **This PR**"
		}

		if branch.PRUrl != "" {
			sb.WriteString(fmt.Sprintf("%d. %s%s\n", num, branch.PRUrl, suffix))
		} else {
			sb.WriteString(fmt.Sprintf("%d. #%d%s\n", num, branch.PRNumber, suffix))
		}
		num++
	}
	sb.WriteString("\n_This stack was created by [ezstack](https://github.com/KulkarniKaustubh/ezstack)_\n")

	return sb.String()
}

func updateBodyWithStack(body, stackSection string, isCurrent bool) string {
	// Normalize line endings (GitHub API may return \r\n)
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")

	// Remove existing stack section - it's always appended at the end,
	// so we just truncate from the first "---\n## PR Stack" marker onwards
	for _, marker := range []string{"---\n## PR Stack", "---\n## 📚 PR Stack"} {
		if idx := strings.Index(body, marker); idx != -1 {
			body = body[:idx]
			break
		}
	}

	// Add new stack section
	return strings.TrimSpace(body) + stackSection
}
