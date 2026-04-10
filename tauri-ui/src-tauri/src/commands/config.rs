use crate::runner::{run_ezs_auto, get_home_dir};
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

/// Returns all repos configured in ~/.ezstack/config.json
#[tauri::command]
pub fn get_ezstack_repos() -> Result<Vec<RepoConfig>, String> {
    let path = config_path();
    let contents = fs::read_to_string(&path)
        .map_err(|e| format!("Could not read {}: {}", path.display(), e))?;
    let config: EzstackConfig = serde_json::from_str(&contents)
        .map_err(|e| format!("Could not parse config: {e}"))?;
    let mut repos: Vec<RepoConfig> = config.repos.into_values().collect();
    repos.sort_by(|a, b| a.repo_path.cmp(&b.repo_path));
    Ok(repos)
}

#[tauri::command]
pub fn get_config(state: State<'_, ConnectionState>, repo_path: String) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    run_ezs_auto(conn.as_ref(), &repo_path, &["config", "show"])
}

#[tauri::command]
pub fn set_config(state: State<'_, ConnectionState>, repo_path: String, key: String, value: String) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    run_ezs_auto(conn.as_ref(), &repo_path, &["config", "set", &key, &value])
}
