use serde::{Deserialize, Serialize};
use std::collections::HashMap;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Branch {
    pub name: String,
    pub parent: String,
    pub is_merged: bool,
    pub is_current: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub pr_number: Option<i32>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub pr_url: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub worktree_path: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StatusBranch {
    #[serde(flatten)]
    pub branch: Branch,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub pr_state: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ci_state: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ci_summary: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub mergeable: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub review_state: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub additions: Option<i32>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub deletions: Option<i32>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Stack {
    pub hash: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    pub root: String,
    pub branches: Vec<Branch>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StatusStack {
    pub hash: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    pub root: String,
    pub branches: Vec<StatusBranch>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CommandResult {
    pub stdout: String,
    pub stderr: String,
    pub exit_code: i32,
}

// Mirrors ~/.ezstack/config.json
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EzstackConfig {
    #[serde(default)]
    pub default_base_branch: String,
    #[serde(default)]
    pub repos: HashMap<String, RepoConfig>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RepoConfig {
    pub repo_path: String,
    #[serde(default)]
    pub worktree_base_dir: String,
    #[serde(default)]
    pub default_base_branch: Option<String>,
    #[serde(default)]
    pub sync_strategy: Option<String>,
}

/// Live SSH connection state for the currently-active remote.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SshConnection {
    pub host: String,
    pub user: String,
    pub port: u16,
    /// Path to SSH private key file. If empty, uses default SSH keys.
    #[serde(default)]
    pub key_path: String,
    /// Optional bastion / jump host in `[user@]host[:port]` form.
    #[serde(default)]
    pub jump_host: String,
    /// Repository path on the remote machine.
    #[serde(default)]
    pub remote_repo_path: String,
    /// Optional human-readable label (matches a saved profile name when one exists).
    #[serde(default)]
    pub label: String,
}

/// Persisted connection profile (saved to disk so users don't re-enter creds).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ConnectionProfile {
    /// Stable ID (uuid-ish; we use ts+host).
    pub id: String,
    /// User-facing name (defaults to user@host).
    pub name: String,
    pub host: String,
    pub user: String,
    #[serde(default = "default_ssh_port")]
    pub port: u16,
    #[serde(default)]
    pub key_path: String,
    #[serde(default)]
    pub jump_host: String,
    /// Optional last-selected repo path on this remote.
    #[serde(default)]
    pub last_repo_path: String,
}

fn default_ssh_port() -> u16 {
    22
}

/// Repo summary returned from remote discovery.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RemoteRepoSummary {
    pub repo_path: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub worktree_base_dir: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub default_base_branch: Option<String>,
    /// True if the path actually resolves on the remote (a quick stat check).
    pub exists: bool,
}

/// Result of a single diagnostic step.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DiagnosticStep {
    pub name: String,
    pub status: String, // "ok" | "warn" | "fail" | "skip"
    pub message: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub duration_ms: Option<u64>,
}

/// Lightweight ping result for the active connection.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ConnectionHealth {
    pub ok: bool,
    pub latency_ms: u64,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub message: Option<String>,
}

/// One host public key fingerprint as produced by `ssh-keygen -l`.
/// Used by the frontend to display fingerprints for manual verification
/// before trusting a remote host on first connect.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HostFingerprint {
    pub key_type: String,
    pub bits: u32,
    pub fingerprint: String,
}
