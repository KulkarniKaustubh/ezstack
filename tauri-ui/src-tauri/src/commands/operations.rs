use crate::runner::{run_ezs, run_ezs_auto};
use crate::types::CommandResult;
use super::connection::ConnectionState;
use tauri::State;

#[tauri::command]
pub fn create_branch(state: State<'_, ConnectionState>, repo_path: String, name: String, parent: Option<String>) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let mut args = vec!["-y", "new", &name];
    let parent_val;
    if let Some(ref p) = parent {
        parent_val = p.clone();
        args.push("-p");
        args.push(&parent_val);
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

#[tauri::command]
pub fn sync_branch(state: State<'_, ConnectionState>, repo_path: String, scope: String) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let mut args = vec!["-y", "sync"];
    match scope.as_str() {
        "all" => args.push("-a"),
        "stack" => args.push("-s"),
        _ => {
            args.push("-c");
        }
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

#[tauri::command]
pub fn push_branch(state: State<'_, ConnectionState>, repo_path: String, stack: bool, force: bool) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let mut args = vec!["-y", "push"];
    if stack {
        args.push("-s");
    }
    if force {
        args.push("-f");
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

#[tauri::command]
pub fn delete_branch(state: State<'_, ConnectionState>, repo_path: String, branch: String, force: bool) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let mut args = vec!["-y", "delete"];
    if force {
        args.push("-f");
    }
    args.push(&branch);
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

#[tauri::command]
pub fn reparent_branch(state: State<'_, ConnectionState>, repo_path: String, branch: String, new_parent: String) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    run_ezs_auto(conn.as_ref(), &repo_path, &["-y", "reparent", &branch, &new_parent])
}

#[tauri::command]
pub fn rename_stack(state: State<'_, ConnectionState>, repo_path: String, stack_hash: String, name: String) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    run_ezs_auto(conn.as_ref(), &repo_path, &["-y", "stack", "rename", &stack_hash, &name])
}

#[tauri::command]
pub fn open_agent(state: State<'_, ConnectionState>, repo_path: String, stack_hash: Option<String>, branch: Option<String>) -> Result<(), String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    if conn.is_some() {
        return Err("Agent commands are not supported for remote connections".to_string());
    }
    let mut args = vec!["agent".to_string()];
    if let Some(ref b) = branch {
        args.push("-b".to_string());
        args.push(b.clone());
    } else if let Some(ref s) = stack_hash {
        args.push("-s".to_string());
        args.push(s.clone());
    }
    crate::runner::open_in_terminal(&repo_path, &args)
}

#[tauri::command]
pub fn open_agent_feature(state: State<'_, ConnectionState>, repo_path: String, stack_hash: String, description: String) -> Result<(), String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    if conn.is_some() {
        return Err("Agent commands are not supported for remote connections".to_string());
    }
    let args = vec![
        "agent".to_string(),
        "-s".to_string(),
        stack_hash,
        "feature".to_string(),
        description,
    ];
    crate::runner::open_in_terminal(&repo_path, &args)
}

/// Get agent prompt templates by running `ezs agent prompt`.
/// Returns stdout with ANSI codes stripped.
#[tauri::command]
pub fn get_agent_prompts(repo_path: String) -> Result<String, String> {
    let result = run_ezs(&repo_path, &["agent", "prompt"])?;
    if result.exit_code != 0 {
        return Err(result.stderr);
    }
    // Strip ANSI escape codes for clean display in UI
    Ok(strip_ansi(&result.stdout))
}

/// Reset agent prompt(s) to built-in defaults.
/// `which` can be "work", "feature", or "both".
#[tauri::command]
pub fn reset_agent_prompts(repo_path: String, which: String) -> Result<String, String> {
    let mut args = vec!["agent", "prompt", "--reset"];
    match which.as_str() {
        "work" => args.push("--work"),
        "feature" => args.push("--feature"),
        _ => {} // both — no extra flag needed
    }
    let result = run_ezs(&repo_path, &args)?;
    if result.exit_code != 0 {
        return Err(result.stderr);
    }
    Ok(strip_ansi(&result.stdout))
}

/// Open agent prompt editor in an external terminal.
/// `which` can be "work", "feature", or "both".
#[tauri::command]
pub fn edit_agent_prompts(repo_path: String, which: String) -> Result<(), String> {
    let mut args = vec!["agent".to_string(), "prompt".to_string(), "--edit".to_string()];
    match which.as_str() {
        "work" => args.push("--work".to_string()),
        "feature" => args.push("--feature".to_string()),
        _ => {} // both
    }
    crate::runner::open_in_terminal(&repo_path, &args)
}

/// Strip ANSI escape codes from a string.
fn strip_ansi(s: &str) -> String {
    let mut result = String::with_capacity(s.len());
    let mut in_escape = false;
    let bytes = s.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == 0x1b && i + 1 < bytes.len() && bytes[i + 1] == b'[' {
            in_escape = true;
            i += 2;
            continue;
        }
        if in_escape {
            if bytes[i].is_ascii_alphabetic() {
                in_escape = false;
            }
            i += 1;
            continue;
        }
        result.push(bytes[i] as char);
        i += 1;
    }
    result
}
