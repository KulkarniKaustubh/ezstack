package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

// agentList implements `ezs agent ls` (alias `ezs agent list`).
//
// Default scope is "every ezstack-bound AI session in the current repo" —
// always within the current repo, so users never see noise from unrelated
// repos in their terminal. Filter flags narrow the view to a smaller scope:
//
//   - --branch    show only the session bound to the user's current branch
//   - --stack     show only sessions bound to the user's current stack
//   - --feature   show only sessions created via `ezs agent feature`
//
// Filters are mutually exclusive: pass at most one. JSON output (--json) is
// the same shape as the text view minus the cosmetic header/grouping.
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
    ezs agent ls [filter] [--json]
    ezs agent list [filter] [--json]   (alias)

%sFILTERS%s (mutually exclusive — default is all sessions in current repo)
    -b, --branch     Show only the session bound to the current branch
    -s, --stack      Show only sessions bound to the current stack
    --feature        Show only sessions created via 'ezs agent feature'

%sOPTIONS%s
    --json           Emit a machine-readable JSON array instead of the text table
    -h, --help       Show this help message

%sDESCRIPTION%s
    Each row reports one ezstack-bound AI session: which stack or branch
    it's attached to, the display name shown in the agent's resume picker,
    a short prefix of the session ID, the launch mode (work or feature),
    and the exact 'ezs agent' command that resumes it. Only sessions ezs
    has minted (display name prefixed with "_ezstack-") are listed.

    The text output is grouped: stack-scoped sessions first, then
    branch-scoped sessions. JSON fields:
        scope         "stack" | "branch"
        mode          "work" | "feature"  (legacy entries written before
                      mode tracking show as "work")
        stack_hash    hash of the stack the session belongs to
        stack_name    optional friendly name of the stack
        branch_name   set only when scope == "branch"
        display_name  the "_ezstack-<...>" label passed to the agent CLI
        session_id    full UUID
        resume_cmd    suggested 'ezs agent' invocation to resume

    Filters resolve "current branch" / "current stack" from your git HEAD
    and ezstack tracking; running them outside a tracked branch errors
    instead of silently emitting an empty list.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	branchFilter := fs.BoolP("branch", "b", false, "Filter to current branch's session")
	stackFilter := fs.BoolP("stack", "s", false, "Filter to current stack's sessions")
	featureFilter := fs.Bool("feature", false, "Filter to feature-mode sessions")
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

	// Filters are mutually exclusive. A combined filter like `--branch
	// --stack` is ambiguous (different scopes) and would silently coerce to
	// one or the other; reject it up front so the user knows their input
	// didn't mean what they thought.
	pickedFilters := 0
	pickedNames := []string{}
	if *branchFilter {
		pickedFilters++
		pickedNames = append(pickedNames, "--branch")
	}
	if *stackFilter {
		pickedFilters++
		pickedNames = append(pickedNames, "--stack")
	}
	if *featureFilter {
		pickedFilters++
		pickedNames = append(pickedNames, "--feature")
	}
	if pickedFilters > 1 {
		return fmt.Errorf("filters %v are mutually exclusive — pick at most one", pickedNames)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	g := git.New(cwd)
	currentRepo := getMainWorktreePath(g)

	mgr, err := stack.NewReadOnlyManager(cwd)
	if err != nil {
		return err
	}

	rows := collectAgentSessionsFromStackConfig(currentRepo, mgr.GetStackConfig())

	// Apply the chosen filter (if any). The filter resolution can fail with
	// a clear "not on a stack branch" error rather than silently emptying
	// the list — that surface area is a real foot-gun for `agent ls
	// --branch` when the user is on main.
	switch {
	case *branchFilter:
		currentBranch, brErr := g.CurrentBranch()
		if brErr != nil || currentBranch == "" || currentBranch == "HEAD" {
			return fmt.Errorf("--branch needs a tracked current branch; HEAD is %q", currentBranch)
		}
		if mgr.GetBranch(currentBranch) == nil {
			return fmt.Errorf("--branch: current branch %q is not tracked by ezstack", currentBranch)
		}
		rows = filterRowsByBranch(rows, currentBranch)
	case *stackFilter:
		s, _, csErr := mgr.GetCurrentStack()
		if csErr != nil || s == nil {
			return fmt.Errorf("--stack needs a current stack; %v", csErr)
		}
		rows = filterRowsByStack(rows, s.Hash)
	case *featureFilter:
		rows = filterRowsByMode(rows, config.AgentSessionFeatureMode)
	}

	if *jsonFlag {
		return emitAgentSessionsJSON(rows)
	}
	emitAgentSessionsText(rows, currentRepo, filterLabel(*branchFilter, *stackFilter, *featureFilter))
	return nil
}

