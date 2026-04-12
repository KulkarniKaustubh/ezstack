use crate::types::{CommandResult, SshConnection};
use std::path::PathBuf;
use std::process::Command;
use std::sync::OnceLock;
use std::time::{SystemTime, UNIX_EPOCH};

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

/// Generate a unique-ish suffix for temp files.
fn unique_suffix() -> String {
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or(0);
    format!("{}-{}", std::process::id(), nanos)
}

/// Open an ezs command in an external terminal window.
/// Used for interactive commands like `agent` that need stdin/stdout.
pub fn open_in_terminal(repo_path: &str, args: &[String]) -> Result<(), String> {
    let tmp_dir = std::env::temp_dir();
    let script_name = format!("ezstack-agent-{}.sh", unique_suffix());
    let script_path = tmp_dir.join(script_name);

    let ezs_path = ezs_binary();
    let mut script = String::from("#!/bin/bash\n");
    // Clean up the temp script on exit
    script.push_str(&format!(
        "trap 'rm -f '\\''{}'\\''' EXIT\n",
        script_path.display()
    ));
    script.push_str(&format!("cd '{}'\n", repo_path.replace('\'', "'\\''")));
    script.push_str(&format!("'{}'", ezs_path.replace('\'', "'\\''")));
    for arg in args {
        script.push(' ');
        script.push('\'');
        script.push_str(&arg.replace('\'', "'\\''"));
        script.push('\'');
    }
    script.push('\n');

    // Create the script with 0o700 from the start (avoid the
    // chmod-after-write TOCTOU window on shared /tmp filesystems) and
    // refuse to overwrite an existing file (defends against predictable
    // name collisions from pid+nanos suffix).
    #[cfg(unix)]
    {
        use std::io::Write;
        use std::os::unix::fs::OpenOptionsExt;
        let mut f = std::fs::OpenOptions::new()
            .create_new(true)
            .write(true)
            .mode(0o700)
            .open(&script_path)
            .map_err(|e| format!("Failed to create temp script: {e}"))?;
        f.write_all(script.as_bytes())
            .map_err(|e| format!("Failed to write temp script: {e}"))?;
    }

    #[cfg(not(unix))]
    {
        std::fs::write(&script_path, &script)
            .map_err(|e| format!("Failed to write temp script: {e}"))?;
    }

    let path_str = script_path
        .to_str()
        .ok_or("Temp script path contains invalid UTF-8")?;

    #[cfg(target_os = "macos")]
    {
        std::process::Command::new("open")
            .args(["-a", "Terminal", path_str])
            .spawn()
            .map_err(|e| format!("Failed to open terminal: {e}"))?;
    }

    #[cfg(target_os = "linux")]
    {
        // Try a sequence of common terminal emulators.
        let attempts: &[(&str, &[&str])] = &[
            ("x-terminal-emulator", &["-e"]),
            ("gnome-terminal", &["--"]),
            ("konsole", &["-e"]),
            ("xterm", &["-e"]),
        ];
        let mut spawned = false;
        for (term, flag) in attempts {
            let mut cmd = std::process::Command::new(term);
            cmd.args(*flag);
            cmd.arg(path_str);
            if cmd.spawn().is_ok() {
                spawned = true;
                break;
            }
        }
        if !spawned {
            return Err("No supported terminal emulator found (tried gnome-terminal, konsole, xterm)".to_string());
        }
    }

    #[cfg(target_os = "windows")]
    {
        // Run the script via WSL bash if available, otherwise via cmd.
        std::process::Command::new("cmd")
            .args(["/c", "start", "", "bash", path_str])
            .spawn()
            .map_err(|e| format!("Failed to open terminal: {e}"))?;
    }

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
pub fn shell_escape(s: &str) -> String {
    format!("'{}'", s.replace('\'', "'\\''"))
}

/// Build an SSH command prefix from the connection config.
fn ssh_base(conn: &SshConnection) -> Command {
    let mut cmd = Command::new("ssh");
    // TOFU: accept new host keys, but reject changed ones (real MITM defense).
    cmd.args(["-o", "StrictHostKeyChecking=accept-new"]);
    cmd.args(["-o", "ConnectTimeout=10"]);
    cmd.args(["-o", "ServerAliveInterval=30"]);
    cmd.args(["-o", "ServerAliveCountMax=3"]);
    cmd.args(["-o", "BatchMode=yes"]);
    // Suppress noisy "Warning: Permanently added 'host' (ED25519) to known_hosts"
    // on stderr so error classification doesn't trip on it.
    cmd.args(["-o", "LogLevel=ERROR"]);
    if !conn.key_path.is_empty() {
        cmd.args(["-i", &conn.key_path]);
    }
    if !conn.jump_host.is_empty() {
        cmd.args(["-J", &conn.jump_host]);
    }
    if conn.port != 22 {
        cmd.args(["-p", &conn.port.to_string()]);
    }
    cmd.arg(format!("{}@{}", conn.user, conn.host));
    cmd
}

/// Classify common SSH stderr patterns into a friendlier message.
/// Falls back to the raw stderr (trimmed) if no pattern matches.
pub fn classify_ssh_error(stderr: &str) -> String {
    let s = stderr.trim();
    let lower = s.to_lowercase();

    if lower.contains("permission denied") {
        return "SSH authentication failed (permission denied). Check the username, SSH key, and that your public key is in the remote ~/.ssh/authorized_keys.".to_string();
    }
    if lower.contains("host key verification failed") {
        return "SSH host key verification failed. The remote host key has changed or is unknown. Inspect ~/.ssh/known_hosts on this machine.".to_string();
    }
    if lower.contains("could not resolve hostname") || lower.contains("name or service not known") {
        return "Could not resolve remote hostname. Check spelling and that DNS is reachable.".to_string();
    }
    if lower.contains("connection timed out") || lower.contains("operation timed out") {
        return "Connection to remote timed out. Check the host is reachable on the given port and the firewall allows SSH.".to_string();
    }
    if lower.contains("connection refused") {
        return "Connection refused. Is sshd running on the remote on the configured port?".to_string();
    }
    if lower.contains("network is unreachable") || lower.contains("no route to host") {
        return "Remote network is unreachable from this machine.".to_string();
    }
    if lower.contains("kex_exchange_identification") || lower.contains("connection closed by") {
        return "SSH handshake failed (connection closed by remote). The host may be rate-limiting or sshd may be misconfigured.".to_string();
    }
    if lower.contains("warning: unprotected private key") {
        return "Your SSH private key has overly permissive permissions. Run `chmod 600` on it.".to_string();
    }
    if lower.contains("no such identity") || lower.contains("no such file or directory") {
        return "SSH key file not found. Check the path is correct and readable.".to_string();
    }
    if lower.contains("too many authentication failures") {
        return "Too many authentication failures. Specify an explicit key with -i / IdentityFile to avoid trying every key in the SSH agent.".to_string();
    }
    if s.is_empty() {
        return "SSH connection failed (no error output).".to_string();
    }
    s.to_string()
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

/// Scan a remote host's public keys via `ssh-keyscan` and pipe the result
/// through `ssh-keygen -lf -` to produce SHA256 fingerprint lines.
/// Returns one `<bits> SHA256:<hash> <host> (<keytype>)` line per host key.
/// Used by the host-fingerprint preview so users can manually verify a key
/// on first connect (defends against silent TOFU acceptance of MITM keys).
pub fn ssh_host_fingerprints(host: &str, port: u16) -> Result<String, String> {
    use std::io::Write;
    use std::process::Stdio;

    let port_s = port.to_string();
    let scan = Command::new("ssh-keyscan")
        .args(["-T", "5", "-t", "ed25519,ecdsa,rsa", "-p", &port_s, host])
        .stderr(Stdio::null())
        .output()
        .map_err(|e| format!("Failed to run ssh-keyscan: {e}"))?;
    if !scan.status.success() || scan.stdout.is_empty() {
        return Err(format!("ssh-keyscan returned no host keys for {host}:{port} (host unreachable or DNS failure)"));
    }

    let mut keygen = Command::new("ssh-keygen")
        .args(["-l", "-f", "-", "-E", "sha256"])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| format!("Failed to run ssh-keygen: {e}"))?;
    {
        let stdin = keygen
            .stdin
            .as_mut()
            .ok_or_else(|| "ssh-keygen stdin unavailable".to_string())?;
        stdin
            .write_all(&scan.stdout)
            .map_err(|e| format!("Failed to write to ssh-keygen: {e}"))?;
    }
    let out = keygen
        .wait_with_output()
        .map_err(|e| format!("Failed to wait for ssh-keygen: {e}"))?;
    if !out.status.success() {
        return Err(format!(
            "ssh-keygen fingerprint failed: {}",
            String::from_utf8_lossy(&out.stderr).trim()
        ));
    }
    Ok(String::from_utf8_lossy(&out.stdout).to_string())
}
