use crate::runner::run_ezs;
use crate::types::CommandResult;

#[tauri::command]
pub fn get_config(repo_path: String) -> Result<CommandResult, String> {
    run_ezs(&repo_path, &["config", "show"])
}

#[tauri::command]
pub fn set_config(repo_path: String, key: String, value: String) -> Result<CommandResult, String> {
    run_ezs(&repo_path, &["config", "set", &key, &value])
}
