package commands

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
)

// The human-readable PrintStack output renders a (remote) tag for stack roots
// picked up via `ezs new origin/*` / `-r`. Machine consumers (MCP server,
// neovim plugin, agent harness) read the JSON envelope produced by `ezs ls
// --json` and `ezs status --json`. If `root_is_remote` doesn't surface there,
// those clients have no way to render the same indicator and silently
// disagree with the terminal output. Lock the JSON contract here.
func TestPrintStacksJSON_IncludesRootIsRemoteAndPerBranchIsRemote(t *testing.T) {
	stacks := []*config.Stack{
		{
			Hash:         "abc1234",
			Root:         "alice/feature",
			RootBase:     "main",
			RootIsRemote: true,
			Branches: []*config.Branch{
				// Root rendered as a virtual branch alongside tree members.
				{Name: "alice/feature", Parent: "main", IsRemote: true},
				{Name: "my-feature", Parent: "alice/feature"},
			},
		},
	}

	stdout, _ := captureStdAndErr(t, func() {
		if err := printStacksJSON(stacks, "my-feature", nil); err != nil {
			t.Fatalf("printStacksJSON: %v", err)
		}
	})

	var got []map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, stdout)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 stack, got %d:\n%s", len(got), stdout)
	}
	if v, ok := got[0]["root_is_remote"].(bool); !ok || !v {
		t.Errorf("root_is_remote missing or not true; got: %v\nfull output:\n%s", got[0]["root_is_remote"], stdout)
	}

	// Per-branch is_remote: alice/feature must be flagged, my-feature must not.
	branches, _ := got[0]["branches"].([]interface{})
	if len(branches) != 2 {
		t.Fatalf("expected 2 branches in JSON, got %d:\n%s", len(branches), stdout)
	}
	for _, br := range branches {
		bm, _ := br.(map[string]interface{})
		switch bm["name"] {
		case "alice/feature":
			if v, _ := bm["is_remote"].(bool); !v {
				t.Errorf("branch alice/feature: is_remote should be true, got %v", bm["is_remote"])
			}
		case "my-feature":
			// `omitempty` on bool keeps `is_remote` out of the JSON for the
			// false case — assert by absence (or explicit false).
			if v, ok := bm["is_remote"].(bool); ok && v {
				t.Errorf("branch my-feature: is_remote should be false/absent, got %v", bm["is_remote"])
			}
		}
	}
}

// Same JSON contract for the status-flavored output. `ezs status --json` is
// the authoritative wire shape for the MCP server, so a parity gap here is
// load-bearing.
func TestPrintStacksStatusJSON_IncludesRootIsRemoteAndPerBranchIsRemote(t *testing.T) {
	stacks := []*config.Stack{
		{
			Hash:         "def5678",
			Root:         "alice/feature",
			RootBase:     "main",
			RootIsRemote: true,
			Branches: []*config.Branch{
				{Name: "alice/feature", Parent: "main", IsRemote: true},
				{Name: "my-feature", Parent: "alice/feature"},
			},
		},
	}

	stdout, _ := captureStdAndErr(t, func() {
		if err := printStacksStatusJSON(stacks, "my-feature", []map[string]*ui.BranchStatus{nil}); err != nil {
			t.Fatalf("printStacksStatusJSON: %v", err)
		}
	})

	if !strings.Contains(stdout, `"root_is_remote": true`) {
		t.Errorf("status JSON missing root_is_remote=true:\n%s", stdout)
	}

	var got []map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, stdout)
	}
	branches, _ := got[0]["branches"].([]interface{})
	for _, br := range branches {
		bm, _ := br.(map[string]interface{})
		if bm["name"] == "alice/feature" {
			if v, _ := bm["is_remote"].(bool); !v {
				t.Errorf("status JSON branch alice/feature: is_remote should be true, got %v", bm["is_remote"])
			}
		}
	}
}

// Negative: a user-owned stack (root is a normal local branch, no remote
// pickup) must NOT surface root_is_remote=true. omitempty keeps the field
// out of the JSON entirely for the false case, so ML/regex consumers don't
// match a substring like `root_is_remote` and conclude the wrong thing.
func TestPrintStacksJSON_OmitsRootIsRemoteForUserOwnedStacks(t *testing.T) {
	stacks := []*config.Stack{
		{
			Hash:     "ghi9012",
			Root:     "main",
			RootBase: "",
			Branches: []*config.Branch{
				{Name: "feature", Parent: "main"},
			},
		},
	}

	stdout, _ := captureStdAndErr(t, func() {
		if err := printStacksJSON(stacks, "feature", nil); err != nil {
			t.Fatalf("printStacksJSON: %v", err)
		}
	})

	if strings.Contains(stdout, "root_is_remote") {
		t.Errorf("user-owned stack JSON should omit root_is_remote, got:\n%s", stdout)
	}
}
