package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/github"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

// List lists all stacks and branches
func List(args []string) error {
	fs := pflag.NewFlagSet("list", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sList all stacks and branches%s

%sUSAGE%s
    ezs list [options]
    ezs ls [options]

%sOPTIONS%s
    -a, --all     Show all stacks
    --json        Output as JSON (machine-readable)
    -d, --debug   Show debug output
    -h, --help    Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	all := fs.BoolP("all", "a", false, "Show all stacks")
	jsonFlag := fs.Bool("json", false, "Output as JSON")
	debug := fs.BoolP("debug", "d", false, "Show debug output")
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

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)
	currentBranch, _ := g.CurrentBranch()

	if *debug {
		remoteURL, err := g.GetRemote("origin")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[DEBUG] GetRemote error: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG] Remote URL: %s\n", remoteURL)
		}
	}

	mgr, err := stack.NewReadOnlyManager(cwd)
	if err != nil {
		return err
	}

	stacks := mgr.ListStacks()
	if len(stacks) == 0 {
		if *jsonFlag {
			fmt.Println("[]")
			return nil
		}
		ui.Info("No stacks found. Create one with: ezs new <branch-name>")
		return nil
	}

	var stacksToShow []*config.Stack
	currentStack, _, csErr := mgr.GetCurrentStack()
	if *all {
		stacksToShow = stacks
	} else if csErr != nil {
		// Not on a stacked branch — only show stacks rooted on the current branch
		stacksToShow = mgr.GetStacksWithRoot(currentBranch)
		if len(stacksToShow) == 0 {
			return ui.NewExitError(ui.ExitNotInStack, "current branch '%s' is not part of any stack. Use -a to show all stacks", currentBranch)
		}
	} else {
		stacksToShow = []*config.Stack{currentStack}
	}

	// Fetch diff stats in parallel (local git ops, fast)
	diffMaps := make([]map[string]*ui.BranchStatus, len(stacksToShow))
	for i, s := range stacksToShow {
		diffMaps[i] = fetchDiffStats(g, s)
	}

	if *jsonFlag {
		return printStacksJSON(stacksToShow, currentBranch, diffMaps)
	}

	for i, s := range stacksToShow {
		ui.PrintStack(s, currentBranch, false, diffMaps[i])
	}
	return nil
}

// stackJSON represents a stack in JSON output
type stackJSON struct {
	Hash         string       `json:"hash"`
	Name         string       `json:"name,omitempty"`
	Root         string       `json:"root"`
	RootBase     string       `json:"root_base,omitempty"`
	RootPRNumber int          `json:"root_pr_number,omitempty"`
	RootPRUrl    string       `json:"root_pr_url,omitempty"`
	RootAdds     int          `json:"root_additions,omitempty"`
	RootDels     int          `json:"root_deletions,omitempty"`
	Branches     []branchJSON `json:"branches"`
}

