<div align="center">

<img src="assets/logo.png" alt="ezstack logo" width="120">

# ezstack

**Manage stacked PRs with git worktrees**

A CLI tool for managing stacked pull requests using git worktrees. Create branches, sync rebases, manage PRs — all from one command line tool.

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

[Website](https://kulkarnikaustubh.github.io/ezstack/) · [Documentation](DOCUMENTATION.md) · [Releases](https://github.com/KulkarniKaustubh/ezstack/releases)

</div>

---

## Requirements

- [Git](https://git-scm.com/) 2.20+
- [fzf](https://github.com/junegunn/fzf) for interactive selection
- [GitHub CLI](https://cli.github.com/) (`gh`) for PR operations

## Installation

### Homebrew (macOS/Linux)

```bash
brew tap KulkarniKaustubh/ezstack
brew install ezstack
```

### Go Install

```bash
go install github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs@latest

# Optional: MCP server for Claude Code and other MCP-compatible agents
go install github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs-mcp@latest
```

### From Source

```bash
git clone https://github.com/KulkarniKaustubh/ezstack.git
cd ezstack
make install
```

### Shell Integration (Recommended)

Add to your `~/.bashrc` or `~/.zshrc`:

```bash
eval "$(ezs --shell-init)"
```

This creates a shell function that wraps the `ezs` binary, enabling commands like `ezs goto`, `ezs up`, and `ezs new` to change your shell's directory. Without shell integration, these commands will print the path and instruct you to `cd` manually.

## Quick Start

```bash
# Configure ezstack for your repository
ezs config

# Create your first branch
ezs new feature-1

# Stack another branch on top
ezs new feature-2 --parent feature-1

# View your stack with PR and CI status
ezs status

# Create PRs
ezs pr create -t "Part 1: Add feature"

# Commit and auto-sync children
ezs commit -m "Add feature"

# Sync after changes
ezs sync -a
```

## PR Review

Quickly check out a teammate's branch into its own worktree for review or collaboration:

```bash
# Creates a local worktree tracking the remote branch
# Registers it as a stack (root = PR base branch or main)
# Shows as (remote) in ezs ls
ezs new origin/feature-branch

# Shows PR info, review status, and line diff automatically
# You can work on it, push, sync — all commands work normally
# Fork PRs are auto-detected: adds the fork remote and pushes there
# When done, clean up with:
ezs delete feature-branch
```

## Commands

| Command | Aliases | Description |
|---------|---------|-------------|
| `agent` | | Launch AI agent with stack context (work session or feature builder) |
| `agent prompt` | | View or edit agent prompt templates |
| `amend` | | Amend last commit and auto-sync children |
| `commit` | `ci` | Commit and auto-sync child branches |
| `config` | `cfg` | Configure ezstack |
| `delete` | `del`, `rm` | Delete a branch and its worktree |
| `diff` | | Show diff against parent branch |
| `down` | | Navigate down the stack (toward children) |
| `goto` | `go` | Navigate to a branch worktree |
| `list` | `ls` | List all stacks and branches |
| `log` | | Show commits in a branch since its parent |
| `menu` | | Interactive command menu |
| `new` | `n` | Create a new branch in the stack |
| `pr` | | Manage pull requests (create, update, merge, draft, stack) |
| `push` | | Push current branch or entire stack |
| `reparent` | `rp` | Change the parent of a branch |
| `stack` | | Add a branch to a stack |
| `status` | `st` | Show status with PR and CI info |
| `sync` | | Sync stack with remote (rebase or merge) |
| `unstack` | | Remove a branch from tracking |
| `up` | | Navigate up the stack (toward parent) |

**Global flags:** `-y, --yes` auto-confirm prompts · `-h, --help` · `-v, --version`

Run `ezs <command> --help` for command-specific help.

## AI Agent

ezstack can launch an AI coding agent (Claude, Cursor, etc.) with full stack context injected automatically. The agent is scoped to a single stack and knows about all branches, worktree paths, and available commands. **Requires worktree mode** (`use_worktrees: true`, which is the default) — the agent needs separate working directories for each branch to operate in isolation.

```bash
# Launch agent (works from any branch, including main)
ezs agent

# Build a feature as stacked branches
ezs agent feature "Add user authentication with JWT tokens"

# View or edit the agent's prompt templates
ezs agent prompt --shipped work
ezs agent prompt --edit work
```

Agent prompts are composed from three layers: a shipped prompt (updated with releases), custom instructions (`~/.ezstack/`), and repo-specific instructions (`<repo>/.ezstack/`). See the [agent workflows](DOCUMENTATION.md#ezs-agent) section of DOCUMENTATION.md for full details.

## MCP Server (Claude Code & other MCP clients)

ezstack ships a standalone MCP server, `ezs-mcp`, that exposes the full stack workflow as Model Context Protocol tools. Point any MCP-compatible agent at it (Claude Code, Zed, etc.) and the agent can drive `ezs` directly: inspect (`status`, `list`, `diff`, `log`, `config_show`), mutate (`commit`, `amend`, `sync`, `push`, `new`, `delete`, `reparent`, `stack`, `unstack`, `config_set`), navigate (`goto`), and manage PRs (`pr_create`, `pr_update`, `pr_merge`, `pr_draft`, `pr_stack`). 21 tools, one binary.

Install:

```bash
go install github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs-mcp@latest
# or, from source
make install-mcp
```

Register with Claude Code (one registration, every repo — `ezs-mcp` operates on whichever directory Claude launches it in):

```bash
claude mcp add ezstack --scope user -- ezs-mcp
```

If you open Claude Code at a monorepo root but your ezstack-configured repo is a subdirectory, Claude will launch `ezs-mcp` with the monorepo root as its cwd, which won't match any sub-repo. In that case, register a per-subrepo entry with an absolute path: `claude mcp add ezstack-foo -- ezs-mcp --repo /abs/path/to/foo`. Read-only tools (`ezstack_status`, `ezstack_list`, `ezstack_diff`, `ezstack_log`, `ezstack_config_show`) return JSON by default, or pass `decorated=true` (where supported) to get the terminal-styled output. Destructive tools (`ezstack_commit`, `ezstack_amend`, `ezstack_sync`, `ezstack_push`, `ezstack_delete`, `ezstack_pr_merge`, `ezstack_pr_update`) are tagged with the MCP destructive annotation so the client can prompt before running them. `ezstack_commit` requires an explicit `message` and `ezstack_amend` defaults to `--no-edit` so neither can ever launch `$EDITOR` and corrupt the JSON-RPC transport. Most tools accept an optional `branch` parameter so they work even when the MCP server's working directory is on a non-stack branch like `main`.

Full feature tour and tool catalog: [mcp.html](https://kulkarnikaustubh.github.io/ezstack/mcp.html).

## Configuration

ezstack supports both worktree-based and checkout-based workflows:

- **Worktrees (default, recommended):** Each branch gets its own worktree directory for parallel work. Required for `ezs agent`.
- **No worktrees:** Branches use `git checkout` for a simpler, single-directory workflow. All core commands (`sync`, `commit`, `push`, `pr`, `reparent`) work fully. Note: `ezs agent` is not available in this mode.

Configure with `ezs config set use_worktrees true/false`.

### Sync Strategy

By default, ezstack uses `git rebase` to sync branches. You can switch to `git merge` per-repo:

```bash
ezs config set sync_strategy merge
```

Or override per-command with `--merge` / `--rebase` flags:

```bash
ezs sync -a --merge       # merge just this once
ezs commit -m "fix" --rebase  # rebase children even if config says merge
```

The `--merge` and `--rebase` flags work with `sync`, `commit`, `amend`, and `reparent`.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Usage/argument error |
| 3 | Rebase conflict |
| 4 | Not in a git repository |
| 5 | Not in a stack |
| 6 | GitHub authentication required |
| 7 | Branch not found |
| 8 | Network/remote error |
| 10 | User cancelled |

## Editor & Desktop Integrations

ezstack ships with first-party clients so you can drive the same `ezs` CLI from
your editor or a native GUI:

| Integration | Description | Docs |
|---|---|---|
| **VS Code Extension** (`vscode-extension/`) | Sidebar stack tree, per-branch file browser, PR & CI status, command palette, agent integration | [vscode.html](https://kulkarnikaustubh.github.io/ezstack/vscode.html) · [README](vscode-extension/README.md) |
| **Neovim Plugin** (`neovim-plugin/`) | Native Lua plugin with `:Ezs` command suite, styled stack viewer, Telescope pickers, statusline component, fugitive auto-refresh | [nvim.html](https://kulkarnikaustubh.github.io/ezstack/nvim.html) · [README](neovim-plugin/README.md) |
| **Desktop App** (`tauri-ui/`) | Tauri v2 + React 19 desktop GUI. Three-panel layout, visual stack graph with drag-to-reparent, branch reflog history, sidebar repo filter, toast notifications, remote SSH mode | [desktop.html](https://kulkarnikaustubh.github.io/ezstack/desktop.html) · [README](tauri-ui/README.md) |
| **MCP Server** (`cmd/ezs-mcp/`) | Model Context Protocol server for Claude Code and other MCP-compatible agents. Exposes eleven stack operations as tools with destructive annotations and required-arg schemas | [mcp.html](https://kulkarnikaustubh.github.io/ezstack/mcp.html) |

### VS Code

```bash
# Install from a pre-built VSIX (download from the Releases page)
code --install-extension ezstack-4.0.0.vsix
```

Then open the **ezstack** panel in the activity bar. Auto-refreshes on
`~/.ezstack/stacks.json` changes. Configure with `ezstack.cliPath`,
`ezstack.autoRefresh`, and `ezstack.ticketPattern`.

### Neovim

```lua
-- lazy.nvim
{
  "KulkarniKaustubh/ezstack",
  subdir = "neovim-plugin",
  cmd    = { "Ezs" },
  config = function() require("ezstack").setup() end,
}
```

Requires Neovim 0.10+ and the `ezs` CLI on `$PATH`. Run `:Ezs` to open the
stack viewer or `:Telescope ezstack branches` for fuzzy picking. See
`:help ezstack` for the full reference.

### Desktop App

```bash
cd tauri-ui
npm install
npm run tauri dev      # development
npm run tauri build    # production bundle in src-tauri/target/release/bundle/
```

Or grab a prebuilt installer from the
[Releases page](https://github.com/KulkarniKaustubh/ezstack/releases).
Supports Remote (SSH) mode for driving `ezs` on a dev VM.

## Documentation

See [DOCUMENTATION.md](DOCUMENTATION.md) for comprehensive documentation, including [AI-assisted workflows with `ezs agent`](DOCUMENTATION.md#ezs-agent).

## License

MIT
