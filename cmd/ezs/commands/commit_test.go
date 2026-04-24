package commands

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseCommitArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErr     string
		wantFlags   commitFlags
		wantPassage []string
	}{
		{
			name: "no flags",
			args: []string{"-m", "hello"},
			wantFlags: commitFlags{
				gitPassthrough: []string{"-m", "hello"},
			},
			wantPassage: []string{"-m", "hello"},
		},
		{
			name: "merge override stripped",
			args: []string{"--merge", "-m", "x"},
			wantFlags: commitFlags{
				mergeOverride:  true,
				gitPassthrough: []string{"-m", "x"},
			},
			wantPassage: []string{"-m", "x"},
		},
		{
			name: "rebase override stripped",
			args: []string{"-m", "x", "--rebase"},
			wantFlags: commitFlags{
				rebaseOverride: true,
				gitPassthrough: []string{"-m", "x"},
			},
			wantPassage: []string{"-m", "x"},
		},
		{
			name:    "merge + rebase errors",
			args:    []string{"--merge", "--rebase"},
			wantErr: "both --merge and --rebase",
		},
		{
			name: "push flag",
			args: []string{"-m", "hi", "--push"},
			wantFlags: commitFlags{
				pushFlag:       true,
				gitPassthrough: []string{"-m", "hi"},
			},
			wantPassage: []string{"-m", "hi"},
		},
		{
			name: "push-children implies push",
			args: []string{"--push-children"},
			wantFlags: commitFlags{
				pushFlag:         true,
				pushChildrenFlag: true,
			},
		},
		{
			name: "no-push",
			args: []string{"--no-push", "-m", "x"},
			wantFlags: commitFlags{
				noPushFlag:     true,
				gitPassthrough: []string{"-m", "x"},
			},
			wantPassage: []string{"-m", "x"},
		},
		{
			name:    "no-push conflicts with push",
			args:    []string{"--push", "--no-push"},
			wantErr: "cannot combine --no-push",
		},
		{
			name:    "no-push conflicts with push-children",
			args:    []string{"--push-children", "--no-push"},
			wantErr: "cannot combine --no-push",
		},
		{
			name: "commit message containing our flag name is preserved",
			args: []string{"-m", "--push", "--push"},
			// -m takes the next arg as value, so "--push" after -m is the
			// commit message, not our flag. The standalone --push at the end
			// IS our flag.
			wantFlags: commitFlags{
				pushFlag:       true,
				gitPassthrough: []string{"-m", "--push"},
			},
			wantPassage: []string{"-m", "--push"},
		},
		{
			name: "commit message containing --no-push is preserved",
			args: []string{"--message", "--no-push"},
			wantFlags: commitFlags{
				gitPassthrough: []string{"--message", "--no-push"},
			},
			wantPassage: []string{"--message", "--no-push"},
		},
		{
			name: "passthrough arbitrary git flags",
			args: []string{"-a", "--signoff", "-m", "hi"},
			wantFlags: commitFlags{
				gitPassthrough: []string{"-a", "--signoff", "-m", "hi"},
			},
			wantPassage: []string{"-a", "--signoff", "-m", "hi"},
		},
		{
			name: "full kitchen sink",
			args: []string{"--rebase", "--push-children", "-m", "msg"},
			wantFlags: commitFlags{
				rebaseOverride:   true,
				pushFlag:         true,
				pushChildrenFlag: true,
				gitPassthrough:   []string{"-m", "msg"},
			},
			wantPassage: []string{"-m", "msg"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCommitArgs(tc.args)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want to contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.mergeOverride != tc.wantFlags.mergeOverride ||
				got.rebaseOverride != tc.wantFlags.rebaseOverride ||
				got.pushFlag != tc.wantFlags.pushFlag ||
				got.pushChildrenFlag != tc.wantFlags.pushChildrenFlag ||
				got.noPushFlag != tc.wantFlags.noPushFlag {
				t.Errorf("flags mismatch:\n got %+v\nwant %+v", got, tc.wantFlags)
			}
			if !reflect.DeepEqual(got.gitPassthrough, tc.wantFlags.gitPassthrough) {
				t.Errorf("gitPassthrough = %v, want %v", got.gitPassthrough, tc.wantFlags.gitPassthrough)
			}
		})
	}
}
