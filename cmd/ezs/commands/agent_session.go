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
// launch we generate a UUID, persist it (on the Branch or Stack), and expose
// it to the agent CLI so the agent records its session under that ID. On
// subsequent launches we surface the same ID so the agent can resume.
//
// CLI-flag injection is only performed for Claude-family CLIs (where we know
// the flag schema: `--session-id`, `--resume`, `--name`). For agents whose
// argv we don't understand, we still mint and persist the UUID and expose it
// via the EZS_AGENT_SESSION_ID environment variable — user-supplied wrappers
// can read that and decide how to use it. This matches what `agent ls`
// surfaces and keeps the persistence story uniform across agents.

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

// preferLiveAgentName picks the label we hand to `claude --name` on a
// resume. When the user has run `/rename` (or otherwise renamed the session
// inside Claude), the latest `agent-name` event in the session journal
// reflects what they currently see in their resume picker — re-asserting
// the deterministic ezstack label would silently overwrite their rename
// the next time they run `ezs agent`.
//
// Returns the live name when:
//   - storedID is non-empty (we have a session to resume against)
//   - forceFresh is false (the user didn't pass --no-resume)
//   - the JSONL contains an `agent-name` event for storedID
//
// Otherwise returns fallback (the deterministic `_ezstack-<...>` label),
// preserving original behavior for fresh launches and non-claude agents.
func preferLiveAgentName(storedID string, forceFresh bool, fallback string) string {
	if storedID == "" || forceFresh {
		return fallback
	}
	if name := claudeAgentName(storedID); name != "" {
		return name
	}
	return fallback
}

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

// agentSessionInjection describes how an agent should be invoked given the
// persisted session state.
//
// For claude-family agents we know `--session-id` / `--resume` / `--name`
// flags, so we inject them into argv and (when resuming) skip appending the
// prompt — claude reopens the prior conversation interactively. For agents
// whose argv we don't understand, Args is nil; the session ID is exposed
// only through EZS_AGENT_SESSION_ID and the prompt is always appended.
type agentSessionInjection struct {
	// Args is the prefix of args inserted before any user-supplied extras and
	// (for fresh / non-resuming sessions) the prompt. Empty for non-claude
	// agents — those see the session via the env var instead.
	Args []string
	// IncludePrompt indicates whether the caller should append the rendered
	// prompt as the final argv. False only for claude-family resume; true
	// otherwise (fresh claude or any non-claude invocation).
	IncludePrompt bool
	// SessionID is the UUID we either reused or freshly generated. Stored back
	// on the branch/stack by the caller after a successful spawn.
	SessionID string
	// Fresh is true if a brand-new session ID was minted (vs reusing one).
	Fresh bool
}

