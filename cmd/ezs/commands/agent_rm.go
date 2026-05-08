package commands

import (
	"fmt"
	"os"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

// agentRm implements `ezs agent rm` (alias `ezs agent remove`).
//
// Clears the persisted AI session binding for a stack or branch, so the next
// `ezs agent` invocation in that scope mints a fresh UUID instead of
// resuming. The agent's underlying conversation history (claude's session
// journal, etc.) is NOT touched — `rm` only forgets ezs's pointer to it.
//
// Filters mirror `ezs agent ls`:
//
//   - --branch    forget the session bound to the user's current branch
//   - --stack     forget the session bound to the user's current stack
//   - --all       forget every session in the current repo (stack + branch)
//
// Filters are mutually exclusive; one is required. The bare `ezs agent rm`
// errors out with guidance — silently picking a default would risk wiping
// the wrong session, and the typing cost of one extra flag is trivial.
//
// A confirmation prompt protects --all (the only non-recoverable shape since
// it touches multiple slots in one call). Single-target rm is one slot and
// easily re-bound by re-launching the agent, so we don't gate it.
// `ui.YesMode` (set by -y / MCP) bypasses both prompts as usual.
func agentRm(args []string) error {
	fs := pflag.NewFlagSet("agent rm", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sForget a tracked AI session binding%s

%sUSAGE%s
    ezs agent rm <filter>
    ezs agent remove <filter>   (alias)

%sFILTERS%s (mutually exclusive — one is required)
    -b, --branch     Forget the session bound to the current branch
    -s, --stack      Forget the session bound to the current stack
    --all            Forget every session in this repo (stack + branch)

%sOPTIONS%s
    -h, --help       Show this help message

%sDESCRIPTION%s
    Clears ezs's stored pointer to the AI session for the chosen scope. The
    next 'ezs agent' invocation against that scope will mint a fresh UUID
    instead of resuming.

    The agent's own session journal (e.g. claude's transcript) is NOT
    deleted — only ezs forgets the pointer. To resume the prior session
    manually after running rm, pass the agent's resume flag explicitly
    (e.g. 'ezs agent -- --resume <uuid>'); 'ezs agent ls' is the canonical
    place to find UUIDs before clearing them.

    --all asks for confirmation since it touches multiple bindings at once.
    -y / YesMode bypasses the prompt.
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	branchFilter := fs.BoolP("branch", "b", false, "Forget the session for the current branch")
	stackFilter := fs.BoolP("stack", "s", false, "Forget the session for the current stack")
	allFilter := fs.Bool("all", false, "Forget every session in this repo")
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
		return fmt.Errorf("ezs agent rm takes no positional arguments (got %q)", fs.Arg(0))
	}

	picked := 0
	names := []string{}
	if *branchFilter {
		picked++
		names = append(names, "--branch")
	}
	if *stackFilter {
		picked++
		names = append(names, "--stack")
	}
	if *allFilter {
		picked++
		names = append(names, "--all")
	}
	switch picked {
	case 0:
		return fmt.Errorf("ezs agent rm requires one of --branch, --stack, --all\nFor help: ezs agent rm --help")
	case 1:
		// fall through
	default:
		return fmt.Errorf("filters %v are mutually exclusive — pick one", names)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	g := git.New(cwd)

	mgr, err := stack.NewManager(cwd)
	if err != nil {
		return err
	}

	switch {
	case *branchFilter:
		return agentRmBranchScope(mgr, g)
	case *stackFilter:
		return agentRmStackScope(mgr)
	case *allFilter:
		return agentRmAllScope(mgr)
	}
	return nil // unreachable
}

// agentRmBranchScope clears the session bound to the current branch. We hard
// fail when the branch isn't tracked or has no session — silent no-ops here
// would leave the user wondering "did it work?" the same way a silent ls
// would.
func agentRmBranchScope(mgr *stack.Manager, g *git.Git) error {
	branch, err := g.CurrentBranch()
	if err != nil || branch == "" || branch == "HEAD" {
		return fmt.Errorf("--branch needs a tracked current branch; HEAD is %q", branch)
	}
	if mgr.GetBranch(branch) == nil {
		return fmt.Errorf("--branch: current branch %q is not tracked by ezstack", branch)
	}
	stored := lookupBranchSessionID(mgr.GetRepoDir(), branch)
	if stored == "" {
		return fmt.Errorf("no session is bound to branch %q", branch)
	}
	if err := mgr.SetBranchAgentSessionID(branch, "", ""); err != nil {
		return fmt.Errorf("failed to clear branch session: %w", err)
	}
	ui.Success(fmt.Sprintf("Forgot session %s on branch %s", shortAgentSessionID(stored), branch))
	return nil
}

// agentRmStackScope clears the session bound to the current stack. Same
// hard-fail policy as the branch variant.
func agentRmStackScope(mgr *stack.Manager) error {
	s, _, err := mgr.GetCurrentStack()
	if err != nil || s == nil {
		return fmt.Errorf("--stack needs a current stack; %v", err)
	}
	if s.AgentSessionID == "" {
		return fmt.Errorf("no session is bound to stack %s", s.DisplayName())
	}
	stored := s.AgentSessionID
	if err := mgr.SetStackAgentSessionID(s.Hash, "", ""); err != nil {
		return fmt.Errorf("failed to clear stack session: %w", err)
	}
	ui.Success(fmt.Sprintf("Forgot session %s on stack %s", shortAgentSessionID(stored), s.DisplayName()))
	return nil
}

// agentRmAllScope clears every session binding in the current repo (stack
// and branch). Confirmation gate runs unless ui.YesMode is set; the count
// is included in the prompt so the user knows the blast radius before
// agreeing.
func agentRmAllScope(mgr *stack.Manager) error {
	sc := mgr.GetStackConfig()
	if sc == nil {
		return fmt.Errorf("no stack config loaded for this repo")
	}

	// Snapshot the bindings we'd clear so we can both report a count up
	// front and skip the save when there's nothing to do (write-skip
	// avoids touching the config file's mtime for a no-op).
	stackTargets := []string{}
	for h, s := range sc.Stacks {
		if s != nil && s.AgentSessionID != "" {
			stackTargets = append(stackTargets, h)
		}
	}
	branchTargets := []string{}
	if sc.Cache != nil {
		for name, bc := range sc.Cache.Branches {
			if bc != nil && bc.AgentSessionID != "" {
				branchTargets = append(branchTargets, name)
			}
		}
	}
	total := len(stackTargets) + len(branchTargets)
	if total == 0 {
		ui.Info("No sessions to forget in this repo.")
		return nil
	}

	if !ui.ConfirmTUIWithDefault(
		fmt.Sprintf("Forget %d AI session binding(s) in this repo?", total), false,
	) {
		ui.Warn("Cancelled")
		return nil
	}

	for _, h := range stackTargets {
		if err := mgr.SetStackAgentSessionID(h, "", ""); err != nil {
			return fmt.Errorf("failed to clear stack %s: %w", h, err)
		}
	}
	for _, name := range branchTargets {
		if err := mgr.SetBranchAgentSessionID(name, "", ""); err != nil {
			return fmt.Errorf("failed to clear branch %s: %w", name, err)
		}
	}

	ui.Success(fmt.Sprintf("Forgot %d session binding(s) (%d stack, %d branch)",
		total, len(stackTargets), len(branchTargets)))
	return nil
}
