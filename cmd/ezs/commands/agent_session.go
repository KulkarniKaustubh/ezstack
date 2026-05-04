package commands

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/google/uuid"
)

// Agent session tracking.
//
// `ezs agent` lets the user resume a previous AI agent session bound to a
// branch (branch-scoped) or to the entire stack (stack-scoped). On the first
// launch we generate a UUID, persist it (on the Branch or Stack), and pass it
// to the agent CLI so the agent records its session under that ID. On
// subsequent launches we resume the session by ID instead of starting fresh.
//
// Resume is currently implemented for the Claude CLI family because that's
// the configured default and the only agent we can reliably drive. For other
// agents we surface the session ID via the EZS_AGENT_SESSION_ID environment
// variable so user-supplied wrappers can opt in.

// agentCLIBase returns the lowercase basename of an agent CLI command,
// stripped of any extension. Used to detect known agent families.
//
// Examples:
//
//	"claude"                           -> "claude"
//	"/usr/local/bin/claude"            -> "claude"
//	"claude-code --foo"                -> "claude-code"  // parsed as base of fields[0]
//	"C:\\bin\\claude.exe"              -> "claude"
func agentCLIBase(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	base := filepath.Base(fields[0])
	// Drop a single trailing extension (`.exe` on Windows, `.cmd`, etc.).
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	return strings.ToLower(base)
}

// isClaudeFamily reports whether the configured agent CLI is claude-cli (or
// a wrapper named "claude*"). Only claude-family CLIs accept --session-id and
// --resume in the form ezstack uses.
func isClaudeFamily(agentCmd string) bool {
	base := agentCLIBase(agentCmd)
	if base == "" {
		return false
	}
	// Match "claude" or "claude-*". Anything else is treated as an unknown
	// agent and gets no session resumption (we don't want to inject flags
	// into a CLI that may interpret them differently).
	return base == "claude" || strings.HasPrefix(base, "claude-") || strings.HasPrefix(base, "claude_")
}

// newSessionID returns a fresh UUID v4 string suitable for claude --session-id.
// Variable so tests can replace it with a deterministic generator.
var newSessionID = func() string { return uuid.NewString() }

// agentSessionScope describes the persistence target for a session ID.
type agentSessionScope int

const (
	// scopeStack: session is bound to the whole stack (default for `ezs agent`).
	scopeStack agentSessionScope = iota
	// scopeBranch: session is bound to a single branch (`ezs agent --branch`).
	scopeBranch
)

// sessionDisplayName builds the "_ezstack-<identifier>" name we hand to
// `claude --name`. The name is shown in claude's /resume picker and terminal
// title, so it should be readable and short. We sanitize it because claude
// trims whitespace but does not impose other limits — nonetheless we keep
// the identifier filesystem-safe so it round-trips through any tooling that
// uses the name as a file/component.
//
// Examples:
//
//	sessionDisplayName("feature-auth", scopeBranch) -> "_ezstack-feature-auth"
//	sessionDisplayName("my stack", scopeStack)      -> "_ezstack-my-stack"
//	sessionDisplayName("8b7a542b", scopeStack)      -> "_ezstack-8b7a542b"
func sessionDisplayName(identifier string, _ agentSessionScope) string {
	return "_ezstack-" + sanitizeSessionLabel(identifier)
}

// sanitizeSessionLabel collapses characters that are awkward in display names
// into hyphens. We deliberately keep this conservative: ASCII alnum, dot,
// underscore, hyphen pass through; everything else collapses.
func sanitizeSessionLabel(s string) string {
	if s == "" {
		return "session"
	}
	var b strings.Builder
	b.Grow(len(s))
	prevHyphen := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "session"
	}
	return out
}

