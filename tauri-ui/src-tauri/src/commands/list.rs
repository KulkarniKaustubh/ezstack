use crate::runner::{run_ezs_auto, run_git_auto};
use crate::types::StatusStack;
use super::connection::ConnectionState;
use serde::Serialize;
use tauri::State;

#[derive(Debug, Serialize, PartialEq, Eq)]
pub struct ReflogEntry {
    pub hash: String,
    pub relative: String,
    pub action: String,
    pub message: String,
}

/// Cap on the number of reflog entries a single call may request. Prevents a
/// pathological frontend call from asking git to enumerate an unbounded log.
const REFLOG_MAX_LIMIT: usize = 200;
const REFLOG_DEFAULT_LIMIT: usize = 20;

#[tauri::command]
pub fn get_stacks_status(state: State<'_, ConnectionState>, repo_path: String) -> Result<Vec<StatusStack>, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let result = run_ezs_auto(conn.as_ref(), &repo_path, &["-y", "status", "--json", "--all"])?;
    if result.exit_code != 0 {
        return Err(format!("ezs status failed (exit {}): {}", result.exit_code, result.stderr));
    }
    let stacks: Vec<StatusStack> = serde_json::from_str(&result.stdout)
        .map_err(|e| format!("Failed to parse ezs output: {e}"))?;
    Ok(stacks)
}

#[tauri::command]
pub fn get_repo_path(start_path: String) -> Result<String, String> {
    let result = crate::runner::run_git(&start_path, &["rev-parse", "--show-toplevel"])?;
    if result.exit_code != 0 {
        return Err("Not a git repository".to_string());
    }
    Ok(result.stdout.trim().to_string())
}

#[tauri::command]
pub fn get_branch_reflog(
    state: State<'_, ConnectionState>,
    repo_path: String,
    branch: String,
    limit: Option<usize>,
) -> Result<Vec<ReflogEntry>, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let max = limit
        .unwrap_or(REFLOG_DEFAULT_LIMIT)
        .clamp(1, REFLOG_MAX_LIMIT);
    let result = run_git_auto(
        conn.as_ref(),
        &repo_path,
        &[
            "reflog",
            "show",
            "--no-decorate",
            "--format=%h%x09%gd%x09%gs",
            &format!("-n{}", max),
            &branch,
        ],
    )?;
    if result.exit_code != 0 {
        // Branch may have no reflog (e.g. just created); return empty list
        // rather than error so the frontend can render "No recent activity".
        return Ok(Vec::new());
    }
    Ok(parse_reflog(&result.stdout))
}

#[tauri::command]
pub fn get_current_branch(state: State<'_, ConnectionState>, repo_path: String) -> Result<String, String> {
    let conn = state.0.lock().map_err(|e| e.to_string())?.clone();
    let result = run_git_auto(conn.as_ref(), &repo_path, &["rev-parse", "--abbrev-ref", "HEAD"])?;
    if result.exit_code != 0 {
        return Err("Could not determine current branch".to_string());
    }
    Ok(result.stdout.trim().to_string())
}

// ─── Pure helpers (testable without a git working tree) ───────────────────

fn is_short_sha(s: &str) -> bool {
    !s.is_empty() && s.len() <= 40 && s.chars().all(|c| c.is_ascii_hexdigit())
}

/// Parse the raw stdout of `git reflog show --format=%h%x09%gd%x09%gs`.
/// Malformed lines (missing fields, non-hex hash) are skipped. Returns the
/// parsed entries in the order git emitted them (newest-first).
pub fn parse_reflog(stdout: &str) -> Vec<ReflogEntry> {
    let mut entries = Vec::new();
    for line in stdout.lines() {
        if line.trim().is_empty() {
            continue;
        }
        let parts: Vec<&str> = line.splitn(3, '\t').collect();
        if parts.len() < 3 {
            continue;
        }
        let hash = parts[0].trim();
        if !is_short_sha(hash) {
            continue;
        }
        let relative = parts[1].trim().to_string();
        if relative.is_empty() {
            continue;
        }
        let message = parts[2].to_string();
        let action = message
            .split(':')
            .next()
            .unwrap_or("")
            .trim()
            .to_string();
        entries.push(ReflogEntry {
            hash: hash.to_string(),
            relative,
            action,
            message,
        });
    }
    entries
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_well_formed_reflog() {
        let stdout = "abc1234\tHEAD@{0}\tcheckout: moving from main to feat/x\n\
                      def5678\tHEAD@{1}\trebase (start): checkout origin/main\n";
        let entries = parse_reflog(stdout);
        assert_eq!(entries.len(), 2);
        assert_eq!(entries[0].hash, "abc1234");
        assert_eq!(entries[0].relative, "HEAD@{0}");
        assert_eq!(entries[0].action, "checkout");
        assert_eq!(
            entries[0].message,
            "checkout: moving from main to feat/x"
        );
        assert_eq!(entries[1].action, "rebase (start)");
    }

    #[test]
    fn skips_blank_and_malformed_lines() {
        let stdout = "\n\
                      garbage-no-tabs\n\
                      abc1234\tHEAD@{0}\tcheckout: x\n\
                      \tHEAD@{1}\tempty hash\n\
                      not_hex!\tHEAD@{2}\tbad hash\n\
                      dead\t\tempty relative\n";
        let entries = parse_reflog(stdout);
        assert_eq!(entries.len(), 1);
        assert_eq!(entries[0].hash, "abc1234");
    }

    #[test]
    fn message_without_colon_uses_whole_message_as_action() {
        let stdout = "abc1234\tHEAD@{0}\tcommit\n";
        let entries = parse_reflog(stdout);
        assert_eq!(entries.len(), 1);
        assert_eq!(entries[0].action, "commit");
        assert_eq!(entries[0].message, "commit");
    }

    #[test]
    fn empty_input_returns_empty_vec() {
        assert!(parse_reflog("").is_empty());
        assert!(parse_reflog("\n\n").is_empty());
    }

    #[test]
    fn is_short_sha_accepts_typical_widths() {
        assert!(is_short_sha("a1b2c3d"));
        assert!(is_short_sha("0123456789abcdef0123456789abcdef01234567"));
        assert!(!is_short_sha(""));
        assert!(!is_short_sha("g123456"));
        assert!(!is_short_sha("0123456789abcdef0123456789abcdef012345678")); // > 40
    }
}
