<div align="center">

# ezstack

**Manage stacked PRs with git worktrees**

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

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
| `new` | `n` | Create a new branch in the stack |
| `list` | `ls` | List all stacks and branches |
| `status` | `st` | Show status with PR and CI info |
| `sync` | `rebase`, `rb` | Sync stack with remote |
| `goto` | `go` | Navigate to a branch worktree |
| `up` | | Navigate up the stack (toward parent) |
| `down` | | Navigate down the stack (toward children) |
| `reparent` | `rp` | Change the parent of a branch |
| `stack` | | Add a branch to a stack |
| `unstack` | | Remove a branch from tracking |
| `delete` | `del`, `rm` | Delete a branch and its worktree |
| `commit` | `ci` | Commit and auto-sync child branches |
| `amend` | | Amend last commit and auto-sync children |
| `pr` | | Manage pull requests (create, update, merge, draft, stack) |
| `config` | `cfg` | Configure ezstack |
| `menu` | | Interactive command menu |

**Global flags:** `-y, --yes` auto-confirm prompts · `-h, --help` · `-v, --version`

Run `ezs <command> --help` for command-specific help.

## Configuration

ezstack supports both worktree-based and checkout-based workflows:

- **Worktrees (default):** Each branch gets its own worktree directory for parallel work
- **No worktrees:** Branches use `git checkout` for a simpler, single-directory workflow

Configure with `ezs config set use_worktrees true/false`.

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
