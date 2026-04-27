// cmd/docgen/main.go
// Generates the HTML command reference accordion for docs/index.html
// from DOCUMENTATION.md.
//
// Usage:
//
//	go run ./cmd/docgen                         # prints HTML snippet to stdout
//	go run ./cmd/docgen -w                      # rewrites docs/index.html in-place
//	go run ./cmd/docgen -w -doc DOCUMENTATION.md -html docs/index.html
package main

import (
	"flag"
	"fmt"
	"html"
	"os"
	"regexp"
	"strings"
)

// category groups commands for the accordion display.
type category struct {
	Label    string
	Commands []string // command keys as they appear in DOCUMENTATION.md headings, e.g. "new", "pr"
	Extra    string   // optional extra HTML to append after the table
}

// categories defines the accordion groupings and order.
// Keys must match the command name extracted from `### \`ezs <name>\“ headings.
// Combined headings like "commit / amend" are keyed by the first name ("commit").
// Sub-headings like "up / down" are keyed "down" (heading is `### \`ezs down\` / \`ezs up\“).
var categories = []category{
	{
		Label:    "Stack Management",
		Commands: []string{"new", "list", "status", "stack", "unstack", "delete", "reparent"},
	},
	{
		Label:    "Navigation",
		Commands: []string{"goto", "down"}, // "down" heading produces up+down rows; goto is its own heading
	},
	{
		Label:    "Sync & Workflow",
		Commands: []string{"sync", "commit", "push", "diff"},
	},
	{
		Label:    "PR Management",
		Commands: []string{"pr"},
	},
	{
		Label:    "Configuration & Utilities",
		Commands: []string{"config", "doctor", "upgrade"},
	},
	{
		Label:    "AI Agent",
		Commands: []string{"agent"},
		Extra: `            <div class="cmd-detail">
              <h4>Prompt Templates</h4>
              <p>Agent prompts are stored as editable Markdown files in <code>~/.ezstack/</code>:</p>
              <ul>
                <li><code>agent-work-prompt.md</code> &mdash; Work session prompt</li>
                <li><code>agent-feature-prompt.md</code> &mdash; Feature builder prompt</li>
              </ul>
              <p>Prompts use template variables replaced at runtime: <code>{{STACK_JSON}}</code>, <code>{{BRANCH_NAME}}</code>, <code>{{PARENT_NAME}}</code>, <code>{{WORKTREE_PATH}}</code>, <code>{{EZS_DOCS}}</code>, <code>{{FEATURE_DESCRIPTION}}</code>.</p>
            </div>`,
	},
}

// cmdInfo holds parsed information about a single command section.
type cmdInfo struct {
	// Raw heading text, e.g. "`ezs down` / `ezs up`"
	Heading string
	// Full body text (everything between this heading and the next ### or ## heading)
	Body string
}

func main() {
	docPath := flag.String("doc", "DOCUMENTATION.md", "path to DOCUMENTATION.md")
	htmlPath := flag.String("html", "docs/index.html", "path to index.html")
	write := flag.Bool("w", false, "rewrite index.html in-place (otherwise print snippet to stdout)")
	flag.Parse()

	data, err := os.ReadFile(*docPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", *docPath, err)
		os.Exit(1)
	}

	commands := parseCommands(string(data))
	snippet := renderAccordion(commands)

	if !*write {
		fmt.Print(snippet)
		return
	}

	htmlData, err := os.ReadFile(*htmlPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", *htmlPath, err)
		os.Exit(1)
	}

	result := injectSnippet(string(htmlData), snippet)
	if err := os.WriteFile(*htmlPath, []byte(result), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", *htmlPath, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Updated %s\n", *htmlPath)
}

// parseCommands extracts command sections from DOCUMENTATION.md.
// Returns a map keyed by the first command name in each heading.
func parseCommands(md string) map[string]cmdInfo {
	// Split on ### headings that contain `ezs ...`
	// Pattern: ### `ezs <name>`  (possibly with / `ezs <name2>`)
	headingRe := regexp.MustCompile("(?m)^### `ezs ")
	locs := headingRe.FindAllStringIndex(md, -1)

	result := make(map[string]cmdInfo)
	for i, loc := range locs {
		start := loc[0]
		var end int
		if i+1 < len(locs) {
			end = locs[i+1][0]
		} else {
			// Find next ## heading or end of file
			nextH2 := regexp.MustCompile("(?m)^## ").FindStringIndex(md[start+4:])
			if nextH2 != nil {
				end = start + 4 + nextH2[0]
			} else {
				end = len(md)
			}
		}

		section := md[start:end]
		lines := strings.SplitN(section, "\n", 2)
		heading := strings.TrimPrefix(lines[0], "### ")
		body := ""
		if len(lines) > 1 {
			body = strings.TrimSpace(lines[1])
		}

		// Extract the first command name from the heading.
		// Heading looks like: `ezs new` or `ezs down` / `ezs up` or `ezs commit` / `ezs amend`
		nameRe := regexp.MustCompile("`ezs ([a-z]+)`")
		m := nameRe.FindStringSubmatch(heading)
		if m == nil {
			continue
		}
		key := m[1]
		result[key] = cmdInfo{Heading: heading, Body: body}
	}

	return result
}

