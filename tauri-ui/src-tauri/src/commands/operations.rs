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
    stats: Option<bool>,
    squash: Option<bool>,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    let mut args: Vec<&str> = vec!["-y", "sync", scope.flag()];
    if stats.unwrap_or(false) {
        args.push("--stats");
    }
    if squash.unwrap_or(false) {
        args.push("--squash");
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

#[tauri::command]
pub fn push_branch(
    state: State<'_, ConnectionState>,
    repo_path: String,
    stack: bool,
    force: bool,
    verify: Option<bool>,
    all_remotes: Option<bool>,
    branch: Option<String>,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    let mut args: Vec<&str> = vec!["-y", "push"];
    if stack {
        args.push("-s");
    }
    if force {
        args.push("-f");
    }
    if verify.unwrap_or(false) {
        args.push("--verify");
    }
    if all_remotes.unwrap_or(false) {
        args.push("--all-remotes");
    }
    // Without -b the CLI pushes whatever git HEAD points at — wrong when
    // the caller (right-pane "Push" button or per-row right-click) targets
    // a specific branch in the UI rather than HEAD. Forward the explicit
    // branch through so what runs matches what the UI promises.
    let branch_value = branch.unwrap_or_default();
    if !branch_value.is_empty() {
        args.push("-b");
        args.push(&branch_value);
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

#[tauri::command]
pub fn delete_branch(
    state: State<'_, ConnectionState>,
    repo_path: String,
    branch: String,
    force: bool,
    cascade: Option<bool>,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    let mut args: Vec<&str> = vec!["-y", "delete"];
    if force {
        args.push("-f");
    }
    if cascade.unwrap_or(false) {
        args.push("--cascade");
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
    no_push: Option<bool>,
    preset: Option<String>,
) -> Result<(), String> {
    let conn = locked_conn(&state)?;
    if conn.is_some() {
        return Err("Agent commands run in an interactive terminal and are not supported over SSH. SSH into the remote and run `ezs agent` there.".to_string());
    }
    let mut args = vec!["agent".to_string()];
    if let Some(ref b) = branch {
        args.push("-b".to_string());
        args.push(b.clone());
    } else if let Some(ref s) = stack_hash {
        args.push("-s".to_string());
        args.push(s.clone());
    }
    if no_push.unwrap_or(false) {
        args.push("--no-push".to_string());
    }
    if let Some(p) = preset {
        if !p.is_empty() {
            args.push("--preset".to_string());
            args.push(p);
        }
    }
    crate::runner::open_in_terminal(&repo_path, &args)
}

#[tauri::command]
pub fn open_agent_feature(
    state: State<'_, ConnectionState>,
    repo_path: String,
    stack_hash: String,
    description: String,
) -> Result<(), String> {
    let conn = locked_conn(&state)?;
    if conn.is_some() {
        return Err("Agent commands run in an interactive terminal and are not supported over SSH. SSH into the remote and run `ezs agent` there.".to_string());
    }
    let args = vec![
        "agent".to_string(),
        "-s".to_string(),
        stack_hash,
        "feature".to_string(),
        description,
    ];
    crate::runner::open_in_terminal(&repo_path, &args)
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

/// Run `ezs doctor` and return the combined diagnostic report. doctor
/// always exits with stdout/stderr we want to surface; non-zero exit just
/// signals problems were found, which is information the caller wants.
#[tauri::command]
pub fn doctor(
    state: State<'_, ConnectionState>,
    repo_path: String,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    run_ezs_auto(conn.as_ref(), &repo_path, &["doctor"])
}

/// Sync strategy override for commit / amend. Mirrors the CLI's
/// `--merge` / `--rebase` flags; `Default` leaves it to the configured
/// `sync_strategy`.
#[derive(Debug, Clone, Copy, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum SyncStrategy {
    #[default]
    Default,
    Merge,
    Rebase,
}

impl SyncStrategy {
    fn flag(self) -> Option<&'static str> {
        match self {
            SyncStrategy::Default => None,
            SyncStrategy::Merge => Some("--merge"),
            SyncStrategy::Rebase => Some("--rebase"),
        }
    }
}

/// Commit and auto-sync child branches.
///
/// `--no-push` is passed unconditionally so the desktop app surface
/// never blocks on the CLI's interactive push prompt — push is its own
/// explicit user action via `push_branch`. `all=true` maps to `git
/// commit -a`; the message is required (no editor launch).
#[tauri::command]
pub fn commit(
    state: State<'_, ConnectionState>,
    repo_path: String,
    message: String,
    all: Option<bool>,
    strategy: Option<SyncStrategy>,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    let mut args: Vec<&str> = vec!["-y", "commit", "-m", &message, "--no-push"];
    if all.unwrap_or(false) {
        args.push("-a");
    }
    if let Some(flag) = strategy.unwrap_or_default().flag() {
        args.push(flag);
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

/// Amend the last commit and auto-sync children.
///
/// When `message` is `None` the existing message is preserved via
/// `--no-edit` (no editor launch — the desktop surface has no place
/// to host one). Like `commit`, push is suppressed; amend rewrites
/// history, so the intended follow-up is `push_branch(force=true)`.
#[tauri::command]
pub fn amend(
    state: State<'_, ConnectionState>,
    repo_path: String,
    message: Option<String>,
    all: Option<bool>,
    strategy: Option<SyncStrategy>,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    let mut args: Vec<String> =
        vec!["-y".into(), "amend".into(), "--no-push".into()];
    match message.as_ref().filter(|m| !m.is_empty()) {
        Some(m) => {
            args.push("-m".into());
            args.push(m.clone());
        }
        None => args.push("--no-edit".into()),
    }
    if all.unwrap_or(false) {
        args.push("-a".into());
    }
    if let Some(flag) = strategy.unwrap_or_default().flag() {
        args.push(flag.into());
    }
    let arg_refs: Vec<&str> = args.iter().map(|s| s.as_str()).collect();
    run_ezs_auto(conn.as_ref(), &repo_path, &arg_refs)
}

/// Output mode for `diff_branch`. `Stat` returns `git diff --stat` text,
/// `Json` returns the file-level numstat as the CLI's `diffOutputJSON`
/// (the frontend can `JSON.parse` it).
#[derive(Debug, Clone, Copy, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum DiffMode {
    #[default]
    Json,
    Stat,
}

/// Diff a branch against its parent in the stack.
///
/// Returns the raw `CommandResult` so the frontend can choose to parse
/// JSON (when `mode` is `Json`) or render the stat text directly. When
/// `branch` is `None` the CLI defaults to the current branch.
#[tauri::command]
pub fn diff_branch(
    state: State<'_, ConnectionState>,
    repo_path: String,
    branch: Option<String>,
    mode: Option<DiffMode>,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    let mut args: Vec<&str> = vec!["diff"];
    args.push(match mode.unwrap_or_default() {
        DiffMode::Json => "--json",
        DiffMode::Stat => "--stat",
    });
    if let Some(ref b) = branch {
        args.push("--branch");
        args.push(b);
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

/// Show commits in `branch` since its parent. Always returns JSON
/// (the CLI's `logOutputJSON`) so the frontend can render structured
/// commit lists.
#[tauri::command]
pub fn log_branch(
    state: State<'_, ConnectionState>,
    repo_path: String,
    branch: Option<String>,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    let mut args: Vec<&str> = vec!["log", "--json"];
    if let Some(ref b) = branch {
        args.push("--branch");
        args.push(b);
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

/// Add a branch to a stack.
///
/// `parent` and `base` are mutually exclusive (the CLI rejects them
/// together). Pass `parent` to attach under an existing tracked branch
/// or `base` to start a new stack rooted on a non-default base
/// (e.g. `develop`, `staging`). Both being `None` triggers the CLI's
/// interactive picker, which — like the rest of this layer — is not a
/// supported configuration over Tauri; callers should resolve the
/// target branch in the frontend before invoking.
#[tauri::command]
pub fn stack_branch(
    state: State<'_, ConnectionState>,
    repo_path: String,
    branch: String,
    parent: Option<String>,
    base: Option<String>,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    let mut args: Vec<&str> = vec!["-y", "stack", "-b", &branch];
    // base wins over parent, matching the precedence in the VS Code
    // wrapper and the MCP tool. The CLI surfaces the conflict as a
    // hard error; resolving it client-side prevents that from
    // bubbling up to Tauri callers that pass both.
    if let Some(ref b) = base {
        args.push("-B");
        args.push(b);
    } else if let Some(ref p) = parent {
        args.push("-p");
        args.push(p);
    }
    run_ezs_auto(conn.as_ref(), &repo_path, &args)
}

/// Remove a branch from ezstack tracking. The git branch and worktree
/// are kept; any children are reparented to the untracked branch's
/// parent. Caller should confirm with the user first — this is a
/// metadata mutation, not a destructive git op, but it does change
/// the shape of the stack.
#[tauri::command]
pub fn unstack_branch(
    state: State<'_, ConnectionState>,
    repo_path: String,
    branch: String,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    run_ezs_auto(conn.as_ref(), &repo_path, &["-y", "unstack", "-b", &branch])
}

/// Run `ezs pr --draft-all` to create draft PRs for every branch in the
/// current stack without one. Confirmation is handled by the caller.
#[tauri::command]
pub fn pr_draft_all(
    state: State<'_, ConnectionState>,
    repo_path: String,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    run_ezs_auto(conn.as_ref(), &repo_path, &["-y", "pr", "--draft-all"])
}

/// Run `ezs goto --search <query>`. Note that goto's success is "shell-eval'd
/// `cd` line printed to stdout"; we surface the result as-is so the frontend
/// can extract the path or fall back to listing matches.
#[tauri::command]
pub fn goto_search(
    state: State<'_, ConnectionState>,
    repo_path: String,
    query: String,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    run_ezs_auto(conn.as_ref(), &repo_path, &["goto", "--search", &query])
}

/// Export the global ezstack config to a user-chosen path. The frontend
/// supplies the path via Tauri's dialog plugin.
#[tauri::command]
pub fn config_export(
    state: State<'_, ConnectionState>,
    repo_path: String,
    file_path: String,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    run_ezs_auto(conn.as_ref(), &repo_path, &["config", "export", &file_path])
}

/// Import a previously-exported config file, replacing the current global
/// config. Confirmation is handled by the caller; -y suppresses the CLI's.
#[tauri::command]
pub fn config_import(
    state: State<'_, ConnectionState>,
    repo_path: String,
    file_path: String,
) -> Result<CommandResult, String> {
    let conn = locked_conn(&state)?;
    run_ezs_auto(conn.as_ref(), &repo_path, &["-y", "config", "import", &file_path])
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
