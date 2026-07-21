package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/version"
)

// matchingVersionStub is a shell ezs-mcp stub that reports the version the
// running ezs binary expects — i.e. no skew, no reinstall. It uses the same
// "ezstack-mcp version X.Y.Z" shape the real binary prints (see
// cmd/ezs-mcp/main.go) so the version-parser path under test matches what
// production sees, not a synthetic bare-semver output.
func matchingVersionStub() string {
	return `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "-v" ]; then
  echo "ezstack-mcp version ` + version.Version + `"
  exit 0
fi
exit 0
`
}

// matchingVersionStubLegacyBareFormat is a stub that emulates an older
// ezs-mcp build which printed only the bare semver. parseMCPVersionOutput
// must still recognize this format so users of pre-banner builds aren't
// reinstalled-on-every-run when they upgrade ezs but not ezs-mcp.
func matchingVersionStubLegacyBareFormat() string {
	return `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "-v" ]; then
  echo "` + version.Version + `"
  exit 0
fi
exit 0
`
}

// ── Pure-function tests ────────────────────────────────────────────────────────

// TestParseMCPVersionOutput pins the shapes parseMCPVersionOutput must accept.
// This is the parser that, when broken, made `ezs agent` reinstall the MCP
// binary on every single run — the production output `"ezstack-mcp version
// X.Y.Z"` never equaled the bare `version.Version` constant. We pin both the
// current banner shape AND the legacy bare-version shape (older ezs-mcp builds)
// AND a future-tolerant shape with trailing platform metadata, so any one of
// them changing the banner without updating the parser fails this test
// instead of silently degrading every agent launch.
func TestParseMCPVersionOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"banner format (current production)", "ezstack-mcp version 4.8.3", "4.8.3"},
		{"banner with trailing newline", "ezstack-mcp version 4.8.3\n", "4.8.3"},
		{"bare semver (legacy ezs-mcp builds)", "4.8.3", "4.8.3"},
		{"v-prefixed bare", "v4.8.3", "4.8.3"},
		{"v-prefixed banner", "ezstack-mcp version v4.8.3", "4.8.3"},
		{"banner with platform suffix (future-proof)", "ezstack-mcp version 4.8.3 (linux/amd64)", "4.8.3"},
		{"prerelease suffix", "ezstack-mcp version 4.8.3-rc1", "4.8.3-rc1"},
		{"build metadata", "ezstack-mcp version 4.8.3+abc1234", "4.8.3+abc1234"},
		{"surrounding whitespace", "  ezstack-mcp version 4.8.3  \n", "4.8.3"},
		{"empty output", "", ""},
		{"non-semver garbage", "wat is this", ""},
		{"two-part version is not semver", "ezstack-mcp version 4.8", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseMCPVersionOutput(c.in); got != c.want {
				t.Errorf("parseMCPVersionOutput(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestDocsReferenceFor(t *testing.T) {
	if got := docsReferenceFor(true); got != mcpDocsStub {
		t.Errorf("docsReferenceFor(true) should return the MCP stub, got %q", got[:min(80, len(got))])
	}
	if got := docsReferenceFor(false); got != ezsDocsReference {
		t.Errorf("docsReferenceFor(false) should return the full embedded docs")
	}
}

func TestBuildRenderedFeaturePromptUsesMCPStub(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EZSTACK_HOME", tmpDir)

	prompt, err := buildRenderedFeaturePrompt("/path/to/repo", "Add feature", nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "ezstack MCP server is registered") {
		t.Error("expected MCP stub to appear in rendered feature prompt")
	}
	// Should NOT contain a signature line from DOCUMENTATION.md.
	// Pick a stable heading that's unlikely to appear in the stub.
	if strings.Contains(prompt, "# ezstack Documentation") {
		t.Error("feature prompt with useMCP=true should not contain the full embedded DOCUMENTATION.md")
	}
}

func TestBuildRenderedWorkPromptUsesMCPStub(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EZSTACK_HOME", tmpDir)

	ctx := &agentContext{
		branchName:   "feature-auth",
		parentName:   "main",
		worktreePath: "/path/to/worktree",
		stackJSON:    `{"hash":"abc1234","root":"main","branches":[]}`,
		hasStack:     true,
		branchScoped: true,
		useMCP:       true,
		repoPath:     "/path/to/repo",
	}
	prompt, err := buildRenderedWorkPrompt(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "ezstack MCP server is registered") {
		t.Error("expected MCP stub in rendered work prompt when useMCP=true")
	}
	if strings.Contains(prompt, "# ezstack Documentation") {
		t.Error("work prompt with useMCP=true should not contain full embedded docs")
	}
}

func TestBuildRenderedWorkPromptWithoutMCPHasFullDocs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EZSTACK_HOME", tmpDir)

	ctx := &agentContext{
		branchName:   "feature-auth",
		parentName:   "main",
		worktreePath: "/path/to/worktree",
		stackJSON:    `{"hash":"abc1234","root":"main","branches":[]}`,
		hasStack:     true,
		branchScoped: true,
		useMCP:       false,
		repoPath:     "/path/to/repo",
	}
	prompt, err := buildRenderedWorkPrompt(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(prompt, "ezstack MCP server is registered") {
		t.Error("useMCP=false should not inject the MCP stub")
	}
	// Should contain at least some content from embedded docs — pick a substring
	// that is reasonably stable across edits of DOCUMENTATION.md.
	if !strings.Contains(prompt, "ezstack") {
		t.Error("useMCP=false should include embedded docs content")
	}
}

// ── ensureEzstackMCP behaviour tests ───────────────────────────────────────────
//
// These tests stub `claude` and `ezs-mcp` on PATH with shell scripts so we can
// drive ensureEzstackMCP through its branches without spawning real binaries.

// writeStubBin writes an executable shell script to dir/name. The script's body
// is whatever is passed in body (a full bash script including shebang).
func writeStubBin(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0755); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
}

// isolateStubPath makes `dir` the only entry on PATH for the duration of the
// test. This prevents a real `claude` or `ezs-mcp` on the dev machine from
// influencing results, and prevents tests from accidentally launching the
// user's real agent CLI.
func isolateStubPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir)
}

