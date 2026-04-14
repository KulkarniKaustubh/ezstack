package commands

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/version"
)

// ezsMCPModulePath is the go-install path for the ezs-mcp binary.
const ezsMCPModulePath = "github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs-mcp"

// ezsMCPName is the name under which the server is registered with claude.
const ezsMCPName = "ezstack"

// ensureEzstackMCP guarantees that (a) the ezs-mcp binary is available and
// (b) it is registered with the local Claude Code install. It returns true
// when the MCP is ready to use, false when the caller should fall back to
// the legacy doc-paste prompt.
//
// It only runs when the agent CLI is `claude` — MCP registration is Claude
// Code-specific. Pass noMCP=true to opt out entirely. Pass probeOnly=true
// (e.g. for --dry-run) to detect existing state without installing or
// registering anything.
func ensureEzstackMCP(agentCmd string, noMCP, probeOnly bool) bool {
	if noMCP {
		return false
	}
	// Only the claude CLI supports MCP registration. Match on the basename so
	// a fully-qualified agent_command (e.g. "/usr/local/bin/claude --some-flag")
	// still counts as claude.
	fields := strings.Fields(agentCmd)
	if len(fields) == 0 || filepath.Base(fields[0]) != "claude" {
		return false
	}
	claudeBin, err := exec.LookPath(fields[0])
	if err != nil {
		return false
	}

	if probeOnly {
		// Pure detection: no go install, no claude mcp add.
		if _, err := exec.LookPath("ezs-mcp"); err != nil {
			return false
		}
		return isMCPRegistered(claudeBin)
	}

	if err := ensureMCPBinary(); err != nil {
		ui.Warn(fmt.Sprintf("ezs-mcp unavailable: %v — falling back to embedded docs.", err))
		return false
	}

	if err := ensureMCPRegistered(claudeBin); err != nil {
		ui.Warn(fmt.Sprintf("could not register ezstack MCP with claude: %v — falling back to embedded docs.", err))
		return false
	}
	return true
}

// ensureMCPBinary guarantees ezs-mcp is on PATH and version-aligned with the
// running ezs binary. Missing or skewed → run `go install`.
func ensureMCPBinary() error {
	if _, err := exec.LookPath("ezs-mcp"); err != nil {
		return installMCPBinary("not installed")
	}
	installed := readMCPBinaryVersion()
	if installed == "" {
		// Older ezs-mcp without --version, or a broken binary. Reinstall to
		// guarantee a version-aligned build.
		return installMCPBinary("version unknown (pre-4.x build?)")
	}
	if installed != version.Version {
		return installMCPBinary(fmt.Sprintf("version skew: ezs %s vs ezs-mcp %s", version.Version, installed))
	}
	return nil
}

// installMCPBinary runs `go install` for ezs-mcp. It first tries the exact
// release matching the running ezs binary (@v<version>), and falls back to
// @latest if that tag doesn't exist — this keeps release builds pinned
// while still working on dev builds whose version hasn't been tagged yet.
// reason is surfaced to the user so they understand why we're reinstalling.
func installMCPBinary(reason string) error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("ezs-mcp %s and no `go` in PATH to install one (try `go install %s@v%s` manually)", reason, ezsMCPModulePath, version.Version)
	}

	pinnedTarget := ezsMCPModulePath + "@v" + version.Version
	ui.Info(fmt.Sprintf("Installing %s (%s)...", pinnedTarget, reason))
	if err := runGoInstall(pinnedTarget); err != nil {
		// Pinned tag may not exist yet on an untagged dev build. Fall back to
		// @latest so the MCP still comes up, just not version-pinned.
		latestTarget := ezsMCPModulePath + "@latest"
		ui.Warn(fmt.Sprintf("go install %s failed (%v) — retrying with %s", pinnedTarget, err, latestTarget))
		if err := runGoInstall(latestTarget); err != nil {
			return fmt.Errorf("go install failed for both %s and %s: %w", pinnedTarget, latestTarget, err)
		}
	}

	if _, err := exec.LookPath("ezs-mcp"); err != nil {
		return fmt.Errorf("ezs-mcp installed but not on PATH (is your GOBIN / $HOME/go/bin on PATH?)")
	}
	return nil
}

// runGoInstall shells out to `go install <target>`, streaming go's output to
// stderr so the user sees any network or compile errors in real time.
func runGoInstall(target string) error {
	cmd := exec.Command("go", "install", target)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// readMCPBinaryVersion runs `ezs-mcp --version` and returns the printed
// version string, or "" if the binary doesn't support --version or fails.
func readMCPBinaryVersion() string {
	cmd := exec.Command("ezs-mcp", "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// ensureMCPRegistered checks whether the ezstack MCP is registered with
// claude at user scope and registers it if not.
func ensureMCPRegistered(claudeBin string) error {
	if isMCPRegistered(claudeBin) {
		return nil
	}
	ui.Info("Registering ezstack MCP with claude (user scope)...")
	cmd := exec.Command(claudeBin, "mcp", "add", ezsMCPName, "--scope", "user", "--", "ezs-mcp")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// isMCPRegistered returns true if `claude mcp get ezstack` succeeds.
func isMCPRegistered(claudeBin string) bool {
	cmd := exec.Command(claudeBin, "mcp", "get", ezsMCPName)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// mcpDocsStub is the short note that replaces the embedded DOCUMENTATION.md
// dump when the ezstack MCP is active. Agents get the full tool surface via
// the MCP registration itself; this just tells them to prefer those tools.
const mcpDocsStub = `## ezstack MCP

The ezstack MCP server is registered with this Claude Code session. Prefer its tools over shelling out:

- ezstack_status, ezstack_list, ezstack_log, ezstack_diff — inspect state
- ezstack_new, ezstack_goto, ezstack_delete, ezstack_reparent — manage branches
- ezstack_commit, ezstack_amend, ezstack_push, ezstack_sync — edit & propagate
- ezstack_pr_create, ezstack_pr_stack, ezstack_pr_merge, ezstack_pr_update — PR flow

Call the tools directly instead of running ` + "`ezs`" + ` in a shell. Only shell out for anything the MCP tools do not cover (then run ` + "`ezs help <command>`" + ` first).`
