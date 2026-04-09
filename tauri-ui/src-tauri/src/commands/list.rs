use crate::runner::{run_ezs, run_git};
use crate::types::{StatusStack};

#[tauri::command]
pub fn get_stacks_status(repo_path: String) -> Result<Vec<StatusStack>, String> {
    let result = run_ezs(&repo_path, &["-y", "status", "--json", "--all"])?;
    if result.exit_code != 0 {
        return Err(format!("ezs status failed (exit {}): {}", result.exit_code, result.stderr));
    }
    let stacks: Vec<StatusStack> = serde_json::from_str(&result.stdout)
        .map_err(|e| format!("Failed to parse ezs output: {e}"))?;
    Ok(stacks)
}

#[tauri::command]
pub fn get_repo_path(start_path: String) -> Result<String, String> {
    let result = run_git(&start_path, &["rev-parse", "--show-toplevel"])?;
    if result.exit_code != 0 {
        return Err("Not a git repository".to_string());
    }
    Ok(result.stdout.trim().to_string())
}

#[tauri::command]
pub fn get_current_branch(repo_path: String) -> Result<String, String> {
    let result = run_git(&repo_path, &["rev-parse", "--abbrev-ref", "HEAD"])?;
    if result.exit_code != 0 {
        return Err("Could not determine current branch".to_string());
    }
    Ok(result.stdout.trim().to_string())
}