func TestEnsureEzstackMCP_SkipWhenNotClaude(t *testing.T) {
	if got := ensureEzstackMCP("cursor", false, false); got {
		t.Error("non-claude agent should return false")
	}
}

func TestEnsureEzstackMCP_SkipWhenSkipTrue(t *testing.T) {
	if got := ensureEzstackMCP("claude", true, false); got {
		t.Error("skip=true should short-circuit to false")
	}
}

func TestEnsureEzstackMCP_NoClaudeInPath(t *testing.T) {
	dir := t.TempDir()
	isolateStubPath(t, dir)
	// no claude stub written
	if got := ensureEzstackMCP("claude", false, false); got {
		t.Error("missing claude on PATH should return false")
	}
}

func TestEnsureEzstackMCP_AlreadyRegistered(t *testing.T) {
	dir := t.TempDir()

	// claude: `claude mcp get ezstack` exits 0 (already registered), anything
	// else should NOT be called — fail loudly if it is.
	writeStubBin(t, dir, "claude", `#!/bin/sh
if [ "$1" = "mcp" ] && [ "$2" = "get" ] && [ "$3" = "ezstack" ]; then
  exit 0
fi
echo "unexpected claude invocation: $*" >&2
exit 99
`)
	// ezs-mcp must exist on PATH so the binary check passes. We never execute it.
	writeStubBin(t, dir, "ezs-mcp", matchingVersionStub())
	isolateStubPath(t, dir)

	if got := ensureEzstackMCP("claude", false, false); !got {
		t.Error("already-registered case should return true")
	}
}

