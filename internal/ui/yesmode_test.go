package ui

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

// withYesMode flips YesMode for the duration of fn and restores it
// afterwards even if fn panics. Tests that mutate the package var must
// use this helper to avoid leaking state into other tests in the same
// `go test` run (the suite is sequential by default but a parallel
// invocation would still expose the leak).
func withYesMode(t *testing.T, fn func()) {
	t.Helper()
	old := YesMode
	YesMode = true
	defer func() { YesMode = old }()
	fn()
}

// captureStderr redirects os.Stderr to a pipe, runs fn, restores
// os.Stderr, and returns whatever fn wrote. The stderr output of
// YesMode prompts (the "→ value" trace) must appear so users running
// scripted runs can see what answer was auto-accepted; this helper
// makes that observable in tests.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() {
		os.Stderr = orig
	}()

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()
	w.Close()
	<-done
	return buf.String()
}

// TestPrompt_YesMode_ReturnsDefault asserts the headline production-grade
// guarantee for `Prompt`: under YesMode (MCP / -y) it returns the default
// value immediately rather than blocking on readline. Without the guard,
// `ezs-mcp ezstack_pr_create` with no `title` arg would hang forever
// waiting for keystrokes that no terminal will ever supply.
func TestPrompt_YesMode_ReturnsDefault(t *testing.T) {
	withYesMode(t, func() {
		var got string
		out := captureStderr(t, func() {
			got = Prompt("PR title", "feat/x")
		})
		if got != "feat/x" {
			t.Errorf("Prompt under YesMode = %q, want default %q", got, "feat/x")
		}
		if !strings.Contains(out, "PR title") {
			t.Errorf("expected stderr trace to mention prompt; got %q", out)
		}
		if !strings.Contains(out, "feat/x") {
			t.Errorf("expected stderr trace to show selected value; got %q", out)
		}
	})
}

