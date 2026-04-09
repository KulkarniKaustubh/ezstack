use crate::types::{CommandResult, SshConnection};
use std::process::Command;

/// Shell-escape a string by wrapping in single quotes.
fn shell_escape(s: &str) -> String {
    // Replace each ' with '\'' (end quote, escaped quote, start quote)
    format!("'{}'", s.replace('\'', "'\\''"))
}

/// Build an SSH command prefix from the connection config.
fn ssh_base(conn: &SshConnection) -> Command {
    let mut cmd = Command::new("ssh");
    cmd.args(["-o", "StrictHostKeyChecking=accept-new"]);
    cmd.args(["-o", "ConnectTimeout=10"]);
    cmd.args(["-o", "BatchMode=yes"]);
    if !conn.key_path.is_empty() {
        cmd.args(["-i", &conn.key_path]);
    }
    if conn.port != 22 {
        cmd.args(["-p", &conn.port.to_string()]);
    }
    cmd.arg(format!("{}@{}", conn.user, conn.host));
    cmd
}

/// Run a command either locally or over SSH.
fn run_command(
    binary: &str,
    repo_path: &str,
    args: &[&str],
    conn: Option<&SshConnection>,
) -> Result<CommandResult, String> {
    let output = match conn {
        None => {
            // Local execution
            Command::new(binary)
                .args(args)
                .current_dir(repo_path)
                .output()
                .map_err(|e| format!("Failed to run {binary}: {e}"))?
        }
        Some(ssh) => {
            // Build the remote command string with proper escaping
            let escaped_args: Vec<String> = args.iter().map(|a| shell_escape(a)).collect();
            let remote_cmd = format!(
                "cd {} && {} {}",
                shell_escape(&ssh.remote_repo_path),
                binary,
                escaped_args.join(" ")
            );
            let mut cmd = ssh_base(ssh);
            cmd.arg(remote_cmd);
            cmd.output()
                .map_err(|e| format!("Failed to run SSH command: {e}"))?
        }
    };

    Ok(CommandResult {
        stdout: String::from_utf8_lossy(&output.stdout).to_string(),
        stderr: String::from_utf8_lossy(&output.stderr).to_string(),
        exit_code: output.status.code().unwrap_or(-1),
    })
}

/// Run an ezs CLI command in the given repo directory.
pub fn run_ezs(
    repo_path: &str,
    args: &[&str],
    conn: Option<&SshConnection>,
) -> Result<CommandResult, String> {
    let result = run_command("ezs", repo_path, args, conn)?;
    if conn.is_some() && result.exit_code == 255 && result.stderr.contains("ssh") {
        return Err(format!(
            "SSH connection failed: {}",
            result.stderr.trim()
        ));
    }
    Ok(result)
}

/// Run a git command in the given repo directory.
pub fn run_git(
    repo_path: &str,
    args: &[&str],
    conn: Option<&SshConnection>,
) -> Result<CommandResult, String> {
    let result = run_command("git", repo_path, args, conn)?;
    if conn.is_some() && result.exit_code == 255 && result.stderr.contains("ssh") {
        return Err(format!(
            "SSH connection failed: {}",
            result.stderr.trim()
        ));
    }
    Ok(result)
}

/// Run a raw SSH command (for connection testing).
pub fn run_ssh_command(conn: &SshConnection, remote_cmd: &str) -> Result<CommandResult, String> {
    let mut cmd = ssh_base(conn);
    cmd.arg(remote_cmd);
    let output = cmd
        .output()
        .map_err(|e| format!("Failed to run SSH: {e}"))?;

    Ok(CommandResult {
        stdout: String::from_utf8_lossy(&output.stdout).to_string(),
        stderr: String::from_utf8_lossy(&output.stderr).to_string(),
        exit_code: output.status.code().unwrap_or(-1),
    })
}
