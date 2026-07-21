package commands

import (
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

// Tests for adoptActiveAgentSession / adoptActiveAgentSessionForBranch.
//
// The adoption helper exists so a feature session launched via
// `ezs agent feature "..."` (which mints a UUID but has no stack to bind to
// at launch time) can be stitched onto a stack the agent creates mid-flight
// via `ezs new` or `mcp__ezstack__ezstack_new`. Without this, `ezs agent ls`
// would show no row for the new stack and a follow-up `ezs agent` inside it
// would mint a fresh session instead of resuming the agent's conversation.
//
// Adoption is gated on EZS_AGENT_SESSION_MODE=feature so work-mode sessions
// don't silently follow the agent into unrelated stacks the user didn't ask
// to bind. The cases below pin those rules in place.

// seedStack inserts a bare Stack into the manager's in-memory config (no
// branches or worktrees) and persists. Side-steps RegisterExistingBranch so
// these tests don't need real git branches — adoption only touches the
// AgentSessionID/Mode slot, which is independent of the tree.
func seedStack(t *testing.T, mgr managerLike, hash, name string) {
	t.Helper()
	sc := mgr.GetStackConfig()
	sc.Stacks[hash] = &config.Stack{Hash: hash, Name: name, Root: "main"}
	if err := sc.Save(mgr.GetRepoDir()); err != nil {
		t.Fatalf("seed save: %v", err)
	}
}

// managerLike is the subset of *stack.Manager the seed helper needs. Defined
// here to keep the helper's signature short and to avoid pulling internal/stack
// into test-only typing concerns.
type managerLike interface {
	GetStackConfig() *config.StackConfig
	GetRepoDir() string
}

func TestAdoptActiveAgentSession_NoEnvVarIsNoOp(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	seedStack(t, mgr, "abc1234", "feature-stack")

	// EZS_AGENT_SESSION_ID is unset (no active feature session in flight).
	// adoptActiveAgentSession must leave the stack's AgentSessionID empty,
	// preserving the long-standing "ezs new from a plain shell doesn't
	// touch session bookkeeping" behavior.
	t.Setenv(agentSessionIDEnv, "")
	t.Setenv(agentSessionModeEnv, "")

	adoptActiveAgentSession(repoDir, "abc1234")

	mgr2 := reloadManager(t, repoDir)
	if got := mgr2.GetStackConfig().Stacks["abc1234"].AgentSessionID; got != "" {
		t.Errorf("stack adopted a session despite no env var; AgentSessionID=%q", got)
	}
}

func TestAdoptActiveAgentSession_NoModeEnvIsNoOp(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	seedStack(t, mgr, "def5678", "")

	// ID present, mode absent — happens for sessions launched by an older
	// ezs binary (before mode propagation existed) that the user hasn't
	// re-launched yet. We deliberately don't adopt: we don't know whether
	// the inherited session is feature or work mode, and silently picking
	// one risks mistagging.
	t.Setenv(agentSessionIDEnv, "deadbeef-uuid")
	t.Setenv(agentSessionModeEnv, "")

	adoptActiveAgentSession(repoDir, "def5678")

	mgr2 := reloadManager(t, repoDir)
	if got := mgr2.GetStackConfig().Stacks["def5678"].AgentSessionID; got != "" {
		t.Errorf("adopted with no mode tag — should be a no-op; AgentSessionID=%q", got)
	}
}

func TestAdoptActiveAgentSession_WorkModeIsNoOp(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	seedStack(t, mgr, "1111111", "")

	// Work-mode sessions are scoped to the stack they were launched in.
	// If the agent (running inside that work session) creates a new stack,
	// auto-binding the work session to the new stack would surprise the
	// user — they'd see two unrelated stacks pointing at the same UUID
	// with no clear reason. Stay out of work-mode adoption.
	t.Setenv(agentSessionIDEnv, "work-session-uuid")
	t.Setenv(agentSessionModeEnv, config.AgentSessionWorkMode)

	adoptActiveAgentSession(repoDir, "1111111")

	mgr2 := reloadManager(t, repoDir)
	if got := mgr2.GetStackConfig().Stacks["1111111"].AgentSessionID; got != "" {
		t.Errorf("work-mode session must not adopt; AgentSessionID=%q", got)
	}
}

func TestAdoptActiveAgentSession_FeatureModeAdopts(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	seedStack(t, mgr, "feabc01", "my-feature")

	// The headline case: `ezs agent feature` minted UUID `S`, the agent
	// (running in feature mode) called `ezs new` which created stack
	// `feabc01`. Adoption should bind `S` to `feabc01` tagged feature mode
	// so `ezs agent ls` surfaces the row and `ezs agent -s feabc01`
	// resumes the conversation.
	t.Setenv(agentSessionIDEnv, "feature-session-uuid")
	t.Setenv(agentSessionModeEnv, config.AgentSessionFeatureMode)

	adoptActiveAgentSession(repoDir, "feabc01")

	mgr2 := reloadManager(t, repoDir)
	s := mgr2.GetStackConfig().Stacks["feabc01"]
	if s.AgentSessionID != "feature-session-uuid" {
		t.Errorf("AgentSessionID = %q, want feature-session-uuid", s.AgentSessionID)
	}
	if s.AgentSessionMode != config.AgentSessionFeatureMode {
		t.Errorf("AgentSessionMode = %q, want %q", s.AgentSessionMode, config.AgentSessionFeatureMode)
	}
}

func TestAdoptActiveAgentSession_RebindsAcrossMultipleStacks(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	seedStack(t, mgr, "stack-a", "feature-a")
	seedStack(t, mgr, "stack-b", "feature-b")

	// One feature session, two stacks created in a row. The session UUID
	// should land on both stacks — claude resumes by UUID (not by stack),
	// so every stack the session created should be a valid resume target.
	// `ezs agent ls` will surface one row per stack with the same short
	// ID, which truthfully reflects "one session, multiple stacks."
	t.Setenv(agentSessionIDEnv, "shared-feature-uuid")
	t.Setenv(agentSessionModeEnv, config.AgentSessionFeatureMode)

	adoptActiveAgentSession(repoDir, "stack-a")
	adoptActiveAgentSession(repoDir, "stack-b")

	mgr2 := reloadManager(t, repoDir)
	for _, hash := range []string{"stack-a", "stack-b"} {
		s := mgr2.GetStackConfig().Stacks[hash]
		if s.AgentSessionID != "shared-feature-uuid" {
			t.Errorf("stack %s AgentSessionID = %q, want shared-feature-uuid", hash, s.AgentSessionID)
		}
		if s.AgentSessionMode != config.AgentSessionFeatureMode {
			t.Errorf("stack %s AgentSessionMode = %q, want %q", hash, s.AgentSessionMode, config.AgentSessionFeatureMode)
		}
	}
}

func TestAdoptActiveAgentSession_OverwritesExistingSlot(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	seedStack(t, mgr, "preowned", "")
	// Pre-populate with a stale work-mode session (e.g. a stack the user
	// previously ran `ezs agent` in). When a feature session creates a new
	// stack and we adopt onto it, last-write-wins is the existing slot
	// semantic (documented in resolveFeatureSession's comment about
	// stack/feature sharing the same AgentSessionID slot).
	if err := mgr.SetStackAgentSessionID("preowned", "stale-work-uuid", config.AgentSessionWorkMode); err != nil {
		t.Fatalf("seed work session: %v", err)
	}

	t.Setenv(agentSessionIDEnv, "fresh-feature-uuid")
	t.Setenv(agentSessionModeEnv, config.AgentSessionFeatureMode)

	adoptActiveAgentSession(repoDir, "preowned")

	mgr2 := reloadManager(t, repoDir)
	s := mgr2.GetStackConfig().Stacks["preowned"]
	if s.AgentSessionID != "fresh-feature-uuid" {
		t.Errorf("AgentSessionID = %q, want fresh-feature-uuid (overwrite)", s.AgentSessionID)
	}
	if s.AgentSessionMode != config.AgentSessionFeatureMode {
		t.Errorf("AgentSessionMode = %q, want %q (mode tag updated to match new ID)", s.AgentSessionMode, config.AgentSessionFeatureMode)
	}
}

func TestAdoptActiveAgentSession_EmptyStackHashIsNoOp(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	t.Setenv(agentSessionIDEnv, "uuid")
	t.Setenv(agentSessionModeEnv, config.AgentSessionFeatureMode)

	// Defensive: a caller with no stack hash to bind shouldn't trigger
	// any persistence (and shouldn't crash).
	adoptActiveAgentSession(repoDir, "")
}

func TestAdoptActiveAgentSession_UnknownStackHashIsBestEffort(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	t.Setenv(agentSessionIDEnv, "uuid")
	t.Setenv(agentSessionModeEnv, config.AgentSessionFeatureMode)

	// Hash doesn't exist in config — the underlying SetStackAgentSessionID
	// returns "stack not found", but adoption is best-effort: it should
	// log and return, not panic or surface an error to the caller (the
	// stack creation that called it has already mutated git state).
	adoptActiveAgentSession(repoDir, "missinghash")
}

func TestAdoptActiveAgentSessionForBranch_LooksUpStack(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	// Use the manager API end-to-end: register a real worktree-rooted
	// branch so GetStackForBranch resolves correctly.
	mustGit(t, repoDir, "checkout", "-b", "child")
	mustGit(t, repoDir, "checkout", "main")
	if _, err := mgr.RegisterExistingBranch("child", repoDir, "main"); err != nil {
		t.Fatalf("register branch: %v", err)
	}

	t.Setenv(agentSessionIDEnv, "branch-feature-uuid")
	t.Setenv(agentSessionModeEnv, config.AgentSessionFeatureMode)

	mgr2 := reloadManager(t, repoDir)
	adoptActiveAgentSessionForBranch(mgr2, "child")

	mgr3 := reloadManager(t, repoDir)
	s := mgr3.GetStackForBranch("child")
	if s == nil {
		t.Fatal("branch lost its stack registration")
	}
	if s.AgentSessionID != "branch-feature-uuid" {
		t.Errorf("AgentSessionID = %q, want branch-feature-uuid", s.AgentSessionID)
	}
	if s.AgentSessionMode != config.AgentSessionFeatureMode {
		t.Errorf("AgentSessionMode = %q, want %q", s.AgentSessionMode, config.AgentSessionFeatureMode)
	}
}

func TestAdoptActiveAgentSessionForBranch_UntrackedBranchIsNoOp(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)

	t.Setenv(agentSessionIDEnv, "uuid")
	t.Setenv(agentSessionModeEnv, config.AgentSessionFeatureMode)

	mgr := reloadManager(t, repoDir)
	// Branch has never been registered — no stack to bind to. Helper must
	// return cleanly without writing anything.
	adoptActiveAgentSessionForBranch(mgr, "ghost-branch")
}

