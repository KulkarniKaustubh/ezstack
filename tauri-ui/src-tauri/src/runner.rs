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
            // Local execution — use resolved path for ezs, bare name for others
            let resolved = if binary == "ezs" { ezs_binary() } else { binary };
            Command::new(resolved)
                .args(args)
                .current_dir(repo_path)
                .output()
                .map_err(|e| format!("Failed to run {binary}: {e}"))?
        }
        Some(ssh) => {
            // Build the remote command string with proper escaping
            // Remote uses bare binary name — PATH is handled by the remote shell
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
