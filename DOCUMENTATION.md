<div align="center">

# ezstack docs

**Comprehensive guide to using ezstack**

</div>

---

**Table of Contents**

[Overview](#overview) · [Installation](#installation) · [Configuration](#configuration) · [Commands](#commands) · [Workflows](#workflows)

**Commands:** [new](#ezs-new) · [status](#ezs-status) · [list](#ezs-list) · [sync](#ezs-sync) · [goto](#ezs-goto) · [pr](#ezs-pr) · [delete](#ezs-delete) · [reparent](#ezs-reparent) · [stack](#ezs-stack) · [unstack](#ezs-unstack) · [update](#ezs-update) · [config](#ezs-config)

---

## Overview

ezstack is a CLI tool for managing stacked pull requests using git worktrees. Each branch in your stack lives in its own worktree, allowing you to work on multiple parts of a feature simultaneously.

**Key Concepts**

- **Stack** — A chain of branches where each branch builds on its parent
- **Worktree** — A separate working directory for each branch
- **Sync** — Rebase branches when parents are merged or updated

---

## Installation

**Prerequisites**

- Go 1.25+
- Git 2.20+
- [fzf](https://github.com/junegunn/fzf) — interactive selection
- [GitHub CLI](https://cli.github.com/) (`gh`) — PR operations

**Homebrew (macOS/Linux)**

```bash
brew tap KulkarniKaustubh/ezstack
brew install ezstack
```

**Go Install**

```bash
go install github.com/KulkarniKaustubh/ezstack/cmd/ezs@latest
```

**Build from source**

```bash
git clone https://github.com/KulkarniKaustubh/ezstack.git
cd ezstack
make install
```

**Shell integration**

Add to your shell configuration:

For `bash`:
```bash
echo 'eval "$(ezs --shell-init)"' >> ~/.bashrc
```

For `zsh`:
```bash
echo 'eval "$(ezs --shell-init)"' >> ~/.zshrc
```

This enables automatic directory changes when using `goto`, `new`, `delete`, and `sync` commands.

---

## Configuration

Run `ezs config` in your repository to configure:

- **Worktree base directory** — Where branch worktrees will be created
- **Main branch name** — Usually `main` or `master`

Configuration is stored in `~/.ezstack/config.json`.

**Subcommands**

```
ezs config set <key> <value>    Set a configuration value
ezs config show                 Show current configuration
```

**Available keys:** `worktree_base_dir`, `default_base_branch`, `github_token`, `cd_after_new`

**Global flags**

These flags work with any command and can appear in any position:

```
-y, --yes        Auto-confirm all yes/no prompts (selection menus still show)
-h, --help       Show help
-v, --version    Show version
--shell-init     Output shell function for cd support
```

---

## Commands

### `ezs new`

Create a new branch in the stack. Aliases: `n`

```
ezs new [branch-name] [options]

Options:
    -p, --parent <branch>     Parent branch (defaults to current branch)
    -w, --worktree <path>     Worktree path (defaults to configured base dir + branch name)
    -c, --cd                  Change to the new worktree after creation
    -C, --no-cd               Don't change to the new worktree (overrides config)
    -f, --from-worktree       Register an existing worktree as a stack root
    -r, --from-remote         Create a stack from a remote branch
```

<details>
<summary>Examples</summary>

```bash
# Create a new branch with current branch as parent
ezs new feature-1

# Create a branch with a specific parent
ezs new feature-2 --parent feature-1

# Create and cd into the new worktree
ezs new feature-3 -c

# Register an existing worktree as a stack root
ezs new -f

# Create a stack from a remote branch
ezs new -r
```

</details>

---

### `ezs status`

Show status of current stack with PR and CI info. Aliases: `st`

```
ezs status [options]

Options:
    -a, --all     Show all stacks
    -d, --debug   Show debug output
```

<details>
<summary>Examples</summary>

```bash
# Show current stack status
ezs status

# Show all stacks
ezs status -a
```

</details>

---

### `ezs list`

List all stacks and branches. Aliases: `ls`

```
ezs list [options]

Options:
    -a, --all     Show all stacks
    -d, --debug   Show debug output
```

<details>
<summary>Examples</summary>

```bash
# List current stack
ezs list

# List all stacks
ezs ls -a
```

</details>

---

### `ezs sync`

Sync stack with remote. Handles rebasing onto updated parents, cleaning up merged branches, and force pushing after rebase. Aliases: `rebase`, `rb`

```
ezs sync [options]
ezs sync <hash-prefix>

Options:
    -a, --all              Sync current stack (auto-detect what needs syncing)
    --all-stacks           Sync ALL stacks (not just current stack)
    -c, --current          Sync current branch only (auto-detect what it needs)
    -p, --parent           Rebase current branch onto its parent
    -C, --children         Rebase child branches onto current branch
    --no-delete-local      Don't delete local branches after their PRs are merged
```

You can sync a specific stack by passing its hash prefix (minimum 3 characters).
Use `ezs list` to see stack hashes.

<details>
<summary>Examples</summary>

```bash
# Interactive sync menu
ezs sync

# Sync a specific stack by hash prefix (min 3 chars)
ezs sync a1b2c

# Auto-sync current stack
ezs sync -a

# Sync all stacks
ezs sync --all-stacks

# Sync only current branch
ezs sync -c

# Rebase current branch onto its parent
ezs sync -p

# Rebase children onto current branch
ezs sync -C
```

</details>

---

### `ezs goto`

Navigate to a branch worktree. Aliases: `go`

```
ezs goto [branch-name]
```

If branch-name is omitted, shows interactive selection of all worktrees.

<details>
<summary>Examples</summary>

```bash
# Interactive worktree selection
ezs goto

# Go to a specific branch
ezs go feature-1
```

</details>

---

### `ezs pr`

Manage pull requests.

```
ezs pr <subcommand> [options]

Subcommands:
    create    Create a new pull request
    update    Push changes to existing PR
    stack     Update all PR descriptions with stack info
```

#### `ezs pr create`

```
ezs pr create [options]

Options:
    -t, --title <title>    PR title (defaults to branch name)
    -b, --body <body>      PR body/description
    -d, --draft            Create as draft PR
```

<details>
<summary>Examples</summary>

```bash
# Create a PR with a title
ezs pr create -t "Add new feature"

# Create a draft PR
ezs pr create -t "WIP: New feature" -d

# Create with title and body
ezs pr create -t "Add feature" -b "This PR adds..."

# Push updates to existing PR
ezs pr update

# Update all PR descriptions with stack info
ezs pr stack
```

</details>

---

### `ezs delete`

Delete a branch and its worktree. Aliases: `del`, `rm`

```
ezs delete [branch-name] [options]

Options:
    -f, --force    Force delete even if branch has children
```

<details>
<summary>Examples</summary>

```bash
# Interactive branch selection
ezs delete

# Delete a specific branch
ezs rm feature-1

# Force delete a branch with children
ezs delete feature-1 -f
```

</details>

---

### `ezs reparent`

Change the parent of a branch. Aliases: `rp`

```
ezs reparent [branch] [new-parent] [options]

Options:
    -b, --branch <name>     Branch to reparent
    -p, --parent <name>     New parent branch
    -n, --no-rebase         Don't rebase, just update metadata (default: rebase)
```

<details>
<summary>Examples</summary>

```bash
# Interactive mode
ezs reparent

# Reparent feature-2 to feature-1
ezs reparent feature-2 feature-1

# Reparent using flags
ezs rp -b feature-2 -p main

# Update metadata only (no rebase)
ezs reparent feature-2 main --no-rebase
```

</details>

---

### `ezs stack`

Add an untracked branch/worktree to an existing stack, or start a new stack.

```
ezs stack [branch] [parent] [options]

Options:
    -b, --branch <name>     Branch to add to stack
    -p, --parent <name>     Parent branch in the stack
    -B, --base <name>       Base branch for a new stack (e.g. develop, staging)
```

In interactive mode, you can choose to:
- Add a branch to an existing stack
- Start a new stack with a custom base branch (e.g. `develop`, `staging`)
- Start a new stack from a remote PR (stack on top of someone else's branch)

`--base` and `--parent` are mutually exclusive. Use `--base` to start a new stack rooted on a branch other than the default base branch.

<details>
<summary>Examples</summary>

```bash
# Interactive mode (choose from menu)
ezs stack

# Add my-branch under feature-1
ezs stack my-branch feature-1

# Add using flags
ezs stack -b my-branch -p main

# Start a new stack on develop
ezs stack -b my-branch --base develop
```

</details>

---

### `ezs unstack`

Remove a branch from stack tracking without deleting the git branch or worktree.

```
ezs unstack [branch] [options]

Options:
    -b, --branch <name>     Branch to untrack
```

If the branch has children, they will be reparented to the untracked branch's parent.

<details>
<summary>Examples</summary>

```bash
# Interactive mode
ezs unstack

# Untrack a specific branch
ezs unstack feature-1
```

</details>

---

### `ezs update`

Reconcile ezstack config with git reality. Aliases: `up`

```
ezs update [options]

Options:
    -a, --auto        Auto-accept all changes without prompting
    -d, --dry-run     Show what would be changed without making changes
```

This command:
- Detects renamed branches (`git branch -m`) and updates config automatically
- Removes branches from config if their worktree folder was deleted
- Removes branches from config if the git branch no longer exists

<details>
<summary>Examples</summary>

```bash
# Interactive mode
ezs update

# Auto-accept all changes
ezs update --auto

# Preview changes without applying
ezs update --dry-run
```

</details>

---

### `ezs config`

Configure ezstack for the current repository. Aliases: `cfg`

```
ezs config [subcommand] [options]

Subcommands:
    set <key> <value>    Set a configuration value
    show                 Show current configuration
```

**Available keys:**
- `worktree_base_dir` — Base directory for worktrees (per-repo)
- `default_base_branch` — Default base branch (e.g., main)
- `github_token` — GitHub token for API access
- `cd_after_new` — Auto-cd to new worktree (true/false, per-repo)

If no subcommand is provided, runs interactive configuration.

<details>
<summary>Examples</summary>

```bash
# Interactive configuration
ezs config

# Show current configuration
ezs config show

# Set worktree base directory
ezs config set worktree_base_dir ~/worktrees

# Enable auto-cd after creating new branches
ezs config set cd_after_new true
```

</details>

---

## Manual Git Operations

If you rename or delete branches outside of ezstack (e.g., with `git branch -m` or `git branch -D`), run `ezs update` to reconcile:

```bash
# Renamed a branch with git? ezs update detects it automatically
git branch -m old-name new-name
ezs update    # detects the rename, preserves stack position and PR metadata

# Deleted a branch with git? ezs update cleans it up
git branch -D some-branch
ezs update    # removes orphaned branch from config
```

Use `--auto` to skip confirmation prompts, or `--dry-run` to preview changes.

---

## Workflows

### Creating a Stacked PR

```bash
# Start from main
ezs new feature-1

# Make changes, then stack the next part
ezs new feature-2 --parent feature-1

# Create PRs
ezs pr create -t "Part 1: Add feature"
ezs goto feature-2
ezs pr create -t "Part 2: Add feature"

# Update all PR descriptions with stack info
ezs pr stack
```

---

### After Parent is Merged

```bash
# Sync will detect merged parents and rebase
ezs sync -a
```

---

### Stacking on a Remote PR

```bash
# Start a new stack from someone else's PR
ezs stack
# Select "Start a new stack from a remote PR"
# Pick the PR, then pick your branch to stack on top

# Or use ezs new with --from-remote
ezs new my-branch -r
```
