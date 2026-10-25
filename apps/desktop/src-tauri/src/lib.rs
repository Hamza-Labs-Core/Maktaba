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
//   - window-state persistence (Story 13.1 AC-2), the auto-update
//     plugin (Story 13.8) and the notification plugin (Epic 35: a release
//     the home ordered is ready to install here).
//   - `discover_servers` — passive mDNS browse for `_maktaba._tcp.local.`
//     (Story 13.5) and `app_version` for the Settings→About skew check
//     (Story 13.8), both exposed via the invoke handler.
//   - Epic 36: the menu-bar item with the server's status, the server
//     login item and the first-run setup commands (shell.rs), and the
//     `--server-agent` mode the LaunchAgent runs (agent.rs).
use std::time::Duration;

use serde::Serialize;
use tauri::{
    menu::{Menu, MenuItem, PredefinedMenuItem, Submenu},
    Emitter, Manager, WindowEvent,
};
use tauri_plugin_deep_link::DeepLinkExt;

mod agent;
mod attempts;
mod autoinstall;
mod lang;
mod mode;
mod setup;
mod shell;
mod status;
mod tray;

use shell::focus_main;

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
    // The LaunchAgent (macOS) or the sign-in task (Windows) runs this
    // executable as `Maktaba --server-agent`: become the server's agent
    // instead of opening a window. The Windows installer's hooks run it with
    // --server-start / --server-stop / --server-remove.
    match std::env::args().nth(1).as_deref() {
        Some(agent::AGENT_FLAG) => match agent::run_server_agent() {
            Ok(code) => std::process::exit(code),
            Err(e) => {
                eprintln!("maktaba --server-agent: {e}");
                std::process::exit(1);
            }
        },
        #[cfg(windows)]
        Some(flag @ (agent::START_FLAG | agent::STOP_FLAG | agent::REMOVE_FLAG)) => {
            std::process::exit(agent::installer_hook(flag))
        }
        _ => {}
    }

    let mut builder = tauri::Builder::default();

    // single-instance is desktop-only and MUST be the first plugin
    // registered (Tauri requirement). On a second launch — e.g. the OS
    // spawning a new process to deliver a `maktaba://` URL on
    // Windows/Linux — this focuses the already-running window instead of
    // opening a new one; the `deep-link` feature forwards the URL to
    // on_open_url.
    #[cfg(desktop)]
    {
        builder = builder.plugin(tauri_plugin_single_instance::init(|app, argv, _cwd| {
            // the server asking for an app update it ordered (Epic 35): no window
            if argv.iter().any(|a| a.starts_with(autoinstall::INSTALL_URL)) {
                shell::install_requested(app);
                return;
            }
            focus_main(app);
        }));
    }

    builder
        .plugin(tauri_plugin_deep_link::init())
        .plugin(tauri_plugin_window_state::Builder::default().build())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        // the OS notification of a release the home ordered, ready here (shell.rs)
        .plugin(tauri_plugin_notification::init())
        .setup(|app| {
            // Deep links (`maktaba://…`, Story 13.x). At runtime we
            // register the scheme (no-op when the bundle already owns it
            // via tauri.conf.json; required for `cargo tauri dev` on
            // Linux) and forward every opened URL to the frontend as a
            // `deep-link` event. The shared web shell maps it to an SPA
            // route via deepLinkToPath() (web/src/lib/native.ts), reusing
            // the exact same parser the Capacitor shell uses.
            {
                let handle = app.handle().clone();
                // Runtime scheme registration is a desktop concern (on
                // mobile the scheme is declared in the manifest). Best-
                // effort: dev builds may lack registration rights.
                #[cfg(desktop)]
                let _ = app.deep_link().register_all();
                app.deep_link().on_open_url(move |event| {
                    let urls: Vec<String> = event.urls().iter().map(|u| u.to_string()).collect();
                    if urls.iter().any(|u| u.starts_with(autoinstall::INSTALL_URL)) {
                        shell::install_requested(&handle);
                        return;
                    }
                    if !urls.is_empty() {
                        focus_main(&handle);
                        let _ = handle.emit("deep-link", urls);
                    }
                });
            }

            // Linux (Epic 39): without a StatusNotifier host there is no tray
            // to come back from. Closing the window then quits the app (the
            // server is a systemd unit and keeps running), and the window's
            // menu carries the tray's server items.
            #[cfg(target_os = "linux")]
            let keep_in_tray = agent::linux::tray_available();
            #[cfg(not(target_os = "linux"))]
            let keep_in_tray = true;

            // Menu bar (Story 13.1 AC-1 / Story 13.7).
            let handle = app.handle();
            let menu = build_menu(handle, !keep_in_tray)?;
            app.set_menu(menu)?;

            // App-level menu handler (Story 13.1 / 13.7) — emit the
            // clicked item id to the frontend, which owns the routing.
            // Predefined items (quit/close/copy/…) are handled natively
            // and never reach here, so this is purely the custom set. The
            // window's Server items (Linux without a tray) act here.
            app.on_menu_event(|app, event| {
                let id = event.id().as_ref().to_string();
                #[cfg(target_os = "linux")]
                if let Some(tray_id) = id.strip_prefix(tray::WINDOW_PREFIX) {
                    shell::on_window_menu(app, tray_id);
                    return;
                }
                let _ = app.emit("menu", id);
            });

            // System tray / menu-bar item (Story 13.4, Epic 36 story
            // 36.5): server status, Open Maktaba, Pause/Resume server,
            // Open logs, the player's Play / Pause, Settings, Quit (the
            // window only) and Stop server and quit. See shell.rs/tray.rs.
            shell::init(handle)?;

            // Close-to-tray (Story 13.4 AC-5): closing the window hides
            // it instead of terminating the app; Quit (tray/menu) still
            // exits. In server mode the server keeps running either way.
            // Linux: started by the tray's autostart entry (`--hidden`), the
            // window stays hidden; without a tray, closing it quits.
            if let Some(main) = app.get_webview_window("main") {
                #[cfg(target_os = "linux")]
                if keep_in_tray && std::env::args().any(|a| a == agent::linux::HIDDEN_FLAG) {
                    let _ = main.hide();
                }
                let win = main.clone();
                main.on_window_event(move |event| {
                    if let WindowEvent::CloseRequested { api, .. } = event {
                        if keep_in_tray {
                            api.prevent_close();
                            let _ = win.hide();
                        }
                    }
                });
            }

            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            app_version,
            discover_servers,
            shell::app_state,
            shell::setup_step,
            shell::finish_setup,
            shell::open_login_items,
            shell::server_status,
            shell::open_local_server,
            shell::use_remote_server,
            shell::open_external,
        ])
        .run(tauri::generate_context!())
        .expect("error while running maktaba desktop");
}

