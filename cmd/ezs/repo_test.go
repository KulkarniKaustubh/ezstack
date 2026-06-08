package main

import (
	"bytes"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestResolveRepoOverride(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		env      string
		wantRest []string
		wantPath string
		wantSrc  string
		wantErr  bool
	}{
		{
			name:     "flag space form",
			args:     []string{"--repo", "/x", "sync"},
			wantRest: []string{"sync"},
			wantPath: "/x",
			wantSrc:  "--repo",
		},
		{
			name:     "flag equals form keeps other tokens",
			args:     []string{"--repo=/x", "sync", "-y"},
			wantRest: []string{"sync", "-y"},
			wantPath: "/x",
			wantSrc:  "--repo",
		},
		{
			name:     "flag in non-leading position",
			args:     []string{"sync", "--repo", "/x", "--all"},
			wantRest: []string{"sync", "--all"},
			wantPath: "/x",
			wantSrc:  "--repo",
		},
		{
			name:     "env fallback when no flag",
			args:     []string{"sync"},
			env:      "/e",
			wantRest: []string{"sync"},
			wantPath: "/e",
			wantSrc:  "EZSTACK_REPO",
		},
		{
			name:     "flag beats env",
			args:     []string{"--repo", "/x"},
			env:      "/e",
			wantRest: []string{},
			wantPath: "/x",
			wantSrc:  "--repo",
		},
		{
			name:     "no override",
			args:     []string{"sync"},
			wantRest: []string{"sync"},
			wantPath: "",
			wantSrc:  "",
		},
		{
			name:     "last occurrence wins",
			args:     []string{"--repo", "/a", "--repo", "/b"},
			wantRest: []string{},
			wantPath: "/b",
			wantSrc:  "--repo",
		},
		{
			name:    "missing value is an error",
			args:    []string{"sync", "--repo"},
			wantErr: true,
		},
		{
			name:    "empty equals value is an error",
			args:    []string{"--repo="},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rest, path, src, err := resolveRepoOverride(tt.args, tt.env)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (rest=%v path=%q src=%q)", rest, path, src)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(rest, tt.wantRest) {
				t.Errorf("rest = %v, want %v", rest, tt.wantRest)
			}
			if path != tt.wantPath {
				t.Errorf("path = %q, want %q", path, tt.wantPath)
			}
			if src != tt.wantSrc {
				t.Errorf("src = %q, want %q", src, tt.wantSrc)
			}
		})
	}
}

func TestRepoSourceLabel(t *testing.T) {
	if got := repoSourceLabel("--repo", "/x"); got != "--repo /x" {
		t.Errorf("flag label = %q, want %q", got, "--repo /x")
	}
	if got := repoSourceLabel("EZSTACK_REPO", "/x"); got != "EZSTACK_REPO=/x" {
		t.Errorf("env label = %q, want %q", got, "EZSTACK_REPO=/x")
	}
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	w.Close()
	os.Stdout = orig
	return <-done
}

// TestShellInitSkipsLeadingRepoFlag guards the wrapper edge case: a leading
// --repo must not stop navigation commands' "cd <path>" output from being
// eval'd. The generated function resolves the command word past --repo /
// --repo= / -y before dispatching, so the emitted script must contain that
// skip logic.
func TestShellInitSkipsLeadingRepoFlag(t *testing.T) {
	out := captureStdout(t, printShellInit)
	for _, want := range []string{"--repo) _ezs_skip=1", "--repo=*|-y|--yes)", "_ezs_cmd"} {
		if !strings.Contains(out, want) {
			t.Errorf("shell-init output missing %q\n---\n%s", want, out)
		}
	}
}

func TestUsageDocumentsRepoFlag(t *testing.T) {
	out := captureStdout(t, printUsage)
	if !strings.Contains(out, "--repo") {
		t.Errorf("usage missing --repo option\n%s", out)
	}
	if !strings.Contains(out, "EZSTACK_REPO") {
		t.Errorf("usage missing EZSTACK_REPO mention\n%s", out)
	}
}
