<div align="center">

# ezstack Desktop

**A desktop GUI for managing stacked PRs — powered by Tauri**

[![Tauri](https://img.shields.io/badge/Tauri-v2-FFC131?style=flat&logo=tauri)](https://v2.tauri.app/)
[![React](https://img.shields.io/badge/React-19-61DAFB?style=flat&logo=react)](https://react.dev/)
[![TypeScript](https://img.shields.io/badge/TypeScript-5.7-3178C6?style=flat&logo=typescript)](https://www.typescriptlang.org/)

</div>

---

ezstack Desktop is a native desktop app that wraps the [`ezs` CLI](../README.md), providing a visual interface for managing stacked pull requests with git worktrees.

## Screenshot

```
┌─────────────────────────────────────────────────────┐
│  ezstack  v2.0.0-beta.2             🌙  ↻  ⚙      │
├──────────┬──────────────────────┬───────────────────┤
│ Stacks   │   main (base)        │  feat/auth        │
│          │    └─ feat/auth       │  PR: #42 Open ↗   │
│ ● myapp  │       └─ feat/login  │  CI: 3/3 passed ✓ │
│   2 br   │                      │  Review: Approved  │
│          │                      │  [Sync] [Push]     │
│          │                      │  [Update PR]       │
├──────────┴──────────────────────┴───────────────────┤
│  ~/code/myapp  │  feat/auth  │  12:34:05            │
└─────────────────────────────────────────────────────┘
```

## Requirements

- [Rust](https://rustup.rs/) toolchain (for building)
- [Node.js](https://nodejs.org/) 18+
- [`ezs` CLI](../README.md#installation) installed and on PATH
- [Git](https://git-scm.com/) 2.20+
- [GitHub CLI](https://cli.github.com/) (`gh`) for PR operations

## Getting Started

### 1. Install dependencies

```bash
cd tauri-ui
npm install
```

### 2. Run in development mode

```bash
npm run tauri dev
```

This starts the Vite dev server (hot reload) and launches the Tauri window.

### 3. Build for production

```bash
npm run tauri build
```

The built app will be in `src-tauri/target/release/bundle/`.

## Architecture

```
tauri-ui/
├── src/                    # React frontend
│   ├── App.tsx             # Main app with three-panel layout
│   ├── commands/ezs.ts    # Typed Tauri invoke wrappers
│   ├── store/app-store.ts # Zustand state management
│   ├── hooks/             # Data fetching, operations, theme
│   ├── components/
│   │   ├── layout/        # TitleBar, Sidebar, StatusBar
│   │   ├── stack/         # StackGraph (tree), StackNode
│   │   ├── branch/        # BranchDetail, status badges, actions
│   │   ├── operations/    # Dialogs: new, sync, delete, PR, reparent
│   │   ├── shared/        # OperationOutput, EmptyState
│   │   └── ui/            # Primitives: button, badge, card, dialog, etc.
│   └── types/ezstack.ts   # TypeScript types matching CLI JSON output
├── src-tauri/              # Rust backend
│   ├── src/
│   │   ├── lib.rs          # Tauri app setup, command registration
│   │   ├── runner.rs       # Spawns ezs/git CLI commands
│   │   ├── types.rs        # Serde structs matching CLI JSON
│   │   └── commands/       # Tauri commands: list, operations, pr, config
│   ├── Cargo.toml
│   └── tauri.conf.json
└── package.json
```

### How it works

The Rust backend is a thin wrapper around the `ezs` CLI:

1. **Queries** — Runs `ezs status --json --all` and parses the JSON output into typed structs
2. **Mutations** — Runs `ezs -y <command>` (the `-y` flag auto-confirms prompts; the desktop app shows its own confirmation dialogs)
3. **Frontend** — React app receives typed data via Tauri's `invoke()` IPC, renders the UI, and sends commands back

The app polls `ezs status` every 30 seconds (pauses when the window loses focus) and refreshes automatically after any operation.

## Features

### Stack Visualization
- Visual tree graph of branch relationships
- Color-coded health indicators per stack
- Current branch highlighting

### Branch Status
- **PR state**: Open (green), Draft (gray), Merged (purple), Closed (red)
- **CI status**: Pass (✓), Fail (✗), Pending (spinner)
- **Review**: Approved, Changes Requested, Review Required
- **Mergeable state**: Mergeable, Conflicting, Unknown

### Operations (via dialogs)
- **Create branch** — with optional parent selection
- **Sync** — current branch, stack, or all stacks
- **Push** — current branch or full stack
- **Delete** — with force option
- **Reparent** — change a branch's parent
- **PR Create** — title, description, draft toggle
- **PR Update** — push and update
- **PR Merge** — squash, merge, or rebase
- **Toggle Draft** — switch between draft and ready
- **Update Stack** — update all PR descriptions with stack info
- **Agent** — launch AI agent (branch-scoped or stack-scoped)
- **Agent Feature** — build a feature with AI agent (description prompt)
- **Agent Prompts** — view/edit/reset prompts across 3 layers (shipped, custom, repo)

### UI/UX
- Dark / light / system theme with toggle
- Keyboard shortcuts: `Cmd+R` (refresh), `Cmd+N` (new branch)
- Operation output panel (terminal-like CLI output display)
- Repo picker (open any git repository)
- Status bar with repo path, current branch, last refresh time
- Live connection pill in title bar with tri-state health (green / yellow / red) and latency
- Polling uses exponential backoff on failure (30s → 60s → 120s → 240s → 300s cap)

## Remote (SSH) Mode

ezstack Desktop can drive an `ezs` install on a remote machine over SSH. The local
app becomes a thin client; every `ezs`, `git`, and `gh` invocation runs on the remote
host. This is useful for working against repos that live on a dev VM, build server, or
beefy workstation without leaving your laptop.

### Requirements (on the remote host)
- `ezs` CLI on the login `PATH` (the diagnostics tab will tell you if it's missing)
- `git` 2.20+ and `gh` (authenticated, for PR operations)
- An entry in `~/.ezstack/config.json` for each repo you want to manage
- OpenSSH server with key-based auth (password prompts won't work — `BatchMode` is on)

### Connecting

1. Click the **Connect** pill in the title bar.
2. Fill in host, user, port, and (optionally) an SSH key path. The key picker uses a
   native file dialog.
3. **Jump host** (optional) — any value here is passed as `ssh -J <jump>`, so the same
   `user@host[:port]` syntax OpenSSH uses works.
4. **Label** (optional) — friendly name shown in the title-bar pill.
5. Click **Connect**. The app runs a quick reachability probe, then lists every repo
   from the remote `~/.ezstack/config.json` along with whether the worktree base dir
   actually exists on disk.
6. Pick a repo. The sidebar and stack views are now backed by the remote.

### Diagnose

The connect dialog has a **Diagnose** button that runs a 6-step health check:

1. SSH connectivity + latency
2. Login shell `PATH`
3. `ezs` binary present and runnable
4. `git` present
5. `gh` authenticated
6. `~/.ezstack/config.json` readable

Each step reports pass / warn / fail with a per-step duration so you can see exactly
where things break.

### Saved profiles

Profiles are saved to `~/.ezstack/desktop/connections.json` (mode `0600`) and contain
host, user, port, key path, jump host, and the last repo you opened on that profile.
No secrets are stored — auth still goes through your SSH agent or key file. Delete a
profile by hovering it in the picker and clicking the trash icon.

Override the profile location with the `EZSTACK_DESKTOP_HOME` environment variable
(useful for tests or portable installs).

### Health checks

Once connected, the app pings the remote every 30 seconds (pauses on window blur).
The title-bar pill turns:

- **green** — healthy, latency ≤ 800 ms
- **yellow** — healthy but slow (> 800 ms)
- **red** — last ping failed (hover for the error)

### Known limitations

- **Agent prompt editor** is local-only — opening `$EDITOR` over SSH from a GUI is
  fragile, so the desktop app blocks `agent prompts edit` while connected. View and
  reset still work.
- **Host key policy** — first connections use `StrictHostKeyChecking=accept-new`, so a
  freshly-trusted host is added to your `known_hosts` automatically. Existing host
  keys are still verified strictly.
- **Latency** — every operation is at least one SSH round-trip. Stacked-PR workflows
  are not chatty, but expect refreshes to feel a beat slower than local mode.

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Framework | [Tauri v2](https://v2.tauri.app/) |
| Frontend | [React 19](https://react.dev/) + [TypeScript 5.7](https://www.typescriptlang.org/) |
| Styling | [Tailwind CSS v4](https://tailwindcss.com/) |
| State | [Zustand 5](https://zustand.docs.pmnd.rs/) |
| Icons | [Lucide React](https://lucide.dev/) |
| Build | [Vite 6](https://vite.dev/) |
| Backend | Rust + `std::process::Command` (shells out to `ezs` CLI) |

## License

[MIT](../LICENSE)
