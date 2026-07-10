//! Native app menu: Settings… + Reload live under the app's own submenu, plus
//! standard Quit and a standard Edit submenu (so Cmd+C/V/X/A/Z work in the
//! shell's URL input). Works independent of whatever the webview currently
//! shows — that's the point (docs/MACAPP_PLAN.md architecture step 3).

use tauri::{
    menu::{Menu, MenuItem, PredefinedMenuItem, SubmenuBuilder},
    AppHandle, Runtime,
};

pub const SETTINGS_ITEM_ID: &str = "settings";
pub const RELOAD_ITEM_ID: &str = "reload";

pub fn build<R: Runtime>(app: &AppHandle<R>) -> tauri::Result<Menu<R>> {
    let settings = MenuItem::with_id(app, SETTINGS_ITEM_ID, "Settings…", true, Some("Cmd+,"))?;
    let reload = MenuItem::with_id(app, RELOAD_ITEM_ID, "Reload", true, Some("Cmd+R"))?;
    let quit = PredefinedMenuItem::quit(app, None)?;

    let app_menu = SubmenuBuilder::new(app, "taskman")
        .item(&settings)
        .separator()
        .item(&reload)
        .separator()
        .item(&quit)
        .build()?;

    let edit_menu = SubmenuBuilder::new(app, "Edit")
        .undo()
        .redo()
        .separator()
        .cut()
        .copy()
        .paste()
        .select_all()
        .build()?;

    Menu::with_items(app, &[&app_menu, &edit_menu])
}
