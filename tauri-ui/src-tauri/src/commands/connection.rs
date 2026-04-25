use crate::runner::{
    classify_ssh_error, get_home_dir, run_ssh_command, shell_escape, ssh_host_fingerprints,
};
use crate::types::{
    ConnectionHealth, ConnectionProfile, DiagnosticStep, HostFingerprint, RemoteRepoSummary,
    SshConnection,
};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::fs;
use std::path::PathBuf;
use std::sync::Mutex;
use std::time::{Instant, SystemTime, UNIX_EPOCH};
use tauri::State;

pub struct ConnectionState(pub Mutex<Option<SshConnection>>);

#[derive(Debug, Clone, Serialize, Deserialize)]
struct RemoteRepoConfigEntry {
    repo_path: String,
    #[serde(default)]
    worktree_base_dir: Option<String>,
    #[serde(default)]
    default_base_branch: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct RemoteEzstackConfig {
    #[serde(default)]
    repos: HashMap<String, RemoteRepoConfigEntry>,
}

// ─── Helpers ──────────────────────────────────────────────────────────────

fn profiles_dir() -> PathBuf {
    if let Ok(p) = std::env::var("EZSTACK_DESKTOP_HOME") {
        return PathBuf::from(p);
    }
    if let Some(home) = get_home_dir() {
        return home.join(".ezstack").join("desktop");
    }
    PathBuf::from(".ezstack/desktop")
}

fn profiles_path() -> PathBuf {
    profiles_dir().join("connections.json")
}

fn active_connection_path() -> PathBuf {
    profiles_dir().join("active.json")
}

/// Read the persisted active connection (if any). Best-effort — returns None
/// on any error so a corrupt file can't brick app startup.
pub fn read_active_connection() -> Option<SshConnection> {
    let path = active_connection_path();
    let raw = fs::read_to_string(&path).ok()?;
    if raw.trim().is_empty() {
        return None;
    }
    serde_json::from_str(&raw).ok()
}

fn write_active_connection(conn: Option<&SshConnection>) -> Result<(), String> {
    let dir = profiles_dir();
    fs::create_dir_all(&dir)
        .map_err(|e| format!("Failed to create profiles dir ({}): {}", dir.display(), e))?;
    let path = active_connection_path();
    match conn {
        Some(c) => {
            let json = serde_json::to_string_pretty(c)
                .map_err(|e| format!("Failed to serialize active connection: {e}"))?;
            fs::write(&path, json).map_err(|e| {
                format!("Failed to write active connection ({}): {}", path.display(), e)
            })?;
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                let _ = fs::set_permissions(&path, fs::Permissions::from_mode(0o600));
            }
        }
        None => {
            // Best-effort delete; ignore "not found".
            let _ = fs::remove_file(&path);
        }
    }
    Ok(())
}

fn read_profiles() -> Result<Vec<ConnectionProfile>, String> {
    let path = profiles_path();
    if !path.exists() {
        return Ok(Vec::new());
    }
    let raw = fs::read_to_string(&path)
        .map_err(|e| format!("Failed to read profiles ({}): {}", path.display(), e))?;
    if raw.trim().is_empty() {
        return Ok(Vec::new());
    }
    let profiles: Vec<ConnectionProfile> = serde_json::from_str(&raw)
        .map_err(|e| format!("Failed to parse profiles file: {e}"))?;
    Ok(profiles)
}

fn write_profiles(profiles: &[ConnectionProfile]) -> Result<(), String> {
    let dir = profiles_dir();
    fs::create_dir_all(&dir)
        .map_err(|e| format!("Failed to create profiles dir ({}): {}", dir.display(), e))?;
    let path = profiles_path();
    let json = serde_json::to_string_pretty(profiles)
        .map_err(|e| format!("Failed to serialize profiles: {e}"))?;
    fs::write(&path, json)
        .map_err(|e| format!("Failed to write profiles ({}): {}", path.display(), e))?;
    // Best-effort restrictive permissions on Unix.
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = fs::set_permissions(&path, fs::Permissions::from_mode(0o600));
    }
    Ok(())
}