/// The window's menu bar. `server_items`: add the tray's server items as a
/// Server menu (Linux without a tray; shell.rs keeps them in step).
fn build_menu(app: &tauri::AppHandle, server_items: bool) -> tauri::Result<Menu<tauri::Wry>> {
    // CROSS-LANGUAGE CONTRACT: every custom `MenuItem::with_id(...)` id
    // below (and the `format!("library-{slot}")` ids) is the cross-language
    // contract with the web shell. They MUST stay in sync with
    // `resolveMenuAction` / `KNOWN_MENU_IDS` in `web/src/lib/desktop.ts`
    // (the authoritative mirror) — a typo on either side silently makes the
    // menu item a dead no-op (`resolveMenuAction` returns `null`).
    //
    // Maktaba menu (macOS: appears as the app name; Win/Linux: leftmost)
    let about = PredefinedMenuItem::about(app, Some("About Maktaba"), None)?;
    let prefs = MenuItem::with_id(app, "preferences", "Settings…", true, Some("CmdOrCtrl+,"))?;
    let switch_server =
        MenuItem::with_id(app, "switch-server", "Switch Server…", true, None::<&str>)?;
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
    let scan = MenuItem::with_id(
        app,
        "scan-library",
        "Scan Library",
        true,
        Some("CmdOrCtrl+R"),
    )?;
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

    let menu = Menu::with_items(
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
    )?;
    if server_items {
        menu.insert(&server_menu(app)?, 1)?;
    }
    Ok(menu)
}

/// The Server menu: the status line (disabled), then the tray's server items,
/// all disabled until the first status refresh enables what applies.
fn server_menu(app: &tauri::AppHandle) -> tauri::Result<Submenu<tauri::Wry>> {
    // the tray's words (lang.rs)
    let lang = lang::Lang::system();
    let sub = Submenu::with_id(
        app,
        tray::WINDOW_MENU_ID,
        tray::window_menu_title(lang),
        true,
    )?;
    sub.append(&MenuItem::with_id(
        app,
        tray::WINDOW_STATUS_ID,
        lang.words().brand,
        false,
        None::<&str>,
    )?)?;
    sub.append(&PredefinedMenuItem::separator(app)?)?;
    for entry in tray::WINDOW_ITEMS {
        match entry {
            Some(id) => sub.append(&MenuItem::with_id(
                app,
                format!("{}{id}", tray::WINDOW_PREFIX),
                tray::window_label(lang, id).unwrap_or(id),
                false,
                None::<&str>,
            )?)?,
            None => sub.append(&PredefinedMenuItem::separator(app)?)?,
        }
    }
    Ok(sub)
}
