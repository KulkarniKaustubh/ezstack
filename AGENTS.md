# AGENTS.md — AI-Assisted Workflows with ezstack

This document describes how to use ezstack effectively with AI coding agents (Claude Code, Cursor, Copilot, etc.).

## Machine-Readable Output

Use `--json` for programmatic access to stack state:

```bash
# Get stack structure as JSON
ezs ls --json

# Preview sync operations as JSON
ezs sync --dry-run --json
```

## Structured Exit Codes

ezstack returns specific exit codes that agents can use for control flow:

| Code | Meaning | Agent Action |
|------|---------|--------------|
| 0 | Success | Continue |
| 1 | General error | Report to user |
| 2 | Usage error | Fix command syntax |
| 3 | Rebase conflict | Resolve conflicts, then `git rebase --continue` |
| 4 | Not in a git repo | cd to a repo first |
| 5 | Not in a stack | Create a branch with `ezs new` |
| 6 | Auth required | Run `gh auth login` |
| 7 | Branch not found | Check branch name |
| 8 | Network error | Check connectivity |
| 10 | User cancelled | Respect cancellation |

## Non-Interactive Mode

Use `-y` / `--yes` to skip confirmation prompts:

```bash
ezs -y sync -a          # Auto-sync without confirmation
ezs -y delete feature-1 # Delete without confirmation
```

## Common Agent Workflows

### Environment sanity check

```bash
# Verify git, gh, fzf, and ezstack config are all healthy.
# Exits non-zero with a problem count if anything is missing or broken.
ezs doctor
```

Run this as the first step of any automated workflow on a fresh machine — it surfaces missing dependencies before the agent starts issuing `ezs` commands that would fail with cryptic errors deep in the pipeline.

### Creating a stacked PR series

```bash
ezs new feature-part1
# ... make changes ...
ezs commit -m "Add part 1"
ezs new feature-part2 --parent feature-part1
# ... make changes ...
ezs commit -m "Add part 2"
ezs -y pr create -t "Part 1" -d   # Create as draft
ezs goto feature-part2
ezs -y pr create -t "Part 2" -d
```

### Checking stack state

```bash
# Machine-readable stack info
ezs ls --json

# Check what needs syncing
ezs sync --dry-run --json
```

### Syncing after changes

```bash
# Auto-sync with stash support
ezs -y sync -a
```

## Built-in Agent Command

ezstack can launch an AI agent with full stack context injected automatically.

> **Requires worktree mode** (`use_worktrees: true`, which is the default). The agent needs separate working directories for each branch to work in isolation without disrupting your workspace. If worktrees are disabled, `ezs agent` will show an error with instructions to enable them.

### Work Session — Agent scoped to current branch

```bash
# Launch agent on current branch with stack context
ezs agent

# Launch on a specific branch
ezs agent --branch feature-auth

# Block the child agent from auto-pushing during its run
ezs agent --no-push

# Append a saved preset (~/.ezstack/agent-presets/<name>.md) to the composed prompt
ezs agent --preset reviewer

# Dump the composed prompt to a file instead of launching (handy with --dry-run)
ezs agent --dry-run --save-prompt /tmp/agent-prompt.md

# Show registered examples for the agent command
ezs agent --examples
```

### Agent flags

| Flag | Effect |
|------|--------|
| `--cmd <command>` | Override the configured agent CLI |
| `-s, --stack <hash>` | Target a stack by hash prefix or name |
| `-b, --branch <name>` | Target a specific branch (implies stack) |
| `--dry-run` | Print the composed prompt and exit without launching |
| `--save-prompt <file>` | Write the composed prompt to a file (pairs with `--dry-run`) |
| `--no-push` | Set `EZS_AGENT_NO_PUSH=1` in the spawned agent's env; downstream `ezs push` / auto-push steps that honor it will be skipped |
| `--preset <name>` | Append `~/.ezstack/agent-presets/<name>.md` to the end of the composed prompt |
| `--examples` | Print example invocations and exit |

**`EZS_AGENT_NO_PUSH`** — exported into the child agent process whenever `--no-push` is passed. Hooks and any push-adjacent tooling running under the agent can check this variable to short-circuit pushes; it is never set for normal (non-agent) `ezs` invocations.

The agent is launched in the branch's worktree directory with a prompt containing:
- Current stack structure (branches, parents, worktree paths)
- Current branch and parent info
- Available ezs commands with `-y` flag for non-interactive use

### Feature Builder — Agent creates stacked branches

```bash
# Agent plans and implements a feature as incremental stacked branches
ezs agent feature "Add user authentication with JWT tokens"
```

The agent will:
1. Explore the codebase
2. Present a plan of stacked branches for approval
3. Create each branch with `ezs -y new`, implement changes, commit, and push
4. Each branch is one small, reviewable unit of work

### Customizing Agent Prompts

Agent prompts are composed from three layers:

1. **Shipped prompt** — built into ezstack, updated with releases
2. **Custom instructions** — `~/.ezstack/agent-{work,feature}-prompt.md` (personal, all repos)
3. **Repo instructions** — `<repo>/.ezstack/agent-{work,feature}-prompt.md` (per-repo, committable)

Custom and repo instructions are injected into the shipped prompt automatically. To fully override the shipped prompt, add `override: full` as the first line of your custom instructions file.

#### Template Variables

Prompts support the following template variables, replaced at runtime:

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

#### Managing Prompts

All prompt commands require a positional argument: `work` or `feature`.

```bash
# View the shipped work prompt
ezs agent prompt --shipped work

# View your custom work instructions
ezs agent prompt --custom work

# Edit custom work instructions in your $EDITOR
ezs agent prompt --edit work

# Edit repo-specific work instructions
ezs agent prompt --edit --repo work

# Reset custom work instructions
ezs agent prompt --reset work

# Reset repo-specific feature instructions
ezs agent prompt --reset --repo feature
```

In the VS Code extension, right-click a stack and select **Edit Agent Prompt** to open the prompt file directly in the editor.

### Configuration

```bash
# Set the agent CLI (default: claude)
ezs config set agent_command claude
```

## Architecture Notes

- **Config location:** `~/.ezstack/config.json` (global), `~/.ezstack/stacks.json` (stack state + branch cache)
- **PR data:** PR numbers are derived from PR URLs at runtime, not stored separately. The URL is the source of truth.
- **Worktrees:** Optional for core commands, controlled by `use_worktrees` config. When disabled, branches use `git checkout`. **Required for `ezs agent`** — the agent needs separate working directories per branch
- **Shell integration:** `eval "$(ezs --shell-init)"` enables cd support. Without it, commands print paths instead
- **GitHub integration:** Requires `gh` CLI authenticated via `gh auth login`
- **Stack identity:** Each stack has a unique hash. Use 3+ character prefixes to reference stacks by hash