// claudeSessionInjection describes how a claude-family agent should be
// invoked given the persisted session state.
//
// When resuming an existing session, the prompt is NOT included in the argv —
// claude opens the prior conversation interactively and the user types into
// it. When starting a fresh session, the prompt is appended last (existing
// behavior) so claude lands the user mid-conversation with full context.
type claudeSessionInjection struct {
	// Args is the prefix of args inserted before any user-supplied extras and
	// (for fresh sessions) the prompt. Always non-empty when applicable.
	Args []string
	// IncludePrompt indicates whether the caller should append the rendered
	// prompt as the final argv. False when resuming.
	IncludePrompt bool
	// SessionID is the UUID we either reused or freshly generated. Stored back
	// on the branch/stack by the caller after a successful spawn.
	SessionID string
	// Fresh is true if a brand-new session ID was minted (vs reusing one).
	Fresh bool
}

// buildClaudeSessionArgs computes the session-related argv to inject for a
// claude-family agent.
//
//   - storedID is the previously-persisted session ID (empty if none).
//   - displayName is the human-readable label (e.g. "_ezstack-my-stack").
//   - forceFresh, when true, ignores storedID and mints a new UUID — used by
//     the --no-resume flag.
//
// The returned IncludePrompt tells the caller whether to append the prompt
// to the command line. SessionID is what the caller should persist.
func buildClaudeSessionArgs(storedID, displayName string, forceFresh bool) claudeSessionInjection {
	if storedID != "" && !forceFresh {
		// Resume mode: claude --resume <id> --name <label>
		return claudeSessionInjection{
			Args:          []string{"--resume", storedID, "--name", displayName},
			IncludePrompt: false,
			SessionID:     storedID,
			Fresh:         false,
		}
	}
	id := newSessionID()
	return claudeSessionInjection{
		Args:          []string{"--session-id", id, "--name", displayName},
		IncludePrompt: true,
		SessionID:     id,
		Fresh:         true,
	}
}

// splitAgentExtras splits a command-line argv slice on the standalone "--"
// separator: tokens to its left are returned as agentArgs (parsed by ezs's
// own flag set); tokens to its right are returned as extras (passed through
// to the agent CLI verbatim).
//
// If "--" does not appear, all args are considered ezs args and extras is nil.
func splitAgentExtras(args []string) (agentArgs, extras []string) {
	for i, a := range args {
		if a == "--" {
			out := append([]string{}, args[:i]...)
			rest := append([]string{}, args[i+1:]...)
			return out, rest
		}
	}
	return args, nil
}

// agentSessionPlan is the resolved session-tracking plan for one `ezs agent`
// invocation. The injection (if non-nil) is what the spawn layer pastes into
// argv. The persist callback is invoked after the agent process exits so the
// session ID is written even when the agent crashes — claude records the
// conversation as soon as it starts, so resuming next time is still useful.
type agentSessionPlan struct {
	injection *claudeSessionInjection
	persist   func(string) error // no-op for non-claude or scope-less feature mode
}

// resolveWorkSession builds an agentSessionPlan for `ezs agent` work mode.
// For claude-family agents:
//   - branchScoped: bind to the branch (cache.AgentSessionID)
//   - otherwise: bind to the stack (Stack.AgentSessionID)
//
// For non-claude agents, returns a no-op plan: ezs has no way to drive
// resume in tools whose flag schema we don't know. The user can still use
// `--` to pass their own resume flag through.
func resolveWorkSession(repoPath, agentCmd string, targetStack *config.Stack, branchName string, branchScoped, forceFresh bool) *agentSessionPlan {
	if !isClaudeFamily(agentCmd) {
		return nil
	}
	if branchScoped {
		var stored string
		if targetStack != nil {
			for _, b := range targetStack.Branches {
				if b.Name == branchName {
					stored = lookupBranchSessionID(repoPath, b.Name)
					break
				}
			}
		}
		label := sessionDisplayName(branchName, scopeBranch)
		inj := buildClaudeSessionArgs(stored, label, forceFresh)
		return &agentSessionPlan{
			injection: &inj,
			persist: func(id string) error {
				return persistBranchSessionID(repoPath, branchName, id)
			},
		}
	}

	// Stack-scoped.
	stored := ""
	identifier := ""
	stackHash := ""
	if targetStack != nil {
		stored = targetStack.AgentSessionID
		stackHash = targetStack.Hash
		// Prefer the stack name when set — it's what the user typed and
		// what they'll recognize in the claude /resume picker.
		identifier = targetStack.Name
		if identifier == "" {
			identifier = targetStack.Hash
		}
	}
	label := sessionDisplayName(identifier, scopeStack)
	inj := buildClaudeSessionArgs(stored, label, forceFresh)
	return &agentSessionPlan{
		injection: &inj,
		persist: func(id string) error {
			if stackHash == "" {
				return nil
			}
			return persistStackSessionID(repoPath, stackHash, id)
		},
	}
}

