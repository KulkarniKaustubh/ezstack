import { invoke } from "@tauri-apps/api/core";
import type { StatusStack, CommandResult, RepoConfig, SshConnection } from "../types/ezstack";

export type { CommandResult, SshConnection };

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
  return invoke<CommandResult>("reparent_branch", { repoPath, branch, new_parent: newParent });
}

export async function renameStack(
  repoPath: string,
  stackHash: string,
  name: string,
): Promise<CommandResult> {
  return invoke<CommandResult>("rename_stack", { repoPath, stack_hash: stackHash, name });
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
  return invoke<string>("get_agent_prompt_layer", { repoPath, layer, prompt_type: promptType });
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

// Remote connection commands

export async function connectRemote(
  host: string,
  user: string,
  port: number,
  keyPath: string,
): Promise<string[]> {
  return invoke<string[]>("connect_remote", {
    host,
    user,
    port,
    keyPath,
  });
}

export async function selectRemoteRepo(
  host: string,
  user: string,
  port: number,
  keyPath: string,
  remoteRepoPath: string,
): Promise<string> {
  return invoke<string>("select_remote_repo", {
    host,
    user,
    port,
    keyPath,
    remoteRepoPath,
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
): Promise<string> {
  return invoke<string>("test_ssh_connection", { host, user, port, keyPath });
}