// agentSessionRow is one entry in the `ezs agent ls` output. Field tags are
// the JSON contract documented in the help text — don't rename them without
// also updating the help and any consumer scripts.
type agentSessionRow struct {
	Scope       string `json:"scope"`          // "stack" or "branch"
	Mode        string `json:"mode,omitempty"` // "work" or "feature"; empty on legacy rows
	StackHash   string `json:"stack_hash"`
	StackName   string `json:"stack_name,omitempty"`
	BranchName  string `json:"branch_name,omitempty"`
	DisplayName string `json:"display_name"`
	SessionID   string `json:"session_id"`
	ResumeCmd   string `json:"resume_cmd"`
}

// collectAgentSessionsFromStackConfig pulls every session out of the current
// repo's StackConfig. Returns an empty slice (not nil) when nothing is
// tracked, so JSON encoders emit `[]` and emit code paths can len-check
// without nil guards.
//
// Branch-scoped sessions are sourced from the cache (keyed by branch name)
// and only included when the branch still exists in some stack's tree —
// otherwise we'd emit orphans the user can't actually resume.
func collectAgentSessionsFromStackConfig(currentRepo string, sc *config.StackConfig) []agentSessionRow {
	if sc == nil {
		return nil
	}
	rows := make([]agentSessionRow, 0)

	// Sort stacks by hash so iteration is deterministic.
	hashes := make([]string, 0, len(sc.Stacks))
	for h := range sc.Stacks {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)

	for _, hash := range hashes {
		s := sc.Stacks[hash]
		if s == nil {
			continue
		}
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
				Mode:        modeOrDefault(s.AgentSessionMode),
				StackHash:   s.Hash,
				StackName:   s.Name,
				DisplayName: stackDisplayLabel(s),
				SessionID:   s.AgentSessionID,
				ResumeCmd:   resume,
			})
		}
		// Branch-scoped sessions live in the cache, keyed by branch name.
		// We iterate the cache directly (rather than s.Branches) so a row
		// surfaces even if the branch was removed from the tree but its
		// cache entry lingers — useful for "I deleted the branch, what
		// orphaned session is still around?"
		branchNames := make([]string, 0)
		if sc.Cache != nil {
			for name := range sc.Cache.Branches {
				branchNames = append(branchNames, name)
			}
		}
		sort.Strings(branchNames)
		for _, name := range branchNames {
			bc := sc.Cache.Branches[name]
			if bc == nil || bc.AgentSessionID == "" {
				continue
			}
			// Only include this branch under the stack it belongs to.
			// Without this guard, every stack would re-list every branch's
			// session (the cache is repo-wide, not stack-scoped).
			if !s.HasBranch(name) {
				continue
			}
			rows = append(rows, agentSessionRow{
				Scope:       "branch",
				Mode:        modeOrDefault(bc.AgentSessionMode),
				StackHash:   s.Hash,
				StackName:   s.Name,
				BranchName:  name,
				DisplayName: sessionDisplayName(name, scopeBranch),
				SessionID:   bc.AgentSessionID,
				ResumeCmd:   "ezs agent --branch " + quoteIfNeeded(name),
			})
		}
	}
	return rows
}

// stackDisplayLabel returns the "_ezstack-<...>" label for a stack-scoped
// session, picking up the feature-mode prefix when the stack's session was
// created via `ezs agent feature`. Without the feature-prefix branch, a
// stack with a feature-mode session would surface as `_ezstack-<name>` —
// indistinguishable from a work-mode session at the visual level, even
// though the stored mode tag is correct. Reproducing the launch-time label
// here keeps `agent ls` consistent with what the user saw in claude's
// /resume picker.
func stackDisplayLabel(s *config.Stack) string {
	identifier := s.Name
	if identifier == "" {
		identifier = s.Hash
	}
	if s.AgentSessionMode == config.AgentSessionFeatureMode {
		return sessionDisplayName("feature-"+identifier, scopeStack)
	}
	return sessionDisplayName(identifier, scopeStack)
}

// modeOrDefault returns the persisted mode, defaulting to
// AgentSessionWorkMode for legacy entries that pre-date mode tracking.
// The empty-string fallback makes JSON output stable: callers can rely on
// `mode` always being present and parseable.
func modeOrDefault(mode string) string {
	if mode == "" {
		return config.AgentSessionWorkMode
	}
	return mode
}