fn now_unix_ms() -> u128 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis())
        .unwrap_or(0)
}

fn make_conn(
    host: String,
    user: String,
    port: u16,
    key_path: String,
    jump_host: String,
) -> SshConnection {
    SshConnection {
        host,
        user,
        port,
        key_path,
        jump_host,
        remote_repo_path: String::new(),
        label: String::new(),
    }
}

// ─── Connect / discover repos ─────────────────────────────────────────────

/// Connect to remote, discover repos from ~/.ezstack/config.json, and return repo summaries.
#[tauri::command]
pub fn connect_remote(
    host: String,
    user: String,
    port: u16,
    key_path: String,
    jump_host: Option<String>,
) -> Result<Vec<RemoteRepoSummary>, String> {
    let conn = make_conn(host, user, port, key_path, jump_host.unwrap_or_default());

    // Step 1: Test SSH connectivity
    let test = run_ssh_command(&conn, "echo ok")?;
    if test.exit_code != 0 {
        return Err(classify_ssh_error(&test.stderr));
    }

    // Step 2: Verify ezs is available
    let ezs_check = run_ssh_command(&conn, "command -v ezs")?;
    if ezs_check.exit_code != 0 {
        return Err("`ezs` is not installed or not on PATH on the remote machine. Install it on the remote and ensure your login shell PATH includes it.".to_string());
    }

    // Step 3: Read ~/.ezstack/config.json to discover repos
    let config_read = run_ssh_command(&conn, "cat ~/.ezstack/config.json 2>/dev/null")?;
    if config_read.exit_code != 0 || config_read.stdout.trim().is_empty() {
        return Err("No ezstack configuration found on remote (~/.ezstack/config.json missing or empty). Initialize ezstack on the remote first.".to_string());
    }

    let config: RemoteEzstackConfig =
        serde_json::from_str(config_read.stdout.trim()).map_err(|e| {
            format!(
                "Failed to parse remote ezstack config: {} — first bytes: {:?}",
                e,
                &config_read.stdout.chars().take(80).collect::<String>()
            )
        })?;

    if config.repos.is_empty() {
        return Err("No repositories configured in remote ~/.ezstack/config.json. Run `ezs new` on the remote to create one.".to_string());
    }

    // Step 4: For each configured repo, check whether the path actually exists.
    // Build one shell command that emits "<path>\t<state>" lines so we don't open
    // a separate SSH session per repo. We pass the path as a printf argument
    // (single-quoted) so weird characters can't break the parser. Paths
    // containing tab characters are unsupported (and would never be valid
    // ezstack repo paths anyway).
    let paths: Vec<String> = config
        .repos
        .values()
        .map(|r| r.repo_path.clone())
        .filter(|p| !p.contains('\t'))
        .collect();
    let test_cmd = paths
        .iter()
        .map(|p| {
            let q = shell_escape(p);
            format!(
                "if [ -d {p}/.git ] || [ -f {p}/.git ]; then printf '%s\\tok\\n' {p}; else printf '%s\\tmissing\\n' {p}; fi",
                p = q,
            )
        })
        .collect::<Vec<_>>()
        .join(" ; ");
    let exists_check = run_ssh_command(&conn, &test_cmd)?;
    let mut existence: HashMap<String, bool> = HashMap::new();
    if exists_check.exit_code == 0 {
        for line in exists_check.stdout.lines() {
            let mut parts = line.splitn(2, '\t');
            if let (Some(p), Some(state)) = (parts.next(), parts.next()) {
                existence.insert(p.to_string(), state == "ok");
            }
        }
    }

    let mut summaries: Vec<RemoteRepoSummary> = config
        .repos
        .into_values()
        .map(|r| RemoteRepoSummary {
            exists: *existence.get(&r.repo_path).unwrap_or(&true),
            repo_path: r.repo_path,
            worktree_base_dir: r.worktree_base_dir,
            default_base_branch: r.default_base_branch,
        })
        .collect();
    summaries.sort_by(|a, b| a.repo_path.cmp(&b.repo_path));
    Ok(summaries)
}