// resolveFeatureSession builds a plan for feature-builder mode. When an
// existing stack is provided we bind the session to it; without a stack the
// feature run is one-shot and we still mint a session ID (so claude has a
// stable handle) but nothing is persisted on the ezs side.
func resolveFeatureSession(repoPath, agentCmd string, existingStack *config.Stack, forceFresh bool) *agentSessionPlan {
	if !isClaudeFamily(agentCmd) {
		return nil
	}
	if existingStack == nil {
		// One-shot session: fresh UUID, claude --name "_ezstack-feature".
		// No persist target.
		label := sessionDisplayName("feature", scopeStack)
		inj := buildClaudeSessionArgs("", label, true /*forceFresh — nothing to resume*/)
		return &agentSessionPlan{injection: &inj, persist: func(string) error { return nil }}
	}
	identifier := existingStack.Name
	if identifier == "" {
		identifier = existingStack.Hash
	}
	label := sessionDisplayName("feature-"+identifier, scopeStack)
	inj := buildClaudeSessionArgs(existingStack.AgentSessionID, label, forceFresh)
	hash := existingStack.Hash
	return &agentSessionPlan{
		injection: &inj,
		persist: func(id string) error {
			return persistStackSessionID(repoPath, hash, id)
		},
	}
}

// lookupBranchSessionID reads the cached agent session ID for a branch.
// Returns empty string if the cache is missing or the branch isn't tracked.
func lookupBranchSessionID(repoPath, branchName string) string {
	cache, err := config.LoadCacheConfig(repoPath)
	if err != nil {
		return ""
	}
	bc := cache.GetBranchCache(branchName)
	if bc == nil {
		return ""
	}
	return bc.AgentSessionID
}

// persistBranchSessionID writes the session ID to the branch cache.
func persistBranchSessionID(repoPath, branchName, sessionID string) error {
	mgr, err := stack.NewReadOnlyManager(repoPath)
	if err != nil {
		return err
	}
	return mgr.SetBranchAgentSessionID(branchName, sessionID)
}

// persistStackSessionID writes the session ID onto the stack with the given hash.
func persistStackSessionID(repoPath, stackHash, sessionID string) error {
	mgr, err := stack.NewReadOnlyManager(repoPath)
	if err != nil {
		return err
	}
	return mgr.SetStackAgentSessionID(stackHash, sessionID)
}

// sessionLogSuffix returns a short " (resumed: <id>)" or " (fresh session: <id>)"
// fragment for ui.Info log lines. Empty string when no plan is in effect (so
// non-claude logs read identically to before).
func sessionLogSuffix(plan *agentSessionPlan) string {
	if plan == nil || plan.injection == nil || plan.injection.SessionID == "" {
		return ""
	}
	short := plan.injection.SessionID
	if len(short) > 8 {
		short = short[:8]
	}
	if plan.injection.Fresh {
		return " (new session " + short + ")"
	}
	return " (resuming " + short + ")"
}

// printSessionDryRun prints a summary line under the dry-run prompt explaining
// what session args would have been injected.
func printSessionDryRun(plan *agentSessionPlan) {
	if plan == nil || plan.injection == nil {
		fmt.Println("\n── Session: not tracked (non-claude agent or feature one-shot) ──")
		return
	}
	mode := "resume"
	if plan.injection.Fresh {
		mode = "fresh"
	}
	fmt.Printf("\n── Session: %s, id=%s ──\n", mode, plan.injection.SessionID)
	fmt.Printf("── Injected args: %s ──\n", strings.Join(plan.injection.Args, " "))
}
