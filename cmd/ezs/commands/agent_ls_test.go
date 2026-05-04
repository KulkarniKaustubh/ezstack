package commands

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

func encodeJSONForTest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// TestQuoteIfNeeded covers the resume-command quoter used by `agent ls`.
// The function uses an allowlist (alnum + a small set of POSIX-safe punctuation),
// so anything outside that — whitespace, $, `, ", ', and shell control chars
// like &, |, ;, (, ) — must round-trip through ShellQuote. This is what keeps
// the suggested resume_cmd safe to paste, even when branch names contain
// metacharacters git allows (& and | in particular pass `git check-ref-format`).
func TestQuoteIfNeeded(t *testing.T) {
	cases := map[string]string{
		// Empty must produce a quoted empty token, not a bare empty string —
		// otherwise concatenating into "cd " + quoteIfNeeded(path) would tear
		// the argv if path were ever empty.
		"": "''",
		// Allowlisted-only strings: pass through unquoted for readability.
		"plain":             "plain",
		"already-safe":      "already-safe",
		"normal-stuff_v1.2": "normal-stuff_v1.2",
		"a/b/c.tar.gz":      "a/b/c.tar.gz",
		"key:value":         "key:value",
		"a@b.com":           "a@b.com",
		"100%":              "100%",
		// Whitespace and quoting:
		"with space": "'with space'",
		"with\ttab":  "'with\ttab'",
		"O'Brien":    "'O'\\''Brien'",
		"has\"quote": "'has\"quote'",
		// Shell expansion characters:
		"$variable":  "'$variable'",
		"`backtick`": "'`backtick`'",
		// Shell control characters — the regression that motivated the
		// allowlist switch. Without this, branch names like "feat&deploy"
		// or "stage|prod" produced resume_cmd that, when pasted, would
		// background or pipe into something other than ezs.
		"feat&deploy": "'feat&deploy'",
		"stage|prod":  "'stage|prod'",
		"a;b":         "'a;b'",
		"sub(shell)":  "'sub(shell)'",
		"redir>out":   "'redir>out'",
		"hash#tag":    "'hash#tag'",
		"glob*":       "'glob*'",
		"home~":       "'home~'",
		"new\nline":   "'new\nline'",
	}
	for in, want := range cases {
		if got := quoteIfNeeded(in); got != want {
			t.Errorf("quoteIfNeeded(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestShortSessionID verifies the "first 8 chars + ellipsis" display format
// used by the table output. The full UUID is preserved in JSON; this is a
// purely cosmetic helper so a regression here just makes the table noisier,
// not wrong.
func TestShortSessionID(t *testing.T) {
	cases := map[string]string{
		"":                                     "",
		"short":                                "short",
		"01234567":                             "01234567",
		"012345678":                            "01234567…",
		"9f237219-f026-4669-a2cd-209c824161d9": "9f237219…",
	}
	for in, want := range cases {
		if got := shortSessionID(in); got != want {
			t.Errorf("shortSessionID(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFilterByScope is the helper that splits a row list into the two
// groups the text renderer prints. Empty input must return a non-nil empty
// slice so callers can len-check without nil-guards.
func TestFilterByScope(t *testing.T) {
	rows := []agentSessionRow{
		{Scope: "stack", StackHash: "a"},
		{Scope: "branch", StackHash: "a", BranchName: "feat-x"},
		{Scope: "stack", StackHash: "b"},
		{Scope: "branch", StackHash: "b", BranchName: "feat-y"},
	}
	stacks := filterByScope(rows, "stack")
	if len(stacks) != 2 {
		t.Errorf("expected 2 stack rows, got %d", len(stacks))
	}
	for _, r := range stacks {
		if r.Scope != "stack" {
			t.Errorf("stack filter leaked non-stack row: %+v", r)
		}
	}

	branches := filterByScope(rows, "branch")
	if len(branches) != 2 {
		t.Errorf("expected 2 branch rows, got %d", len(branches))
	}

	none := filterByScope(rows, "neither")
	if none == nil {
		t.Error("filterByScope must return non-nil empty slice for unmatched scope")
	}
	if len(none) != 0 {
		t.Errorf("expected 0 rows for unknown scope, got %d", len(none))
	}
}

// TestAgentSessionRow_ResumeCommandShape pins the shape of the suggested
// resume command. It's a small thing but it's the user-facing payoff of
// having the row at all — if this drifts, the "how do I resume?" answer
// breaks.
func TestAgentSessionRow_ResumeCommandShape(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"branch with no spaces", "feat-auth", "ezs agent --branch feat-auth"},
		{"branch with space", "feat auth", "ezs agent --branch 'feat auth'"},
		{"branch with quote", "O'Brien", "ezs agent --branch 'O'\\''Brien'"},
	}
	for _, c := range cases {
		got := "ezs agent --branch " + quoteIfNeeded(c.in)
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// TestModeOrDefault verifies legacy entries (written before mode tracking)
// surface as "work" — without this, jq pipelines would have to handle the
// empty-string case explicitly, and the text renderer would print bare
// parens "()" next to the stack label.
func TestModeOrDefault(t *testing.T) {
	cases := map[string]string{
		"":                                  config.AgentSessionWorkMode,
		config.AgentSessionWorkMode:         config.AgentSessionWorkMode,
		config.AgentSessionFeatureMode:      config.AgentSessionFeatureMode,
		"unrecognized-mode-from-the-future": "unrecognized-mode-from-the-future",
	}
	for in, want := range cases {
		if got := modeOrDefault(in); got != want {
			t.Errorf("modeOrDefault(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestStackDisplayLabel confirms feature-mode stack sessions get the
// "_ezstack-feature-<name>" label that matches what's shown in claude's
// /resume picker at launch time. Work-mode sessions get the bare
// "_ezstack-<name>" form.
func TestStackDisplayLabel(t *testing.T) {
	work := &config.Stack{Hash: "abc1234", Name: "alpha", AgentSessionMode: config.AgentSessionWorkMode}
	if got := stackDisplayLabel(work); got != "_ezstack-alpha" {
		t.Errorf("work-mode label = %q, want _ezstack-alpha", got)
	}
	feature := &config.Stack{Hash: "abc1234", Name: "alpha", AgentSessionMode: config.AgentSessionFeatureMode}
	if got := stackDisplayLabel(feature); got != "_ezstack-feature-alpha" {
		t.Errorf("feature-mode label = %q, want _ezstack-feature-alpha", got)
	}
	// Empty mode (legacy entry) defaults to work-mode label.
	legacy := &config.Stack{Hash: "abc1234", Name: "alpha"}
	if got := stackDisplayLabel(legacy); got != "_ezstack-alpha" {
		t.Errorf("legacy (empty mode) label = %q, want _ezstack-alpha", got)
	}
	// Hash-only fallback when Name is empty.
	hashOnly := &config.Stack{Hash: "abc1234", AgentSessionMode: config.AgentSessionFeatureMode}
	if got := stackDisplayLabel(hashOnly); got != "_ezstack-feature-abc1234" {
		t.Errorf("hash-only feature label = %q, want _ezstack-feature-abc1234", got)
	}
}

// TestCollectAgentSessionsFromStackConfig exercises the per-repo collector.
// Behaviors pinned:
//
//   - branches whose cache has a session BUT aren't in this stack's tree
//     are skipped (the cache is repo-wide; without HasBranch every stack
//     would re-list every branch's session)
//   - branches with empty AgentSessionID don't produce rows
//   - stack-scoped sessions surface once per stack with their AgentSessionID
//   - mode tag is propagated onto the row from the stored AgentSessionMode
//   - feature-mode stacks get the "_ezstack-feature-<name>" display label
func TestCollectAgentSessionsFromStackConfig(t *testing.T) {
	workStack := &config.Stack{
		Hash:             "abc1234",
		Name:             "my-stack",
		Root:             "main",
		AgentSessionID:   "uuid-stack",
		AgentSessionMode: config.AgentSessionWorkMode,
		Tree: config.BranchTree{
			"feat-x": config.BranchTree{},
			"feat-y": config.BranchTree{},
		},
	}
	featureStack := &config.Stack{
		Hash:             "def5678",
		Name:             "my-feature",
		Root:             "main",
		AgentSessionID:   "uuid-feature",
		AgentSessionMode: config.AgentSessionFeatureMode,
		Tree: config.BranchTree{
			"feat-z": config.BranchTree{},
		},
	}
	cache := &config.CacheConfig{
		Branches: map[string]*config.BranchCache{
			"feat-x":   {AgentSessionID: "uuid-x", AgentSessionMode: config.AgentSessionWorkMode},
			"feat-y":   {AgentSessionID: ""},                                                         // no session — skip
			"feat-z":   {},                                                                           // in featureStack but no session
			"orphan":   {AgentSessionID: "uuid-orph", AgentSessionMode: config.AgentSessionWorkMode}, // not in any stack
			"untouchd": {},
		},
	}
	workStack.SetCache(cache)
	workStack.PopulateBranchesWithCache(cache)
	featureStack.SetCache(cache)
	featureStack.PopulateBranchesWithCache(cache)

	sc := &config.StackConfig{
		Stacks: map[string]*config.Stack{"abc1234": workStack, "def5678": featureStack},
		Cache:  cache,
	}

	rows := collectAgentSessionsFromStackConfig("/r", sc)

	t.Run("expected number of rows", func(t *testing.T) {
		// Expected: 1 stack-scoped (work) + 1 stack-scoped (feature) +
		// 1 branch-scoped (feat-x). orphan is filtered (not in any tree),
		// feat-y/feat-z have no session.
		if len(rows) != 3 {
			t.Errorf("got %d rows, want 3: %+v", len(rows), rows)
		}
	})

	t.Run("orphan branch (in cache, not in stack tree) is filtered", func(t *testing.T) {
		for _, r := range rows {
			if r.BranchName == "orphan" {
				t.Errorf("orphan branch should not appear; got %v", r)
			}
		}
	})

	t.Run("branch with empty session id is skipped", func(t *testing.T) {
		for _, r := range rows {
			if r.BranchName == "feat-y" || r.BranchName == "feat-z" {
				t.Errorf("branch %q has empty session id; should not produce a row: %v", r.BranchName, r)
			}
		}
	})

	t.Run("work-mode stack row carries mode=work and bare display label", func(t *testing.T) {
		var stackRow *agentSessionRow
		for i := range rows {
			if rows[i].Scope == "stack" && rows[i].StackHash == "abc1234" {
				stackRow = &rows[i]
				break
			}
		}
		if stackRow == nil {
			t.Fatal("expected a work-mode stack row")
		}
		if stackRow.Mode != config.AgentSessionWorkMode {
			t.Errorf("Mode = %q, want %q", stackRow.Mode, config.AgentSessionWorkMode)
		}
		if stackRow.DisplayName != "_ezstack-my-stack" {
			t.Errorf("DisplayName = %q, want _ezstack-my-stack", stackRow.DisplayName)
		}
	})

	t.Run("feature-mode stack row carries mode=feature and feature- display label", func(t *testing.T) {
		var featureRow *agentSessionRow
		for i := range rows {
			if rows[i].Scope == "stack" && rows[i].StackHash == "def5678" {
				featureRow = &rows[i]
				break
			}
		}
		if featureRow == nil {
			t.Fatal("expected a feature-mode stack row")
		}
		if featureRow.Mode != config.AgentSessionFeatureMode {
			t.Errorf("Mode = %q, want %q", featureRow.Mode, config.AgentSessionFeatureMode)
		}
		if featureRow.DisplayName != "_ezstack-feature-my-feature" {
			t.Errorf("DisplayName = %q, want _ezstack-feature-my-feature", featureRow.DisplayName)
		}
	})

	t.Run("branch-scoped row mode=work (branches can't be feature)", func(t *testing.T) {
		var branchRow *agentSessionRow
		for i := range rows {
			if rows[i].Scope == "branch" {
				branchRow = &rows[i]
				break
			}
		}
		if branchRow == nil {
			t.Fatal("expected a branch-scoped row")
		}
		if branchRow.Mode != config.AgentSessionWorkMode {
			t.Errorf("branch row Mode = %q, want %q", branchRow.Mode, config.AgentSessionWorkMode)
		}
	})

	t.Run("rows never carry repo_path or cd-prefixed resume_cmd", func(t *testing.T) {
		// `agent ls` is current-repo-only now: rows should never carry a
		// repo_path field, and resume_cmd never needs the cd-prefix.
		// Pinned here so a regression in the JSON shape or resume builder
		// surfaces immediately.
		for _, r := range rows {
			if strings.HasPrefix(r.ResumeCmd, "cd ") {
				t.Errorf("row %q has unexpected cd-prefix: %q", r.DisplayName, r.ResumeCmd)
			}
		}
	})
}

// TestCollectAgentSessions_NilStackConfig pins the defensive return: a
// nil StackConfig (which the loader can hand us if a repo entry is
// malformed) yields nil instead of panicking.
func TestCollectAgentSessions_NilStackConfig(t *testing.T) {
	rows := collectAgentSessionsFromStackConfig("/r", nil)
	if rows != nil {
		t.Errorf("expected nil rows for nil StackConfig, got %v", rows)
	}
}

// TestFilterRowsByBranch / ByStack / ByMode pin the three filter helpers
// behind the --branch / --stack / --feature flags. Each must return a
// non-nil empty slice on no-match so JSON output stays as `[]`.
func TestFilterRowsByBranch(t *testing.T) {
	rows := []agentSessionRow{
		{Scope: "branch", BranchName: "feat-x", SessionID: "x"},
		{Scope: "stack", StackHash: "abc", SessionID: "s"},
		{Scope: "branch", BranchName: "feat-y", SessionID: "y"},
	}
	got := filterRowsByBranch(rows, "feat-x")
	if len(got) != 1 || got[0].SessionID != "x" {
		t.Errorf("filterRowsByBranch(feat-x) = %+v, want single row x", got)
	}
	none := filterRowsByBranch(rows, "missing")
	if none == nil {
		t.Error("must return non-nil slice on no-match")
	}
	if len(none) != 0 {
		t.Errorf("expected empty slice, got %d rows", len(none))
	}
}

func TestFilterRowsByStack(t *testing.T) {
	rows := []agentSessionRow{
		{Scope: "stack", StackHash: "abc", SessionID: "s1"},
		{Scope: "branch", StackHash: "abc", BranchName: "feat-x", SessionID: "b1"},
		{Scope: "stack", StackHash: "def", SessionID: "s2"},
	}
	got := filterRowsByStack(rows, "abc")
	if len(got) != 2 {
		t.Errorf("expected 2 rows for stack abc, got %d: %+v", len(got), got)
	}
	none := filterRowsByStack(rows, "missing")
	if none == nil {
		t.Error("must return non-nil slice on no-match")
	}
	if len(none) != 0 {
		t.Errorf("expected empty slice, got %d", len(none))
	}
}

func TestFilterRowsByMode(t *testing.T) {
	rows := []agentSessionRow{
		{Scope: "stack", Mode: config.AgentSessionWorkMode, SessionID: "w"},
		{Scope: "stack", Mode: config.AgentSessionFeatureMode, SessionID: "f"},
		{Scope: "branch", Mode: config.AgentSessionWorkMode, SessionID: "bw"},
	}
	got := filterRowsByMode(rows, config.AgentSessionFeatureMode)
	if len(got) != 1 || got[0].SessionID != "f" {
		t.Errorf("filterRowsByMode(feature) = %+v, want single row f", got)
	}
	work := filterRowsByMode(rows, config.AgentSessionWorkMode)
	if len(work) != 2 {
		t.Errorf("filterRowsByMode(work) = %d rows, want 2", len(work))
	}
}

// TestFilterLabel verifies the human-readable description used in the
// empty-list message under each filter. Drift here makes the empty
// message lie about which filter is active.
func TestFilterLabel(t *testing.T) {
	cases := []struct {
		branch, stack, feature bool
		want                   string
	}{
		{false, false, false, ""},
		{true, false, false, "for the current branch"},
		{false, true, false, "for the current stack"},
		{false, false, true, "in feature mode"},
	}
	for _, c := range cases {
		if got := filterLabel(c.branch, c.stack, c.feature); got != c.want {
			t.Errorf("filterLabel(%v,%v,%v) = %q, want %q", c.branch, c.stack, c.feature, got, c.want)
		}
	}
}

// TestAgentSessionRow_JSONShape pins the JSON contract documented in the
// `agent ls --help` text. Renaming a field here is a breaking change for
// any user piping output through jq.
func TestAgentSessionRow_JSONShape(t *testing.T) {
	row := agentSessionRow{
		Scope:       "branch",
		Mode:        config.AgentSessionWorkMode,
		StackHash:   "abc1234",
		StackName:   "my-stack",
		BranchName:  "feat-x",
		DisplayName: "_ezstack-feat-x",
		SessionID:   "uuid-here",
		ResumeCmd:   "ezs agent --branch feat-x",
	}
	enc, err := encodeJSONForTest(row)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for _, want := range []string{
		`"scope":"branch"`,
		`"mode":"work"`,
		`"stack_hash":"abc1234"`,
		`"stack_name":"my-stack"`,
		`"branch_name":"feat-x"`,
		`"display_name":"_ezstack-feat-x"`,
		`"session_id":"uuid-here"`,
		`"resume_cmd":"ezs agent --branch feat-x"`,
	} {
		if !strings.Contains(enc, want) {
			t.Errorf("JSON missing %q; got:\n%s", want, enc)
		}
	}

	// `repo_path` was removed when cross-repo --all was dropped; pin the
	// removal so a re-introduction trips the test before users are
	// surprised.
	if strings.Contains(enc, `"repo_path"`) {
		t.Errorf("JSON should NOT contain repo_path (removed with --all); got:\n%s", enc)
	}

	// Optional fields drop out via omitempty — branch_name has no meaning
	// on a stack-scoped row, stack_name has no meaning on a hash-only stack,
	// and mode drops out only when explicitly set to "" (legacy hand-rolled
	// rows in tests; production rows always set Mode via modeOrDefault).
	stackOnly := agentSessionRow{
		Scope:       "stack",
		StackHash:   "abc1234",
		DisplayName: "_ezstack-abc1234",
		SessionID:   "uuid",
		ResumeCmd:   "ezs agent -s abc1234",
	}
	enc, err = encodeJSONForTest(stackOnly)
	if err != nil {
		t.Fatalf("encode stack-only: %v", err)
	}
	for _, banned := range []string{`"stack_name"`, `"branch_name"`, `"mode"`, `"repo_path"`} {
		if strings.Contains(enc, banned) {
			t.Errorf("stack-only row should omit %s; got:\n%s", banned, enc)
		}
	}
}
