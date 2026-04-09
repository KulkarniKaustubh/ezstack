use crate::commands::connection::ConnectionState;
use crate::runner::run_ezs;
use crate::types::CommandResult;
use tauri::State;

#[tauri::command]
pub fn pr_create(
    state: State<'_, ConnectionState>,
    repo_path: String,
    title: String,
    body: Option<String>,
    draft: bool,
    branch: Option<String>,
) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?;
    let mut args = vec!["-y", "pr", "create", "-t", &title];
    if let Some(ref b) = body {
        args.push("-b");
        args.push(b);
    }
    if draft {
        args.push("--draft");
    }
    if let Some(ref br) = branch {
        args.push("--branch");
        args.push(br);
    }
    run_ezs(&repo_path, &args, conn.as_ref())
}

#[tauri::command]
pub fn pr_update(
    state: State<'_, ConnectionState>,
    repo_path: String,
    branch: Option<String>,
) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?;
    let mut args = vec!["-y", "pr", "update"];
    if let Some(ref b) = branch {
        args.push("--branch");
        args.push(b);
    }
    run_ezs(&repo_path, &args, conn.as_ref())
}

#[tauri::command]
pub fn pr_merge(
    state: State<'_, ConnectionState>,
    repo_path: String,
    method: String,
    branch: Option<String>,
) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?;
    let mut args = vec!["-y", "pr", "merge", "-m", &method];
    if let Some(ref b) = branch {
        args.push("--branch");
        args.push(b);
    }
    run_ezs(&repo_path, &args, conn.as_ref())
}

#[tauri::command]
pub fn pr_toggle_draft(
    state: State<'_, ConnectionState>,
    repo_path: String,
    branch: Option<String>,
) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?;
    let mut args = vec!["-y", "pr", "draft"];
    if let Some(ref b) = branch {
        args.push("--branch");
        args.push(b);
    }
    run_ezs(&repo_path, &args, conn.as_ref())
}

#[tauri::command]
pub fn pr_update_stack(
    state: State<'_, ConnectionState>,
    repo_path: String,
) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?;
    run_ezs(&repo_path, &["-y", "pr", "stack"], conn.as_ref())
}
