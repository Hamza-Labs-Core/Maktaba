// Declaring the app's commands makes each one a permission
// (`allow-<command>`), so a capability grants exactly the commands it
// lists. capabilities/default.json (the bundled pages) gets them all;
// capabilities/local-server.json (this Mac's server's web app, Epic 36)
// gets only app_version and the permissions walkthrough's read-only grant
// states, its allow-listed System Settings panes and its "seen" flag —
// never the setup commands.
const COMMANDS: &[&str] = &[
    "app_version",
    "discover_servers",
    "app_state",
    "setup_step",
    "finish_setup",
    "open_login_items",
    "server_status",
    "open_local_server",
    "use_remote_server",
    "open_external",
    "host_permissions",
    "open_privacy_pane",
    "permissions_seen",
];

fn main() {
    tauri_build::try_build(
        tauri_build::Attributes::new()
            .app_manifest(tauri_build::AppManifest::new().commands(COMMANDS)),
    )
    .expect("tauri build script")
}
