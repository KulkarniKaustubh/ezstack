use crate::types::{CommandResult, SshConnection};
use std::path::PathBuf;
use std::process::Command;
use std::sync::OnceLock;

/// Cached resolved path to the local `ezs` binary.
static EZS_BINARY: OnceLock<String> = OnceLock::new();

/// Resolve the local `ezs` binary path, matching the VS Code extension logic:
///   1. Try `whereis ezs` (finds actual binary, unlike `which` which may show shell functions)
///   2. Check common install locations
///   3. Fallback to bare "ezs"
fn find_ezs_binary() -> String {
    // 1. Try `whereis ezs` — `which` may return a shell function instead of the binary
    //    whereis output format: "ezs: /path/to/ezs [/other/path ...]"
    if let Ok(output) = Command::new("whereis").arg("ezs").output() {
        if output.status.success() {
            let line = String::from_utf8_lossy(&output.stdout).trim().to_string();
            // Strip the "ezs: " prefix and take the first path
            if let Some(paths) = line.strip_prefix("ezs:") {
                let paths = paths.trim();
                if let Some(first) = paths.split_whitespace().next() {
                    if PathBuf::from(first).exists() {
                        return first.to_string();
                    }
                }
            }
        }
    }

    // 2. Check common install locations
    let home = dirs::home_dir().unwrap_or_default();
    let mut common_paths: Vec<PathBuf> = vec![
        // make install (XDG convention)
        home.join(".local").join("bin").join("ezs"),
    ];

    // go install (GOBIN)
    if let Ok(gobin) = std::env::var("GOBIN") {
        if !gobin.is_empty() {
            common_paths.push(PathBuf::from(&gobin).join("ezs"));
        }
    }

    // go install (GOPATH/bin)
    if let Ok(gopath) = std::env::var("GOPATH") {
        if !gopath.is_empty() {
            common_paths.push(PathBuf::from(&gopath).join("bin").join("ezs"));
        }
    }

    // go install (default ~/go/bin)
    common_paths.push(home.join("go").join("bin").join("ezs"));

    // system-wide
    common_paths.push(PathBuf::from("/usr/local/bin/ezs"));

    for p in &common_paths {
        if p.exists() {
            return p.to_string_lossy().to_string();
        }
    }

    // 3. Fallback — will fail with a clear error if not found
    "ezs".to_string()
}

/// Get the resolved `ezs` binary path (cached after first call).
fn ezs_binary() -> &'static str {
    EZS_BINARY.get_or_init(find_ezs_binary)
}

/// Run an ezs CLI command in the given repo directory.
pub fn run_ezs(repo_path: &str, args: &[&str]) -> Result<CommandResult, String> {
    let output = Command::new(ezs_binary())
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

    let ezs_path = ezs_binary();
    let mut script = String::from("#!/bin/bash\n");
    script.push_str(&format!("cd '{}'\n", repo_path.replace('\'', "'\\''")));
    script.push_str(&format!("'{}'", ezs_path.replace('\'', "'\\''")));
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

/// Shell-escape a string by wrapping in single quotes.
#[allow(dead_code)]
fn shell_escape(s: &str) -> String {
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
