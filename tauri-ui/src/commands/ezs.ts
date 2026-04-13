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
): Promise<CommandResult> {
  return invoke<CommandResult>("sync_branch", { repoPath, scope });
}

export async function pushBranch(
  repoPath: string,
  stack: boolean = false,
  force: boolean = false,
): Promise<CommandResult> {
  return invoke<CommandResult>("push_branch", { repoPath, stack, force });
}

export async function deleteBranch(
  repoPath: string,
  branch: string,
  force: boolean = false,
): Promise<CommandResult> {
  return invoke<CommandResult>("delete_branch", { repoPath, branch, force });
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
): Promise<void> {
  return invoke<void>("open_agent", { repoPath, stackHash, branch });
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
