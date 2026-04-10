use crate::commands::connection::ConnectionState;
use crate::runner::run_ezs;
use crate::types::CommandResult;
use tauri::State;

#[tauri::command]
pub fn get_config(
    state: State<'_, ConnectionState>,
    repo_path: String,
) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?;
    run_ezs(&repo_path, &["config", "show"], conn.as_ref())
}

#[tauri::command]
pub fn set_config(
    state: State<'_, ConnectionState>,
    repo_path: String,
    key: String,
    value: String,
) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?;
    run_ezs(&repo_path, &["config", "set", &key, &value], conn.as_ref())
}
