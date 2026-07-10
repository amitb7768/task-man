//! Commands callable from the local shell (see capabilities/default.json —
//! this capability is scoped to the local window origin only, so the
//! *remote* SPA served by the Go server can never reach these, even though
//! it shares the same window).

use tauri::{AppHandle, Url};

use crate::config::{self, ServerConfig};
use crate::health;
use crate::nav;

#[tauri::command]
pub fn get_server_url(app: AppHandle) -> String {
    config::load(&app).server_url
}

#[tauri::command]
pub fn set_server_url(app: AppHandle, url: String) -> Result<(), String> {
    let normalized = normalize_server_url(&url)?;
    config::save(
        &app,
        &ServerConfig {
            server_url: normalized.clone(),
        },
    )?;
    check_and_go(&app, &normalized)
}

/// Re-checks the currently configured server and either navigates to it or
/// reports the failure. Doubles as the shell's initial "connecting" check
/// (`check_and_go` in docs/MACAPP_PLAN.md) and the Retry button's handler —
/// both are the same operation.
#[tauri::command]
pub fn retry(app: AppHandle) -> Result<(), String> {
    let cfg = config::load(&app);
    check_and_go(&app, &cfg.server_url)
}

#[tauri::command]
pub fn open_settings(app: AppHandle) -> Result<(), String> {
    nav::go_to_shell(&app, "settings")
}

fn check_and_go(app: &AppHandle, server_url: &str) -> Result<(), String> {
    // Re-validate the scheme even on the boot/retry path — the stored value
    // comes straight off disk, so only http/https may ever be navigated to,
    // never javascript:/file:/data:/tauri:. Deliberate defense-in-depth rather
    // than relying on is_reachable's host check to incidentally block them.
    let server_url = normalize_server_url(server_url)?;
    if health::is_reachable(&server_url) {
        nav::go_to_remote(app, &server_url)
    } else {
        Err(format!("Can't reach taskman at {server_url}"))
    }
}

/// Validates scheme (http/https) + host[:port], strips any path/query, and
/// returns the normalized `scheme://host[:port]` form to persist.
fn normalize_server_url(input: &str) -> Result<String, String> {
    let trimmed = input.trim();
    if trimmed.is_empty() {
        return Err("Enter a server URL".to_string());
    }
    let url = Url::parse(trimmed)
        .map_err(|_| "Enter a full URL, e.g. http://localhost:8484".to_string())?;

    match url.scheme() {
        "http" | "https" => {}
        other => return Err(format!("Unsupported scheme '{other}' — use http or https")),
    }

    let host = url
        .host_str()
        .ok_or_else(|| "URL must include a host".to_string())?;

    let mut normalized = format!("{}://{host}", url.scheme());
    if let Some(port) = url.port() {
        normalized.push_str(&format!(":{port}"));
    }
    Ok(normalized)
}