// filterRowsByBranch keeps only the row whose BranchName matches name.
// Returns an empty slice (not nil) so JSON output stays as `[]` rather than
// `null` when the current branch has no session yet.
func filterRowsByBranch(rows []agentSessionRow, name string) []agentSessionRow {
	out := make([]agentSessionRow, 0, 1)
	for _, r := range rows {
		if r.BranchName == name {
			out = append(out, r)
		}
	}
	return out
}

// filterRowsByStack keeps only rows belonging to the stack with hash.
// Includes both stack-scoped and branch-scoped sessions for that stack.
func filterRowsByStack(rows []agentSessionRow, hash string) []agentSessionRow {
	out := make([]agentSessionRow, 0)
	for _, r := range rows {
		if r.StackHash == hash {
			out = append(out, r)
		}
	}
	return out
}

// filterRowsByMode keeps only rows whose Mode matches.
func filterRowsByMode(rows []agentSessionRow, mode string) []agentSessionRow {
	out := make([]agentSessionRow, 0)
	for _, r := range rows {
		if r.Mode == mode {
			out = append(out, r)
		}
	}
	return out
}

// filterLabel returns a short human-readable description of the active
// filter for the empty-list message. Empty string when no filter is active.
func filterLabel(branch, stack, feature bool) string {
	switch {
	case branch:
		return "for the current branch"
	case stack:
		return "for the current stack"
	case feature:
		return "in feature mode"
	}
	return ""
}

// quoteIfNeeded wraps s in single quotes when it contains any character that
// a POSIX shell could interpret. Uses an allowlist (the same conservative set
// as Python's shlex.quote: alnum, "_", "-", ".", "/", ":", ",", "+", "=",
// "@", "%") so the output is always safe to paste into a shell — even when
// branch or repo names contain shell metacharacters like &, |, ;, (, ), <, >,
// or whitespace.
//
// Strings made entirely of safe characters are returned unquoted to keep the
// common case readable. The empty string is returned as ” so callers that
// concatenate this into a command (e.g. `cd ` + quoteIfNeeded(path)) never
// produce a torn argv.
func quoteIfNeeded(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-', r == '.', r == '/',
			r == ':', r == ',', r == '+', r == '=',
			r == '@', r == '%':
			// safe — keep scanning
		default:
			return ShellQuote(s)
		}
	}
	return s
}

// emitAgentSessionsText renders rows in a human-friendly layout grouped as
// stack-scoped first, branch-scoped after.
//
// Empty result is its own helpful message instead of a blank line. The
// message names the active filter (when any) so users on `--branch` against
// a session-less branch don't see "no sessions in <repo>" and assume the
// command itself is broken.
func emitAgentSessionsText(rows []agentSessionRow, currentRepo, filterDesc string) {
	if len(rows) == 0 {
		switch {
		case filterDesc != "":
			ui.Info(fmt.Sprintf("No tracked AI sessions %s.", filterDesc))
		default:
			ui.Info(fmt.Sprintf("No tracked AI sessions in %s yet.", currentRepo))
		}
		ui.Info("Run 'ezs agent' to start one — ezs will track it for you.")
		return
	}

	header := fmt.Sprintf("Tracked AI sessions in %s", currentRepo)
	if filterDesc != "" {
		header += " " + filterDesc
	}
	fmt.Fprintf(os.Stderr, "%s%s%s\n\n", ui.Bold, header, ui.Reset)
	emitGroupedRows(rows)
}

// emitGroupedRows prints rows split into stack-scoped and branch-scoped
// sub-blocks. Each row also surfaces its mode tag so users can tell at a
// glance which session came from `ezs agent` vs `ezs agent feature`.
func emitGroupedRows(rows []agentSessionRow) {
	stackRows := filterByScope(rows, "stack")
	branchRows := filterByScope(rows, "branch")

	if len(stackRows) > 0 {
		fmt.Fprintf(os.Stderr, "%sStack-scoped:%s\n", ui.Cyan, ui.Reset)
		for _, r := range stackRows {
			label := r.StackHash
			if r.StackName != "" {
				label = fmt.Sprintf("%s [%s]", r.StackName, r.StackHash)
			}
			fmt.Fprintf(os.Stderr, "  %s %s %s(%s)%s\n", ui.IconStack, ui.Bold+label+ui.Reset, ui.Gray, r.Mode, ui.Reset)
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
			fmt.Fprintf(os.Stderr, "  %s %s %s(in %s, %s)%s\n", ui.IconBranch, ui.Bold+r.BranchName+ui.Reset, ui.Gray, stackLabel, r.Mode, ui.Reset)
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
