<div align="center">

# ezstack docs

**Comprehensive guide to using ezstack**

</div>

---

**Table of Contents**

[Overview](#overview) · [Installation](#installation) · [Configuration](#configuration) · [Commands](#commands) · [Workflows](#workflows) · [Editor & Desktop Integrations](#editor--desktop-integrations)

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

## Getting Started

A guided path from zero to a live stacked PR. Every command and every
parenthetical describes what ezstack actually does — no glossed-over details.

### 1. Install the binary

Pick whichever fits your setup. Both drop `ezs` (and `ezs-mcp`, if you also
want the MCP server) onto your `PATH` without cloning the repo.

```bash
# Homebrew — macOS / Linux
brew install KulkarniKaustubh/ezstack/ezstack

# Go toolchain — Go 1.25+ required (matches the module's go directive)
go install github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs@latest
```

You'll also need `git` 2.20+, [`fzf`](https://github.com/junegunn/fzf) for
interactive selection prompts, and the [GitHub CLI](https://cli.github.com/)
(`gh`) for any PR-related commands.

Verify the install:

```bash
ezs --version
# → ezstack version 4.3.5
```

### 2. Wire up shell integration

A handful of ezstack commands need to change your shell's working directory:
`goto`, `new`, `delete`, `sync`, `up`, `down`, and `menu` (these are the
commands the shell wrapper intercepts in `cmd/ezs/main.go`). A one-line eval in
your rc file installs the wrapper.

```bash
# zsh
echo 'eval "$(ezs --shell-init)"' >> ~/.zshrc && exec zsh

# bash
echo 'eval "$(ezs --shell-init)"' >> ~/.bashrc && exec bash
```

Without the wrapper installed, those commands still run — they just print
something like `cd /path/to/worktree` instead of actually moving your shell,
and you'd have to copy-paste the path yourself.

### 3. Configure the repo

`ezs config` (with no subcommand) launches an interactive first-run wizard
that writes settings into `~/.ezstack/config.json` — one global section plus a
per-repo section keyed by the repository's absolute path.

```bash
cd ~/code/my-project
ezs config
```

The wizard asks these prompts, in order:

| # | Prompt | What it sets | Default | Notes |
|---|---|---|---|---|
| 1 | Use git worktrees for new branches (recommended) | `use_worktrees` (per-repo) | **yes** | Required for `ezs agent`. Strongly recommended; enables parallel work across the stack. |
| 2 | Worktree base directory (where new worktrees will be created) | `worktree_base_dir` (per-repo) | `<parent-of-repo>/<repo-name>_worktrees` | Only asked if you said yes to step 1. Must be **outside** the repo itself — the wizard re-prompts if you point it inside. |
| 3 | Auto-cd into new worktrees after creation | `cd_after_new` (per-repo) | **yes** | Only effective if the shell wrapper from step 2 is installed. |
| 4 | Select your sync strategy | `sync_strategy` (per-repo) | **`merge`** | The wizard explicitly recommends `merge` because `rebase` rewrites history and forces a force-push on every sync. The two options shown are `merge` and `rebase`. |
| 5 | AI agent CLI command (used by `ezs agent`) | `agent_command` (per-repo) | **`claude`** | The literal command name `ezs agent` will exec inside a worktree. Set to `aider`, `cursor-agent`, `codex`, etc. if you want a different CLI. |

The wizard does **not** prompt for the base branch name. `default_base_branch`
is a global setting that defaults to the literal string `"main"` if unset
(see `cmd/ezs/commands/config.go`); change it with `ezs config set` below if
your repo uses something else.

You can change any setting later without re-running the wizard:

```bash
ezs config show                                    # dump global + active-repo config
ezs config set use_worktrees true                  # toggle worktree mode
ezs config set worktree_base_dir ~/code/wt         # move the worktree root
ezs config set cd_after_new true                   # toggle auto-cd
ezs config set sync_strategy rebase                # switch to rebase-based sync
ezs config set agent_command aider                 # switch the agent CLI
ezs config set default_base_branch master          # override the global default base
ezs config set github_token ghp_...                # optional; otherwise falls back to `gh auth`
```

### 4. Create your first stacked branch

`ezs new <name>` creates a branch whose parent defaults to **your current
branch**, not `main`. So if you want a fresh stack rooted on `main`, make sure
you're on `main` first (or pass `--parent main` explicitly).

```bash
git checkout main          # or:  ezs new feature-1 --parent main
ezs new feature-1
#   What this actually does (see cmd/ezs/commands/new.go):
#     - reads use_worktrees / worktree_base_dir from the per-repo config
#     - creates branch `feature-1` from the current branch (no implicit
#       `git fetch` — work off whatever your local main currently points at)
#     - if use_worktrees=true: `git worktree add <worktree_base_dir>/feature-1 feature-1`
#     - records the branch in ~/.ezstack/stacks.json with parent=<your previous branch>
#     - if this is the first branch in a new stack, prompts you for a stack name
#     - if cd_after_new=true and the shell wrapper is installed: cd into the worktree
```

Make some edits, then commit. `ezs commit` is a thin wrapper over `git commit`
that also rebases child branches and offers to push for you.

```bash
ezs commit -am "Scaffold feature"
#   What this actually does (see cmd/ezs/commands/commit.go):
#     1. Runs `git commit -am "..."` interactively (your editor still works for
#        long messages — the -m here just bypasses it).
#     2. If the branch already exists on the remote, prompts:
#          "Push to remote? [Y/n]"
#        — answer Y to push, or n to defer. (Amend prompts for `--force` push.)
#     3. Looks up child branches in stacks.json and rebases (or merges,
#        per sync_strategy) each child onto the new tip. On a brand-new
#        leaf branch this is a no-op.
```

Stack a second branch on top:

```bash
ezs new feature-2 --parent feature-1
# ... edit files in the feature-2 worktree ...
ezs commit -am "Wire up feature"
```

### 5. Push the stack and open PRs

```bash
ezs push --stack
#   What this actually does (see cmd/ezs/commands/push.go):
#     - walks every branch in the current stack, root → leaf, skipping any
#       branches already marked merged
#     - for each: `git push -u <remote> <branch>` (plain push, sets upstream)
#     - skips branches whose remote is "no-push" (fork PRs without
#       maintainer-push permission) with a warning
#     - add `--force` (or `-f`) to switch to `git push -u --force-with-lease`,
#       which is what you want after rebases or amends
```

Open a PR for each branch. The base branch ezstack passes to `gh pr create`
is the branch's recorded **parent** in `stacks.json`, NOT `main`. So
`feature-2`'s PR targets `feature-1` and the diff only shows feature-2's
commits.

```bash
ezs goto feature-1
ezs pr create -t "Part 1: scaffolding"

ezs goto feature-2
ezs pr create -t "Part 2: wire it up"
#   - shells out to `gh pr create --base <parent> --head <branch> --title "..."`
#   - records the PR number back into stacks.json
#   - add `-d` / `--draft` to open as a draft
```

Now inject stack-navigation links into every PR description so reviewers can
hop around the stack:

```bash
ezs pr stack
#   - calls gh.UpdateStackDescription for every PR in the stack
#     (see cmd/ezs/commands/utils.go and the github client)
#   - rewrites each PR body so it contains a managed block listing every
#     branch in the stack, with the active branch marked
```

### 6. Inspect the stack any time

```bash
ezs status        # current branch + its position in the stack + PR / CI status
ezs ls            # alias for `ezs list` — every stack in the repo, tree-formatted
ezs diff          # diff of the current branch against its parent (numstat / diffstat)
ezs log           # commits on the current branch since its parent (hash, msg, author, date)
```

That's the whole flow. For day-two operations (merged parents, reviewing
remote PRs, stacking on top of someone else's work) jump to
[Workflows](#workflows), or [Commands](#commands) for the full reference of
every flag on every subcommand.

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
go install github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs@latest
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

## Workflows

Real end-to-end flows, annotated so you can see exactly what ezstack is doing to
your git state on every step. If you want the full reference for any individual
command, jump to the [Commands](#commands) section below.

### Creating a Stacked PR

The canonical flow: build two dependent branches, push them, open linked PRs.
Every `ezs` line is a thin shell over git — the comments show the underlying
operation so there is no magic.

```bash
# 1. Start a new stack rooted on the default base branch (main).
#    - Fetches origin/main
#    - Creates branch `feature-1` pointing at origin/main
#    - Creates a worktree at <worktree_base_dir>/feature-1/ (if use_worktrees=true)
#    - Records the branch in ~/.ezstack/stacks.json with parent=main
#    - cd's your shell into the new worktree
ezs new feature-1

# ... edit files in the feature-1 worktree ...

# 2. Commit. Equivalent to `git add -A && git commit -m ...` followed by an
#    automatic `ezs sync --children` so any descendant branches get rebased
#    onto the new tip. On a brand-new branch there are no children yet, so
#    this is just: stage + commit.
ezs commit -am "Add feature part 1"

# 3. Stack a second branch on top of feature-1.
#    - Creates branch `feature-2` pointing at feature-1 (not main)
#    - Creates a second worktree at <worktree_base_dir>/feature-2/
#    - Records parent=feature-1 in stacks.json
#    - cd's into the feature-2 worktree
ezs new feature-2 --parent feature-1

# ... edit files in the feature-2 worktree ...

ezs commit -am "Add feature part 2"

# 4. Push the whole stack to the remote in one shot.
#    - Walks the stack from root to leaf
#    - For each branch: `git push --force-with-lease origin <branch>`
#    - Sets upstream on the first push
ezs push --stack

# 5. Open a pull request for each branch. The base of each PR is the parent
#    branch recorded in stacks.json, NOT main — so feature-2's PR targets
#    feature-1, and only shows the feature-2 diff.
ezs goto feature-1
ezs pr create -t "Part 1: scaffolding"
ezs goto feature-2
ezs pr create -t "Part 2: wire it up"

# 6. Write stack-navigation links into every PR description.
#    - Fetches each PR body via `gh pr view`
#    - Injects a managed block listing every branch in the stack with
#      links and ✅ / 🔵 markers for merged / current
#    - Pushes the updated bodies back with `gh pr edit`
ezs pr stack
```

### Committing into the middle of an existing stack

Dependent branches below you would normally get left behind on the old tip of
`feature-1`. `ezs commit` handles this automatically.

```bash
ezs goto feature-1
ezs commit -am "Address review comment on part 1"
# What this does under the hood:
#   - git add -A && git commit -m "..."
#   - For each descendant (feature-2, feature-3, ...):
#       git rebase --onto <new feature-1 tip> <old feature-1 tip> <descendant>
#     so their worktrees now sit on top of the amended parent.
#   - If the branch is already on the remote, it auto-force-pushes the branch
#     and every descendant, so the open PRs update in one shot.
```

### After a Parent is Merged

When an upstream PR (or a parent branch in the stack) lands on `main`, the
descendants need to be re-rooted. `ezs sync` does the surgery.

```bash
# Case A: GitHub merged the PR (squash/rebase/merge — all handled).
#   - Fetches origin/main
#   - Detects that feature-1 has been merged (matching commit on main OR
#     the PR's mergedAt field via `gh pr view --json`)
#   - Drops feature-1 from the stack
#   - Rebases feature-2 onto main: `git rebase --onto main <old feature-1> feature-2`
#   - Updates stacks.json so feature-2's parent is now main
#   - Deletes the merged local branch + worktree (unless you `cd`'d elsewhere)
ezs sync --all

# Case B: you want to merge from the CLI and keep the stack clean in one go.
ezs goto feature-1
ezs pr merge -m squash    # shells out to `gh pr merge --squash`
ezs goto feature-2
ezs sync --all            # same reparent-onto-main as Case A
```

### Navigating the Stack

All navigation uses worktrees when `use_worktrees=true`, so switching branches
never touches your working tree — it literally `cd`s into the other worktree
directory. No stashes, no file churn.

```bash
ezs up               # parent:  `cd <worktree_base_dir>/<parent>`
ezs down             # child:   `cd <worktree_base_dir>/<child>`
ezs up 2             # grandparent (walks the stack twice)
ezs goto feature-1   # any branch by name; accepts fzf when run with no arg
```

### Reviewing a Remote PR

Pull down a teammate's PR into an isolated worktree so you can run it, poke at
it, and still commit back if you have maintainer access — all without touching
your own stack.

```bash
# Checkout someone else's branch into a fresh stack.
#   - Runs `git fetch origin <branch>` (or the PR's head ref for fork PRs)
#   - Creates a local branch tracking that ref
#   - Creates a worktree for it
#   - Looks up the PR via `gh pr view --json` and records it in stacks.json
#     with its base branch as the stack root
#   - Prints a summary panel (PR title, URL, state, review status, +/- diff)
ezs new origin/feature-branch

# The branch now shows up in `ezs ls` with a (remote) tag, and every ezs
# command works on it — you can edit, `ezs commit`, `ezs push`, `ezs sync`.

# For fork PRs, ezstack auto-detects maintainer-push capability:
#   - If the PR has "Allow edits from maintainers" AND you have write access
#     to the fork → adds the fork as a git remote and pushes there.
#   - Otherwise → the branch is marked read-only so `ezs push` / `ezs sync`
#     won't try to publish commits you can't land.

# When you're done, blow away the worktree and the local branch in one call.
ezs delete feature-branch
```

### Stacking on a Remote PR

When you need to build on top of a teammate's in-flight PR without waiting for
it to merge:

```bash
ezs stack
# Launches an interactive picker:
#   1. "Start a new stack from a remote PR"  → pick the PR via fzf
#   2. Pick a local branch (or create one)   → it gets reparented onto the PR
#
# Result in stacks.json:
#   <teammate-pr-branch>      parent=main      (remote, read-only)
#     └── <your-branch>        parent=<teammate-pr-branch>
#
# Your branch is now rebased on top of their work, and `ezs sync --all` will
# keep it up to date as they push new commits.
```

---

## Commands

### `ezs agent`

Launch an AI agent with full stack context. The agent is scoped to a single stack and receives stack structure, branch info, and ezstack documentation automatically. **Requires worktree mode** (`use_worktrees: true`) — the agent needs separate working directories for each branch to work in isolation without disrupting your workspace.

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
| `use_worktrees` | Use git worktrees for new branches (required for `ezs agent`) | `true` / `false` (per-repo) |
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
    -b, --branch <name>  Show diff for a specific branch (default: current)
    --stat               Show diffstat only
    --json               Output file-level diff stats as JSON
```

Shows the diff between a branch and its parent in the stack. Any arguments after `--` are passed directly to `git diff`. Use `--branch` to diff any branch without switching to it. Use `--json` for machine-readable output with per-file additions/deletions.

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

### `ezs log`

Show commits in a branch since its parent.

```
ezs log [options]

Options:
    -b, --branch <name>  Show log for a specific branch (default: current)
    --json               Output as JSON
```

Shows the commits that exist in a branch but not in its parent branch. Use `--branch` to view commits for any branch without switching to it. The `--json` flag outputs structured commit data (hash, message, author, date) for editor integrations.

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

**Fork PR handling:** When the PR comes from a fork, ezstack automatically:
- Detects the fork repository via the GitHub API
- Checks if "Allow edits from maintainers" is enabled on the PR
- Verifies that you have push access to the fork repo
- Adds a git remote for the fork (named after the fork owner) and fetches it
- All subsequent push/sync operations target the fork remote instead of `origin`

If the fork doesn't allow maintainer edits, or you don't have push access, the branch is marked as read-only — sync will still rebase/merge locally, but push is skipped with a warning.

With `--from-remote`, positional args are `[pr-number-or-branch] [new-branch-name]`:
```bash
ezs new -r                          # Interactive PR selection + branch name prompt
ezs new -r 42                       # Use PR #42, prompt for branch name
ezs new -r feature-branch           # Use PR for that branch, prompt for branch name
ezs new -r 42 my-feature            # Use PR #42, create branch "my-feature" (no prompts)
```

When `use_worktrees` is disabled, creates a git branch without a worktree and optionally checks it out. All core commands (`sync`, `commit`, `reparent`, `push`, `pr`) work fully in this mode via checkout-based sync. Note: `ezs agent` requires worktree mode.

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
    -s, --stack          Push all branches in the current stack
    -b, --branch <name>  Push a specific branch by name
    -f, --force          Force push
```

Each branch pushes to its configured remote — `origin` by default, or the fork remote for fork-based PR branches. Branches marked as read-only (fork PRs where you don't have push access) are skipped with a warning.

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
    -a, --all              Show all stacks
    -b, --branch <name>    Show status for a specific branch's stack
    -d, --debug            Show debug output
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
    -b, --branch <name>    Sync a specific branch by name (rebase onto parent + cascade to children)
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

**Fork branches:** After syncing, each branch is pushed to its configured remote. For fork-based PR branches, this is the fork's remote (not `origin`). If you don't have push access to the fork, the push step is skipped automatically — the local rebase/merge still happens so your working copy stays up to date.

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

## Editor & Desktop Integrations

ezstack ships with four first-party clients that wrap the `ezs` CLI: a VS Code
extension, a Neovim plugin, a desktop app, and an MCP server for AI agents.
They all read and write the same on-disk state (`~/.ezstack/stacks.json` and
per-repo config), so you can mix and match them freely &mdash; the CLI, your
editor, the desktop app, and Claude Code all stay in sync.

### MCP Server (Claude Code & other MCP clients)

Located in `cmd/ezs-mcp/`. A standalone Model Context Protocol server that
exposes the full stack workflow as MCP tools. Point any MCP-compatible agent
(Claude Code, Zed, etc.) at it and the agent can drive `ezs` directly &mdash;
inspect, mutate, navigate, and manage pull requests without leaving the agent
loop. 21 tools, one binary.

**Install**

```bash
# Homebrew (ships alongside ezs)
brew install KulkarniKaustubh/ezstack/ezstack

# Go install
go install github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs-mcp@latest

# From source
make install-mcp
```

**Register with Claude Code** (one registration, every repo):

```bash
claude mcp add ezstack --scope user -- ezs-mcp
```

`ezs-mcp` operates on whichever directory Claude Code launches it in, and
Claude launches MCP servers with the current project's directory as their
cwd — so a single user-scope registration works across every repo.

If you open Claude Code at a monorepo root but your ezstack-configured repo
is a subdirectory, Claude will launch `ezs-mcp` with the monorepo root as
cwd, which won't match any sub-repo. In that case, register a per-subrepo
entry with an absolute `--repo` path:

```bash
claude mcp add ezstack-foo -- ezs-mcp --repo /abs/path/to/foo
```

**Tools**

**Inspection**

| Tool | Annotation | Description |
|---|---|---|
| `ezstack_status` | read-only | Current stack with PR and CI status. `all`, `decorated`. |
| `ezstack_list` | read-only | List all stacks and branches. `all`, `decorated`. |
| `ezstack_diff` | read-only | Diff against parent branch as JSON numstat (default) or diffstat. `branch`, `stat`. |
| `ezstack_log` | read-only | Commits since parent as JSON (hash, message, author, ISO date). `branch`. |
| `ezstack_config_show` | read-only | Full ezstack configuration for the active repo. |

**Branch management**

| Tool | Annotation | Description |
|---|---|---|
| `ezstack_goto` | &mdash; | Switch to a branch. `branch` (required). |
| `ezstack_new` | &mdash; | Create a new branch. `name` (required), `parent`. |
| `ezstack_delete` | destructive | Delete a branch and its worktree. `branch` (required). |
| `ezstack_reparent` | &mdash; | Move a branch to a new parent. `branch` and `new_parent` (both required). |
| `ezstack_stack` | &mdash; | Add a standalone branch to a stack. `branch` (required), `parent` or `base`. |
| `ezstack_unstack` | &mdash; | Remove a branch from ezstack tracking (leaves the git branch/worktree intact). `branch` (required). |

**Committing & syncing**

| Tool | Annotation | Description |
|---|---|---|
| `ezstack_commit` | destructive | Commit staged (or all) changes and auto-sync children. `message` (required), `all`, `merge`, `rebase`. Auto-pushes if the branch is already on the remote. |
| `ezstack_amend` | destructive | Amend the last commit and auto-sync children. Optional `message` (otherwise `--no-edit`), `all`, `merge`, `rebase`. Force-pushes if the branch is already on the remote. |
| `ezstack_sync` | destructive | Rebase (or merge) branches with their base. `stack`, `all`, `current`, `parent`, `children`, `merge`, `dry_run`, `resume` (maps to `--continue`). |
| `ezstack_push` | destructive | Push current branch or entire stack. `stack`, `force`. |

**Pull requests**

| Tool | Annotation | Description |
|---|---|---|
| `ezstack_pr_create` | &mdash; | Create a pull request for the current branch. `title`, `draft`. |
| `ezstack_pr_update` | destructive | Push the latest commits and refresh the PR base branch / stack description. `branch`. |
| `ezstack_pr_merge` | destructive | Merge the pull request for the current branch. |
| `ezstack_pr_draft` | &mdash; | Toggle a PR between draft and ready-for-review. `branch`. |
| `ezstack_pr_stack` | &mdash; | Update every PR description in the stack with navigation links. |

**Configuration**

| Tool | Annotation | Description |
|---|---|---|
| `ezstack_config_set` | &mdash; | Set a config value. `key` and `value` (both required). Valid keys: `worktree_base_dir`, `default_base_branch`, `github_token`, `cd_after_new`, `use_worktrees`, `sync_strategy`, `agent_command`. |

Read-only inspection tools return JSON by default; `ezstack_status` and
`ezstack_list` accept `decorated=true` for terminal-styled output. Destructive
tools are tagged with the MCP destructive annotation so the client prompts
before running them. Branch-management tools mark their positional arguments as
`Required` in the tool schema so the agent cannot trigger an interactive `fzf`
selection that would hang in a no-terminal context. `ezstack_commit` requires
an explicit `message` and `ezstack_amend` defaults to `--no-edit` so neither
can ever launch `$EDITOR` and corrupt the JSON-RPC transport.

**Safety** &mdash; every tool handler acquires a process-wide mutex before
running, since `ezs` operates on shared process state (stdout/stderr, the
`ui.Backend`, `ui.YesMode`). Stdout and stderr are captured via concurrent pipe
drainers started before the command runs, so large outputs can't block on the
OS pipe buffer. Both behaviors are covered by unit tests under
`cmd/ezs-mcp/*_test.go` and a stdio integration test under `itests/mcp_test.go`
that boots the real binary.

Full feature tour: <https://kulkarnikaustubh.github.io/ezstack/mcp.html>.



### VS Code Extension

Located in `vscode-extension/`. Adds an **ezstack** panel to the activity bar
with two views: a stack tree (branches grouped by stack, with PR state, CI
checks, and review status) and a per-branch file browser. Auto-refreshes when
`~/.ezstack/stacks.json` changes.

**Install**

```bash
# Pre-built (from the Releases page)
code --install-extension ezstack-4.0.0.vsix

# From source
cd vscode-extension
npm install
npm run compile
npx vsce package
code --install-extension ezstack-4.0.0.vsix
```

**Commands** are available under the `ezstack:` prefix in the command palette
(`Cmd+Shift+P`):

- **Branch ops**: `New Branch`, `Sync`, `Sync Branch`, `Push Branch`,
  `Push Stack`, `Delete Branch`, `Reparent Branch`
- **PR ops**: `Create PR`, `Update PR`, `Merge PR`, `Toggle PR Draft`,
  `Update Stack Info in PRs`
- **Agent**: `Open Agent`, `Build Feature with Agent`, `Edit Agent Prompt`
- **File navigation**: `Cmd+Alt+Up` / `Cmd+Alt+Down` jump to the same file in
  the parent / child PR; right-click to compare against the previous PR

**Settings**

| Setting | Default | Description |
|---|---|---|
| `ezstack.cliPath` | `"ezs"` | Path to the `ezs` binary |
| `ezstack.autoRefresh` | `true` | Refresh tree view when config files change |
| `ezstack.ticketPattern` | `""` | Regex to extract ticket IDs from branch names (e.g. `PROJ-\d+`). Shown in the status bar and folder badges |

Full feature tour: <https://kulkarnikaustubh.github.io/ezstack/vscode.html>.

### Neovim Plugin

Located in `neovim-plugin/`. Native Lua plugin for Neovim 0.10+. Exposes a
single `:Ezs` user command with subcommand and flag completion, plus a styled
stack viewer buffer, Telescope pickers, and a statusline component.

**Install (lazy.nvim)**

```lua
{
  "KulkarniKaustubh/ezstack",
  subdir = "neovim-plugin",
  cmd    = { "Ezs" },
  keys   = { { "<leader>ez", "<cmd>Ezs<cr>", desc = "Ezstack viewer" } },
  config = function()
    require("ezstack").setup()
    require("telescope").load_extension("ezstack")  -- optional
  end,
}
```

`packer.nvim` and a manual `runtimepath+=...` install also work &mdash; see
`neovim-plugin/README.md` for the alternatives.

**Key commands** (every `ezs` subcommand has a `:Ezs` mirror):

```vim
:Ezs                 " open the stack viewer
:Ezs status          " viewer with PR/CI info
:Ezs new <name> [parent]
:Ezs sync -s         " sync entire stack
:Ezs sync --continue " resume after conflicts
:Ezs push -s         " push entire stack
:Ezs pr create [title]
:Ezs pr merge        " prompts for method
:Ezs goto [branch]   " switch worktree (uses :tcd by default)
:Ezs up | :Ezs down  " navigate the stack
:Ezs diff            " parent..HEAD in a scratch split (async)
:Ezs diff -- --stat  " forward to `ezs diff` (any git-diff options)
:Ezs graph           " ASCII tree of every stack in a scratch split
:EzsActions          " quick-action menu (also :Ezs actions)
:Ezs agent           " launch the AI agent
:Ezs agent feature "description"
```

The viewer is a non-modifiable buffer with single-key bindings: `<CR>` goto,
`o` open PR, `r` refresh, `n` new, `d` delete, `p`/`P` push, `s` sync, `a`/`A`
agent, `?` help, `q` close.

**Quick action menu (`:EzsActions`)** &mdash; a `vim.ui.select` dropdown with
sync (current / stack / continue), push branch / stack, PR create /
update / draft / merge / open / stack, new / delete / goto branch, and
graph. Bind it to `<leader>ea` if you reach for it often.

**Stack graph (`:Ezs graph`)** &mdash; reads `ezs list --json` and renders
every stack as an ASCII tree. Branches whose parent chain does not reach
`stack.root` are surfaced under an `(orphans &mdash; parent not reachable
from root)` header rather than being silently dropped. Press `q` to close.

**Telescope pickers** (when telescope.nvim is installed):

```vim
:Telescope ezstack branches    " fuzzy-find branches across stacks
:Telescope ezstack stacks      " fuzzy-find stacks
```

**Setup options**

| Option | Default | Description |
|---|---|---|
| `cli_path` | `"ezs"` | Path to the `ezs` binary (auto-discovered) |
| `auto_refresh` | `true` | Refresh on `FugitiveChanged` / `EzstackChanged` |
| `viewer_position` | `"botright"` | Split position for the viewer |
| `viewer_height` | `15` | Viewer window height |
| `statusline_cache_ttl` | `5000` | Statusline cache TTL (ms) |
| `goto_strategy` | `"tcd"` | `"tcd"` (tab-local), `"cd"` (global), or `"lcd"` (window) |
| `goto_close_buffers` | `false` | Close unmodified buffers from the previous worktree on goto |
| `goto_open_explorer` | `true` | Open the file explorer at the new worktree root |
| `default_keymaps` | `false` | Install opt-in `]s` / `[s` stack-navigation mappings (never clobbers existing user mappings, and deliberately avoids Vim's built-in `gn` / `gp`) |
| `statusline_format` | `"stack"` | `"stack"` → ` branch \| stack [hash]`, `"pr"` → ` branch \| PR#N STATE`, `"full"` → both |
| `welcome` | `true` | Show a one-time welcome notification on first `setup()`. The idempotency marker lives under `stdpath("state")/ezstack/welcomed` &mdash; never under `~/.ezstack`, which belongs to the CLI |

**Autocommands** &mdash; the plugin fires `User EzstackSetup` at the end of
`setup()`, `User EzstackChanged` after every CLI mutation, and
`User EzstackGoto` after a worktree switch. Hook your own logic in via
`autocmd`. Run `:help ezstack` for the bundled vimdoc reference.

**Tests** &mdash; a plenary.nvim busted suite lives in
`neovim-plugin/tests/`. Run it with
`nvim --headless --noplugin -u neovim-plugin/tests/minimal_init.lua -c "PlenaryBustedDirectory neovim-plugin/tests/ {minimal_init = 'neovim-plugin/tests/minimal_init.lua', sequential = true}"`.
It covers subcommand-dispatch completeness, statusline formatters, graph
rendering (including orphan handling), default-keymap installation, and
welcome-marker idempotency.

Full feature tour: <https://kulkarnikaustubh.github.io/ezstack/nvim.html>.

### Desktop App

Located in `tauri-ui/`. A native desktop app built with **Tauri v2** (Rust
backend) and **React 19 + TypeScript** on the frontend. The Rust backend is a
thin wrapper that runs `ezs status --json --all` for queries and `ezs -y
<command>` for mutations &mdash; the desktop app shows its own confirmation
dialogs.

**Install / build**

```bash
cd tauri-ui
npm install

# Development (hot reload via Vite + Tauri window)
npm run tauri dev

# Production bundle
npm run tauri build
# → src-tauri/target/release/bundle/
```

Or grab a prebuilt installer from the
[Releases page](https://github.com/KulkarniKaustubh/ezstack/releases).

**Layout** &mdash; three panels:

1. **Repositories sidebar** &mdash; every repo tracked in `~/.ezstack/config.json`,
   with a filter box at the top (type to narrow by name or full path, `Esc`
   or the ✕ button clears it). The currently selected repo stays visible
   even when it doesn't match the filter, so the UI can never drift into a
   state where the selection is hidden and unreachable.
2. **Stack graph** &mdash; visual tree of every stack in the repo, color-coded
   by health with the current branch highlighted. Branch nodes are
   **drag-and-drop reparentable**: drag a node onto another branch and the
   desktop app runs `ezs reparent` with the configured sync strategy. Drops
   that would create a cycle (onto a descendant), onto the branch itself, or
   onto the current parent are blocked with an inline toast.
3. **Branch detail** &mdash; PR state, CI checks, review status, mergeable
   state, a **History** panel showing the most recent reflog entries for
   the branch (hash, action, timestamp), and action buttons.

The status bar shows repo path, current branch, and last refresh time. The
title bar has a theme toggle (dark / light / system) and a connection pill
that turns green / yellow / red based on health.

**Operations** are exposed as dialogs: new branch, sync, push, delete,
reparent, PR create/update/merge, toggle draft, update stack tables, agent
(branch- or stack-scoped), agent feature, and agent prompt management
(view/edit/reset across the shipped, custom, and repo layers). Every
operation surfaces a **toast notification** in the bottom-right: success
toasts auto-dismiss after five seconds, error toasts stay until dismissed so
the CLI output stays available to copy. The full raw CLI output still lands
in a terminal-like panel below the main view.

**Polling** &mdash; every 30 seconds (paused when the window loses focus).
Failures back off exponentially (30s → 60s → 120s → 240s → 300s).

**Keyboard shortcuts**: `Cmd+R` refresh, `Cmd+N` new branch, `Esc` clears the
branch selection (or clears the sidebar filter when the filter input is
focused), `↑/↓` moves between branches in the selected stack, `←/→` or `[`/`]`
moves between stacks.

**Remote (SSH) mode** &mdash; the desktop app can drive an `ezs` install on a
remote machine. Click the **Connect** pill in the title bar, fill in
host/user/port/key (and optionally a jump host), and pick a repo from the
remote `~/.ezstack/config.json`. Profiles are saved to
`~/.ezstack/desktop/connections.json` (mode `0600`); override with the
`EZSTACK_DESKTOP_HOME` environment variable.

The connect dialog has a **Diagnose** button that runs a 6-step health check
(SSH connectivity + latency, login `PATH`, `ezs` present, `git` present, `gh`
authenticated, `~/.ezstack/config.json` readable) and reports per-step pass /
warn / fail with timings. Once connected, the app pings the remote every 30
seconds and the title-bar pill reflects the result.

**Known limitations of remote mode:**

- The agent prompt **editor** is local-only &mdash; opening `$EDITOR` over SSH
  from a GUI is fragile, so the desktop app blocks `agent prompts edit` while
  connected. View and reset still work.
- First connections use `StrictHostKeyChecking=accept-new`; existing host keys
  are still verified strictly.
- Every operation is at least one SSH round-trip &mdash; expect a beat of
  added latency on refreshes.

Full feature tour: <https://kulkarnikaustubh.github.io/ezstack/desktop.html>.
