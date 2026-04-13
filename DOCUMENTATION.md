<div align="center">

# ezstack docs

**Comprehensive guide to using ezstack**

</div>

---

**Table of Contents**

[Overview](#overview) · [Installation](#installation) · [Configuration](#configuration) · [Commands](#commands) · [Workflows](#workflows) · [Editor & Desktop Integrations](#editor--desktop-integrations)

**Commands:** [agent](#ezs-agent) · [new](#ezs-new) · [status](#ezs-status) · [list](#ezs-list) · [sync](#ezs-sync) · [goto](#ezs-goto) · [up/down](#ezs-up--ezs-down) · [pr](#ezs-pr) · [commit/amend](#ezs-commit--ezs-amend) · [push](#ezs-push) · [diff](#ezs-diff) · [delete](#ezs-delete) · [reparent](#ezs-reparent) · [stack](#ezs-stack) · [unstack](#ezs-unstack) · [config](#ezs-config) · [doctor](#ezs-doctor)

**Extras:** [Hooks](#hooks) · [Discoverability](#discoverability-info---examples-did-you-mean)

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
--info           Print a diagnostic dump (versions, config state) for bug reports
```

`ezs --info` prints ezstack version, `go`/`git`/`gh`/`fzf` versions, the config directory path, whether `config.json` is present and loads cleanly, the number of configured repos, and the default base branch. It's safe to paste into bug reports — no secrets are included.

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
    --save-prompt <file> Write the composed prompt to <file> (pairs well with --dry-run)
    --no-push            Set EZS_AGENT_NO_PUSH=1 in the spawned agent's environment
    --preset <name>      Append ~/.ezstack/agent-presets/<name>.md to the composed prompt
    --examples           Print example invocations and exit
```

**`--no-push` and `EZS_AGENT_NO_PUSH`.** When `--no-push` is passed, the child agent process is launched with `EZS_AGENT_NO_PUSH=1` in its environment. Tooling run inside the agent session (hooks, helper scripts, nested `ezs` calls) can check this variable and skip push steps. The variable is only set when `--no-push` is explicitly used; regular `ezs` commands never see it.

**`--preset <name>`.** Looks up `~/.ezstack/agent-presets/<name>.md` and appends it to the end of the fully composed prompt under a `## Preset: <name>` header. Use presets for reusable persona / review-style overlays without having to edit the work/feature prompt files.

**`--save-prompt <file>`.** Writes the fully composed prompt (after all three layers and any `--preset`) to `<file>`. Most useful with `--dry-run` to inspect exactly what the agent would see without spawning it.

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
    --template <name>         Seed the new worktree from ~/.ezstack/templates/<name>
```

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
    stack     Update all PR descriptions with stack info
    update    Push changes and update PR metadata (base branch, descriptions)

Top-level flags:
    --draft-all    Create draft PRs for every branch in the current stack that
                   doesn't already have one
```

**`--draft-all`.** Walks every branch in the current stack and, for any branch that doesn't already have an associated PR, creates a new draft PR against its parent. Branches that already have a PR are left alone (use `ezs pr draft` to toggle an existing PR into draft state). This is the fastest way to seed a full stack of draft PRs for early-visibility review.

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
    --merge                Use git merge instead of git rebase
    --rebase               Use git rebase (overrides sync_strategy config)
    --no-delete-local      Don't delete local branches after their PRs are merged
    --dry-run              Preview what would be synced without making changes
    --continue             Continue after resolving conflicts
    --no-autostash         Don't stash uncommitted changes before rebase (autostash is on by default)
    --json                 Output dry-run results as JSON (requires --dry-run)
    --stats                Print a commits-per-branch summary after syncing
    --squash               Squash each child's commits into one before rebasing onto parent
```

**`--stats`.** Prints a post-sync summary listing, for each branch in the synced set, the number of commits ahead of its parent after the sync completes. The summary is registered so it runs after the `post-sync` hook fires (via LIFO-ordered defers), so the numbers you see reflect the final state on disk.

**`--squash`.** Before rebasing each child onto its parent, collapses the child's commits into a single commit. Only branches with ≥2 commits since their parent are affected; branches that are already a single commit are left alone. Because `--squash` rewrites history, any already-pushed branch will need `git push --force-with-lease` afterward — ezstack prints a warning reminding you of this up front.

**Hooks.** `ezs sync` runs `~/.ezstack/hooks/pre-sync` before the sync (aborting on non-zero exit) and `~/.ezstack/hooks/post-sync` after (warning only). See the [Hooks](#hooks) section below.

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

---

## Discoverability: `--info`, `--examples`, "did you mean"

A handful of ergonomics make it easier to discover commands and debug problems.

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

# For fork PRs, ezstack auto-detects the fork remote:
#   - If maintainer push is enabled AND you have access → adds fork remote, pushes there
#   - Otherwise → marks branch read-only, skips push during sync

# When you're done, clean up
ezs delete feature-branch
```

### Stacking on a Remote PR

```bash
ezs stack
# Select "Start a new stack from a remote PR"
# Pick the PR, then pick your branch to stack on top
```

---

## Editor & Desktop Integrations

ezstack ships with three first-party clients that wrap the `ezs` CLI. They all
read and write the same on-disk state (`~/.ezstack/stacks.json` and per-repo
config), so you can mix and match them freely &mdash; the CLI, your editor, and
the desktop app all stay in sync.

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