// buildAgentSessionArgs computes the session-related argv (if any) to inject
// for the configured agent CLI, plus the session ID to persist and expose to
// the spawned process via EZS_AGENT_SESSION_ID.
//
//   - agentCmd is the configured agent command (used to decide whether ezs
//     can drive the agent's resume semantics directly via flags).
//   - storedID is the previously-persisted session ID (empty if none).
//   - displayName is the human-readable label (e.g. "_ezstack-my-stack").
//   - forceFresh, when true, ignores storedID and mints a new UUID — used by
//     the --no-resume flag.
//
// The returned IncludePrompt tells the caller whether to append the prompt
// to the command line. SessionID is what the caller should persist.
//
// For claude-family agents the flags are injected into argv. For other
// agents Args is empty (we don't risk misparsing flags whose schema we
// don't know); the session ID still flows through via the env var.
func buildAgentSessionArgs(agentCmd, storedID, displayName string, forceFresh bool) agentSessionInjection {
	isClaude := isClaudeFamily(agentCmd)
	resuming := storedID != "" && !forceFresh

	if resuming {
		if isClaude {
			// Resume mode: claude --resume <id> --name <label>; prompt omitted
			// because claude reopens the prior conversation interactively.
			return agentSessionInjection{
				Args:          []string{"--resume", storedID, "--name", displayName},
				IncludePrompt: false,
				SessionID:     storedID,
				Fresh:         false,
			}
		}
		// Non-claude resume: no CLI args (we don't know the agent's resume
		// flag), session ID exposed via env var, prompt still appended so
		// the wrapper has something to start from if it doesn't reload state.
		return agentSessionInjection{
			Args:          nil,
			IncludePrompt: true,
			SessionID:     storedID,
			Fresh:         false,
		}
	}

	id := newSessionID()
	if isClaude {
		return agentSessionInjection{
			Args:          []string{"--session-id", id, "--name", displayName},
			IncludePrompt: true,
			SessionID:     id,
			Fresh:         true,
		}
	}
	return agentSessionInjection{
		Args:          nil,
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
// argv and exposes via EZS_AGENT_SESSION_ID. The persist callback is invoked
// after the agent process exits so the session ID is written even when the
// agent crashes — claude records the conversation as soon as it starts, so
// resuming next time is still useful, and any non-claude wrapper that bound
// internal state to the env-exposed UUID can recover too.
//
// mode is the launch mode (config.AgentSessionWorkMode or
// config.AgentSessionFeatureMode); the persist callback writes it alongside
// the UUID so `ezs agent ls --feature` can filter rows. mode is set at plan
// resolution time so it's known before the agent process even starts.
type agentSessionPlan struct {
	injection *agentSessionInjection
	persist   func(string) error // no-op for scope-less feature mode
}

// resolveWorkSession builds an agentSessionPlan for `ezs agent` work mode.
//
//   - branchScoped: bind to the branch (cache.AgentSessionID)
//   - otherwise: bind to the stack (Stack.AgentSessionID)
//
// Plans are produced for any agent (not just claude). Claude-family agents
// get CLI flag injection; others get only the EZS_AGENT_SESSION_ID env var
// (the persisted UUID is what `agent ls` surfaces and what user-supplied
// wrappers can opt into).
//
// Persisted entries are tagged config.AgentSessionWorkMode so `agent ls
// --feature` can exclude them.
func resolveWorkSession(repoPath, agentCmd string, targetStack *config.Stack, branchName string, branchScoped, forceFresh bool) *agentSessionPlan {
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
		label := preferLiveAgentName(stored, forceFresh, sessionDisplayName(branchName, scopeBranch))
		inj := buildAgentSessionArgs(agentCmd, stored, label, forceFresh)
		return &agentSessionPlan{
			injection: &inj,
			persist: func(id string) error {
				return persistBranchSessionID(repoPath, branchName, id, config.AgentSessionWorkMode)
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
	label := preferLiveAgentName(stored, forceFresh, sessionDisplayName(identifier, scopeStack))
	inj := buildAgentSessionArgs(agentCmd, stored, label, forceFresh)
	return &agentSessionPlan{
		injection: &inj,
		persist: func(id string) error {
			if stackHash == "" {
				return nil
			}
			return persistStackSessionID(repoPath, stackHash, id, config.AgentSessionWorkMode)
		},
	}
}

// resolveFeatureSession builds a plan for feature-builder mode. When an
// existing stack is provided we bind the session to it; without a stack the
// feature run is one-shot — we still mint a session ID (so the agent has a
// stable handle exposed via EZS_AGENT_SESSION_ID) but nothing is persisted
// on the ezs side because there's no scope to attach to.
//
// Persisted entries are tagged config.AgentSessionFeatureMode so `agent ls
// --feature` includes them. Note that work-mode and feature-mode share the
// same Stack.AgentSessionID storage slot — running the other mode against
// the same stack overwrites both the UUID and the mode tag, which means
// "the most recent launch wins" for resume semantics.
//
// Plans are produced for any agent. Claude-family agents get CLI flag
// injection; others get the env var only.
func resolveFeatureSession(repoPath, agentCmd string, existingStack *config.Stack, forceFresh bool) *agentSessionPlan {
	if existingStack == nil {
		// One-shot session: fresh UUID. forceFresh=true because there's no
		// stored ID to resume from in this mode regardless of caller intent.
		label := sessionDisplayName("feature", scopeStack)
		inj := buildAgentSessionArgs(agentCmd, "", label, true)
		return &agentSessionPlan{injection: &inj, persist: func(string) error { return nil }}
	}
	identifier := existingStack.Name
	if identifier == "" {
		identifier = existingStack.Hash
	}
	label := preferLiveAgentName(existingStack.AgentSessionID, forceFresh, sessionDisplayName("feature-"+identifier, scopeStack))
	inj := buildAgentSessionArgs(agentCmd, existingStack.AgentSessionID, label, forceFresh)
	hash := existingStack.Hash
	return &agentSessionPlan{
		injection: &inj,
		persist: func(id string) error {
			return persistStackSessionID(repoPath, hash, id, config.AgentSessionFeatureMode)
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

// persistBranchSessionID writes the session ID + mode to the branch cache.
func persistBranchSessionID(repoPath, branchName, sessionID, mode string) error {
	mgr, err := stack.NewReadOnlyManager(repoPath)
	if err != nil {
		return err
	}
	return mgr.SetBranchAgentSessionID(branchName, sessionID, mode)
}

// persistStackSessionID writes the session ID + mode onto the stack with the given hash.
func persistStackSessionID(repoPath, stackHash, sessionID, mode string) error {
	mgr, err := stack.NewReadOnlyManager(repoPath)
	if err != nil {
		return err
	}
	return mgr.SetStackAgentSessionID(stackHash, sessionID, mode)
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
// what session args would have been injected and how the session ID would
// have been exposed to the agent process.
func printSessionDryRun(plan *agentSessionPlan) {
	if plan == nil || plan.injection == nil {
		fmt.Println("\n── Session: not tracked ──")
		return
	}
	mode := "resume"
	if plan.injection.Fresh {
		mode = "fresh"
	}
	fmt.Printf("\n── Session: %s, id=%s ──\n", mode, plan.injection.SessionID)
	if len(plan.injection.Args) > 0 {
		fmt.Printf("── Injected args: %s ──\n", strings.Join(plan.injection.Args, " "))
	} else {
		fmt.Printf("── Injected args: none (agent CLI not recognized; session ID exposed via $%s) ──\n", agentSessionIDEnv)
	}
}