/// Re-discover repos on the currently active connection without changing it.
#[tauri::command]
pub fn list_remote_repos(
    state: State<'_, ConnectionState>,
) -> Result<Vec<RemoteRepoSummary>, String> {
    let conn_opt = state.0.lock().map_err(|e| e.to_string())?.clone();
    let conn = conn_opt.ok_or_else(|| "No active remote connection".to_string())?;
    connect_remote(
        conn.host,
        conn.user,
        conn.port,
        conn.key_path,
        if conn.jump_host.is_empty() {
            None
        } else {
            Some(conn.jump_host)
        },
    )
}

/// Select a repo after connecting and store the connection state.
#[tauri::command]
pub fn select_remote_repo(
    state: State<'_, ConnectionState>,
    host: String,
    user: String,
    port: u16,
    key_path: String,
    jump_host: Option<String>,
    remote_repo_path: String,
    label: Option<String>,
) -> Result<String, String> {
    let mut conn = make_conn(host, user, port, key_path, jump_host.unwrap_or_default());
    conn.remote_repo_path = remote_repo_path.clone();
    conn.label = label.unwrap_or_default();

    // Verify the repo path exists and is a git repo.
    let repo_check = run_ssh_command(
        &conn,
        &format!(
            "cd {} && git rev-parse --show-toplevel",
            shell_escape(&conn.remote_repo_path)
        ),
    )?;
    if repo_check.exit_code != 0 {
        return Err(format!(
            "Not a git repository on remote: {}",
            classify_ssh_error(&repo_check.stderr)
        ));
    }

    let resolved_path = repo_check.stdout.trim().to_string();

    // Store the connection with the resolved repo path
    let stored = SshConnection {
        remote_repo_path: resolved_path.clone(),
        ..conn
    };
    {
        let mut state = state.0.lock().map_err(|e| e.to_string())?;
        *state = Some(stored.clone());
    }
    // Best-effort persist so the next app launch can auto-reconnect.
    let _ = write_active_connection(Some(&stored));

    Ok(resolved_path)
}

#[tauri::command]
pub fn disconnect_remote(state: State<'_, ConnectionState>) -> Result<(), String> {
    {
        let mut state = state.0.lock().map_err(|e| e.to_string())?;
        *state = None;
    }
    // Best-effort: clear persisted active connection so we don't auto-reconnect.
    let _ = write_active_connection(None);
    Ok(())
}

#[tauri::command]
pub fn get_connection(state: State<'_, ConnectionState>) -> Result<Option<SshConnection>, String> {
    let state = state.0.lock().map_err(|e| e.to_string())?;
    Ok(state.clone())
}

#[tauri::command]
pub fn test_ssh_connection(
    host: String,
    user: String,
    port: u16,
    key_path: String,
    jump_host: Option<String>,
) -> Result<String, String> {
    let conn = make_conn(host, user, port, key_path, jump_host.unwrap_or_default());

    let start = Instant::now();
    let test = run_ssh_command(&conn, "echo ok")?;
    let elapsed = start.elapsed().as_millis();
    if test.exit_code != 0 {
        return Err(classify_ssh_error(&test.stderr));
    }

    Ok(format!("Connection successful ({} ms)", elapsed))
}

/// Heartbeat / latency probe for the *active* connection.
#[tauri::command]
pub fn ping_connection(state: State<'_, ConnectionState>) -> Result<ConnectionHealth, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let conn = match conn {
        Some(c) => c,
        None => {
            return Ok(ConnectionHealth {
                ok: false,
                latency_ms: 0,
                message: Some("not connected".to_string()),
            });
        }
    };

    let start = Instant::now();
    let result = run_ssh_command(&conn, "echo ok");
    let elapsed = start.elapsed().as_millis() as u64;
    match result {
        Ok(r) if r.exit_code == 0 => Ok(ConnectionHealth {
            ok: true,
            latency_ms: elapsed,
            message: None,
        }),
        Ok(r) => Ok(ConnectionHealth {
            ok: false,
            latency_ms: elapsed,
            message: Some(classify_ssh_error(&r.stderr)),
        }),
        Err(e) => Ok(ConnectionHealth {
            ok: false,
            latency_ms: elapsed,
            message: Some(e),
        }),
    }
}

