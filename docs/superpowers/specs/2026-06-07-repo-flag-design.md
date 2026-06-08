# `--repo` flag + `EZSTACK_REPO` env var across the ezstack ecosystem

**Date:** 2026-06-07
**Status:** Approved (design)

## Problem

`ezs` infers which repository to operate on from the current working directory
(`os.Getwd()` → `checkRepoRoot()`). To run it against a particular repo the user
must `cd` into that repo (or one of its worktrees) first. Users want to point
ezstack at a repository they "always" work on, without changing directories —
both as a one-off override and as a persistent default — and they want this to
work consistently across the whole ecosystem (CLI, MCP server, VS Code
extension, Neovim plugin, Tauri desktop UI).

The MCP server already has a precedent: a `--repo` flag that does
`os.Chdir(repo)` before serving. The CLI and the GUI/editor clients do not.

## Goals

- Add a `--repo <path>` flag to the `ezs` CLI for a one-off repo override.
- Add an `EZSTACK_REPO` env var for the persistent "always run on this repo" case.
- Align the MCP server's existing `--repo` flag with the same env var.
- Expose a default-repo setting in the VS Code extension, Neovim plugin, and
  Tauri desktop UI, each of which injects `--repo` into its ezs invocations.

## Non-goals

- Pointing `--repo` at an arbitrary worktree/subdir and resolving that
  worktree's branch context. `--repo` resolves to the repo root only (see
  Semantics). "Run on branch X" is out of scope.
- Remembering the last-active branch/worktree per repo. No new per-repo state.
- A persistent `default_repo` field in ezstack's global config. Persistence is
  provided by the env var and by each client's own settings, not by ezstack
  config. (Keeps config surface and migration burden flat, and avoids a
  "which repo am I really on" ambiguity baked into shared config.)

## Decisions

### Naming

- Flag: `--repo` (matches the existing MCP flag).
- Env var: `EZSTACK_REPO` — chosen over `EZS_REPO` to match the existing
  `EZSTACK_HOME` env var convention (`internal/config/config.go`).

### Precedence

For every surface that resolves a repo:

```
--repo <path>  (flag)   >   EZSTACK_REPO  (env)   >   cwd discovery  (today's behavior)
```

### Semantics: chdir-to-root

When a repo is resolved from the flag or env var, ezs resolves it to an absolute
path and `os.Chdir`s there **before** the existing `checkRepoRoot()` runs.
Everything downstream then behaves exactly as if the user had `cd`'d into the
repo root:

- You are on the repo's default branch (whatever the main worktree has checked
  out).
- Stack-context commands (`status`, `sync`, `diff`, `log`, `up`/`down`) that
  infer "the current branch" from cwd will see the root/default branch, so they
  require `--branch` / `--all` / `--stack` just as they already do when run from
  the repo root. This is existing, documented behavior — `--repo` does not
  change it.

This deliberately reuses all existing code paths and mirrors the MCP server.

## Detailed design

### 1. CLI (`cmd/ezs/main.go`)

The CLI is a hand-rolled dispatcher (not cobra). `--repo` is pre-parsed in
`main()` the same way `-y/--yes` already is — position-independent, before
command dispatch — so it composes with every subcommand without touching the
per-command parsers.

- Parse forms: `--repo <path>` (two tokens) and `--repo=<path>` (one token).
- Both `--repo` tokens are removed from the cleaned arg slice before the command
  word and args are extracted, so subcommand parsers never see them (identical
  to the `-y` handling).
- Resolution: if the flag is present use it; else read `EZSTACK_REPO`; else fall
  through to today's cwd behavior.
- When a repo is resolved: `os.Chdir(resolved)`. On failure (path missing, not a
  directory), emit an error that **names the source** — e.g.
  `--repo <path>: <err>` or `EZSTACK_REPO=<path>: <err>` — and exit with the
  existing not-in-repo exit code (`ui.ExitNotInRepo`).
- After a successful chdir, `checkRepoRoot()` runs as today. If the resolved
  path is a directory but not a git repo, the resulting "must be run from a git
  repository root" error should also name the `--repo`/`EZSTACK_REPO` source so
  the misconfiguration is obvious rather than looking like a cwd problem.
- A bare `--repo` with no value, or `--repo` pointing at an empty string, is a
  usage error (`ui.ExitUsage`).

Implementation note: extract a small helper (e.g. `resolveRepoOverride(args)
(remainingArgs []string, repoPath string, source string, err error)`) so the
precedence and parsing are unit-testable in isolation, then `os.Chdir` from
`main` using its result.

`printUsage()` gains a `--repo <path>` entry in the OPTIONS block documenting
the flag and the `EZSTACK_REPO` env var.

