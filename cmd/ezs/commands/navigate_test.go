package commands

import (
	"strings"
	"testing"
)

// parseNavigateArgs is the validation choke point for `ezs up`/`ezs down`.
// Bash had a long-running silent-drop bug where `ezs up 1 typo` ignored the
// extra arg and ran navigation anyway. These tests pin the validator against
// every shape we've seen abused: missing arg, multiple positional args, a
// flag-shaped token that the integer parser used to reject only because
// strconv.Atoi happened to fail. The flag-shaped case is the load-bearing
// one — it makes typos like `ezs up 2 --bogus` visible.

func TestParseNavigateArgs_DefaultsToOne(t *testing.T) {
	got, err := parseNavigateArgs("up", nil)
	if err != nil {
		t.Fatalf("nil args returned error: %v", err)
	}
	if got != 1 {
		t.Errorf("default = %d, want 1", got)
	}
}

func TestParseNavigateArgs_AcceptsPositiveInt(t *testing.T) {
	got, err := parseNavigateArgs("up", []string{"3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 3 {
		t.Errorf("got %d, want 3", got)
	}
}

func TestParseNavigateArgs_RejectsZeroAndNegative(t *testing.T) {
	for _, bad := range []string{"0", "-1", "-99"} {
		t.Run(bad, func(t *testing.T) {
			_, err := parseNavigateArgs("up", []string{bad})
			if err == nil {
				t.Errorf("expected error for %q", bad)
			}
			// Negative integers must surface as "invalid step count", NOT
			// "unknown flag" — the user clearly typed a number, even if a
			// nonsensical one. The integer-parse-first ordering exists to
			// give them the right diagnostic.
			if !strings.Contains(err.Error(), "invalid step count") {
				t.Errorf("error %q should say 'invalid step count' for %q", err.Error(), bad)
			}
		})
	}
}

func TestParseNavigateArgs_RejectsNonInteger(t *testing.T) {
	_, err := parseNavigateArgs("up", []string{"three"})
	if err == nil {
		t.Fatal("non-integer should be rejected")
	}
	if !strings.Contains(err.Error(), "invalid step count") {
		t.Errorf("error %q should mention 'invalid step count'", err.Error())
	}
}

// TestParseNavigateArgs_RejectsFlagShaped guards against the prior silent-
// drop bug: `ezs up 1 --bogus` looked successful because `--bogus` was
// dropped. Now any flag-shaped extra must surface as a flag error.
func TestParseNavigateArgs_RejectsFlagShaped(t *testing.T) {
	_, err := parseNavigateArgs("up", []string{"--bogus"})
	if err == nil {
		t.Fatal("flag-shaped arg should be rejected")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("error %q should mention 'unknown flag'", err.Error())
	}
}

func TestParseNavigateArgs_RejectsExtraPositional(t *testing.T) {
	_, err := parseNavigateArgs("down", []string{"1", "extra"})
	if err == nil {
		t.Fatal("extra positional should be rejected")
	}
	if !strings.Contains(err.Error(), "at most one argument") {
		t.Errorf("error %q should mention the cardinality constraint", err.Error())
	}
}

func TestParseNavigateArgs_RejectsExtraFlagAfterStep(t *testing.T) {
	// The exact case the user originally hit: `ezs up 1 --bogus`. Two
	// positional-shaped tokens — fail at the cardinality check. Whether
	// the second token starts with `-` or not, we still reject.
	_, err := parseNavigateArgs("up", []string{"1", "--bogus"})
	if err == nil {
		t.Fatal("step + bogus flag should be rejected")
	}
}

func TestParseNavigateArgs_DirectionAppearsInError(t *testing.T) {
	// The error message embeds direction so users can tell up vs down at a
	// glance. Keep the string discoverable.
	_, err := parseNavigateArgs("down", []string{"1", "2"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "down") {
		t.Errorf("error %q should embed direction 'down'", err.Error())
	}
}
