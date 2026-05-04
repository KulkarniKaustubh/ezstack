package commands

import (
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
)

// ── parseAIPRResponse ──────────────────────────────────────────────────────────

func TestParseAIPRResponse_HappyPath(t *testing.T) {
	raw := `{"title":"Add login","body":"## Summary\nAdds login flow."}`
	res, err := parseAIPRResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Title != "Add login" {
		t.Errorf("title = %q", res.Title)
	}
	if !strings.Contains(res.Body, "Adds login flow") {
		t.Errorf("body = %q", res.Body)
	}
}

func TestParseAIPRResponse_TolerateMarkdownFences(t *testing.T) {
	// The model sometimes wraps JSON in ```json ... ``` despite our instruction.
	// Our parser scans for the first balanced { } object, so the fence is
	// outside the scanned span and ignored.
	raw := "Sure! Here's the PR:\n\n```json\n{\"title\":\"Fix bug\",\"body\":\"Fixes #1.\"}\n```\n"
	res, err := parseAIPRResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Title != "Fix bug" {
		t.Errorf("title = %q", res.Title)
	}
}

func TestParseAIPRResponse_TolerateProseAround(t *testing.T) {
	raw := `I'll draft a PR for you:

{"title":"Refactor cache","body":"### Summary\nRefactors the cache layer."}

Let me know if you want changes.`
	res, err := parseAIPRResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Title != "Refactor cache" {
		t.Errorf("title = %q", res.Title)
	}
}

func TestParseAIPRResponse_HandlesBracesInsideStrings(t *testing.T) {
	// A close-brace inside a JSON string must not prematurely close the
	// outer object. This is the regression for naive single-pass scanning.
	raw := `{"title":"Use {brace} delimiters","body":"Body has } in it"}`
	res, err := parseAIPRResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Title, "{brace}") {
		t.Errorf("title = %q", res.Title)
	}
	if !strings.Contains(res.Body, "} in it") {
		t.Errorf("body = %q", res.Body)
	}
}

func TestParseAIPRResponse_EscapedQuotesInsideString(t *testing.T) {
	// JSON-escaped backslash-quote inside a string must not end the string
	// early — otherwise the scanner thinks it's outside-string and treats
	// later `}` as a closer.
	raw := `prose {"title":"a \"quoted\" thing","body":"text"} more prose`
	res, err := parseAIPRResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Title != `a "quoted" thing` {
		t.Errorf("title = %q", res.Title)
	}
}

func TestParseAIPRResponse_RejectsMissingFields(t *testing.T) {
	cases := []string{
		`{"title":"only title"}`,                  // missing body
		`{"body":"only body"}`,                    // missing title
		`{"title":"   ","body":"non-empty"}`,      // whitespace-only title
		`{"title":"non-empty","body":"   "}`,      // whitespace-only body
		`no json here at all`,                     // no object
		`{"title":"abc","body":"def"`,             // unclosed
	}
	for _, raw := range cases {
		if _, err := parseAIPRResponse(raw); err == nil {
			t.Errorf("expected error for %q", raw)
		}
	}
}

// ── findFirstJSONObject ────────────────────────────────────────────────────────

func TestFindFirstJSONObject_NestedAndStrings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"flat", `{"a":1}`, `{"a":1}`, true},
		{"nested", `{"a":{"b":2}}`, `{"a":{"b":2}}`, true},
		{"prefix prose", `prefix {"a":1} suffix`, `{"a":1}`, true},
		{"close in string", `{"x":"}"}`, `{"x":"}"}`, true},
		{"escaped quote in string", `{"x":"\""}`, `{"x":"\""}`, true},
		{"no object", `no braces`, ``, false},
		{"unbalanced", `{"a":1`, ``, false},
	}
	for _, c := range cases {
		got, ok := findFirstJSONObject(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got (%q,%v), want (%q,%v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

// ── buildAIPRPrompt ────────────────────────────────────────────────────────────

func TestBuildAIPRPrompt_Substitutes(t *testing.T) {
	commits := []git.Commit{
		{Hash: "abcdef0123456", Subject: "Add auth"},
		{Hash: "fedcba", Subject: "Fix typo"},
	}
	prompt := buildAIPRPrompt("feat-auth", "main", "diff content here", commits, "## Summary\n\n## Test plan\n")

	for _, want := range []string{
		"feat-auth",
		"main",
		"diff content here",
		"abcdef0 Add auth",  // 7-char short hash + subject
		"fedcba Fix typo",   // hash <= 7 chars passed through
		"## Summary",
		"## Test plan",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildAIPRPrompt_NoTemplateUsesFallbackInstruction(t *testing.T) {
	prompt := buildAIPRPrompt("feat", "main", "diff", nil, "")
	if !strings.Contains(prompt, "no PR template found") {
		t.Errorf("expected fallback instruction when template is empty; prompt=%q", prompt)
	}
}

func TestBuildAIPRPrompt_TruncatesLargeDiff(t *testing.T) {
	huge := strings.Repeat("x", aiPRDiffMaxBytes+1024)
	prompt := buildAIPRPrompt("feat", "main", huge, nil, "")
	if !strings.Contains(prompt, "[diff truncated") {
		t.Errorf("expected truncation marker for diff > %d bytes", aiPRDiffMaxBytes)
	}
	// The prompt should contain only the truncated diff plus some surrounding
	// scaffolding, never the full huge diff.
	if len(prompt) > aiPRDiffMaxBytes+8192 {
		t.Errorf("prompt too large: %d bytes (expected ~%d + scaffold)", len(prompt), aiPRDiffMaxBytes)
	}
}

func TestBuildAIPRPrompt_NoCommitsGracefully(t *testing.T) {
	prompt := buildAIPRPrompt("feat", "main", "diff", nil, "tmpl")
	if !strings.Contains(prompt, "(none)") {
		t.Errorf("expected '(none)' for empty commits, got prompt=%q", prompt)
	}
}

// ── newPRGeneratorForAgent ─────────────────────────────────────────────────────

func TestNewPRGeneratorForAgent_RejectsNonClaude(t *testing.T) {
	_, err := newPRGeneratorForAgent("aider --model gpt-4", "/tmp")
	if err == nil {
		t.Fatal("expected error for non-claude agent")
	}
	if !strings.Contains(err.Error(), "Claude-family") {
		t.Errorf("error should mention Claude-family; got %v", err)
	}
}

// ── truncateForError ───────────────────────────────────────────────────────────

func TestTruncateForError(t *testing.T) {
	if got := truncateForError("short", 100); got != "short" {
		t.Errorf("short string passthrough: %q", got)
	}
	if got := truncateForError(strings.Repeat("a", 50), 10); got != "aaaaaaaaaa…" {
		t.Errorf("long-string truncation = %q", got)
	}
	if got := truncateForError("  spaced  ", 10); got != "spaced" {
		t.Errorf("trim then short-circuit = %q", got)
	}
}
