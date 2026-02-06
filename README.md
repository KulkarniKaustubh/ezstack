<div align="center">

# ezstack

**Manage stacked PRs with git worktrees (beta)**

[![Go Version](https://img.shields.io/badge/Go-1.25.1+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Beta](https://img.shields.io/badge/Status-Beta-orange.svg)]()

</div>

> ⚠️ **BETA SOFTWARE**: This tool is currently in beta and under heavy development. It is subject to major changes at any time.

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

### Shell Integration (Required)

Add to your `~/.bashrc` or `~/.zshrc`:

```bash
eval "$(ezs --shell-init)"
```

This creates a shell function that wraps the `ezs` binary, enabling commands like `ezs goto` to change your shell's directory.

## Quick Start

```bash
# Configure ezstack for your repository
ezs config

# Create your first branch
ezs new feature-1

# Stack another branch on top
ezs new feature-2 --parent feature-1

# View your stack
ezs status

# Create PRs
ezs pr create -t "Part 1: Add feature"

# Sync after changes
ezs sync
```

## Commands

| Command | Description |
|---------|-------------|
| `new` | Create a new branch in the stack |
| `list` | List all stacks and branches |
| `status` | Show status of current stack |
| `sync` | Sync stack with remote |
| `goto` | Navigate to a branch worktree |
| `reparent` | Change the parent of a branch |
| `stack` | Add a branch to a stack |
| `unstack` | Remove a branch from tracking |
| `update` | Sync config with git |
| `delete` | Delete a branch and its worktree |
| `pr` | Manage pull requests |
| `config` | Configure ezstack |

Run `ezs <command> --help` for command-specific help.

## Documentation

See [DOCUMENTATION.md](DOCUMENTATION.md) for comprehensive documentation.

## License

MIT
