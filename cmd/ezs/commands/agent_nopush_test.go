package commands

import (
	"strings"
	"testing"
)

// TestAgentProcessEnv_InjectsNoPushFlag is the direct regression for the
// child-env propagation half of `ezs agent --no-push`. When the flag is set,
// the spawned agent must see EZS_AGENT_NO_PUSH=1 so its nested `ezs push`
// calls hit the push gate at push.go:53.
func TestAgentProcessEnv_InjectsNoPushFlag(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "HOME=/home/test", "UNRELATED=x"}

	got := agentProcessEnv(parent, true)

	var seen bool
	for _, kv := range got {
		if kv == agentNoPushEnv+"=1" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("agent env missing %s=1; got %v", agentNoPushEnv, got)
	}

	// Parent env must still be present — the agent depends on PATH/HOME/etc.
	for _, want := range parent {
		found := false
		for _, kv := range got {
			if kv == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("agent env dropped parent var %q", want)
		}
	}
}

// TestAgentProcessEnv_DoesNotSetFlagWhenDisabled asserts the gate variable
// is absent from the spawned env when noPush=false, and that the rest of
// the parent env is passed through.
func TestAgentProcessEnv_DoesNotSetFlagWhenDisabled(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "HOME=/home/test"}

	got := agentProcessEnv(parent, false)

	for _, kv := range got {
		if strings.HasPrefix(kv, agentNoPushEnv+"=") {
			t.Errorf("agent env unexpectedly carries %s=...; got %v", agentNoPushEnv, got)
		}
	}
	for _, want := range parent {
		found := false
		for _, kv := range got {
			if kv == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("agent env dropped parent var %q", want)
		}
	}
}

// TestAgentProcessEnv_StripsInheritedGateWhenDisabled is the regression for
// nested-agent leakage: if the parent process already has
// EZS_AGENT_NO_PUSH=1 set (because *it* was spawned by an `agent --no-push`),
// a nested invocation that does NOT pass --no-push must not inherit the gate.
// Otherwise the user gets a transitive push-block they didn't ask for.
func TestAgentProcessEnv_StripsInheritedGateWhenDisabled(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		agentNoPushEnv + "=1", // pre-existing gate from outer agent
		"HOME=/home/test",
	}

	got := agentProcessEnv(parent, false)

	for _, kv := range got {
		if strings.HasPrefix(kv, agentNoPushEnv+"=") {
			t.Errorf("inherited %s=... was not stripped when noPush=false; got %v", agentNoPushEnv, got)
		}
	}
}

// TestAgentProcessEnv_DeduplicatesWhenEnabled covers the symmetric case:
// if the parent already had EZS_AGENT_NO_PUSH set (any value) and we're
// spawning with noPush=true, the result must contain exactly one entry —
// "EZS_AGENT_NO_PUSH=1". Duplicates rely on POSIX last-wins semantics that
// not every process reads correctly.
func TestAgentProcessEnv_DeduplicatesWhenEnabled(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		agentNoPushEnv + "=stale-value",
	}

	got := agentProcessEnv(parent, true)

	count := 0
	for _, kv := range got {
		if strings.HasPrefix(kv, agentNoPushEnv+"=") {
			count++
			if kv != agentNoPushEnv+"=1" {
				t.Errorf("expected gate value to be 1; got %q", kv)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one %s entry; got %d in %v", agentNoPushEnv, count, got)
	}
}

// TestAgentProcessEnv_NilParentDoesNotCrash pins down defensive behavior
// when the caller passes a nil env (possible in synthetic test contexts).
func TestAgentProcessEnv_NilParentDoesNotCrash(t *testing.T) {
	got := agentProcessEnv(nil, true)

	if len(got) < 1 || got[len(got)-1] != agentNoPushEnv+"=1" {
		t.Errorf("expected gate var as final element even with nil parent; got %v", got)
	}
}
