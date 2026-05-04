package commands

import (
	"encoding/json"
	"strings"
	"testing"
)

func encodeJSONForTest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// TestQuoteIfNeeded covers the resume-command quoter used by `agent ls` so
// stack/branch names containing whitespace produce copy-pasteable shell
// commands. We don't try to be comprehensive about every shell metachar;
// we just lean on ShellQuote (which uses single quotes and escapes
// embedded ones) any time we see whitespace or quoting characters.
func TestQuoteIfNeeded(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"plain":             "plain",
		"already-safe":      "already-safe",
		"with space":        "'with space'",
		"with\ttab":         "'with\ttab'",
		"O'Brien":           "'O'\\''Brien'", // ShellQuote escapes embedded single quote
		"has\"quote":        "'has\"quote'",
		"$variable":         "'$variable'",
		"`backtick`":        "'`backtick`'",
		"normal-stuff_v1.2": "normal-stuff_v1.2",
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

// TestSortAgentSessions verifies the deterministic ordering: stacks first
// (alphabetical by display name), branches after. Without a stable order
// the table flips around between runs and `agent ls --json` diffs become
// useless.
func TestSortAgentSessions(t *testing.T) {
	rows := []agentSessionRow{
		{Scope: "branch", DisplayName: "_ezstack-zeta", BranchName: "zeta"},
		{Scope: "stack", DisplayName: "_ezstack-bravo", StackHash: "b"},
		{Scope: "branch", DisplayName: "_ezstack-alpha", BranchName: "alpha"},
		{Scope: "stack", DisplayName: "_ezstack-alpha", StackHash: "a"},
	}
	// Re-run the same sort the production code uses (collectAgentSessions
	// applies it; we replicate via the same comparator here).
	for i := 0; i < len(rows)-1; i++ {
		for j := i + 1; j < len(rows); j++ {
			if !lessAgentRow(rows[i], rows[j]) {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
	want := []string{
		"stack:_ezstack-alpha",
		"stack:_ezstack-bravo",
		"branch:_ezstack-alpha",
		"branch:_ezstack-zeta",
	}
	for i, r := range rows {
		got := r.Scope + ":" + r.DisplayName
		if got != want[i] {
			t.Errorf("position %d: got %s, want %s", i, got, want[i])
		}
	}
}

// lessAgentRow mirrors the comparator inside collectAgentSessions. Lifted
// into a test helper to avoid spinning up a full repo just to assert order.
func lessAgentRow(a, b agentSessionRow) bool {
	if a.Scope != b.Scope {
		return a.Scope == "stack"
	}
	return a.DisplayName < b.DisplayName
}

// TestAgentSessionRow_JSONShape pins the JSON contract documented in the
// `agent ls --help` text. Renaming a field here is a breaking change for
// any user piping output through jq.
func TestAgentSessionRow_JSONShape(t *testing.T) {
	row := agentSessionRow{
		Scope:       "branch",
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

	// Optional fields must drop out via omitempty — branch_name has no
	// meaning on a stack-scoped row and stack_name has no meaning on a
	// hash-only stack.
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
	for _, banned := range []string{`"stack_name"`, `"branch_name"`} {
		if strings.Contains(enc, banned) {
			t.Errorf("stack-only row should omit %s; got:\n%s", banned, enc)
		}
	}
}
