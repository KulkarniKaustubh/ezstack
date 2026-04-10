use crate::runner::run_ezs;
use crate::types::CommandResult;

#[tauri::command]
pub fn create_branch(repo_path: String, name: String, parent: Option<String>) -> Result<CommandResult, String> {
    let mut args = vec!["-y", "new", &name];
    let parent_val;
    if let Some(ref p) = parent {
        parent_val = p.clone();
        args.push("-p");
        args.push(&parent_val);
    }
    run_ezs(&repo_path, &args)
}

#[tauri::command]
pub fn sync_branch(repo_path: String, scope: String) -> Result<CommandResult, String> {
    let mut args = vec!["-y", "sync"];
    match scope.as_str() {
        "all" => args.push("-a"),
        "stack" => args.push("-s"),
        _ => {
            args.push("-c");
        }
    }
    run_ezs(&repo_path, &args)
}

#[tauri::command]
pub fn push_branch(repo_path: String, stack: bool, force: bool) -> Result<CommandResult, String> {
    let mut args = vec!["-y", "push"];
    if stack {
        args.push("-s");
    }
    if force {
        args.push("-f");
    }
    run_ezs(&repo_path, &args)
}

#[tauri::command]
pub fn delete_branch(repo_path: String, branch: String, force: bool) -> Result<CommandResult, String> {
    let mut args = vec!["-y", "delete"];
    if force {
        args.push("-f");
    }
    args.push(&branch);
    run_ezs(&repo_path, &args)
}

#[tauri::command]
pub fn reparent_branch(repo_path: String, branch: String, new_parent: String) -> Result<CommandResult, String> {
    run_ezs(&repo_path, &["-y", "reparent", &branch, &new_parent])
}

#[tauri::command]
pub fn rename_stack(repo_path: String, stack_hash: String, name: String) -> Result<CommandResult, String> {
    run_ezs(&repo_path, &["-y", "stack", "rename", &stack_hash, &name])
}

#[tauri::command]
pub fn open_agent(repo_path: String, stack_hash: Option<String>, branch: Option<String>) -> Result<(), String> {
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
pub fn open_agent_feature(repo_path: String, stack_hash: String, description: String) -> Result<(), String> {
    let args = vec![
        "agent".to_string(),
        "-s".to_string(),
        stack_hash,
        "feature".to_string(),
        description,
    ];
    crate::runner::open_in_terminal(&repo_path, &args)
}
