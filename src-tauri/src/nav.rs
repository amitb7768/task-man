//! Navigation between the two things this window ever shows: the bundled
//! local shell (settings / connection-error UI) and the remote SPA served
//! by the Go server. Same window, two different origins — see
//! docs/MACAPP_PLAN.md "Architecture — two loads in one window".

use std::sync::Mutex;

use tauri::{AppHandle, Manager, Url};

pub const MAIN_WINDOW: &str = "main";

/// The local shell's own origin (e.g. `tauri://localhost/index.html`),
/// captured once at startup — before the window ever navigates away to a
/// remote server — so we can navigate *back* to it later (Settings…, or a
/// failed health-check) without hardcoding a platform-specific scheme.
pub struct ShellOrigin(Mutex<Url>);

/// Must be called once from `setup()`, before any navigation away from the
/// window's initial (shell) URL.
pub fn capture_shell_origin(app: &AppHandle) -> tauri::Result<()> {
    let window = app
        .get_webview_window(MAIN_WINDOW)
        .expect("main window must exist at setup time");
    let origin = window.url()?;
    app.manage(ShellOrigin(Mutex::new(origin)));
    Ok(())
}

/// Navigates the main window to the remote taskman SPA at `server_url`.
pub fn go_to_remote(app: &AppHandle, server_url: &str) -> Result<(), String> {
    let window = app
        .get_webview_window(MAIN_WINDOW)
        .ok_or("main window not found")?;
    let target = format!("{}/", server_url.trim_end_matches('/'));
    let url = Url::parse(&target).map_err(|e| format!("invalid server URL: {e}"))?;
    window.navigate(url).map_err(|e| e.to_string())
}

/// Navigates the main window back to the bundled local shell, in the given
/// screen (`"connecting"`, `"error"`, or `"settings"` — read by the shell's
/// own JS via `location.search`).
pub fn go_to_shell(app: &AppHandle, screen: &str) -> Result<(), String> {
    let window = app
        .get_webview_window(MAIN_WINDOW)
        .ok_or("main window not found")?;
    let state = app.state::<ShellOrigin>();
    let mut url = state
        .0
        .lock()
        .map_err(|_| "shell origin lock poisoned".to_string())?
        .clone();
    url.set_query(Some(&format!("screen={screen}")));
    window.navigate(url).map_err(|e| e.to_string())
}
