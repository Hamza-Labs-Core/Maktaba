// Declaring the app's commands makes each one a permission
// (`allow-<command>`), so a capability grants exactly the commands it
// lists. capabilities/default.json (the bundled pages) gets them all;
// capabilities/local-server.json (this Mac's server's web app, Epic 36)
// gets only app_version and the permissions walkthrough's read-only grant
// states, its allow-listed System Settings panes and its "seen" flag, and
// big screen's full screen and start setting (Epic 46), and the window's
// frame (story 13.10) — never the setup commands.
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
    // Epic 46: big screen (bigscreen.rs): the window full screen and the start setting
    "big_screen_state",
    "set_big_screen",
    "set_big_screen_start",
    // Epic 53 story 53.14: the shell's own crashes, sent by the web app when crash reports
    // are on (crashes.rs)
    "shell_crashes",
    "shell_crashes_sent",
    // Epic 13 story 13.10: Maktaba's own window frame (frame.rs): the page claims it, works
    // its caption buttons and Snap Layouts, and lists and chooses the menu bar's items
    "frame_claim",
    "frame_action",
    "frame_menu_items",
    "frame_menu",
    // Epic 13 story 13.13: the status window (ownwindows.rs), granted to its own window only
    // (capabilities/status-window.json)
    "status_window_state",
    "status_window_action",
    // Epic 13 story 13.12: the tray's panel (traypanel.rs), granted to its own window only
    // (capabilities/tray-panel.json)
    "tray_panel_state",
    "tray_panel_choose",
    "tray_panel_hide",
    // Epic 13 story 13.11: Maktaba's own dialog for the shell's errors (shelldialog.rs), granted
    // to its own window only (capabilities/shell-dialog.json)
    "shell_dialog_content",
    "shell_dialog_choose",
];

fn main() {
    tauri_build::try_build(
        tauri_build::Attributes::new()
            .app_manifest(tauri_build::AppManifest::new().commands(COMMANDS)),
    )
    .expect("tauri build script")
}