// ─── Diagnostics ──────────────────────────────────────────────────────────

fn step(name: &str, status: &str, msg: impl Into<String>, dur: Option<u64>) -> DiagnosticStep {
    DiagnosticStep {
        name: name.to_string(),
        status: status.to_string(),
        message: msg.into(),
        duration_ms: dur,
    }
}

/// Run a battery of diagnostic checks against a remote (without storing as active connection).
#[tauri::command]
pub fn diagnose_connection(
    host: String,
    user: String,
    port: u16,
    key_path: String,
    jump_host: Option<String>,
) -> Result<Vec<DiagnosticStep>, String> {
    let conn = make_conn(host, user, port, key_path, jump_host.unwrap_or_default());
    let mut steps: Vec<DiagnosticStep> = Vec::new();

    // 1. SSH reachability
    let t = Instant::now();
    match run_ssh_command(&conn, "echo ok") {
        Ok(r) if r.exit_code == 0 => steps.push(step(
            "SSH connectivity",
            "ok",
            format!("connected to {}@{}", conn.user, conn.host),
            Some(t.elapsed().as_millis() as u64),
        )),
        Ok(r) => {
            steps.push(step(
                "SSH connectivity",
                "fail",
                classify_ssh_error(&r.stderr),
                Some(t.elapsed().as_millis() as u64),
            ));
            // Cannot proceed with later steps.
            return Ok(steps);
        }
        Err(e) => {
            steps.push(step("SSH connectivity", "fail", e, None));
            return Ok(steps);
        }
    }

    // 2. Login shell PATH probe
    let t = Instant::now();
    match run_ssh_command(&conn, "echo \"$PATH\"") {
        Ok(r) if r.exit_code == 0 => steps.push(step(
            "Login shell PATH",
            "ok",
            r.stdout.trim().to_string(),
            Some(t.elapsed().as_millis() as u64),
        )),
        Ok(r) => steps.push(step(
            "Login shell PATH",
            "warn",
            classify_ssh_error(&r.stderr),
            Some(t.elapsed().as_millis() as u64),
        )),
        Err(e) => steps.push(step("Login shell PATH", "warn", e, None)),
    }

    // 3. ezs version
    let t = Instant::now();
    match run_ssh_command(&conn, "ezs --version 2>&1 || ezs -v 2>&1") {
        Ok(r) if r.exit_code == 0 => steps.push(step(
            "ezs binary",
            "ok",
            r.stdout.trim().to_string(),
            Some(t.elapsed().as_millis() as u64),
        )),
        Ok(r) => steps.push(step(
            "ezs binary",
            "fail",
            "ezs not found on remote PATH — install on remote first",
            Some(t.elapsed().as_millis() as u64),
        ).with_stderr(&r.stderr)),
        Err(e) => steps.push(step("ezs binary", "fail", e, None)),
    }

    // 4. git version
    let t = Instant::now();
    match run_ssh_command(&conn, "git --version") {
        Ok(r) if r.exit_code == 0 => steps.push(step(
            "git binary",
            "ok",
            r.stdout.trim().to_string(),
            Some(t.elapsed().as_millis() as u64),
        )),
        Ok(r) => steps.push(step(
            "git binary",
            "fail",
            classify_ssh_error(&r.stderr),
            Some(t.elapsed().as_millis() as u64),
        )),
        Err(e) => steps.push(step("git binary", "fail", e, None)),
    }

    // 5. gh CLI auth (best-effort warn)
    let t = Instant::now();
    match run_ssh_command(&conn, "gh auth status 2>&1") {
        Ok(r) if r.exit_code == 0 => steps.push(step(
            "gh CLI auth",
            "ok",
            "authenticated",
            Some(t.elapsed().as_millis() as u64),
        )),
        Ok(_) => steps.push(step(
            "gh CLI auth",
            "warn",
            "gh CLI not authenticated on remote — PR operations may fail",
            Some(t.elapsed().as_millis() as u64),
        )),
        Err(e) => steps.push(step("gh CLI auth", "warn", e, None)),
    }

    // 6. ezstack config readable
    let t = Instant::now();
    match run_ssh_command(&conn, "test -f ~/.ezstack/config.json && echo present") {
        Ok(r) if r.exit_code == 0 && r.stdout.trim() == "present" => steps.push(step(
            "ezstack config",
            "ok",
            "~/.ezstack/config.json present",
            Some(t.elapsed().as_millis() as u64),
        )),
        _ => steps.push(step(
            "ezstack config",
            "warn",
            "~/.ezstack/config.json missing — repo discovery will fail",
            Some(t.elapsed().as_millis() as u64),
        )),
    }

    Ok(steps)
}

