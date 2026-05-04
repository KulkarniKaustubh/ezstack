package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

// agentList implements `ezs agent ls` (alias `ezs agent list`).
//
// Walks every stack in the current repo and reports any AI session bound to
// it (stack-scoped) or to one of its branches (branch-scoped). Output is
// human-friendly by default with a "how to resume" hint, or machine-readable
// with --json so users can pipe it into jq/scripts.
//
// Empty cases are reported explicitly ("no sessions yet") instead of
// silently returning nothing — this is one of those commands users run when
// they're confused about state, and silence reads as "broken" more often
// than "everything's fine."
func agentList(args []string) error {
	fs := pflag.NewFlagSet("agent ls", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sList AI sessions tracked by ezs in this repo%s

%sUSAGE%s
    ezs agent ls [--json]
    ezs agent list [--json]   (alias)

%sOPTIONS%s
    --json     Emit a machine-readable JSON array instead of the text table
    -h, --help Show this help message

%sDESCRIPTION%s
    Each row reports one ezstack-bound AI session: which stack or branch
    it's attached to, the display name shown in the agent's resume picker,
    a short prefix of the session ID, and the exact 'ezs agent' command
    that resumes it.

    The text output is grouped: stack-scoped sessions first, then
    branch-scoped sessions. JSON output has fields:
        scope         "stack" | "branch"
        stack_hash    hash of the stack the session belongs to
        stack_name    optional friendly name of the stack
        branch_name   set only when scope == "branch"
        display_name  the "_ezstack-<...>" label passed to the agent CLI
        session_id    full UUID
        resume_cmd    suggested 'ezs agent' invocation to resume
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	jsonFlag := fs.Bool("json", false, "Emit JSON instead of the human-readable table")
	helpFlag := fs.BoolP("help", "h", false, "Show help")

	if err := fs.Parse(args); err != nil {
		if err == pflag.ErrHelp {
			return nil
		}
		return err
	}
	if *helpFlag {
		fs.Usage()
		return nil
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("ezs agent ls takes no positional arguments (got %q)", fs.Arg(0))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	g := git.New(cwd)
	mgr, err := stack.NewReadOnlyManager(cwd)
	if err != nil {
		return err
	}
	repoPath := getMainWorktreePath(g)

	rows := collectAgentSessions(mgr, repoPath)

	if *jsonFlag {
		return emitAgentSessionsJSON(rows)
	}
	emitAgentSessionsText(rows, repoPath)
	return nil
}

// agentSessionRow is one entry in the `ezs agent ls` output. Field tags are
// the JSON contract documented in the help text — don't rename them without
// also updating the help and any consumer scripts.
type agentSessionRow struct {
	Scope       string `json:"scope"` // "stack" or "branch"
	StackHash   string `json:"stack_hash"`
	StackName   string `json:"stack_name,omitempty"`
	BranchName  string `json:"branch_name,omitempty"`
	DisplayName string `json:"display_name"`
	SessionID   string `json:"session_id"`
	ResumeCmd   string `json:"resume_cmd"`
}

// collectAgentSessions walks every stack in the manager's view and returns
// one row per attached session. Sorted with stacks first (alphabetical by
// display name) and branches after (alphabetical by branch name) so the
// output is stable across runs — important for diff-friendly piping.
func collectAgentSessions(mgr *stack.Manager, repoPath string) []agentSessionRow {
	stacks := mgr.ListStacks()
	rows := make([]agentSessionRow, 0)

	for _, s := range stacks {
		// Stack-scoped session.
		if s.AgentSessionID != "" {
			identifier := s.Name
			if identifier == "" {
				identifier = s.Hash
			}
			resume := "ezs agent -s " + s.Hash
			if s.Name != "" {
				resume = "ezs agent -s " + quoteIfNeeded(s.Name)
			}
			rows = append(rows, agentSessionRow{
				Scope:       "stack",
				StackHash:   s.Hash,
				StackName:   s.Name,
				DisplayName: sessionDisplayName(identifier, scopeStack),
				SessionID:   s.AgentSessionID,
				ResumeCmd:   resume,
			})
		}

		// Branch-scoped sessions. Reuse lookupBranchSessionID (defined in
		// agent_session.go) so we have a single source of truth for "which
		// cache field is the branch session ID stored in".
		for _, b := range s.Branches {
			if b == nil {
				continue
			}
			id := lookupBranchSessionID(repoPath, b.Name)
			if id == "" {
				continue
			}
			rows = append(rows, agentSessionRow{
				Scope:       "branch",
				StackHash:   s.Hash,
				StackName:   s.Name,
				BranchName:  b.Name,
				DisplayName: sessionDisplayName(b.Name, scopeBranch),
				SessionID:   id,
				ResumeCmd:   "ezs agent --branch " + quoteIfNeeded(b.Name),
			})
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Scope != rows[j].Scope {
			// "branch" sorts after "stack"
			return rows[i].Scope == "stack"
		}
		return rows[i].DisplayName < rows[j].DisplayName
	})
	return rows
}

// quoteIfNeeded wraps s in single quotes when it contains characters a
// shell would split on. Keeps copy-pasteable resume commands one-liners.
func quoteIfNeeded(s string) string {
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '"' || r == '\'' || r == '$' || r == '`' {
			return ShellQuote(s)
		}
	}
	return s
}

// emitAgentSessionsText renders rows in a human-friendly grouped layout.
// Empty result is its own helpful message instead of a blank line.
func emitAgentSessionsText(rows []agentSessionRow, repoPath string) {
	if len(rows) == 0 {
		ui.Info(fmt.Sprintf("No tracked AI sessions in %s yet.", repoPath))
		ui.Info("Run 'ezs agent' to start one — ezs will track it for you.")
		return
	}

	fmt.Fprintf(os.Stderr, "%sTracked AI sessions in %s%s\n\n", ui.Bold, repoPath, ui.Reset)

	stackRows := filterByScope(rows, "stack")
	branchRows := filterByScope(rows, "branch")

	if len(stackRows) > 0 {
		fmt.Fprintf(os.Stderr, "%sStack-scoped:%s\n", ui.Cyan, ui.Reset)
		for _, r := range stackRows {
			label := r.StackHash
			if r.StackName != "" {
				label = fmt.Sprintf("%s [%s]", r.StackName, r.StackHash)
			}
			fmt.Fprintf(os.Stderr, "  %s %s\n", ui.IconStack, ui.Bold+label+ui.Reset)
			fmt.Fprintf(os.Stderr, "    %sname:%s    %s\n", ui.Gray, ui.Reset, r.DisplayName)
			fmt.Fprintf(os.Stderr, "    %ssession:%s %s\n", ui.Gray, ui.Reset, shortSessionID(r.SessionID))
			fmt.Fprintf(os.Stderr, "    %sresume:%s  %s%s%s\n\n", ui.Gray, ui.Reset, ui.Cyan, r.ResumeCmd, ui.Reset)
		}
	}

	if len(branchRows) > 0 {
		fmt.Fprintf(os.Stderr, "%sBranch-scoped:%s\n", ui.Cyan, ui.Reset)
		for _, r := range branchRows {
			stackLabel := r.StackHash
			if r.StackName != "" {
				stackLabel = fmt.Sprintf("%s [%s]", r.StackName, r.StackHash)
			}
			fmt.Fprintf(os.Stderr, "  %s %s %s(in %s)%s\n", ui.IconBranch, ui.Bold+r.BranchName+ui.Reset, ui.Gray, stackLabel, ui.Reset)
			fmt.Fprintf(os.Stderr, "    %sname:%s    %s\n", ui.Gray, ui.Reset, r.DisplayName)
			fmt.Fprintf(os.Stderr, "    %ssession:%s %s\n", ui.Gray, ui.Reset, shortSessionID(r.SessionID))
			fmt.Fprintf(os.Stderr, "    %sresume:%s  %s%s%s\n\n", ui.Gray, ui.Reset, ui.Cyan, r.ResumeCmd, ui.Reset)
		}
	}
}

// emitAgentSessionsJSON writes the rows as a JSON array on stdout. Empty
// result emits "[]" — not an error, just nothing to list.
func emitAgentSessionsJSON(rows []agentSessionRow) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if rows == nil {
		rows = []agentSessionRow{}
	}
	return enc.Encode(rows)
}

func filterByScope(rows []agentSessionRow, scope string) []agentSessionRow {
	out := make([]agentSessionRow, 0, len(rows))
	for _, r := range rows {
		if r.Scope == scope {
			out = append(out, r)
		}
	}
	return out
}

// shortSessionID returns the first 8 characters of a UUID for display, with
// an ellipsis appended. Full UUIDs are noisy in a list view; the short form
// is enough to disambiguate at a glance and the JSON output keeps the full
// value for scripting.
func shortSessionID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}