// commandDisplay describes how a command appears in the HTML table.
type commandDisplay struct {
	Name        string
	Aliases     string
	Description string
}

// extractDisplayInfo produces one or more display rows from a parsed command section.
func extractDisplayInfo(key string, info cmdInfo) []commandDisplay {
	switch key {
	case "commit":
		return extractCommitAmend(info)
	case "down":
		return extractUpDown(info)
	case "pr":
		return extractPR(info)
	case "config":
		return extractConfig(info)
	case "agent":
		return extractAgent(info)
	default:
		return []commandDisplay{extractSingle(key, info)}
	}
}

func extractSingle(key string, info cmdInfo) commandDisplay {
	aliases := extractAliases(info.Body)
	desc := extractFirstSentence(info.Body)
	return commandDisplay{Name: key, Aliases: aliases, Description: desc}
}

func extractCommitAmend(info cmdInfo) []commandDisplay {
	return []commandDisplay{
		{Name: "commit", Aliases: "ci", Description: "Commit and auto-sync child branches"},
		{Name: "amend", Aliases: "", Description: "Amend last commit and auto-sync children"},
	}
}

func extractUpDown(info cmdInfo) []commandDisplay {
	return []commandDisplay{
		{Name: "up", Aliases: "", Description: "Navigate up the stack toward parent"},
		{Name: "down", Aliases: "", Description: "Navigate down the stack toward children"},
	}
}

func extractPR(info cmdInfo) []commandDisplay {
	return []commandDisplay{
		{Name: "pr create", Aliases: "", Description: "Create a new pull request"},
		{Name: "pr update", Aliases: "", Description: "Push changes and update PR metadata"},
		{Name: "pr merge", Aliases: "", Description: "Merge a pull request (merge, squash, or rebase)"},
		{Name: "pr draft", Aliases: "", Description: "Toggle PR between draft and ready"},
		{Name: "pr stack", Aliases: "", Description: "Update all PR descriptions with stack info"},
	}
}

func extractConfig(info cmdInfo) []commandDisplay {
	return []commandDisplay{
		{Name: "config", Aliases: "cfg", Description: "Interactive configuration setup"},
		{Name: "config set", Aliases: "", Description: "Set a configuration value"},
		{Name: "config show", Aliases: "", Description: "Show current configuration"},
		{Name: "menu", Aliases: "", Description: "Interactive command menu"},
	}
}

func extractAgent(info cmdInfo) []commandDisplay {
	return []commandDisplay{
		{Name: "agent", Aliases: "", Description: "Launch AI agent scoped to a stack with full context"},
		{Name: "agent feature", Aliases: "", Description: "Break a feature into stacked branches via AI"},
		{Name: "agent prompt", Aliases: "", Description: "View or edit agent prompt templates"},
	}
}

// extractAliases pulls alias info from "Aliases: x, y" lines.
func extractAliases(body string) string {
	// Match the full Aliases line
	re := regexp.MustCompile(`(?m)Aliases?:\s*(.+)$`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	// Extract all backtick-quoted names
	nameRe := regexp.MustCompile("`([^`]+)`")
	matches := nameRe.FindAllStringSubmatch(m[1], -1)
	if len(matches) > 0 {
		var names []string
		for _, mm := range matches {
			names = append(names, mm[1])
		}
		return strings.Join(names, ", ")
	}
	// Fallback: use the raw text
	return strings.TrimSpace(m[1])
}

// extractFirstSentence gets the first meaningful line of description.
func extractFirstSentence(body string) string {
	inCodeBlock := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			continue
		}
		if trimmed == "" || trimmed == "---" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Handle lines with "Aliases:" — extract the part before it if any
		if strings.Contains(trimmed, "Aliases:") || strings.Contains(trimmed, "Alias:") {
			idx := strings.Index(trimmed, "Aliases:")
			if idx == 0 {
				continue
			}
			if idx < 0 {
				idx = strings.Index(trimmed, "Alias:")
			}
			if idx > 0 {
				trimmed = strings.TrimSpace(trimmed[:idx])
				if trimmed == "" {
					continue
				}
				trimmed = strings.ReplaceAll(trimmed, "`", "")
				if strings.HasSuffix(trimmed, ".") {
					return trimmed
				}
				return trimmed
			}
			continue
		}
		// Skip lines that look like usage syntax (start with ezs or Options:/Modes:/Subcommands:)
		if strings.HasPrefix(trimmed, "ezs ") || strings.HasPrefix(trimmed, "Options:") ||
			strings.HasPrefix(trimmed, "Modes:") || strings.HasPrefix(trimmed, "Subcommands:") {
			continue
		}
		// Skip table/pipe lines
		if strings.HasPrefix(trimmed, "|") {
			continue
		}
		// Clean up markdown formatting
		trimmed = strings.ReplaceAll(trimmed, "`", "")
		// Truncate at first period if present
		if idx := strings.Index(trimmed, ". "); idx > 0 && idx < 120 {
			return trimmed[:idx+1]
		}
		if strings.HasSuffix(trimmed, ".") {
			return trimmed
		}
		return trimmed
	}
	return ""
}

