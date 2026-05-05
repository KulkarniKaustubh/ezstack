import { invoke } from "@tauri-apps/api/core";
import type {
  StatusStack,
  CommandResult,
  RepoConfig,
  SshConnection,
  ConnectionProfile,
  RemoteRepoSummary,
  DiagnosticStep,
  ConnectionHealth,
  HostFingerprint,
} from "../types/ezstack";

export type {
  CommandResult,
  SshConnection,
  ConnectionProfile,
  RemoteRepoSummary,
  DiagnosticStep,
  ConnectionHealth,
  HostFingerprint,
};

export async function getEzstackRepos(): Promise<RepoConfig[]> {
  return invoke<RepoConfig[]>("get_ezstack_repos");
}

export async function getStacksStatus(repoPath: string): Promise<StatusStack[]> {
  return invoke<StatusStack[]>("get_stacks_status", { repoPath });
}

export async function getRepoPath(startPath: string): Promise<string> {
  return invoke<string>("get_repo_path", { startPath });
}

export async function getCurrentBranch(repoPath: string): Promise<string> {
  return invoke<string>("get_current_branch", { repoPath });
}

export interface ReflogEntry {
  hash: string;
  relative: string;
  action: string;
  message: string;
}

export async function getBranchReflog(
  repoPath: string,
  branch: string,
  limit: number = 20,
): Promise<ReflogEntry[]> {
  return invoke<ReflogEntry[]>("get_branch_reflog", { repoPath, branch, limit });
}

export async function createBranch(
  repoPath: string,
  name: string,
  parent?: string,
): Promise<CommandResult> {
  return invoke<CommandResult>("create_branch", { repoPath, name, parent });
}

export async function syncBranch(
  repoPath: string,
  scope: "current" | "stack" | "all",
  opts?: { stats?: boolean; squash?: boolean },
): Promise<CommandResult> {
  return invoke<CommandResult>("sync_branch", {
    repoPath,
    scope,
    stats: opts?.stats ?? false,
    squash: opts?.squash ?? false,
  });
}

export async function pushBranch(
  repoPath: string,
  stack: boolean = false,
  force: boolean = false,
  opts?: { verify?: boolean; allRemotes?: boolean },
): Promise<CommandResult> {
  return invoke<CommandResult>("push_branch", {
    repoPath,
    stack,
    force,
    verify: opts?.verify ?? false,
    allRemotes: opts?.allRemotes ?? false,
  });
}

export async function deleteBranch(
  repoPath: string,
  branch: string,
  force: boolean = false,
  opts?: { cascade?: boolean },
): Promise<CommandResult> {
  return invoke<CommandResult>("delete_branch", {
    repoPath,
    branch,
    force,
    cascade: opts?.cascade ?? false,
  });
}

export async function reparentBranch(
  repoPath: string,
  branch: string,
  newParent: string,
): Promise<CommandResult> {
  return invoke<CommandResult>("reparent_branch", { repoPath, branch, newParent });
}

export async function renameStack(
  repoPath: string,
  stackHash: string,
  name: string,
): Promise<CommandResult> {
  return invoke<CommandResult>("rename_stack", { repoPath, stackHash, name });
}

export async function prCreate(
  repoPath: string,
  title: string,
  body?: string,
  draft: boolean = false,
  branch?: string,
): Promise<CommandResult> {
  return invoke<CommandResult>("pr_create", { repoPath, title, body, draft, branch });
}

export async function prUpdate(
  repoPath: string,
  branch?: string,
): Promise<CommandResult> {
  return invoke<CommandResult>("pr_update", { repoPath, branch });
}

export async function prMerge(
  repoPath: string,
  method: "squash" | "merge" | "rebase",
  branch?: string,
): Promise<CommandResult> {
  return invoke<CommandResult>("pr_merge", { repoPath, method, branch });
}

export async function prToggleDraft(
  repoPath: string,
  branch?: string,
): Promise<CommandResult> {
  return invoke<CommandResult>("pr_toggle_draft", { repoPath, branch });
}

export async function prUpdateStack(repoPath: string): Promise<CommandResult> {
  return invoke<CommandResult>("pr_update_stack", { repoPath });
}

export async function getConfig(repoPath: string): Promise<CommandResult> {
  return invoke<CommandResult>("get_config", { repoPath });
}

export async function setConfig(
  repoPath: string,
  key: string,
  value: string,
): Promise<CommandResult> {
  return invoke<CommandResult>("set_config", { repoPath, key, value });
}

export async function openAgent(
  repoPath: string,
  stackHash?: string,
  branch?: string,
  opts?: { noPush?: boolean; preset?: string },
): Promise<void> {
  return invoke<void>("open_agent", {
    repoPath,
    stackHash,
    branch,
    noPush: opts?.noPush ?? false,
    preset: opts?.preset ?? null,
  });
}

export async function openAgentFeature(
  repoPath: string,
  stackHash: string,
  description: string,
): Promise<void> {
  return invoke<void>("open_agent_feature", { repoPath, stackHash, description });
}

