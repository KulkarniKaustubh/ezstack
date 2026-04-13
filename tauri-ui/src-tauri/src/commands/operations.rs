use crate::runner::run_ezs_auto;
use crate::types::CommandResult;
use super::connection::ConnectionState;
use serde::{Deserialize, Serialize};
use tauri::State;

/// Scope for `ezs sync`. Strongly typed via serde rather than magic strings.
#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum SyncScope {
    Current,
    Stack,
    All,
}

impl SyncScope {
    fn flag(self) -> &'static str {
        match self {
            SyncScope::Current => "-c",
            SyncScope::Stack => "-s",
            SyncScope::All => "-a",
        }
    }
}

fn locked_conn(state: &State<'_, ConnectionState>) -> Result<Option<crate::types::SshConnection>, String> {
    Ok(state.0.lock().map_err(|e| e.to_string())?.clone())
}

#[tauri::command]
pub fn create_branch(
    state: State<'_, ConnectionState>,
    repo_path: String,
    name: String,
    parent: Option<String>,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    let mut args = vec!["-y", "new", &name];
    let parent_val;
    if let Some(ref p) = parent {
        parent_val = p.clone();
        args.push("-p");
        args.push(&parent_val);
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

#[tauri::command]
pub fn sync_branch(
    state: State<'_, ConnectionState>,
    repo_path: String,
    scope: SyncScope,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    let args = vec!["-y", "sync", scope.flag()];
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

#[tauri::command]
pub fn push_branch(
    state: State<'_, ConnectionState>,
    repo_path: String,
    stack: bool,
    force: bool,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    let mut args = vec!["-y", "push"];
    if stack {
        args.push("-s");
    }
    if force {
        args.push("-f");
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

#[tauri::command]
pub fn delete_branch(
    state: State<'_, ConnectionState>,
    repo_path: String,
    branch: String,
    force: bool,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    let mut args = vec!["-y", "delete"];
    if force {
        args.push("-f");
    }
    args.push(&branch);
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

#[tauri::command]
pub fn reparent_branch(
    state: State<'_, ConnectionState>,
    repo_path: String,
    branch: String,
    new_parent: String,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    run_ezs_auto(conn.as_ref(), &repo_path, &["-y", "reparent", &branch, &new_parent])
}

#[tauri::command]
pub fn rename_stack(
    state: State<'_, ConnectionState>,
    repo_path: String,
    stack_hash: String,
    name: String,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    run_ezs_auto(conn.as_ref(), &repo_path, &["-y", "stack", "rename", &stack_hash, &name])
}

#[tauri::command]
pub fn open_agent(
    state: State<'_, ConnectionState>,
    repo_path: String,
    stack_hash: Option<String>,
    branch: Option<String>,
) -> Result<(), String> {
    let conn = locked_conn(&state)?;
    let mut args = vec!["agent".to_string()];
    if let Some(ref b) = branch {
        args.push("-b".to_string());
        args.push(b.clone());
    } else if let Some(ref s) = stack_hash {
        args.push("-s".to_string());
        args.push(s.clone());
    }
    match conn {
        Some(c) => crate::runner::open_in_remote_terminal(&c, &repo_path, &args),
        None => crate::runner::open_in_terminal(&repo_path, &args),
    }
}

#[tauri::command]
pub fn open_agent_feature(
    state: State<'_, ConnectionState>,
    repo_path: String,
    stack_hash: String,
    description: String,
) -> Result<(), String> {
    let conn = locked_conn(&state)?;
    let args = vec![
        "agent".to_string(),
        "-s".to_string(),
        stack_hash,
        "feature".to_string(),
        description,
    ];
    match conn {
        Some(c) => crate::runner::open_in_remote_terminal(&c, &repo_path, &args),
        None => crate::runner::open_in_terminal(&repo_path, &args),
    }
}

/// Get shipped agent prompts for both work and feature modes.
/// Returns stdout with ANSI codes stripped.
///
/// Routes through the active SSH connection when one is set, so users on
/// remote repos can still inspect prompts.
#[tauri::command]
pub fn get_agent_prompts(
    state: State<'_, ConnectionState>,
    repo_path: String,
) -> Result<String, String> {
    let conn = locked_conn(&state)?;
    let mut output = String::new();
    for prompt_type in &["work", "feature"] {
        let result = run_ezs_auto(
            conn.as_ref(),
            &repo_path,
            &["agent", "prompt", "--shipped", prompt_type],
        )?;
        if result.exit_code != 0 {
            return Err(if result.stderr.trim().is_empty() {
                format!("ezs exited {}", result.exit_code)
            } else {
                result.stderr
            });
        }
        if !output.is_empty() {
            output.push_str("\n\n");
        }
        output.push_str(&format!("═══ Shipped {} prompt ═══\n\n", prompt_type));
        output.push_str(&strip_ansi(&result.stdout));
    }
    Ok(output)
}

/// Get a specific agent prompt layer.
/// `layer` is "shipped", "custom", or "repo".
/// `prompt_type` is "work" or "feature".
#[tauri::command]
pub fn get_agent_prompt_layer(
    state: State<'_, ConnectionState>,
    repo_path: String,
    layer: String,
    prompt_type: String,
) -> Result<String, String> {
    let conn = locked_conn(&state)?;
    let flag = match layer.as_str() {
        "shipped" => "--shipped",
        "custom" => "--custom",
        "repo" => "--repo",
        _ => return Err(format!("Invalid layer: {}", layer)),
    };
    let result = run_ezs_auto(
        conn.as_ref(),
        &repo_path,
        &["agent", "prompt", flag, &prompt_type],
    )?;
    if result.exit_code != 0 {
        return Err(if result.stderr.trim().is_empty() {
            format!("ezs exited {}", result.exit_code)
        } else {
            result.stderr
        });
    }
    Ok(strip_ansi(&result.stdout))
}

/// Reset agent prompt(s) to built-in defaults.
/// `which` can be "work", "feature", or "both".
/// `repo` indicates whether to reset repo-specific instructions.
#[tauri::command]
pub fn reset_agent_prompts(
    state: State<'_, ConnectionState>,
    repo_path: String,
    which: String,
    repo: bool,
) -> Result<String, String> {
    let conn = locked_conn(&state)?;
    let types: Vec<&str> = match which.as_str() {
        "work" => vec!["work"],
        "feature" => vec!["feature"],
        _ => vec!["work", "feature"], // "both"
    };
    let mut output = String::new();
    for pt in types {
        let mut args = vec!["agent", "prompt", "--reset"];
        if repo {
            args.push("--repo");
        }
        args.push(pt);
        let result = run_ezs_auto(conn.as_ref(), &repo_path, &args)?;
        if result.exit_code != 0 {
            return Err(if result.stderr.trim().is_empty() {
                format!("ezs exited {}", result.exit_code)
            } else {
                result.stderr
            });
        }
        if !output.is_empty() {
            output.push('\n');
        }
        output.push_str(&strip_ansi(&result.stdout));
    }
    Ok(output)
}

/// Open agent prompt editor in an external terminal.
/// `which` is "work" or "feature".
/// `repo` indicates whether to edit repo-specific instructions.
///
/// This command opens a local terminal and is therefore unavailable over SSH.
#[tauri::command]
pub fn edit_agent_prompts(
    state: State<'_, ConnectionState>,
    repo_path: String,
    which: String,
    repo: bool,
) -> Result<(), String> {
    let conn = locked_conn(&state)?;
    if conn.is_some() {
        return Err("Editing prompts opens an interactive editor and isn't supported over SSH. SSH in and run `ezs agent prompt --edit` directly.".to_string());
    }
    let prompt_type = match which.as_str() {
        "work" | "feature" => which.clone(),
        _ => "work".to_string(),
    };
    let mut args = vec!["agent".to_string(), "prompt".to_string(), "--edit".to_string()];
    if repo {
        args.push("--repo".to_string());
    }
    args.push(prompt_type);
    crate::runner::open_in_terminal(&repo_path, &args)
}

/// Strip ANSI escape codes from a string.
///
/// UTF-8 safe: operates on `chars()` rather than raw bytes so multi-byte
/// codepoints survive intact.
fn strip_ansi(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut chars = s.chars().peekable();
    while let Some(c) = chars.next() {
        // Recognize CSI: ESC '['
        if c == '\u{1b}' {
            if let Some(&next) = chars.peek() {
                if next == '[' {
                    chars.next(); // consume '['
                    // Consume parameter / intermediate bytes until a final byte (0x40..=0x7e).
                    while let Some(&p) = chars.peek() {
                        chars.next();
                        if ('\u{40}'..='\u{7e}').contains(&p) {
                            break;
                        }
                    }
                    continue;
                }
                // OSC: ESC ']' ... BEL or ST
                if next == ']' {
                    chars.next();
                    while let Some(&p) = chars.peek() {
                        chars.next();
                        if p == '\u{07}' {
                            break;
                        }
                        if p == '\u{1b}' {
                            // ST: ESC \
                            if let Some(&q) = chars.peek() {
                                if q == '\\' {
                                    chars.next();
                                    break;
                                }
                            }
                        }
                    }
                    continue;
                }
                // Two-byte escapes (e.g. ESC =, ESC >).
                chars.next();
                continue;
            }
            continue;
        }
        out.push(c);
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn strip_ansi_keeps_unicode() {
        let s = "═══ \u{1b}[1mhello\u{1b}[0m 你好 ═══";
        assert_eq!(strip_ansi(s), "═══ hello 你好 ═══");
    }
}
