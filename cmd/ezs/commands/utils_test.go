package commands

import (
	"io"
	"os"
	"testing"
)

// silenceStdout swallows stdout for the duration of fn. HasExamplesFlag calls
// PrintExamples which writes to stdout; tests that exercise the hit path don't
// want that noise cluttering `go test` output.
func silenceStdout(t *testing.T, fn func()) {
	t.Helper()
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, r); close(done) }()
	defer func() {
		w.Close()
		<-done
		os.Stdout = orig
	}()
	fn()
}

func TestHasExamplesFlag_DirectMatch(t *testing.T) {
	var got bool
	silenceStdout(t, func() {
		got = HasExamplesFlag("commit", []string{"--examples"})
	})
	if !got {
		t.Error("expected --examples to be detected")
	}
}

func TestHasExamplesFlag_ConsumedAsMessageValue(t *testing.T) {
	// `ezs commit -m "--examples"` must commit, not print help.
	// Regression guard for the naive "contains --examples" check.
	if HasExamplesFlag("commit", []string{"-m", "--examples"}) {
		t.Error("--examples consumed as -m value must not trigger help")
	}
	if HasExamplesFlag("commit", []string{"--message", "--examples"}) {
		t.Error("--examples consumed as --message value must not trigger help")
	}
}

func TestHasExamplesFlag_AfterEqualsForm(t *testing.T) {
	// --message=foo does NOT consume the next arg, so a following --examples is real.
	var got bool
	silenceStdout(t, func() {
		got = HasExamplesFlag("commit", []string{"--message=foo", "--examples"})
	})
	if !got {
		t.Error("--examples after --message=foo should be detected")
	}
}

func TestHasExamplesFlag_MixedFlags(t *testing.T) {
	// --preset consumes its value; --examples afterward is real.
	var got bool
	silenceStdout(t, func() {
		got = HasExamplesFlag("agent", []string{"--preset", "thorough", "--examples"})
	})
	if !got {
		t.Error("--examples after --preset value should be detected")
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"/path/to/dir", "'/path/to/dir'"},
		{"/path/with spaces/dir", "'/path/with spaces/dir'"},
		{"/path/with'quote", "'/path/with'\\''quote'"},
		{"", "''"},
		{"$(rm -rf /)", "'$(rm -rf /)'"},
		{"`whoami`", "'`whoami`'"},
		{"a;b", "'a;b'"},
		{"a\nb", "'a\nb'"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ShellQuote(tt.input)
			if got != tt.want {
				t.Errorf("ShellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