export async function getAgentPrompts(repoPath: string): Promise<string> {
  return invoke<string>("get_agent_prompts", { repoPath });
}

export async function getAgentPromptLayer(
  repoPath: string,
  layer: "shipped" | "custom" | "repo",
  promptType: "work" | "feature",
): Promise<string> {
  return invoke<string>("get_agent_prompt_layer", { repoPath, layer, promptType });
}

export async function resetAgentPrompts(
  repoPath: string,
  which: "work" | "feature" | "both",
  repo: boolean = false,
): Promise<string> {
  return invoke<string>("reset_agent_prompts", { repoPath, which, repo });
}

export async function editAgentPrompts(
  repoPath: string,
  which: "work" | "feature",
  repo: boolean = false,
): Promise<void> {
  return invoke<void>("edit_agent_prompts", { repoPath, which, repo });
}

// ─── CLI bundle additions (doctor, draft-all, search, config ex/import) ──────
// These bindings forward to Rust commands that already wrap the corresponding
// CLI features end-to-end, but no React component calls them yet — the UI
// integration (settings dialog, search modal, diagnostics panel) is intentional
// future work. Don't remove the bindings; doing so would also strand the Rust
// side, and the next UI change set will pick them up.

/** Run `ezs doctor` and return the diagnostic report. */
export async function doctor(repoPath: string): Promise<CommandResult> {
  return invoke<CommandResult>("doctor", { repoPath });
}

/** Create draft PRs for every branch in the current stack without one. */
export async function prDraftAll(repoPath: string): Promise<CommandResult> {
  return invoke<CommandResult>("pr_draft_all", { repoPath });
}

/** Run `ezs goto --search <query>`. Returns the CLI result; the frontend
 *  parses the cd-line on success or shows the error on a no-match. */
export async function gotoSearch(
  repoPath: string,
  query: string,
): Promise<CommandResult> {
  return invoke<CommandResult>("goto_search", { repoPath, query });
}

/** Export the global ezstack config (token redacted) to the given path. */
export async function configExport(
  repoPath: string,
  filePath: string,
): Promise<CommandResult> {
  return invoke<CommandResult>("config_export", { repoPath, filePath });
}

/** Import a previously-exported config, replacing the current one. */
export async function configImport(
  repoPath: string,
  filePath: string,
): Promise<CommandResult> {
  return invoke<CommandResult>("config_import", { repoPath, filePath });
}

// ─── CLI parity additions (commit/amend/diff/log/stack/unstack/pr_refresh) ──
// Same orphan-binding shape as the bundle additions above: the Rust handlers
// in src-tauri wrap each CLI feature end-to-end, but the React UI doesn't
// call them yet (a commit/amend dialog, a diffstat / log panel, an
// add-to-stack flow, and a "refresh PR cache" button are the intended
// integration points). Keeping the bindings live so the next UI change set
// can pick them up without round-tripping through Rust again.

/** Strategy override for {@link commit} / {@link amend}. `default` keeps
 *  the configured `sync_strategy`. */
export type SyncStrategy = "default" | "merge" | "rebase";

/**
 * Commit and auto-sync child branches. The Tauri layer always passes
 * `--no-push` because the desktop surface has no place to answer the
 * CLI's interactive push prompt; pushes are explicit user actions via
 * {@link pushBranch}. `all=true` maps to `git commit -a`.
 */
export async function commit(
  repoPath: string,
  message: string,
  opts?: { all?: boolean; strategy?: SyncStrategy },
): Promise<CommandResult> {
  return invoke<CommandResult>("commit", {
    repoPath,
    message,
    all: opts?.all ?? false,
    strategy: opts?.strategy ?? "default",
  });
}

/**
 * Amend the last commit and auto-sync children.
 *
 * When `message` is empty/undefined the existing commit message is
 * preserved (`--no-edit`). Like {@link commit}, push is suppressed —
 * amend rewrites history, so the intended follow-up is
 * `pushBranch(repoPath, false, true)` with `force=true`.
 */
export async function amend(
  repoPath: string,
  opts?: { message?: string; all?: boolean; strategy?: SyncStrategy },
): Promise<CommandResult> {
  return invoke<CommandResult>("amend", {
    repoPath,
    message: opts?.message ?? null,
    all: opts?.all ?? false,
    strategy: opts?.strategy ?? "default",
  });
}

/** Display mode for {@link diffBranch}. JSON returns the file-level numstat
 *  (parse stdout with `JSON.parse`); stat returns `git diff --stat` text. */
export type DiffMode = "json" | "stat";

/**
 * Diff a branch against its parent in the stack. Returns the raw
 * `CommandResult` so the caller can decide between JSON and text
 * rendering. When `branch` is omitted, the CLI defaults to the
 * current branch.
 */
