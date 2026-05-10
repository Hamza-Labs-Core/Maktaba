// Tauri 2 application entry point (library form so platform shims —
// notably mobile — can call run() without re-declaring main).
//
// Builds the native menu bar (Story 13.1 AC-1), wires the system-tray
// icon (Story 13.4), and registers the plugins for window-state
// persistence (Story 13.1 AC-2 — "open at last position") and
// auto-update (Story 13.8).
use tauri::{
    menu::{Menu, MenuItem, PredefinedMenuItem, Submenu},
    tray::TrayIconBuilder,
    Manager,
};

#[tauri::command]
fn app_version() -> String {
    env!("CARGO_PKG_VERSION").to_string()
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
            // Menu bar (Story 13.1 AC-1)
            let handle = app.handle();
            let menu = build_menu(handle)?;
            app.set_menu(menu)?;

            // System tray (Story 13.4 — minimum: show/hide window)
            let tray_show = MenuItem::with_id(handle, "show", "Show Maktaba", true, None::<&str>)?;
            let tray_hide = MenuItem::with_id(handle, "hide", "Hide", true, None::<&str>)?;
            let tray_quit = MenuItem::with_id(handle, "quit", "Quit", true, None::<&str>)?;
            let tray_menu = Menu::with_items(handle, &[&tray_show, &tray_hide, &tray_quit])?;
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
                        "quit" => {
                            app.exit(0);
                        }
                        _ => {}
                    }
                })
                .build(handle)?;

            Ok(())
        })
        .invoke_handler(tauri::generate_handler![app_version])
        .run(tauri::generate_context!())
        .expect("error while running maktaba desktop");
}

fn build_menu(app: &tauri::AppHandle) -> tauri::Result<Menu<tauri::Wry>> {
    // Maktaba menu (macOS: appears as the app name; Win/Linux: leftmost)
    let about = PredefinedMenuItem::about(app, Some("About Maktaba"), None)?;
    let prefs = MenuItem::with_id(app, "preferences", "Settings…", true, Some("CmdOrCtrl+,"))?;
    let quit = PredefinedMenuItem::quit(app, None)?;
    let app_menu = Submenu::with_items(app, "Maktaba", true, &[&about, &prefs, &quit])?;

    // File menu
    let new_window = MenuItem::with_id(app, "new-window", "New Window", true, Some("CmdOrCtrl+N"))?;
    let close = PredefinedMenuItem::close_window(app, None)?;
    let file_menu = Submenu::with_items(app, "File", true, &[&new_window, &close])?;

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

    // View menu
    let view_menu = Submenu::with_items(
        app,
        "View",
        true,
        &[&PredefinedMenuItem::fullscreen(app, None)?],
    )?;

    // Library menu
    let scan = MenuItem::with_id(app, "scan-library", "Scan Library", true, Some("CmdOrCtrl+R"))?;
    let lib_menu = Submenu::with_items(app, "Library", true, &[&scan])?;

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
