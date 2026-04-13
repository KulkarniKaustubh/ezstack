use crate::runner::{run_ezs_auto, run_git_auto};
use crate::types::StatusStack;
use super::connection::ConnectionState;
use serde::Serialize;
use tauri::State;

#[derive(Debug, Serialize)]
pub struct ReflogEntry {
    pub hash: String,
    pub relative: String,
    pub action: String,
    pub message: String,
}

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
pub fn get_branch_reflog(
    state: State<'_, ConnectionState>,
    repo_path: String,
    branch: String,
    limit: Option<usize>,
) -> Result<Vec<ReflogEntry>, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let max = limit.unwrap_or(20);
    let max_str = max.to_string();
    let result = run_git_auto(
        conn.as_ref(),
        &repo_path,
        &[
            "reflog",
            "show",
            "--no-decorate",
            "--format=%h%x09%gd%x09%gs",
            &format!("-n{}", max_str),
            &branch,
        ],
    )?;
    if result.exit_code != 0 {
        // Branch may have no reflog (e.g. just created); return empty list rather than error.
        return Ok(Vec::new());
    }
    let mut entries = Vec::new();
    for line in result.stdout.lines() {
        let parts: Vec<&str> = line.splitn(3, '\t').collect();
        if parts.len() < 3 {
            continue;
        }
        let message = parts[2].to_string();
        let action = message.split(':').next().unwrap_or("").trim().to_string();
        entries.push(ReflogEntry {
            hash: parts[0].to_string(),
            relative: parts[1].to_string(),
            action,
            message,
        });
    }
    Ok(entries)
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
