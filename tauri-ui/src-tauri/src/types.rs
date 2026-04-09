use serde::{Deserialize, Serialize};

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

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SshConnection {
    pub host: String,
    pub user: String,
    pub port: u16,
    /// Path to SSH private key file. If empty, uses default SSH keys.
    #[serde(default)]
    pub key_path: String,
    /// Repository path on the remote machine.
    pub remote_repo_path: String,
}
