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

## Architecture Notes

- **Config location:** `~/.ezstack/config.json` (global), `~/.ezstack/stacks.json` (stack state)
- **Worktrees:** Optional, controlled by `use_worktrees` config. When disabled, branches use `git checkout`
- **Shell integration:** `eval "$(ezs --shell-init)"` enables cd support. Without it, commands print paths instead
- **GitHub integration:** Requires `gh` CLI authenticated via `gh auth login`
- **Stack identity:** Each stack has a unique hash. Use 3+ character prefixes to reference stacks by hash
