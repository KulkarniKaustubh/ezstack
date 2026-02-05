<div align="center">

# 📚 ezstack docs

**Comprehensive guide to using ezstack!**

</div>

---

**📖 Table of Contents**

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

- Go 1.25.1+
- Git 2.20+
- [fzf](https://github.com/junegunn/fzf) — interactive selection
- [GitHub CLI](https://cli.github.com/) (`gh`) — PR operations

**Build from source**

```bash
git clone https://github.com/ezstack/ezstack.git
cd ezstack
make build
```

**Shell integration**

Add to `~/.bashrc` or `~/.zshrc`:

```bash
eval "$(ezs --shell-init)"
```

This enables automatic directory changes when using `goto`, `new`, and `delete` commands.

---

## Configuration

Run `ezs config` in your repository to configure:

- **Worktree base directory** — Where branch worktrees will be created
- **Main branch name** — Usually `main` or `master`

Configuration is stored in `~/.config/ezstack/config.json`.

**Subcommands**

```
ezs config set <key> <value>    Set a configuration value
ezs config show                 Show current configuration
```

**Available keys:** `worktree_base_dir`, `default_base_branch`, `github_token`, `cd_after_new`

---

## Commands

### `ezs new`

Create a new branch in the stack.

```
ezs new [branch-name] [options]

Options:
    -p, --parent <branch>     Parent branch (defaults to current branch)
    -w, --worktree <path>     Worktree path
    -c, --cd                  Change to the new worktree after creation
    -C, --no-cd               Don't change to the new worktree
    -f, --from-worktree       Register an existing worktree as a stack root
    -r, --from-remote         Create a stack from a remote branch
```

<details>
<summary>💡 Examples</summary>

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

<!-- ![ezs new](./assets/new.png) -->

---

### `ezs status`

Show status of current stack with PR and CI info.

```
ezs status [options]

Options:
    -a, --all     Show all stacks
```

<details>
<summary>💡 Examples</summary>

```bash
# Show current stack status
ezs status

# Show all stacks
ezs status -a
```

</details>

<!-- ![ezs status](./assets/status.png) -->

---

### `ezs list`

List all stacks and branches.

```
ezs list [options]
ezs ls [options]

Options:
    -a, --all     Show all stacks
```

<details>
<summary>💡 Examples</summary>

```bash
# List current stack
ezs list

# List all stacks
ezs ls -a
```

</details>

<!-- ![ezs list](./assets/list.png) -->

---

### `ezs sync`

Sync stack with remote. Handles rebasing onto updated parents, cleaning up merged branches, and force pushing after rebase.

```
ezs sync [options]

Options:
    -a, --all              Sync current stack (auto-detect what needs syncing)
    --all-stacks           Sync ALL stacks (not just current stack)
    -cur, --current        Sync current branch only
    -p, --parent           Rebase current branch onto its parent
    -c, --children         Rebase child branches onto current branch
    --no-delete-local      Don't delete local branches after their PRs are merged
```

<details>
<summary>💡 Examples</summary>

```bash
# Interactive sync menu
ezs sync

# Auto-sync current stack
ezs sync -a

# Sync all stacks
ezs sync --all-stacks

# Sync only current branch
ezs sync -cur

# Rebase current branch onto its parent
ezs sync -p

# Rebase children onto current branch
ezs sync -c
```

</details>

<!-- ![ezs sync](./assets/sync.png) -->

---

### `ezs goto`

Navigate to a branch worktree.

```
ezs goto [branch-name]
ezs go [branch-name]
```

If branch-name is omitted, shows interactive selection of all worktrees.

<details>
<summary>💡 Examples</summary>

```bash
# Interactive worktree selection
ezs goto

# Go to a specific branch
ezs go feature-1
```

</details>

<!-- ![ezs goto](./assets/goto.png) -->

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
<summary>💡 Examples</summary>

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

<!-- ![ezs pr create](./assets/pr-create.png) -->

---

### `ezs delete`

Delete a branch and its worktree.

```
ezs delete [branch-name]
ezs rm [branch-name]

Options:
    -f, --force    Force delete even if branch has children
```

<details>
<summary>💡 Examples</summary>

```bash
# Interactive branch selection
ezs delete

# Delete a specific branch
ezs rm feature-1

# Force delete a branch with children
ezs delete feature-1 -f
```

</details>

<!-- ![ezs delete](./assets/delete.png) -->

---

### `ezs reparent`

Change the parent of a branch.

```
ezs reparent [branch] [new-parent] [options]
ezs rp [branch] [new-parent] [options]

Options:
    -b, --branch <name>     Branch to reparent
    -p, --parent <name>     New parent branch
    -n, --no-rebase         Don't rebase, just update metadata
```

<details>
<summary>💡 Examples</summary>

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

<!-- ![ezs reparent](./assets/reparent.png) -->

---

### `ezs stack`

Add an untracked branch/worktree to an existing stack.

```
ezs stack [branch] [parent] [options]

Options:
    -b, --branch <name>     Branch to add to stack
    -p, --parent <name>     Parent branch in the stack
```

<details>
<summary>💡 Examples</summary>

```bash
# Interactive mode
ezs stack

# Add my-branch under feature-1
ezs stack my-branch feature-1

# Add using flags
ezs stack -b my-branch -p main
```

</details>

<!-- ![ezs stack](./assets/stack.png) -->

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
<summary>💡 Examples</summary>

```bash
# Interactive mode
ezs unstack

# Untrack a specific branch
ezs unstack feature-1
```

</details>

<!-- ![ezs unstack](./assets/unstack.png) -->

---

### `ezs update`

Reconcile ezstack config with git reality.

```
ezs update [options]

Options:
    -a, --auto        Auto-accept all changes without prompting
    -d, --dry-run     Show what would be changed without making changes
```

This command:
- Removes branches from config if their worktree folder was deleted
- Removes branches from config if the git branch no longer exists
- Offers to add worktrees that exist but aren't tracked

<details>
<summary>💡 Examples</summary>

```bash
# Interactive mode
ezs update

# Auto-accept all changes
ezs update --auto

# Preview changes without applying
ezs update --dry-run
```

</details>

<!-- ![ezs update](./assets/update.png) -->

---

### `ezs config`

Configure ezstack for the current repository.

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
<summary>💡 Examples</summary>

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

<!-- ![ezs config](./assets/config.png) -->

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
```

<!-- ![Stacked PR workflow](./assets/workflow-stacked-pr.gif) -->

---

### After Parent is Merged

```bash
# Sync will detect merged parents and rebase
ezs sync -a
```

<!-- ![Sync after merge](./assets/workflow-sync.gif) -->
