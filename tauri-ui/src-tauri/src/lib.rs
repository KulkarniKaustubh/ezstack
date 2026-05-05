mod commands;
mod runner;
mod types;

use commands::{config, connection, list, operations, pr};
use connection::{read_active_connection, ConnectionState};
use std::sync::Mutex;

pub fn run() {
    // Hydrate the previously-active SSH connection (if any) before the
    // frontend mounts so getConnection() returns the live session immediately.
    let initial_conn = read_active_connection();

    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(ConnectionState(Mutex::new(initial_conn)))
        .invoke_handler(tauri::generate_handler![
            // Query commands
            list::get_stacks_status,
            list::get_repo_path,
            list::get_current_branch,
            list::get_branch_reflog,
            // Branch operations
            operations::create_branch,
            operations::sync_branch,
            operations::push_branch,
            operations::delete_branch,
            operations::reparent_branch,
            operations::rename_stack,
            operations::open_agent,
            operations::open_agent_feature,
            operations::get_agent_prompts,
            operations::get_agent_prompt_layer,
            operations::reset_agent_prompts,
            operations::edit_agent_prompts,
            // CLI bundle additions
            operations::doctor,
            operations::pr_draft_all,
            operations::goto_search,
            operations::config_export,
            operations::config_import,
            // CLI parity additions — orphan bindings that mirror CLI
            // features the React UI doesn't render yet. Keep them
            // wired so the next UI change set can pick them up
            // without touching Rust again.
            operations::commit,
            operations::amend,
            operations::diff_branch,
            operations::log_branch,
            operations::stack_branch,
            operations::unstack_branch,
            // PR operations
            pr::pr_create,
            pr::pr_update,
            pr::pr_merge,
            pr::pr_toggle_draft,
            pr::pr_update_stack,
            pr::pr_refresh,
            // Config
            config::get_ezstack_repos,
            config::get_config,
            config::set_config,
            // Connection — lifecycle
            connection::connect_remote,
            connection::list_remote_repos,
            connection::select_remote_repo,
            connection::disconnect_remote,
            connection::get_connection,
            connection::test_ssh_connection,
            connection::ping_connection,
            connection::diagnose_connection,
            connection::get_host_fingerprint,
            // Connection — saved profiles
            connection::list_connection_profiles,
            connection::save_connection_profile,
            connection::delete_connection_profile,
            connection::update_profile_last_repo,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