export async function diffBranch(
  repoPath: string,
  opts?: { branch?: string; mode?: DiffMode },
): Promise<CommandResult> {
  return invoke<CommandResult>("diff_branch", {
    repoPath,
    branch: opts?.branch ?? null,
    mode: opts?.mode ?? "json",
  });
}

/** Show commits in `branch` since its parent. Always returns JSON
 *  (`logOutputJSON` shape) on stdout. */
export async function logBranch(
  repoPath: string,
  branch?: string,
): Promise<CommandResult> {
  return invoke<CommandResult>("log_branch", {
    repoPath,
    branch: branch ?? null,
  });
}

/**
 * Add a branch to a stack. `parent` and `base` are mutually exclusive:
 * pass `parent` to attach under an existing tracked branch, `base` to
 * start a new stack rooted on a non-default base (e.g. `develop`).
 * `base` wins when both are passed, mirroring the precedence in the
 * VS Code wrapper and the MCP `ezstack_stack` tool.
 */
export async function stackBranch(
  repoPath: string,
  branch: string,
  target: { parent?: string; base?: string },
): Promise<CommandResult> {
  return invoke<CommandResult>("stack_branch", {
    repoPath,
    branch,
    parent: target.parent ?? null,
    base: target.base ?? null,
  });
}

/** Remove a branch from ezstack tracking. Children are reparented to
 *  the untracked branch's parent; the git branch and worktree are
 *  kept. Caller should confirm with the user first. */
export async function unstackBranch(
  repoPath: string,
  branch: string,
): Promise<CommandResult> {
  return invoke<CommandResult>("unstack_branch", { repoPath, branch });
}

/**
 * Reconcile the local PR cache from GitHub. `stack=true` refreshes
 * every PR in the current stack; otherwise pass `branch` (or omit
 * both for the current branch). `stack` and `branch` are mutually
 * exclusive — `stack` wins when both are supplied.
 */
export async function prRefresh(
  repoPath: string,
  opts?: { branch?: string; stack?: boolean },
): Promise<CommandResult> {
  return invoke<CommandResult>("pr_refresh", {
    repoPath,
    branch: opts?.branch ?? null,
    stack: opts?.stack ?? false,
  });
}

// ─── Remote connection commands ──────────────────────────────────────────

export async function connectRemote(
  host: string,
  user: string,
  port: number,
  keyPath: string,
  jumpHost: string = "",
): Promise<RemoteRepoSummary[]> {
  return invoke<RemoteRepoSummary[]>("connect_remote", {
    host,
    user,
    port,
    keyPath,
    jumpHost: jumpHost || null,
  });
}

export async function listRemoteRepos(): Promise<RemoteRepoSummary[]> {
  return invoke<RemoteRepoSummary[]>("list_remote_repos");
}

export async function selectRemoteRepo(
  host: string,
  user: string,
  port: number,
  keyPath: string,
  jumpHost: string,
  remoteRepoPath: string,
  label: string = "",
): Promise<string> {
  return invoke<string>("select_remote_repo", {
    host,
    user,
    port,
    keyPath,
    jumpHost: jumpHost || null,
    remoteRepoPath,
    label: label || null,
  });
}

export async function disconnectRemote(): Promise<void> {
  return invoke<void>("disconnect_remote");
}

export async function getConnection(): Promise<SshConnection | null> {
  return invoke<SshConnection | null>("get_connection");
}

export async function testSshConnection(
  host: string,
  user: string,
  port: number,
  keyPath: string,
  jumpHost: string = "",
): Promise<string> {
  return invoke<string>("test_ssh_connection", {
    host,
    user,
    port,
    keyPath,
    jumpHost: jumpHost || null,
  });
}

export async function pingConnection(): Promise<ConnectionHealth> {
  return invoke<ConnectionHealth>("ping_connection");
}

export async function getHostFingerprint(
  host: string,
  port: number,
): Promise<HostFingerprint[]> {
  return invoke<HostFingerprint[]>("get_host_fingerprint", { host, port });
}

export async function diagnoseConnection(
  host: string,
  user: string,
  port: number,
  keyPath: string,
  jumpHost: string = "",
): Promise<DiagnosticStep[]> {
  return invoke<DiagnosticStep[]>("diagnose_connection", {
    host,
    user,
    port,
    keyPath,
    jumpHost: jumpHost || null,
  });
}

// ─── Saved profiles ──────────────────────────────────────────────────────

export async function listConnectionProfiles(): Promise<ConnectionProfile[]> {
  return invoke<ConnectionProfile[]>("list_connection_profiles");
}

export async function saveConnectionProfile(
  profile: ConnectionProfile,
): Promise<ConnectionProfile> {
  return invoke<ConnectionProfile>("save_connection_profile", { profile });
}

export async function deleteConnectionProfile(id: string): Promise<void> {
  return invoke<void>("delete_connection_profile", { id });
}

export async function updateProfileLastRepo(
  id: string,
  repoPath: string,
): Promise<void> {
  return invoke<void>("update_profile_last_repo", { id, repoPath });
}
