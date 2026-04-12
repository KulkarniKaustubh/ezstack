<div align="center">

# ezstack docs

**Comprehensive guide to using ezstack**

</div>

---

**Table of Contents**

[Overview](#overview) · [Installation](#installation) · [Configuration](#configuration) · [Commands](#commands) · [Workflows](#workflows)

**Commands:** [agent](#ezs-agent) · [new](#ezs-new) · [status](#ezs-status) · [list](#ezs-list) · [sync](#ezs-sync) · [goto](#ezs-goto) · [up/down](#ezs-up--ezs-down) · [pr](#ezs-pr) · [commit/amend](#ezs-commit--ezs-amend) · [push](#ezs-push) · [diff](#ezs-diff) · [delete](#ezs-delete) · [reparent](#ezs-reparent) · [stack](#ezs-stack) · [unstack](#ezs-unstack) · [config](#ezs-config)

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

This enables automatic directory changes for `goto`, `new`, `delete`, `sync`, `up`, and `down` commands.

Without shell integration, commands that would change your directory will instead print a helpful message with the path to `cd` to manually.

---

## Configuration

Run `ezs config` in your repository to configure:

- **Use worktrees** — Whether to create worktrees for new branches (default: yes)
- **Worktree base directory** — Where branch worktrees will be created
- **Main branch name** — Usually `main` or `master`
- **Auto-cd** — Whether to cd into new worktrees after creation (default: yes)
- **Sync strategy** — Whether to use `rebase` (default) or `merge` when syncing branches

Configuration is stored in `~/.ezstack/config.json`.

**Subcommands**

```
ezs config set <key> <value>    Set a configuration value
ezs config show                 Show current configuration
```

**Available keys:** `worktree_base_dir`, `default_base_branch`, `cd_after_new`, `use_worktrees`, `sync_strategy`

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

### `ezs agent`

Launch an AI agent with full stack context. The agent is scoped to a single stack and receives stack structure, branch info, and ezstack documentation automatically.

```
ezs agent [options]
ezs agent feature "description"
ezs agent prompt <flag> <work|feature>

Modes:
    (default)   Work session — agent scoped to a stack with full context
    feature     Feature builder — agent breaks a feature into stacked branches
    prompt      View or edit the prompt templates used by the agent

Options:
    --cmd <command>      Agent CLI to use (default: configured or "claude")
    -s, --stack <hash>   Stack to work on (hash prefix or "name")
    -b, --branch <name>  Branch to work in (implies stack)
    --dry-run            Print the composed prompt and exit (don't launch agent)
```

#### Prompt Composition

The final agent prompt is composed from three layers:

1. **Shipped prompt** — built into ezstack, updated with releases
2. **Custom instructions** — `~/.ezstack/agent-{work,feature}-prompt.md` (personal, all repos)
3. **Repo instructions** — `<repo>/.ezstack/agent-{work,feature}-prompt.md` (per-repo, committable)

Custom and repo instructions are injected into the shipped prompt. To fully override the shipped prompt, add `override: full` as the first line of your custom instructions file. Repo instructions are still injected.

These files use template variables that are replaced at runtime:

| Variable | Description |
|----------|-------------|
| `{{STACK_JSON}}` | Current stack structure as JSON |
| `{{BRANCH_NAME}}` | Current branch name |
| `{{PARENT_NAME}}` | Parent branch name |
| `{{WORKTREE_PATH}}` | Path to the current worktree |
| `{{EZS_COMMANDS}}` | Available ezs commands reference |
| `{{EZS_DOCS}}` | Full ezstack documentation for AI agents |
| `{{FEATURE_DESCRIPTION}}` | Feature description (feature mode only) |
| `{{CUSTOM_INSTRUCTIONS}}` | Custom instructions slot |
| `{{REPO_INSTRUCTIONS}}` | Repository instructions slot |

#### `ezs agent prompt`

View or edit the prompt templates. Requires a positional argument: `work` or `feature`.

```
Flags:
    --shipped            View the shipped (built-in) prompt template
    --custom             View your custom instructions (~/.ezstack/)
    --repo               View or target repo-specific instructions (<repo>/.ezstack/)
    --edit               Edit custom instructions (combine with --repo for repo-specific)
    --reset              Delete custom instructions (combine with --repo for repo-specific)
```

**Examples:**

```bash
# View the shipped work prompt
ezs agent prompt --shipped work

# View your custom work instructions
ezs agent prompt --custom work

# Edit custom work instructions
ezs agent prompt --edit work

# Edit repo-specific work instructions
ezs agent prompt --edit --repo work

# Reset custom work instructions
ezs agent prompt --reset work

# Reset repo-specific feature instructions
ezs agent prompt --reset --repo feature
```

#### Configuration

```bash
# Set the agent CLI (default: claude)
ezs config set agent_command claude
```

---

### `ezs commit` / `ezs amend`

Wrap `git commit` / `git commit --amend` and auto-sync child branches. Aliases: `ci`

```
ezs commit [git-commit-options] [--merge|--rebase]
ezs amend [git-commit-options] [--merge|--rebase]
```

All arguments are passed through to `git commit`. After committing, any child branches in the stack are automatically synced onto the updated branch.

Uses the configured `sync_strategy` (default: rebase) for child syncing. Use `--merge` or `--rebase` to override.

---

### `ezs config`

Configure ezstack for the current repository. Aliases: `cfg`

```
ezs config [subcommand] [options]

Subcommands:
    set <key> <value>    Set a configuration value
    show                 Show current configuration
```

**Available keys for `set`:**

| Key | Description | Values |
|-----|-------------|--------|
| `worktree_base_dir` | Base directory for worktrees | Path (per-repo) |
| `default_base_branch` | Default base branch | e.g. `main`, `master` |
| `cd_after_new` | Auto-cd to new worktree | `true` / `false` (per-repo) |
| `use_worktrees` | Use git worktrees for new branches | `true` / `false` (per-repo) |
| `sync_strategy` | Sync method for rebase/merge | `rebase` / `merge` (per-repo) |
| `github_token` | GitHub token for API access | Token string |

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

### `ezs diff`

Show diff against parent branch.

```
ezs diff [options] [-- git-diff-options]

Options:
    --stat         Show diffstat only
```

Shows the diff between the current branch and its parent in the stack. Any arguments after `--` are passed directly to `git diff`.

---

### `ezs down` / `ezs up`

Navigate down (toward children/leaves) or up (toward parent/base) in the stack.

```
ezs down [n]    Navigate n levels toward children (default: 1)
ezs up [n]      Navigate n levels toward parent (default: 1)
```

When navigating down with multiple children, shows an interactive selector.

---

### `ezs goto`

Navigate to a branch worktree. Aliases: `go`

```
ezs goto [branch-name]
```

If branch-name is omitted, shows interactive selection. Falls back to `git checkout` when the branch has no worktree.

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

The list view also shows diff stats (+/-) for each branch relative to its parent, giving a quick sense of change size across the stack.

---

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
    -r, --from-remote         Create a stack from a remote branch/PR
```

With `origin/<branch>`, creates a local worktree tracking the remote branch and registers it in a stack (root = PR base branch, or `main` by default). The branch is marked as `(remote)` in `ezs ls` output. All commands (sync, push, commit, etc.) work normally on it.
```bash
ezs new origin/feature-branch       # Checkout remote branch into a worktree + register stack
```

This fetches the latest remote refs, creates a local tracking branch, sets up a worktree, and registers the branch in ezstack's config. If the branch has an associated PR, it displays PR info (title, state, review status) and a line diff summary against the base branch.

With `--from-remote`, positional args are `[pr-number-or-branch] [new-branch-name]`:
```bash
ezs new -r                          # Interactive PR selection + branch name prompt
ezs new -r 42                       # Use PR #42, prompt for branch name
ezs new -r feature-branch           # Use PR for that branch, prompt for branch name
ezs new -r 42 my-feature            # Use PR #42, create branch "my-feature" (no prompts)
```

When `use_worktrees` is disabled, creates a git branch without a worktree and optionally checks it out.

---

### `ezs pr`

Manage pull requests.

```
ezs pr <subcommand> [options]

Subcommands:
    create    Create a new pull request
    draft     Toggle PR between draft and ready
    merge     Merge a pull request
    stack     Update all PR descriptions with stack info
    update    Push changes and update PR metadata (base branch, descriptions)
```

#### `ezs pr create`

```
Options:
    -t, --title <title>    PR title (defaults to branch name)
    -b, --body <body>      PR body/description
    -d, --draft            Create as draft PR
```

#### `ezs pr draft`

Toggles the current branch's PR between draft and ready-for-review state.

#### `ezs pr merge`

```
Options:
    -m, --method <method>      Merge method: merge, squash, rebase (default: interactive)
    --no-delete-branch         Don't delete the remote branch after merge
```

---

### `ezs push`

Push current branch or entire stack to remote.

```
ezs push [options]

Options:
    -s, --stack    Push all branches in the current stack
    -f, --force    Force push
```

---

### `ezs reparent`

Change the parent of a branch and sync commits onto the new parent. Aliases: `rp`

```
ezs reparent [branch] [new-parent] [options]

Options:
    -b, --branch <name>     Branch to reparent
    -p, --parent <name>     New parent branch
    --merge                 Use git merge instead of git rebase
    --rebase                Use git rebase (overrides sync_strategy config)
```

Uses the configured `sync_strategy` (default: rebase). If the sync conflicts, the reparent metadata is still updated and you can resolve conflicts manually.

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

### `ezs status`

Show status of current stack with PR and CI info. Aliases: `st`

```
ezs status [options]

Options:
    -a, --all     Show all stacks
    -d, --debug   Show debug output
```

---

### `ezs sync`

Sync stack with remote. Handles rebasing onto updated parents, cleaning up merged branches, and force pushing after rebase.

```
ezs sync [options]
ezs sync <hash-prefix>

Options:
    -s, --stack            Sync current stack (auto-detect what needs syncing)
    -a, --all              Sync ALL stacks
    -c, --current          Sync current branch only (auto-detect what it needs)
    -p, --parent           Rebase current branch onto its parent
    -C, --children         Rebase child branches onto current branch
    --merge                Use git merge instead of git rebase
    --rebase               Use git rebase (overrides sync_strategy config)
    --no-delete-local      Don't delete local branches after their PRs are merged
    --dry-run              Preview what would be synced without making changes
    --continue             Continue after resolving conflicts
    --no-autostash         Don't stash uncommitted changes before rebase (autostash is on by default)
    --json                 Output dry-run results as JSON (requires --dry-run)
```

By default, sync uses git rebase. Use `--merge` to use git merge instead, which preserves commit history and avoids force pushes. The default strategy can be set per-repo with `ezs config set sync_strategy merge`. Use `--rebase` or `--merge` to override the configured strategy for a single run.

You can sync a specific stack by passing its hash prefix (minimum 3 characters).

---

### `ezs unstack`

Remove a branch from stack tracking without deleting the git branch or worktree.

```
ezs unstack [branch] [options]

Options:
    -b, --branch <name>     Branch to untrack
```

---

## Manual Git Operations

If you rename or delete branches outside of ezstack, the next `ezs` command will automatically detect the change and reconcile config:

```bash
git branch -m old-name new-name
ezs status    # auto-detects the rename, preserves stack position and PR metadata

git branch -D some-branch
ezs ls        # auto-removes orphaned branch from config
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

### Reviewing a Remote PR

```bash
# Checkout a teammate's branch into its own worktree
# Registers a stack with the PR's base branch as root
ezs new origin/feature-branch

# ezstack fetches, creates a tracking worktree, and shows:
#   PR #42: Add user authentication
#   URL: https://github.com/you/repo/pull/42
#   State: OPEN  Base: main
#   Review: REVIEW_REQUIRED
#   Diff vs main: +320 / -45 lines

# The branch shows up in ezs ls with a (remote) tag
# You can work on it, push changes, sync — all commands work

# When you're done, clean up
ezs delete feature-branch
```

### Stacking on a Remote PR

```bash
ezs stack
# Select "Start a new stack from a remote PR"
# Pick the PR, then pick your branch to stack on top
```
