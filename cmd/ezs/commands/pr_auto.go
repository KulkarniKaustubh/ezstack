package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
)

// `ezs pr create --auto` (alias `--ai`) hands the PR's diff, commits, and the
// repo's pull-request template to the configured AI agent and asks it to
// produce {"title", "body"}. The result is fed into the existing PR-create
// flow so all the regular gating (push, base validation, fork detection,
// stack-description update) still happens. The user-facing flow is
// non-interactive for the title/body — but Draft-vs-Ready and force-push
// prompts still apply because those are policy decisions, not content.
//
// Claude is the only supported agent at the moment. Other agent CLIs differ
// too much (input shape, exit semantics, JSON output options) for ezs to
// drive automatically. Passing `--cmd` to point at a non-claude binary will
// be rejected with a clear error so users don't silently get an empty PR.

// aiPRPromptTemplate is the prompt fed to the agent. It deliberately asks
// for raw JSON only; we do tolerate ```json fences in the response, but the
// less of that the better. We provide explicit failure instructions so the
// model returns a parseable answer even when the diff is empty.
const aiPRPromptTemplate = `You are writing a pull request description for a code change.

## Branch
- Name: {{BRANCH_NAME}}
- Parent (PR base): {{PARENT_NAME}}

## Commits (newest first)
{{COMMITS}}

## Diff (truncated if very large)
` + "```diff\n{{DIFF}}\n```" + `

## PR template (from the repository's pull_request_template.md)

{{TEMPLATE_OR_NONE}}

## Output

Respond with ONLY a single JSON object on one line, no markdown fences, no
commentary, no preface, no trailing prose. Both fields are required.

{"title":"<imperative-mood, <72 chars, no trailing period>","body":"<markdown body; if a template is provided above, fill it in based on the diff and commits, otherwise write a Summary + Test plan section>"}
`

// aiPRDiffMaxBytes caps how much diff we ship to the agent. Beyond ~80KB the
// model's context fills with diff and the quality of the description drops.
// Truncation appends a short marker so the model knows the diff is partial.
const aiPRDiffMaxBytes = 80 * 1024

// aiPRResult is what the agent returns. Field tags are lowercase to match the
// JSON contract above.
type aiPRResult struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// aiPRGenerator produces a title and body for a branch using an AI agent.
// Defined as an interface so tests can swap in a deterministic generator
// without spawning a real CLI.
type aiPRGenerator interface {
	Generate(prompt string) (aiPRResult, error)
}

// claudePRGenerator implements aiPRGenerator by shelling out to claude CLI in
// non-interactive mode (`-p`). The agent's full configured command (e.g.
// `"claude --model opus"`) is honored — we only require it to be claude-family
// so we can rely on `-p` for non-interactive output.
type claudePRGenerator struct {
	agentCmd string
	cwd      string // repo root; passed as cmd.Dir so claude resolves CLAUDE.md, settings, etc.
}

func (c *claudePRGenerator) Generate(prompt string) (aiPRResult, error) {
	fields := strings.Fields(c.agentCmd)
	if len(fields) == 0 {
		return aiPRResult{}, fmt.Errorf("agent_command is empty")
	}
	args := append([]string{}, fields[1:]...)
	args = append(args, "-p", prompt)
	cmd := exec.Command(fields[0], args...)
	cmd.Dir = c.cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return aiPRResult{}, fmt.Errorf("agent CLI %q failed: %w (stderr: %s)", fields[0], err, strings.TrimSpace(stderr.String()))
	}
	res, err := parseAIPRResponse(stdout.String())
	if err != nil {
		return aiPRResult{}, fmt.Errorf("parsing agent response: %w (raw: %s)", err, truncateForError(stdout.String(), 200))
	}
	return res, nil
}

// parseAIPRResponse extracts a JSON {"title","body"} object from the agent's
// output. It tolerates leading/trailing whitespace, an optional `\`\`\`json`
// (or bare `\`\`\``) code fence, and any prose before/after the object.
//
// Strategy: scan for the first `{` and the matching closing `}` (depth-aware,
// honoring strings) and decode that span as JSON. If the model wraps the
// answer in markdown fences, the fence chars are outside the scanned span so
// they don't break decoding.
func parseAIPRResponse(raw string) (aiPRResult, error) {
	span, ok := findFirstJSONObject(raw)
	if !ok {
		return aiPRResult{}, fmt.Errorf("no JSON object found in response")
	}
	var res aiPRResult
	if err := json.Unmarshal([]byte(span), &res); err != nil {
		return aiPRResult{}, fmt.Errorf("decode JSON: %w", err)
	}
	if strings.TrimSpace(res.Title) == "" {
		return aiPRResult{}, fmt.Errorf("response missing title")
	}
	if strings.TrimSpace(res.Body) == "" {
		return aiPRResult{}, fmt.Errorf("response missing body")
	}
	return res, nil
}

