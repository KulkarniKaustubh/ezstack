package commands

import (
	"strings"
	"testing"
)

// TestUpgradeHelp verifies that --help short-circuits before any network
// activity. This is the only safe end-to-end exercise of the CLI command
// in unit tests; the actual upgrade flow is covered by internal/upgrade.
func TestUpgradeHelp(t *testing.T) {
	_, stderr := captureStdAndErr(t, func() {
		if err := Upgrade([]string{"--help"}); err != nil {
			t.Fatalf("Upgrade --help: %v", err)
		}
	})
	for _, want := range []string{"USAGE", "ezs upgrade", "--check", "--no-mcp"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("--help output missing %q. Got:\n%s", want, stderr)
		}
	}
}

func TestUpgradeBadFlag(t *testing.T) {
	_, _ = captureStdAndErr(t, func() {
		err := Upgrade([]string{"--no-such-flag"})
		if err == nil {
			t.Fatal("expected error for unknown flag")
		}
	})
}
