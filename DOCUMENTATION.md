<div align="center">

# ezstack docs

**Comprehensive guide to using ezstack**

</div>

---

**Table of Contents**

[Overview](#overview) · [Installation](#installation) · [Configuration](#configuration) · [Commands](#commands) · [Workflows](#workflows) · [Editor & Desktop Integrations](#editor--desktop-integrations)

**Commands:** [agent](#ezs-agent) · [commit/amend](#ezs-commit--ezs-amend) · [config](#ezs-config) · [delete](#ezs-delete) · [diff](#ezs-diff) · [doctor](#ezs-doctor) · [down/up](#ezs-down--ezs-up) · [goto](#ezs-goto) · [list](#ezs-list) · [log](#ezs-log) · [menu](#ezs-menu) · [new](#ezs-new) · [pr](#ezs-pr) · [push](#ezs-push) · [reparent](#ezs-reparent) · [stack](#ezs-stack) · [status](#ezs-status) · [sync](#ezs-sync) · [unstack](#ezs-unstack) · [upgrade](#ezs-upgrade)

**Extras:** [Hooks](#hooks) · [Exit codes](#exit-codes) · [Discoverability](#discoverability)

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
# Homebrew — macOS / Linux (installs ezs and ezs-mcp side by side)
brew tap KulkarniKaustubh/ezstack
brew install ezstack

# Go toolchain — Go 1.25+ required (matches the module's go directive)
go install github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs@latest
go install github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs-mcp@latest
```

`ezs-mcp` is the companion MCP server that `ezs agent` (and any standalone
MCP client) uses to drive `ezs` from inside Claude Code. If you install via
`go install`, run both lines so the CLI and the MCP server stay in
lock-step. If you skip the `ezs-mcp` line, `ezs agent` will still bootstrap
it on first launch &mdash; but installing it upfront is faster and works
offline.

You'll also need `git` 2.20+, [`fzf`](https://github.com/junegunn/fzf) for
interactive selection prompts, and the [GitHub CLI](https://cli.github.com/)
(`gh`) for any PR-related commands.

Verify the install:

```bash
ezs --version
# → ezstack version 4.8.5
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
ezs config set agent_command "claude --dangerously-skip-permissions"
```

Multi-word values (or any value starting with `-`) MUST be quoted so the shell
collapses them to a single argument — the last line above shows the common case
for `agent_command`.

### 4. Create a worktree from an existing branch

Most people install `ezs` while standing inside an active repo with a feature
branch (or two) already in flight. The fastest way to start using ezstack is
to point it at one of those existing branches — ezs will mint a stack root
out of it and every command (`commit`, `sync`, `push`, `pr`, …) starts working
immediately. Three flavors below; pick whichever matches what's already in
your repo.

**a) You have a local branch that already has a worktree.** Use
`ezs new --from-worktree`. ezs lists every worktree on the repo and registers
the one you pick as the root of a new stack — no rewriting history, no fresh
checkout.

```bash
ezs new --from-worktree
ezs ls       # the new stack now shows up at the root level
```

What `ezs new --from-worktree` does (see `cmd/ezs/commands/new.go`:
`useFromWorktree`):

1. Calls `git worktree list --porcelain` to enumerate every worktree.
2. Shows a picker labelled `<branch> (<path>)`; you pick one and confirm before
   anything is mutated.
3. Calls `mgr.RegisterExistingBranch(branch, path, base)` — writes the branch
   into `~/.ezstack/stacks.json` with `parent=<configured base branch>` and
   mints a fresh stack hash.
4. If a PR for the branch already exists on GitHub, hydrates the cached PR
   number, URL, state, and review status on the first lookup.
5. Prompts for an optional stack name (Enter to skip).
6. `cd`s your shell into the worktree, when `cd_after_new=true` and the shell
   wrapper from step 2 is installed.

**b) You have a local branch but NO worktree yet.** First materialize a
worktree for it (one git command), then adopt it with the same flow:

```bash
git worktree add <worktree_base_dir>/<branch> <branch>
ezs new --from-worktree     # then pick the just-created worktree
```

(The `<worktree_base_dir>` is whatever you set in step 3's wizard. Run
`ezs config show` if you've forgotten.)

**c) You want to track a teammate's branch (or a remote PR).** `ezs new
origin/<branch>` is a one-shot fetch + worktree + register:

```bash
ezs new origin/feature-branch
ezs new -r 42 my-local-name   # PR #42 → local branch "my-local-name"
ezs new -r                    # interactive PR picker
```

What `ezs new origin/<branch>` does (see `cmd/ezs/commands/new.go`:
`newFromRemoteRef`):

1. Runs `git fetch` so it works off the latest remote refs.
2. Verifies the remote branch exists; errors out if not.
3. Runs `git worktree add <base>/<dirified-branch-name> <branch>` tracking
   `origin`.
4. Mirrors initialized submodules from the main worktree (configurable).
5. Looks up the PR via `gh pr view --json` and registers the branch as a stack
   root with `base = PR.base` (or `main`/`master` fallback).
6. For fork PRs: detects "Allow edits from maintainers" and your push access;
   adds a fork remote when both are true, otherwise marks the branch read-only
   so `ezs push` won't try to publish commits you can't land.

The branch shows up in `ezs ls` with a `(remote)` tag, and every ezs command
works on it (sync, push, commit, etc.).

**Already have a stack and want to graft an existing untracked worktree onto
it as a child branch?** That's [`ezs stack`](#ezs-stack) — interactive, or
`ezs stack -b <branch> -p <parent>` scripted.

Once you have at least one stack root in `ezs ls`, you can move on to step 5
and start stacking branches on top of it.

### 5. Create your first stacked branch

`ezs new <name>` creates a branch whose parent defaults to **your current
branch**, not `main`. So if you want a fresh stack rooted on `main`, make sure
you're on `main` first (or pass `--parent main` explicitly).

```bash
git checkout main          # or:  ezs new feature-1 --parent main
ezs new feature-1
```

What `ezs new feature-1` does (see `cmd/ezs/commands/new.go`):

1. Reads `use_worktrees` / `worktree_base_dir` from the per-repo config.
2. Creates branch `feature-1` from the current branch (no implicit `git fetch`
   — works off whatever your local `main` currently points at).
3. If `use_worktrees=true`: runs
   `git worktree add <worktree_base_dir>/feature-1 feature-1`.
4. Records the branch in `~/.ezstack/stacks.json` with
   `parent=<your previous branch>`.
5. If this is the first branch in a new stack, prompts you for a stack name.
6. If `cd_after_new=true` and the shell wrapper is installed: `cd`s into the
   worktree.

Make some edits, then commit. `ezs commit` is a thin wrapper over `git commit`
that also rebases child branches and offers to push for you.

```bash
ezs commit -am "Scaffold feature"
```

What `ezs commit` does (see `cmd/ezs/commands/commit.go`):

1. Runs `git commit -am "..."` interactively (your editor still works for long
   messages — the `-m` here just bypasses it).
2. If the branch already exists on the remote, prompts `Push to remote? [Y/n]`.
   Answer `Y` to push or `n` to defer; `ezs amend` prompts for a `--force` push
   instead.
3. Looks up child branches in `stacks.json` and rebases (or merges, per
   `sync_strategy`) each child onto the new tip. On a brand-new leaf branch
   this is a no-op.

Stack a second branch on top:

```bash
ezs new feature-2 --parent feature-1
# ... edit files in the feature-2 worktree ...
ezs commit -am "Wire up feature"
```

### 6. Push the stack and open PRs

```bash
ezs push --stack
```

What `ezs push --stack` does (see `cmd/ezs/commands/push.go`):

1. Walks every branch in the current stack, root → leaf, skipping branches
   already marked merged.
2. For each branch: `git push -u <remote> <branch>` (plain push, sets upstream).
3. Skips branches whose remote is "no-push" (fork PRs without maintainer-push
   permission) with a warning.
4. Add `--force` (or `-f`) to switch to `git push -u --force-with-lease` — what
   you want after rebases or amends.

Open a PR for each branch. The base branch ezstack passes to `gh pr create`
is the branch's recorded **parent** in `stacks.json`, NOT `main`. So
`feature-2`'s PR targets `feature-1` and the diff only shows feature-2's
commits.

```bash
ezs goto feature-1
ezs pr create -t "Part 1: scaffolding"

ezs goto feature-2
ezs pr create -t "Part 2: wire it up"
```

What `ezs pr create` does:

1. Shells out to `gh pr create --base <parent> --head <branch> --title "..."`.
2. Records the PR number back into `stacks.json`.
3. Add `-d` / `--draft` to open as a draft.

Now inject stack-navigation links into every PR description so reviewers can
hop around the stack:

```bash
ezs pr stack
```

What `ezs pr stack` does:

1. Calls `gh.UpdateStackDescription` for every PR in the stack (see
   `cmd/ezs/commands/utils.go` and the GitHub client).
2. Rewrites each PR body so it contains a managed block listing every branch
   in the stack, with the active branch marked.

### 7. Inspect the stack any time

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

**Updating**

For binary installs, ezstack can update itself in place:

```bash
ezs upgrade            # download the latest release tarball, verify checksum, swap binaries
ezs upgrade --check    # see whether an upgrade is available without downloading
ezs upgrade --version v4.6.0   # pin to a specific release tag
```

`ezs upgrade` detects how the binary was installed and routes to the right channel: a manual binary install gets an in-place atomic swap, a `go install` install is re-installed by re-running `go install …@<tag>` so the toolchain stays the source of truth for the install location, and a Homebrew install is left alone with a hint to run `brew upgrade ezstack` (so brew's receipt of the install stays in sync). The companion `ezs-mcp` binary is upgraded alongside `ezs` in lock-step — first by checking next to `ezs`, and then by falling back to `PATH` so a `go install`-only `ezs-mcp` (e.g. at `~/go/bin/ezs-mcp` while `ezs` lives in `~/.local/bin/`) is still picked up. A Homebrew-managed `ezs-mcp` resolved through `PATH` is left alone with the same brew hint. Pass `--no-mcp` to skip it entirely.

`ezs-mcp` exposes the same flow under `--upgrade`, `--upgrade-check`, `--upgrade-tag`, and `--upgrade-force` for the rare case where it is installed without `ezs`.

**Shell integration (recommended)**

Add to your shell configuration:

```bash
# bash
echo 'eval "$(ezs --shell-init)"' >> ~/.bashrc

# zsh
echo 'eval "$(ezs --shell-init)"' >> ~/.zshrc
```

This enables automatic directory changes for `goto`, `new`, `delete`, `sync`, `up`, and `down` commands. It also installs bash/zsh tab completion for ezs subcommands, flags, branch names, stack hashes/names, and `config set` keys — `ezs <TAB>` shows commands, `ezs goto <TAB>` lists branches, `ezs sync <TAB>` lists stacks, `ezs config set <TAB>` lists valid keys, and `ezs <cmd> --<TAB>` lists that command's flags.

Without shell integration, commands that would change your directory will instead print a helpful message with the path to `cd` to manually, and tab completion is unavailable.

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

**Available keys:** `worktree_base_dir`, `default_base_branch`, `cd_after_new`, `use_worktrees`, `sync_strategy`, `init_submodules`

- `init_submodules` (per-repo, default `true`): when creating a new worktree, mirror the same set of initialized submodules that are active in the main worktree. Useful for monorepos (e.g. SONiC) where developers only init the submodules they actively work on. Overridable per-invocation with `ezs new --init-submodules` / `--no-init-submodules`.

**Global flags**

These flags work with any command and can appear in any position:

```
-y, --yes        Auto-confirm all yes/no prompts (selection menus still show)
-h, --help       Show help
-v, --version    Show version
--shell-init     Output shell function for cd support
--info           Print a diagnostic dump (versions, config state) for bug reports
```

`ezs --info` prints ezstack version, `go`/`git`/`gh`/`fzf` versions, the config directory path, whether `config.json` is present and loads cleanly, the number of configured repos, and the default base branch. It's safe to paste into bug reports — no secrets are included.

---

## Workflows

Real end-to-end flows, annotated so you can see exactly what ezstack is doing to
your git state on every step. If you want the full reference for any individual
command, jump to the [Commands](#commands) section below.

### Creating a Stacked PR

The canonical flow: build two dependent branches, push them, open linked PRs.
Every `ezs` line is a thin shell over git — the step-by-step list below the
block names the underlying operation so there is no magic.

```bash
ezs new feature-1
# ... edit files in the feature-1 worktree ...
ezs commit -am "Add feature part 1"

ezs new feature-2 --parent feature-1
# ... edit files in the feature-2 worktree ...
ezs commit -am "Add feature part 2"

ezs push --stack

ezs goto feature-1
ezs pr create -t "Part 1: scaffolding"
ezs goto feature-2
ezs pr create -t "Part 2: wire it up"

ezs pr stack
```

Step by step:

1. **Start a new stack rooted on `main`.** `ezs new feature-1` fetches
   `origin/main`, creates branch `feature-1` pointing at it, materializes a
   worktree at `<worktree_base_dir>/feature-1/` (when `use_worktrees=true`),
   records the branch in `~/.ezstack/stacks.json` with `parent=main`, and
   `cd`s your shell into the new worktree.
2. **Commit.** `ezs commit -am` is equivalent to
   `git add -A && git commit -m ...` followed by an automatic
   `ezs sync --children` so descendants get rebased onto the new tip. On a
   brand-new branch there are no children, so this is just stage + commit.
3. **Stack a second branch on top.** `ezs new feature-2 --parent feature-1`
   creates the branch pointing at `feature-1` (not `main`), creates a second
   worktree, records `parent=feature-1`, and `cd`s into it.
4. **Push the whole stack.** `ezs push --stack` walks root → leaf and runs
   `git push --force-with-lease origin <branch>` for each, setting upstream
   on the first push.
5. **Open a pull request for each branch.** The base of each PR is the
   *parent* branch recorded in `stacks.json`, not `main` — so `feature-2`'s
   PR targets `feature-1` and only shows the `feature-2` diff.
6. **Cross-link the PRs.** `ezs pr stack` fetches each PR body via
   `gh pr view`, injects a managed block listing every branch in the stack
   with links and ✅ / 🔵 markers for merged / current, then pushes the
   updated bodies back with `gh pr edit`.

### Committing into the middle of an existing stack

Dependent branches below you would normally get left behind on the old tip of
`feature-1`. `ezs commit` handles this automatically.

```bash
ezs goto feature-1
ezs commit -am "Address review comment on part 1"
```

What `ezs commit` does under the hood:

1. `git add -A && git commit -m "..."`.
2. For each descendant (`feature-2`, `feature-3`, ...) runs
   `git rebase --onto <new feature-1 tip> <old feature-1 tip> <descendant>`,
   so their worktrees now sit on top of the amended parent.
3. If the branch is already on the remote, auto-force-pushes the branch and
   every descendant, so the open PRs update in one shot.

### After a Parent is Merged

When an upstream PR (or a parent branch in the stack) lands on `main`, the
descendants need to be re-rooted. `ezs sync` does the surgery.

**Case A: GitHub merged the PR** (squash / rebase / merge — all handled).

```bash
ezs sync --all
```

1. Fetches `origin/main`.
2. Detects that `feature-1` has been merged (matching commit on `main` OR the
   PR's `mergedAt` field via `gh pr view --json`).
3. Drops `feature-1` from the stack.
4. Rebases `feature-2` onto `main`:
   `git rebase --onto main <old feature-1> feature-2`.
5. Updates `stacks.json` so `feature-2`'s parent is now `main`.
6. Deletes the merged local branch + worktree (unless you `cd`'d elsewhere).

**Case B: you want to merge from the CLI** and keep the stack clean in one go.

```bash
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
ezs new origin/feature-branch
# ... review, run, edit, ezs commit / push / sync as needed ...
ezs delete feature-branch
```

What `ezs new origin/<branch>` does:

1. Runs `git fetch origin <branch>` (or the PR's head ref for fork PRs).
2. Creates a local branch tracking that ref.
3. Creates a worktree for it.
4. Looks up the PR via `gh pr view --json` and records it in `stacks.json`
   with its base branch as the stack root.
5. Prints a summary panel (PR title, URL, state, review status, +/- diff).

The branch now shows up in `ezs ls` with a `(remote)` tag, and every ezs
command works on it. `ezs delete` at the end removes the worktree and local
branch in one call.

For fork PRs, ezstack auto-detects maintainer-push capability:

- If the PR has "Allow edits from maintainers" AND you have write access to
  the fork → adds the fork as a git remote and pushes there.
- Otherwise → the branch is marked read-only so `ezs push` / `ezs sync`
  won't try to publish commits you can't land.

### Stacking on a Remote PR

When you need to build on top of a teammate's in-flight PR without waiting for
it to merge:

```bash
ezs stack
```

`ezs stack` launches an interactive picker:

1. Choose "Start a new stack from a remote PR" and pick the PR via fzf.
2. Pick a local branch (or create one); it gets reparented onto the PR.

Result in `stacks.json`:

```
<teammate-pr-branch>      parent=main      (remote, read-only)
  └── <your-branch>        parent=<teammate-pr-branch>
```

Your branch is now rebased on top of their work, and `ezs sync --all` will
keep it up to date as they push new commits.

### Adopting an Existing Worktree as a Stack Root

Already have a long-lived branch with its own worktree (created by hand, or
inherited from a teammate's checkout) and want ezstack to start tracking it?
You don't have to delete and recreate — `ezs new --from-worktree` registers an
existing worktree as the root of a new stack in place.

```bash
ezs new --from-worktree
ezs ls                           # the new stack shows up at the root level
ezs new sub-feature              # add a child branch on top
ezs pr create -t "Long-running feature"
```

What `ezs new --from-worktree` does (see `cmd/ezs/commands/new.go`:
`useFromWorktree` branch):

1. Calls `git worktree list --porcelain` to enumerate every worktree attached
   to this repo.
2. Shows them in a picker labelled `<branch> (<path>)` and confirms before
   mutating anything.
3. Calls `mgr.RegisterExistingBranch(branch, path, base)` — writes the branch
   into `~/.ezstack/stacks.json` with `parent=<configured base branch>` and
   mints a fresh stack hash.
4. If a PR already exists for the branch on GitHub, hydrates the cached PR
   number, URL, state, and review status on the first lookup.
5. Prompts for an optional stack name (Enter to skip).
6. `cd`s your shell into the worktree, when `cd_after_new=true` and the shell
   wrapper is installed.

From here on, every `ezs` command works against that branch.

Already have a tracked stack but a *separate* untracked worktree you want to
graft onto it? Use `ezs stack` instead — it's the symmetric move:

```bash
ezs stack                                 # interactive: pick worktree, then parent
ezs stack -b my-fix -p feature-1     # add my-fix as a child of feature-1
ezs stack -b my-fix -B develop       # start a NEW stack rooted on `develop`
```

The interactive form (no flags) walks you through choosing the untracked
worktree and then the parent inside an existing stack — see
`cmd/ezs/commands/stack.go` (choice 0).

`ezs stack` only updates the metadata — it doesn't rebase your commits onto the
new parent. If you want the rebase too, use `ezs reparent` (see the
[`ezs reparent`](#ezs-reparent) reference).

### Inserting a Branch Between Two Stacked Branches

You're mid-review and realize a stack of `main → feature-1 → feature-2` should
have a small refactor or schema change between the two branches. ezstack can
splice a new branch into the middle without rebuilding the stack — `ezs new`
creates the leaf, then `ezs reparent` re-points the existing child at it.

```bash
# Before:    main → feature-1 → feature-2
# After:     main → feature-1 → refactor → feature-2

ezs new refactor --parent feature-1
# ... edit files in the refactor worktree ...
ezs commit -am "Extract shared helper"

ezs reparent feature-2 refactor
```

Step by step:

1. **Create the inserted branch as a leaf on the upper half.** `ezs new refactor --parent feature-1` creates `refactor` pointing at the current tip of `feature-1`, materializes its own worktree, and records `parent=feature-1` in `stacks.json`. At this point the stack is `main → feature-1 → {refactor, feature-2}` — `refactor` is a sibling of `feature-2`, not yet between them.
2. **Commit the change you actually wanted to splice in.** `ezs commit -am "..."` works exactly like in the canonical flow.
3. **Reparent the lower half onto the new branch.** `ezs reparent feature-2 refactor` rewrites `stacks.json` so `feature-2`'s parent becomes `refactor`, then runs `git rebase --onto refactor <old feature-1 tip> feature-2`. Anything stacked under `feature-2` (`feature-3`, `feature-4`, …) rides along — the reparent operates on the whole subtree.
4. **PR base is updated automatically.** If `feature-2` already has a PR, ezstack calls `gh pr edit --base refactor` so reviewers see only the diff against the new parent, then offers a force-push to publish the rebase.

Notes:

- If you want the metadata update only (no rebase yet — for example because
  the new branch isn't ready to share commits with `feature-2` yet), pass
  `ezs reparent feature-2 refactor --no-rebase`. The two branches will diverge
  on disk until you sync.
- `ezs stack` does the metadata-only insert as a one-liner: `ezs stack -b feature-2 -p refactor` — same effect as `--no-rebase`, no rebase or push offered.
- Conflicts during the rebase are non-fatal: ezstack saves the new tracking metadata first, then drops you into the worktree with `git rebase --continue` instructions if the rebase trips. See [Recovering from a Sync Conflict](#recovering-from-a-sync-conflict).

### Swapping a Branch With Its Parent

You stacked `feature-2` on top of `feature-1`, then realized the dependency
should run the other way — `feature-1`'s changes actually need `feature-2`
underneath them. ezstack doesn't have a single `swap` command, but two
`ezs reparent` calls do it. The order matters because `ezs reparent` rejects
moves that would create a cycle (`internal/stack/stack.go`,
`wouldCreateCycle`), so you can't go straight from `feature-1 → feature-2` to
`feature-2 → feature-1` in one step — the child has to be detached first.

```bash
# Before:    main → feature-1 → feature-2
# After:     main → feature-2 → feature-1

ezs reparent feature-2 main      # main → feature-1, main → feature-2 (siblings)
ezs reparent feature-1 feature-2 # main → feature-2 → feature-1
```

Step by step:

1. **Move the child up to be a sibling of its current parent.** `ezs reparent feature-2 main` rewrites `feature-2`'s parent to `main` and runs `git rebase --onto main <old feature-1 tip> feature-2`. After this step both branches sit directly on `main`, which breaks the parent/child relationship the cycle check is worried about.
2. **Reparent the original parent onto the original child.** `ezs reparent feature-1 feature-2` is now legal because `feature-1` is no longer an ancestor of `feature-2`. The rebase replays `feature-1`'s commits on top of `feature-2`.
3. **PR bases get rewritten automatically.** Both PRs end up pointing at their new parents (`feature-2` → `main`, `feature-1` → `feature-2`). Each step offers a force-push so the GitHub diffs match the new bases. Use `ezs pr stack` afterwards to refresh the cross-link block in the PR bodies.

Notes:

- If `feature-2` had its own children, they ride with it through step 1 and
  end up *above* `feature-1` after step 2. That's usually what you want for a
  swap; if not, reparent the unwanted descendants back onto `feature-1`
  before step 2.
- Add `--no-rebase` to either call if you only want the tracking metadata
  flipped (no commit replay). Useful when you plan to rewrite history by hand
  with `git rebase -i` afterwards.
- The interactive parent picker (`ezs reparent` with no args) hides
  descendants of the branch being moved, so it won't even offer the
  one-shot swap — you have to do the two-step.

### Splitting a Single Large Worktree Into a Stack — Manually

Sometimes a feature branch grows into a 2,000-line monster before review even
starts. ezstack doesn't care how a stack came to exist, so you can carve up an
existing branch by hand and turn the pieces into reviewable stacked PRs.

The recipe below assumes:

- You're on a branch called `mega-feature`.
- The branch has N commits since `main` that you'd like to split into K stacked
  PRs (N ≥ K).
- You already ran `ezs config` so worktrees and a base directory are
  configured.

```bash
git push origin mega-feature      # safety backup
git log --oneline main..HEAD       # eyeball the commits you'll redistribute
BASE=$(git rev-parse origin/main)

git checkout main
ezs new part-1 --parent main
git cherry-pick <sha-1> <sha-2>
ezs commit --amend --no-edit       # optional: fold commits together

ezs new part-2
git cherry-pick <sha-3>
git cherry-pick <sha-4>

ezs new part-3
git cherry-pick <sha-5>

ezs ls
ezs push --stack
ezs pr create --stack
ezs pr stack

ezs delete mega-feature              # or `git branch -D` if untracked
git push origin --delete mega-feature
```

Step by step:

1. **Push to remote first.** Splitting rewrites history locally; a stray
   force-push could lose work without this safety backup.
2. **Snapshot the base SHA** with `git rev-parse origin/main` so each new
   stack branch can be anchored to a known-good commit.
3. **Pick a carve-up pattern.** Pattern A — commit-by-commit, each existing
   commit becomes its own stack branch (used in the example above; works best
   when commits are already small and self-contained). Pattern B —
   diff-by-feature, redistribute the diff into new branches by topic when the
   existing commits are messy.
4. **Create the first stack branch on top of `main`.**
   `ezs new part-1 --parent main` gives the new stack a known-good base;
   `cd_after_new` moves you into `<worktree_base_dir>/part-1`.
5. **Cherry-pick the relevant commits from `mega-feature` into `part-1`'s
   worktree.** Cherry-pick (rather than checkout) because the worktree is
   already on its own branch.
6. **Stack `part-2` on top of `part-1`, repeat.** `ezs new` chains the parents
   automatically because `cd_after_new` put you on `part-1`.
7. **Repeat for as many slices as you need** — `part-3`, `part-4`, etc.
8. **Verify the layout** with `ezs ls`:

   ```
   main
   └── part-1   PR—   +210 -12
       └── part-2  PR—   +180 -9
           └── part-3  PR—   +95 -3
   ```

9. **Push the entire stack and open one PR per branch.**
   `ezs pr create --stack` makes a PR per branch and `ezs pr stack`
   cross-links them in the PR descriptions.
10. **Retire the old monolith** (optional) once the new stack is reviewable.
    `ezs delete` if it was tracked by ezstack, plain `git branch -D` if it
    wasn't, then `git push origin --delete` to clean up the remote.

Note: `git cherry-pick` runs inside whichever worktree your shell is in, so
each `ezs new` step *must* leave you cd'd into the new branch's worktree (the
default when `cd_after_new=true`). If you opted out of auto-cd, prefix each
cherry-pick with `cd <worktree_base_dir>/<branch>` first — otherwise you'll
cherry-pick into the wrong branch.

### Splitting a Single Large Worktree Into a Stack — With `ezs agent feature`

If you'd rather have the agent plan the split, `ezs agent feature` accepts an
*existing* stack as scope. The trick is to first make the big branch a stack
root (so the agent has somewhere to plan from), then ask the agent to refactor
the diff into incremental child branches.

```bash
ezs new --from-worktree
ezs stack rename

ezs agent feature "$(cat <<'EOF'
split mega-feature into reviewable stacked branches: one per concern
(data model, business logic, API surface, tests).
Cherry-pick or move commits as needed; leave mega-feature as the parent
of the new branches and trim it once everything is moved.
EOF
)"

ezs ls                                # in another terminal
ezs status --watch                    # live PR/CI dashboard

ezs push --stack
ezs pr create --stack --auto          # ← see "AI-drafted PRs" below
ezs pr stack
```

Step by step:

1. **Adopt the existing big-branch worktree as a stack root.**
   `ezs new --from-worktree` opens a picker; pick the worktree for
   `mega-feature`. It is now the root of a brand-new stack with no children
   yet.
2. **Optionally name the stack** with `ezs stack rename` (interactive: pick
   the stack, type a name) so it's easy to find later.
3. **Launch feature mode against THIS stack** with `ezs agent feature "..."`
   so the agent sees the existing branch as part of its scope. The heredoc
   above is one safe way to keep a multi-line prompt readable; a single
   quoted string works too. See `cmd/ezs/commands/agent.go` (`agentFeature`
   runs against an `existingStack` when one is provided).
4. **Watch the agent work** in a second terminal with `ezs ls` and
   `ezs status --watch`. The agent will propose a plan first; approve before
   it starts running `ezs new` / `ezs commit` / `git cherry-pick` calls.
5. **When the agent is done, push and open PRs as usual.**
   `ezs pr create --stack --auto` will AI-draft titles and bodies for every
   PR (see "AI-drafted PRs" below), then `ezs pr stack` cross-links them.

What `ezs agent feature` does:

- Composes the shipped feature-mode prompt + your repo's
  `<repo>/.ezstack/agent-feature-prompt.md` (if any) +
  `~/.ezstack/agent-feature-prompt.md` (if any) + the current stack JSON.
- When a stack is provided, swaps in the EXISTING-STACK instructions (see
  `buildRenderedFeaturePrompt` in `cmd/ezs/commands/agent.go`): the agent is
  told to use the existing branches as a starting point and to add new ones
  on top.
- Spawns the configured `agent_command` (`claude` by default) inside the
  stack root's worktree, with a persistent session bound to the stack.
  Re-running `ezs agent` later resumes the same conversation.
- When the configured agent is `claude`, auto-installs and registers
  `ezs-mcp` so the agent drives ezstack via MCP tools instead of shelling
  out — far more reliable for a complex split.

Tips:

- The agent never deletes the original branch automatically. Once you've
  reviewed its work and the new stack looks right, run
  `ezs delete mega-feature` (or `ezs unstack mega-feature` to keep the branch
  but stop tracking it).
- If you want a dry run, pass `--dry-run` to inspect the composed prompt
  without launching the agent: `ezs agent feature "..." --dry-run`.
- For a one-off custom persona ("review carefully", "add tests"), drop a
  preset at `~/.ezstack/agent-presets/<name>.md` and pass `--preset <name>`.

### AI-Drafted PR Title & Body for a Single Branch (`--auto`)

`ezs pr create --auto` (alias `--ai`) hands the branch's diff, commit messages,
and the repo's PR template to the configured agent CLI and asks it to fill in
the title and body. The result is fed into the regular create flow, so all the
usual gating still happens — push, base validation, fork detection, stack
description update.

```bash
ezs goto feature-2
ezs pr create --auto       # single branch, AI-drafted title and body
```

What `ezs pr create --auto` does (see `cmd/ezs/commands/pr_auto.go`):

1. Validates that the configured `agent_command` is a Claude-family CLI.
   (`--auto` currently only works with `claude` — the prompt expects JSON
   output and other agents don't have a stable non-interactive contract.)
2. Captures `git diff <parent>..HEAD` for the branch (capped at 80 KB).
3. Captures `git log --oneline <parent>..HEAD` for the commits.
4. Reads the repo's pull-request template from
   `.github/pull_request_template.md` / `PULL_REQUEST_TEMPLATE.md` /
   `docs/pull_request_template.md` (or repo-root variants).
5. Wraps everything in a hardened prompt (every untrusted span is wrapped in
   `<data>` tags so the agent can't be diff-injected) and shells out to
   `claude --print --output-format json --append-system-prompt …`. A 5-minute
   timeout caps the call.
6. Parses the JSON `{title, body}` and feeds it into the regular PR create
   flow.

Pin one field, let the agent draft the other:

```bash
ezs pr create --auto -t "Part 2: wire it up"   # title is fixed, body is AI
ezs pr create --auto -b "Implements bullet 3"  # body is fixed, title is AI
```

Roll back when you don't like the result:

```bash
gh pr view --web                       # eyeball it
gh pr edit <num> --title "..." --body "..."
ezs pr unlink                          # drop the cached PR association
ezs pr create --auto                   # regenerate
```

The last two lines recreate the PR from scratch with a different prompt
context — useful after squashing more commits in, for example.

### AI-Drafted PRs Across the Whole Stack

`--auto` composes with `--stack` (and `--draft-all`). Use this on a freshly
pushed stack so every branch gets a populated description in one shot.

```bash
ezs push --stack
ezs pr create --stack --auto
ezs pr stack
```

Step by step:

1. **Push every branch in the current stack** with `ezs push --stack`.
2. **Create a real PR per branch, AI-drafted title + body for each.**
   `ezs pr create --stack --auto` (see `cmd/ezs/commands/pr.go`:
   `prCreateAllForceAI`) walks every branch root → leaf. For each branch
   without a live PR it runs the same diff/commits/template flow as
   single-branch `--auto`, then
   `gh pr create --base <parent> --head <branch> --title "..." --body "..."`.
   Branches that already have a non-merged PR are skipped (use `--force` to
   override per-branch).
3. **Cross-link them** with `ezs pr stack` so reviewers can navigate.

Variants:

```bash
ezs pr create --draft-all --auto    # every branch = DRAFT PR, AI-drafted body
ezs pr --draft-all                  # every branch = draft PR, branch-name title, NO AI
```

The first variant is the same flow but emits drafts (see
`cmd/ezs/commands/pr.go`: `--draft-all` + `--auto`). The second is cheaper and
faster — no agent dependency, just `gh pr create --draft` per branch with the
branch name as the title.

A few practical notes:

- `--auto` requires the configured `agent_command` to resolve to a Claude
  binary (`claude`, `claude-code`, etc.). Pass an explicit binary with
  `--cmd` only for the *interactive* `ezs agent` flow — `pr create --auto`
  is non-interactive and rejects non-claude binaries with a clear error.
- The PR template path is what GitHub itself looks at:
  `.github/pull_request_template.md`, `.github/PULL_REQUEST_TEMPLATE.md`,
  `docs/pull_request_template.md`, or root-level variants. If your repo
  doesn't have one, the agent falls back to a Summary + Test plan layout.
- If the agent times out or returns malformed JSON, ezstack falls back to a
  branch-name title + empty body and prints the agent's stderr so you can
  diagnose. The PR still gets created.

### Recovering from a Sync Conflict

`ezs sync` rebases each branch onto its parent. When a hunk conflicts, git
stops mid-rebase and ezstack returns exit code `3` (rebase conflict). The
recovery flow is the same as a stock git rebase, with a single ezs follow-up
to re-cascade the rest of the stack.

```bash
ezs sync --stack
# →  CONFLICT (content): Merge conflict in src/auth.go
# →  exits with code 3.

$EDITOR src/auth.go                   # fix conflict markers
git add src/auth.go
git rebase --continue                 # advance the rebase one commit

ezs sync --continue
```

Step by step:

1. **Try to sync.** `ezs sync --stack` rebases each branch onto its parent.
   If a hunk conflicts it stops mid-rebase and exits with code `3`.
2. **Resolve the conflict in the worktree git stopped in.** Edit, `git add`,
   `git rebase --continue` until the rebase finishes (no more conflicts).
3. **Tell ezs to finish the cascade** with `ezs sync --continue`. It replays
   the descendant subtree using the pre-sync SHAs it snapshotted in
   `stacks.json` before the conflict, picks up where the original sync left
   off, honors the same scope flags as the original call (`-s`, `-a`, `-c`,
   `-b`, `<hash-prefix>` — re-add them if the original was scoped narrower
   than "everything"), force-pushes any branches that were already on the
   remote, and runs the post-sync hook to mark the sync complete.

If you want to bail out instead of resolving:

```bash
git rebase --abort                    # back out the rebase you were in
```

`ezs sync` exited cleanly with code `3` already; nothing else to do.

Notes:

- `git rerere` is auto-enabled on the repo on first sync, so resolving a
  conflict once teaches git to replay it across siblings — useful when the
  same hunk recurs in every child branch.
- A second `ezs sync` invocation while one is already running fails fast
  with "another `ezs sync` is already running" (an exclusive flock on
  `~/.ezstack/stacks.json.sync.lock`). If you accidentally Ctrl-C'd a sync,
  the lock is released — no manual cleanup needed.

### Squash-Merging Children Into Their Parent Before PR

When a stack has grown noisy with WIP commits and you want each branch to
land as a single squashed commit:

```bash
ezs sync --stack --squash
```

What `ezs sync --stack --squash` does:

- Walks each branch in the stack with ≥2 commits since its parent.
- Collapses those commits into one before rebasing onto the parent.
- Branches that are already a single commit are left untouched.
- Auto-force-pushes any branch that was already on the remote (the squash
  rewrites history, so a regular push would be rejected).

Pair with `ezs pr update` to push the collapsed history and keep PR bases /
descriptions in sync:

```bash
ezs pr update --branch part-1
ezs pr update --branch part-2
ezs sync --stack && ezs pr stack    # or, for the whole stack
```

### Pre-Push Validation as a Hard Requirement (`--verify`)

Want to enforce that every push is gated by tests, lints, or a security check
— not just *if* the hook happens to be installed? Use `--verify` to require
the hook.

First, install the hook once:

```bash
mkdir -p ~/.ezstack/hooks
cat > ~/.ezstack/hooks/pre-push <<'SH'
#!/usr/bin/env bash
set -euo pipefail

cd "$EZS_REPO_ROOT"
echo "Running pre-push checks for $EZS_BRANCH..."

# Skip pushes initiated from inside an `ezs agent --no-push` session.
if [ "${EZS_AGENT_NO_PUSH:-}" = "1" ]; then
  echo "Agent --no-push mode detected; skipping checks."
  exit 0
fi

go test ./...
golangci-lint run
SH
chmod +x ~/.ezstack/hooks/pre-push
```

Then push with `--verify` so a missing or non-executable hook is a hard error:

```bash
ezs push --stack --verify
```

`--verify` makes a missing/non-executable hook a hard error: without it the
hook runs if installed and is a no-op otherwise; with it ezs aborts when the
hook is missing, not executable, or exits non-zero — useful in CI and shared
dev hosts.

Available hook environment variables: `EZS_HOOK`, `EZS_REPO_ROOT`,
`EZS_BRANCH`, `EZS_STACK_HASH`, `EZS_STACK_NAME`, plus `EZS_AGENT_NO_PUSH=1`
when the push originates from an agent session launched with `--no-push`. See
the [Hooks](#hooks) section for the full contract.

### Live CI Dashboard With `ezs status --watch`

Stack-status with a refresh loop is the closest ezs gets to a TUI. Useful when
you've just pushed a multi-branch stack and want to watch CI light up branch
by branch.

```bash
ezs status --watch                       # default: re-render every 5 seconds
ezs status --watch 10                    # custom interval (space- or =-separated)
ezs status --watch=10
ezs status --branch feature-1 --watch    # scope: specific branch's stack
```

The interval is clamped to a 2-second minimum so we don't hammer `gh`.

Caveats: watch mode requires a TTY (it clears the screen on each refresh) and
cannot be combined with `--json`. Ctrl-C exits cleanly.

### Bootstrapping a Fresh Machine

Just installed `ezs` on a new laptop or a CI runner? Run this top-to-bottom
once:

```bash
ezs doctor
ezs config import ~/Dropbox/ezs-backup.json
cd ~/code/my-project
ezs config
echo 'eval "$(ezs --shell-init)"' >> ~/.zshrc && exec zsh
```

Step by step:

1. **Sanity-check tooling** with `ezs doctor` (doesn't require being inside a
   git repo). It reports git/gh/fzf presence, versions, config dir state, and
   per-repo worktree base directory health. Exit `0` means you're good;
   non-zero with a one-line summary otherwise.
2. **Restore your global config** from a backup if you have one. Per-machine
   `github_token` is preserved across imports — exporting redacts it,
   importing skips the redaction sentinel — so round-trips are safe.
3. **Run the per-repo wizard** inside each repo you want ezs to manage.
4. **Wire up the shell wrapper** so cd-on-new and tab completion work.

For the corresponding *backup* flow before you wipe a machine:

```bash
ezs config export ~/Dropbox/ezs-backup.json
```

The export is mode `0600` with `github_token` replaced by the literal sentinel
`<redacted-by-ezs-export>`, so the file is safe to share / commit / sync to
untrusted storage.

### Resetting a Stale PR Association — `unlink`, then `create` or `refresh`

ezstack caches per-branch PR metadata (`pr_url`, `pr_state`, `is_merged`) in
`~/.ezstack/stacks.json`. When that cache and the live state on GitHub
disagree — you closed and reopened a PR by hand, deleted it, lost track of
which PR a branch was tied to, etc. — the recovery primitive is
`ezs pr unlink`.

```bash
ezs pr unlink                          # current branch
ezs pr unlink --branch feature-2       # specific branch
ezs pr unlink --all                    # every branch in the current stack
ezs pr unlink --all -y                 # skip the confirmation prompt
```

`ezs pr unlink` (see `cmd/ezs/commands/pr.go`: `prUnlink`) drops the cached
link by clearing `pr_url`, `pr_state`, and `is_merged` in `stacks.json`. It
does NOT touch GitHub.

From here you have two paths back to a healthy cache, depending on whether
you want to **start over with a brand-new PR** or **re-discover an existing
PR that's still live on GitHub**:

**Path A: start over.** ezs opens a brand-new PR for the branch and writes the
new `pr_url` into `stacks.json`. Use this when the prior PR was closed,
replaced, or just "burn it down and start fresh".

```bash
ezs pr create -t "Part 2 (v2): wire it up"
ezs pr create --auto                   # AI-drafted title + body
```

After `unlink` the cache is empty, so `pr create` has no existing-PR guard to
trip and proceeds straight to `gh pr create --base <parent> --head <branch>`
(see `cmd/ezs/commands/pr.go`: `prCreate`).

**Path B: re-discover the existing PR.** If a PR for the branch still exists
on GitHub, `pr refresh` queries GitHub by branch name, repopulates the cache,
and restores the link without opening a new PR.

> **Important:** `pr refresh` requires a non-empty cache to operate on — after
> a hard `pr unlink` (which clears both `pr_number` and `pr_url`), refresh
> exits with `"Branch '...' has no cached PR association."` For Path B you
> typically want to skip the unlink and just run refresh on the still-linked
> branch:

```bash
ezs pr refresh                         # current branch
ezs pr refresh --branch feature-2
ezs pr refresh --stack                 # parallel refresh across the stack
```

For each targeted branch, ezs calls `gh.GetPR(<cached number>)`; on a stale
or 404 number it falls back to `gh.GetPRByBranch(<branch name>)`. Whatever it
finds (live PR, merged PR, closed PR, no PR) is written back into the cache
(see `fetchLivePR` + `applyPRRefresh` in `cmd/ezs/commands/utils.go`). This is
the "GitHub UI moved underneath me" recovery path.

Mental model: `pr unlink` is the *forget* primitive, `pr create` is the *make*
primitive, and `pr refresh` is the *re-discover and reconcile* primitive. Run
`pr refresh` first if you want to keep an existing PR; only `pr unlink` if
you've decided the cache is poison and you'd rather rebuild it from scratch.

### Worktree Templates: Pre-Seeded New Branches

If every new worktree needs the same `.envrc`, IDE settings, or scaffold
files, drop them into `~/.ezstack/templates/<name>/` once and use the
`--template` flag to overlay them on every new worktree.

Author a template once:

```bash
mkdir -p ~/.ezstack/templates/go-service
cp .envrc ~/.ezstack/templates/go-service/
cp -r .vscode ~/.ezstack/templates/go-service/
```

Then create a worktree from the template:

```bash
ezs new feature-x --template go-service
```

The overlay is applied AFTER the worktree is created — existing files are
overwritten, new dirs created, file modes (including the executable bit)
preserved.

Safety: the overlay aborts on symlinks at the template root, any
source/destination path that escapes the worktree (ezs validates symlinks
and `..` components inside the template), and any failure mid-copy. Partial
overlays are never left behind.

Templates compose with everything else — `--from-worktree`, `--from-remote`,
`--parent`, etc. all work alongside `--template`.

---

## Commands

### `ezs agent`

Launch an AI agent with full stack context. The agent is scoped to a single stack and receives stack structure, branch info, and ezstack documentation automatically. **Requires worktree mode** (`use_worktrees: true`) — the agent needs separate working directories for each branch to work in isolation without disrupting your workspace.

```
ezs agent [options] [-- <agent-args>]
ezs agent feature "description"
ezs agent ls [filter] [--json]
ezs agent rm <filter>
ezs agent prompt <flag> <work|feature>

Modes:
    (default)   Work session — agent scoped to a stack with full context
    feature     Feature builder — agent breaks a feature into stacked branches
    ls (list)   List the AI sessions ezs has bound to stacks/branches in this repo
    rm (remove) Forget a tracked session binding (stack, branch, or all)
    prompt      View or edit the prompt templates used by the agent

Options:
    --cmd <command>      Agent CLI to use (default: configured or "claude")
    -s, --stack <hash>   Stack to work on (hash prefix or "name")
    -b, --branch <name>  Branch to work in (implies stack)
    --no-resume          Start a fresh session even if one exists for this branch/stack
    --dry-run            Print the composed prompt and exit (don't launch agent)
    --save-prompt <file> Write the composed prompt to <file> (pairs well with --dry-run)
    --no-push            Set EZS_AGENT_NO_PUSH=1 in the spawned agent's environment
    --preset <name>      Append ~/.ezstack/agent-presets/<name>.md to the composed prompt
    --examples           Print example invocations and exit
    --no-mcp             Do not auto-install/register ezs-mcp; embed docs in
                         the prompt instead (escape hatch for non-claude CLIs
                         or air-gapped environments)

`agent ls` filters (mutually exclusive — default is all sessions in current repo):
    -b, --branch         Show only the session bound to the current branch
    -s, --stack          Show only sessions bound to the current stack
    --feature            Show only sessions created via `ezs agent feature`

Anything after a literal `--` is forwarded to the agent CLI verbatim, so
you can always pass agent-specific flags ezs doesn't know about (e.g.
`ezs agent -- --debug --model opus`).
```

You can run `ezs agent` from any branch, including `main` or other non-stack branches. If you're not on a stack branch, ezstack auto-selects the stack when there is exactly one, or shows an interactive picker when there are multiple stacks. You can always skip the picker with `--stack` or `--branch`.

**`--no-push` and `EZS_AGENT_NO_PUSH`.** When `--no-push` is passed, the child agent process is launched with `EZS_AGENT_NO_PUSH=1` in its environment. Tooling run inside the agent session (hooks, helper scripts, nested `ezs` calls) can check this variable and skip push steps. The variable is only set when `--no-push` is explicitly used; regular `ezs` commands never see it.

#### Session tracking and resumption

`ezs agent` binds a UUID-based session to each stack — or to a single branch when `--branch` is set — and reuses that UUID on every subsequent run against the same scope. Sessions are persisted in `~/.ezstack/stacks.json` under `agent_session_id` (on the stack for stack-scoped runs, on the branch cache for branch-scoped) and survive process restarts. They get cleaned up automatically when you `ezs delete` the branch or the entire stack.

| Run | What ezs does |
|-----|----------------|
| First run for a stack/branch | Mints a fresh UUID and persists it. Exposes the UUID as `EZS_AGENT_SESSION_ID` to the spawned agent. |
| Subsequent runs | Reads the persisted UUID and re-exposes the same value to the agent. |
| `--no-resume` | Forces a brand-new UUID, replacing the persisted one for future runs. |

**Claude integration.** For Claude-family CLIs (`claude`, `claude-code`, etc.), ezs additionally injects flags so claude binds its session to ezs's UUID:

- First run: `claude --session-id <uuid> --name "_ezstack-<identifier>" "<prompt>"`.
- Subsequent runs: `claude --resume <uuid> --name "_ezstack-<identifier>"` (no prompt — claude reopens the prior conversation interactively).

The display name (`_ezstack-<identifier>`) is what shows up in claude's `/resume` picker and the terminal title, so you can tell ezstack-managed sessions apart from ad-hoc ones.

**Other agents.** For agent CLIs ezs doesn't recognize, ezs does **not** inject CLI flags (the schema differs per tool — anything we don't understand might misparse them). The session UUID is still minted, persisted, and exposed via `EZS_AGENT_SESSION_ID`, so user-supplied wrappers can read it and wire their own resume logic on top. Combine `--cmd` with `-- <agent-args>` to forward arbitrary flags to such wrappers.

Use `ezs agent ls` (alias `ezs agent list`) to see every tracked session in the **current repo**, with the stack/branch each session is bound to and the exact `ezs agent` invocation that resumes it. Add `--json` for a machine-readable array suitable for piping into `jq` or other scripts. The JSON object has `scope`, `mode`, `stack_hash`, `stack_name`, `branch_name`, `display_name`, `session_id`, and `resume_cmd`.

Filter the list with mutually-exclusive scope flags:

- `-b` / `--branch` — show only the session bound to the user's current branch. Errors when the current branch isn't tracked by ezstack.
- `-s` / `--stack` — show only sessions bound to the user's current stack (both stack-scoped and branch-scoped sessions in that stack). Errors when the cwd isn't on a stack branch.
- `--feature` — show only sessions created via `ezs agent feature`. The `mode` field on each session row distinguishes work-mode (`"work"`) from feature-mode (`"feature"`); legacy entries written before mode tracking surface as `"work"`.

`agent ls` is intentionally scoped to the current repo only. Sessions from other ezstack-tracked repos in `~/.ezstack/stacks.json` are not surfaced — there's no cross-repo listing flag, by design, because surfacing unrelated sessions creates more confusion than it resolves. Only ezstack-bound sessions are listed — freestanding `claude` sessions you started by hand are not.

#### Forgetting a session binding

`ezs agent rm <filter>` (alias `ezs agent remove`) clears ezs's stored pointer to the AI session for the chosen scope. The next `ezs agent` run against that scope mints a fresh UUID instead of resuming. The agent's own conversation history (claude's session journal, etc.) is not touched — only ezs forgets the pointer. Use `ezs agent ls` to grab the UUID first if you want to resume manually later (`ezs agent -- --resume <uuid>`).

Filters mirror `ezs agent ls` and are mutually exclusive — exactly one is required:

- `-b` / `--branch` — forget the session bound to the current branch. Errors if no session is bound there.
- `-s` / `--stack` — forget the session bound to the current stack. Errors if no session is bound there.
- `--all` — forget every session in the current repo (stack + branch). Asks for confirmation since it touches multiple bindings at once; `-y` / `ui.YesMode` bypasses the prompt.

The bare `ezs agent rm` errors out with guidance — silently picking a default would risk wiping the wrong session.

**Display names follow `/rename`.** The `display_name` column is read live from the agent's session journal, so renaming a session inside Claude (`/rename my-name`) is reflected in `agent ls` on the next run. Fresh sessions and non-claude agents fall back to the deterministic `_ezstack-<identifier>` label. The rename also survives subsequent `ezs agent` resumes — ezs picks up the latest name from the journal and re-asserts it via `claude --name`, so your rename doesn't get clobbered the next time you launch.

**`--preset <name>`.** Looks up `~/.ezstack/agent-presets/<name>.md` and appends it to the end of the fully composed prompt under a `## Preset: <name>` header. Use presets for reusable persona / review-style overlays without having to edit the work/feature prompt files.

**`--save-prompt <file>`.** Writes the fully composed prompt (after all three layers and any `--preset`) to `<file>`. Most useful with `--dry-run` to inspect exactly what the agent would see without spawning it.

#### Automatic MCP integration (Claude Code)

When the configured agent CLI is `claude`, `ezs agent` automatically:

1. **Ensures `ezs-mcp` is installed and version-aligned.** If the binary is
   missing or was built against a different ezstack release, ezs runs
   `go install github.com/KulkarniKaustubh/ezstack/v4/cmd/ezs-mcp@v<version>`,
   falling back to `@latest` for untagged dev builds.
2. **Registers `ezs-mcp` with Claude Code at user scope** (equivalent to
   running `claude mcp add ezstack --scope user -- ezs-mcp` yourself), so
   the full 28-tool ezstack surface is available to the agent from the
   first message.
3. **Swaps the shipped prompt for a short MCP stub** that tells the agent
   to prefer MCP tools over shelling out to `ezs`. The large
   `DOCUMENTATION.md` body is no longer pasted into context &mdash; the
   agent gets the tool schemas directly via MCP, which is both cheaper and
   more reliable than prose instructions.

The result: `ezs agent` on a fresh machine with claude installed is a
single command. No manual `go install`, no manual `claude mcp add`, no
hand-maintained prompt about what commands exist.

Opt out with `--no-mcp` (restores the legacy doc-paste prompt) or by
setting `agent_command` to a non-`claude` CLI &mdash; MCP auto-install is
only attempted when the CLI basename is `claude`.

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

# Pass flags to the agent CLI by quoting the whole value as one shell arg:
ezs config set agent_command "claude --dangerously-skip-permissions"
ezs config set agent_command "aider --model gpt-4"
```

> Multi-word values must be quoted. The CLI takes `<value>` as a single
> positional argument and rejects anything trailing — this keeps typos like
> `ezs config set worktree_base_dir /tmp/foo --bogus` from being silently
> stored as the literal path `/tmp/foo --bogus`.

---

### `ezs commit` / `ezs amend`

Wrap `git commit` / `git commit --amend` and auto-sync child branches. Aliases: `ci`

```
ezs commit [git-commit-options] [--merge|--rebase]
ezs amend [git-commit-options] [--merge|--rebase]
```

All arguments are passed through to `git commit`. After committing, any child branches in the stack are automatically synced onto the updated branch.

Uses the configured `sync_strategy` (default: rebase) for child syncing. Use `--merge` or `--rebase` to override.

**Hooks.** `ezs commit` runs `~/.ezstack/hooks/pre-commit` before the commit (aborting on non-zero exit) and `~/.ezstack/hooks/post-commit` after the commit and the auto-restack complete (warning only). See the [Hooks](#hooks) section below.

---

### `ezs config`

Configure ezstack for the current repository. Aliases: `cfg`

```
ezs config [subcommand] [options]

Subcommands:
    set <key> <value>    Set a configuration value
    show                 Show current configuration
    export <file>        Write the global config to <file> (mode 0600, token redacted)
    import <file>        Replace the global config from <file>
```

#### `ezs config export <file>`

Writes the global config to `<file>` in JSON form with mode `0600`. The `github_token` field, if set, is replaced by the literal sentinel `<redacted-by-ezs-export>` so the exported file is safe to commit, share, or back up to untrusted storage. Repo layouts and other fields are preserved as-is.

```bash
ezs config export ~/ezs-backup.json
```

#### `ezs config import <file>`

Replaces the current global config with the contents of `<file>`. The import is validated against the `Config` schema before it is applied — unknown fields cause the import to fail, so a stale backup from a future schema version won't silently drop data. A summary of changed fields and per-repo diffs is printed before the replacement is committed.

Token handling is safe by default: if the imported `github_token` is the redaction sentinel or is empty, the existing real token is preserved. Only an explicit, non-sentinel token in the import file will overwrite the token currently on disk, so round-tripping an exported file never leaves you without a token.

```bash
ezs config import ~/ezs-backup.json
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
    --cascade              Also delete all descendant branches
```

**`--cascade`.** Recursively deletes all descendants of the target branch before deleting the branch itself. Descendants are computed deepest-first so children are always removed before their parents. Before anything is deleted, ezstack scans every descendant worktree for uncommitted changes — if any descendant is dirty the cascade is aborted with a list of the dirty branches, so a single uncommitted file can never silently shred a subtree. Pass `--force` alongside `--cascade` to override the dirty-worktree check. After the descendants are removed, the root delete proceeds without needing `--force` (children no longer exist).

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

### `ezs doctor`

Check that ezstack's runtime dependencies and on-disk config are healthy.

```
ezs doctor
```

`doctor` does not require being inside a git repository — it's designed to be the first thing you run on a fresh machine. It reports:

- Whether `git`, `gh`, and `fzf` are on `PATH` (all three are required; missing ones are flagged as errors).
- Whether the config directory can be resolved and whether `config.json` loads cleanly.
- For every configured repo: whether `worktree_base_dir` is set, whether that directory exists, and whether it passes the containment validation used by `ezs new`.

Exit code is `0` when no problems are detected, non-zero with a one-line summary otherwise. Pair with `ezs --info` when filing bug reports.

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
ezs goto --search <query>
```

If branch-name is omitted, shows interactive selection. Falls back to `git checkout` when the branch has no worktree.

**`--search <query>`.** Case-insensitive substring fuzzy match against every branch name across every known stack. Exactly one match jumps straight to that worktree; multiple matches open an interactive selector limited to the matching set; zero matches exits with branch-not-found. Useful when you remember part of a branch name but not which stack it lives in.

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

### `ezs menu`

```
ezs menu
```

Opens the interactive command launcher — an fzf-style picker that lists the
common ezstack commands and runs the one you select. Useful when you don't
remember the exact subcommand name. The picker covers: `config`, `delete`,
`goto`, `help`, `new`, `pr`, `reparent`, `stack`, `status`, `sync`, and
`unstack`. On exit (Esc or Ctrl-c) the menu returns without making any
changes.

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
    -s, --init-submodules     Mirror main worktree's initialized submodules (overrides config)
    -S, --no-init-submodules  Skip submodule initialization (overrides config)
    -f, --from-worktree       Register an existing worktree as a stack root
    -r, --from-remote         Create a stack from a remote branch/PR
    --template <name>         Seed the new worktree from ~/.ezstack/templates/<name>
```

**Submodule mirroring.** By default, `ezs new` inspects the main worktree's initialized submodules (via `git submodule status`) and runs `git submodule update --init -- <paths>` on the same paths in the new worktree. Submodules that are deinit'd in the main worktree are left uninitialized in the new worktree too — which matches the monorepo workflow (e.g. SONiC) where developers only init the subset of submodules they work on. Mirroring can be disabled globally via `ezs config set init_submodules false`, and overridden per-invocation with `--init-submodules` / `--no-init-submodules`. A failure to mirror submodules is logged as a warning and does not fail branch creation.

**Submodule auto-refresh after HEAD-changing operations.** When ezstack moves HEAD via rebase, merge, or branch checkout, it follows up with `git submodule update --recursive` so already-initialized submodules advance to the SHA the new HEAD records. This avoids the "submodule pointer changed but working tree didn't" gotcha that bites users after `git rebase` or `git checkout`. The refresh deliberately does *not* pass `--init`, so submodules a user has chosen not to clone stay that way — opt-outs are respected. Failures are warnings, never fatal.

**Submodule doctor checks.** `ezs doctor` walks the current worktree's initialized submodules — including nested submodules-of-submodules, with their full path from the worktree root — and surfaces:

- unresolved merge conflicts (error)
- uncommitted changes (warning)
- local commits in the submodule's checkout that aren't on `origin` (warning — informational; remember to push the submodule too)
- detached-HEAD edits in progress (warning — commits there can be orphaned)
- pointer drift between the parent's record and the submodule's checkout (warning)

A clean repo with N initialized submodules prints `Submodules clean (N initialized)`.

**Submodule push gate.** Before `ezs push` invokes git, it checks the *gitlink SHA the parent will publish* against the submodule's `origin/*` refs. If the gitlink SHA isn't on origin, it warns — teammates won't be able to fetch the submodule SHA the parent records. The push still proceeds (the user may be intentionally pushing parent first), but the warning makes the order explicit. Local commits in the submodule's checkout that haven't been bumped into the parent's gitlink yet are *not* push-gated: pushing the parent in that state publishes the old gitlink, which is on origin, so teammates aren't broken. The doctor surfaces the uncommitted gitlink change separately.

**`--template <name>`.** After the worktree is created, ezstack copies the contents of `~/.ezstack/templates/<name>/` into it as an overlay. Existing files in the worktree are overwritten, new directories are created, and file modes (including the executable bit) are preserved. The copy is guarded:

- The template root must be a real directory, never a symlink.
- The template's own `.git` directory (if any) is skipped — it's an authoring artifact.
- Every source and destination path is validated so a symlink or `..`-laden entry inside the template can't read or write outside the worktree.
- Symlinks inside the template are recreated as symlinks in the destination verbatim (not dereferenced), so intentional symlinks survive and accidental escape links stay dangling rather than exfiltrating data.

If any entry fails these checks the whole overlay aborts with an error — partial template overlays are never left behind.

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
ezs pr --draft-all

Subcommands:
    create    Create a new pull request
    draft     Toggle PR between draft and ready
    merge     Merge a pull request
    refresh   Reconcile cached PR state from GitHub
    stack     Update all PR descriptions with stack info
    unlink    Clear cached PR association for a branch
    update    Push changes and update PR metadata (base branch, descriptions)

Top-level flags:
    --draft-all    Create draft PRs for every branch in the current stack that
                   doesn't already have one
```

**`--draft-all`.** Walks every branch in the current stack and, for any branch that doesn't already have an associated PR, creates a new draft PR against its parent. Branches that already have a PR are left alone (use `ezs pr draft` to toggle an existing PR into draft state). This is the fastest way to seed a full stack of draft PRs for early-visibility review.

**Cache reconciliation.** ezstack caches per-branch PR metadata (`pr_url`, `pr_state`, `is_merged`) in `~/.ezstack/stacks.json`. The `pr` subcommands keep that cache in sync with GitHub:

- `pr create` queries GitHub before refusing to create. If the cached PR is in a terminal state (`MERGED` / `CLOSED`), a new PR is allowed silently. If the PR is still live on GitHub, `pr create` refuses unless `--force` (alias `--recreate`) is passed.
- `pr update` queries GitHub before pushing. If the PR has been merged or closed externally, it refuses to push and updates the cache.
- `pr refresh` and `pr unlink` are the explicit recovery primitives — useful when GitHub state has changed in ways ezstack didn't initiate.

#### `ezs pr create`

```
Options:
    -s, --stack            Create PRs for all branches in the current stack
    --draft-all            Create draft PRs across the whole stack (implies --stack --draft)
    -t, --title <title>    PR title (defaults to branch name)
    -b, --body <body>      PR body/description
    -d, --draft            Create as draft PR
    --branch <name>        Create PR for a specific branch (instead of current)
    --auto, --ai           Use the configured AI agent to draft PR title and body
                           from the diff and the repo's PR template
    -f, --force            Create a new PR even if one already exists
                           (alias: --recreate)
```

**`--force`.** Bypasses the existing-PR guard. When the cached PR has been merged or closed, this is a no-op (the cached terminal state already lets create proceed). When the cached PR is still live on GitHub, a warning is printed and a new PR is created — the existing PR stays open and GitHub may reject the new PR as a duplicate. `--force` does not prompt, so scripts can pass it without holding stdin open.

**`--auto` (alias `--ai`).** Hands the branch's diff, commit messages, and the repo's `pull_request_template.md` to the configured AI agent (`agent_command`) and asks it to fill in `{title, body}`. The result is fed into the regular create flow, so all the usual gating still happens — push, base validation, fork detection, stack-description update. Combine with `-s` / `--stack` to draft a body for every branch in the stack in one go. `-t` / `-b` always win over the AI's output for the field they specify, so you can pin a title and let the AI handle just the body (or vice versa). `--auto` currently requires a Claude-family agent because that's the only CLI ezs can drive non-interactively with predictable JSON output; passing `--cmd` to point at a different binary will be rejected with a clear error. The PR template's location is the standard set GitHub looks at: `.github/pull_request_template.md`, `.github/PULL_REQUEST_TEMPLATE.md`, `docs/pull_request_template.md`, and the repo-root variants.

#### `ezs pr draft`

Toggles the current branch's PR between draft and ready-for-review state.

#### `ezs pr merge`

```
Options:
    -m, --method <method>      Merge method: merge, squash, rebase (default: interactive)
    --branch <name>            Merge PR for a specific branch (instead of current)
    --no-delete-branch         Don't delete the remote branch after merge
```

#### `ezs pr refresh`

Queries GitHub for the current state of one or more PRs and updates the local cache. Use after PRs have been merged, closed, or re-targeted via the GitHub UI to bring `ezs ls` and other commands back in sync without pushing or recreating anything.

```
Options:
    --branch <name>    Refresh a specific branch (instead of current)
    -s, --stack        Refresh every PR in the current stack (parallelized)
```

#### `ezs pr stack`

Update all PR descriptions in the stack with navigation links.

```
Options:
    --branch <name>    Target a specific branch's stack (instead of current)
```

#### `ezs pr unlink`

Clears the cached PR association (`pr_url`, `pr_state`, `is_merged`) for one or more branches. Does not touch GitHub — the PR itself is unaffected. Use when the cached association has gone stale and you want `ezs pr create` to make a fresh PR for the branch.

```
Options:
    --branch <name>    Unlink a specific branch (instead of current)
    --all              Unlink every branch in the current stack
    -y, --yes          Skip the confirmation prompt
```

#### `ezs pr update`

Reconciles cached PR state from GitHub, then pushes code changes and updates the PR base branch and stack descriptions to match the current stack structure. If the PR has been merged or closed externally, refuses to push and reports the new state.

```
Options:
    --branch <name>    Update PR for a specific branch (instead of current)
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
    --verify             Require ~/.ezstack/hooks/pre-push to exist and pass
    --all-remotes        Push to origin and the configured fork remote
```

Each branch pushes to its configured remote — `origin` by default, or the fork remote for fork-based PR branches. Branches marked as read-only (fork PRs where you don't have push access) are skipped with a warning.

**Hooks.** `ezs push` runs `~/.ezstack/hooks/pre-push` before the push (aborting on non-zero exit) and `~/.ezstack/hooks/post-push` after (warning on non-zero exit, but never failing the command). See the [Hooks](#hooks) section for the environment contract.

**`--verify`.** Promotes the `pre-push` hook from optional ("run if present") to required ("must be installed and must pass"). Without `--verify`, a missing hook is simply a no-op; with `--verify`, ezstack fails fast if the hook file is absent or not executable. Useful in CI or shared dev machines where push-time validation is a hard requirement.

**`--all-remotes`.** Pushes each branch to both `origin` and its configured fork remote (if any). Each remote is treated as best-effort — a failure to push to one remote doesn't block the other, and read-only fork branches are still skipped. Without this flag, each branch pushes only to its single "effective" remote (origin for same-repo branches, the fork remote for fork-tracking branches).

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
    --watch [seconds]      Auto-refresh every N seconds (default 5, minimum 2)
```

**`--watch [seconds]`.** Clears the screen and re-runs status on a fixed interval until you interrupt with Ctrl-C. The interval defaults to 5 seconds and is clamped to a 2-second minimum to avoid hammering `gh`. You can pass the interval either space-separated (`--watch 10`) or with `=` (`--watch=10`); non-numeric or non-positive values fall back to the default. Watch mode cannot be combined with `--json` (watch is fundamentally an interactive TTY mode). A clean Ctrl-C / SIGTERM exits without leaving the terminal in an odd state.

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
    --continue             Continue after resolving conflicts (completes rebase/merge, pushes, then re-syncs the entire descendant subtree). Honors -s, -a, -c, -b, and positional <hash-prefix> to limit the scope.
    --merge                Use git merge instead of git rebase
    --rebase               Use git rebase (overrides sync_strategy config)
    --stats                Print a commits-per-branch summary after syncing
    --squash               Squash each child's commits into one before rebasing onto parent
    --no-delete-local      Don't delete local branches after their PRs are merged
    --dry-run              Preview what would be synced without making changes
    --no-autostash         Don't stash uncommitted changes before rebase (autostash is on by default)
    --json                 Output dry-run results as JSON (requires --dry-run)
    --include-remote-worktrees   Include pickup branches (`ezs new origin/<branch>` / `-r`) in
                                 bulk sync. Excluded by default to avoid rewriting another
                                 contributor's history.
```

**`--stats`.** Prints a post-sync summary listing, for each branch in the synced set, the number of commits ahead of its parent after the sync completes. The summary is registered so it runs after the `post-sync` hook fires (via LIFO-ordered defers), so the numbers you see reflect the final state on disk.

**`--include-remote-worktrees`.** Pickup branches — created by `ezs new origin/<branch>` or `ezs new -r` — belong to another contributor. Bulk sync (`ezs sync -a` / `-s` / interactive auto-sync) skips them by default: rebasing rewrites their history, and a follow-up force-push would clobber the contributor's work. Pass `--include-remote-worktrees` to opt in (e.g., to fast-forward your local copy of the remote branch when you do own the underlying repo). Per-branch sync (`-b <name>` / `-c`) is unaffected — those forms are explicit about which branch to touch.

**`--squash`.** Before rebasing each child onto its parent, collapses the child's commits into a single commit. Only branches with ≥2 commits since their parent are affected; branches that are already a single commit are left alone. Because `--squash` rewrites history, any already-pushed branch will need `git push --force-with-lease` afterward — ezstack prints a warning reminding you of this up front.

**Hooks.** `ezs sync` runs `~/.ezstack/hooks/pre-sync` before the sync (aborting on non-zero exit) and `~/.ezstack/hooks/post-sync` after (warning only). See the [Hooks](#hooks) section below.

**Picking up collaborator commits.** Before rebasing each branch onto its parent, sync also fast-forwards the local branch to `origin/<branch>` when a teammate has pushed new commits there. Strict fast-forward only — if your local has unpushed commits *and* the remote has commits you don't, sync prints a `diverged` note and skips the pull (auto-pulling could re-introduce pre-rebase parent commits when the local was just ezstack-rebased). Run `git pull --rebase` in the worktree yourself in that case.

**Cascading conflicts.** When the parent of a stacked chain conflicts with the new base, only the parent's rebase hits the conflict — children get rebased with `git rebase --onto newParent oldParentSHA`, replaying just their own commits. The pre-rebase SHA of every selected branch is snapshotted into `~/.ezstack/stacks.json` (field `pre_sync_commit`) before any rewriting starts, so a later `ezs sync --continue` (a separate process) can use it. `git rerere` is also auto-enabled on the repo on first sync as a safety net for hunks that recur across siblings.

**Concurrent syncs.** A non-blocking exclusive lock on `~/.ezstack/stacks.json.sync.lock` prevents two `ezs sync` invocations (including `--continue`) from racing on snapshot reads/writes or PR-metadata updates. A second invocation while one is already running fails fast with "another `ezs sync` is already running". Backed by `flock` on Unix and `LockFileEx` on Windows. `--dry-run` skips the lock since it's read-only.

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

### `ezs upgrade`

Self-update the running `ezs` binary (and the companion `ezs-mcp` if installed) to the latest published GitHub release.

```
ezs upgrade [options]
ezs update  [options]    # alias

Options
  --check            Print current vs latest version and exit (no download)
  --version <tag>    Pin to a specific release tag (e.g. v4.6.3)
  --force            Reinstall even when already at the target version
  --no-mcp           Skip the companion ezs-mcp binary
  -y, --yes          Skip the replace-binaries confirmation
```

`upgrade` does not require being inside a git repository. It works in three steps:

1. Resolves the running binary path with `os.Executable()` and classifies the install:
   - **Homebrew** (`/opt/homebrew/Cellar/ezstack/...`, `/usr/local/Cellar/ezstack/...`, `/home/linuxbrew/.linuxbrew/Cellar/ezstack/...`) — prints `brew upgrade ezstack` and exits (so brew's receipt of the install stays in sync with the binary on disk).
   - **`go install`** (under `$GOBIN`, `$GOPATH/bin`, or `~/go/bin`) — re-runs `go install github.com/KulkarniKaustubh/ezstack/v<major>/cmd/ezs@<tag>` (and `cmd/ezs-mcp@<tag>` when an existing `ezs-mcp` is present) so the Go toolchain stays the source of truth for the install location. The major-version segment is derived from the resolved release tag.
   - Otherwise — proceeds with an in-place swap.
2. Hits the GitHub Releases API for the requested tag (default: `/releases/latest`), downloads `ezstack_<os>_<arch>.tar.gz` plus `checksums.txt`, and verifies the SHA-256. The `go install` path skips this step — the toolchain hashes the module against `go.sum` itself.
3. Atomically renames the new binaries on top of the old ones (per binary, in their own directories — `ezs` and `ezs-mcp` can live in different bin dirs). On Unix this is safe even for the currently-running process: the kernel keeps the old inode alive until exit.

`ezs-mcp` is resolved in two stages so a split-directory install layout still upgrades in lock-step:

- **Sibling first.** If `ezs-mcp` lives next to the running `ezs`, that copy is swapped (the happy path for Homebrew, manual tarball, and "drop both binaries together" installs).
- **`PATH` fallback.** Otherwise `exec.LookPath("ezs-mcp")` is consulted, so an `ezs-mcp` planted by `ezs agent`'s `go install` at `~/go/bin/ezs-mcp` is still updated when `ezs` itself was installed under, say, `~/.local/bin/`. A Homebrew-managed `ezs-mcp` resolved through `PATH` is intentionally skipped — the user is asked to run `brew upgrade ezstack` so brew's receipt of the install stays in sync. The swap is otherwise all-or-nothing across both binaries: a failed `ezs-mcp` rename rolls `ezs` back to its previous version.

Exit codes: `0` success, `1` general I/O / extraction failure, `2` usage error, `8` GitHub API or download failure, `10` user declined the confirm prompt.

`ezs-mcp` exposes the same flow under `--upgrade`, `--upgrade-check`, `--upgrade-tag`, and `--upgrade-force` for installations that ship the MCP binary without the CLI.

---

## Exit codes

`ezs` returns these exit codes; each is wrapped via `ui.NewExitError` in
`internal/ui/ui.go`. Use them to drive scripts and CI gates without parsing
stderr.

| Code | Meaning |
|------|---------|
| 0  | Success |
| 1  | General error |
| 2  | Usage / argument error |
| 3  | Rebase conflict |
| 4  | Not in a git repository |
| 5  | Not in a stack |
| 6  | GitHub authentication required |
| 7  | Branch not found |
| 8  | Network / remote error |
| 10 | User cancelled |

---

## Hooks

ezstack runs optional user-defined shell hooks around certain commands. Hooks live in `~/.ezstack/hooks/` and follow a strict `{pre,post}-{commit,push,sync}` naming convention.

### Installed hook names

| Hook | Fires for | Contract |
|------|-----------|----------|
| `pre-commit`  | `ezs commit` / `ezs amend` | non-zero exit aborts the commit |
| `post-commit` | `ezs commit` / `ezs amend` | non-zero exit warns only |
| `pre-push`    | `ezs push`                 | non-zero exit aborts the push |
| `post-push`   | `ezs push`                 | non-zero exit warns only |
| `pre-sync`    | `ezs sync`                 | non-zero exit aborts the sync |
| `post-sync`   | `ezs sync`                 | non-zero exit warns only |

### Install a hook

A hook is simply an executable file at `~/.ezstack/hooks/<name>`. It is executed directly (not through `sh -c`), so it must carry the executable bit and, if it is a script, start with a valid shebang line. Non-existent, non-executable, or directory entries at those paths are treated as "no hook installed" — a no-op, not an error.

```bash
mkdir -p ~/.ezstack/hooks
cat > ~/.ezstack/hooks/pre-push <<'SH'
#!/usr/bin/env bash
set -e
echo "pre-push on $EZS_BRANCH in $EZS_REPO_ROOT"
SH
chmod +x ~/.ezstack/hooks/pre-push
```

### Exit-code contract

- `pre-*` hooks abort the action on non-zero exit. ezstack returns an error and the underlying git operation never happens.
- `post-*` hooks warn on non-zero exit but never abort the command — the underlying action has already succeeded by the time they fire, so a flaky notifier script can't "fail" a successful push.

### Environment

Each hook runs with `stdin`, `stdout`, and `stderr` inherited from `ezs`, so it can prompt the user or stream output normally. The following variables are added to the environment (empty fields are simply not set):

| Variable | Description |
|----------|-------------|
| `EZS_HOOK`       | The hook name (e.g. `pre-push`) |
| `EZS_REPO_ROOT`  | Absolute path to the repo root |
| `EZS_BRANCH`     | Current branch, if known |
| `EZS_STACK_HASH` | Current stack hash, if known |
| `EZS_STACK_NAME` | Current stack name, if set |

When the hook is invoked from an `ezs agent` session that was launched with `--no-push`, the spawned agent process also has `EZS_AGENT_NO_PUSH=1` in its environment — a hook can check this to short-circuit push logic while the agent is driving.

### Requiring a hook

`ezs push --verify` promotes `pre-push` from optional to required: ezstack aborts with an error if the hook file is missing or not executable. This is the knob to use in CI or on shared machines where push-time validation is non-negotiable.

### Dry-run contract

Hooks only fire when ezstack is about to mutate state. Preview commands never invoke them:

- `ezs sync --dry-run` does **not** run `pre-sync` or `post-sync`. A failing `pre-sync` hook will not block a dry-run, and a `--dry-run --squash` combination will not rewrite history — the squash is part of the dry-run short-circuit as well.
- `ezs sync --continue` runs `post-sync` (to signal "sync is done") but never re-runs `pre-sync`; the original invocation already fired it.
- `ezs commit` / `ezs push` have no dry-run mode of their own, so their hook behaviour is unconditional.

### Hook recipes

Real-world examples that lean on the `EZS_*` env or events with no git-hook equivalent. Each recipe is a complete file you can drop into `~/.ezstack/hooks/<name>` and `chmod +x`.

#### Sync submodule pointers after a rebase

`ezs new` mirrors initialized submodules into a freshly created worktree, but `ezs sync` does not run `git submodule update` after rebasing — so a rebase that bumps a submodule SHA leaves the working copy stale. A `post-sync` hook keeps them aligned, and skips uninitialized submodules so it works in monorepos where developers only init the subset they care about.

```bash
#!/usr/bin/env bash
# ~/.ezstack/hooks/post-sync
set -euo pipefail
cd "$EZS_REPO_ROOT"

# `git submodule status` prefixes uninitialized entries with `-`; skip those
# and only update what the user has already opted into.
inited=$(git submodule status --recursive \
  | awk '$0 !~ /^-/ {print $2}')

[ -z "$inited" ] && exit 0

# shellcheck disable=SC2086
git submodule update --init --recursive -- $inited
```

The hook fires once at the end of the cascade (not per branch), and `post-sync` is warning-only — a flaky submodule remote will print a warning but won't fail the sync. Pair with a `pre-sync` that refuses to start when submodules have uncommitted changes:

```bash
#!/usr/bin/env bash
# ~/.ezstack/hooks/pre-sync
set -euo pipefail
cd "$EZS_REPO_ROOT"

if ! git submodule foreach --quiet 'git diff --quiet || exit 1'; then
  echo "Refusing to sync: submodule has uncommitted changes." >&2
  exit 1
fi
```

#### Skip slow checks when an agent is driving

`ezs agent --no-push` exposes `EZS_AGENT_NO_PUSH=1` to the spawned agent and any nested `ezs` calls. Use it to keep the full test suite gating human pushes while letting the agent iterate fast:

```bash
#!/usr/bin/env bash
# ~/.ezstack/hooks/pre-push
set -euo pipefail
cd "$EZS_REPO_ROOT"

if [ "${EZS_AGENT_NO_PUSH:-}" = "1" ]; then
  echo "Agent push detected; skipping full check suite."
  exit 0
fi

go test ./...
golangci-lint run
```

Combine with `ezs push --verify` on shared dev hosts and CI to make the hook mandatory: a missing or non-executable `~/.ezstack/hooks/pre-push` is then a hard error, not a no-op.

#### Per-stack policy

`EZS_STACK_NAME` lets a single hook file route to different rules per stack. Use it to gate release stacks more strictly than wip stacks, or to enable submodule sync only where it matters:

```bash
#!/usr/bin/env bash
# ~/.ezstack/hooks/pre-push
set -euo pipefail
cd "$EZS_REPO_ROOT"

case "$EZS_STACK_NAME" in
  release-*)
    go test ./...
    golangci-lint run
    gitleaks protect --staged
    ;;
  wip-*|spike-*)
    go vet ./...
    ;;
  "")
    # Branch isn't part of a named stack — minimal gate.
    go vet ./...
    ;;
  *)
    go test ./...
    ;;
esac
```

The hook fires per branch on stack-wide pushes (`ezs push --stack`), so `EZS_BRANCH` and `EZS_STACK_HASH` are also available if you want per-branch policy inside a stack.

#### Snapshot dirty state across a sync

`pre-sync` aborts the sync; `post-sync` warns only. Combined, they bracket the cascade and let you stash uncommitted work safely without ezs's autostash:

```bash
#!/usr/bin/env bash
# ~/.ezstack/hooks/pre-sync
set -euo pipefail
cd "$EZS_REPO_ROOT"

if ! git diff --quiet || ! git diff --cached --quiet; then
  stash_msg="ezs-sync-snapshot $(date -u +%FT%TZ) $EZS_BRANCH"
  git stash push -u -m "$stash_msg"
  echo "$stash_msg" > "$EZS_REPO_ROOT/.git/ezs-sync-stash"
fi
```

```bash
#!/usr/bin/env bash
# ~/.ezstack/hooks/post-sync
set -euo pipefail
cd "$EZS_REPO_ROOT"

marker="$EZS_REPO_ROOT/.git/ezs-sync-stash"
[ -f "$marker" ] || exit 0

stash_msg=$(cat "$marker")
rm -f "$marker"

# Find the stash by message; pop it if found.
ref=$(git stash list | awk -F: -v msg="$stash_msg" '$0 ~ msg {print $1; exit}')
[ -n "$ref" ] && git stash pop "$ref"
```

Why a hook and not ezs's built-in autostash: the hook persists across `ezs sync --continue`, includes the stack name in the stash message, and keeps the marker on a per-worktree basis (`.git/` is worktree-local).

#### Open the PR in your browser after push

A `post-push` one-liner that turns "I just pushed" into "the PR is on screen":

```bash
#!/usr/bin/env bash
# ~/.ezstack/hooks/post-push
set -euo pipefail
cd "$EZS_REPO_ROOT"

# `gh pr view --web` no-ops cleanly when the branch has no PR yet.
gh pr view --web "$EZS_BRANCH" 2>/dev/null || true
```

`post-push` is warning-only, so a missing `gh` (or no PR for the branch) won't fail the command.

---

## Discoverability

A handful of ergonomics make it easier to discover commands and debug problems: `--info` for diagnostics, `--examples` for per-command recipes, and a built-in "did you mean…?" suggester for typos.

### `ezs --info`

Prints a diagnostic dump (ezstack version, `go`/`git`/`gh`/`fzf` versions, config directory, whether `config.json` is present and loads cleanly, repo count, default base branch). Designed to be pasted into bug reports — no secrets, no stack contents.

### `ezs <command> --examples`

Most commands accept a `--examples` flag that prints a short list of usage recipes with one-line descriptions and exits. Currently registered for: `commit`, `sync`, `push`, `pr`, `new`, `delete`, `goto`, `agent`, `config`, `status`, `doctor`. Output goes to stdout so it can be piped or grepped. Example:

```bash
$ ezs sync --examples
Examples for 'ezs sync'

  # Interactive sync of current stack
  ezs sync

  # Auto-sync ALL stacks
  ezs sync -a

  # Show summary of commits rebased per child
  ezs sync --stats
  ...
```

### "Did you mean…?"

When you run an unknown top-level command, ezstack computes a Levenshtein-based suggestion against the known command set and prints a `Did you mean 'X'?` hint. This catches typos like `ezs statsu` → `status` or `ezs comit` → `commit` without hunting through `--help`.

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
loop. 28 tools, one binary.

**Install**

```bash
# Homebrew (ships alongside ezs)
brew tap KulkarniKaustubh/ezstack
brew install ezstack

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
| `ezstack_status` | read-only | Current stack with PR and CI status. `all`, `branch`, `decorated`. |
| `ezstack_list` | read-only | List all stacks and branches. `all`, `decorated`. |
| `ezstack_diff` | read-only | Diff against parent branch as JSON numstat (default) or diffstat. `branch`, `stat`. |
| `ezstack_log` | read-only | Commits since parent as JSON (hash, message, author, ISO date). `branch`. |
| `ezstack_doctor` | read-only | Run diagnostics: ezstack version, prerequisite versions (go/git/gh/fzf), config directory state, and the configured default base branch. Safe to paste into bug reports — no secrets included. |
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
| `ezstack_push` | destructive | Push current branch or entire stack. `branch`, `stack`, `force`. |

**Pull requests**

| Tool | Annotation | Description |
|---|---|---|
| `ezstack_pr_create` | &mdash; | Create a pull request. `branch`, `title`, `draft`, `auto` (delegate title/body drafting to the configured `agent_command`; combine with `stack=true` to draft for every branch in the stack). |
| `ezstack_pr_update` | destructive | Push the latest commits and refresh the PR base branch / stack description. `branch`. |
| `ezstack_pr_merge` | destructive | Merge the pull request for a branch. `branch`, `method`. |
| `ezstack_pr_draft` | &mdash; | Toggle a PR between draft and ready-for-review. `branch`. |
| `ezstack_pr_draft_all` | &mdash; | Create draft PRs for every branch in the current stack that doesn't already have one. Branches with an existing PR are left alone (use `ezstack_pr_draft` to toggle an existing PR's draft state). |
| `ezstack_pr_stack` | &mdash; | Update every PR description in the stack with navigation links. `branch`. |
| `ezstack_pr_refresh` | &mdash; | Reconcile the local PR cache from GitHub (use after PRs are merged/closed/re-targeted via the GitHub UI). `branch` or `stack` (mutually exclusive); omit both for the current branch. |

**Configuration**

| Tool | Annotation | Description |
|---|---|---|
| `ezstack_config_set` | &mdash; | Set a config value. `key` and `value` (both required). Valid keys: `worktree_base_dir`, `default_base_branch`, `github_token`, `cd_after_new`, `use_worktrees`, `sync_strategy`, `agent_command`. |
| `ezstack_config_export` | &mdash; | Export the global ezstack config (excluding the per-machine `github_token`) to a file. Safe to share — no secrets included. `file` (required). |
| `ezstack_config_import` | destructive | Replace the global ezstack config with the contents of `<file>`. Validated against the schema before being applied; the existing local `github_token` is preserved. `file` (required). |

Read-only inspection tools return JSON by default; `ezstack_status` and
`ezstack_list` accept `decorated=true` for terminal-styled output. Destructive
tools are tagged with the MCP destructive annotation so the client prompts
before running them. Branch-management tools mark their positional arguments as
`Required` in the tool schema so the agent cannot trigger an interactive `fzf`
selection that would hang in a no-terminal context. `ezstack_commit` requires
an explicit `message` and `ezstack_amend` defaults to `--no-edit` so neither
can ever launch `$EDITOR` and corrupt the JSON-RPC transport.

**Branch targeting from non-stack branches** &mdash; most tools accept an
optional `branch` parameter so they can be used when the MCP server's working
directory is on a non-stack branch like `main`. Pass the target branch name
explicitly and the tool resolves the stack from config instead of relying on
`GetCurrentStack()`. For `ezstack_list`, pass `all=true` to discover all
stacks. Tools that operate on the working tree (`ezstack_commit`,
`ezstack_amend`) are inherently tied to the current worktree and should be
invoked from the correct branch's directory.

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
code --install-extension ezstack-4.8.5.vsix

# From source
cd vscode-extension
npm install
npm run compile
npx vsce package
code --install-extension ezstack-4.8.5.vsix
```

**Commands** are available under the `ezstack:` prefix in the command palette
(`Cmd+Shift+P`):

- **Branch ops**: `New Branch`, `Sync`, `Sync Branch`, `Push Branch`,
  `Push Stack`, `Delete Branch`, `Delete Branch and Descendants (Cascade)`,
  `Reparent Branch`
- **PR ops**: `Create PR`, `Update PR`, `Merge PR`, `Toggle PR Draft`,
  `Update Stack Info in PRs`, `Refresh PR State from GitHub`,
  `Unlink PR (Clear Cached Association)`
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

Distributed as [`ezstack.nvim`](https://github.com/KulkarniKaustubh/ezstack.nvim). Native Lua plugin for Neovim 0.10+. Exposes a
single `:Ezs` user command with subcommand and flag completion, plus a styled
stack viewer buffer, Telescope pickers, and a statusline component.

**Install (lazy.nvim)**

```lua
{
  "KulkarniKaustubh/ezstack.nvim",
  cmd    = { "Ezs" },
  keys   = { { "<leader>ez", "<cmd>Ezs<cr>", desc = "Ezstack viewer" } },
  config = function()
    require("ezstack").setup()
    require("telescope").load_extension("ezstack")  -- optional
  end,
}
```

`packer.nvim` and a manual `runtimepath+=...` install also work &mdash; see
the [ezstack.nvim README](https://github.com/KulkarniKaustubh/ezstack.nvim#readme)
for the alternatives.

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

**Tests** &mdash; a plenary.nvim busted suite lives in the `ezstack.nvim` repo
under `tests/`. From inside that repo, run:
`nvim --headless --noplugin -u tests/minimal_init.lua -c "PlenaryBustedDirectory tests/ {minimal_init = 'tests/minimal_init.lua', sequential = true}"`.
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

---

## FAQ — "I want to…"

A scannable index for the questions that come up most often. Each row points
at the workflow (or command reference) that walks through it end-to-end — no
command shown here is invented; if you can't find what you need below, the
[Commands](#commands) section is the full surface.

**Setting up & adopting work**

- **I want ezstack to start tracking an existing branch that already has a
  worktree.** → [Adopting an Existing Worktree as a Stack Root](#adopting-an-existing-worktree-as-a-stack-root). Run `ezs new --from-worktree`, pick the worktree from the picker.
- **I have an untracked worktree I want to graft onto an existing stack.** →
  [`ezs stack`](#ezs-stack) — interactive picker, or `ezs stack -b <branch> -p <parent>` scripted.
- **I want to start a new stack on a non-default base (e.g. `develop`,
  `staging`).** → `ezs stack -b <branch> -B develop`. See [`ezs stack`](#ezs-stack).
- **I'm on a fresh laptop or a CI runner.** → [Bootstrapping a Fresh Machine](#bootstrapping-a-fresh-machine).

**Working with remote branches & teammates' PRs**

- **I want to check out a teammate's branch / PR to review or run it.** →
  [Reviewing a Remote PR](#reviewing-a-remote-pr). One command:
  `ezs new origin/<branch>` (or `ezs new -r <pr-number>`).
- **I want to build a branch on top of a teammate's in-flight PR.** →
  [Stacking on a Remote PR](#stacking-on-a-remote-pr). Run `ezs stack` and
  pick "Start a new stack from a remote PR".

**Restructuring an existing branch**

- **I have one big WIP branch and want to break it into a stack of small
  PRs.** → [Splitting a Single Large Worktree Into a Stack — Manually](#splitting-a-single-large-worktree-into-a-stack--manually), or have the agent plan it via [Splitting a Single Large Worktree Into a Stack — With `ezs agent feature`](#splitting-a-single-large-worktree-into-a-stack--with-ezs-agent-feature).
- **I want to move a branch under a different parent.** → [`ezs reparent`](#ezs-reparent).
- **I want to stop tracking a branch in ezstack but keep the git branch /
  worktree.** → [`ezs unstack`](#ezs-unstack).
- **I want to delete a branch and its descendants in one shot.** →
  [`ezs delete --cascade`](#ezs-delete).

**PR creation & maintenance**

- **I want the AI to write the title and body for one PR.** →
  [AI-Drafted PR Title & Body for a Single Branch](#ai-drafted-pr-title--body-for-a-single-branch---auto). One command: `ezs pr create --auto`.
- **I want to open a PR for every branch in my current stack.** →
  [AI-Drafted PRs Across the Whole Stack](#ai-drafted-prs-across-the-whole-stack). `ezs pr create --stack --auto` (with AI), or `ezs pr --draft-all` (branch-name titles, no AI).
- **My PR cache and GitHub disagree, and I want to start over for a
  branch.** → [Resetting a Stale PR Association](#resetting-a-stale-pr-association--unlink-then-create-or-refresh). `ezs pr unlink` then `ezs pr create`.
- **My PR cache is stale because the PR was merged / closed / re-targeted via
  the GitHub UI.** → `ezs pr refresh` re-discovers and re-syncs the cache
  without making a new PR.
- **I want to update an open PR's base branch and stack-navigation links
  after re-parenting.** → [`ezs pr update`](#ezs-pr-update), or
  `ezs pr stack` to update only the stack-description block.

**Commits, sync, and push**

- **I made a fix on a branch in the middle of my stack — I don't want
  descendants left behind on the old SHA.** → [Committing into the middle of an existing stack](#committing-into-the-middle-of-an-existing-stack). `ezs commit -am "..."` auto-rebases descendants and force-pushes them.
- **A parent branch was merged on GitHub and the rest of my stack needs to
  re-root.** → [After a Parent is Merged](#after-a-parent-is-merged).
  `ezs sync --all` does the surgery.
- **My `ezs sync` hit a rebase conflict.** → [Recovering from a Sync Conflict](#recovering-from-a-sync-conflict). Resolve it, `git rebase --continue`, then `ezs sync --continue` to finish the cascade.
- **I want each branch in my stack to land as a single commit.** →
  [Squash-Merging Children Into Their Parent Before PR](#squash-merging-children-into-their-parent-before-pr). `ezs sync --stack --squash`.
- **I want pushes to be gated by tests / lints / a custom check.** →
  [Pre-Push Validation as a Hard Requirement](#pre-push-validation-as-a-hard-requirement---verify). Drop a hook at `~/.ezstack/hooks/pre-push` and run `ezs push --verify`.

**Day-two ergonomics**

- **I want a live dashboard of CI / review status across my stack.** →
  [Live CI Dashboard With `ezs status --watch`](#live-ci-dashboard-with-ezs-status---watch).
- **I want every new worktree to come pre-populated with the same dotfiles
  / IDE settings.** → [Worktree Templates: Pre-Seeded New Branches](#worktree-templates-pre-seeded-new-branches). `ezs new <name> --template <tpl>`.
- **I want to back up my ezstack config to another machine.** →
  [`ezs config export`](#ezs-config-export-file) and
  [`ezs config import`](#ezs-config-import-file) — token redaction is automatic.
- **I want to drive ezstack from Claude Code, VS Code, Neovim, or a desktop
  app instead of the CLI.** → [Editor & Desktop Integrations](#editor--desktop-integrations). All four read and write the same on-disk state.

If a scenario isn't covered above, the [Workflows](#workflows) section has
the full annotated walkthrough, and [Commands](#commands) has every flag on
every subcommand.
