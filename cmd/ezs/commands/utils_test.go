package commands

import "testing"

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