// TestPrompt_YesMode_DoesNotBlock pins the regression: pre-fix, Prompt
// would call readline.NewEx → block forever on closed stdin. We assert
// the call returns within a tight bound.
func TestPrompt_YesMode_DoesNotBlock(t *testing.T) {
	withYesMode(t, func() {
		done := make(chan string, 1)
		go func() {
			done <- Prompt("anything?", "yes")
		}()
		select {
		case got := <-done:
			if got != "yes" {
				t.Errorf("got %q, want default %q", got, "yes")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Prompt under YesMode hung — regression of the MCP-hang bug")
		}
	})
}

// TestPromptPath_YesMode_ReturnsDefault: the same guarantee for the
// path-completion variant, which has its own readline session and is
// reachable from `ezs config set worktree_base_dir` and `ezs new`.
func TestPromptPath_YesMode_ReturnsDefault(t *testing.T) {
	withYesMode(t, func() {
		got := PromptPath("Worktree base dir", "/tmp/ezs")
		if got != "/tmp/ezs" {
			t.Errorf("PromptPath under YesMode = %q, want %q", got, "/tmp/ezs")
		}
	})
}

// TestPromptRequired_YesMode_ExitsCleanly verifies that
// PromptRequired — which has no defaultVal to fall back to — does NOT
// spin forever under YesMode. The spec is: print a clear diagnostic to
// stderr and exit 2. Caller running scripted needs the "this needed a
// value" signal to fix their invocation. We test by re-execing the
// current binary with an env flag that triggers a tiny program that
// calls PromptRequired under YesMode.
func TestPromptRequired_YesMode_ExitsCleanly(t *testing.T) {
	if os.Getenv("EZS_TEST_PROMPT_REQUIRED_YESMODE") == "1" {
		// Child mode: simulate the production path.
		YesMode = true
		PromptRequired("Enter branch name")
		// Should never reach here.
		os.Exit(0)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestPromptRequired_YesMode_ExitsCleanly")
	cmd.Env = append(os.Environ(), "EZS_TEST_PROMPT_REQUIRED_YESMODE=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit from PromptRequired under YesMode, got success")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exit error, got: %v", err)
	}
	if exitErr.ExitCode() != 2 {
		t.Errorf("exit code = %d, want 2", exitErr.ExitCode())
	}
	if !strings.Contains(stderr.String(), "Enter branch name") {
		t.Errorf("stderr should name the prompt; got: %q", stderr.String())
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "non-interactive") {
		t.Errorf("stderr should explain why; got: %q", stderr.String())
	}
}

// TestEditWithEditor_YesMode_ReturnsInitialContent: under YesMode the
// editor is skipped (no TTY for $EDITOR) and the unchanged initial
// content is returned. Pre-fix, `ezs pr create` (which runs
// `ConfirmTUI("Edit PR description?")` → returns true under YesMode →
// EditWithEditor) would launch vim against a non-existent terminal and
// hang.
func TestEditWithEditor_YesMode_ReturnsInitialContent(t *testing.T) {
	withYesMode(t, func() {
		initial := "# PR description\n\nDetails here.\n"
		got, err := EditWithEditor(initial, ".md")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// The function trims trailing whitespace from the returned content;
		// initial string ends with "\n" so the trimmed comparison is valid.
		want := strings.TrimSpace(initial)
		if got != want {
			t.Errorf("EditWithEditor under YesMode = %q, want %q", got, want)
		}
	})
}

// TestEditWithEditor_YesMode_DoesNotShellOut: belt-and-braces — make
// sure no `vim` / $EDITOR process is spawned. We point $EDITOR at a
// command that would fail loudly if invoked, and assert no error
// surfaces (the function must short-circuit).
func TestEditWithEditor_YesMode_DoesNotShellOut(t *testing.T) {
	t.Setenv("EDITOR", "/nonexistent-editor-binary")
	t.Setenv("VISUAL", "")
	withYesMode(t, func() {
		_, err := EditWithEditor("hello", ".md")
		if err != nil {
			t.Fatalf("EditWithEditor under YesMode tried to shell out: %v", err)
		}
	})
}

// TestPromptRequired_NotInYesMode_StillCallsBackend: regression guard —
// the YesMode branch must not change behavior when YesMode is false.
// The TerminalBackend would normally try readline; we install a fake
// backend that records the call and returns a fixed answer.
func TestPromptRequired_NotInYesMode_StillCallsBackend(t *testing.T) {
	old := activeBackend
	defer func() { activeBackend = old }()
	fb := &fakeBackend{TerminalBackend: TerminalBackend{}, promptRequiredAnswer: "hello"}
	activeBackend = fb

	got := PromptRequired("Enter something")
	if got != "hello" {
		t.Errorf("PromptRequired = %q, want %q (backend should be called when not in YesMode)", got, "hello")
	}
	if fb.promptRequiredCalls != 1 {
		t.Errorf("backend.PromptRequired calls = %d, want 1", fb.promptRequiredCalls)
	}
}

// fakeBackend lets us assert that the activeBackend is bypassed under
// YesMode for the prompt-family functions and used otherwise. Embeds
// TerminalBackend so we satisfy the full interface without rewriting
// every method.
type fakeBackend struct {
	TerminalBackend
	promptRequiredAnswer string
	promptRequiredCalls  int
}

func (f *fakeBackend) PromptRequired(prompt string) string {
	f.promptRequiredCalls++
	return f.promptRequiredAnswer
}

// runWithDeadline guards against test regressions where a Select* helper
// reaches its fzf invocation despite YesMode being on — `exec.Command("fzf",
// ...)` against a missing TTY would block indefinitely. The deadline is
// generous so a slow CI doesn't false-fail; the failure mode under regression
// is "blocks forever", so any reasonable bound catches it.
func runWithDeadline(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("Select* call did not complete within %s — YesMode guard regressed and the helper is blocked on fzf / TTY", d)
	}
}

// TestSelectBranchWithStacks_YesMode pins three contracts:
// (1) zero branches → not-found error, not a hang.
// (2) one branch → return it without prompting (callers that already
//
//	narrowed don't pay an interactive cost).
//
// (3) multiple branches → structured "multiple ... match" error naming the
//
//	candidates, so MCP / scripted callers can surface guidance.
//
// The deadline guard catches the regression mode (block on fzf with no TTY).
func TestSelectBranchWithStacks_YesMode(t *testing.T) {
	withYesMode(t, func() {
		runWithDeadline(t, 2*time.Second, func() {
			// (1) empty
			if _, err := SelectBranchWithStacks(nil, nil, "x"); err == nil || !strings.Contains(err.Error(), "no branches") {
				t.Errorf("empty branches: want 'no branches' err, got %v", err)
			}

			// (2) single
			only := &config.Branch{Name: "only"}
			got, err := SelectBranchWithStacks([]*config.Branch{only}, nil, "x")
			if err != nil {
				t.Fatalf("single branch: unexpected err %v", err)
			}
			if got != only {
				t.Errorf("single branch: got %v, want %v", got, only)
			}

			// (3) multiple → structured error
			a := &config.Branch{Name: "a"}
			b := &config.Branch{Name: "b"}
			_, err = SelectBranchWithStacks([]*config.Branch{a, b}, nil, "x")
			if err == nil {
				t.Fatal("multiple branches under YesMode: expected error, got nil")
			}
			if !strings.Contains(err.Error(), "multiple branches") {
				t.Errorf("multi err = %q, want 'multiple branches' substring", err)
			}
			if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
				t.Errorf("multi err must name candidates a & b: %q", err)
			}
		})
	})
}

