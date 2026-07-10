// taskman desktop shell — a thin Tauri v2 wrapper. The window loads a
// bundled local shell first (settings / connection-error UI), which then
// navigates the SAME window to the remote SPA served by the Go server once
// it's reachable. See docs/MACAPP_PLAN.md for the full contract.
// macOS-only (see non-goals) — no Windows subsystem attribute needed.

mod commands;
mod config;
mod health;
mod menu;
mod nav;

use tauri::Manager;

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_window_state::Builder::default().build())
        .invoke_handler(tauri::generate_handler![
            commands::get_server_url,
            commands::set_server_url,
            commands::retry,
            commands::open_settings,
        ])
        .setup(|app| {
            nav::capture_shell_origin(app.handle())?;

            let app_menu = menu::build(app.handle())?;
            app.set_menu(app_menu)?;

            Ok(())
        })
        .on_menu_event(|app, event| match event.id().as_ref() {
            menu::SETTINGS_ITEM_ID => {
                let _ = nav::go_to_shell(app, "settings");
            }
            menu::RELOAD_ITEM_ID => {
                if let Some(window) = app.get_webview_window(nav::MAIN_WINDOW) {
                    let _ = window.eval("location.reload()");
                }
            }
            _ => {}
        })
        .run(tauri::generate_context!())
        .expect("error while running taskman");
}
