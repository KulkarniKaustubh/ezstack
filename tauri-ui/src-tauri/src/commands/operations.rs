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