// ─── Saved profiles ───────────────────────────────────────────────────────

#[tauri::command]
pub fn list_connection_profiles() -> Result<Vec<ConnectionProfile>, String> {
    read_profiles()
}

/// Catch the most common foot-gun: typoed or stale key_path that points
/// nowhere. Without this check the profile saves cleanly and the user
/// only sees "permission denied (publickey)" on the first connect, which
/// looks like an authentication problem rather than a missing file.
/// Empty key_path is allowed — ssh-agent / default keys are valid setups.
fn validate_key_path(key_path: &str) -> Result<(), String> {
    if key_path.is_empty() {
        return Ok(());
    }
    let key = std::path::Path::new(key_path);
    match key.metadata() {
        Ok(md) if md.is_file() => Ok(()),
        Ok(_) => Err(format!(
            "SSH key path is not a regular file: {}",
            key_path
        )),
        Err(e) => Err(format!(
            "SSH key path not accessible ({}): {}",
            key_path, e
        )),
    }
}

#[tauri::command]
pub fn save_connection_profile(profile: ConnectionProfile) -> Result<ConnectionProfile, String> {
    let mut profiles = read_profiles().unwrap_or_default();
    let mut profile = profile;
    if profile.name.is_empty() {
        profile.name = format!("{}@{}", profile.user, profile.host);
    }
    if profile.port == 0 {
        profile.port = 22;
    }
    validate_key_path(&profile.key_path)?;
    // Dedupe by content tuple — two saves of the same (host,user,port,key,jump)
    // pair should overwrite each other rather than create duplicates.
    let content_match = profiles.iter().position(|p| {
        p.host == profile.host
            && p.user == profile.user
            && p.port == profile.port
            && p.key_path == profile.key_path
            && p.jump_host == profile.jump_host
    });

    if profile.id.is_empty() {
        if let Some(idx) = content_match {
            // Re-use the existing id so the active profile pointer stays valid.
            profile.id = profiles[idx].id.clone();
            profile.last_repo_path = profiles[idx].last_repo_path.clone();
        } else {
            profile.id = format!("{}-{}", now_unix_ms(), profile.host.replace('.', "_"));
        }
    }

    if let Some(existing) = profiles.iter_mut().find(|p| p.id == profile.id) {
        // Preserve last_repo_path if the caller didn't supply one.
        if profile.last_repo_path.is_empty() {
            profile.last_repo_path = existing.last_repo_path.clone();
        }
        *existing = profile.clone();
    } else if let Some(idx) = content_match {
        // Same content but different id — collapse onto the existing entry.
        if profile.last_repo_path.is_empty() {
            profile.last_repo_path = profiles[idx].last_repo_path.clone();
        }
        profile.id = profiles[idx].id.clone();
        profiles[idx] = profile.clone();
    } else {
        profiles.push(profile.clone());
    }
    write_profiles(&profiles)?;
    Ok(profile)
}

#[tauri::command]
pub fn delete_connection_profile(id: String) -> Result<(), String> {
    let mut profiles = read_profiles().unwrap_or_default();
    let before = profiles.len();
    profiles.retain(|p| p.id != id);
    if profiles.len() == before {
        return Err(format!("No profile with id {}", id));
    }
    write_profiles(&profiles)
}

