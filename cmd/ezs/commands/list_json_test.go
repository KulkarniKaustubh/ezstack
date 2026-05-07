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

// Public-fork stacking: when fork mode is enabled and the stack roots on
// upstream's default branch, the JSON envelope must surface
// is_fork_mode + upstream_repo + per-branch pr_target_repo / label so UI
// clients (VSCode, Tauri, nvim) can render the fork badge and chips.
func TestPrintStacksStatusJSON_PopulatesForkModeFields(t *testing.T) {
	stacks := []*config.Stack{
		{
			Hash: "fork0001",
			Root: "main",
			Branches: []*config.Branch{
				{
					Name:         "feat-a",
					Parent:       "main",
					PRNumber:     100,
					PRTargetRepo: config.PRTargetRepoUpstream,
					IsMerged:     true,
				},
				{
					Name:         "feat-b",
					Parent:       "feat-a",
					PRNumber:     101,
					PRTargetRepo: config.PRTargetRepoFork,
				},
			},
		},
	}
	fm := &forkModeJSONContext{
		IsForkStack: func(s *config.Stack) bool { return s.Root == "main" },
		OriginLabel: "alice/myproject",
		Up: &upstreamInfo{
			Owner: "upstreamOrg", Repo: "myproject",
			Remote: "upstream", DefaultBranch: "main",
			Enabled: true,
		},
	}

	stdout, _ := captureStdAndErr(t, func() {
		if err := printStacksStatusJSONWithFork(stacks, "feat-b", []map[string]*ui.BranchStatus{nil}, fm); err != nil {
			t.Fatalf("printStacksStatusJSONWithFork: %v", err)
		}
	})

	var got []map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 stack, got %d", len(got))
	}
	if v, _ := got[0]["is_fork_mode"].(bool); !v {
		t.Errorf("is_fork_mode missing or false; got: %v", got[0]["is_fork_mode"])
	}
	if v, _ := got[0]["upstream_repo"].(string); v != "upstreamOrg/myproject" {
		t.Errorf("upstream_repo = %q, want upstreamOrg/myproject", v)
	}

	branches, _ := got[0]["branches"].([]interface{})
	for _, br := range branches {
		bm, _ := br.(map[string]interface{})
		switch bm["name"] {
		case "feat-a":
			if v, _ := bm["pr_target_repo"].(string); v != "upstream" {
				t.Errorf("feat-a pr_target_repo = %q, want upstream", v)
			}
			if v, _ := bm["pr_target_repo_label"].(string); v != "upstreamOrg/myproject" {
				t.Errorf("feat-a label = %q, want upstreamOrg/myproject", v)
			}
		case "feat-b":
			if v, _ := bm["pr_target_repo"].(string); v != "fork" {
				t.Errorf("feat-b pr_target_repo = %q, want fork", v)
			}
			if v, _ := bm["pr_target_repo_label"].(string); v != "alice/myproject" {
				t.Errorf("feat-b label = %q, want alice/myproject", v)
			}
			if v, _ := bm["is_promote_pending"].(bool); !v {
				t.Errorf("feat-b is_promote_pending = false, want true (parent merged in upstream)")
			}
		}
	}
}

// Without a forkModeJSONContext (back-compat path), the JSON must NOT
// include any of the fork-mode fields — even on a stack whose branches
// have PRTargetRepo set in their cache. This guards UI clients that
// pre-date fork mode against accidental new fields appearing.
func TestPrintStacksJSON_NoForkContextOmitsForkFields(t *testing.T) {
	stacks := []*config.Stack{
		{
			Hash: "f00d0001",
			Root: "main",
			Branches: []*config.Branch{
				{Name: "feat-a", Parent: "main", PRTargetRepo: config.PRTargetRepoUpstream},
			},
		},
	}
	stdout, _ := captureStdAndErr(t, func() {
		if err := printStacksJSON(stacks, "feat-a", nil); err != nil {
			t.Fatalf("printStacksJSON: %v", err)
		}
	})
	// Stack-level fields absent.
	if strings.Contains(stdout, "is_fork_mode") {
		t.Errorf("classic JSON path should not surface is_fork_mode, got:\n%s", stdout)
	}
	// Branch-level pr_target_repo is sourced from b.PRTargetRepo, so it
	// IS present even without context — assert label is empty though.
	if strings.Contains(stdout, "pr_target_repo_label") {
		t.Errorf("classic JSON path should not surface pr_target_repo_label, got:\n%s", stdout)
	}
}