// TestSelectBranch_YesMode pins that the public SelectBranch wrapper (which
// dispatches to TerminalBackend.SelectBranch → SelectBranchWithStacks) honors
// the same YesMode contract. Without this test, a future backend rewrite
// could route around the guard.
func TestSelectBranch_YesMode(t *testing.T) {
	withYesMode(t, func() {
		runWithDeadline(t, 2*time.Second, func() {
			only := &config.Branch{Name: "only"}
			got, err := SelectBranch([]*config.Branch{only}, "x")
			if err != nil || got != only {
				t.Errorf("SelectBranch(single): got (%v, %v), want (%v, nil)", got, err, only)
			}

			_, err = SelectBranch([]*config.Branch{{Name: "a"}, {Name: "b"}}, "x")
			if err == nil {
				t.Errorf("SelectBranch(multi) under YesMode: expected error, got nil")
			}
		})
	})
}

// TestSelectWorktree_YesMode mirrors the SelectBranch contract for
// SelectWorktree (the non-stack-preview variant). The stack-preview variant
// already had a guard before this change; the bare SelectWorktree was the
// regression path.
func TestSelectWorktree_YesMode(t *testing.T) {
	withYesMode(t, func() {
		runWithDeadline(t, 2*time.Second, func() {
			one := []WorktreeInfo{{Branch: "feat", Path: "/tmp/feat"}}
			got, err := SelectWorktree(one, "x")
			if err != nil {
				t.Fatalf("single worktree: unexpected err %v", err)
			}
			if got == nil || got.Branch != "feat" {
				t.Errorf("single worktree: got %+v, want branch=feat", got)
			}

			two := []WorktreeInfo{{Branch: "a"}, {Branch: "b"}}
			_, err = SelectWorktree(two, "x")
			if err == nil {
				t.Fatal("multi worktrees: expected error, got nil")
			}
			if !strings.Contains(err.Error(), "multiple worktrees") {
				t.Errorf("multi err = %q, want 'multiple worktrees' substring", err)
			}
		})
	})
}

// TestSelectStack_YesMode pins that the SelectStack helper either auto-picks
// a single stack or surfaces a structured error listing candidates — never
// blocks on fzf.
func TestSelectStack_YesMode(t *testing.T) {
	withYesMode(t, func() {
		runWithDeadline(t, 2*time.Second, func() {
			one := []*config.Stack{{Hash: "abc12345"}}
			got, err := SelectStack(one, "x")
			if err != nil || got != one[0] {
				t.Errorf("single stack: got (%v, %v), want (%v, nil)", got, err, one[0])
			}

			two := []*config.Stack{{Hash: "abc12345"}, {Hash: "def67890"}}
			_, err = SelectStack(two, "x")
			if err == nil {
				t.Fatal("multi stacks: expected error, got nil")
			}
			if !strings.Contains(err.Error(), "multiple stacks") {
				t.Errorf("multi err = %q, want 'multiple stacks' substring", err)
			}
		})
	})
}

// TestSelectOptionWithSuggested_YesMode pins three resolution cases in
// priority order: (a) suggested index in range → return it; (b) no suggestion
// but only one option → return 0; (c) ambiguous → structured error. The
// suggestion-first ordering matters: many ezstack code paths (e.g. parent
// pickers) compute a sensible default and fall back to interactive only when
// they can't.
func TestSelectOptionWithSuggested_YesMode(t *testing.T) {
	withYesMode(t, func() {
		runWithDeadline(t, 2*time.Second, func() {
			// (a) honor suggestion
			idx, err := SelectOptionWithSuggested([]string{"x", "y", "z"}, "p", 2)
			if err != nil || idx != 2 {
				t.Errorf("suggested=2: got (%d, %v), want (2, nil)", idx, err)
			}
			// (b) single option
			idx, err = SelectOptionWithSuggested([]string{"only"}, "p", -1)
			if err != nil || idx != 0 {
				t.Errorf("single option: got (%d, %v), want (0, nil)", idx, err)
			}
			// (c) ambiguous + no suggestion
			_, err = SelectOptionWithSuggested([]string{"a", "b"}, "p", -1)
			if err == nil || !strings.Contains(err.Error(), "multiple options") {
				t.Errorf("ambiguous: want 'multiple options' err, got %v", err)
			}
		})
	})
}

