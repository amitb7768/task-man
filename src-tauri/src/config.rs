//! Persisted app config: just the server URL, as a plain JSON file in the
//! OS app-config directory. No store plugin needed for one field.

use std::fs;
use std::path::PathBuf;

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Manager};

/// Default server the shell tries before the user ever opens Settings.
pub const DEFAULT_SERVER_URL: &str = "http://localhost:8484";

const CONFIG_FILE_NAME: &str = "config.json";

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ServerConfig {
    #[serde(default = "default_server_url")]
    pub server_url: String,
}

fn default_server_url() -> String {
    DEFAULT_SERVER_URL.to_string()
}

impl Default for ServerConfig {
    fn default() -> Self {
        Self {
            server_url: DEFAULT_SERVER_URL.to_string(),
        }
    }
}

fn config_path(app: &AppHandle) -> Result<PathBuf, String> {
    let dir = app
        .path()
        .app_config_dir()
        .map_err(|e| format!("could not resolve app config dir: {e}"))?;
    fs::create_dir_all(&dir).map_err(|e| format!("could not create app config dir: {e}"))?;
    Ok(dir.join(CONFIG_FILE_NAME))
}

/// Loads the persisted config, falling back to defaults if the file doesn't
/// exist yet or fails to parse (never a hard error for the caller).
pub fn load(app: &AppHandle) -> ServerConfig {
    let path = match config_path(app) {
        Ok(p) => p,
        Err(_) => return ServerConfig::default(),
    };
    let Ok(raw) = fs::read_to_string(&path) else {
        return ServerConfig::default();
    };
    serde_json::from_str(&raw).unwrap_or_default()
}

pub fn save(app: &AppHandle, cfg: &ServerConfig) -> Result<(), String> {
    let path = config_path(app)?;
    let raw =
        serde_json::to_string_pretty(cfg).map_err(|e| format!("could not serialize config: {e}"))?;
    fs::write(&path, raw).map_err(|e| format!("could not write config: {e}"))
}