func TestEnsureEzstackMCP_RegistersWhenMissing(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "claude.log")

	// claude: `mcp get` exits 1 (not registered); `mcp add` writes its args
	// to a log file and exits 0.
	writeStubBin(t, dir, "claude", `#!/bin/sh
if [ "$1" = "mcp" ] && [ "$2" = "get" ]; then
  exit 1
fi
if [ "$1" = "mcp" ] && [ "$2" = "add" ]; then
  echo "$@" > "`+logPath+`"
  exit 0
fi
echo "unexpected claude invocation: $*" >&2
exit 99
`)
	writeStubBin(t, dir, "ezs-mcp", matchingVersionStub())
	isolateStubPath(t, dir)

	if got := ensureEzstackMCP("claude", false, false); !got {
		t.Error("unregistered-but-installable case should return true")
	}
	// Verify claude mcp add was actually called with the expected args.
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected `claude mcp add` to have been invoked (no log): %v", err)
	}
	got := strings.TrimSpace(string(logged))
	want := "mcp add ezstack --scope user -- ezs-mcp"
	if got != want {
		t.Errorf("claude mcp add args mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestEnsureEzstackMCP_RegistrationFailureFallsBack(t *testing.T) {
	dir := t.TempDir()

	// claude: `mcp get` exits 1, `mcp add` exits 1 → registration fails.
	writeStubBin(t, dir, "claude", `#!/bin/sh
if [ "$1" = "mcp" ] && [ "$2" = "get" ]; then
  exit 1
fi
if [ "$1" = "mcp" ] && [ "$2" = "add" ]; then
  echo "boom" >&2
  exit 1
fi
exit 99
`)
	writeStubBin(t, dir, "ezs-mcp", matchingVersionStub())
	isolateStubPath(t, dir)

	if got := ensureEzstackMCP("claude", false, false); got {
		t.Error("failed registration should fall back (return false)")
	}
}

// TestEnsureEzstackMCP_VersionSkewReinstalls simulates ezs-mcp on PATH but
// reporting a stale version. It stubs `go` so that `go install …@latest`
// rewrites the ezs-mcp stub to report the matching version. After the call
// the binary must report the matching version and the function must return
// true. A marker file records that `go install` was actually invoked.
func TestEnsureEzstackMCP_VersionSkewReinstalls(t *testing.T) {
	dir := t.TempDir()
	goMarker := filepath.Join(dir, "go_install_called.txt")
	mcpPath := filepath.Join(dir, "ezs-mcp")

	// claude: already registered.
	writeStubBin(t, dir, "claude", `#!/bin/sh
if [ "$1" = "mcp" ] && [ "$2" = "get" ] && [ "$3" = "ezstack" ]; then
  exit 0
fi
exit 99
`)

	// Initial ezs-mcp reports a skewed version.
	writeStubBin(t, dir, "ezs-mcp", `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "-v" ]; then
  echo "0.0.0-stale"
  exit 0
fi
exit 0
`)

	// Stub `go`: on `go install …`, touch a marker and rewrite ezs-mcp to
	// report the matching version (simulating a successful upgrade).
	writeStubBin(t, dir, "go", `#!/bin/sh
PATH="/usr/bin:/bin:$PATH"
if [ "$1" = "install" ]; then
  echo "$@" > "`+goMarker+`"
  cat > "`+mcpPath+`" <<'EOF'
#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "-v" ]; then
  echo "`+version.Version+`"
  exit 0
fi
exit 0
EOF
  chmod +x "`+mcpPath+`"
  exit 0
fi
exit 99
`)

	isolateStubPath(t, dir)

	if got := ensureEzstackMCP("claude", false, false); !got {
		t.Error("skewed-then-reinstalled case should return true")
	}
	if _, err := os.Stat(goMarker); err != nil {
		t.Errorf("expected `go install` to have been invoked on skew, got: %v", err)
	}
	// After reinstall, readMCPBinaryVersion should now report the expected version.
	if got := readMCPBinaryVersion(); got != version.Version {
		t.Errorf("after reinstall expected version %q, got %q", version.Version, got)
	}
}

// TestEnsureEzstackMCP_PinnedInstallFallsBackToLatest simulates a dev build
// where @v<version> doesn't exist yet (the tag hasn't been pushed). The go
// stub fails the pinned install and succeeds on the @latest retry. The
// function must return true and both install invocations must be recorded.
func TestEnsureEzstackMCP_PinnedInstallFallsBackToLatest(t *testing.T) {
	dir := t.TempDir()
	goLog := filepath.Join(dir, "go_install_calls.txt")
	mcpPath := filepath.Join(dir, "ezs-mcp")

	writeStubBin(t, dir, "claude", `#!/bin/sh
if [ "$1" = "mcp" ] && [ "$2" = "get" ] && [ "$3" = "ezstack" ]; then
  exit 0
fi
exit 99
`)

	// Force a reinstall by reporting a stale version.
	writeStubBin(t, dir, "ezs-mcp", `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "-v" ]; then
  echo "0.0.0-stale"
  exit 0
fi
exit 0
`)

	// go install <target>: fail on @v<version>, succeed on @latest. Record
	// every call so the test can assert the fallback actually happened.
	writeStubBin(t, dir, "go", `#!/bin/sh
PATH="/usr/bin:/bin:$PATH"
if [ "$1" = "install" ]; then
  echo "$2" >> "`+goLog+`"
  case "$2" in
    *@latest)
      cat > "`+mcpPath+`" <<'EOF'
#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "-v" ]; then
  echo "`+version.Version+`"
  exit 0
fi
exit 0
EOF
      chmod +x "`+mcpPath+`"
      exit 0
      ;;
    *)
      echo "no such tag" >&2
      exit 1
      ;;
  esac
fi
exit 99
`)

	isolateStubPath(t, dir)

	if got := ensureEzstackMCP("claude", false, false); !got {
		t.Error("pinned-fail-then-latest case should return true")
	}
	logged, err := os.ReadFile(goLog)
	if err != nil {
		t.Fatalf("expected go install log: %v", err)
	}
	calls := strings.Split(strings.TrimSpace(string(logged)), "\n")
	if len(calls) != 2 {
		t.Fatalf("expected 2 go install calls (pinned + latest), got %d: %v", len(calls), calls)
	}
	if !strings.HasSuffix(calls[0], "@v"+version.Version) {
		t.Errorf("first call should pin @v%s, got %q", version.Version, calls[0])
	}
	if !strings.HasSuffix(calls[1], "@latest") {
		t.Errorf("second call should fall back to @latest, got %q", calls[1])
	}
}

// TestEnsureEzstackMCP_VersionMatchSkipsReinstall verifies that when the
// installed ezs-mcp already reports the matching version, `go install` is
// NOT invoked (no marker).
//
// Regression: matchingVersionStub now uses the real "ezstack-mcp version
// X.Y.Z" banner shape, so this test exercises the production code path
// end-to-end. Before the parser fix, the stub printed bare semver and this
// test passed even though the real binary triggered a reinstall on every
// `ezs agent` invocation.
func TestEnsureEzstackMCP_VersionMatchSkipsReinstall(t *testing.T) {
	dir := t.TempDir()
	goMarker := filepath.Join(dir, "go_install_called.txt")

	writeStubBin(t, dir, "claude", `#!/bin/sh
if [ "$1" = "mcp" ] && [ "$2" = "get" ] && [ "$3" = "ezstack" ]; then
  exit 0
fi
exit 99
`)
	writeStubBin(t, dir, "ezs-mcp", matchingVersionStub())
	writeStubBin(t, dir, "go", `#!/bin/sh
echo "$@" > "`+goMarker+`"
exit 0
`)
	isolateStubPath(t, dir)

	if got := ensureEzstackMCP("claude", false, false); !got {
		t.Error("matching-version case should return true")
	}
	if _, err := os.Stat(goMarker); err == nil {
		body, _ := os.ReadFile(goMarker)
		t.Errorf("matching version should NOT invoke `go install`, but saw:\n%s", body)
	}
}

// TestEnsureEzstackMCP_LegacyBareFormatSkipsReinstall covers the second
// shape parseMCPVersionOutput accepts: an older ezs-mcp build that prints
// only the bare semver (no "ezstack-mcp version " prefix). Users on those
// builds should not see a reinstall on every `ezs agent` either, as long
// as the version matches.
func TestEnsureEzstackMCP_LegacyBareFormatSkipsReinstall(t *testing.T) {
	dir := t.TempDir()
	goMarker := filepath.Join(dir, "go_install_called.txt")

	writeStubBin(t, dir, "claude", `#!/bin/sh
if [ "$1" = "mcp" ] && [ "$2" = "get" ] && [ "$3" = "ezstack" ]; then
  exit 0
fi
exit 99
`)
	writeStubBin(t, dir, "ezs-mcp", matchingVersionStubLegacyBareFormat())
	writeStubBin(t, dir, "go", `#!/bin/sh
echo "$@" > "`+goMarker+`"
exit 0
`)
	isolateStubPath(t, dir)

	if got := ensureEzstackMCP("claude", false, false); !got {
		t.Error("legacy bare-format matching-version case should return true")
	}
	if _, err := os.Stat(goMarker); err == nil {
		body, _ := os.ReadFile(goMarker)
		t.Errorf("legacy bare-format matching version should NOT invoke `go install`, but saw:\n%s", body)
	}
}

func TestEnsureEzstackMCP_ProbeOnlyDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	mutLog := filepath.Join(dir, "claude_mutations.log")

	// claude stub: `mcp get` exits 1 (not registered). Any other `mcp` call
	// appends to mutLog — probe must NOT hit these paths.
	writeStubBin(t, dir, "claude", `#!/bin/sh
if [ "$1" = "mcp" ] && [ "$2" = "get" ]; then
  exit 1
fi
if [ "$1" = "mcp" ]; then
  echo "$@" >> "`+mutLog+`"
  exit 0
fi
exit 99
`)
	writeStubBin(t, dir, "ezs-mcp", matchingVersionStub())
	isolateStubPath(t, dir)

	// Probe-only against not-registered state should return false, and must
	// not have invoked `claude mcp add` (no mutation log).
	if got := ensureEzstackMCP("claude", false, true); got {
		t.Error("probe-only on unregistered state should return false")
	}
	if _, err := os.Stat(mutLog); err == nil {
		body, _ := os.ReadFile(mutLog)
		t.Errorf("probe-only must not mutate claude state, but saw:\n%s", body)
	}
}

func TestEnsureEzstackMCP_ProbeOnlyReportsReadyState(t *testing.T) {
	dir := t.TempDir()
	writeStubBin(t, dir, "claude", `#!/bin/sh
if [ "$1" = "mcp" ] && [ "$2" = "get" ] && [ "$3" = "ezstack" ]; then
  exit 0
fi
exit 99
`)
	writeStubBin(t, dir, "ezs-mcp", matchingVersionStub())
	isolateStubPath(t, dir)

	if got := ensureEzstackMCP("claude", false, true); !got {
		t.Error("probe-only on ready state should return true")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
