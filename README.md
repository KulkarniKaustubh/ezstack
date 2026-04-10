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
go install github.com/KulkarniKaustubh/ezstack/cmd/ezs@latest
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

ezstack can launch an AI coding agent (Claude, Cursor, etc.) with full stack context injected automatically. The agent is scoped to a single stack and knows about all branches, worktree paths, and available commands.

```bash
# Launch agent on current stack
ezs agent

# Build a feature as stacked branches
ezs agent feature "Add user authentication with JWT tokens"

# View or edit the agent's prompt templates
ezs agent prompt
ezs agent prompt --edit --work
```

Agent prompts are stored as editable Markdown files in `~/.ezstack/` and use template variables (`{{STACK_JSON}}`, `{{BRANCH_NAME}}`, etc.) that are replaced at runtime. See [AGENTS.md](AGENTS.md) for full details.

## Configuration

ezstack supports both worktree-based and checkout-based workflows:

- **Worktrees (default):** Each branch gets its own worktree directory for parallel work
- **No worktrees:** Branches use `git checkout` for a simpler, single-directory workflow

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

## Documentation

See [DOCUMENTATION.md](DOCUMENTATION.md) for comprehensive documentation, or [AGENTS.md](AGENTS.md) for AI-assisted workflows.

## License

MIT
