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
// Walks every stack in the current repo (or every repo when --all is set)
// and reports any AI session bound to it (stack-scoped) or to one of its
// branches (branch-scoped). Output is human-friendly by default with a
// "how to resume" hint, or machine-readable with --json so users can pipe
// it into jq/scripts.
//
// Empty cases are reported explicitly ("no sessions yet") instead of
// silently returning nothing — this is one of those commands users run when
// they're confused about state, and silence reads as "broken" more often
// than "everything's fine."
func agentList(args []string) error {
	fs := pflag.NewFlagSet("agent ls", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sList AI sessions tracked by ezs%s

%sUSAGE%s
    ezs agent ls [--all] [--json]
    ezs agent list [--all] [--json]   (alias)

%sOPTIONS%s
    -a, --all   List sessions across every repo recorded in stacks.json,
                grouped by repo path (default: only the current repo)
    --json      Emit a machine-readable JSON array instead of the text table
    -h, --help  Show this help message

%sDESCRIPTION%s
    Each row reports one ezstack-bound AI session: which stack or branch
    it's attached to, the display name shown in the agent's resume picker,
    a short prefix of the session ID, and the exact 'ezs agent' command
    that resumes it. Only sessions ezs has minted (display name prefixed
    with "_ezstack-") are listed; freestanding claude sessions are not.

    The text output is grouped: stack-scoped sessions first, then
    branch-scoped sessions. With --all, an additional outer grouping by
    repo path is added. JSON output has fields:
        scope         "stack" | "branch"
        repo_path     repo the session belongs to (always set under --all)
        stack_hash    hash of the stack the session belongs to
        stack_name    optional friendly name of the stack
        branch_name   set only when scope == "branch"
        display_name  the "_ezstack-<...>" label passed to the agent CLI
        session_id    full UUID
        resume_cmd    suggested 'ezs agent' invocation to resume; under
                      --all, prefixed with 'cd <repo> && ' for sessions
                      outside the current working directory's repo
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	allFlag := fs.BoolP("all", "a", false, "List sessions across all repos in stacks.json")
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
	currentRepo := getMainWorktreePath(g)

	var rows []agentSessionRow
	if *allFlag {
		rows, err = collectAllReposAgentSessions(currentRepo)
		if err != nil {
			return err
		}
	} else {
		// Single-repo path keeps the read-only manager so reconcile-style
		// state (worktree presence checks, etc.) is consistent with other
		// ezs commands. The cross-repo path bypasses Manager because most
		// of those checks aren't meaningful when listing from outside the
		// repo's working directory.
		mgr, mErr := stack.NewReadOnlyManager(cwd)
		if mErr != nil {
			return mErr
		}
		rows = collectAgentSessionsFromStackConfig(currentRepo, mgr.GetStackConfig(), currentRepo)
	}

	if *jsonFlag {
		return emitAgentSessionsJSON(rows)
	}
	emitAgentSessionsText(rows, currentRepo, *allFlag)
	return nil
}

// agentSessionRow is one entry in the `ezs agent ls` output. Field tags are
// the JSON contract documented in the help text — don't rename them without
// also updating the help and any consumer scripts.
type agentSessionRow struct {
	Scope       string `json:"scope"` // "stack" or "branch"
	RepoPath    string `json:"repo_path,omitempty"`
	StackHash   string `json:"stack_hash"`
	StackName   string `json:"stack_name,omitempty"`
	BranchName  string `json:"branch_name,omitempty"`
	DisplayName string `json:"display_name"`
	SessionID   string `json:"session_id"`
	ResumeCmd   string `json:"resume_cmd"`
}

// collectAllReposAgentSessions iterates every repo recorded in stacks.json
// and returns every ezstack-tracked session across all of them. Sessions
// outside the caller's current repo get a `cd <repo> && ` prefix on their
// resume command so the user knows where to run it.
//
// Sorted by (repoPath, scope, displayName) so output is stable across runs.
func collectAllReposAgentSessions(currentRepo string) ([]agentSessionRow, error) {
	all, err := config.LoadAllStackConfigs()
	if err != nil {
		return nil, err
	}
	rows := make([]agentSessionRow, 0)
	for repoPath, sc := range all {
		if sc == nil {
			continue
		}
		rows = append(rows, collectAgentSessionsFromStackConfig(repoPath, sc, currentRepo)...)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].RepoPath != rows[j].RepoPath {
			return rows[i].RepoPath < rows[j].RepoPath
		}
		if rows[i].Scope != rows[j].Scope {
			return rows[i].Scope == "stack"
		}
		return rows[i].DisplayName < rows[j].DisplayName
	})
	return rows, nil
}

