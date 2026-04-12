use crate::runner::{get_home_dir, run_ezs_auto, run_ssh_command};
use crate::types::{CommandResult, EzstackConfig, RepoConfig};
use super::connection::ConnectionState;
use std::env;
use std::fs;
use std::path::PathBuf;
use tauri::State;

fn config_path() -> PathBuf {
    if let Ok(home) = env::var("EZSTACK_HOME") {
        PathBuf::from(home).join("config.json")
    } else if let Some(home) = get_home_dir() {
        home.join(".ezstack").join("config.json")
    } else if let Ok(home) = env::var("HOME") {
        PathBuf::from(home).join(".ezstack").join("config.json")
    } else {
        PathBuf::from(".ezstack").join("config.json")
    }
}

fn parse_repos(raw: &str) -> Result<Vec<RepoConfig>, String> {
    let config: EzstackConfig =
        serde_json::from_str(raw).map_err(|e| format!("Could not parse config: {e}"))?;
    let mut repos: Vec<RepoConfig> = config.repos.into_values().collect();
    repos.sort_by(|a, b| a.repo_path.cmp(&b.repo_path));
    Ok(repos)
}

/// Returns all repos configured in ezstack's config.
///
/// When a remote SSH connection is active, reads `~/.ezstack/config.json`
/// from the remote so the desktop UI can populate its repo list automatically.
#[tauri::command]
pub fn get_ezstack_repos(state: State<'_, ConnectionState>) -> Result<Vec<RepoConfig>, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    if let Some(conn) = conn {
        let res = run_ssh_command(&conn, "cat ~/.ezstack/config.json 2>/dev/null")?;
        if res.exit_code != 0 || res.stdout.trim().is_empty() {
            return Err(
                "No ezstack config found on remote (~/.ezstack/config.json missing)".to_string(),
            );
        }
        return parse_repos(res.stdout.trim());
    }

    let path = config_path();
    let contents = fs::read_to_string(&path)
        .map_err(|e| format!("Could not read {}: {}", path.display(), e))?;
    parse_repos(&contents)
}

#[tauri::command]
pub fn get_config(state: State<'_, ConnectionState>, repo_path: String) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    run_ezs_auto(conn.as_ref(), &repo_path, &["config", "show"])
}

#[tauri::command]
pub fn set_config(
    state: State<'_, ConnectionState>,
    repo_path: String,
    key: String,
    value: String,
) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    run_ezs_auto(conn.as_ref(), &repo_path, &["config", "set", &key, &value])
}