// TestSelectOptionWithBack_YesMode pins that the back-menu variant either
// auto-picks the lone option or errors loudly. Critically: this helper has
// no "suggested" parameter — the only safe auto-resolution is len==1, since
// silently picking option [0] under YesMode would let MCP scripts proceed
// past a menu they didn't actually consent to.
func TestSelectOptionWithBack_YesMode(t *testing.T) {
	withYesMode(t, func() {
		runWithDeadline(t, 2*time.Second, func() {
			idx, err := SelectOptionWithBack([]string{"only"}, "p")
			if err != nil || idx != 0 {
				t.Errorf("single: got (%d, %v), want (0, nil)", idx, err)
			}
			_, err = SelectOptionWithBack([]string{"a", "b", "c"}, "p")
			if err == nil || !strings.Contains(err.Error(), "multiple options") {
				t.Errorf("multi: want 'multiple options' err, got %v", err)
			}
		})
	})
}

// fakeMCPishBackend simulates a non-Terminal backend (MCPBackend in
// production). Each Select* method increments a counter and returns a
// deterministic answer — we only care whether the package-level wrapper
// dispatched to us at all.
type fakeMCPishBackend struct {
	TerminalBackend
	stackCalls     int
	optionWithBack int
	branchCalls    int
}

func (f *fakeMCPishBackend) SelectStack(stacks []*config.Stack, _ string) (*config.Stack, error) {
	f.stackCalls++
	if len(stacks) == 0 {
		return nil, nil
	}
	return stacks[0], nil
}

func (f *fakeMCPishBackend) SelectOptionWithBack(options []string, _ string) (int, error) {
	f.optionWithBack++
	if len(options) == 0 {
		return -1, nil
	}
	return 0, nil
}

func (f *fakeMCPishBackend) SelectBranch(branches []*config.Branch, _ string) (*config.Branch, error) {
	f.branchCalls++
	if len(branches) == 0 {
		return nil, nil
	}
	return branches[0], nil
}

// TestBackendDispatch_YesMode_PreservesElicitation pins the asymmetric design
// described in ui.go:445 (and the SelectOptionWithBack docstring): for
// helpers dispatched through the Backend interface (SelectStack,
// SelectOptionWithBack, SelectBranch), the YesMode guard must live on the
// TerminalBackend method, NOT the package-level wrapper. Otherwise an MCP
// invocation — which installs MCPBackend (uses elicitation, doesn't hang on
// missing TTY) AND flips YesMode=true — would never reach MCPBackend's
// elicitation code, breaking multi-stack disambiguation flows that depend
// on user input via the MCP client.
//
// We assert this by replacing activeBackend with a fake that records calls,
// flipping YesMode=true, and verifying the fake is invoked even when there
// are 2+ items (the case where TerminalBackend would short-circuit).
func TestBackendDispatch_YesMode_PreservesElicitation(t *testing.T) {
	old := activeBackend
	defer func() { activeBackend = old }()
	fb := &fakeMCPishBackend{}
	activeBackend = fb

	withYesMode(t, func() {
		runWithDeadline(t, 2*time.Second, func() {
			// SelectStack: 2+ stacks. With package-level guard this would
			// auto-error and never call the backend. We expect the fake's
			// SelectStack to be invoked once.
			stacks := []*config.Stack{{Hash: "abc12345"}, {Hash: "def67890"}}
			got, err := SelectStack(stacks, "x")
			if err != nil {
				t.Errorf("SelectStack(non-Terminal, multi): unexpected err %v", err)
			}
			if got != stacks[0] {
				t.Errorf("SelectStack: got %v, want stacks[0]", got)
			}
			if fb.stackCalls != 1 {
				t.Errorf("SelectStack: backend called %d times, want 1 (regression: package-level guard short-circuited the backend)", fb.stackCalls)
			}

			// Same for SelectOptionWithBack.
			idx, err := SelectOptionWithBack([]string{"a", "b", "c"}, "p")
			if err != nil || idx != 0 {
				t.Errorf("SelectOptionWithBack: got (%d, %v), want (0, nil)", idx, err)
			}
			if fb.optionWithBack != 1 {
				t.Errorf("SelectOptionWithBack: backend called %d times, want 1", fb.optionWithBack)
			}

			// SelectBranch dispatches to the backend, which on TerminalBackend
			// flows through SelectBranchWithStacks (package-level guard).
			// On a non-Terminal backend it must call SelectBranch directly.
			branches := []*config.Branch{{Name: "a"}, {Name: "b"}}
			b, err := SelectBranch(branches, "p")
			if err != nil {
				t.Errorf("SelectBranch: unexpected err %v", err)
			}
			if b != branches[0] {
				t.Errorf("SelectBranch: got %v, want branches[0]", b)
			}
			if fb.branchCalls != 1 {
				t.Errorf("SelectBranch: backend called %d times, want 1", fb.branchCalls)
			}
		})
	})
}
