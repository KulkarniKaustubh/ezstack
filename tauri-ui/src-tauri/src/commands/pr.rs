use crate::runner::run_ezs_auto;
use crate::types::CommandResult;
use super::connection::ConnectionState;
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
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
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
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

#[tauri::command]
pub fn pr_update(state: State<'_, ConnectionState>, repo_path: String, branch: Option<String>) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let mut args = vec!["-y", "pr", "update"];
    if let Some(ref b) = branch {
        args.push("--branch");
        args.push(b);
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

#[tauri::command]
pub fn pr_merge(
    state: State<'_, ConnectionState>,
    repo_path: String,
    method: String,
    branch: Option<String>,
) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let mut args = vec!["-y", "pr", "merge", "-m", &method];
    if let Some(ref b) = branch {
        args.push("--branch");
        args.push(b);
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

#[tauri::command]
pub fn pr_toggle_draft(state: State<'_, ConnectionState>, repo_path: String, branch: Option<String>) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let mut args = vec!["-y", "pr", "draft"];
    if let Some(ref b) = branch {
        args.push("--branch");
        args.push(b);
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

#[tauri::command]
pub fn pr_update_stack(state: State<'_, ConnectionState>, repo_path: String) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    run_ezs_auto(conn.as_ref(), &repo_path, &["-y", "pr", "stack"])
}

/// Reconcile the local PR cache from GitHub. Use after PRs are merged,
/// closed, or re-targeted via the GitHub UI to bring `get_stacks_status`
/// back in sync.
///
/// `stack` and `branch` are mutually exclusive on the CLI side; we
/// resolve the conflict client-side by giving `stack` precedence so
/// callers that pass both don't surface the CLI's rejection error.
/// When both are `None` the CLI defaults to the current branch.
#[tauri::command]
pub fn pr_refresh(
    state: State<'_, ConnectionState>,
    repo_path: String,
    branch: Option<String>,
    stack: Option<bool>,
) -> Result<CommandResult, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let mut args: Vec<&str> = vec!["-y", "pr", "refresh"];
    if stack.unwrap_or(false) {
        args.push("--stack");
    } else if let Some(ref b) = branch {
        args.push("--branch");
        args.push(b);
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}
