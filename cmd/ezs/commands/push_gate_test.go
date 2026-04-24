package commands

import (
	"strings"
	"testing"
)

// TestPush_RefusesWhenAgentNoPushEnvSet is the direct regression for the
// `EZS_AGENT_NO_PUSH=1` push gate wired in at push.go:53. If an agent session
// was launched with `ezs agent --no-push`, nested `ezs push` calls must bail
// out immediately rather than silently reach origin.
//
// The env-var check runs before any git / manager work, so no fake repo is
// required — Push should error out on the env check alone.
func TestPush_RefusesWhenAgentNoPushEnvSet(t *testing.T) {
	t.Setenv(agentNoPushEnv, "1")

	err := Push(nil)
	if err == nil {
		t.Fatal("Push returned nil with EZS_AGENT_NO_PUSH=1 — the gate is broken")
	}
	if !strings.Contains(err.Error(), "agent --no-push") {
		t.Errorf("error %q should mention the agent --no-push source", err.Error())
	}
}
