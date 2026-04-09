use crate::types::CommandResult;
use std::process::Command;

/// Run an ezs CLI command in the given repo directory.
pub fn run_ezs(repo_path: &str, args: &[&str]) -> Result<CommandResult, String> {
    let output = Command::new("ezs")
        .args(args)
        .current_dir(repo_path)
        .output()
        .map_err(|e| format!("Failed to run ezs: {e}. Is ezs installed and on PATH?"))?;

    Ok(CommandResult {
        stdout: String::from_utf8_lossy(&output.stdout).to_string(),
        stderr: String::from_utf8_lossy(&output.stderr).to_string(),
        exit_code: output.status.code().unwrap_or(-1),
    })
}

/// Run a git command in the given repo directory.
pub fn run_git(repo_path: &str, args: &[&str]) -> Result<CommandResult, String> {
    let output = Command::new("git")
        .args(args)
        .current_dir(repo_path)
        .output()
        .map_err(|e| format!("Failed to run git: {e}"))?;

    Ok(CommandResult {
        stdout: String::from_utf8_lossy(&output.stdout).to_string(),
        stderr: String::from_utf8_lossy(&output.stderr).to_string(),
        exit_code: output.status.code().unwrap_or(-1),
    })
}