// collectAgentSessionsFromStackConfig pulls every session out of one repo's
// StackConfig. Both single-repo (`agent ls`) and cross-repo (`agent ls -a`)
// paths funnel through here so the row construction is uniform.
//
// repoPath is the repo the StackConfig belongs to. currentRepo is the
// caller's current directory's repo — used to decide whether the resume
// command needs a `cd <repo> && ` prefix.
func collectAgentSessionsFromStackConfig(repoPath string, sc *config.StackConfig, currentRepo string) []agentSessionRow {
	if sc == nil {
		return nil
	}
	rows := make([]agentSessionRow, 0)

	// Sort stacks by hash so iteration is deterministic without an explicit
	// post-sort step (the caller still re-sorts to merge multiple repos in
	// the cross-repo case).
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
				RepoPath:    repoPath,
				StackHash:   s.Hash,
				StackName:   s.Name,
				DisplayName: sessionDisplayName(identifier, scopeStack),
				SessionID:   s.AgentSessionID,
				ResumeCmd:   prefixResumeCmd(resume, repoPath, currentRepo),
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
				RepoPath:    repoPath,
				StackHash:   s.Hash,
				StackName:   s.Name,
				BranchName:  name,
				DisplayName: sessionDisplayName(name, scopeBranch),
				SessionID:   bc.AgentSessionID,
				ResumeCmd:   prefixResumeCmd("ezs agent --branch "+quoteIfNeeded(name), repoPath, currentRepo),
			})
		}
	}
	return rows
}

// prefixResumeCmd prepends `cd <repo> && ` to the resume command when the
// session is in a different repo than the caller's cwd. Empty currentRepo
// or matching repos return cmd unchanged.
//
// Single-repo (default) callers pass repoPath == currentRepo, so this is a
// no-op for them. The cross-repo (-a) path is where it earns its keep.
func prefixResumeCmd(cmd, repoPath, currentRepo string) string {
	if repoPath == "" || currentRepo == "" || repoPath == currentRepo {
		return cmd
	}
	return "cd " + quoteIfNeeded(repoPath) + " && " + cmd
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

// emitAgentSessionsText renders rows in a human-friendly layout. Single-repo
// mode prints stack-scoped then branch-scoped under one header. --all mode
// adds an outer grouping by repo path so it's clear which sessions live
// where.
//
// Empty result is its own helpful message instead of a blank line.
func emitAgentSessionsText(rows []agentSessionRow, currentRepo string, allMode bool) {
	if len(rows) == 0 {
		if allMode {
			ui.Info("No tracked AI sessions in any repo yet.")
		} else {
			ui.Info(fmt.Sprintf("No tracked AI sessions in %s yet.", currentRepo))
		}
		ui.Info("Run 'ezs agent' to start one — ezs will track it for you.")
		return
	}

	if !allMode {
		fmt.Fprintf(os.Stderr, "%sTracked AI sessions in %s%s\n\n", ui.Bold, currentRepo, ui.Reset)
		emitGroupedRows(rows)
		return
	}

	// --all: group by repo. Rows are already sorted by RepoPath.
	fmt.Fprintf(os.Stderr, "%sTracked AI sessions across all repos%s\n\n", ui.Bold, ui.Reset)
	repoOrder := make([]string, 0)
	byRepo := make(map[string][]agentSessionRow)
	for _, r := range rows {
		if _, seen := byRepo[r.RepoPath]; !seen {
			repoOrder = append(repoOrder, r.RepoPath)
		}
		byRepo[r.RepoPath] = append(byRepo[r.RepoPath], r)
	}
	for _, repoPath := range repoOrder {
		// Highlight the user's current repo with a small marker so they can
		// spot it in a long list.
		marker := ""
		if repoPath == currentRepo {
			marker = " " + ui.Cyan + "(current)" + ui.Reset
		}
		fmt.Fprintf(os.Stderr, "%s──── %s%s%s%s%s\n\n", ui.Yellow, ui.Bold, repoPath, ui.Reset, marker, ui.Reset)
		emitGroupedRows(byRepo[repoPath])
	}
}

// emitGroupedRows prints one repo's rows split into stack-scoped and branch-
// scoped sub-blocks. Extracted so the single-repo and per-repo paths share
// the inner formatting.
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
