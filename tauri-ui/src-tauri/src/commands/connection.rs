use crate::runner::run_ssh_command;
use crate::types::SshConnection;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::Mutex;
use tauri::State;

pub struct ConnectionState(pub Mutex<Option<SshConnection>>);

#[derive(Debug, Clone, Serialize, Deserialize)]
struct EzstackRepoConfig {
    repo_path: String,
    #[serde(default)]
    worktree_base_dir: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct EzstackConfig {
    #[serde(default)]
    repos: HashMap<String, EzstackRepoConfig>,
}

/// Connect to remote, discover repos from ~/.ezstack/config.json, and return repo paths.
#[tauri::command]
pub fn connect_remote(
    host: String,
    user: String,
    port: u16,
    key_path: String,
) -> Result<Vec<String>, String> {
    let conn = SshConnection {
        host,
        user,
        port,
        key_path,
        remote_repo_path: String::new(),
    };

    // Test SSH connectivity
    let test = run_ssh_command(&conn, "echo ok")?;
    if test.exit_code != 0 {
        return Err(format!(
            "SSH connection failed: {}",
            test.stderr.trim()
        ));
    }

    // Verify ezs is available
    let ezs_check = run_ssh_command(&conn, "command -v ezs")?;
    if ezs_check.exit_code != 0 {
        return Err("ezs is not installed or not on PATH on the remote machine".to_string());
    }

    // Read ~/.ezstack/config.json to discover repos
    let config_read = run_ssh_command(&conn, "cat ~/.ezstack/config.json 2>/dev/null")?;
    if config_read.exit_code != 0 || config_read.stdout.trim().is_empty() {
        return Err("No ezstack configuration found on remote (~/.ezstack/config.json)".to_string());
    }

    let config: EzstackConfig = serde_json::from_str(config_read.stdout.trim())
        .map_err(|e| format!("Failed to parse remote ezstack config: {}", e))?;

    if config.repos.is_empty() {
        return Err("No repositories configured in remote ~/.ezstack/config.json".to_string());
    }

    let repo_paths: Vec<String> = config.repos.keys().cloned().collect();
    Ok(repo_paths)
}

/// Select a repo after connecting and store the connection state.
#[tauri::command]
pub fn select_remote_repo(
    state: State<'_, ConnectionState>,
    host: String,
    user: String,
    port: u16,
    key_path: String,
    remote_repo_path: String,
) -> Result<String, String> {
    let conn = SshConnection {
        host,
        user,
        port,
        key_path,
        remote_repo_path: remote_repo_path.clone(),
    };

    // Verify the repo path exists and is a git repo
    let repo_check = run_ssh_command(
        &conn,
        &format!(
            "cd '{}' && git rev-parse --show-toplevel",
            conn.remote_repo_path.replace('\'', "'\\''")
        ),
    )?;
    if repo_check.exit_code != 0 {
        return Err(format!(
            "Not a git repository on remote: {}",
            repo_check.stderr.trim()
        ));
    }

    let resolved_path = repo_check.stdout.trim().to_string();

    // Store the connection with the resolved repo path
    let mut state = state.0.lock().map_err(|e| e.to_string())?;
    *state = Some(SshConnection {
        remote_repo_path: resolved_path.clone(),
        ..conn
    });

    Ok(resolved_path)
}

#[tauri::command]
pub fn disconnect_remote(state: State<'_, ConnectionState>) -> Result<(), String> {
    let mut state = state.0.lock().map_err(|e| e.to_string())?;
    *state = None;
    Ok(())
}

#[tauri::command]
pub fn get_connection(state: State<'_, ConnectionState>) -> Result<Option<SshConnection>, String> {
    let state = state.0.lock().map_err(|e| e.to_string())?;
    Ok(state.clone())
}

#[tauri::command]
pub fn test_ssh_connection(
    host: String,
    user: String,
    port: u16,
    key_path: String,
) -> Result<String, String> {
    let conn = SshConnection {
        host,
        user,
        port,
        key_path,
        remote_repo_path: String::new(),
    };

    let test = run_ssh_command(&conn, "echo ok")?;
    if test.exit_code != 0 {
        return Err(format!(
            "SSH connection failed: {}",
            test.stderr.trim()
        ));
    }

    Ok("Connection successful".to_string())
}
