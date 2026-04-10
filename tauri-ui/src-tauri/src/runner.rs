use crate::types::{CommandResult, SshConnection};
use std::path::PathBuf;
use std::process::Command;
use std::sync::OnceLock;

/// Cached resolved path to the local `ezs` binary.
static EZS_BINARY: OnceLock<String> = OnceLock::new();

/// Get the user's home directory reliably.
/// Tries multiple approaches since Tauri apps launched from Finder/dock
/// may not inherit the shell's environment variables.
pub fn get_home_dir() -> Option<PathBuf> {
    // 1. Try dirs crate (uses platform-specific APIs, not just env vars)
    if let Some(home) = dirs::home_dir() {
        if home.is_absolute() && home.exists() {
            return Some(home);
        }
    }

    // 2. Try HOME env var directly
    if let Ok(home) = std::env::var("HOME") {
        let p = PathBuf::from(&home);
        if p.is_absolute() && p.exists() {
            return Some(p);
        }
    }

    // 3. macOS fallback: /Users/<current_user>
    #[cfg(target_os = "macos")]
    {
        if let Ok(output) = Command::new("id").arg("-un").output() {
            if output.status.success() {
                let user = String::from_utf8_lossy(&output.stdout).trim().to_string();
                if !user.is_empty() {
                    let p = PathBuf::from(format!("/Users/{}", user));
                    if p.exists() {
                        return Some(p);
                    }
                }
            }
        }
    }

    None
}

/// Resolve the local `ezs` binary path.
/// Tauri apps launched from Finder/dock do NOT inherit the user's shell PATH,
/// so we must check common install locations directly.
fn find_ezs_binary() -> String {
    let home = match get_home_dir() {
        Some(h) => h,
        None => {
            // Can't determine home — try bare "ezs" and hope for the best
            return "ezs".to_string();
        }
    };

    // Check common install locations (same as VS Code extension)
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

    // Homebrew on Apple Silicon
    common_paths.push(PathBuf::from("/opt/homebrew/bin/ezs"));

    for p in &common_paths {
        if p.exists() {
            return p.to_string_lossy().to_string();
        }
    }

    // Fallback: resolve via login shell (handles custom PATH setups)
    for shell in &["bash", "zsh"] {
        if let Ok(output) = Command::new(shell)
            .args(["-lc", "command -v ezs 2>/dev/null"])
            .output()
        {
            if output.status.success() {
                let path = String::from_utf8_lossy(&output.stdout).trim().to_string();
                if !path.is_empty() {
                    let p = PathBuf::from(&path);
                    if p.is_absolute() && p.exists() {
                        return path;
                    }
                }
            }
        }
    }

    // Fallback — will fail with a clear error if not found
    "ezs".to_string()
}

/// Get the resolved `ezs` binary path (cached after first call).
fn ezs_binary() -> &'static str {
    EZS_BINARY.get_or_init(find_ezs_binary)
}

/// Run an ezs CLI command in the given repo directory.
pub fn run_ezs(repo_path: &str, args: &[&str]) -> Result<CommandResult, String> {
    let binary = ezs_binary();
    let output = Command::new(binary)
        .args(args)
        .current_dir(repo_path)
        .output()
        .map_err(|e| format!("Failed to run ezs (resolved: {binary}): {e}"))?;

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
/// Wraps the command in a login shell so the user's PATH is loaded.
pub fn run_ssh_command(conn: &SshConnection, remote_cmd: &str) -> Result<CommandResult, String> {
    let mut cmd = ssh_base(conn);
    // Use bash -lc to load the user's login profile (PATH, etc.)
    // This is necessary because BatchMode=yes skips interactive shell init.
    cmd.arg(format!("bash -lc {}", shell_escape(remote_cmd)));
    let output = cmd
        .output()
        .map_err(|e| format!("Failed to run SSH: {e}"))?;

    Ok(CommandResult {
        stdout: String::from_utf8_lossy(&output.stdout).to_string(),
        stderr: String::from_utf8_lossy(&output.stderr).to_string(),
        exit_code: output.status.code().unwrap_or(-1),
    })
}

/// Run an ezs command on a remote machine via SSH.
pub fn run_remote_ezs(conn: &SshConnection, repo_path: &str, args: &[&str]) -> Result<CommandResult, String> {
    let escaped_path = shell_escape(repo_path);
    let escaped_args: Vec<String> = args.iter().map(|a| shell_escape(a)).collect();
    let cmd = format!("cd {} && ezs {}", escaped_path, escaped_args.join(" "));
    run_ssh_command(conn, &cmd)
}

/// Run a git command on a remote machine via SSH.
pub fn run_remote_git(conn: &SshConnection, repo_path: &str, args: &[&str]) -> Result<CommandResult, String> {
    let escaped_path = shell_escape(repo_path);
    let escaped_args: Vec<String> = args.iter().map(|a| shell_escape(a)).collect();
    let cmd = format!("cd {} && git {}", escaped_path, escaped_args.join(" "));
    run_ssh_command(conn, &cmd)
}

/// Run an ezs command, routing through SSH if a connection is active.
pub fn run_ezs_auto(conn: Option<&SshConnection>, repo_path: &str, args: &[&str]) -> Result<CommandResult, String> {
    match conn {
        Some(c) => run_remote_ezs(c, repo_path, args),
        None => run_ezs(repo_path, args),
    }
}

/// Run a git command, routing through SSH if a connection is active.
pub fn run_git_auto(conn: Option<&SshConnection>, repo_path: &str, args: &[&str]) -> Result<CommandResult, String> {
    match conn {
        Some(c) => run_remote_git(c, repo_path, args),
        None => run_git(repo_path, args),
    }
}