// renderAccordion generates the full HTML accordion snippet.
func renderAccordion(commands map[string]cmdInfo) string {
	var b strings.Builder
	b.WriteString("      <div class=\"accordion\" data-animate>\n")

	for _, cat := range categories {
		var rows []commandDisplay
		for _, cmdKey := range cat.Commands {
			info, ok := commands[cmdKey]
			if !ok {
				// For "goto" which is in the "down" combined heading, skip—
				// it's handled by extractUpDown via the "down" key.
				// For "down" in Navigation, it produces goto+up+down.
				continue
			}
			rows = append(rows, extractDisplayInfo(cmdKey, info)...)
		}

		if len(rows) == 0 {
			continue
		}

		b.WriteString("\n        <div class=\"accordion-item\">\n")
		b.WriteString("          <button class=\"accordion-header\" aria-expanded=\"false\">\n")
		b.WriteString(fmt.Sprintf("            <span class=\"accordion-label\">%s</span>\n", html.EscapeString(cat.Label)))
		cmdWord := "commands"
		if len(rows) == 1 {
			cmdWord = "command"
		}
		b.WriteString(fmt.Sprintf("            <span class=\"accordion-count\">%d %s</span>\n", len(rows), cmdWord))
		b.WriteString("            <svg class=\"accordion-chevron\" width=\"20\" height=\"20\" viewBox=\"0 0 20 20\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"2\"><polyline points=\"6 8 10 12 14 8\"/></svg>\n")
		b.WriteString("          </button>\n")
		b.WriteString("          <div class=\"accordion-body\">\n")
		b.WriteString("            <table class=\"cmd-table\">\n")
		b.WriteString("              <tbody>\n")

		for _, row := range rows {
			alias := ""
			if row.Aliases != "" {
				alias = fmt.Sprintf(" <span class=\"cmd-alias\">%s</span>", html.EscapeString(row.Aliases))
			}
			b.WriteString(fmt.Sprintf("                <tr><td class=\"cmd-name\"><code>%s</code>%s</td><td>%s</td></tr>\n",
				html.EscapeString(row.Name), alias, html.EscapeString(row.Description)))
		}

		b.WriteString("              </tbody>\n")
		b.WriteString("            </table>\n")
		if cat.Extra != "" {
			b.WriteString(cat.Extra + "\n")
		}
		b.WriteString("          </div>\n")
		b.WriteString("        </div>\n")
	}

	b.WriteString("\n      </div>\n")
	return b.String()
}

// injectSnippet replaces the accordion content inside the commands section of index.html.
// It looks for markers: <div class="accordion" ... > and the closing </div> before </section>.
func injectSnippet(htmlContent, snippet string) string {
	// Find the commands section accordion
	startMarker := `<div class="accordion" data-animate>`
	startIdx := strings.Index(htmlContent, startMarker)
	if startIdx == -1 {
		fmt.Fprintln(os.Stderr, "warning: could not find accordion start marker in index.html")
		return htmlContent
	}

	// Back up to the start of the line (consume leading whitespace)
	lineStart := startIdx
	for lineStart > 0 && htmlContent[lineStart-1] == ' ' {
		lineStart--
	}

	// Find the closing </div> for the accordion.
	// Count nesting to find the matching close.
	depth := 0
	searchStart := startIdx
	endIdx := -1
	for i := searchStart; i < len(htmlContent); i++ {
		if strings.HasPrefix(htmlContent[i:], "<div") {
			depth++
		} else if strings.HasPrefix(htmlContent[i:], "</div>") {
			depth--
			if depth == 0 {
				endIdx = i + len("</div>")
				break
			}
		}
	}

	if endIdx == -1 {
		fmt.Fprintln(os.Stderr, "warning: could not find accordion end marker in index.html")
		return htmlContent
	}

	// Consume trailing newline if present
	if endIdx < len(htmlContent) && htmlContent[endIdx] == '\n' {
		endIdx++
	}

	return htmlContent[:lineStart] + snippet + htmlContent[endIdx:]
}