// findFirstJSONObject returns the substring of s containing the first
// brace-balanced JSON object. Honors quoted strings (so a `}` inside a
// string literal doesn't close the object) and escape sequences. Returns
// "", false when no balanced object is found.
func findFirstJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

func truncateForError(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// buildAIPRPrompt fills aiPRPromptTemplate with branch context. Empty
// templates are replaced with an explicit "(no template)" so the model
// knows to fall back to a Summary/Test-plan layout.
func buildAIPRPrompt(branchName, parentName, diff string, commits []git.Commit, template string) string {
	commitLines := make([]string, 0, len(commits))
	for _, c := range commits {
		short := c.Hash
		if len(short) > 7 {
			short = short[:7]
		}
		commitLines = append(commitLines, fmt.Sprintf("- %s %s", short, c.Subject))
	}
	commitsBlock := "(none)"
	if len(commitLines) > 0 {
		commitsBlock = strings.Join(commitLines, "\n")
	}

	templateBlock := "(no PR template found in this repository — write a Summary + Test plan section)"
	if t := strings.TrimSpace(template); t != "" {
		templateBlock = "```markdown\n" + t + "\n```"
	}

	if len(diff) > aiPRDiffMaxBytes {
		diff = diff[:aiPRDiffMaxBytes] + "\n\n[diff truncated — original was " + fmt.Sprintf("%d", len(diff)) + " bytes]\n"
	}

	r := strings.NewReplacer(
		"{{BRANCH_NAME}}", branchName,
		"{{PARENT_NAME}}", parentName,
		"{{COMMITS}}", commitsBlock,
		"{{DIFF}}", diff,
		"{{TEMPLATE_OR_NONE}}", templateBlock,
	)
	return r.Replace(aiPRPromptTemplate)
}

// generatePRContent gathers the diff/commits/template for a single branch and
// asks the agent to produce a {title, body} pair. Returns a clear error when
// the inputs are unusable (no diff, agent failure, malformed JSON) so the
// caller can decide whether to skip or fall back to interactive prompts.
func generatePRContent(g *git.Git, gen aiPRGenerator, branch *config.Branch) (aiPRResult, error) {
	parent := branch.Parent
	parentRef := parent
	if g.RemoteBranchExists(parent) {
		parentRef = "origin/" + parent
	}

	diff, err := g.RunCapture("diff", parentRef+"..."+branch.Name)
	if err != nil {
		return aiPRResult{}, fmt.Errorf("git diff %s...%s: %w", parentRef, branch.Name, err)
	}
	if strings.TrimSpace(diff) == "" {
		return aiPRResult{}, fmt.Errorf("no diff between %s and %s — make at least one commit before using --auto", parentRef, branch.Name)
	}

	commits, err := g.GetCommitsBetween(parentRef, branch.Name)
	if err != nil {
		// Non-fatal: commits are nice to have but the diff is the main signal.
		commits = nil
	}

	template := g.GetPRTemplate()
	prompt := buildAIPRPrompt(branch.Name, parent, diff, commits, template)
	return gen.Generate(prompt)
}

// newPRGeneratorForAgent returns the right aiPRGenerator for the configured
// agent CLI, or an error when the agent isn't supported. We restrict --auto
// to claude family because we can't reason about another tool's output shape.
//
// Variable so tests can substitute a deterministic generator without
// reimplementing the agent-detection logic.
var newPRGeneratorForAgent = func(agentCmd, cwd string) (aiPRGenerator, error) {
	if !isClaudeFamily(agentCmd) {
		return nil, fmt.Errorf("--auto currently requires a Claude-family agent (configured: %q). Use 'ezs config set agent_command claude' or pass --cmd claude", agentCmd)
	}
	if _, err := exec.LookPath(strings.Fields(agentCmd)[0]); err != nil {
		return nil, fmt.Errorf("agent CLI %q not found in PATH", strings.Fields(agentCmd)[0])
	}
	return &claudePRGenerator{agentCmd: agentCmd, cwd: cwd}, nil
}

// buildAIPRGenerator is the helper used by all `pr create --auto` paths to
// resolve the configured agent and construct an aiPRGenerator for it. cwd
// should be inside (or equal to) the target repo so we can locate the main
// worktree for config lookup. Returns a clear error on misconfiguration so
// the user fails fast before any GitHub API calls are made.
func buildAIPRGenerator(cwd string) (aiPRGenerator, error) {
	g := git.New(cwd)
	repoPath := getMainWorktreePath(g)
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	repoCfg := cfg.GetRepoConfig(repoPath)
	agentCmd := repoCfg.GetAgentCommand()
	if strings.TrimSpace(agentCmd) == "" {
		return nil, fmt.Errorf("agent_command is not configured for this repo. Set it: ezs config set agent_command claude")
	}
	return newPRGeneratorForAgent(agentCmd, repoPath)
}
