use crate::runner::{run_ezs_auto, run_git_auto};
use crate::types::StatusStack;
use super::connection::ConnectionState;
use tauri::State;

#[tauri::command]
pub fn get_stacks_status(state: State<'_, ConnectionState>, repo_path: String) -> Result<Vec<StatusStack>, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let result = run_ezs_auto(conn.as_ref(), &repo_path, &["-y", "status", "--json", "--all"])?;
    if result.exit_code != 0 {
        return Err(format!("ezs status failed (exit {}): {}", result.exit_code, result.stderr));
    }
    let stacks: Vec<StatusStack> = serde_json::from_str(&result.stdout)
        .map_err(|e| format!("Failed to parse ezs output: {e}"))?;
    Ok(stacks)
}

#[tauri::command]
pub fn get_repo_path(start_path: String) -> Result<String, String> {
    let result = crate::runner::run_git(&start_path, &["rev-parse", "--show-toplevel"])?;
    if result.exit_code != 0 {
        return Err("Not a git repository".to_string());
    }
    Ok(result.stdout.trim().to_string())
}

#[tauri::command]
pub fn get_current_branch(state: State<'_, ConnectionState>, repo_path: String) -> Result<String, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let result = run_git_auto(conn.as_ref(), &repo_path, &["rev-parse", "--abbrev-ref", "HEAD"])?;
    if result.exit_code != 0 {
        return Err("Could not determine current branch".to_string());
    }
    Ok(result.stdout.trim().to_string())
}
