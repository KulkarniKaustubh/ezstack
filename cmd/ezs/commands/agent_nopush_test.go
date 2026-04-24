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

// TestAgentProcessEnv_DoesNotSetFlagWhenDisabled asserts we return nil (which
// makes exec.Cmd inherit parent's env verbatim) rather than duplicating it.
// Returning a copy would also work, but nil is the documented sentinel and
// saves an allocation on the common path.
func TestAgentProcessEnv_DoesNotSetFlagWhenDisabled(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "HOME=/home/test"}

	got := agentProcessEnv(parent, false)

	if got != nil {
		// If someone ever chooses to eagerly copy the env, at minimum the
		// result must not contain the gate variable.
		for _, kv := range got {
			if strings.HasPrefix(kv, agentNoPushEnv+"=") {
				t.Errorf("agent env unexpectedly carries %s=...; got %v", agentNoPushEnv, got)
			}
		}
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
