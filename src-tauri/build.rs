fn main() {
    // Autogenerate ACL permissions (`allow-<command>` / `deny-<command>`) for
    // our own app-level commands, so they can be granted to the local shell
    // capability by name in capabilities/default.json — same mechanism a
    // real Tauri plugin would use, just inlined for the app itself.
    tauri_build::try_build(
        tauri_build::Attributes::new().app_manifest(
            tauri_build::AppManifest::new().commands(&[
                "get_server_url",
                "set_server_url",
                "retry",
                "open_settings",
            ]),
        ),
    )
    .expect("failed to run tauri-build");
}
