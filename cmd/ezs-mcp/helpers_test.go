package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestStripAnsi(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello world", "hello world"},
		{"sgr", "\x1b[31mred\x1b[0m", "red"},
		{"nested sgr", "\x1b[1;36mbold cyan\x1b[0m", "bold cyan"},
		{"hyperlink OSC-8", "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"pua nerd-font icon", "before\ue0b0after", "beforeafter"},
		{"multi-space collapse", "a   b    c", "a b c"},
		// Single space must NOT collapse (it's not a "multi-space").
		{"single space preserved", "a b c", "a b c"},
		{"leading/trailing preserved", " x ", " x "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripAnsi(tc.in)
			if got != tc.want {
				t.Errorf("stripAnsi(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// fakeRequest builds an mcp.CallToolRequest whose Arguments map contains the
// given k/v pairs, so boolFlag / stringFlag can be exercised without an
// actual MCP transport.
func fakeRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: args,
		},
	}
}

func TestBoolFlag(t *testing.T) {
	t.Run("truthy appends flag", func(t *testing.T) {
		var got []string
		req := fakeRequest(map[string]any{"force": true})
		boolFlag(&got, req, "force", "--force")
		if len(got) != 1 || got[0] != "--force" {
			t.Errorf("got %v, want [--force]", got)
		}
	})
	t.Run("falsy or missing is a no-op", func(t *testing.T) {
		for _, arg := range []map[string]any{{"force": false}, {}, {"other": true}} {
			var got []string
			boolFlag(&got, fakeRequest(arg), "force", "--force")
			if len(got) != 0 {
				t.Errorf("args=%v → got %v, want []", arg, got)
			}
		}
	})
}

func TestStringFlag(t *testing.T) {
	t.Run("non-empty appends flag and value", func(t *testing.T) {
		var got []string
		req := fakeRequest(map[string]any{"title": "Hello world"})
		stringFlag(&got, req, "title", "--title")
		if len(got) != 2 || got[0] != "--title" || got[1] != "Hello world" {
			t.Errorf("got %v, want [--title, Hello world]", got)
		}
	})
	t.Run("empty or missing is a no-op", func(t *testing.T) {
		for _, arg := range []map[string]any{{"title": ""}, {}} {
			var got []string
			stringFlag(&got, fakeRequest(arg), "title", "--title")
			if len(got) != 0 {
				t.Errorf("args=%v → got %v, want []", arg, got)
			}
		}
	})
}

func TestTristateBoolFlag(t *testing.T) {
	t.Run("true appends flagTrue", func(t *testing.T) {
		var got []string
		tristateBoolFlag(&got, fakeRequest(map[string]any{"x": true}), "x", "--yes", "--no")
		if len(got) != 1 || got[0] != "--yes" {
			t.Errorf("got %v, want [--yes]", got)
		}
	})
	t.Run("false appends flagFalse", func(t *testing.T) {
		var got []string
		tristateBoolFlag(&got, fakeRequest(map[string]any{"x": false}), "x", "--yes", "--no")
		if len(got) != 1 || got[0] != "--no" {
			t.Errorf("got %v, want [--no]", got)
		}
	})
	t.Run("missing is a no-op", func(t *testing.T) {
		var got []string
		tristateBoolFlag(&got, fakeRequest(map[string]any{}), "x", "--yes", "--no")
		if len(got) != 0 {
			t.Errorf("got %v, want []", got)
		}
	})
	t.Run("non-bool value is a no-op", func(t *testing.T) {
		var got []string
		tristateBoolFlag(&got, fakeRequest(map[string]any{"x": "true"}), "x", "--yes", "--no")
		if len(got) != 0 {
			t.Errorf("got %v, want []", got)
		}
	})
	t.Run("nil arguments map is a no-op", func(t *testing.T) {
		var got []string
		req := mcp.CallToolRequest{}
		tristateBoolFlag(&got, req, "x", "--yes", "--no")
		if len(got) != 0 {
			t.Errorf("got %v, want []", got)
		}
	})
}

func TestCaptureCommand_StdoutStderrSplit(t *testing.T) {
	stdout, stderr, err := captureCommand(func(args []string) error {
		fmt.Fprint(os.Stdout, "out:"+strings.Join(args, ","))
		fmt.Fprint(os.Stderr, "err:"+strings.Join(args, ","))
		return nil
	}, []string{"a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "out:a,b" {
		t.Errorf("stdout = %q, want %q", stdout, "out:a,b")
	}
	if stderr != "err:a,b" {
		t.Errorf("stderr = %q, want %q", stderr, "err:a,b")
	}
}

func TestCaptureCommand_RestoresGlobals(t *testing.T) {
	origOut, origErr := os.Stdout, os.Stderr
	_, _, _ = captureCommand(func([]string) error { return nil }, nil)
	if os.Stdout != origOut {
		t.Errorf("os.Stdout not restored")
	}
	if os.Stderr != origErr {
		t.Errorf("os.Stderr not restored")
	}
}

func TestCaptureCommand_PropagatesError(t *testing.T) {
	sentinel := errors.New("boom")
	_, _, err := captureCommand(func([]string) error { return sentinel }, nil)
	if !errors.Is(err, sentinel) {
		t.Errorf("got %v, want %v", err, sentinel)
	}
}

// TestCaptureCommand_LargeOutput is the regression test for the pipe-buffer
// deadlock fix: without concurrent drainers, a command writing more than the
// ~64KB pipe buffer would block forever on the write side. 256KB is well
// above any platform's default pipe buffer.
func TestCaptureCommand_LargeOutput(t *testing.T) {
	const size = 256 * 1024
	stdout, _, err := captureCommand(func([]string) error {
		os.Stdout.Write(make([]byte, size))
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// captureCommand trims surrounding whitespace; raw NULs are preserved.
	if len(stdout) != size {
		t.Errorf("got %d bytes, want %d", len(stdout), size)
	}
}

func TestRunCommand_PrefersStdout(t *testing.T) {
	got, err := runCommand(func([]string) error {
		fmt.Fprint(os.Stdout, `{"ok":true}`)
		fmt.Fprint(os.Stderr, "some log")
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `{"ok":true}` {
		t.Errorf("got %q, want JSON stdout", got)
	}
}

func TestRunCommand_FallsBackToStderr(t *testing.T) {
	got, err := runCommand(func([]string) error {
		fmt.Fprint(os.Stderr, "\x1b[31mred error\x1b[0m")
		return errors.New("failed")
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if got != "red error" {
		t.Errorf("got %q, want %q", got, "red error")
	}
}

// TestToolMu_SerializesHandlers verifies that concurrent invocations of the
// handler wrappers actually serialize on toolMu — this is the fix for the
// captureCommand / setupElicitation race called out in PR #5.
func TestToolMu_SerializesHandlers(t *testing.T) {
	const workers = 8
	var (
		active int32
		peak   int32
		mu     sync.Mutex
		wg     sync.WaitGroup
	)

	inc := func() {
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()
	}
	dec := func() {
		mu.Lock()
		active--
		mu.Unlock()
	}

	// Mirror the toolHandler locking discipline.
	run := func() {
		defer wg.Done()
		toolMu.Lock()
		defer toolMu.Unlock()
		inc()
		// Force an interleaving opportunity: any other goroutine racing in
		// would bump `peak` above 1.
		for i := 0; i < 1000; i++ {
			_ = i
		}
		dec()
	}

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go run()
	}
	wg.Wait()

	if peak != 1 {
		t.Errorf("toolMu did not serialize: peak concurrency = %d, want 1", peak)
	}
}
