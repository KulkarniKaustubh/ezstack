package commands

import (
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

// Tests for `ezs agent rm`. Each case sets up a minimal stack/branch
// session binding via the manager API and then runs agentRm directly with
// the relevant flags, asserting the right slot is cleared (or untouched)
// and that the cli surface rejects ambiguous/empty flag combinations.

func TestAgentRm_RequiresAFilter(t *testing.T) {
	setupCLITestEnv(t)
	// No --branch / --stack / --all is the only realistic typo that could
	// silently wipe the wrong session if we picked a default. Reject it.
	err := agentRm([]string{})
	if err == nil || !strings.Contains(err.Error(), "requires one of --branch, --stack, --all") {
		t.Errorf("expected guidance error, got %v", err)
	}
}

func TestAgentRm_FiltersAreMutuallyExclusive(t *testing.T) {
	setupCLITestEnv(t)
	err := agentRm([]string{"--stack", "--all"})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually-exclusive error, got %v", err)
	}
}

func TestAgentRm_RejectsPositional(t *testing.T) {
	setupCLITestEnv(t)
	err := agentRm([]string{"--all", "extra"})
	if err == nil || !strings.Contains(err.Error(), "positional") {
		t.Errorf("expected positional rejection, got %v", err)
	}
}

func TestAgentRm_StackScope_ClearsCurrentStack(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	// Register a real stack rooted at "feat" so GetCurrentStack resolves.
	mustGit(t, repoDir, "checkout", "-b", "feat")
	if _, err := mgr.RegisterExistingBranch("feat", repoDir, "main"); err != nil {
		t.Fatalf("register: %v", err)
	}
	stk := mgr.GetStackForBranch("feat")
	if stk == nil {
		t.Fatal("stack not found after register")
	}
	if err := mgr.SetStackAgentSessionID(stk.Hash, "stale-uuid", config.AgentSessionWorkMode); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	if err := agentRm([]string{"-s"}); err != nil {
		t.Fatalf("agent rm -s: %v", err)
	}

	mgr2 := reloadManager(t, repoDir)
	got := mgr2.GetStackConfig().Stacks[stk.Hash]
	if got.AgentSessionID != "" {
		t.Errorf("expected stack AgentSessionID cleared; got %q", got.AgentSessionID)
	}
	if got.AgentSessionMode != "" {
		t.Errorf("expected stack AgentSessionMode cleared; got %q", got.AgentSessionMode)
	}
}

func TestAgentRm_StackScope_ErrorsWhenNoSession(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	mustGit(t, repoDir, "checkout", "-b", "feat")
	if _, err := mgr.RegisterExistingBranch("feat", repoDir, "main"); err != nil {
		t.Fatalf("register: %v", err)
	}
	// No session bound — rm should be loud, not a silent no-op.
	err := agentRm([]string{"-s"})
	if err == nil || !strings.Contains(err.Error(), "no session is bound to stack") {
		t.Errorf("expected no-session error; got %v", err)
	}
}

func TestAgentRm_BranchScope_ClearsCurrentBranch(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	mustGit(t, repoDir, "checkout", "-b", "feat")
	if _, err := mgr.RegisterExistingBranch("feat", repoDir, "main"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := mgr.SetBranchAgentSessionID("feat", "branch-uuid", config.AgentSessionWorkMode); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := agentRm([]string{"-b"}); err != nil {
		t.Fatalf("agent rm -b: %v", err)
	}

	mgr2 := reloadManager(t, repoDir)
	bc := mgr2.GetStackConfig().Cache.Branches["feat"]
	if bc == nil {
		t.Fatal("branch cache disappeared")
	}
	if bc.AgentSessionID != "" {
		t.Errorf("expected branch AgentSessionID cleared; got %q", bc.AgentSessionID)
	}
}

func TestAgentRm_BranchScope_ErrorsOnUntrackedBranch(t *testing.T) {
	repoDir, _ := setupCLITestEnv(t)
	// Stay on main, which is the base branch and not tracked as a stack
	// member. agentRm -b should refuse.
	mustGit(t, repoDir, "checkout", "main")
	err := agentRm([]string{"-b"})
	if err == nil || !strings.Contains(err.Error(), "not tracked") {
		t.Errorf("expected not-tracked error; got %v", err)
	}
}

func TestAgentRm_AllScope_ClearsEverything(t *testing.T) {
	repoDir, mgr := setupCLITestEnv(t)
	// Two stacks + a branch session, all bound.
	mustGit(t, repoDir, "checkout", "-b", "feat")
	if _, err := mgr.RegisterExistingBranch("feat", repoDir, "main"); err != nil {
		t.Fatalf("register feat: %v", err)
	}
	mustGit(t, repoDir, "checkout", "-b", "other")
	if _, err := mgr.RegisterExistingBranch("other", repoDir, "main"); err != nil {
		t.Fatalf("register other: %v", err)
	}
	stk1 := mgr.GetStackForBranch("feat")
	stk2 := mgr.GetStackForBranch("other")
	if stk1 == nil || stk2 == nil {
		t.Fatal("stacks not found")
	}
	if err := mgr.SetStackAgentSessionID(stk1.Hash, "uuid-stack-1", config.AgentSessionFeatureMode); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetStackAgentSessionID(stk2.Hash, "uuid-stack-2", config.AgentSessionWorkMode); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetBranchAgentSessionID("feat", "uuid-branch", config.AgentSessionWorkMode); err != nil {
		t.Fatal(err)
	}

	// setupCLITestEnv enables ui.YesMode so the confirmation gate auto-ack.
	if err := agentRm([]string{"--all"}); err != nil {
		t.Fatalf("agent rm --all: %v", err)
	}

	mgr2 := reloadManager(t, repoDir)
	for _, hash := range []string{stk1.Hash, stk2.Hash} {
		s := mgr2.GetStackConfig().Stacks[hash]
		if s.AgentSessionID != "" {
			t.Errorf("stack %s still has AgentSessionID=%q", hash, s.AgentSessionID)
		}
	}
	if bc := mgr2.GetStackConfig().Cache.Branches["feat"]; bc != nil && bc.AgentSessionID != "" {
		t.Errorf("branch session still set: %q", bc.AgentSessionID)
	}
}

func TestAgentRm_AllScope_NoBindingsIsClean(t *testing.T) {
	setupCLITestEnv(t)
	// No sessions bound anywhere — rm --all should succeed without
	// touching the config and without error.
	if err := agentRm([]string{"--all"}); err != nil {
		t.Errorf("expected clean exit when nothing to forget; got %v", err)
	}
}