### 2. Shell wrapper (`--shell-init`)

The generated `ezs()` shell function decides whether to `eval` a `cd <path>`
line by matching `$1` against the navigation commands
(`goto|go|new|n|delete|del|rm|sync|up|down|menu`). With a **leading** flag —
`ezs --repo /x goto feat` — `$1` is `--repo`, so the `cd` emitted by navigation
would be printed instead of executed, silently breaking the directory change.

Fix: update the generated shell function to skip a leading `--repo <val>` /
`--repo=val` (and a leading `-y`/`--yes`) when determining the command word,
then dispatch on that resolved word. The persistent `EZSTACK_REPO` path needs no
special handling because there the command word is already in `$1`
(`ezs goto feat`).

The `--completions` path should likewise honor `EZSTACK_REPO` (it inherits the
env from the shell) so branch-name completion suggests branches from the
configured repo. This is a nice-to-have that falls out of reading the env in the
same resolver; no extra UX.

### 3. MCP server (`cmd/ezs-mcp/main.go`) — alignment

The server already parses `--repo` (pflag) and `os.Chdir`s to it. Single change:
when the `--repo` flag value is empty, fall back to `os.Getenv("EZSTACK_REPO")`
before deciding whether to chdir. Error handling on chdir failure stays as-is
(already names `--repo`; extend to name `EZSTACK_REPO` when that was the
source).

### 4. VS Code extension

- Add a `ezstack.repo` setting (type `string`, default `""`) to
  `package.json` `contributes.configuration`, documented as "Absolute path to a
  repository to run ezstack against, overriding the open workspace folder."
- In `EzsCli.exec(args)` (`src/ezsCli.ts`), read the setting; when non-empty,
  prepend `["--repo", repoPath]` to `args` for every invocation. Chosen over
  silently overriding `cwd` so the mechanism is explicit and identical across
  surfaces. `cwd` stays `workspaceRoot`; `--repo` overrides it inside ezs.
- The existing `shellQuote` is for display/logging only (execFile passes args
  as an array, so no shell-injection concern from the path).

### 5. Neovim plugin

- Add `repo` to the `defaults` table in `lua/ezstack/init.lua` (default `nil`/
  empty), documented in the README and `doc/`.
- In `lua/ezstack/cli.lua`, where the command is assembled
  (`cmd = { cli_path() }` then `vim.list_extend(cmd, args)`), inject
  `--repo <path>` ahead of `args` in both the async (`run_async`) and any sync
  runner when `config.repo` is set.

### 6. Tauri desktop UI

- `src-tauri/src/runner.rs` runs ezs with `current_dir`. Add support for a
  configured default repo passed through as `--repo` on every invocation, and
  honor `EZSTACK_REPO` from the environment as a fallback.
- The exact UI surface for setting/persisting the default repo will be wired to
  the existing repo-selection flow during implementation; the runner-level
  `--repo` plumbing and env fallback are the contract this spec fixes.

## Testing

- **CLI (`cmd/ezs`)**
  - `resolveRepoOverride`: precedence (flag beats env beats neither), both flag
    forms (`--repo X`, `--repo=X`), token stripping leaves the command + args
    intact, bare `--repo` is a usage error.
  - chdir success changes the effective repo; chdir failure produces an error
    naming the source (`--repo` vs `EZSTACK_REPO`) and the not-in-repo exit code.
  - A resolved-but-non-git path yields the source-named "not a git repo" error.
- **Shell wrapper**: assert the generated function resolves the command word
  past a leading `--repo`/`--repo=`/`-y` (string-level test of the emitted
  script, consistent with how `--shell-init` output is already validated).
- **MCP (`cmd/ezs-mcp`)**: with no `--repo` flag and `EZSTACK_REPO` set, the
  server chdirs to the env path; flag still wins when both are present.
- **VS Code (`src/ezsCli.test.ts`)**: when `ezstack.repo` is set, emitted args
  begin with `--repo <path>`; when unset, args are unchanged.
- **Neovim (`tests/`)**: when `config.repo` is set, the assembled command
  includes `--repo <path>`; when unset, it does not.

## Documentation

- `cmd/ezs/main.go` `printUsage()` OPTIONS block.
- `DOCUMENTATION.md` and top-level `README.md`: a short section on running ezs
  against a fixed repo via `--repo` / `EZSTACK_REPO`.
- VS Code extension README + setting description; Neovim plugin README/`doc`;
  Tauri UI docs.
- **Footgun note** (in CLI/docs): `EZSTACK_REPO` set globally makes *every* `ezs`
  in *every* terminal target that one repo. Recommend setting it per-project
  (e.g. direnv) or in a dedicated shell, not in a global shell rc.