// branchJSON represents a branch in JSON output
type branchJSON struct {
	Name         string `json:"name"`
	Parent       string `json:"parent"`
	IsMerged     bool   `json:"is_merged"`
	IsCurrent    bool   `json:"is_current"`
	PRNumber     int    `json:"pr_number,omitempty"`
	PRUrl        string `json:"pr_url,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
}

// statusStackJSON represents a stack in JSON status output (with PR/CI info)
type statusStackJSON struct {
	Hash         string             `json:"hash"`
	Name         string             `json:"name,omitempty"`
	Root         string             `json:"root"`
	RootBase     string             `json:"root_base,omitempty"`
	RootPRNumber int                `json:"root_pr_number,omitempty"`
	RootPRUrl    string             `json:"root_pr_url,omitempty"`
	RootAdds     int                `json:"root_additions,omitempty"`
	RootDels     int                `json:"root_deletions,omitempty"`
	Branches     []statusBranchJSON `json:"branches"`
}

// statusBranchJSON extends branchJSON with PR and CI status fields.
// Additions/Deletions live on the embedded branchJSON — do not redeclare
// them here, or Go's JSON encoder will silently shadow the inner copy and
// any caller that sets only the inner field will see zeros in the output.
type statusBranchJSON struct {
	branchJSON
	PRState     string `json:"pr_state,omitempty"`
	CIState     string `json:"ci_state,omitempty"`
	CISummary   string `json:"ci_summary,omitempty"`
	Mergeable   string `json:"mergeable,omitempty"`
	ReviewState string `json:"review_state,omitempty"`
}

// printStacksJSON outputs stacks as JSON to stdout
func printStacksJSON(stacks []*config.Stack, currentBranch string, diffMaps []map[string]*ui.BranchStatus) error {
	result := make([]stackJSON, 0, len(stacks))
	for i, s := range stacks {
		sj := stackJSON{
			Hash:         s.Hash,
			Name:         s.Name,
			Root:         s.Root,
			RootBase:     s.RootBase,
			RootPRNumber: s.RootPRNumber,
			RootPRUrl:    s.RootPRUrl,
			Branches:     make([]branchJSON, 0, len(s.Branches)),
		}
		var dm map[string]*ui.BranchStatus
		if i < len(diffMaps) {
			dm = diffMaps[i]
		}
		if dm != nil {
			if rs, ok := dm[s.Root]; ok {
				sj.RootAdds = rs.Additions
				sj.RootDels = rs.Deletions
			}
		}
		for _, b := range s.Branches {
			bj := branchJSON{
				Name:         b.Name,
				Parent:       b.Parent,
				IsMerged:     b.IsMerged,
				IsCurrent:    b.Name == currentBranch,
				PRNumber:     b.PRNumber,
				PRUrl:        b.PRUrl,
				WorktreePath: b.WorktreePath,
			}
			if dm != nil {
				if bs, ok := dm[b.Name]; ok {
					bj.Additions = bs.Additions
					bj.Deletions = bs.Deletions
				}
			}
			sj.Branches = append(sj.Branches, bj)
		}
		result = append(result, sj)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// printStacksStatusJSON outputs stacks with PR/CI status as JSON to stdout
func printStacksStatusJSON(stacks []*config.Stack, currentBranch string, statusMaps []map[string]*ui.BranchStatus) error {
	result := make([]statusStackJSON, 0, len(stacks))
	for i, s := range stacks {
		sj := statusStackJSON{
			Hash:         s.Hash,
			Name:         s.Name,
			Root:         s.Root,
			RootBase:     s.RootBase,
			RootPRNumber: s.RootPRNumber,
			RootPRUrl:    s.RootPRUrl,
			Branches:     make([]statusBranchJSON, 0, len(s.Branches)),
		}
		var sm map[string]*ui.BranchStatus
		if i < len(statusMaps) {
			sm = statusMaps[i]
		}
		if sm != nil {
			if rs, ok := sm[s.Root]; ok {
				sj.RootAdds = rs.Additions
				sj.RootDels = rs.Deletions
			}
		}
		for _, b := range s.Branches {
			sbj := statusBranchJSON{
				branchJSON: branchJSON{
					Name:         b.Name,
					Parent:       b.Parent,
					IsMerged:     b.IsMerged,
					IsCurrent:    b.Name == currentBranch,
					PRNumber:     b.PRNumber,
					PRUrl:        b.PRUrl,
					WorktreePath: b.WorktreePath,
				},
			}
			if sm != nil {
				if bs, ok := sm[b.Name]; ok {
					sbj.PRState = bs.PRState
					sbj.CIState = bs.CIState
					sbj.CISummary = bs.CISummary
					sbj.Mergeable = bs.Mergeable
					sbj.ReviewState = bs.ReviewState
					sbj.branchJSON.Additions = bs.Additions
					sbj.branchJSON.Deletions = bs.Deletions
				}
			}
			sj.Branches = append(sj.Branches, sbj)
		}
		result = append(result, sj)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// parseWatchFlag pre-extracts --watch / --watch=N from args. It returns the
// watch interval in seconds (0 if no --watch flag was present, 5 as the
// default when --watch is present without a valid positive integer) plus the
// remaining args in their original order. Supports both "--watch",
// "--watch 10" (eats the next arg if it parses as a positive int), and
// "--watch=10" forms. Bogus values like "--watch=abc" fall back to 5.
func parseWatchFlag(args []string) (int, []string) {
	watchInterval := 0
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--watch" {
			watchInterval = 5
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil && n > 0 {
					watchInterval = n
					i++
				}
			}
			continue
		}
		if strings.HasPrefix(a, "--watch=") {
			if n, err := strconv.Atoi(strings.TrimPrefix(a, "--watch=")); err == nil && n > 0 {
				watchInterval = n
			} else {
				watchInterval = 5
			}
			continue
		}
		rest = append(rest, a)
	}
	return watchInterval, rest
}

// Status shows the status of current stack or all stacks with PR and CI info
func Status(args []string) error {
	if HasExamplesFlag("status", args) {
		return nil
	}
	// Pre-extract --watch (and optional integer interval) so the inner Status
	// invocation never sees it. Supports both "--watch" and "--watch=10".
	watchInterval, passthrough := parseWatchFlag(args)
	if watchInterval > 0 {
		// --watch is fundamentally interactive: it clears the screen and
		// redraws on a timer. --json is meant for machine consumption and
		// expects a single parseable document. Combining them produces
		// garbage on both ends, so reject up front.
		for _, a := range passthrough {
			if a == "--json" {
				return fmt.Errorf("--watch and --json cannot be combined")
			}
		}
		// Throttle: GitHub's gh API isn't free. 2s is already aggressive
		// for a stack with N branches × gh calls; anything lower is abuse.
		// Surface the throttle so users notice their requested interval was
		// overridden, rather than silently rewriting it.
		if watchInterval < 2 {
			ui.Warn(fmt.Sprintf("--watch interval %ds is below the 2s minimum; using 2s", watchInterval))
			watchInterval = 2
		}
		return runStatusWatch(passthrough, watchInterval)
	}
	args = passthrough

	fs := pflag.NewFlagSet("status", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sShow status of current stack with PR and CI info%s

%sUSAGE%s
    ezs status [options]

%sDESCRIPTION%s
    Shows the current stack with PR and CI status for each branch.
    When not in a stack, shows a message. Use -a to see all stacks.

%sOPTIONS%s
    -a, --all              Show all stacks
    -b, --branch <name>    Show status for a specific branch's stack
    -d, --debug            Show debug output
    --json                 Output as JSON (machine-readable)
    --watch [seconds]      Auto-refresh every N seconds (default 5)
    -h, --help             Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	helpFlag := fs.BoolP("help", "h", false, "Show help")
	all := fs.BoolP("all", "a", false, "Show all stacks")
	branchFlag := fs.StringP("branch", "b", "", "Show status for a specific branch's stack")
	debug := fs.BoolP("debug", "d", false, "Show debug output")
	jsonFlag := fs.Bool("json", false, "Output as JSON")
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

	// Check gh auth in background while we load config
	ghAvailableCh := make(chan bool, 1)
	go func() {
		ghAvailableCh <- github.CheckAuth() == nil
	}()

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)
	currentBranch, err := g.CurrentBranch()
	if err != nil {
		return err
	}

	mgr, err := stack.NewReadOnlyManager(cwd)
	if err != nil {
		return err
	}

	stacks := mgr.ListStacks()
	if len(stacks) == 0 {
		if *jsonFlag {
			fmt.Fprintln(os.Stdout, "[]")
			return nil
		}
		ui.Info("No stacks found. Create one with: ezs new <branch-name>")
		return nil
	}

	ghAvailable := <-ghAvailableCh

	// fetchStatusMaps fetches PR/CI status for a list of stacks in parallel
	fetchStatusMaps := func(targetStacks []*config.Stack) []map[string]*ui.BranchStatus {
		statusMaps := make([]map[string]*ui.BranchStatus, len(targetStacks))
		if !ghAvailable {
			return statusMaps
		}
		if !*jsonFlag {
			spinner := ui.NewDelayedSpinner("Fetching PR and CI status...")
			spinner.Start()
			defer spinner.Stop()
		}
		var wg sync.WaitGroup
		for i, s := range targetStacks {
			wg.Add(1)
			go func(idx int, stack *config.Stack) {
				defer wg.Done()
				statusMaps[idx] = fetchBranchStatuses(g, stack, *debug)
			}(i, s)
		}
		wg.Wait()
		return statusMaps
	}

	// printOrJSON prints stacks to terminal or outputs JSON based on --json flag
	printOrJSON := func(targetStacks []*config.Stack, statusMaps []map[string]*ui.BranchStatus) error {
		if *jsonFlag {
			return printStacksStatusJSON(targetStacks, currentBranch, statusMaps)
		}
		for i, s := range targetStacks {
			var sm map[string]*ui.BranchStatus
			if i < len(statusMaps) {
				sm = statusMaps[i]
			}
			ui.PrintStack(s, currentBranch, ghAvailable, sm)
		}
		if !ghAvailable {
			ui.Warn("GitHub CLI not authenticated. Run 'gh auth login' for PR/CI status.")
		}
		return nil
	}

	// Handle --branch flag: show status for a specific branch's stack
	if *branchFlag != "" {
		targetStack := mgr.FindStackForBranch(*branchFlag)
		if targetStack == nil {
			if *jsonFlag {
				fmt.Fprintln(os.Stdout, "[]")
				return nil
			}
			return fmt.Errorf("branch %q not found in any stack", *branchFlag)
		}
		statusMaps := fetchStatusMaps([]*config.Stack{targetStack})
		return printOrJSON([]*config.Stack{targetStack}, statusMaps)
	}

	currentStack, branch, err := mgr.GetCurrentStack()
	if *all {
		statusMaps := fetchStatusMaps(stacks)
		if err := printOrJSON(stacks, statusMaps); err != nil {
			return err
		}
		if !*jsonFlag && ghAvailable {
			offerFullyMergedStackCleanup(mgr, stacks)
		}
		return nil
	}
	if err != nil {
		// Not on a stacked branch — only show stacks rooted on the current branch
		rootStacks := mgr.GetStacksWithRoot(currentBranch)
		if len(rootStacks) == 0 {
			if *jsonFlag {
				fmt.Fprintln(os.Stdout, "[]")
				return nil
			}
			return ui.NewExitError(ui.ExitNotInStack, "current branch '%s' is not part of any stack. Use -a to show all stacks", currentBranch)
		}
		statusMaps := fetchStatusMaps(rootStacks)
		if err := printOrJSON(rootStacks, statusMaps); err != nil {
			return err
		}
		if !*jsonFlag && ghAvailable {
			offerFullyMergedStackCleanup(mgr, rootStacks)
		}
		return nil
	}

	statusMaps := fetchStatusMaps([]*config.Stack{currentStack})
	if err := printOrJSON([]*config.Stack{currentStack}, statusMaps); err != nil {
		return err
	}

	// In JSON mode, skip interactive output and return
	if *jsonFlag {
		return nil
	}

	parentRef := branch.Parent
	if g.RemoteBranchExists(branch.Parent) {
		parentRef = "origin/" + branch.Parent
	}
	commits, err := g.GetCommitsBetween(parentRef, currentBranch)
	if err == nil && len(commits) > 0 {
		fmt.Fprintf(os.Stderr, "%s%sCommits in this branch:%s\n", ui.Bold, ui.Cyan, ui.Reset)
		for _, c := range commits {
			fmt.Fprintf(os.Stderr, "  %s%.7s%s %s\n", ui.Yellow, c.Hash, ui.Reset, c.Subject)
		}
		fmt.Fprintln(os.Stderr)
	}

	inRebase, _ := g.IsRebaseInProgress()
	if inRebase {
		ui.Warn("Rebase in progress! Resolve conflicts and run: git rebase --continue")
	}

	if ghAvailable {
		offerFullyMergedStackCleanup(mgr, []*config.Stack{currentStack})
	}

	if !ghAvailable {
		ui.Warn("GitHub CLI not authenticated. Run 'gh auth login' for PR/CI status.")
	}

	return nil
}

// runStatusWatch repeatedly clears the screen and runs status until the user
// interrupts with SIGINT/SIGTERM. The wrapped invocation must not include --watch.
// We use signal.NotifyContext so Ctrl-C breaks the sleep cleanly between refreshes
// instead of killing the process mid-render or mid-gh-request.
func runStatusWatch(passthrough []string, intervalSec int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	interval := time.Duration(intervalSec) * time.Second
	for {
		fmt.Fprint(os.Stderr, "\033[2J\033[H")
		fmt.Fprintf(os.Stderr, "%sezs status --watch (every %ds, Ctrl-C to exit)%s\n\n", ui.Bold, intervalSec, ui.Reset)
		if err := Status(passthrough); err != nil {
			ui.Warn(err.Error())
		}
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr)
			return nil
		case <-time.After(interval):
		}
	}
}

// offerFullyMergedStackCleanup checks each stack whose branches are all marked merged
// (updated in-memory by fetchBranchStatuses) and offers to delete them.
// A full (non-read-only) Manager is created on demand for mutations so that
// reconciliation runs before any config changes.
func offerFullyMergedStackCleanup(mgr *stack.Manager, stacks []*config.Stack) {
	var mutMgr *stack.Manager // lazily created when a mutation is needed

	getMutMgr := func() *stack.Manager {
		if mutMgr != nil {
			return mutMgr
		}
		cwd, err := os.Getwd()
		if err != nil {
			return nil
		}
		m, err := stack.NewManager(cwd)
		if err != nil {
			return nil
		}
		mutMgr = m
		return mutMgr
	}

	for _, s := range stacks {
		if len(s.Branches) == 0 || s.DeleteDeclined {
			continue
		}
		allMerged := true
		for _, b := range s.Branches {
			if !b.IsMerged {
				allMerged = false
				break
			}
		}
		if !allMerged {
			continue
		}
		fmt.Fprintln(os.Stderr)
		ui.Info(fmt.Sprintf("Stack '%s' is fully merged", s.DisplayName()))
		if ui.ConfirmTUI(fmt.Sprintf("Clean up stack '%s' (delete worktrees, branches, and tracking)?", s.DisplayName())) {
			m := getMutMgr()
			if m == nil {
				ui.Warn(fmt.Sprintf("Failed to clean up stack '%s': could not create manager", s.DisplayName()))
				continue
			}
			needsCd, err := m.DeleteStack(s.Hash)
			if err != nil {
				ui.Warn(fmt.Sprintf("Failed to clean up stack '%s': %v", s.DisplayName(), err))
			} else {
				ui.Success(fmt.Sprintf("Removed fully merged stack '%s'", s.DisplayName()))
				if needsCd {
					EmitCd(m.GetRepoDir())
				}
			}
		} else {
			m := getMutMgr()
			if m != nil {
				m.DeclineStackDelete(s.Hash)
			}
		}
	}
}
