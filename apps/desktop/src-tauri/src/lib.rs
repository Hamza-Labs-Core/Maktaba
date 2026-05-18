// Tauri 2 application entry point (library form so platform shims —
// notably mobile — can call run() without re-declaring main).
//
// Wires:
//   - the native menu bar with all custom items + accelerators
//     (Story 13.1 AC-1, Story 13.7) AND an app-level `on_menu_event`
//     handler that emits a `menu` event carrying the clicked item id.
//     The shared web bundle (web/src/lib/desktop.ts) subscribes and
//     routes it (navigate / scan / open-picker / …). Before this, every
//     custom item was a dead no-op — the headline Epic 13 gap.
//   - the system-tray icon + menu (Story 13.4) with Show / Hide / Quit
//     and a window-close interception so closing hides to tray instead
//     of quitting (Story 13.4 AC-5).
//   - window-state persistence (Story 13.1 AC-2) and the auto-update
//     plugin (Story 13.8).
//   - `discover_servers` — passive mDNS browse for `_maktaba._tcp.local.`
//     (Story 13.5) and `app_version` for the Settings→About skew check
//     (Story 13.8), both exposed via the invoke handler.
use std::time::Duration;

use serde::Serialize;
use tauri::{
    menu::{Menu, MenuItem, PredefinedMenuItem, Submenu},
    tray::TrayIconBuilder,
    Emitter, Manager, WindowEvent,
};

#[tauri::command]
fn app_version() -> String {
    env!("CARGO_PKG_VERSION").to_string()
}

#[derive(Debug, Clone, Serialize)]
pub struct DiscoveredServer {
    pub name: String,
    pub host: String,
    pub port: u16,
    pub addresses: Vec<String>,
}

/// Passive mDNS discovery of Maktaba servers on the LAN (Story 13.5).
///
/// Browses `_maktaba._tcp.local.` for `timeout_ms` (default 2s — a
/// short, low-bandwidth passive scan, not a continuous advertiser) and
/// returns the resolved instances. The first-launch wizard / "Switch
/// server" picker calls this via `invoke('discover_servers')`.
#[tauri::command]
async fn discover_servers(timeout_ms: Option<u64>) -> Result<Vec<DiscoveredServer>, String> {
    use mdns_sd::{ServiceDaemon, ServiceEvent};

    let daemon = ServiceDaemon::new().map_err(|e| e.to_string())?;
    let service_type = "_maktaba._tcp.local.";
    let receiver = daemon.browse(service_type).map_err(|e| e.to_string())?;

    let deadline = std::time::Instant::now()
        + Duration::from_millis(timeout_ms.unwrap_or(2000).clamp(250, 10_000));
    let mut found: Vec<DiscoveredServer> = Vec::new();

    while std::time::Instant::now() < deadline {
        let remaining = deadline.saturating_duration_since(std::time::Instant::now());
        match receiver.recv_timeout(remaining) {
            Ok(ServiceEvent::ServiceResolved(info)) => {
                let addresses: Vec<String> =
                    info.get_addresses().iter().map(|a| a.to_string()).collect();
                found.push(DiscoveredServer {
                    name: info.get_fullname().to_string(),
                    host: info.get_hostname().to_string(),
                    port: info.get_port(),
                    addresses,
                });
            }
            Ok(_) => {}
            Err(_) => break, // timeout / channel closed → stop scanning
        }
    }

    // Best-effort shutdown; ignore errors (daemon drops anyway).
    let _ = daemon.shutdown();
    Ok(found)
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_window_state::Builder::default().build())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .setup(|app| {
            // Menu bar (Story 13.1 AC-1 / Story 13.7).
            let handle = app.handle();
            let menu = build_menu(handle)?;
            app.set_menu(menu)?;

            // App-level menu handler (Story 13.1 / 13.7) — emit the
            // clicked item id to the frontend, which owns the routing.
            // Predefined items (quit/close/copy/…) are handled natively
            // and never reach here, so this is purely the custom set.
            app.on_menu_event(|app, event| {
                let id = event.id().as_ref().to_string();
                let _ = app.emit("menu", id);
            });

            // System tray (Story 13.4).
            let tray_show = MenuItem::with_id(handle, "show", "Show Maktaba", true, None::<&str>)?;
            let tray_hide = MenuItem::with_id(handle, "hide", "Hide", true, None::<&str>)?;
            let tray_settings = MenuItem::with_id(
                handle,
                "tray-settings",
                "Settings",
                true,
                None::<&str>,
            )?;
            let tray_quit = MenuItem::with_id(handle, "quit", "Quit", true, None::<&str>)?;
            let tray_menu = Menu::with_items(
                handle,
                &[
                    &tray_show,
                    &tray_hide,
                    &PredefinedMenuItem::separator(handle)?,
                    &tray_settings,
                    &PredefinedMenuItem::separator(handle)?,
                    &tray_quit,
                ],
            )?;
            let _tray = TrayIconBuilder::new()
                .menu(&tray_menu)
                .tooltip("Maktaba")
                .on_menu_event(|app, event| {
                    let id = event.id().as_ref();
                    match id {
                        "show" => {
                            if let Some(w) = app.get_webview_window("main") {
                                let _ = w.show();
                                let _ = w.set_focus();
                            }
                        }
                        "hide" => {
                            if let Some(w) = app.get_webview_window("main") {
                                let _ = w.hide();
                            }
                        }
                        "tray-settings" => {
                            if let Some(w) = app.get_webview_window("main") {
                                let _ = w.show();
                                let _ = w.set_focus();
                            }
                            // Reuse the same routing path as the menu.
                            let _ = app.emit("menu", "preferences");
                        }
                        "quit" => {
                            app.exit(0);
                        }
                        _ => {}
                    }
                })
                .build(handle)?;

            // Close-to-tray (Story 13.4 AC-5): closing the window hides
            // it instead of terminating the app; Quit (tray/menu) still
            // exits. Without this, closing the window quit the app,
            // contradicting the AC.
            if let Some(main) = app.get_webview_window("main") {
                let win = main.clone();
                main.on_window_event(move |event| {
                    if let WindowEvent::CloseRequested { api, .. } = event {
                        api.prevent_close();
                        let _ = win.hide();
                    }
                });
            }

            Ok(())
        })
        .invoke_handler(tauri::generate_handler![app_version, discover_servers])
        .run(tauri::generate_context!())
        .expect("error while running maktaba desktop");
}

