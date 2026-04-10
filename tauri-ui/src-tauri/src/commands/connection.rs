use crate::runner::run_ssh_command;
use crate::types::SshConnection;
use std::sync::Mutex;
use tauri::State;

pub struct ConnectionState(pub Mutex<Option<SshConnection>>);

#[tauri::command]
pub fn connect_remote(
    state: State<'_, ConnectionState>,
    host: String,
    user: String,
    port: u16,
    key_path: String,
    remote_repo_path: String,
) -> Result<String, String> {
    let conn = SshConnection {
        host,
        user,
        port,
        key_path,
        remote_repo_path: remote_repo_path.clone(),
    };

    // Test SSH connectivity
    let test = run_ssh_command(&conn, "echo ok")?;
    if test.exit_code != 0 {
        return Err(format!(
            "SSH connection failed: {}",
            test.stderr.trim()
        ));
    }

    // Verify the repo path exists and is a git repo
    let repo_check = run_ssh_command(
        &conn,
        &format!(
            "cd '{}' && git rev-parse --show-toplevel",
            conn.remote_repo_path.replace('\'', "'\\''")
        ),
    )?;
    if repo_check.exit_code != 0 {
        return Err(format!(
            "Not a git repository on remote: {}",
            repo_check.stderr.trim()
        ));
    }

    // Verify ezs is available
    let ezs_check = run_ssh_command(&conn, "command -v ezs")?;
    if ezs_check.exit_code != 0 {
        return Err("ezs is not installed or not on PATH on the remote machine".to_string());
    }

    let resolved_path = repo_check.stdout.trim().to_string();

    // Store the connection with the resolved repo path
    let mut state = state.0.lock().map_err(|e| e.to_string())?;
    *state = Some(SshConnection {
        remote_repo_path: resolved_path.clone(),
        ..conn
    });

    Ok(resolved_path)
}

#[tauri::command]
pub fn disconnect_remote(state: State<'_, ConnectionState>) -> Result<(), String> {
    let mut state = state.0.lock().map_err(|e| e.to_string())?;
    *state = None;
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
) -> Result<String, String> {
    let conn = SshConnection {
        host,
        user,
        port,
        key_path,
        remote_repo_path: String::new(),
    };

    let test = run_ssh_command(&conn, "echo ok")?;
    if test.exit_code != 0 {
        return Err(format!(
            "SSH connection failed: {}",
            test.stderr.trim()
        ));
    }

    Ok("Connection successful".to_string())
}
