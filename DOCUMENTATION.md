<div align="center">

# ezstack docs

**Comprehensive guide to using ezstack**

</div>

---

**Table of Contents**

[Overview](#overview) · [Installation](#installation) · [Configuration](#configuration) · [Commands](#commands) · [Workflows](#workflows)

**Commands:** [new](#ezs-new) · [status](#ezs-status) · [list](#ezs-list) · [sync](#ezs-sync) · [goto](#ezs-goto) · [up/down](#ezs-up--ezs-down) · [pr](#ezs-pr) · [commit/amend](#ezs-commit--ezs-amend) · [delete](#ezs-delete) · [reparent](#ezs-reparent) · [stack](#ezs-stack) · [unstack](#ezs-unstack) · [update](#ezs-update) · [config](#ezs-config)

---

## Overview

ezstack is a CLI tool for managing stacked pull requests. It supports two workflow modes:

- **Worktree mode (default):** Each branch lives in its own git worktree, allowing parallel work across the stack
- **Checkout mode:** Branches use standard `git checkout`, keeping a single working directory

**Key Concepts**

- **Stack** — A chain of branches where each branch builds on its parent
- **Worktree** — A separate working directory for each branch (optional)
- **Sync** — Rebase branches when parents are merged or updated
- **Auto-restack** — `ezs commit` and `ezs amend` automatically rebase children

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

**Shell integration (recommended)**

Add to your shell configuration:

```bash
# bash
echo 'eval "$(ezs --shell-init)"' >> ~/.bashrc

# zsh
echo 'eval "$(ezs --shell-init)"' >> ~/.zshrc
```

This enables automatic directory changes for `goto`, `new`, `delete`, `sync`, `up`, `down`, `commit`, and `amend` commands.

Without shell integration, commands that would change your directory will instead print a helpful message with the path to `cd` to manually.

---

## Configuration

Run `ezs config` in your repository to configure:

- **Use worktrees** — Whether to create worktrees for new branches (default: yes)
- **Worktree base directory** — Where branch worktrees will be created
- **Main branch name** — Usually `main` or `master`
- **Auto-cd** — Whether to cd into new worktrees after creation (default: yes)

Configuration is stored in `~/.ezstack/config.json`.

**Subcommands**

```
ezs config set <key> <value>    Set a configuration value
ezs config show                 Show current configuration
```

**Available keys:** `worktree_base_dir`, `default_base_branch`, `cd_after_new`, `use_worktrees`

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

When `use_worktrees` is disabled, creates a git branch without a worktree and optionally checks it out.

---

### `ezs status`

Show status of current stack with PR and CI info. Aliases: `st`

```
ezs status [options]

Options:
    -a, --all     Show all stacks
    -d, --debug   Show debug output
```

---

### `ezs list`

List all stacks and branches. Aliases: `ls`

```
ezs list [options]

Options:
    -a, --all     Show all stacks
    --json        Output as JSON (machine-readable)
    -d, --debug   Show debug output
```

The `--json` flag outputs stack structure to stdout for editor integrations and scripts.

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
    --dry-run              Preview what would be synced without making changes
    --autostash            Stash uncommitted changes before rebase, pop after
    --json                 Output dry-run results as JSON (requires --dry-run)
```

You can sync a specific stack by passing its hash prefix (minimum 3 characters).

---

### `ezs goto`

Navigate to a branch worktree. Aliases: `go`

```
ezs goto [branch-name]
```

If branch-name is omitted, shows interactive selection. Falls back to `git checkout` when the branch has no worktree.

---

### `ezs up` / `ezs down`

Navigate up (toward parent/base) or down (toward children/leaves) in the stack.

```
ezs up [n]      Navigate n levels toward parent (default: 1)
ezs down [n]    Navigate n levels toward children (default: 1)
```

When navigating down with multiple children, shows an interactive selector.

---

### `ezs pr`

Manage pull requests.

```
ezs pr <subcommand> [options]

Subcommands:
    create    Create a new pull request
    update    Push changes to existing PR
    merge     Merge a pull request
    draft     Toggle PR between draft and ready
    stack     Update all PR descriptions with stack info
```

#### `ezs pr create`

```
Options:
    -t, --title <title>    PR title (defaults to branch name)
    -b, --body <body>      PR body/description
    -d, --draft            Create as draft PR
```

#### `ezs pr merge`

```
Options:
    -m, --method <method>      Merge method: merge, squash, rebase (default: interactive)
    --no-delete-branch         Don't delete the remote branch after merge
```

#### `ezs pr draft`

Toggles the current branch's PR between draft and ready-for-review state.

---

### `ezs commit` / `ezs amend`

Wrap `git commit` / `git commit --amend` and auto-sync child branches. Aliases: `ci`

```
ezs commit [git-commit-options]
ezs amend [git-commit-options]
```

All arguments are passed through to `git commit`. After committing, any child branches in the stack are automatically rebased onto the updated branch.

---

### `ezs delete`

Delete a branch and its worktree. Aliases: `del`, `rm`

```
ezs delete [branch-name] [options]
ezs delete [stack-hash] [options]

Options:
    -f, --force            Force delete even if branch has children
    -s, --stack            Treat argument as a stack hash (delete entire stack)
```

---

### `ezs reparent`

Change the parent of a branch. Always rebases onto the new parent. Aliases: `rp`

```
ezs reparent [branch] [new-parent] [options]

Options:
    -b, --branch <name>     Branch to reparent
    -p, --parent <name>     New parent branch
```

---

### `ezs stack`

Add an untracked branch/worktree to an existing stack, start a new stack, or rename a stack.

```
ezs stack [branch] [parent] [options]
ezs stack rename [stack-hash] [name]

Options:
    -b, --branch <name>     Branch to add to stack
    -p, --parent <name>     Parent branch in the stack
    -B, --base <name>       Base branch for a new stack (e.g. develop, staging)
```

---

### `ezs unstack`

Remove a branch from stack tracking without deleting the git branch or worktree.

```
ezs unstack [branch] [options]

Options:
    -b, --branch <name>     Branch to untrack
```

---

### `ezs update`

Reconcile ezstack config with git reality.

```
ezs update [options]

Options:
    -a, --auto        Auto-accept all changes without prompting
    -d, --dry-run     Show what would be changed without making changes
```

Detects renamed branches, removes orphaned entries, and cleans up stale config.

---

### `ezs config`

Configure ezstack for the current repository. Aliases: `cfg`

```
ezs config [subcommand] [options]

Subcommands:
    set <key> <value>    Set a configuration value
    show                 Show current configuration
```

---

## Manual Git Operations

If you rename or delete branches outside of ezstack, run `ezs update` to reconcile:

```bash
git branch -m old-name new-name
ezs update    # detects the rename, preserves stack position and PR metadata

git branch -D some-branch
ezs update    # removes orphaned branch from config
```

---

## Workflows

### Creating a Stacked PR

```bash
ezs new feature-1
# make changes
ezs commit -m "Add feature part 1"
ezs new feature-2 --parent feature-1
# make changes
ezs commit -m "Add feature part 2"

# Create PRs for the whole stack
ezs pr create -t "Part 1: Add feature"
ezs goto feature-2
ezs pr create -t "Part 2: Add feature"

# Update all PR descriptions with stack info
ezs pr stack
```

### After Parent is Merged

```bash
# Sync will detect merged parents and rebase
ezs sync -a

# Or merge from the CLI and sync
ezs pr merge -m squash
ezs goto feature-2
ezs sync -a
```

### Navigating the Stack

```bash
# Move between branches
ezs up        # go to parent
ezs down      # go to child
ezs up 2      # go up two levels
ezs goto feature-1   # jump to a specific branch
```

### Stacking on a Remote PR

```bash
ezs stack
# Select "Start a new stack from a remote PR"
# Pick the PR, then pick your branch to stack on top
```