// TestAdoptActiveAgentSession_SurvivesSubsequentMgrSave pins the ordering
// requirement in new.go: a same-process Save on `mgr` after adoption (e.g.
// the user accepting promptStackName with a non-empty value) must not
// overwrite the freshly-adopted session ID.
//
// Adoption persists via a fresh stack.Manager, which means the *original*
// mgr that called the adoption helper does not see the write. If new.go
// then runs `mgr.SetStackName` (or any other mgr-mediated mutation), mgr's
// Save 3-way-merges against a stale origSnapshot — modify-vs-modify on the
// same stack value, last-writer-wins, mine has session="". Net effect: the
// session ID disappears silently, breaking the headline `ezs new` flow.
//
// new.go avoids this by running promptStackName BEFORE adoption (so the
// only Save that sees a stale snapshot is the harmless one with no
// concurrent peer); this test pins that contract by exercising the
// adopt-first-then-save shape and asserting the session survives.
//
// If this test fails, check the call order at every adopt site in new.go:
// adoption must come AFTER any other mgr.Save you intend to run for the
// same stack, not before.
func TestAdoptActiveAgentSession_SurvivesSubsequentMgrSave(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	mustGit(t, repoDir, "checkout", "-b", "feat")
	if _, err := mgr.RegisterExistingBranch("feat", repoDir, "main"); err != nil {
		t.Fatalf("register: %v", err)
	}
	hash := mgr.GetStackForBranch("feat").Hash

	t.Setenv(agentSessionIDEnv, "feature-uuid")
	t.Setenv(agentSessionModeEnv, config.AgentSessionFeatureMode)

	// Mirror new.go's required order: rename first (mgr's only Save), then
	// adopt (fresh-manager write).
	if err := mgr.SetStackName(hash, "my-feature"); err != nil {
		t.Fatalf("SetStackName: %v", err)
	}
	adoptActiveAgentSessionForBranch(mgr, "feat")

	got := reloadManager(t, repoDir).GetStackConfig().Stacks[hash]
	if got.AgentSessionID != "feature-uuid" {
		t.Errorf("AgentSessionID = %q, want feature-uuid", got.AgentSessionID)
	}
	if got.Name != "my-feature" {
		t.Errorf("Name = %q, want my-feature", got.Name)
	}
	if got.AgentSessionMode != config.AgentSessionFeatureMode {
		t.Errorf("AgentSessionMode = %q, want %q", got.AgentSessionMode, config.AgentSessionFeatureMode)
	}
}
