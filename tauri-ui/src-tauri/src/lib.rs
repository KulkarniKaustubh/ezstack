mod commands;
mod runner;
mod types;

use commands::{config, list, operations, pr};

pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
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
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