fn build_menu(app: &tauri::AppHandle) -> tauri::Result<Menu<tauri::Wry>> {
    // Maktaba menu (macOS: appears as the app name; Win/Linux: leftmost)
    let about = PredefinedMenuItem::about(app, Some("About Maktaba"), None)?;
    let prefs = MenuItem::with_id(app, "preferences", "Settings…", true, Some("CmdOrCtrl+,"))?;
    let switch_server = MenuItem::with_id(
        app,
        "switch-server",
        "Switch Server…",
        true,
        None::<&str>,
    )?;
    let quit = PredefinedMenuItem::quit(app, None)?;
    let app_menu = Submenu::with_items(
        app,
        "Maktaba",
        true,
        &[
            &about,
            &PredefinedMenuItem::separator(app)?,
            &prefs,
            &switch_server,
            &PredefinedMenuItem::separator(app)?,
            &quit,
        ],
    )?;

    // File menu — New Window (Cmd+N) + New Private Window (Cmd+Shift+N).
    let new_window = MenuItem::with_id(app, "new-window", "New Window", true, Some("CmdOrCtrl+N"))?;
    let new_private = MenuItem::with_id(
        app,
        "new-private",
        "New Private Window",
        true,
        Some("CmdOrCtrl+Shift+N"),
    )?;
    let close = PredefinedMenuItem::close_window(app, None)?;
    let file_menu = Submenu::with_items(app, "File", true, &[&new_window, &new_private, &close])?;

    // Edit menu
    let edit_menu = Submenu::with_items(
        app,
        "Edit",
        true,
        &[
            &PredefinedMenuItem::undo(app, None)?,
            &PredefinedMenuItem::redo(app, None)?,
            &PredefinedMenuItem::separator(app)?,
            &PredefinedMenuItem::cut(app, None)?,
            &PredefinedMenuItem::copy(app, None)?,
            &PredefinedMenuItem::paste(app, None)?,
            &PredefinedMenuItem::select_all(app, None)?,
        ],
    )?;

    // View menu — Find/Focus Search (Cmd+F) + fullscreen.
    let focus_search = MenuItem::with_id(
        app,
        "focus-search",
        "Find / Search…",
        true,
        Some("CmdOrCtrl+F"),
    )?;
    let view_menu = Submenu::with_items(
        app,
        "View",
        true,
        &[&focus_search, &PredefinedMenuItem::fullscreen(app, None)?],
    )?;

    // Library menu — Scan (Cmd+R) + Cmd+1..9 library-slot switching
    // (Story 13.7).
    let scan = MenuItem::with_id(app, "scan-library", "Scan Library", true, Some("CmdOrCtrl+R"))?;
    let mut lib_items: Vec<MenuItem<tauri::Wry>> = Vec::with_capacity(9);
    for slot in 1..=9u8 {
        lib_items.push(MenuItem::with_id(
            app,
            format!("library-{slot}"),
            format!("Library {slot}"),
            true,
            Some(format!("CmdOrCtrl+{slot}")),
        )?);
    }
    // Submenu::with_items wants &[&dyn IsMenuItem]; build the slice.
    let mut lib_refs: Vec<&dyn tauri::menu::IsMenuItem<tauri::Wry>> = Vec::new();
    lib_refs.push(&scan);
    let sep = PredefinedMenuItem::separator(app)?;
    lib_refs.push(&sep);
    for it in &lib_items {
        lib_refs.push(it);
    }
    let lib_menu = Submenu::with_items(app, "Library", true, &lib_refs)?;

    // Window menu (macOS gets the system one via predefined items)
    let window_menu = Submenu::with_items(
        app,
        "Window",
        true,
        &[
            &PredefinedMenuItem::minimize(app, None)?,
            &PredefinedMenuItem::maximize(app, None)?,
        ],
    )?;

    // Help menu
    let docs = MenuItem::with_id(app, "open-docs", "Documentation", true, None::<&str>)?;
    let help_menu = Submenu::with_items(app, "Help", true, &[&docs])?;

    Menu::with_items(
        app,
        &[
            &app_menu,
            &file_menu,
            &edit_menu,
            &view_menu,
            &lib_menu,
            &window_menu,
            &help_menu,
        ],
    )
}
