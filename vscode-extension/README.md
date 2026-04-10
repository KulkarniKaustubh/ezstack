<div align="center">

<img src="resources/icon.png" alt="ezstack logo" width="80">

# ezstack VS Code Extension

Manage stacked pull requests from the VS Code sidebar, powered by the `ezs` CLI.

</div>

## Prerequisites

- [ezstack CLI](https://github.com/KulkarniKaustubh/ezstack) (`ezs`) installed and on your PATH
- Git 2.20+
- [GitHub CLI](https://cli.github.com/) (`gh`) authenticated — required for PR and CI status features

## Installation

### From Pre-built VSIX (Recommended)

Download the `.vsix` file from the [latest release](https://github.com/KulkarniKaustubh/ezstack/releases) and install it:

```bash
code --install-extension ezstack-2.1.1.vsix
```

Or in VSCode: **Extensions** sidebar → `...` menu → **Install from VSIX...** → select the file.

### From Source

```bash
cd vscode-extension
npm install
npm run compile
npx vsce package        # produces ezstack-2.1.1.vsix
code --install-extension ezstack-2.1.1.vsix
```

### Development Mode

```bash
cd vscode-extension
npm install
npm run compile
```

Then open this folder in VSCode and press **F5** to launch the Extension Development Host.

## Features

### Stack Tree View

The extension adds an **ezstack** panel in the activity bar. It displays your stacks as a tree:

```
Stack: my-feature [a1b2c]
  ├── feature-part1  PR #42 [OPEN] CI: 3/3 passed  APPROVED
  │   └── feature-part2  PR #43 [DRAFT] CI: pending
  │       └── feature-part3  (no PR)
```

Each branch shows:
- PR number and state (OPEN, DRAFT, MERGED, CLOSED)
- CI status and summary
- Review state (APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED)
- Current branch is highlighted with a green icon

The tree auto-refreshes when `~/.ezstack/stacks.json` changes or when you switch branches.

### Status Bar

The status bar shows your current branch and stack name. Click it to quickly navigate to another branch.

### Commands

All commands are available via the Command Palette (`Cmd+Shift+P` / `Ctrl+Shift+P`) under the **ezstack** category:

| Command | Description |
|---------|-------------|
| **ezstack: New Branch** | Create a new stacked branch (prompts for name and parent) |
| **ezstack: Sync** | Sync branches — runs in terminal for interactive conflict resolution |
| **ezstack: Push Branch** | Push the current branch |
| **ezstack: Push Stack** | Push all branches in the current stack |
| **ezstack: Create PR** | Create a GitHub PR (prompts for title and draft/ready) |
| **ezstack: Update PR** | Update the current branch's PR base |
| **ezstack: Merge PR** | Merge the current PR (prompts for squash/merge/rebase) |
| **ezstack: Toggle PR Draft** | Toggle draft status on the current PR |
| **ezstack: Update Stack Info in PRs** | Update the stack navigation table in all PR descriptions |
| **ezstack: Go to Branch** | Navigate to a branch's worktree folder |
| **ezstack: Go to Parent Branch** | Navigate up the stack |
| **ezstack: Go to Child Branch** | Navigate down the stack |
| **ezstack: Delete Branch** | Delete a branch and its worktree |
| **ezstack: Reparent Branch** | Move a branch to a different parent |
| **ezstack: Refresh** | Manually refresh the stack tree view |

### Context Menus

Right-click branches in the tree view for quick actions:
- Open worktree folder
- Open PR in browser
- Push, Create/Update PR, Merge PR
- Toggle draft, Reparent, Delete

## Configuration

| Setting | Default | Description |
|---------|---------|-------------|
| `ezstack.cliPath` | `"ezs"` | Path to the `ezs` CLI binary |
| `ezstack.autoRefresh` | `true` | Auto-refresh tree view when config files change |

## How It Works

The extension delegates all operations to the `ezs` CLI:

- **Read operations** use `ezs list --json` and `ezs status --json` for structured data
- **Mutations** use the `-y` flag to skip interactive confirmations
- **Sync** runs in the integrated terminal since it may require interactive conflict resolution
- **Navigation** opens worktree folders directly in VSCode

The tree view watches `~/.ezstack/stacks.json` for changes, so it stays in sync regardless of whether you use the extension or the CLI directly.

## Development

```bash
# Install dependencies
npm install

# Compile (one-time)
npm run compile

# Watch mode (recompiles on save)
npm run watch

# Launch Extension Development Host
# Press F5 in VSCode, or:
code --extensionDevelopmentPath=$(pwd)
```

### Project Structure

```
src/
  extension.ts              Entry point (activate/deactivate)
  ezsCli.ts                 CLI wrapper (spawn ezs, parse JSON)
  configWatcher.ts          FileSystemWatcher for auto-refresh
  types.ts                  TypeScript interfaces matching Go JSON structs
  views/
    stackTreeProvider.ts     TreeDataProvider for the stack tree
    statusBarManager.ts      Status bar integration
  commands/
    index.ts                 All command implementations
```
