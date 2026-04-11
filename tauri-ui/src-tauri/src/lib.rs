mod commands;
mod runner;
mod types;

use commands::{config, connection, list, operations, pr};
use connection::ConnectionState;
use std::sync::Mutex;

pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(ConnectionState(Mutex::new(None)))
        .invoke_handler(tauri::generate_handler![
            // Query commands
            list::get_stacks_status,
            list::get_repo_path,
            list::get_current_branch,
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
            operations::reset_agent_prompts,
            operations::edit_agent_prompts,
            // PR operations
            pr::pr_create,
            pr::pr_update,
            pr::pr_merge,
            pr::pr_toggle_draft,
            pr::pr_update_stack,
            // Config
            config::get_ezstack_repos,
            config::get_config,
            config::set_config,
            // Connection
            connection::connect_remote,
            connection::select_remote_repo,
            connection::disconnect_remote,
            connection::get_connection,
            connection::test_ssh_connection,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