#[tauri::command]
pub fn update_profile_last_repo(id: String, repo_path: String) -> Result<(), String> {
    let mut profiles = read_profiles().unwrap_or_default();
    let mut found = false;
    for p in profiles.iter_mut() {
        if p.id == id {
            p.last_repo_path = repo_path.clone();
            found = true;
            break;
        }
    }
    if !found {
        return Ok(()); // silently ignore — profile may have been deleted
    }
    write_profiles(&profiles)
}

// ─── Host key fingerprint preview ─────────────────────────────────────────

/// Fetch SHA256 fingerprints for a host's public keys via ssh-keyscan +
/// ssh-keygen. The frontend displays these so a user can verify them
/// against the host's published fingerprint before trusting on first
/// connect (defends against MITM acceptance under StrictHostKeyChecking=accept-new).
#[tauri::command]
pub fn get_host_fingerprint(host: String, port: u16) -> Result<Vec<HostFingerprint>, String> {
    let raw = ssh_host_fingerprints(&host, port)?;
    let mut out = Vec::new();
    for line in raw.lines() {
        // Format: "<bits> SHA256:<hash> <comment> (<keytype>)"
        let parts: Vec<&str> = line.splitn(4, ' ').collect();
        if parts.len() < 4 {
            continue;
        }
        let bits = parts[0].parse::<u32>().unwrap_or(0);
        let fingerprint = parts[1].to_string();
        let key_type = parts[3]
            .trim()
            .trim_start_matches('(')
            .trim_end_matches(')')
            .to_string();
        out.push(HostFingerprint {
            key_type,
            bits,
            fingerprint,
        });
    }
    if out.is_empty() {
        return Err("ssh-keygen produced no parseable fingerprint lines".to_string());
    }
    Ok(out)
}

// ─── DiagnosticStep helper extension ──────────────────────────────────────

const STDERR_PREVIEW_MAX: usize = 200;

trait DiagnosticStepExt {
    fn with_stderr(self, stderr: &str) -> Self;
}

impl DiagnosticStepExt for DiagnosticStep {
    fn with_stderr(mut self, stderr: &str) -> Self {
        let trimmed = stderr.trim();
        if !trimmed.is_empty() {
            // Truncate so a verbose SSH debug log doesn't blow up the UI.
            let preview: String = if trimmed.chars().count() > STDERR_PREVIEW_MAX {
                let head: String = trimmed.chars().take(STDERR_PREVIEW_MAX).collect();
                format!("{}…", head)
            } else {
                trimmed.to_string()
            };
            self.message = format!("{} ({})", self.message, preview);
        }
        self
    }
}

#[cfg(test)]
mod tests {
    use super::validate_key_path;
    use std::io::Write;

    #[test]
    fn validate_key_path_accepts_empty_for_ssh_agent_setups() {
        // Empty key_path is a legitimate config (user relies on ssh-agent or
        // ~/.ssh/id_* defaults). Don't reject it.
        assert!(validate_key_path("").is_ok());
    }

    #[test]
    fn validate_key_path_accepts_existing_regular_file() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("id_test");
        let mut f = std::fs::File::create(&path).expect("create");
        writeln!(f, "fake-key-bytes").unwrap();
        assert!(validate_key_path(path.to_str().unwrap()).is_ok());
    }

    #[test]
    fn validate_key_path_rejects_missing_path() {
        // Use a path inside a temp dir that we don't create — guarantees
        // it doesn't exist and isn't owned by anything else on the system.
        let dir = tempfile::tempdir().expect("tempdir");
        let missing = dir.path().join("does-not-exist");
        let err = validate_key_path(missing.to_str().unwrap()).unwrap_err();
        assert!(
            err.contains("not accessible"),
            "unexpected error message: {}",
            err
        );
    }

    #[test]
    fn validate_key_path_rejects_directory() {
        // A path that exists but is a directory should be rejected — users
        // sometimes paste the parent dir of a key by mistake.
        let dir = tempfile::tempdir().expect("tempdir");
        let err = validate_key_path(dir.path().to_str().unwrap()).unwrap_err();
        assert!(
            err.contains("not a regular file"),
            "unexpected error message: {}",
            err
        );
    }
}
