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
