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

/// Open an ezs command in an external terminal window.
/// Used for interactive commands like `agent` that need stdin/stdout.
pub fn open_in_terminal(repo_path: &str, args: &[String]) -> Result<(), String> {
    let tmp_dir = std::env::temp_dir();
    let script_path = tmp_dir.join("ezstack-agent.sh");

    let mut script = String::from("#!/bin/bash\n");
    script.push_str(&format!("cd '{}'\n", repo_path.replace('\'', "'\\''")));
    script.push_str("ezs");
    for arg in args {
        script.push(' ');
        script.push('\'');
        script.push_str(&arg.replace('\'', "'\\''"));
        script.push('\'');
    }
    script.push('\n');

    std::fs::write(&script_path, &script)
        .map_err(|e| format!("Failed to write temp script: {e}"))?;

    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&script_path, std::fs::Permissions::from_mode(0o755))
            .map_err(|e| format!("Failed to set script permissions: {e}"))?;
    }

    std::process::Command::new("open")
        .args(["-a", "Terminal", script_path.to_str().unwrap()])
        .spawn()
        .map_err(|e| format!("Failed to open terminal: {e}"))?;

    Ok(())
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
